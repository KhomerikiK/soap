package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

// appsScreen lists the apps, unused ones first, each with its leftovers, and then the leftovers of
// removed apps. It runs on part 2's checklist state. Nothing is ticked until the user asks.
type appsScreen struct{}

// appGroupRef is the group behind a group's row: an app's bundle, or removedKey.
type appGroupRef struct{ key string }

// removedKey is the key of the leftovers of removed apps in cs.open.
const removedKey = "removed"

func (appsScreen) name() string            { return "Apps" }
func (appsScreen) cleanupID() model.Screen { return model.ScreenApps }
func (appsScreen) columns() []column       { return []column{{"APP", 60}, {"SIZE", 10}, {"", 0}} }
func (appsScreen) sortModes() []string     { return []string{"unused", "size"} }
func (appsScreen) hints() []hint           { return nil }
func (appsScreen) problem(*Model) string   { return "" }

func (appsScreen) hintsFor(m *Model) []hint {
	if hints, ok := flowHints(m.checks[model.ScreenApps]); ok {
		return hints
	}
	return []hint{{"j/k", "move"}, {"space", "tick"}, {"enter", "open"}, {"f", "show in Finder"}, {"c", "move to Trash"}, {"h", "history"}, {"r", "rescan"}, {"q", "quit"}}
}

func (appsScreen) onShow(m *Model) tea.Cmd {
	if m.checks[model.ScreenApps].started {
		return nil
	}
	return m.startScan(model.ScreenApps)
}

func (appsScreen) esc(m *Model) bool { return m.flowEsc(model.ScreenApps) }

// focus opens a group, the leftovers of removed apps (model.RemovedApps) or an app's by its bundle,
// and puts the cursor on its row, or at the top when it's gone.
func (appsScreen) focus(m *Model, group string) {
	cs := m.checks[model.ScreenApps]
	if cs.mode == modeHistory {
		cs.mode = modeList
	}
	key := group
	if group == model.RemovedApps {
		key = removedKey
	}
	was := cs.open[key]
	cs.open[key] = true
	if !m.focusRow(func(item any) bool { g, ok := item.(appGroupRef); return ok && g.key == key }) {
		cs.open[key] = was
	}
}

func (appsScreen) empty(m *Model) string {
	cs := m.checks[model.ScreenApps]
	switch {
	case cs.mode == modeHistory:
		return "Nothing cleaned yet."
	case cs.scanning || !cs.started:
		return "Looking at your apps…"
	}
	return "No apps found."
}

