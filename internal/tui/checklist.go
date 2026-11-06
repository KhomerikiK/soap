package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

type checkMode int

const (
	modeList checkMode = iota
	modePreview
	modeCleaning
	modeHistory
)

// checkState is what a checklist screen remembers. It's held by pointer, so every copy of the
// Model sees the same state.
type checkState struct {
	started  bool
	scanning bool
	gen      int // bumped by every scan, so updates from an older one are dropped
	cancel   func()
	done     int
	total    int
	progress string // the latest progress line from a streaming finder

	items  map[string]model.CleanItem
	notes  map[string][]model.Note // by finder
	errs   map[string]string       // by group
	ticked map[string]bool
	open   map[string]bool
	closed map[string]bool // Dupes sets the user closed; sets start open

	mode        checkMode
	queue       []model.CleanItem
	status      map[string]model.CleanResult
	freed       uint64
	cleanCancel func()
	history     []model.HistoryEntry
}

func newCheckState() *checkState {
	return &checkState{
		items: map[string]model.CleanItem{}, notes: map[string][]model.Note{}, errs: map[string]string{},
		ticked: map[string]bool{}, open: map[string]bool{}, closed: map[string]bool{}, status: map[string]model.CleanResult{},
	}
}

func (cs *checkState) list() []model.CleanItem {
	out := make([]model.CleanItem, 0, len(cs.items))
	for _, it := range cs.items {
		out = append(out, it)
	}
	return out
}

func (cs *checkState) groups(byName bool) []model.CleanGroup {
	var notes []model.Note
	for _, ns := range cs.notes {
		notes = append(notes, ns...)
	}
	return model.GroupItems(cs.list(), notes, cs.errs, byName)
}

// tickedItems are the ticked, tickable items in the order the screen shows them.
func (cs *checkState) tickedItems(byName bool) []model.CleanItem {
	var out []model.CleanItem
	for _, g := range cs.groups(byName) {
		for _, it := range g.Items {
			if cs.ticked[it.ID] && it.Tickable() {
				out = append(out, it)
			}
		}
	}
	return out
}

func (cs *checkState) screenNotes() []string {
	var out []string
	for _, ns := range cs.notes {
		for _, n := range ns {
			if n.Group == "" {
				out = append(out, n.Text)
			}
		}
	}
	sort.Strings(out)
	return out
}

// checklistScreen is a grouped checklist of things to clean. Junk and Dev are both one.
type checklistScreen struct {
	id    model.Screen
	title string
}

var (
	junkScreen = checklistScreen{id: model.ScreenJunk, title: "Junk"}
	devScreen  = checklistScreen{id: model.ScreenDev, title: "Dev"}
)

// groupRef is the item behind a group's row.
type groupRef struct{ name string }

// modalHints is a screen whose key hints depend on what it's doing.
type modalHints interface{ hintsFor(m *Model) []hint }

// escaper is a screen that uses esc itself, before the frame clears the filter.
type escaper interface{ esc(m *Model) bool }

// cleanupScreen is a screen that removes things through the shared clean-up flow: a preview, the
// clean-up and history, kept in m.checks under its ID.
type cleanupScreen interface{ cleanupID() model.Screen }

// focuser is a screen that Home can open with the cursor on one of its groups: Junk, Dev and Apps.
type focuser interface{ focus(m *Model, group string) }

// focus opens group and puts the cursor on its row, or at the top when the group is gone. An open
// history list closes, so the group shows.
func (s checklistScreen) focus(m *Model, group string) {
	cs := m.checks[s.id]
	if cs.mode == modeHistory {
		cs.mode = modeList
	}
	was := cs.open[group]
	cs.open[group] = true
	if !m.focusRow(func(item any) bool { g, ok := item.(groupRef); return ok && g.name == group }) {
		cs.open[group] = was
	}
}

// focusRow puts the cursor on the first row of the current tab whose item matches, or at the top
// when none does, and reports whether one did.
func (m *Model) focusRow(match func(item any) bool) bool {
	for i, r := range m.visibleRows() {
		if match(r.item) {
			m.sel[m.tab] = i
			return true
		}
	}
	m.sel[m.tab] = 0
	return false
}

