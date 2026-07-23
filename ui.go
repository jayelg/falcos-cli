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
	justfile  string   // path to the system justfile

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

	cliHidden bool // true when recipe hides CLI output via OSC 9;8
	confirmIdx int  // 0 = proceed (default), 1 = cancel (for inline confirm)

	menuItems    []menuItem      // flat list: headers + recipes + exit
	cursor       int             // selected index into menuItems
	menuOfs      int             // first visible menu line index (smooth scroll)
	filterInput   textinput.Model // search/filter bar above the recipe menu
	filterText    string          // current filter string (lowercased for matching)
	filterVisible bool            // true when the search bar is shown

	lastSavedPath string   // path of the last saved log file, shown as feedback
	loadError     string   // non-empty when recipe loading failed; shown instead of menu
	summaryLines  []string // OSC 9;10 summary lines from the recipe
	showSummary   bool     // OSC 9;11 triggered: render summary now
}

func newModel(recipes []recipe, autorun []string, justfile string) model {
	ti := textinput.New()
	ti.CharLimit = 128
	fi := textinput.New()
	fi.CharLimit = 64
	fi.Placeholder = "Filter recipes..."
	fi.Prompt = "🔍 "
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
		recipes:     recipes,
		prog:        progress.New(progress.WithDefaultGradient()),
		input:       ti,
		filterInput: fi,
		spin:        s,
		autorun:     autorun,
		cliMode:     len(autorun) > 0,
		justfile:    justfile,
		menuItems:   items,
		cursor:      firstSel,
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

// menuHeight returns how many menu lines fit below the sysinfo panel,
// gap line, and optional search bar.
func (m model) menuHeight() int {
	sys := m.sysInfoHeight()
	extra := 1 // gap
	if m.filterVisible {
		extra++ // search bar
	}
	h := m.height - 1 - sys - extra - 1 // title, extra, help
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) paneSize() (w, h int) {
	w = m.overlayWidth()
	if m.cliMode {
		// Full terminal height for inline CLI mode; no overlay caps.
		h = m.height - 1
		if h < 5 {
			h = 5
		}
		return w, h
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
	m.summaryLines = nil
	m.showSummary = false
	m.lastSavedPath = ""
	m.cliHidden = false
	rn, err := startRecipe(name, args, w, h, program, m.justfile)
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
	m.spinOn = true
	return m.spin.Tick
}

// selectRecipe prompts for parameters, then starts the recipe.
func (m *model) selectRecipe(r recipe, preArgs []string) tea.Cmd {
	m.pending = r
	m.pArgs = preArgs

	if len(preArgs) < len(r.Params) {
		nextParam := r.Params[len(preArgs)]
		m.input.Placeholder = nextParam
		m.input.SetValue("")
		m.input.Focus()
		m.state = statePrompt
		return textinput.Blink
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

	case summaryMsg:
		m.summaryLines = append(m.summaryLines, msg.text)
		return m, nil

	case summaryShowMsg:
		m.showSummary = true
		return m, nil

	case summaryClearMsg:
		m.summaryLines = nil
		m.showSummary = false
		return m, nil

	case cliVisibilityMsg:
		m.cliHidden = !msg.visible
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
		// Recipes that hid CLI output go straight back to menu so the
		// user can immediately select another recipe.
		if m.cliHidden {
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
			m.cliHidden = true
		}
		m.confirmIdx = 0 // reused field for confirm button index
		m.state = stateInlineConfirm
		return m, nil

	case tea.MouseMsg:
		switch m.state {
		case stateMenu:
			items := m.filteredItems()
			menuStartY := m.sysInfoHeight() + 2 // title + sysinfo + gap
			if m.filterVisible {
				menuStartY++ // search bar
			}
			switch msg.Type {
			case tea.MouseWheelUp:
				if m.menuOfs > 0 {
					m.menuOfs--
				}
			case tea.MouseWheelDown:
				visible := m.menuHeight()
				if m.menuOfs+visible < len(items) {
					m.menuOfs++
				}
			case tea.MouseMotion:
				idx := m.menuOfs + (msg.Y - menuStartY)
				if idx >= 0 && idx < len(items) && items[idx].kind != kindHeader {
					m.cursor = idx
				}
			case tea.MouseLeft:
				idx := m.menuOfs + (msg.Y - menuStartY)
				if idx >= 0 && idx < len(items) {
					mi := items[idx]
					switch mi.kind {
					case kindRecipe:
						return m, m.selectRecipe(mi.recipe, nil)
					case kindExit:
						return m, tea.Quit
					}
				}
			}
		}
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
			case tea.KeyRunes:
				if msg.String() == "s" {
					if m.run != nil {
						path, err := m.run.saveOutput(m.running)
						if err == nil {
							m.lastSavedPath = path
						}
					}
				} else if b := keyBytes(msg); b != nil && m.run != nil {
					m.run.ptmx.Write(b) //nolint:errcheck
				}
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

		case stateDone:
			switch msg.String() {
			case "enter", "esc":
				m.state = stateMenu
				return m, nil
			case "s":
				if m.run != nil {
					path, err := m.run.saveOutput(m.running)
					if err == nil {
						m.lastSavedPath = path
					}
				}
				return m, nil
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil

		default: // stateMenu
			// When filter is visible and focused, keys go to the filter input.
			if m.filterVisible && m.filterInput.Focused() {
				switch msg.Type {
				case tea.KeyEsc:
					m.filterVisible = false
					m.filterInput.SetValue("")
					m.filterText = ""
					m.filterInput.Blur()
					m.cursor = 0
					m.menuOfs = 0
					return m, nil
				case tea.KeyEnter:
					m.filterVisible = false
					m.filterInput.SetValue("")
					m.filterText = ""
					m.filterInput.Blur()
					items := m.filteredItems()
					if m.cursor >= 0 && m.cursor < len(items) {
						mi := items[m.cursor]
						if mi.kind == kindRecipe {
							return m, m.selectRecipe(mi.recipe, nil)
						}
						if mi.kind == kindExit {
							return m, tea.Quit
						}
					}
					return m, nil
				default:
					var cmd tea.Cmd
					m.filterInput, cmd = m.filterInput.Update(msg)
					m.filterText = m.filterInput.Value()
					items := m.filteredItems()
					if len(items) > 0 {
						firstSel := 0
						for i, it := range items {
							if it.kind != kindHeader {
								firstSel = i
								break
							}
						}
						m.cursor = firstSel
					}
					m.menuOfs = 0
					return m, cmd
				}
			}

			items := m.filteredItems()
			n := len(items)
			visible := m.menuHeight()

			switch msg.String() {
			case "/":
				m.filterVisible = true
				m.filterInput.Focus()
				m.filterInput.SetValue("")
				m.filterText = ""
				return m, textinput.Blink

			case "r":
				// Refresh system info.
				m.fields = nil
				return m, func() tea.Msg { return infoMsg(gatherInfo()) }

			case "up", "k":
				moved := false
				for m.cursor > 0 {
					next := m.cursor - 1
					if items[next].kind == kindHeader {
						above := next - 1
						if above >= 0 && items[above].kind != kindHeader {
							m.cursor = above
							moved = true
						}
						break
					}
					m.cursor = next
					moved = true
					break
				}
				if !moved && m.menuOfs > 0 {
					m.menuOfs--
				}
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
					if items[m.cursor].kind != kindHeader {
						break
					}
				}
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
				if m.cursor >= 0 && m.cursor < n {
					mi := items[m.cursor]
					switch mi.kind {
					case kindRecipe:
						return m, m.selectRecipe(mi.recipe, nil)
					case kindExit:
						return m, tea.Quit
					}
				}

			case "q", "ctrl+c":
				return m, tea.Quit

			case "esc":
				if m.filterText != "" {
					m.filterText = ""
					m.filterInput.SetValue("")
					m.cursor = 0
					m.menuOfs = 0
					return m, nil
				}
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

// filteredItems returns the subset of menuItems matching the current filter.
// Headers are included only when at least one recipe follows in the group.
// When filter is empty, returns the full menuItems unchanged.
func (m model) filteredItems() []menuItem {
	if m.filterText == "" {
		return m.menuItems
	}
	lower := strings.ToLower(m.filterText)
	var out []menuItem
	var pendingHeader *menuItem
	for i := range m.menuItems {
		mi := &m.menuItems[i]
		switch mi.kind {
		case kindHeader:
			pendingHeader = mi
		case kindRecipe:
			if strings.Contains(strings.ToLower(mi.recipe.Name), lower) ||
				strings.Contains(strings.ToLower(mi.recipe.Doc), lower) {
				if pendingHeader != nil {
					out = append(out, *pendingHeader)
					pendingHeader = nil
				}
				out = append(out, *mi)
			}
		case kindExit:
			if pendingHeader != nil {
				out = append(out, *pendingHeader)
				pendingHeader = nil
			}
			out = append(out, *mi)
		}
	}
	if pendingHeader != nil {
		out = append(out, *pendingHeader)
	}
	return out
}

func (m model) viewMenu() string {
	items := m.filteredItems()
	visible := m.menuHeight()
	var b strings.Builder
	for i := m.menuOfs; i < m.menuOfs+visible && i < len(items); i++ {
		mi := items[i]
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

// viewSummary renders OSC 9;10 summary lines accumulated during execution.
func (m model) viewSummary() string {
	if len(m.summaryLines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range m.summaryLines {
		b.WriteString(selStyle.Render("  "+line) + "\n")
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

	if m.run != nil && !m.cliHidden {
		content.WriteString("\n" + strings.TrimRight(m.run.emu.Render(), "\n ") + "\n")
	}

	// Summary lines: explicit show, CLI hidden, or successful completion.
	if m.showSummary || m.cliHidden || (m.state == stateDone && m.exitCode == 0) {
		content.WriteString(m.viewSummary())
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
		content.WriteString(helpStyle.Render("keys go to the recipe · ctrl+q kill · s save log"))
	} else {
		hint := "enter close · s save log · q quit"
		if m.exitCode != 0 {
			hint = "recipe failed · s save log · enter close · q quit"
		}
		content.WriteString(helpStyle.Render(hint))
		if m.lastSavedPath != "" {
			content.WriteString("\n" + okStyle.Render("log saved: "+m.lastSavedPath))
		}
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

	// Emulator output sits above the prompt (skipped if CLI hidden).
	// Trim trailing empty lines so the confirm prompt doesn't get
	// pushed to the bottom when there's little or no output.
	if m.run != nil && !m.cliHidden {
		emuOut := strings.TrimRight(m.run.emu.Render(), "\n ")
		if emuOut != "" {
			content.WriteString("\n" + emuOut + "\n")
		}
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	// Summary lines from OSC 9;10, shown above the confirm prompt.
	if m.showSummary || m.cliHidden {
		content.WriteString(m.viewSummary())
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
	if m.run != nil && !m.cliHidden {
		emuOut := strings.TrimRight(m.run.emu.Render(), "\n ")
		if emuOut != "" {
			content.WriteString("\n" + emuOut + "\n")
		}
	}

	if m.spinOn {
		content.WriteString(m.spin.View() + " " + docStyle.Render("waiting for progress...") + "\n")
	}

	// Summary lines from OSC 9;10, shown above the prompt.
	if m.showSummary || m.cliHidden {
		content.WriteString(m.viewSummary())
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
		if m.cliHidden {
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
		if m.loadError != "" {
			b.WriteString(errStyle.Render("Error: "+m.loadError) + "\n")
			b.WriteString(helpStyle.Render("r refresh · q quit"))
		} else {
			if m.filterVisible {
				b.WriteString(m.filterInput.View() + "\n")
			}
			b.WriteString(m.viewMenu())
			b.WriteString(helpStyle.Render("↑/↓ select · / filter · r refresh · q quit"))
		}
	}
	return b.String()
}
