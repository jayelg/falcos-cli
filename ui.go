package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const paneHeight = 14

type uiState int

const (
	stateMenu   uiState = iota
	statePrompt         // collecting a recipe parameter
	stateRunning
	stateDone
)

var (
	accent     = lipgloss.AdaptiveColor{Light: "63", Dark: "12"}
	dimColor   = lipgloss.AdaptiveColor{Light: "245", Dark: "240"}
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelStyle = lipgloss.NewStyle().Foreground(accent).Width(12)
	groupStyle = lipgloss.NewStyle().Bold(true).Foreground(dimColor).MarginTop(1)
	selStyle   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	docStyle   = lipgloss.NewStyle().Foreground(dimColor)
	paneStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dimColor).Padding(0, 1)
	helpStyle  = lipgloss.NewStyle().Foreground(dimColor)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "10"})
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "9"})
)

type infoMsg []infoField

type model struct {
	state    uiState
	fields   []infoField
	recipes  []recipe
	cursor   int
	width    int
	height   int
	run      *runner
	running  string // recipe name being/last run
	exitCode int
	prog     progress.Model
	progPct  int
	progOn   bool
	input    textinput.Model
	pending  recipe   // recipe awaiting parameters
	pArgs    []string // collected parameter values
	autorun  []string // CLI args: recipe to start immediately
}

func newModel(recipes []recipe, autorun []string) model {
	ti := textinput.New()
	ti.CharLimit = 128
	return model{
		recipes: recipes,
		prog:    progress.New(progress.WithDefaultGradient()),
		input:   ti,
		autorun: autorun,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.SetWindowTitle(osName()),
		func() tea.Msg { return infoMsg(gatherInfo()) },
	}
	return tea.Batch(cmds...)
}

func (m *model) paneSize() (w, h int) {
	w = m.width - 4
	if w < 20 {
		w = 78
	}
	return w, paneHeight
}

func (m *model) startRecipe(name string, args []string) tea.Cmd {
	w, h := m.paneSize()
	r, err := startRecipe(name, args, w, h, program)
	if err != nil {
		m.state = stateDone
		m.exitCode = 1
		return nil
	}
	m.run = r
	m.running = name
	m.state = stateRunning
	m.progOn = false
	return nil
}

// selectRecipe starts the recipe, or prompts for its first parameter.
func (m *model) selectRecipe(r recipe, preArgs []string) tea.Cmd {
	if len(preArgs) >= len(r.Params) {
		return m.startRecipe(r.Name, preArgs)
	}
	m.pending = r
	m.pArgs = preArgs
	m.input.Placeholder = r.Params[len(preArgs)]
	m.input.SetValue("")
	m.input.Focus()
	m.state = statePrompt
	return textinput.Blink
}

func (m *model) recipeByName(name string) (recipe, bool) {
	for _, r := range m.recipes {
		if r.Name == name {
			return r, true
		}
	}
	return recipe{}, false
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.prog.Width = msg.Width - 8
		// First size report: start any recipe given on the command line.
		if len(m.autorun) > 0 {
			args := m.autorun
			m.autorun = nil
			if r, ok := m.recipeByName(args[0]); ok {
				return m, m.selectRecipe(r, args[1:])
			}
			m.running = args[0]
			m.state = stateDone
			m.exitCode = 127
		}
		return m, nil

	case infoMsg:
		m.fields = msg
		return m, nil

	case ptyDataMsg:
		if m.run != nil && !m.run.handover.Load() {
			m.run.emu.Write(msg) //nolint:errcheck
		}
		return m, nil

	case progressMsg:
		m.progOn = msg.active
		m.progPct = msg.pct
		return m, nil

	case recipeExitMsg:
		m.state = stateDone
		m.exitCode = msg.code
		m.progOn = false
		return m, func() tea.Msg { return infoMsg(gatherInfo()) } // refresh panel

	case handoverDoneMsg:
		return m, nil

	case tea.KeyMsg:
		switch m.state {
		case stateRunning:
			switch msg.Type {
			case tea.KeyCtrlQ:
				m.run.kill()
			case tea.KeyCtrlT:
				go m.run.enterHandover(program)
			default:
				if b := keyBytes(msg); b != nil && m.run != nil {
					m.run.ptmx.Write(b) //nolint:errcheck
				}
			}
			return m, nil

		case statePrompt:
			switch msg.Type {
			case tea.KeyEsc:
				m.state = stateMenu
				return m, nil
			case tea.KeyEnter:
				m.pArgs = append(m.pArgs, m.input.Value())
				return m, m.selectRecipe(m.pending, m.pArgs)
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd

		default: // stateMenu, stateDone
			switch msg.String() {
			case "q", "ctrl+c", "esc":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				// len(recipes) is the built-in exit row
				if m.cursor < len(m.recipes) {
					m.cursor++
				}
			case "enter":
				if m.cursor == len(m.recipes) {
					return m, tea.Quit
				}
				if len(m.recipes) > 0 {
					return m, m.selectRecipe(m.recipes[m.cursor], nil)
				}
			}
			return m, nil
		}
	}
	return m, nil
}

