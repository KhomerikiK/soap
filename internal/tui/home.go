package tui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

// homeState is what Home remembers. It's held by pointer, like checkState.
type homeState struct {
	view   homeView // built by refreshHome when data arrives
	cursor string   // the key of the selected line; once the user moves, the cursor follows it
	index  int      // its place in homeKeys, kept when its line goes away
	moved  bool     // the user moved the cursor; until then it stays on the first line
	start  int      // the first finding shown
	frame  int      // the spinner's frame

	spinning bool // a spinner tick is on its way; only one ever is

	history     []model.HistoryEntry // newest first
	historyErr  error
	historyRead bool
}

// homeHistoryMsg is the history, read for Home.
type homeHistoryMsg struct {
	entries []model.HistoryEntry
	err     error
}

// lightScans start when soap starts, and r on Home starts them again. The Disk walk and the
// Dupes scan aren't among them: no clean-up starts while those run, so only their tabs start them.
var lightScans = []model.Screen{model.ScreenJunk, model.ScreenDev, model.ScreenApps}

// startLightScans starts the Junk, Dev and Apps scans, each one cancelling its scan that runs.
func (m *Model) startLightScans() tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range lightScans {
		cmds = append(cmds, m.startScan(s))
	}
	return tea.Batch(cmds...)
}

// readHistory reads the history for Home in the background: at launch, and after each clean-up
// and put-back.
func (m *Model) readHistory() tea.Cmd {
	if m.deps.History == nil {
		return nil
	}
	load := m.deps.History
	return func() tea.Msg {
		entries, err := load()
		return homeHistoryMsg{entries: entries, err: err}
	}
}

// homeScreen is tab 0: the state of the Mac, what each clean-up tab can free, and what needs the
// user. It draws its own box and changes nothing.
type homeScreen struct{}

func (homeScreen) name() string             { return "Home" }
func (homeScreen) columns() []column        { return nil }
func (homeScreen) sortModes() []string      { return []string{"none"} }
func (homeScreen) problem(*Model) string    { return "" }
func (homeScreen) preamble(*Model) []string { return nil }
func (homeScreen) rows(*Model) []row        { return nil }
func (homeScreen) empty(*Model) string      { return "" }

func (homeScreen) hints() []hint {
	return []hint{{"j/k", "move"}, {"enter", "open"}, {"r", "scan again"}, {"p", "pause"}, {"?", "help"}, {"q", "quit"}}
}

// ownsKey takes the keys Home handles itself from the frame. The filter, the sort and esc do
// nothing on Home.
func (homeScreen) ownsKey(_ *Model, key string) bool {
	switch key {
	case "j", "k", "down", "up", "enter", "r", "/", "s", "esc":
		return true
	}
	return false
}

func (homeScreen) key(m *Model, key string, _ *row) (tea.Cmd, bool) { return m.homeKey(key), true }

func (homeScreen) onShow(m *Model) tea.Cmd {
	m.refreshHome()
	return nil
}

func (homeScreen) draw(m *Model, width, height int) []string {
	hs := m.home
	return drawHome(hs.view, width, height, homeSel{cursor: hs.cursor, start: hs.start, frame: hs.frame})
}

// refreshHome builds what Home shows from the other tabs' state, and keeps the cursor on its line.
// The Update loop calls it after every message while Home is open, so View only draws.
func (m *Model) refreshHome() {
	hs := m.home
	hs.view = buildHomeView(m.homeInput())
	keys := m.homeKeys()
	i := slices.Index(keys, hs.cursor)
	switch {
	case !hs.moved:
		i = 0 // findings arrive after the first draw; the worst one stays on top
	case i < 0:
		i = min(hs.index, len(keys)-1)
	}
	hs.index, hs.cursor = i, keys[i]
	cur := -1
	if i < len(hs.view.findings) {
		cur = i
	}
	rows := model.HomeRows(m.height-4, len(hs.view.findings)).Attention
	hs.start = model.AttentionStart(hs.start, cur, rows, len(hs.view.findings))
}

// homeKeys are the lines j and k move through, in order: the findings, the five "What you can
// free" lines, Top memory and Network when the window has room for them, then Ports and History.
func (m *Model) homeKeys() []string {
	var keys []string
	for _, f := range m.home.view.findings {
		keys = append(keys, f.Key)
	}
	for _, l := range m.home.view.free {
		keys = append(keys, l.Key)
	}
	if model.HomeRows(m.height-4, len(m.home.view.findings)).Extra > 0 {
		keys = append(keys, keyTop, keyNetwork)
	}
	return append(keys, keyPorts, keyHistory)
}

