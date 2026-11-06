package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

type (
	scanMsg struct {
		screen model.Screen
		gen    int
		update model.ScanUpdate
		ch     <-chan model.ScanUpdate
	}
	scanDoneMsg struct {
		screen model.Screen
		gen    int
	}
	cleanStartMsg struct{ screen model.Screen }
	cleanEventMsg struct {
		screen model.Screen
		ev     cleanEvent
		ch     <-chan cleanEvent
	}
	historyMsg struct {
		screen  model.Screen
		entries []model.HistoryEntry
		err     error
	}
	restoredMsg struct {
		screen model.Screen
		entry  model.HistoryEntry
		err    error
	}
)

// cleanEvent is one result from a clean-up in progress, or its end.
type cleanEvent struct {
	result *model.CleanResult
	err    error
	done   bool
}

// handleChecklist handles the Junk and Dev messages. ok is false for any other message.
func (m Model) handleChecklist(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case scanMsg:
		return m.onScan(msg)
	case scanDoneMsg:
		if cs := m.checks[msg.screen]; msg.gen == cs.gen {
			cs.scanning, cs.cancel = false, nil
		}
		return m, nil, true
	case cleanStartMsg:
		return m.onCleanStart(msg)
	case cleanEventMsg:
		return m.onCleanEvent(msg)
	case walkDoneMsg:
		if d := m.disk; msg.gen == d.gen {
			d.walking, d.cancel = false, nil
		}
		return m, nil, true
	case diskListMsg:
		m.onDiskList(msg)
		return m, nil, true
	case remeasuredMsg:
		if m.disk.measuring > 0 {
			m.disk.measuring--
		}
		if msg.gen != m.disk.gen {
			return m, nil, true
		}
		return m, m.listDir(m.disk.dir), true
	case historyMsg:
		if msg.err != nil {
			m.setMsg("Couldn't read the history: "+msg.err.Error(), danger)
			return m, nil, true
		}
		cs := m.checks[msg.screen]
		cs.history, cs.mode = msg.entries, modeHistory
		m.resetSelectionOf(msg.screen)
		return m, nil, true
	case restoredMsg:
		if msg.err != nil {
			m.setMsg(sentence(msg.err.Error())+".", danger)
			return m, nil, true
		}
		m.setMsg("Put back "+msg.entry.Name+".", good)
		return m, tea.Batch(m.openHistory(msg.screen), m.afterRestore(msg.screen, msg.entry), m.readHistory()), true
	case homeHistoryMsg:
		m.home.history, m.home.historyErr, m.home.historyRead = msg.entries, msg.err, true
		return m, nil, true
	}
	return m, nil, false
}

// rescan scans a screen again after a clean-up or a put-back, on whatever tab is open, because
// Home shows what the scans find.
func (m *Model) rescan(s model.Screen) tea.Cmd { return m.startScan(s) }

// isCurrent says whether a clean-up screen is the tab being shown.
func (m *Model) isCurrent(s model.Screen) bool {
	cur, ok := m.screens[m.tab].(cleanupScreen)
	return ok && cur.cleanupID() == s
}

// afterClean brings a screen up to date after a clean-up: Junk and Dev scan again, Disk measures
// the folders that changed, and Dupes drops what went to the Trash.
func (m *Model) afterClean(s model.Screen) tea.Cmd {
	switch s {
	case model.ScreenDisk:
		return m.diskAfterClean()
	case model.ScreenDupes:
		m.dupesAfterClean()
		return nil
	}
	return m.rescan(s)
}

// afterRestore brings a screen up to date after something was put back from its history. Dupes
// shows a copy that was put back after r.
func (m *Model) afterRestore(s model.Screen, e model.HistoryEntry) tea.Cmd {
	switch s {
	case model.ScreenDisk:
		return m.remeasure(parents(e.Paths))
	case model.ScreenDupes:
		return nil
	}
	return m.rescan(s)
}

func (m *Model) resetSelectionOf(s model.Screen) {
	if m.isCurrent(s) {
		m.resetSelection()
	}
}

// startScan starts a new scan of a screen, cancelling one already running.
func (m *Model) startScan(s model.Screen) tea.Cmd {
	cs := m.checks[s]
	if m.deps.Scan == nil {
		return nil
	}
	if cs.cancel != nil {
		cs.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cs.gen++
	cs.started, cs.scanning, cs.cancel, cs.done, cs.total, cs.progress = true, true, cancel, 0, 0, ""
	return waitScan(s, cs.gen, m.deps.Scan(ctx, s))
}

func waitScan(s model.Screen, gen int, ch <-chan model.ScanUpdate) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-ch
		if !ok {
			return scanDoneMsg{screen: s, gen: gen}
		}
		return scanMsg{screen: s, gen: gen, update: u, ch: ch}
	}
}

// onScan replaces one finder's items, or adds to them when the update says so. New items start
// ticked if they're safe; items seen before keep the user's choice, unless they can no longer be
// ticked.
func (m Model) onScan(msg scanMsg) (tea.Model, tea.Cmd, bool) {
	cs := m.checks[msg.screen]
	if msg.gen != cs.gen {
		// From an older scan: don't apply it, but keep reading so the sender never blocks.
		return m, waitScan(msg.screen, msg.gen, msg.ch), true
	}
	u := msg.update
	var before []string
	if !u.Add {
		for id, it := range cs.items {
			if it.Group == u.Finder {
				before = append(before, id)
				delete(cs.items, id)
			}
		}
		delete(cs.errs, u.Finder)
		cs.notes[u.Finder] = u.Notes
	} else if len(u.Notes) > 0 {
		cs.notes[u.Finder] = u.Notes
	}
	if u.Err != nil {
		cs.errs[u.Finder] = u.Err.Error()
	}
	if u.Status != "" {
		cs.progress = u.Status
	}
	for _, it := range u.Items {
		if _, seen := cs.ticked[it.ID]; !seen {
			cs.ticked[it.ID] = it.PreTicked()
		}
		if !it.Tickable() {
			cs.ticked[it.ID] = false
		}
		cs.items[it.ID] = it
	}
	for _, id := range before {
		if _, still := cs.items[id]; !still {
			delete(cs.ticked, id) // a tick for something the finder no longer reports is stale
		}
	}
	cs.done, cs.total = u.Done, u.Total
	if m.isCurrent(msg.screen) {
		m.clampSelection()
	}
	return m, waitScan(msg.screen, msg.gen, msg.ch), true
}

