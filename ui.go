package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type uiState int

const (
	stateMenu    uiState = iota
	statePrompt          // collecting a recipe parameter (freeform text)
	stateSelect          // collecting a recipe parameter (choose from options)
	stateConfirm         // showing a proceed/cancel confirmation popup
	stateRunning
	stateDone
	stateInlinePrompt  // mid-execution text/password prompt from the recipe
	stateOptionSelect  // mid-execution option selector (OSC 9;6) from the recipe
	stateInlineConfirm // mid-execution confirm (OSC 9;7) from the recipe
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
	recipe recipe // populated for kindRecipe
	group  string // populated for kindHeader
}

// ── model ─────────────────────────────────────────────────────

type model struct {
	state     uiState
	fields    []infoField
	recipes   []recipe
	run       *runner
	running   string // recipe name being/last run
	exitCode  int
	width     int
	height    int
	prog      progress.Model
	progPct   int
	progOn    bool
	progLabel string // phase label from recipe (e.g. "Downloading...")
	input     textinput.Model
	pending   recipe   // recipe awaiting parameters
	pArgs     []string // collected parameter values
	autorun   []string // CLI args: recipe to start immediately
	cliMode   bool     // true = running from CLI args, not interactive menu

	confirmIdx int // 0 = proceed (default), 1 = cancel

	inlinePrompt struct {
		text   string // prompt text to display
		secret bool   // true = mask input as password
	}

	spin    spinner.Model // indeterminate spinner for progress startup
	spinOn  bool          // spinner currently rendering
	seenOut bool          // first output or progress received, hide spinner

	optSelect struct {
		prompt  string   // text shown above the options
		options []string // selectable choices
		cursor  int      // index into options
	}

	inlineConfirm struct {
		prompt  string   // confirmation text
		options []string // two button labels [opt1, opt2]
	}

	clsOutput bool // true = skip emulator render (set by confirm clear field)

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
	// Empty group header acts as a visual separator before exit.
	items = append(items, menuItem{kind: kindHeader, group: ""})
	items = append(items, menuItem{kind: kindExit})

	// Set initial cursor to the first selectable item (skip group headers).
	firstSel := 0
	for i, it := range items {
		if it.kind != kindHeader {
			firstSel = i
			break
		}
	}

	return model{
		recipes:   recipes,
		prog:      progress.New(progress.WithDefaultGradient()),
		input:     ti,
		spin:      s,
		autorun:   autorun,
		cliMode:   len(autorun) > 0,
		menuItems: items,
		cursor:    firstSel,
	}
}

