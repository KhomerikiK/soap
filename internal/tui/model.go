package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

const (
	minWidth  = 80
	minHeight = 20
	// frameLines is everything around a screen's body: status, tabs, two rules, message, keys.
	frameLines = 6
	// cpuKept is how many CPU samples the frame keeps (2 minutes); the status bar shows cpuShown.
	cpuKept  = 60
	cpuShown = 8
)

// laterScreens are placeholders for the parts not built yet, shown dimmed in the tab line. Every
// planned screen is built; startup items and security checks may come back here later.
var laterScreens []string

// screen is one tab. Screens hold no state; everything they need is on the Model.
type screen interface {
	name() string
	columns() []column
	sortModes() []string
	hints() []hint
	problem(m *Model) string    // a source error that replaces the table; "" when fine
	preamble(m *Model) []string // lines above the column titles
	rows(m *Model) []row
	empty(m *Model) string // shown when there are no rows
	key(m *Model, key string, r *row) (tea.Cmd, bool)
}

// shower is a screen that reacts to being opened.
type shower interface {
	onShow(m *Model) tea.Cmd
}

// drawer is a screen that draws its whole body, as Home draws its box. The frame leaves out its
// two rules for it, so the body is height − 4 lines.
type drawer interface {
	draw(m *Model, width, height int) []string
}

// shortNamer is a screen with a shorter name for the tab line in narrow windows.
type shortNamer interface{ shortName() string }

type hint struct{ key, desc string }

type confirmation struct {
	text   string // the question, on the message line
	detail string // extra context, on the key line before [y] and [n]
	danger bool
	yes    tea.Cmd // what runs on y or enter
	// working is shown on the message line while yes runs, so slow actions don't look frozen.
	working string
	// cancel, when set, runs when the answer is no.
	cancel func(m *Model)
}

// Model is the root Bubble Tea model: the frame around the screens.
type Model struct {
	deps    Deps
	screens []screen
	tab     int
	width   int
	height  int

	snap    engine.Snapshot
	have    bool
	opt     engine.Options
	paused  bool
	cpuHist []float64

	collecting bool // a collect is running; only one is ever in flight
	again      bool // a refresh was asked for while one was running

	sel      []int // selected row per screen
	sortIdx  []int // sort mode per screen
	filter   string
	editing  bool // typing a filter
	help     bool
	expanded map[string]bool // Connections groups that are open

	msg         string
	msgStyle    lipgloss.Style
	confirm     *confirmation
	dbHintShown bool

	checks map[model.Screen]*checkState // every clean-up screen: Junk, Dev, Disk, Dupes and Apps
	disk   *diskState
	home   *homeState
}

// New builds the TUI.
func New(deps Deps) Model {
	if deps.Interval == 0 {
		deps.Interval = 2 * time.Second
	}
	// In the order of model.TabHome to model.TabApps: key 0 opens Home.
	screens := []screen{homeScreen{}, portsScreen{}, connsScreen{}, memoryScreen{}, junkScreen, devScreen, diskScreen{}, dupesScreen{}, appsScreen{}}
	return Model{
		deps:       deps,
		screens:    screens,
		sel:        make([]int, len(screens)),
		sortIdx:    make([]int, len(screens)),
		expanded:   map[string]bool{},
		collecting: true, // Init starts the first collect
		checks: map[model.Screen]*checkState{
			model.ScreenJunk: newCheckState(), model.ScreenDev: newCheckState(), model.ScreenDisk: newCheckState(),
			model.ScreenDupes: newCheckState(), model.ScreenApps: newCheckState(),
		},
		disk: &diskState{},
		home: &homeState{},
	}
}

type (
	tickMsg     struct{}
	snapshotMsg struct{ snap engine.Snapshot }
	redrawMsg   struct{}
	resultMsg   struct {
		text  string
		err   bool // shown as an error
		warn  bool // shown as a warning
		quiet bool // shown faint, for results that aren't good or bad news
	}
)

// Redraw returns a message that makes the TUI draw again, for background lookups that finish
// between refreshes.
func Redraw() tea.Msg { return redrawMsg{} }

// Init starts the first read, the refresh timer, the Junk, Dev and Apps scans and reading the
// history. Scans keep their state in pointers, so starting them here on a copy of the Model sticks.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.collect(), m.tick(), m.startLightScans(), m.readHistory())
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.deps.Interval, func(time.Time) tea.Msg { return tickMsg{} })
}

// collect reads a snapshot in the background with the current options. Use refresh instead,
// except in Init.
func (m Model) collect() tea.Cmd {
	collect, opt := m.deps.Collect, m.opt
	return func() tea.Msg { return snapshotMsg{snap: collect(context.Background(), opt)} }
}

