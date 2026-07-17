package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const paneHeight = 14

type uiState int

const (
	stateMenu    uiState = iota
	statePrompt          // collecting a recipe parameter (freeform text)
	stateSelect          // collecting a recipe parameter (choose from options)
	stateConfirm         // showing a proceed/cancel confirmation popup
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

	confirmIdx int // 0 = proceed (default), 1 = cancel

	spin    spinner.Model // indeterminate spinner for progress startup
	spinOn  bool          // spinner currently rendering
	seenOut bool          // first output or progress received, hide spinner

	selOpts  []string // selectable options for the active parameter
	selCur   int      // cursor into selOpts
}

func newModel(recipes []recipe, autorun []string) model {
	ti := textinput.New()
	ti.CharLimit = 128
	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(accent)
	s.Spinner = spinner.Dot
	return model{
		recipes: recipes,
		prog:    progress.New(progress.WithDefaultGradient()),
		input:   ti,
		spin:    s,
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
	m.seenOut = false
	// Start the indeterminate spinner when a [progress] recipe launches
	// so the user sees activity before the first OSC 9;4 update arrives.
	if r.Progress {
		m.spinOn = true
		return m.spin.Tick
	}
	m.spinOn = false
	return nil
}

// selectRecipe starts the recipe, prompts for parameters, or shows a
// confirmation popup, depending on the recipe's declaration.
func (m *model) selectRecipe(r recipe, preArgs []string) tea.Cmd {
	m.pending = r
	m.pArgs = preArgs

	// If there are still parameters to collect, prompt for the next one.
	if len(preArgs) < len(r.Params) {
		nextParam := r.Params[len(preArgs)]
		// If this parameter has selectable options, show the selection list.
		if opts, ok := r.Select[nextParam]; ok && len(opts) > 0 {
			m.selOpts = opts
			m.selCur = 0
			m.state = stateSelect
			return nil
		}
		m.input.Placeholder = nextParam
		m.input.SetValue("")
		m.input.Focus()
		m.state = statePrompt
		return textinput.Blink
	}

	// All parameters collected: confirm if the recipe asks for it.
	if r.Confirm != "" {
		m.confirmIdx = 0 // default to "proceed"
		m.state = stateConfirm
		return nil
	}

	return m.startRecipe(r.Name, preArgs)
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
		m.seenOut = true
		m.spinOn = false
		return m, nil

	case progressMsg:
		m.progOn = msg.active
		m.progPct = msg.pct
		m.seenOut = true
		m.spinOn = false
		return m, nil

	case recipeExitMsg:
		m.state = stateDone
		m.exitCode = msg.code
		m.progOn = false
		m.spinOn = false
		return m, func() tea.Msg { return infoMsg(gatherInfo()) } // refresh panel

	case handoverDoneMsg:
		return m, nil

	case tea.KeyMsg:
		switch m.state {

		case stateConfirm:
			switch msg.String() {
			case "enter":
				// Proceed, start the recipe now.
				return m, m.startRecipe(m.pending.Name, m.pArgs)
			case "esc", "q":
				// Cancel, back to the menu.
				m.state = stateMenu
				return m, nil
			case "left", "h":
				if m.confirmIdx > 0 {
					m.confirmIdx--
				}
			case "right", "l":
				if m.confirmIdx < 1 {
					m.confirmIdx++
				}
			}
			return m, nil

		case stateRunning:
			if m.spinOn {
				var cmd tea.Cmd
				m.spin, cmd = m.spin.Update(msg)
				return m, cmd
			}
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

		case stateSelect:
			switch msg.String() {
			case "up", "k":
				if m.selCur > 0 {
					m.selCur--
				}
			case "down", "j":
				if m.selCur < len(m.selOpts)-1 {
					m.selCur++
				}
			case "enter":
				m.pArgs = append(m.pArgs, m.selOpts[m.selCur])
				return m, m.selectRecipe(m.pending, m.pArgs)
			case "esc":
				m.state = stateMenu
				return m, nil
			}
			return m, nil

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
	case stateSelect:
		paramName := m.pending.Params[len(m.pArgs)]
		selStr := "\n" + selStyle.Render(m.pending.Name) +
			docStyle.Render(" > ") + labelStyle.Render(paramName) + "\n\n"
		for i, opt := range m.selOpts {
			if i == m.selCur {
				selStr += selStyle.Render("▸ "+opt) + "\n"
			} else {
				selStr += "  " + docStyle.Render(opt) + "\n"
			}
		}
		b.WriteString(selStr)
		b.WriteString(helpStyle.Render("↑/↓ navigate · enter select · esc cancel"))

	case stateConfirm:
		confirmStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2).
			Align(lipgloss.Center).
			Width(m.width - 6)
		if m.width < 20 {
			confirmStyle = confirmStyle.Width(40)
		}

		// Build the confirmation prompt text, expanding {{param}} placeholders.
		prompt := m.pending.Confirm
		for i, p := range m.pending.Params {
			if i < len(m.pArgs) {
				prompt = strings.ReplaceAll(prompt, "{{"+p+"}}", m.pArgs[i])
			}
		}

		// Render the two options: Proceed / Cancel
		proceed := " Proceed "
		cancel := " Cancel "
		if m.confirmIdx == 0 {
			proceed = selStyle.Render("▸ Proceed ") + " "
		} else {
			proceed = "  Proceed  "
		}
		if m.confirmIdx == 1 {
			cancel = selStyle.Render("▸ Cancel ") + " "
		} else {
			cancel = "  Cancel  "
		}

		body := fmt.Sprintf("%s\n\n%s%s\n\n%s",
			selStyle.Render(m.pending.Name),
			prompt,
			"\n\n"+proceed+cancel,
			helpStyle.Render("← → navigate · enter confirm · esc cancel"),
		)
		b.WriteString("\n" + confirmStyle.Render(body) + "\n")

	case statePrompt:
		// Show all recipe parameters as a form: collected values are marked ✓,
		// the active field shows the input widget, and future fields are dimmed.
		formStr := "\n" + selStyle.Render(m.pending.Name) + "\n\n"
		for i, p := range m.pending.Params {
			paramLabel := labelStyle.Render(p)
			if i < len(m.pArgs) {
				// Already collected, show the value with a checkmark.
				formStr += fmt.Sprintf("  %s %s  %s\n",
					okStyle.Render("✓"),
					paramLabel,
					docStyle.Render(m.pArgs[i]),
				)
			} else if i == len(m.pArgs) {
				// Active field, show the input.
				formStr += fmt.Sprintf("  %s %s %s\n",
					selStyle.Render("⌨"),
					paramLabel,
					m.input.View(),
				)
			} else {
				// Future field, dimmed placeholder.
				formStr += fmt.Sprintf("  %s %s  %s\n",
					docStyle.Render("·"),
					paramLabel,
					docStyle.Render("…"),
				)
			}
		}
		b.WriteString(formStr)
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
		if m.spinOn {
			b.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
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
