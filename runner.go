package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// program is set from main so Update can hand the real terminal over to a
// full-screen child (tea.Program.ReleaseTerminal/RestoreTerminal).
var program *tea.Program

// strippedJustfile is set from main to the path of a justfile copy with
// custom attributes removed, so just 1.55+ can parse it.
var strippedJustfile string

type ptyDataMsg []byte
type recipeExitMsg struct{ code int }
type handoverDoneMsg struct{}
type progressMsg struct {
	pct    int    // 0-100
	active bool   // false clears the bar
	label  string // optional phase label (e.g. "Downloading...")
}
type promptRequiredMsg struct {
	text   string
	secret bool
}
type optionRequiredMsg struct {
	prompt  string   // text shown above the options
	options []string // selectable choices
}
type confirmRequiredMsg struct {
	prompt  string   // confirmation text
	options []string // two button labels: [opt1, opt2]
	clear   bool     // true = clear emulator output before showing
}
type summaryMsg struct {
	text string // one line of summary output from the recipe
}
type summaryShowMsg struct{}

type runner struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	emu      *vt.Emulator
	handover atomic.Bool  // raw passthrough active, reader bypasses the emulator
	output   strings.Builder // raw PTY output accumulated for save-to-log
}

// startRecipe launches `just [flags...] <recipe> [args...]` on a PTY sized
// to the output pane, feeding output into a vt emulator for embedded
// rendering. extraFlags are just flags (e.g. --yes) inserted before the
// recipe name.
func startRecipe(name string, args []string, w, h int, prog *tea.Program, extraFlags ...string) (*runner, error) {
	r := &runner{emu: vt.NewEmulator(w, h)}

	// OSC 9 dispatcher: sub-identifier 4 = progress, 5 = prompt, 6 = option select.
	// Async Send: handler runs inside emu.Write which runs inside Update;
	// synchronous Send would deadlock on the message loop.
	r.emu.RegisterOscHandler(9, func(data []byte) bool {
		parts := strings.Split(string(data), ";")
		if len(parts) < 3 || parts[0] != "9" {
			return false
		}
		switch parts[1] {
		case "4": // progress: ESC ] 9 ; 4 ; <state> ; <pct> [ ; <label> ] ST
			state := parts[2]
			pct := 0
			if len(parts) > 3 {
				pct, _ = strconv.Atoi(parts[3])
			}
			label := ""
			if len(parts) > 4 {
				label = parts[4]
			}
			go prog.Send(progressMsg{pct: pct, active: state == "1" || state == "3", label: label})
			return true
		case "5": // prompt: ESC ] 9 ; 5 ; <text> ; <secret> ST
			text := parts[2]
			secret := len(parts) > 3 && parts[3] == "true"
			go prog.Send(promptRequiredMsg{text: text, secret: secret})
			return true
		case "6": // option select: ESC ] 9 ; 6 ; <prompt> ; <opt1|opt2|...> ST
			prompt := parts[2]
			opts := []string{}
			if len(parts) > 3 {
				opts = strings.Split(parts[3], "|")
			}
			go prog.Send(optionRequiredMsg{prompt: prompt, options: opts})
			return true
		case "7": // confirm: ESC ] 9 ; 7 ; <prompt> [ ; <opt1|opt2> [ ; <clear> ]] ST
			prompt := parts[2]
			opts := []string{"Proceed", "Cancel"}
			if len(parts) > 3 {
				opts = strings.Split(parts[3], "|")
			}
			clear := len(parts) > 4 && parts[4] == "1"
			go prog.Send(confirmRequiredMsg{prompt: prompt, options: opts, clear: clear})
			return true
		case "8": // clear CLI output from overlay: ESC ] 9 ; 8 ST
			// Deprecated — clear is now part of OSC 9;7's 5th field.
			// Kept for backward compatibility.
			return true
		case "10": // summary: ESC ] 9 ; 10 ; <text> ST
			go prog.Send(summaryMsg{text: parts[2]})
			return true
		case "11": // summary show: ESC ] 9 ; 11 ST
			go prog.Send(summaryShowMsg{})
			return true
		}
		return false
	})
	r.emu.SetCallbacks(vt.Callbacks{
		AltScreen: func(on bool) {
			// A child went full-screen (vim, htop...): hand it the real
			// terminal, the embedded pane can't host it usefully.
			if on && !r.handover.Load() {
				go r.enterHandover(prog)
			}
		},
	})

	cmdArgs := append(extraFlags, "--justfile", strippedJustfile, name)
	cmdArgs = append(cmdArgs, args...)
	r.cmd = exec.Command("just", cmdArgs...)
	r.cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(r.cmd, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	if err != nil {
		return nil, err
	}
	r.ptmx = ptmx

	// PTY output: to the model for emulator rendering, or straight to the
	// real terminal during handover.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				r.output.Write(buf[:n])
				if r.handover.Load() {
					os.Stdout.Write(buf[:n])
				} else {
					data := make([]byte, n)
					copy(data, buf[:n])
					prog.Send(ptyDataMsg(data))
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// Emulator responses to terminal queries (cursor position, DA...) go
	// back to the child.
	go io.Copy(ptmx, r.emu) //nolint:errcheck

	go func() {
		err := r.cmd.Wait()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			code = 1
		}
		prog.Send(recipeExitMsg{code: code})
	}()

	return r, nil
}

// enterHandover gives the child the real terminal until it exits.
func (r *runner) enterHandover(prog *tea.Program) {
	if err := prog.ReleaseTerminal(); err != nil {
		return
	}
	r.handover.Store(true)

	fd := int(os.Stdin.Fd())
	oldState, rawErr := term.MakeRaw(fd)
	if tw, th, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		pty.Setsize(r.ptmx, &pty.Winsize{Rows: uint16(th), Cols: uint16(tw)}) //nolint:errcheck
		r.emu.Resize(tw, th)
	}
	// Clear screen; live PTY output streams from home.
	os.Stdout.WriteString("\x1b[2J\x1b[H")

	stdinDone := make(chan struct{})
	go func() {
		io.Copy(r.ptmx, os.Stdin) //nolint:errcheck
		close(stdinDone)
	}()

	r.cmd.Process.Wait() //nolint:errcheck
	r.ptmx.Close()       //nolint:errcheck
	<-stdinDone          // wait for the copy goroutine to finish

	if rawErr == nil {
		term.Restore(fd, oldState) //nolint:errcheck
	}
	prog.RestoreTerminal() //nolint:errcheck
	prog.Send(handoverDoneMsg{})
}