// refresh starts a collect unless one is already running; then it runs once more when that one
// finishes. Only one collect is ever in flight, so a slow source can't pile up collects and
// snapshots can't arrive out of order.
func (m *Model) refresh() tea.Cmd {
	if m.collecting {
		m.again = true
		return nil
	}
	m.collecting = true
	return m.collect()
}

// Update handles one message. While Home is open it then builds what Home shows, so its numbers
// follow the data as it arrives and View only draws.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	if nm.tab == model.TabHome {
		nm.refreshHome()
		cmd = tea.Batch(cmd, nm.spin())
	}
	return nm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		if m.paused {
			return m, m.tick()
		}
		cmd := m.refresh()
		return m, tea.Batch(cmd, m.tick())
	case snapshotMsg:
		m.collecting = false
		m.snap, m.have = msg.snap, true
		m.cpuHist = append(m.cpuHist, msg.snap.CPU)
		if len(m.cpuHist) > cpuKept {
			m.cpuHist = m.cpuHist[len(m.cpuHist)-cpuKept:]
		}
		m.clampSelection()
		if m.again {
			m.again = false
			cmd := m.refresh()
			return m, cmd
		}
	case resultMsg:
		switch {
		case msg.err:
			m.setMsg(msg.text, danger)
		case msg.warn:
			m.setMsg(msg.text, warn)
		case msg.quiet:
			m.setMsg(msg.text, faint)
		default:
			m.setMsg(msg.text, good)
		}
		cmd := m.refresh()
		return m, cmd
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case spinMsg:
		m.home.spinning = false
		m.home.frame++
	default:
		if next, cmd, ok := m.handleChecklist(msg); ok {
			return next, cmd
		}
		return m.handleFlow(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	switch {
	case key == "ctrl+c":
		m.stopBackground()
		return m, tea.Quit
	case m.help:
		m.help = false
		return m, nil
	case m.confirm != nil:
		return m.answer(key)
	case m.editing:
		m.editFilter(k)
		return m, nil
	}
	if o, ok := m.screens[m.tab].(keyOwner); ok && o.ownsKey(&m, key) {
		return m.screenKey(key)
	}
	switch key {
	case "q":
		for _, cs := range m.checks {
			if cs.mode == modeCleaning {
				m.setMsg("A clean-up is running. Press esc to stop it after the current item.", warn)
				return m, nil
			}
		}
		m.stopBackground()
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "/":
		m.editing, m.filter = true, ""
		m.resetSelection()
	case "esc":
		if e, ok := m.screens[m.tab].(escaper); ok && e.esc(&m) {
			return m, nil
		}
		m.filter = ""
		m.resetSelection()
	case "s":
		modes := m.screens[m.tab].sortModes()
		m.sortIdx[m.tab] = (m.sortIdx[m.tab] + 1) % len(modes)
	case "p":
		if _, ok := m.screens[m.tab].(cleanupScreen); ok {
			return m, nil // pausing is for the live screens
		}
		m.paused = !m.paused
		if !m.paused {
			m.msg = ""
			cmd := m.refresh()
			return m, cmd
		}
		m.setMsg("Paused. Press p to resume.", warn)
	case "?":
		m.help = true
	default:
		if len(key) == 1 && key[0] >= '0' && int(key[0]-'0') < len(m.screens) {
			cmd := m.switchTab(int(key[0] - '0'))
			return m, cmd
		}
		return m.screenKey(key)
	}
	return m, nil
}

// keyOwner is a screen that takes a key the frame would otherwise use, such as k on Dupes.
type keyOwner interface {
	ownsKey(m *Model, key string) bool
}

// screenKey passes a key to the current screen, with the selected row.
func (m Model) screenKey(key string) (tea.Model, tea.Cmd) {
	rows := m.visibleRows()
	var r *row
	if i := m.sel[m.tab]; i < len(rows) {
		r = &rows[i]
	}
	cmd, _ := m.screens[m.tab].key(&m, key, r)
	return m, cmd
}

// stopBackground stops the jobs that keep running on other tabs, when soap quits.
func (m *Model) stopBackground() {
	if m.disk.cancel != nil {
		m.disk.cancel()
	}
	for _, cs := range m.checks { // a scan nobody reads would hold scan.Run's lock; cancelling ends it
		if cs.cancel != nil {
			cs.cancel()
		}
	}
}

// switchTab opens tab i. Scans keep running on the tab you leave, as the Disk walk does.
func (m *Model) switchTab(i int) tea.Cmd {
	m.tab = i
	m.msg = ""
	m.filter, m.editing = "", false
	if s, ok := m.screens[i].(shower); ok {
		return s.onShow(m)
	}
	return nil
}

