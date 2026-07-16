// os-cli: branded system TUI (aliased to the os-release NAME, e.g. `falcos`).
// No args: system panel + recipe menu. With args: runs that recipe in the
// TUI's output pane. Non-TTY invocations pass through to plain `just`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

const defaultJustfile = "/usr/share/falcos/justfile"

func justfilePath() string {
	if p := os.Getenv("FALCOS_JUSTFILE"); p != "" {
		return p
	}
	return defaultJustfile
}

func main() {
	args := os.Args[1:]

	// Scripts and pipes get plain just behaviour, no TUI.
	if !term.IsTerminal(int(os.Stdout.Fd())) || os.Getenv("FALCOS_PLAIN") != "" {
		justArgs := append([]string{"just", "--justfile", justfilePath()}, args...)
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

	recipes, err := loadRecipes(justfilePath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading recipes from %s: %v\n", justfilePath(), err)
		os.Exit(1)
	}

	m := newModel(recipes, args)
	p := tea.NewProgram(m, tea.WithAltScreen())
	program = p // for terminal handover from within Update
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