// resizePTY resizes the PTY and emulator to the given dimensions.
func (r *runner) resizePTY(w, h int) {
	pty.Setsize(r.ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)}) //nolint:errcheck
	r.emu.Resize(w, h)
}

func (r *runner) kill() {
	if r.cmd != nil && r.cmd.Process != nil {
		r.cmd.Process.Kill() //nolint:errcheck
	}
}

// keyBytes translates a bubbletea key press into the byte sequence a
// terminal would send, for forwarding to the recipe's PTY.
func keyBytes(k tea.KeyMsg) []byte {
	switch k.Type {
	case tea.KeyRunes:
		return []byte(string(k.Runes))
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	case tea.KeyCtrlU:
		return []byte{0x15}
	case tea.KeyCtrlW:
		return []byte{0x17}
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyCtrlE:
		return []byte{0x05}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlR:
		return []byte{0x12}
	}
	return nil
}

// saveOutput writes the accumulated PTY output to a timestamped log file
// under ~/.local/share/goojust/logs/ and returns the path.
func (r *runner) saveOutput(name string) (string, error) {
	dir := filepath.Join(os.Getenv("HOME"), ".local", "share", "goojust", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-%s.log", name, ts)
	path := filepath.Join(dir, filename)
	return path, os.WriteFile(path, []byte(r.output.String()), 0644)
}

func exitLabel(code int) string {
	if code == 0 {
		return "done"
	}
	if code < 0 {
		return "cancelled"
	}
	return fmt.Sprintf("exit %d", code)
}
