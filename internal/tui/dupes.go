package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

// dupesScreen lists sets of identical files, one group per set, on part 2's checklist state.
// Nothing is ticked until the user asks.
type dupesScreen struct{}

// setRef is the set behind a set's row.
type setRef struct{ set model.DupeSet }

func (dupesScreen) name() string            { return "Dupes" }
func (dupesScreen) cleanupID() model.Screen { return model.ScreenDupes }
func (dupesScreen) columns() []column       { return []column{{"COPY", 56}, {"", 0}} }
func (dupesScreen) sortModes() []string     { return []string{"frees", "name"} }
func (dupesScreen) hints() []hint           { return nil }
func (dupesScreen) problem(*Model) string   { return "" }

// ownsKey takes k from the frame in the list, where it ticks a set instead of moving up.
func (dupesScreen) ownsKey(m *Model, key string) bool {
	return key == "k" && m.checks[model.ScreenDupes].mode == modeList
}

func (dupesScreen) hintsFor(m *Model) []hint {
	cs := m.checks[model.ScreenDupes]
	if hints, ok := flowHints(cs); ok {
		return hints
	}
	hints := []hint{{"↑/↓", "move"}, {"space", "tick"}, {"k", "all but kept"}, {"K", "every set"}, {"enter", "open"}, {"c", "move to Trash"}, {"h", "history"}, {"r", "scan again"}}
	if cs.scanning {
		hints = append(hints, hint{"esc", "stop"})
	}
	return hints
}

func (dupesScreen) onShow(m *Model) tea.Cmd {
	if m.checks[model.ScreenDupes].started {
		return nil
	}
	return m.startDupes()
}

func (dupesScreen) esc(m *Model) bool {
	if m.flowEsc(model.ScreenDupes) {
		return true
	}
	if cs := m.checks[model.ScreenDupes]; cs.scanning && cs.cancel != nil {
		cs.cancel()
		m.setMsg("Stopped looking for copies. The sets found so far stay; r looks again.", warn)
		return true
	}
	return false
}

func (dupesScreen) empty(m *Model) string {
	cs := m.checks[model.ScreenDupes]
	switch {
	case cs.mode == modeHistory:
		return "Nothing cleaned yet."
	case !cs.started && !cs.scanning && m.cleaning():
		return "The scan starts when the clean-up is done. Press r then."
	case cs.scanning || !cs.started:
		return "Looking for copies…"
	}
	return "No copies found."
}

func (dupesScreen) preamble(m *Model) []string {
	cs := m.checks[model.ScreenDupes]
	if lines, ok := flowPreamble(cs); ok {
		return withICloudWarning(m.deps.Home, cs, lines)
	}
	sets := model.DupeSets(cs.list(), false)
	var frees, ticked uint64
	for _, s := range sets {
		frees += s.Frees
		for _, it := range s.Items {
			if cs.ticked[it.ID] {
				ticked += it.Unshared
			}
		}
	}
	count := "1 set"
	if len(sets) != 1 {
		count = fmt.Sprintf("%d sets", len(sets))
	}
	lines := []string{" " + accent.Render("Dupes") + faint.Render(fmt.Sprintf("  %s · frees up to %s · ticked copies free %s", count, model.HumanBytes(frees), model.HumanBytes(ticked)))}
	if cs.scanning {
		progress := cs.progress
		if progress == "" {
			progress = "Looking for copies…"
		}
		lines = append(lines, faint.Render(" "+progress))
	}
	for _, n := range cs.screenNotes() {
		lines = append(lines, " "+warn.Render(oneLine(n)))
	}
	groups := make([]string, 0, len(cs.errs))
	for g := range cs.errs {
		groups = append(groups, g)
	}
	slices.Sort(groups)
	for _, g := range groups {
		lines = append(lines, " "+danger.Render(oneLine("Couldn't check "+g+": "+cs.errs[g])))
	}
	return lines
}

func (dupesScreen) rows(m *Model) []row {
	cs := m.checks[model.ScreenDupes]
	if rows, ok := flowRows(cs); ok {
		return rows
	}
	var rows []row
	for _, set := range model.DupeSets(cs.list(), m.sortIdx[m.tab] == 1) {
		names := []string{set.Name}
		ticked := 0
		for _, it := range set.Items {
			names = append(names, it.Name)
			if cs.ticked[it.ID] {
				ticked++
			}
		}
		match := strings.Join(names, " ")
		arrow := "▾"
		if cs.closed[set.ID] {
			arrow = "▸"
		}
		info := fmt.Sprintf("%d copies · %s each · frees up to %s", len(set.Items), model.HumanBytes(set.Size), model.HumanBytes(set.Frees))
		if ticked > 0 {
			info += fmt.Sprintf(" · %d ticked", ticked)
		}
		rows = append(rows, row{cells: []string{arrow + " " + set.Name, info}, styles: []lipgloss.Style{plain, faint}, match: match, item: setRef{set}})
		if cs.closed[set.ID] {
			continue
		}
		for _, it := range set.Items {
			box, keep := "[ ]", "    "
			if cs.ticked[it.ID] {
				box = "[x]"
			}
			if it.Keep {
				keep = "keep"
			}
			where := place(m.deps.Home, filepath.Dir(it.Paths[0]))
			if base := filepath.Base(it.Paths[0]); base != set.Name {
				where += "/" + base
			}
			rows = append(rows, row{cells: []string{"    " + box + " " + keep + "  " + where, it.Detail},
				styles: []lipgloss.Style{plain, faint}, match: match, item: it})
		}
	}
	return rows
}

