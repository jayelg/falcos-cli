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
	groupStyle = lipgloss.NewStyle().Bold(true).Foreground(dimColor)
	selStyle   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	docStyle   = lipgloss.NewStyle().Foreground(dimColor)
	helpStyle  = lipgloss.NewStyle().Foreground(dimColor)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "10"})
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "9"})
)

type infoMsg []infoField

// publicIPCmd fetches the public IP asynchronously so it does not
// block the fastfetch panel load. Always returns a message so the
// "Loading..." placeholder gets replaced.
func publicIPCmd() tea.Cmd {
	return func() tea.Msg {
		ip := publicIP()
		if ip == "" {
			ip = "n/a"
		}
		return infoField{"Public IP", ip}
	}
}

// ── menu items ────────────────────────────────────────────────

type itemKind int

const (
	kindHeader itemKind = iota
	kindRecipe
	kindExit
)

type menuItem struct {
	kind   itemKind
	recipe recipe   // populated for kindRecipe
	group  string   // populated for kindHeader
}

// ── model ─────────────────────────────────────────────────────

type model struct {
	state    uiState
	fields   []infoField
	recipes  []recipe
	run      *runner
	running  string // recipe name being/last run
	exitCode int
	width    int
	height   int
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

	selOpts []string // selectable options for the active parameter
	selCur  int      // cursor into selOpts

	menuItems []menuItem // flat list: headers + recipes + exit
	cursor    int        // selected index into menuItems
	menuOfs   int        // first visible menu line index (smooth scroll)
}

func newModel(recipes []recipe, autorun []string) model {
	ti := textinput.New()
	ti.CharLimit = 128
	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(accent)
	s.Spinner = spinner.Dot

	// Build flat menu items: group headers, recipes, exit.
	var items []menuItem
	group := ""
	for _, r := range recipes {
		if r.Group != group {
			group = r.Group
			items = append(items, menuItem{kind: kindHeader, group: group})
		}
		items = append(items, menuItem{kind: kindRecipe, recipe: r})
	}
	items = append(items, menuItem{kind: kindExit})

	return model{
		recipes:   recipes,
		prog:      progress.New(progress.WithDefaultGradient()),
		input:     ti,
		spin:      s,
		autorun:   autorun,
		menuItems: items,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.SetWindowTitle(osName()),
		func() tea.Msg { return infoMsg(gatherInfo()) },
	}
	return tea.Batch(cmds...)
}

// sysInfoHeight returns how many sysinfo lines fit given the terminal height.
// Reduces one line at a time as space shrinks, down to 3 (OS, Kernel, Uptime).
// When fields are empty (not yet loaded), returns the full expected count
// so the UI layout is stable from the first render.
func (m model) sysInfoHeight() int {
	n := len(m.fields)
	if n == 0 {
		n = 12
	}
	// Reserve space for: title(1) + help(1) + gap(1) + at least 8 menu
	// lines so the recipe list has room before sysinfo starts truncating.
	reserved := 1 + 1 + 1 + 8
	maxFit := m.height - reserved
	if maxFit < 3 {
		maxFit = 3
	}
	if maxFit > n {
		return n
	}
	return maxFit
}

// menuHeight returns how many menu lines fit below the sysinfo panel
// and the gap line.
func (m model) menuHeight() int {
	sys := m.sysInfoHeight()
	h := m.height - 1 - sys - 1 - 1 // title, gap, help
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) paneSize() (w, h int) {
	w = m.width - 4
	if w < 20 {
		w = 78
	}
	sysLines := m.sysInfoHeight()
	available := m.height - 2 - sysLines - 2
	h = available
	if h < 5 {
		h = 5
	}
	if h > 20 {
		h = 20
	}
	return w, h
}

