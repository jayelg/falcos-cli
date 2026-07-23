// goojust: an OS TUI for system info and running just recipes (aliased to
// the os-release NAME). No args: system panel + recipe menu. With args: runs
// that recipe in the TUI's output pane. Non-TTY invocations pass through to
// plain `just`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// version is set at build time via ldflags: -X main.version=v0.1.3
var version = "dev"

const defaultJustfile = "/usr/share/goojust/justfile"

const helpText = `goojust — OS TUI for system info and running just recipes.

Usage:
  goojust [flags]                  Launch interactive TUI
  goojust [flags] <recipe> [args]  Run a recipe in the embedded terminal pane
  goojust --version                Print version and exit
  goojust --help                   Print this help and exit

Flags:
  --justfile <path>  Path to the system justfile (default /usr/share/goojust/justfile)
  --plain            Bypass the TUI and exec just directly

Keybindings (TUI):
  ↑/↓ or j/k    Navigate recipe menu
  enter          Run selected recipe
  /              Show filter bar (type to search recipes)
  r              Refresh system info
  s              Save recipe output to log
  ctrl+q         Kill running recipe
  ctrl+t         Hand terminal to full-screen child (vim, htop, ...)
  q or esc       Quit
`

func main() {
	justfile := defaultJustfile
	plain := false
	args := os.Args[1:]

	// Parse flags before the recipe name. Remaining args after flags are
	// the recipe name and its arguments.
	var remaining []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--version" || a == "-V":
			fmt.Println("goojust", version)
			os.Exit(0)
		case a == "--help" || a == "-h":
			fmt.Print(helpText)
			os.Exit(0)
		case a == "--plain":
			plain = true
		case a == "--justfile":
			i++
			if i < len(args) {
				justfile = args[i]
			}
		case strings.HasPrefix(a, "--justfile="):
			justfile = strings.TrimPrefix(a, "--justfile=")
		default:
			// First non-flag argument starts the recipe + its args.
			remaining = append(remaining, args[i:]...)
			i = len(args) // break
		}
	}

	// Scripts and pipes get plain just behaviour, no TUI.
	if !term.IsTerminal(int(os.Stdout.Fd())) || plain {
		justArgs := append([]string{"just", "--justfile", justfile}, remaining...)
		bin, err := exec.LookPath("just")
		if err != nil {
			fmt.Fprintln(os.Stderr, "just not found")
			os.Exit(1)
		}
		if err := syscall.Exec(bin, justArgs, os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	recipes, err := loadRecipes(justfile)
	if err != nil {
		// Launch TUI with system info only; show error where the menu
		// would be so the user can still see the panel.
		recipes = nil
	}

	m := newModel(recipes, remaining, justfile)
	if err != nil {
		m.loadError = fmt.Sprintf("loading recipes: %v", err)
	}
	// Validate CLI recipe name early so the user gets a clear error
	// instead of a silent exit 127 after the TUI starts.
	if len(remaining) > 0 {
		name := remaining[0]
		if _, ok := m.recipeByName(name); !ok {
			fmt.Fprintf(os.Stderr, "goojust: unknown recipe %q\n", name)
			os.Exit(127)
		}
	}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseAllMotion()}
	if len(remaining) > 0 {
		// CLI mode: no alt-screen so output stays inline in the terminal
		// scrollback. Program exits when the recipe finishes.
		opts = nil
	}
	p := tea.NewProgram(m, opts...)
	program = p // for terminal handover from within Update
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