func (s checklistScreen) cleanupID() model.Screen { return s.id }
func (s checklistScreen) name() string            { return s.title }
func (s checklistScreen) columns() []column       { return []column{{"ITEM", 46}, {"SIZE", 10}, {"", 0}} }
func (s checklistScreen) sortModes() []string     { return []string{"size", "name"} }
func (s checklistScreen) hints() []hint           { return nil }
func (s checklistScreen) problem(*Model) string   { return "" }

func (s checklistScreen) empty(m *Model) string {
	cs := m.checks[s.id]
	switch {
	case cs.mode == modeHistory:
		return "Nothing cleaned yet."
	case cs.scanning || !cs.started:
		return "Scanning…"
	}
	return "Nothing to clean here."
}

// flowHints are the key hints while a screen cleans or shows history. ok is false otherwise.
func flowHints(cs *checkState) (hints []hint, ok bool) {
	switch cs.mode {
	case modeCleaning:
		return []hint{{"esc", "stop after this item"}}, true
	case modeHistory:
		return []hint{{"j/k", "move"}, {"u", "put back"}, {"esc", "back"}}, true
	}
	return nil, false
}

func (s checklistScreen) hintsFor(m *Model) []hint {
	if hints, ok := flowHints(m.checks[s.id]); ok {
		return hints
	}
	return []hint{{"j/k", "move"}, {"space", "tick"}, {"enter", "open"}, {"a", "safe only"}, {"c", "clean"}, {"h", "history"}, {"r", "rescan"}, {"q", "quit"}}
}

func (s checklistScreen) onShow(m *Model) tea.Cmd {
	if m.checks[s.id].started {
		return nil
	}
	return m.startScan(s.id)
}

func (s checklistScreen) esc(m *Model) bool { return m.flowEsc(s.id) }

// flowEsc handles esc while a screen cleans or shows history, and reports whether it did.
func (m *Model) flowEsc(s model.Screen) bool {
	cs := m.checks[s]
	switch cs.mode {
	case modeCleaning:
		if cs.cleanCancel != nil {
			cs.cleanCancel()
		}
		m.setMsg("Stopping after this item…", warn)
		return true
	case modeHistory:
		cs.mode = modeList
		m.resetSelection()
		return true
	}
	return false
}

// flowPreamble is the title line while a screen previews, cleans or shows history. ok is false
// otherwise.
func flowPreamble(cs *checkState) (lines []string, ok bool) {
	switch cs.mode {
	case modePreview, modeCleaning:
		title := "Clean-up preview"
		if cs.mode == modeCleaning {
			title = "Cleaning"
		}
		return []string{" " + accent.Render(title) + faint.Render(fmt.Sprintf("  %d items · %s", len(cs.queue), model.HumanBytes(model.TotalSize(cs.queue))))}, true
	case modeHistory:
		return []string{" " + accent.Render("History") + faint.Render(fmt.Sprintf("  %d entries", len(cs.history)))}, true
	}
	return nil, false
}

// withICloudWarning adds the iCloud Drive line to the preview's title when any item about to be
// removed is inside iCloud Drive.
func withICloudWarning(home string, cs *checkState, lines []string) []string {
	if cs.mode != modePreview {
		return lines
	}
	for _, it := range cs.queue {
		for _, p := range it.Paths {
			if model.InICloudDrive(home, p) {
				return append(lines, " "+warn.Render(model.ICloudWarning))
			}
		}
	}
	return lines
}

func (s checklistScreen) preamble(m *Model) []string {
	cs := m.checks[s.id]
	if lines, ok := flowPreamble(cs); ok {
		return lines
	}
	byName := m.sortIdx[m.tab] == 1
	lines := []string{" " + accent.Render(s.title) + faint.Render(fmt.Sprintf("  found %s · %s ticked",
		model.HumanBytes(model.TotalSize(cs.list())), model.HumanBytes(model.TotalSize(cs.tickedItems(byName)))))}
	switch {
	case cs.scanning && cs.total > 0:
		lines = append(lines, faint.Render(fmt.Sprintf(" Scanning… %d of %d checks done", cs.done, cs.total)))
	case cs.scanning:
		lines = append(lines, faint.Render(" Scanning…"))
	}
	for _, n := range cs.screenNotes() {
		lines = append(lines, " "+warn.Render(oneLine(n)))
	}
	return lines
}