func (m *model) startRecipe(name string, args []string) tea.Cmd {
	w, h := m.paneSize()
	rn, err := startRecipe(name, args, w, h, program)
	if err != nil {
		m.state = stateDone
		m.exitCode = 1
		return nil
	}
	m.run = rn
	m.running = name
	m.state = stateRunning
	m.progOn = false
	m.seenOut = false
	if m.pending.Progress {
		m.spinOn = true
		return m.spin.Tick
	}
	m.spinOn = false
	return nil
}

// isSilent returns true when the running recipe suppresses the CLI overlay.
func (m *model) isSilent() bool {
	return m.pending.Silent && !m.pending.Progress
}

// selectRecipe starts the recipe, prompts for parameters, or shows a
// confirmation popup, depending on the recipe's declaration.
func (m *model) selectRecipe(r recipe, preArgs []string) tea.Cmd {
	m.pending = r
	m.pArgs = preArgs

	if len(preArgs) < len(r.Params) {
		nextParam := r.Params[len(preArgs)]
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

	if r.Confirm != "" {
		m.confirmIdx = 0
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

		if m.state == stateRunning && m.run != nil {
			w, h := m.paneSize()
			m.run.resizePTY(w, h)
		}

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
		// Fire the async public IP fetch, and show a placeholder
		// that will be replaced when the response arrives.
		return m, tea.Batch(publicIPCmd(), func() tea.Msg {
			return infoField{"Public IP", "Loading..."}
		})

	case infoField:
		// Append or replace the Public IP field (last field).
		n := len(m.fields)
		if n > 0 && m.fields[n-1].Label == "Public IP" {
			m.fields[n-1] = msg
		} else {
			m.fields = append(m.fields, msg)
		}
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
		return m, func() tea.Msg { return infoMsg(gatherInfo()) }

	case handoverDoneMsg:
		return m, nil

	case tea.KeyMsg:
		switch m.state {

		case stateConfirm:
			switch msg.String() {
			case "enter":
				return m, m.startRecipe(m.pending.Name, m.pArgs)
			case "esc", "q":
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
			n := len(m.menuItems)
			visible := m.menuHeight()

			switch msg.String() {
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
				// Smooth scroll: shift offset by 1 when cursor passes the
				// top edge of the visible window.
				if m.cursor < m.menuOfs {
					m.menuOfs--
				}

			case "down", "j":
				if m.cursor < n-1 {
					m.cursor++
				}
				// Smooth scroll: shift offset by 1 when cursor passes the
				// bottom edge of the visible window.
				if m.cursor >= m.menuOfs+visible {
					m.menuOfs++
				}

			case "enter":
				mi := m.menuItems[m.cursor]
				switch mi.kind {
				case kindRecipe:
					return m, m.selectRecipe(mi.recipe, nil)
				case kindExit:
					return m, tea.Quit
				}

			case "q", "ctrl+c", "esc":
				return m, tea.Quit
			}
			return m, nil
		}
	}
	return m, nil
}

// ── rendering ─────────────────────────────────────────────────

func (m model) viewPanel(maxLines int) string {
	var b strings.Builder
	if len(m.fields) == 0 {
		for i := 0; i < maxLines; i++ {
			b.WriteString(docStyle.Render("...") + "\n")
		}
		return b.String()
	}
	end := maxLines
	if end > len(m.fields) {
		end = len(m.fields)
	}
	for _, f := range m.fields[:end] {
		b.WriteString(labelStyle.Render(f.Label) + " " + f.Value + "\n")
	}
	return b.String()
}

func (m model) viewMenu() string {
	visible := m.menuHeight()
	var b strings.Builder
	for i := m.menuOfs; i < m.menuOfs+visible && i < len(m.menuItems); i++ {
		mi := m.menuItems[i]
		sel := i == m.cursor
		switch mi.kind {
		case kindHeader:
			b.WriteString(groupStyle.Render(mi.group) + "\n")
		case kindRecipe:
			name := fmt.Sprintf("%-28s", mi.recipe.Name)
			if sel {
				b.WriteString(selStyle.Render("> "+name) + " " + docStyle.Render(mi.recipe.Doc) + "\n")
			} else {
				b.WriteString("  " + name + " " + docStyle.Render(mi.recipe.Doc) + "\n")
			}
		case kindExit:
			name := fmt.Sprintf("%-28s", "exit")
			if sel {
				b.WriteString(selStyle.Render("> "+name) + " " + docStyle.Render("Close this menu") + "\n")
			} else {
				b.WriteString("  " + name + " " + docStyle.Render("Close this menu") + "\n")
			}
		}
	}
	return b.String()
}

// viewOverlay renders the CLI emulator as a bordered overlay box.
func (m model) viewOverlay() string {
	var content strings.Builder

	status := selStyle.Render(m.running)
	if m.state == stateDone {
		if m.exitCode == 0 {
			status += " " + okStyle.Render("✓ "+exitLabel(m.exitCode))
		} else {
			status += " " + errStyle.Render("✗ "+exitLabel(m.exitCode))
		}
	}
	content.WriteString(status + "\n")

	if m.run != nil {
		content.WriteString("\n" + m.run.emu.Render() + "\n")
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	if m.progOn {
		content.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
	}

	if m.state == stateRunning {
		content.WriteString(helpStyle.Render("keys go to the recipe · ctrl+q kill · ctrl+t full terminal"))
	} else {
		content.WriteString(helpStyle.Render("recipe finished · ↑/↓ navigate · enter run · q quit"))
	}

	ow := m.width - 4
	if ow < 20 {
		ow = 40
	}
	overlayStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Padding(0, 1).
		Width(ow)
	return "\n" + overlayStyle.Render(content.String())
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("● "+osName()) + "\n")

	sysLines := m.sysInfoHeight()
	b.WriteString(m.viewPanel(sysLines))
	// Gap line between sysinfo and menu. Outside the menu so it is
	// always present regardless of scroll position.
	b.WriteString("\n")

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

		prompt := m.pending.Confirm
		for i, p := range m.pending.Params {
			if i < len(m.pArgs) {
				prompt = strings.ReplaceAll(prompt, "{{"+p+"}}", m.pArgs[i])
			}
		}

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
		formStr := "\n" + selStyle.Render(m.pending.Name) + "\n\n"
		for i, p := range m.pending.Params {
			paramLabel := labelStyle.Render(p)
			if i < len(m.pArgs) {
				formStr += fmt.Sprintf("  %s %s  %s\n",
					okStyle.Render("✓"),
					paramLabel,
					docStyle.Render(m.pArgs[i]),
				)
			} else if i == len(m.pArgs) {
				formStr += fmt.Sprintf("  %s %s %s\n",
					selStyle.Render("⌨"),
					paramLabel,
					m.input.View(),
				)
			} else {
				formStr += fmt.Sprintf("  %s %s  %s\n",
					docStyle.Render("·"),
					paramLabel,
					docStyle.Render("…"),
				)
			}
		}
		b.WriteString(formStr)
		b.WriteString(helpStyle.Render("enter confirm · esc cancel"))

	case stateRunning:
		if m.isSilent() {
			status := selStyle.Render(m.running)
			b.WriteString("\n" + status + "\n")
			if m.spinOn {
				b.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
			}
			if m.progOn {
				b.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
			}
			b.WriteString(m.viewMenu())
			b.WriteString(helpStyle.Render("ctrl+q kill"))
		} else {
			b.WriteString(m.viewOverlay())
		}

	case stateDone:
		if m.isSilent() {
			status := selStyle.Render(m.running)
			if m.exitCode == 0 {
				status += " " + okStyle.Render("✓ "+exitLabel(m.exitCode))
			} else {
				status += " " + errStyle.Render("✗ "+exitLabel(m.exitCode))
			}
			b.WriteString("\n" + status + "\n")
			b.WriteString(m.viewMenu())
			b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
		} else {
			b.WriteString(m.viewOverlay())
			b.WriteString("\n" + m.viewMenu())
			b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
		}

	default:
		b.WriteString(m.viewMenu())
		b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
	}
	return b.String()
}