func (m model) Init() tea.Cmd {
	if m.cliMode {
		// CLI mode: no sysinfo or panel needed, just wait for
		// the first WindowSizeMsg to trigger the recipe.
		return nil
	}
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
	w = m.overlayWidth()
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

// overlayWidth returns the content width for the CLI overlay.
// Scales proportionally with terminal width so the overlay is always
// centered with equal visible margin.
func (m *model) overlayWidth() int {
	w := m.width * 3 / 4 // 75% of terminal
	if w > 100 {
		w = 100 // cap for very wide terminals
	}
	if w < 40 {
		w = 40 // floor for narrow terminals
	}
	return w
}

func (m *model) startRecipe(name string, args []string) tea.Cmd {
	w, h := m.paneSize()
	var extraFlags []string
	if m.pending.Confirm != "" {
		extraFlags = []string{"--yes"}
	}
	rn, err := startRecipe(name, args, w, h, program, extraFlags...)
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
		// Progress bar fits inside the overlay's inner width:
		// overlayWidth() - RoundedBorder (2) - Padding(0, 1) (2).
		m.prog.Width = m.overlayWidth() - 4

		// Clamp cursor and scroll offset after resize so the selected
		// item doesn't end up off-screen when the terminal shrinks.
		n := len(m.menuItems)
		visible := m.menuHeight()
		if m.cursor >= n {
			m.cursor = n - 1
		}
		maxOfs := n - visible
		if maxOfs < 0 {
			maxOfs = 0
		}
		if m.menuOfs > maxOfs {
			m.menuOfs = maxOfs
		}
		if m.menuOfs < 0 {
			m.menuOfs = 0
		}

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
		m.progPct = msg.pct
		m.progLabel = msg.label
		// Recipe controls visibility via state; keep bar at 100% so user
		// sees completion. Recipe sends state=0 to clear explicitly.
		m.progOn = msg.active || msg.pct == 100
		m.seenOut = true
		m.spinOn = false
		return m, nil

	case recipeExitMsg:
		m.exitCode = msg.code
		if m.progPct < 100 {
			m.progOn = false
		}
		m.spinOn = false
		// CLI mode: recipe was invoked from the command line, not the
		// interactive menu. Exit so output stays inline in the terminal.
		if m.cliMode {
			return m, tea.Quit
		}
		// Silent recipes go straight back to menu so the user can
		// immediately select another recipe without an extra Enter.
		if m.pending.Silent {
			m.state = stateMenu
		} else {
			m.state = stateDone
		}
		return m, nil

	case handoverDoneMsg:
		if m.run != nil {
			w, h := m.paneSize()
			m.run.emu.Resize(w, h)
		}
		return m, nil

	case promptRequiredMsg:
		m.inlinePrompt.text = msg.text
		m.inlinePrompt.secret = msg.secret
		m.state = stateInlinePrompt
		m.input.SetValue("")
		m.input.Focus()
		// Size the input to fill the remaining overlay width after the
		// prompt label so the whole prompt fits on one line.
		m.input.Width = m.overlayWidth() - 4 - lipgloss.Width(msg.text) - 3
		if m.input.Width < 10 {
			m.input.Width = 10
		}
		return m, textinput.Blink

	case optionRequiredMsg:
		m.optSelect.prompt = msg.prompt
		m.optSelect.options = msg.options
		m.optSelect.cursor = 0
		m.state = stateOptionSelect
		return m, nil

	case confirmRequiredMsg:
		m.inlineConfirm.prompt = msg.prompt
		m.inlineConfirm.options = msg.options
		if len(msg.options) < 2 {
			m.inlineConfirm.options = []string{"Proceed", "Cancel"}
		}
		if msg.clear {
			m.clsOutput = true
		}
		m.confirmIdx = 0
		m.state = stateInlineConfirm
		return m, nil

	case tea.KeyMsg:
		switch m.state {

		case stateInlinePrompt:
			switch msg.Type {
			case tea.KeyEsc:
				// Cancel: send empty line to unblock the recipe.
				m.run.ptmx.Write([]byte("\n")) //nolint:errcheck
				m.state = stateRunning
				m.inlinePrompt = struct {
					text   string
					secret bool
				}{}
			case tea.KeyEnter:
				resp := m.input.Value() + "\n"
				m.run.ptmx.Write([]byte(resp)) //nolint:errcheck
				m.state = stateRunning
				m.inlinePrompt = struct {
					text   string
					secret bool
				}{}
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m, nil

		case stateOptionSelect:
			switch msg.String() {
			case "up", "k":
				if m.optSelect.cursor > 0 {
					m.optSelect.cursor--
				}
			case "down", "j":
				if m.optSelect.cursor < len(m.optSelect.options)-1 {
					m.optSelect.cursor++
				}
			case "enter":
				choice := m.optSelect.options[m.optSelect.cursor] + "\n"
				m.run.ptmx.Write([]byte(choice)) //nolint:errcheck
				m.state = stateRunning
			case "esc":
				// Cancel: send empty line to unblock the recipe.
				m.run.ptmx.Write([]byte("\n")) //nolint:errcheck
				m.state = stateRunning
			}
			return m, nil

		case stateInlineConfirm:
			switch msg.String() {
			case "enter":
				resp := m.inlineConfirm.options[m.confirmIdx] + "\n"
				m.run.ptmx.Write([]byte(resp)) //nolint:errcheck
				m.state = stateRunning
			case "esc", "q":
				// Cancel: write the cancel option to unblock the recipe.
				cancel := m.inlineConfirm.options[1] + "\n"
				m.run.ptmx.Write([]byte(cancel)) //nolint:errcheck
				m.state = stateRunning
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
			if msg.Type == tea.KeyEsc {
				m.run.kill()
				m.state = stateMenu
				return m, nil
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

		case stateDone:
			// Overlay is shown; only close or quit, no menu navigation.
			switch msg.String() {
			case "enter", "esc":
				m.state = stateMenu
				return m, nil
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil

		default: // stateMenu
			n := len(m.menuItems)
			visible := m.menuHeight()

			switch msg.String() {
			case "up", "k":
				// Skip past headers; if the next item above is a header,
				// keep going until we find a selectable item or hit the top.
				moved := false
				for m.cursor > 0 {
					next := m.cursor - 1
					if m.menuItems[next].kind == kindHeader {
						above := next - 1
						if above >= 0 && m.menuItems[above].kind != kindHeader {
							m.cursor = above
							moved = true
						}
						break
					}
					m.cursor = next
					moved = true
					break
				}
				// If cursor didn't move (already at the first selectable
				// item), scroll the menu to show the header above it.
				if !moved && m.menuOfs > 0 {
					m.menuOfs--
				}
				// Keep the cursor visible. Clamp offset so it never goes
				// negative or beyond the last page.
				if visible >= n {
					m.menuOfs = 0
				} else {
					for m.cursor < m.menuOfs {
						m.menuOfs--
					}
					if m.menuOfs < 0 {
						m.menuOfs = 0
					}
					maxOfs := n - visible
					if m.menuOfs > maxOfs {
						m.menuOfs = maxOfs
					}
				}

			case "down", "j":
				for m.cursor < n-1 {
					m.cursor++
					if m.menuItems[m.cursor].kind != kindHeader {
						break
					}
				}
				// Keep the cursor visible. Clamp offset so it never goes
				// negative or beyond the last page.
				if visible >= n {
					m.menuOfs = 0
				} else {
					for m.cursor >= m.menuOfs+visible {
						m.menuOfs++
					}
					maxOfs := n - visible
					if m.menuOfs > maxOfs {
						m.menuOfs = maxOfs
					}
					if m.menuOfs < 0 {
						m.menuOfs = 0
					}
				}

			case "enter":
				mi := m.menuItems[m.cursor]
				switch mi.kind {
				case kindRecipe:
					return m, m.selectRecipe(mi.recipe, nil)
				case kindExit:
					return m, tea.Quit
				}

			case "q", "ctrl+c":
				return m, tea.Quit

			case "esc":
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

	if m.run != nil && !m.clsOutput {
		content.WriteString("\n" + strings.TrimRight(m.run.emu.Render(), "\n ") + "\n")
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	if m.progOn {
		if m.progLabel != "" {
			content.WriteString(docStyle.Render(m.progLabel) + "\n")
		}
		content.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
	}

	if m.cliMode {
		content.WriteString(helpStyle.Render("ctrl+q kill"))
	} else if m.state == stateRunning {
		content.WriteString(helpStyle.Render("keys go to the recipe · ctrl+q kill"))
	} else {
		content.WriteString(helpStyle.Render("recipe finished · enter close · ↑/↓ navigate · q quit"))
	}

	return content.String()
}

// viewOverlayWithConfirm renders the running overlay with an embedded
// two-button confirmation popup (OSC 9;7) between the CLI output and
// the progress bar, centred horizontally. Button labels come from the
// recipe via the optional 4th field of the OSC sequence.
func (m model) viewOverlayWithConfirm() string {
	labels := m.inlineConfirm.options
	if len(labels) < 2 {
		labels = []string{"Proceed", "Cancel"}
	}
	var content strings.Builder

	status := selStyle.Render(m.running)
	content.WriteString(status + "\n")

	// Emulator output sits above the prompt (skipped if cleared via clear_cli).
	// Trim trailing empty lines so the confirm prompt doesn't get
	// pushed to the bottom when there's little or no output.
	if m.run != nil && !m.clsOutput {
		emuOut := strings.TrimRight(m.run.emu.Render(), "\n ")
		if emuOut != "" {
			content.WriteString("\n" + emuOut + "\n")
		}
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	// Confirmation prompt and buttons, centred in the overlay,
	// between emulator output and progress bar.
	prompt := m.inlineConfirm.prompt
	if prompt != "" {
		content.WriteString(lipgloss.NewStyle().Width(m.overlayWidth()-4).Align(lipgloss.Center).Render(prompt) + "\n")
	}

	var opt1, opt2 string
	if m.confirmIdx == 0 {
		opt1 = selStyle.Render("▸ "+labels[0]) + " "
	} else {
		opt1 = "  " + labels[0] + "  "
	}
	if m.confirmIdx == 1 {
		opt2 = selStyle.Render("▸ "+labels[1]) + " "
	} else {
		opt2 = "  " + labels[1] + "  "
	}
	btnRow := lipgloss.NewStyle().Width(m.overlayWidth() - 4).Align(lipgloss.Center).Render(opt1 + opt2)
	content.WriteString(btnRow + "\n")

	if m.progOn {
		if m.progLabel != "" {
			content.WriteString(docStyle.Render(m.progLabel) + "\n")
		}
		content.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
	}

	content.WriteString(helpStyle.Render("← → navigate · enter confirm · esc cancel"))

	return content.String()
}

// viewOverlayWithPrompt renders the running overlay with an embedded prompt
// form above the emulator output.
func (m model) viewOverlayWithPrompt() string {
	var content strings.Builder

	status := selStyle.Render(m.running)
	content.WriteString(status + "\n")

	// Emulator output sits above the prompt.
	if m.run != nil && !m.clsOutput {
		emuOut := strings.TrimRight(m.run.emu.Render(), "\n ")
		if emuOut != "" {
			content.WriteString("\n" + emuOut + "\n")
		}
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	// Prompt: styled label + input field, between CLI output and progress bar.
	promptLabel := m.inlinePrompt.text
	if promptLabel == "" {
		promptLabel = "Input:"
	}
	inputView := m.input.View()
	if m.inlinePrompt.secret {
		inputView = strings.Repeat("●", len(m.input.Value()))
	}
	promptRow := lipgloss.NewStyle().Width(m.overlayWidth() - 4).Align(lipgloss.Left).
		Render(selStyle.Render(promptLabel) + " " + inputView)
	content.WriteString("\n" + promptRow + "\n")

	if m.progOn {
		if m.progLabel != "" {
			content.WriteString(docStyle.Render(m.progLabel) + "\n")
		}
		content.WriteString(m.prog.ViewAs(float64(m.progPct)/100) + "\n")
	}

	content.WriteString(helpStyle.Render("enter confirm · esc cancel"))

	return content.String()
}

// overlayView renders an overlay state: builds a bordered foreground from
// content + style variant, composites it over the system panel and menu
// (centered horizontally and vertically). styleFn customizes the base
// bordered style per state (e.g. Padding, Align).
func (m model) overlayView(bgBase, content, bgHelp string, styleFn func(lipgloss.Style) lipgloss.Style) string {
	// Build the foreground with border and width.
	base := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Width(m.overlayWidth())
	fg := styleFn(base).Render(content)

	// Build the background: system panel + menu + help text.
	menu := m.viewMenu()
	bg := bgBase + menu + helpStyle.Render(bgHelp)

	// Composite foreground over background with centering.
	fgLines := strings.Split(fg, "\n")
	bgLines := strings.Split(bg, "\n")
	fgW := lipgloss.Width(fg)
	fgH := len(fgLines)

	for len(bgLines) < m.height {
		bgLines = append(bgLines, "")
	}

	x := (m.width - fgW) / 2
	if x < 0 {
		x = 0
	}

	y := (m.height - fgH) / 2
	if y < 0 {
		y = 0
	}

	out := make([]string, len(bgLines))
	for i, bgLine := range bgLines {
		if i >= y && i < y+fgH {
			fgLine := fgLines[i-y]
			fgLineW := ansi.StringWidth(fgLine)

			left := ansi.Truncate(bgLine, x, "")
			leftW := ansi.StringWidth(left)
			if leftW < x {
				left += strings.Repeat(" ", x-leftW)
			}

			rightStart := x + fgLineW
			right := ansi.TruncateLeft(bgLine, rightStart, "")

			line := left + fgLine + right
			if lw := ansi.StringWidth(line); lw < m.width {
				line += strings.Repeat(" ", m.width-lw)
			}
			out[i] = line
		} else {
			if lw := ansi.StringWidth(bgLine); lw < m.width {
				out[i] = bgLine + strings.Repeat(" ", m.width-lw)
			} else {
				out[i] = bgLine
			}
		}
	}
	return strings.Join(out, "\n")
}

func (m model) View() string {
	var b strings.Builder

	if m.cliMode {
		// CLI mode: output streams inline in the terminal scrollback.
		// No border, no overlay compositing, no sysinfo panel or menu.
		// Each state renders its content as a plain string.
		switch m.state {
		case stateRunning:
			return m.viewOverlay()
		case stateInlinePrompt:
			return m.viewOverlayWithPrompt()
		case stateInlineConfirm:
			return m.viewOverlayWithConfirm()
		case stateOptionSelect:
			var body strings.Builder
			body.WriteString(selStyle.Render(m.running) + "\n\n")
			if m.optSelect.prompt != "" {
				body.WriteString(docStyle.Render(m.optSelect.prompt) + "\n\n")
			}
			for i, opt := range m.optSelect.options {
				if i == m.optSelect.cursor {
					body.WriteString(selStyle.Render("▸ "+opt) + "\n")
				} else {
					body.WriteString("  " + docStyle.Render(opt) + "\n")
				}
			}
			body.WriteString(helpStyle.Render("↑/↓ navigate · enter select · esc cancel"))
			return body.String()
		default:
			return ""
		}
	}

	title := titleStyle.Render("● " + osName())
	if tag := buildTag(); tag != "" {
		title += " " + docStyle.Render(tag)
	}
	b.WriteString(title + "\n")

	sysLines := m.sysInfoHeight()
	b.WriteString(m.viewPanel(sysLines))
	b.WriteString("\n")

	switch m.state {
	case stateSelect:
		paramName := m.pending.Params[len(m.pArgs)]
		body := "\n" + selStyle.Render(m.pending.Name) +
			docStyle.Render(" > ") + labelStyle.Render(paramName) + "\n\n"
		for i, opt := range m.selOpts {
			if i == m.selCur {
				body += selStyle.Render("▸ "+opt) + "\n"
			} else {
				body += "  " + docStyle.Render(opt) + "\n"
			}
		}
		body += helpStyle.Render("↑/↓ navigate · enter select · esc cancel")
		return m.overlayView(b.String(), body, "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })

	case stateConfirm:
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
		return m.overlayView(b.String(), body, "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style {
				return s.Padding(1, 2).Align(lipgloss.Center)
			})

	case statePrompt:
		body := "\n" + selStyle.Render(m.pending.Name) + "\n\n"
		for i, p := range m.pending.Params {
			paramLabel := labelStyle.Render(p)
			if i < len(m.pArgs) {
				body += fmt.Sprintf("  %s %s  %s\n",
					okStyle.Render("✓"),
					paramLabel,
					docStyle.Render(m.pArgs[i]),
				)
			} else if i == len(m.pArgs) {
				body += fmt.Sprintf("  %s %s %s\n",
					selStyle.Render("⌨"),
					paramLabel,
					m.input.View(),
				)
			} else {
				body += fmt.Sprintf("  %s %s  %s\n",
					docStyle.Render("·"),
					paramLabel,
					docStyle.Render("…"),
				)
			}
		}
		body += helpStyle.Render("enter confirm · esc cancel")
		return m.overlayView(b.String(), body, "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })

	case stateOptionSelect:
		selStr := "\n" + selStyle.Render(m.running) + "\n\n"
		if m.optSelect.prompt != "" {
			selStr += docStyle.Render(m.optSelect.prompt) + "\n\n"
		}
		for i, opt := range m.optSelect.options {
			if i == m.optSelect.cursor {
				selStr += selStyle.Render("▸ "+opt) + "\n"
			} else {
				selStr += "  " + docStyle.Render(opt) + "\n"
			}
		}
		body := selStr + "\n" + helpStyle.Render("↑/↓ navigate · enter select · esc cancel")
		return m.overlayView(b.String(), body, "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })

	case stateInlinePrompt:
		return m.overlayView(b.String(), m.viewOverlayWithPrompt(), "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })

	case stateInlineConfirm:
		return m.overlayView(b.String(), m.viewOverlayWithConfirm(), "↑/↓ select · enter run · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1).AlignVertical(lipgloss.Center) })

	case stateRunning:
		if m.isSilent() {
			b.WriteString(m.viewMenu())
			b.WriteString(helpStyle.Render("ctrl+q kill"))
		} else {
			return m.overlayView(b.String(), m.viewOverlay(), "↑/↓ select · enter run · q quit",
				func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })
		}

	case stateDone:
		return m.overlayView(b.String(), m.viewOverlay(), "enter close · ↑/↓ select · q quit",
			func(s lipgloss.Style) lipgloss.Style { return s.Padding(0, 1) })

	default:
		b.WriteString(m.viewMenu())
		b.WriteString(helpStyle.Render("↑/↓ select · enter run · q quit"))
	}
	return b.String()
}
