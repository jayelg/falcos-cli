// goojust: an OS TUI for system info and running just recipes (aliased to
// the os-release NAME, e.g. `falcos`). No args: system panel + recipe menu.
// With args: runs that recipe in the TUI's output pane. Non-TTY invocations
// pass through to plain `just`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// version is set at build time via ldflags: -X main.version=v0.1.3
var version = "dev"

const defaultJustfile = "/usr/share/goojust/justfile"

func justfilePath() string {
	if p := os.Getenv("GOOJUST_JUSTFILE"); p != "" {
		return p
	}
	return defaultJustfile
}

const helpText = `goojust — OS TUI for system info and running just recipes.

Usage:
  falcos                  Launch interactive TUI (system panel + recipe menu)
  falcos <recipe> [args]  Run a recipe in the embedded terminal pane
  falcos --version        Print version and exit
  falcos --help           Print this help and exit

Configuration:
  GOOJUST_JUSTFILE         Path to the system justfile (default /usr/share/goojust/justfile)
  GOOJUST_PLAIN            Set to bypass the TUI and exec just directly

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
	args := os.Args[1:]

	// Flags that work regardless of TTY.
	if len(args) == 1 {
		switch args[0] {
		case "--version", "-V":
			fmt.Println("goojust", version)
			os.Exit(0)
		case "--help", "-h":
			fmt.Print(helpText)
			os.Exit(0)
		}
	}

	// Scripts and pipes get plain just behaviour, no TUI.
	if !term.IsTerminal(int(os.Stdout.Fd())) || os.Getenv("GOOJUST_PLAIN") != "" {
		// Use a stripped copy so just 1.55+ (which rejects unknown
		// attributes) can parse the justfile. Temp file cleaned up
		// by the OS when the process is replaced via syscall.Exec.
		jf := strippedJustfilePath(justfilePath())
		justArgs := append([]string{"just", "--justfile", jf}, args...)
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

	// Create a stripped copy for recipe execution (runner.go).
	strippedJustfile = strippedJustfilePath(justfilePath())

	recipes, err := loadRecipes(justfilePath())
	if err != nil {
		// Launch TUI with system info only; show error where the menu
		// would be so the user can still see the panel.
		recipes = nil
	}

	m := newModel(recipes, args)
	if err != nil {
		m.loadError = fmt.Sprintf("loading recipes: %v", err)
	}
	// Validate CLI recipe name early so the user gets a clear error
	// instead of a silent exit 127 after the TUI starts.
	if len(args) > 0 {
		name := args[0]
		if _, ok := m.recipeByName(name); !ok {
			fmt.Fprintf(os.Stderr, "goojust: unknown recipe %q\n", name)
			os.Exit(127)
		}
	}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseAllMotion()}
	if len(args) > 0 {
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