func (appsScreen) preamble(m *Model) []string {
	cs := m.checks[model.ScreenApps]
	if lines, ok := flowPreamble(cs); ok {
		return withLaunchAgentWarning(m.deps.Home, cs, lines)
	}
	apps := 0
	for _, it := range cs.items {
		if it.IsApp() {
			apps++
		}
	}
	count := "1 app"
	if apps != 1 {
		count = fmt.Sprintf("%d apps", apps)
	}
	lines := []string{" " + accent.Render("Apps") + faint.Render(fmt.Sprintf("  %s · found %s · %s ticked",
		count, model.HumanBytes(model.TotalSize(cs.list())), model.HumanBytes(model.TotalSize(cs.tickedItems(false)))))}
	if cs.scanning {
		lines = append(lines, faint.Render(" Looking at your apps…"))
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

// withLaunchAgentWarning adds a line to the preview's title when it moves a LaunchAgent.
func withLaunchAgentWarning(home string, cs *checkState, lines []string) []string {
	if cs.mode != modePreview {
		return lines
	}
	for _, it := range cs.queue {
		for _, p := range it.Paths {
			if model.InLaunchAgents(home, p) {
				return append(lines, " "+warn.Render(model.LaunchAgentWarning))
			}
		}
	}
	return lines
}

func (appsScreen) rows(m *Model) []row {
	cs := m.checks[model.ScreenApps]
	if rows, ok := flowRows(cs); ok {
		return rows
	}
	var rows []row
	for _, g := range model.AppGroups(cs.list(), m.sortIdx[m.tab] == 1) {
		key := g.Key
		if key == "" {
			key = removedKey
		}
		var all []model.CleanItem
		if g.App != nil {
			all = append(all, *g.App)
		}
		all = append(all, g.Leftovers...)
		names := []string{g.Name}
		partial := false
		for _, it := range all {
			names = append(names, it.Name)
			partial = partial || it.SizeMin
		}
		match := strings.Join(names, " ")
		arrow := "▸"
		if cs.open[key] {
			arrow = "▾"
		}
		size := model.HumanBytes(g.Size)
		if partial {
			size = "≥ " + size
		}
		detail, style := fmt.Sprintf("%d items", len(g.Leftovers)), faint
		switch {
		case g.App == nil && len(g.Leftovers) == 1:
			detail = "1 item"
		case g.App != nil && g.App.Reason() != "":
			detail, style = g.App.Detail+" · "+g.App.Reason(), warn
		case g.App != nil:
			detail = g.App.Detail
		}
		rows = append(rows, row{cells: []string{arrow + " " + tickBox(model.GroupTick(all, cs.ticked)) + " " + g.Name, size, detail},
			styles: []lipgloss.Style{plain, plain, style}, match: match, item: appGroupRef{key}})
		if !cs.open[key] {
			continue
		}
		for _, it := range all {
			box := "[ ]"
			if cs.ticked[it.ID] && it.Tickable() {
				box = "[x]"
			}
			label, where, detail, style := "   ", it.Name, it.Detail, faint
			if it.IsApp() {
				label, where, detail = "app", model.HomePath(m.deps.Home, it.Paths[0]), ""
			}
			if r := it.Reason(); r != "" {
				detail, style = r, warn
			}
			rows = append(rows, row{cells: []string{"    " + box + " " + label + "  " + where, sizeText(it), detail},
				styles: []lipgloss.Style{plain, plain, style}, match: match, item: it})
		}
	}
	return rows
}

func (appsScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	s := model.ScreenApps
	if cmd, handled, inFlow := m.flowKey(s, key, r); inFlow {
		return cmd, handled
	}
	cs := m.checks[s]
	switch key {
	case "space":
		if r != nil {
			m.tickApps(r.item)
		}
	case "enter":
		if r != nil {
			if g, ok := r.item.(appGroupRef); ok {
				cs.open[g.key] = !cs.open[g.key]
				m.clampSelection()
			}
		}
	case "f":
		if r != nil {
			return m.reveal(r.item), true
		}
	case "c":
		return m.askClean(s), true
	case "h":
		return m.openHistory(s), true
	case "r":
		return m.startScan(s), true
	default:
		return nil, false
	}
	return nil, true
}

// appGroup is the Apps group of the app with this bundle, or nil.
func (m *Model) appGroup(bundle string) *model.AppGroup {
	for _, g := range model.AppGroups(m.checks[model.ScreenApps].list(), false) {
		if g.Key == bundle {
			return &g
		}
	}
	return nil
}

// tickApps ticks what space means on the Apps screen. On an app's group or its own row it ticks or
// unticks the app with the leftovers matched by its bundle ID; when the app itself can't be ticked
// (it needs a password, say), just those leftovers. On a leftover it's just that row. The
// leftovers of removed apps are ticked one at a time.
func (m *Model) tickApps(item any) {
	cs := m.checks[model.ScreenApps]
	var g *model.AppGroup
	switch v := item.(type) {
	case model.CleanItem:
		if !v.IsApp() {
			if !v.Tickable() {
				m.setMsg(v.Reason()+".", warn)
				return
			}
			cs.ticked[v.ID] = !cs.ticked[v.ID]
			return
		}
		g = m.appGroup(v.Bundle)
	case appGroupRef:
		if v.key == removedKey {
			m.setMsg("Tick these one at a time: each one could still belong to an app Spotlight can't see.", warn)
			return
		}
		g = m.appGroup(v.key)
	}
	if g == nil || g.App == nil {
		return
	}
	app := *g.App
	var withApp []model.CleanItem
	for _, it := range g.Leftovers {
		if !it.ByName && it.Tickable() {
			withApp = append(withApp, it)
		}
	}
	if !app.Tickable() {
		// The finder's reasons, such as "needs your password", follow the app's name.
		phrase := app.Blocked != "" && startsLower(app.Blocked)
		switch {
		case len(withApp) == 0 && phrase:
			m.setMsg(g.Name+" "+app.Blocked+". f shows it in Finder.", warn)
		case len(withApp) == 0:
			m.setMsg(app.Reason()+".", warn)
		default:
			on := model.GroupTick(withApp, cs.ticked) != model.TickAll
			for _, it := range withApp {
				cs.ticked[it.ID] = on
			}
			if on {
				m.setMsg(g.Name+" "+app.Reason()+", so only its files are ticked. f shows it in Finder.", warn)
			}
		}
		return
	}
	on := !cs.ticked[app.ID]
	cs.ticked[app.ID] = on
	for _, it := range withApp {
		cs.ticked[it.ID] = on
	}
}

// reveal shows the selected app or leftover in Finder, where an app that needs a password can
// be moved to the Trash.
func (m *Model) reveal(item any) tea.Cmd {
	var path string
	switch v := item.(type) {
	case model.CleanItem:
		if len(v.Paths) > 0 {
			path = v.Paths[0]
		}
	case appGroupRef:
		if v.key != removedKey {
			path = v.key
		}
	}
	if path == "" || m.deps.Reveal == nil {
		return nil
	}
	reveal, name := m.deps.Reveal, model.HomePath(m.deps.Home, path)
	return func() tea.Msg {
		if err := reveal(path); err != nil {
			return resultMsg{text: "Couldn't show " + name + " in Finder: " + err.Error(), err: true}
		}
		return resultMsg{text: "Showing " + name + " in Finder.", quiet: true}
	}
}

// startsLower reports whether s begins with a lower-case letter, read as a rune.
func startsLower(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLower(r)
}