func (dupesScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	s := model.ScreenDupes
	if cmd, handled, inFlow := m.flowKey(s, key, r); inFlow {
		return cmd, handled
	}
	cs := m.checks[s]
	var set *model.DupeSet
	var copyItem *model.CleanItem
	if r != nil {
		switch v := r.item.(type) {
		case setRef:
			set = &v.set
		case model.CleanItem:
			copyItem = &v
			for _, found := range model.DupeSets(cs.list(), false) {
				if found.ID == v.Set {
					set = &found
				}
			}
		}
	}
	switch key {
	case "space":
		switch {
		case copyItem != nil:
			m.tickCopy(*copyItem)
		case set != nil:
			tickSet(cs, *set, !anyTicked(cs, *set))
		}
	case "k":
		if set != nil {
			tickSet(cs, *set, true)
			m.setMsg("Ticked every copy of "+set.Name+" but the kept one.", faint)
		}
	case "K":
		sets := model.DupeSets(cs.list(), false)
		for _, one := range sets {
			tickSet(cs, one, true)
		}
		m.setMsg(fmt.Sprintf("Ticked every copy but the kept one, in %d sets.", len(sets)), faint)
	case "enter":
		if set != nil && copyItem == nil {
			cs.closed[set.ID] = !cs.closed[set.ID]
			m.clampSelection()
		}
	case "c":
		return m.askClean(s), true
	case "h":
		return m.openHistory(s), true
	case "r":
		return m.startDupes(), true
	default:
		return nil, false
	}
	return nil, true
}

// place shows a folder the way Finder names it: iCloud Drive by name, the home folder as "~".
func place(home, dir string) string {
	icloud := home + "/Library/Mobile Documents/com~apple~CloudDocs"
	if dir == icloud || strings.HasPrefix(dir, icloud+"/") {
		return "iCloud Drive" + strings.TrimPrefix(dir, icloud)
	}
	return model.HomePath(home, dir)
}

// tickSet ticks every copy of a set but the kept one, or unticks them all.
func tickSet(cs *checkState, set model.DupeSet, on bool) {
	for _, it := range set.Items {
		cs.ticked[it.ID] = on && !it.Keep
	}
}

func anyTicked(cs *checkState, set model.DupeSet) bool {
	for _, it := range set.Items {
		if cs.ticked[it.ID] {
			return true
		}
	}
	return false
}

// tickCopy ticks or unticks one copy. It refuses to tick the last unticked copy of its set.
func (m *Model) tickCopy(it model.CleanItem) {
	cs := m.checks[model.ScreenDupes]
	if cs.ticked[it.ID] {
		cs.ticked[it.ID] = false
		return
	}
	var ticked []model.CleanItem
	for _, other := range cs.items {
		if other.Set == it.Set && (cs.ticked[other.ID] || other.ID == it.ID) {
			ticked = append(ticked, other)
		}
	}
	if len(model.WholeSets(ticked)) > 0 {
		m.setMsg(model.KeepMessage(it), warn)
		return
	}
	cs.ticked[it.ID] = true
}

// startDupes looks for copies in the background, unless a scan or a clean-up is running. The scan
// keeps running on other tabs.
func (m *Model) startDupes() tea.Cmd {
	cs := m.checks[model.ScreenDupes]
	switch {
	case m.deps.Scan == nil:
		return nil
	case cs.scanning:
		m.setMsg("Still looking for copies. Press esc to stop.", warn)
		return nil
	case m.cleaning():
		m.setMsg("A clean-up is running. Look for copies again when it's done.", warn)
		return nil
	}
	return m.startScan(model.ScreenDupes)
}

// dupesAfterClean drops the copies that went to the Trash, and the sets with one copy left. The
// copies that are left get a new kept copy if the old one went.
func (m *Model) dupesAfterClean() {
	cs := m.checks[model.ScreenDupes]
	for id, r := range cs.status {
		if r.Result == model.ResultDone {
			delete(cs.items, id)
			delete(cs.ticked, id)
		}
	}
	bySet := map[string][]model.CleanItem{}
	for _, it := range cs.items {
		bySet[it.Set] = append(bySet[it.Set], it)
	}
	for _, items := range bySet {
		if len(items) < 2 {
			for _, it := range items {
				delete(cs.items, it.ID)
				delete(cs.ticked, it.ID)
			}
			continue
		}
		left := map[string]bool{}
		for _, it := range items {
			left[it.Paths[0]] = true
		}
		var copies []model.Copy
		for _, c := range items[0].Copies {
			if left[c.Path] {
				c.Keep = false
				copies = append(copies, c)
			}
		}
		if keep := model.KeepCopy(m.deps.Home, copies); keep >= 0 {
			copies[keep].Keep = true
		}
		for _, it := range items {
			it.Copies, it.Keep = copies, false
			for _, c := range copies {
				if c.Keep && c.Path == it.Paths[0] {
					it.Keep = true
				}
			}
			cs.items[it.ID] = it
		}
	}
}