// flowRows are a screen's rows while it previews, cleans or shows history. ok is false otherwise.
func flowRows(cs *checkState) (rows []row, ok bool) {
	switch cs.mode {
	case modePreview, modeCleaning:
		return queueRows(cs), true
	case modeHistory:
		return historyRows(cs), true
	}
	return nil, false
}

func (s checklistScreen) rows(m *Model) []row {
	cs := m.checks[s.id]
	if rows, ok := flowRows(cs); ok {
		return rows
	}
	var rows []row
	for _, g := range cs.groups(m.sortIdx[m.tab] == 1) {
		names := []string{g.Name}
		for _, it := range g.Items {
			names = append(names, it.Name)
		}
		match := strings.Join(names, " ")
		arrow := "▸"
		if cs.open[g.Name] {
			arrow = "▾"
		}
		count := ""
		switch n := len(g.Items); {
		case n == 1:
			count = "1 item"
		case n > 1:
			count = fmt.Sprintf("%d items", n)
		}
		ref := groupRef{g.Name}
		rows = append(rows, row{cells: []string{arrow + " " + tickBox(model.GroupTick(g.Items, cs.ticked)) + " " + g.Name, model.HumanBytes(g.Size), count},
			styles: []lipgloss.Style{plain, plain, faint}, match: match, item: ref})
		if g.Err != "" {
			rows = append(rows, row{text: "      " + oneLine("Couldn't check: "+g.Err), textStyle: danger, match: match, item: ref})
		}
		if cs.open[g.Name] {
			for _, it := range g.Items {
				box := "[ ]"
				if cs.ticked[it.ID] && it.Tickable() {
					box = "[x]"
				}
				detail, style := it.Detail, faint
				if r := it.Reason(); r != "" {
					detail, style = r, warn
				}
				rows = append(rows, row{cells: []string{"    " + box + " " + it.Name, sizeText(it), detail},
					styles: []lipgloss.Style{plain, plain, style}, match: match, item: it})
			}
		}
		for _, n := range g.Notes {
			rows = append(rows, row{text: "      " + n, textStyle: faint, match: match, item: ref})
		}
	}
	return rows
}

func queueRows(cs *checkState) []row {
	var rows []row
	current := true // the first item without a result is the one being cleaned
	for _, it := range cs.queue {
		mark, how := "  ", it.How()
		if cs.mode == modeCleaning {
			if r, ok := cs.status[it.ID]; ok {
				mark = statusMark(r.Result)
				if r.Message != "" {
					how = r.Message
				}
			} else if current {
				mark, current = "⋯ ", false
			}
		}
		rows = append(rows, row{cells: []string{mark + it.Name, sizeText(it), how}, styles: []lipgloss.Style{plain, plain, faint}, item: it})
	}
	return rows
}

func historyRows(cs *checkState) []row {
	var rows []row
	for _, e := range cs.history {
		style := faint
		if e.CanRestore() {
			style = warn
		}
		rows = append(rows, row{cells: []string{e.Time.Local().Format("2 Jan 15:04") + "  " + e.Name, model.HumanBytes(e.Size), e.State()},
			styles: []lipgloss.Style{plain, plain, style}, item: e})
	}
	return rows
}

func statusMark(result model.Result) string {
	switch result {
	case "done":
		return "✓ "
	case "partial":
		return "~ "
	case "skipped":
		return "– "
	}
	return "✗ "
}

func tickBox(s model.TickState) string {
	switch s {
	case model.TickAll:
		return "[x]"
	case model.TickSome:
		return "[-]"
	}
	return "[ ]"
}

func sizeText(it model.CleanItem) string {
	if it.SizeMin {
		return "≥ " + model.HumanBytes(it.Size)
	}
	return model.HumanBytes(it.Size)
}