func (m model) viewPanel() string {
	var b strings.Builder
	for _, f := range m.fields {
		b.WriteString(labelStyle.Render(f.Label) + " " + f.Value + "\n")
	}
	if len(m.fields) == 0 {
		b.WriteString(docStyle.Render("gathering system info...") + "\n")
	}
	return b.String()
}

func (m model) viewMenu() string {
	var b strings.Builder
	group := ""
	for i, r := range m.recipes {
		if r.Group != group {
			group = r.Group
			b.WriteString(groupStyle.Render(group) + "\n")
		}
		// Fixed-width name column so the doc column doesn't shift when a
		// row is highlighted (prefix and padding are identical either way).
		name := fmt.Sprintf("%-28s", r.Name)
		if i == m.cursor {
			b.WriteString(selStyle.Render("> "+name) + " " + docStyle.Render(r.Doc) + "\n")
		} else {
			b.WriteString("  " + name + " " + docStyle.Render(r.Doc) + "\n")
		}
	}
	// Built-in exit row, last item of the bottom section
	name := fmt.Sprintf("%-28s", "exit")
	if m.cursor == len(m.recipes) {
		b.WriteString(selStyle.Render("> "+name) + " " + docStyle.Render("Close this menu") + "\n")
	} else {
		b.WriteString("  " + name + " " + docStyle.Render("Close this menu") + "\n")
	}
	return b.String()
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("● "+osName()) + "\n\n")
	b.WriteString(m.viewPanel())

	switch m.state {
	case statePrompt:
		b.WriteString("\n" + m.pending.Name + " needs " +
			selStyle.Render(m.pending.Params[len(m.pArgs)]) + ":\n")
		b.WriteString(m.input.View() + "\n")
		b.WriteString(helpStyle.Render("enter confirm · esc cancel"))

	case stateRunning, stateDone:
		status := selStyle.Render(m.running)
		if m.state == stateDone {
			if m.exitCode == 0 {
				status += " " + okStyle.Render("✓ "+exitLabel(m.exitCode))
			} else {
				status += " " + errStyle.Render("✗ "+exitLabel(m.exitCode))
			}
		}
		b.WriteString("\n" + status + "\n")
		if m.run != nil {
			// Full terminal width: the emulator trims trailing spaces, so
			// without an explicit width the border hugs the widest line.
			ps := paneStyle
			if m.width > 24 {
				ps = ps.Width(m.width - 2)
			}
			b.WriteString(ps.Render(m.run.emu.Render()) + "\n")
		}
		if m.progOn {
			b.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
		}
		if m.state == stateRunning {
			b.WriteString(helpStyle.Render("keys go to the recipe · ctrl+q kill · ctrl+t full terminal"))
		} else {
			b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
			b.WriteString("\n" + m.viewMenu())
		}

	default:
		b.WriteString("\n" + m.viewMenu() + "\n")
		b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
	}
	return b.String()
}