func (m Model) answer(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "enter":
		cmd := m.confirm.yes
		working := m.confirm.working
		m.confirm = nil
		m.msg = ""
		if working != "" {
			m.setMsg(working, faint)
		}
		return m, cmd
	case "n", "esc":
		cancel := m.confirm.cancel
		m.confirm = nil
		m.setMsg("Cancelled.", faint)
		if cancel != nil {
			cancel(&m)
		}
	}
	return m, nil
}

func (m *Model) editFilter(k tea.KeyPressMsg) {
	switch k.String() {
	case "esc":
		m.filter, m.editing = "", false
	case "enter":
		m.editing = false
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	default:
		m.filter += k.Text
	}
	m.resetSelection()
}

func (m *Model) setMsg(text string, style lipgloss.Style) { m.msg, m.msgStyle = oneLine(text), style }

// oneLine collapses all whitespace, including newlines from command output, so a message always
// stays on its own line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (m *Model) move(delta int) {
	rows := m.visibleRows()
	m.sel[m.tab] = max(0, min(m.sel[m.tab]+delta, len(rows)-1))
	if _, ok := m.screens[m.tab].(diskScreen); ok {
		m.disk.selPath = m.selectedDiskPath(rows)
	}
}

func (m *Model) resetSelection() {
	m.sel[m.tab] = 0
	if _, ok := m.screens[m.tab].(diskScreen); ok {
		m.disk.selPath = ""
	}
}

func (m *Model) clampSelection() {
	n := len(m.visibleRows())
	m.sel[m.tab] = max(0, min(m.sel[m.tab], n-1))
}

// visibleRows is the current screen's rows after the filter. On Disk it re-anchors the selection
// (it changes m.sel and selPath); that's safe from the draw because disk is a pointer and sel a slice.
func (m *Model) visibleRows() []row {
	rows := m.screens[m.tab].rows(m)
	if m.filter != "" {
		f := strings.ToLower(m.filter)
		var out []row
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.search()), f) {
				out = append(out, r)
			}
		}
		rows = out
	}
	if _, ok := m.screens[m.tab].(diskScreen); ok {
		m.anchorSelection(rows)
	}
	return rows
}

// View draws the frame and the current screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "soap"
	return v
}