// flowKey handles a key while a screen previews, cleans or shows history. inFlow is false in
// the screen's own list, where the screen handles the key itself.
func (m *Model) flowKey(s model.Screen, key string, r *row) (cmd tea.Cmd, handled, inFlow bool) {
	cs := m.checks[s]
	switch cs.mode {
	case modePreview, modeCleaning:
		return nil, true, true // the preview is answered through the confirmation; esc stops cleaning
	case modeHistory:
		switch key {
		case "h":
			cs.mode = modeList
			m.resetSelection()
			return nil, true, true
		case "u":
			if r == nil {
				return nil, true, true
			}
			e, _ := r.item.(model.HistoryEntry)
			return m.restoreCmd(s, e), true, true
		}
		return nil, false, true
	}
	return nil, false, false
}

func (s checklistScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	cs := m.checks[s.id]
	if cmd, handled, inFlow := m.flowKey(s.id, key, r); inFlow {
		return cmd, handled
	}
	switch key {
	case "space":
		if r != nil {
			m.toggle(s.id, r.item)
		}
	case "enter":
		if r != nil {
			if g, ok := r.item.(groupRef); ok {
				cs.open[g.name] = !cs.open[g.name]
			}
		}
	case "a":
		clear(cs.ticked)
		for id, it := range cs.items {
			cs.ticked[id] = it.PreTicked()
		}
		m.setMsg("Ticked the safe items only.", faint)
	case "c":
		return m.askClean(s.id), true
	case "h":
		return m.openHistory(s.id), true
	case "r":
		return m.startScan(s.id), true
	default:
		return nil, false
	}
	return nil, true
}

// toggle ticks or unticks an item, or every tickable item in a group.
func (m *Model) toggle(s model.Screen, item any) {
	cs := m.checks[s]
	switch v := item.(type) {
	case model.CleanItem:
		if !v.Tickable() {
			m.setMsg(v.Reason()+".", warn)
			return
		}
		cs.ticked[v.ID] = !cs.ticked[v.ID]
	case groupRef:
		var tickable []model.CleanItem
		for _, it := range cs.items {
			if it.Group == v.name && it.Tickable() {
				tickable = append(tickable, it)
			}
		}
		on := model.GroupTick(tickable, cs.ticked) != model.TickAll
		for _, it := range tickable {
			cs.ticked[it.ID] = on
		}
	}
}

// askClean switches to the preview and asks the one question before anything is cleaned.
func (m *Model) askClean(s model.Screen) tea.Cmd {
	if m.cleaning() {
		m.setMsg("A clean-up is already running.", warn)
		return nil
	}
	if job := m.backgroundJob(); job != "" {
		m.setMsg(waitText(job), warn)
		return nil
	}
	cs := m.checks[s]
	items := model.AppsFirst(cs.tickedItems(m.sortIdx[m.tab] == 1)) // as the Cleaner orders them
	if len(items) == 0 {
		m.setMsg("Nothing is ticked.", warn)
		return nil
	}
	if whole := model.WholeSets(items); len(whole) > 0 {
		m.setMsg(model.KeepMessage(whole[0]), warn)
		return nil
	}
	cs.mode, cs.queue = modePreview, items
	m.resetSelection()
	m.confirm = &confirmation{
		text:    model.Summary(items),
		working: "Cleaning…",
		yes:     func() tea.Msg { return cleanStartMsg{screen: s} },
		cancel: func(m *Model) {
			m.checks[s].mode = modeList
			m.resetSelection()
		},
	}
	return nil
}

// waitText is the message for a clean-up refused because a background job runs. Only the walk can
// be stopped with esc.
func waitText(job string) string {
	if job == diskMeasuring {
		return "Wait for " + job + " to finish."
	}
	return "Wait for " + job + " to finish, or press esc on its tab to stop it."
}

const diskMeasuring = "the Disk size update"

// cleaning reports whether a clean-up runs on any screen.
func (m *Model) cleaning() bool {
	for _, cs := range m.checks {
		if cs.mode == modeCleaning {
			return true
		}
	}
	return false
}

// backgroundJob names the long job that is running, the Disk walk or the Dupes scan, or is "".
// No clean-up starts while one runs.
func (m *Model) backgroundJob() string {
	if m.disk.walking {
		return "the Disk walk"
	}
	if m.disk.measuring > 0 {
		return diskMeasuring
	}
	if m.checks[model.ScreenDupes].scanning {
		return "the Dupes scan"
	}
	return ""
}
