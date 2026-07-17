package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// program is set from main so Update can hand the real terminal over to a
// full-screen child (tea.Program.ReleaseTerminal/RestoreTerminal).
var program *tea.Program

type ptyDataMsg []byte
type recipeExitMsg struct{ code int }
type handoverDoneMsg struct{}
type progressMsg struct {
	pct    int  // 0-100
	active bool // false clears the bar
}

type runner struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	emu      *vt.Emulator
	handover atomic.Bool // raw passthrough active, reader bypasses the emulator
}

// startRecipe launches `just <recipe> [args...]` on a PTY sized to the
// output pane, feeding output into a vt emulator for embedded rendering.
func startRecipe(name string, args []string, w, h int, prog *tea.Program) (*runner, error) {
	r := &runner{emu: vt.NewEmulator(w, h)}

	// OSC 9;4 terminal progress (Windows Terminal / ConEmu / systemd
	// convention): ESC ] 9 ; 4 ; <state> ; <pct> BEL. Recipes emit it via
	// the falcos-progress helper; state 0 clears, 1 sets percent.
	r.emu.RegisterOscHandler(9, func(data []byte) bool {
		parts := strings.Split(string(data), ";")
		if len(parts) < 3 || parts[0] != "9" || parts[1] != "4" {
			return false
		}
		state := parts[2]
		pct := 0
		if len(parts) > 3 {
			pct, _ = strconv.Atoi(parts[3])
		}
		// Async: this handler runs inside emu.Write, which runs inside the
		// model's Update. A synchronous Send would block on the message
		// loop that is currently in Update -> deadlock.
		go prog.Send(progressMsg{pct: pct, active: state == "1" || state == "3"})
		return true
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

	cmdArgs := append([]string{"--justfile", justfilePath(), name}, args...)
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
	}
	// Repaint whatever the emulator last held, then stream live.
	os.Stdout.WriteString("\x1b[2J\x1b[H" + r.emu.Render() + "\n")

	stdinDone := make(chan struct{})
	go func() {
		io.Copy(r.ptmx, os.Stdin) //nolint:errcheck
		close(stdinDone)
	}()

	r.cmd.Process.Wait() //nolint:errcheck
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

func exitLabel(code int) string {
	if code == 0 {
		return "ok"
	}
	return fmt.Sprintf("exit %d", code)
}