func (m Model) render() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Make the window at least %d×%d. It's %d×%d now.", minWidth, minHeight, m.width, m.height)
	}
	lines := []string{m.statusLine(), m.tabLine()}
	if d, ok := m.screens[m.tab].(drawer); ok && m.have && !m.help {
		h := m.height - 4 // the box takes the rules' place
		lines = append(lines, padLines(d.draw(&m, m.width, h), h)...)
	} else {
		rule := faint.Render(strings.Repeat("─", m.width))
		lines = append(lines, rule)
		lines = append(lines, m.body(m.height-frameLines)...)
		lines = append(lines, rule)
	}
	lines = append(lines, m.messageLine(), m.keyLine())
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) body(h int) []string {
	var lines []string
	switch {
	case m.help:
		lines = helpLines()
	case !m.have:
		lines = []string{" Reading system data…"}
	default:
		lines = m.screenLines(h)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

func (m Model) screenLines(h int) []string {
	s := m.screens[m.tab]
	if p := s.problem(&m); p != "" {
		return []string{" " + danger.Render(oneLine(p)), faint.Render(fmt.Sprintf(" macOS %s. The other screens keep working.", m.deps.MacOS))}
	}
	lines := s.preamble(&m)
	if len(lines) > 0 {
		lines[0] += faint.Render(m.sortFilterInfo())
	}
	cols := s.columns()
	ws := widths(cols, m.width)
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = c.title
	}
	lines = append(lines, faint.Render(renderCells(titles, nil, ws, false)))
	rows := m.visibleRows()
	if len(rows) == 0 {
		empty := s.empty(&m)
		if m.filter != "" {
			empty = fmt.Sprintf("No rows match “%s”.", m.filter)
		}
		return append(lines, faint.Render(" "+empty))
	}
	room := max(h-len(lines), 1)
	sel := min(m.sel[m.tab], len(rows)-1)
	start := max(0, sel-room+1)
	for i := start; i < len(rows) && i < start+room; i++ {
		lines = append(lines, rows[i].render(ws, i == sel, m.width))
	}
	return lines
}

func (m Model) sortFilterInfo() string {
	modes := m.screens[m.tab].sortModes()
	info := "   sort: " + modes[m.sortIdx[m.tab]]
	if m.filter != "" {
		info += "   filter: " + m.filter
	}
	return info
}

func (m Model) statusLine() string {
	sep := faint.Render("  │  ")
	parts := []string{accent.Render(" soap")}
	if m.have {
		s := m.snap
		parts = append(parts, fmt.Sprintf("CPU %s %3.0f%%", accent.Render(sparkline(m.cpuHist[max(0, len(m.cpuHist)-cpuShown):])), s.CPU))
		if s.MemoryErr == nil {
			parts = append(parts, fmt.Sprintf("mem %s/%s %s", model.HumanBytes(s.Memory.Used), model.HumanBytes(s.Memory.Total),
				pressureStyle(s.Memory.Pressure).Render(s.Memory.Pressure.String())))
		}
		parts = append(parts, fmt.Sprintf("↓ %s  ↑ %s", model.HumanRate(s.Net.Rx), model.HumanRate(s.Net.Tx)))
		if s.DiskTotal > 0 {
			style := good
			if s.DiskFree < 60<<30 {
				style = warn
			}
			parts = append(parts, "disk "+style.Render(model.HumanBytes(s.DiskFree)+" free"))
		}
	}
	if d := m.disk; d.walking && d.tree != nil {
		parts = append(parts, faint.Render("walking "+model.HumanCount(d.tree.Progress().Files)+" files"))
	}
	if m.checks[model.ScreenDupes].scanning {
		parts = append(parts, faint.Render("finding copies"))
	}
	if m.paused {
		parts = append(parts, warn.Render("paused"))
	}
	return strings.Join(parts, sep)
}

func (m Model) tabLine() string {
	var b strings.Builder
	b.WriteString(" ")
	for i, s := range m.screens {
		name := s.name()
		if sn, ok := s.(shortNamer); ok && m.width < 84 { // the whole line is 83 wide with every full name
			name = sn.shortName()
		}
		label := fmt.Sprintf(" %d %s ", i, name)
		if i == m.tab {
			b.WriteString(activeTab.Render(label))
		} else {
			b.WriteString(label)
		}
	}
	for i, n := range laterScreens {
		b.WriteString(faint.Render(fmt.Sprintf(" %d %s ", len(m.screens)+i, n)))
	}
	return b.String()
}

func (m Model) messageLine() string {
	switch {
	case m.confirm != nil:
		style := warn
		if m.confirm.danger {
			style = danger
		}
		return " " + style.Render(m.confirm.text)
	case m.editing:
		return " /" + m.filter + "▏"
	}
	return " " + m.msgStyle.Render(m.msg)
}

func (m Model) keyLine() string {
	if m.confirm != nil {
		line := " "
		if m.confirm.detail != "" {
			line += m.confirm.detail + "  "
		}
		return line + accent.Render("[y]") + " yes  " + faint.Render("[n]") + " no"
	}
	var parts []string
	hints := m.screens[m.tab].hints()
	if mh, ok := m.screens[m.tab].(modalHints); ok {
		hints = mh.hintsFor(&m)
	}
	for _, h := range hints {
		parts = append(parts, accent.Render(h.key)+" "+faint.Render(h.desc))
	}
	return " " + strings.Join(parts, "   ")
}

func helpLines() []string {
	keys := []hint{
		{"0-8", "switch screen"},
		{"j/k ↓/↑", "move"},
		{"/", "filter rows (esc clears)"},
		{"s", "change the sort"},
		{"p", "pause or resume refresh"},
		{"x / X", "kill or quit the selected row; X forces it"},
		{"enter", "open the selected line's tab (Home); open an app (Connections)"},
		{"l", "show loopback connections (Connections)"},
		{"g", "group by app or list processes (Memory)"},
		{"space", "tick or untick (Junk, Dev, Dupes, Apps); mark (Disk)"},
		{"k / K", "tick every copy but the kept one, in this set or in all (Dupes)"},
		{"a", "tick the safe items only (Junk, Dev)"},
		{"f", "show the selected app or file in Finder (Apps)"},
		{"c", "clean what's ticked or marked, after a preview"},
		{"h", "history; u puts a trashed item back"},
		{"r", "scan or walk again; on Home, scan Junk, Dev and Apps again"},
		{"⌫", "go up a folder; enter opens one (Disk)"},
		{".", "show or hide hidden files (Disk)"},
		{"esc", "stop a walk or a scan (Disk, Dupes)"},
		{"q", "quit soap"},
	}
	lines := []string{" " + accent.Render("Keys"), ""}
	for _, k := range keys {
		lines = append(lines, "   "+accent.Render(fit(k.key, 10))+" "+k.desc)
	}
	return append(lines, "", faint.Render(" Press any key to close this help."))
}

func sparkline(vals []float64) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		i := int(v/100*float64(len(bars)-1) + 0.5)
		b.WriteRune(bars[max(0, min(i, len(bars)-1))])
	}
	return b.String()
}

func pressureStyle(p model.Pressure) lipgloss.Style {
	switch p {
	case model.PressureNormal:
		return good
	case model.PressureWarning:
		return warn
	case model.PressureCritical:
		return danger
	}
	return faint
}