func (m Model) onCleanStart(msg cleanStartMsg) (tea.Model, tea.Cmd, bool) {
	cs := m.checks[msg.screen]
	if m.deps.Clean == nil {
		cs.mode = modeList
		return m, nil, true
	}
	if job := m.backgroundJob(); job != "" { // started after the question was asked
		cs.mode = modeList
		m.setMsg(waitText(job), warn)
		return m, nil, true
	}
	if cs.cancel != nil { // scans pause while cleaning
		cs.cancel()
		cs.cancel, cs.scanning = nil, false
		cs.gen++ // updates the paused scan already sent are drained as stale, so a cleaned item can't come back ticked
	}
	ctx, cancel := context.WithCancel(context.Background())
	cs.mode, cs.cleanCancel, cs.status, cs.freed = modeCleaning, cancel, map[string]model.CleanResult{}, 0
	clean, items, s := m.deps.Clean, cs.queue, msg.screen
	ch := make(chan cleanEvent, len(items)+1)
	m.setMsg(fmt.Sprintf("Cleaning 1 of %d…", len(items)), faint)
	return m, func() tea.Msg {
		go func() {
			err := clean(ctx, items, func(r model.CleanResult) { ch <- cleanEvent{result: &r} })
			ch <- cleanEvent{err: err, done: true}
			close(ch)
		}()
		return waitClean(s, ch)()
	}, true
}

func waitClean(s model.Screen, ch <-chan cleanEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return cleanEventMsg{screen: s, ev: ev, ch: ch}
	}
}

func (m Model) onCleanEvent(msg cleanEventMsg) (tea.Model, tea.Cmd, bool) {
	cs := m.checks[msg.screen]
	if r := msg.ev.result; r != nil {
		cs.status[r.Item.ID] = *r
		if r.Result == "done" || r.Result == "partial" {
			delete(cs.ticked, r.Item.ID) // a cleaned item mustn't come back ticked after the rescan
		}
		cs.freed += r.Freed
		if n := len(cs.status); n < len(cs.queue) {
			m.setMsg(fmt.Sprintf("Cleaning %d of %d… %s freed so far", n+1, len(cs.queue), model.HumanBytes(cs.freed)), faint)
		}
		return m, waitClean(msg.screen, msg.ch), true
	}
	cs.mode, cs.cleanCancel = modeList, nil
	m.resetSelectionOf(msg.screen)
	m.cleanSummary(cs, msg.ev.err)
	return m, tea.Batch(m.afterClean(msg.screen), m.readHistory()), true
}

// cleanSummary puts the result of a clean-up on the message line.
func (m *Model) cleanSummary(cs *checkState, err error) {
	failedRun := err != nil && !errors.Is(err, context.Canceled)
	var failed, skipped, partial []string
	trashed := 0
	for _, it := range cs.queue {
		r, ok := cs.status[it.ID]
		if !ok {
			continue
		}
		switch r.Result {
		case "failed":
			failed = append(failed, it.Name+" ("+r.Message+")")
		case "skipped":
			skipped = append(skipped, it.Name+" ("+r.Message+")")
		case "partial":
			partial = append(partial, it.Name+" ("+r.Message+")")
		case "done":
			if it.Method == model.MethodTrash {
				trashed++
			}
		}
	}
	text := "Freed " + model.HumanBytes(cs.freed) + "."
	if errors.Is(err, context.Canceled) {
		text = "Stopped. " + text
	}
	if trashed > 0 {
		text += fmt.Sprintf(" Moved %d to the Trash; press h to put it back.", trashed)
	}
	if len(partial) > 0 {
		text += fmt.Sprintf(" %d partly deleted: %s.", len(partial), strings.Join(partial, ", "))
	}
	if len(failed) > 0 {
		text += fmt.Sprintf(" %d failed: %s.", len(failed), strings.Join(failed, ", "))
	}
	if len(skipped) > 0 {
		text += fmt.Sprintf(" %d skipped: %s.", len(skipped), strings.Join(skipped, ", "))
	}
	style := good
	if len(failed)+len(partial) > 0 {
		style = warn
	}
	if failedRun {
		text += " " + sentence(err.Error()) + "."
		style = danger
	}
	m.setMsg(text, style)
}

func (m *Model) openHistory(s model.Screen) tea.Cmd {
	if m.deps.History == nil {
		return nil
	}
	load := m.deps.History
	return func() tea.Msg {
		entries, err := load()
		return historyMsg{screen: s, entries: entries, err: err}
	}
}

func (m *Model) restoreCmd(s model.Screen, e model.HistoryEntry) tea.Cmd {
	if !e.CanRestore() || m.deps.Restore == nil {
		m.setMsg("Only items moved to the Trash can be put back.", faint)
		return nil
	}
	restore := m.deps.Restore
	return func() tea.Msg {
		got, err := restore(e.ID)
		return restoredMsg{screen: s, entry: got, err: err}
	}
}

// sentence starts s with a capital letter.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