// homeInput is Home's view of the state the other tabs keep: the same items, notes and errors
// that tabs 4, 5, 7 and 8 show, so Home's numbers are theirs.
func (m *Model) homeInput() model.HomeInput {
	s := m.snap
	in := model.HomeInput{
		CPU: s.CPU, CPUHistory: m.cpuHist, Memory: s.Memory, MemoryErr: s.MemoryErr, Top: s.Apps, ProcessErr: s.ProcessErr,
		Listeners: s.Listeners, Groups: s.Groups, SocketErr: s.SocketErr, Net: s.Net, DiskFree: s.DiskFree, DiskTotal: s.DiskTotal,
		Junk: m.scanState(model.ScreenJunk), Dev: m.scanState(model.ScreenDev), Apps: m.scanState(model.ScreenApps),
		Dupes: m.scanState(model.ScreenDupes), Disk: m.diskSummary(),
		History: m.home.history, HistoryErr: m.home.historyErr, HistoryAvailable: m.deps.History != nil, HistoryRead: m.home.historyRead,
	}
	in.NoFullDiskAccess = model.LacksFullDiskAccess(in.Junk.Notes)
	return in
}

func (m *Model) scanState(s model.Screen) model.ScanState {
	cs := m.checks[s]
	var notes []model.Note
	for _, ns := range cs.notes {
		notes = append(notes, ns...)
	}
	return model.ScanState{Available: m.deps.Scan != nil, Started: cs.started, Scanning: cs.scanning, Done: cs.done, Total: cs.total,
		Progress: cs.progress, Items: cs.list(), Notes: notes, Errs: cs.errs}
}

// diskSummary is the Disk walk as Home shows it: the home folder's size and its largest folder.
func (m *Model) diskSummary() model.DiskState {
	d := m.disk
	st := model.DiskState{Available: m.deps.Walk != nil && m.deps.Home != "", Started: d.started, Walking: d.walking}
	if d.tree == nil {
		return st
	}
	p := d.tree.Progress()
	st.Files, st.Stopped = p.Files, p.Stopped
	if e, ok := d.tree.Entry(d.tree.Root); ok {
		st.Size, st.SizeMin = e.Size, e.Min
	}
	for _, k := range d.tree.Children(d.tree.Root) {
		if k.Size > st.BiggestSize {
			st.Biggest, st.BiggestSize = k.Name, k.Size
		}
	}
	return st
}

// homeKey moves the cursor through Home's lines, opens the selected line's tab, or scans again.
func (m *Model) homeKey(key string) tea.Cmd {
	hs := m.home
	keys := m.homeKeys()
	switch key {
	case "j", "down":
		hs.index, hs.moved = min(hs.index+1, len(keys)-1), true
	case "k", "up":
		hs.index, hs.moved = max(hs.index-1, 0), true
	case "enter":
		return m.homeEnter(keys[hs.index])
	case "r":
		return m.homeRescan()
	default:
		return nil
	}
	hs.cursor = keys[hs.index]
	return nil
}

// homeEnter opens the tab that acts on the selected line.
func (m *Model) homeEnter(key string) tea.Cmd {
	for _, f := range m.home.view.findings {
		if f.Key == key {
			return m.jump(f.Tab, f.Group, f.Port)
		}
	}
	for _, l := range m.home.view.free {
		if l.Key == key {
			return m.jump(l.Tab, "", 0)
		}
	}
	switch key {
	case keyTop:
		return m.jump(model.TabMemory, "", 0)
	case keyNetwork:
		return m.jump(model.TabConnections, "", 0)
	case keyPorts:
		return m.jump(model.TabPorts, "", 0)
	case keyHistory:
		cmd := m.jump(model.TabJunk, "", 0)
		if m.checks[model.ScreenJunk].mode == modeCleaning {
			return cmd // the clean-up's list stays; h shows the history after it
		}
		return tea.Batch(cmd, m.openHistory(model.ScreenJunk)) // it lists every tab's clean-ups
	}
	return nil
}

// jump opens a tab, with the cursor on a group or a port when one is named. A tab of -1 opens
// nothing: the line says what to do instead.
func (m *Model) jump(tab int, group string, port uint16) tea.Cmd {
	if tab < 0 {
		return nil
	}
	cmd := m.switchTab(tab)
	if f, ok := m.screens[tab].(focuser); ok && group != "" {
		f.focus(m, group)
	}
	if port != 0 && tab == model.TabPorts {
		m.focusPort(port)
	}
	return cmd
}

// homeRescan is r on Home: the Junk, Dev and Apps scans start again, unless a clean-up runs.
func (m *Model) homeRescan() tea.Cmd {
	switch {
	case m.cleaning():
		m.setMsg("A clean-up is running. Scan again when it's done.", warn)
		return nil
	case m.deps.Scan == nil:
		return nil
	}
	m.setMsg("Scanning Junk, Dev and Apps again…", faint)
	return m.startLightScans()
}

// spinMsg moves the spinner on by one frame.
type spinMsg struct{}

const spinEvery = 120 * time.Millisecond

// homeBusy reports whether a scan, the Disk walk or the Dupes scan runs, which Home shows with the
// spinner.
func (m *Model) homeBusy() bool {
	if m.disk.walking {
		return true
	}
	for _, cs := range m.checks {
		if cs.scanning {
			return true
		}
	}
	return false
}

// spin starts the spinner's next tick while Home is open and something runs. Otherwise Home
// redraws only with the snapshots.
func (m *Model) spin() tea.Cmd {
	hs := m.home
	if hs.spinning || m.tab != model.TabHome || !m.homeBusy() {
		return nil
	}
	hs.spinning = true
	return tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
}
