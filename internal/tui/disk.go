package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

// diskState is what the Disk screen remembers. It's held by pointer, like checkState. The marked
// rows and the clean-up flow live in m.checks[model.ScreenDisk].
type diskState struct {
	tree      *model.DiskTree
	gen       int // bumped by every walk, so messages about an older one are ignored
	walking   bool
	cancel    func()
	started   bool
	measuring int    // folders being measured again after a clean-up or put-back
	selPath   string // the path of the selected row; rows re-sort under the cursor, so it follows this

	dir     string            // the open folder
	listed  string            // the folder that entries belong to
	entries []model.DiskEntry // the open folder's entries, read when it was opened
	listErr string
	focus   string // after going up, the name of the row to select
	hidden  bool   // show names that start with "."
}

type (
	walkDoneMsg struct{ gen int }
	diskListMsg struct {
		dir     string
		entries []model.DiskEntry
		err     error
	}
	remeasuredMsg struct{ gen int }
)

// diskRow is the entry behind a row of the Disk screen.
type diskRow struct{ e model.DiskEntry }

// diskScreen is a folder browser with sizes, starting at the home folder.
type diskScreen struct{}

func (diskScreen) name() string            { return "Disk" }
func (diskScreen) cleanupID() model.Screen { return model.ScreenDisk }
func (diskScreen) columns() []column       { return []column{{"SIZE", 12}, {"SHARE", 10}, {"NAME", 0}} }
func (diskScreen) sortModes() []string     { return []string{"size", "name"} }
func (diskScreen) hints() []hint           { return nil }
func (diskScreen) problem(*Model) string   { return "" }

func (diskScreen) hintsFor(m *Model) []hint {
	if hints, ok := flowHints(m.checks[model.ScreenDisk]); ok {
		return hints
	}
	if m.disk.walking { // c and r are refused during a walk, so the line has room for esc and q
		return []hint{{"enter", "open"}, {"⌫", "up"}, {"space", "mark"}, {".", "hidden"}, {"h", "history"}, {"esc", "stop"}, {"q", "quit"}}
	}
	hints := []hint{{"enter", "open"}, {"⌫", "up"}, {"space", "mark"}, {"c", "move to Trash"}, {"r", "walk again"}, {".", "hidden"}, {"h", "history"}}
	return hints
}

func (diskScreen) onShow(m *Model) tea.Cmd {
	if m.disk.started {
		return m.listDir(m.disk.dir) // files may have changed while another tab was open
	}
	return m.startWalk()
}

func (diskScreen) esc(m *Model) bool {
	if m.flowEsc(model.ScreenDisk) {
		return true
	}
	if d := m.disk; d.walking && d.cancel != nil {
		d.cancel()
		m.setMsg("Stopped the walk. Sizes with ≥ are at least what's shown; r walks again.", warn)
		return true
	}
	return false
}

func (diskScreen) empty(m *Model) string {
	d := m.disk
	switch {
	case m.checks[model.ScreenDisk].mode == modeHistory:
		return "Nothing cleaned yet."
	case m.deps.Walk == nil:
		return "Disk isn't available."
	case !d.started:
		return "The walk starts when the clean-up is done. Press r then."
	case d.listErr != "":
		return "Couldn't read this folder: " + d.listErr
	case d.listed != d.dir:
		return "Reading…"
	}
	return "This folder is empty."
}

func (diskScreen) preamble(m *Model) []string {
	cs := m.checks[model.ScreenDisk]
	if lines, ok := flowPreamble(cs); ok {
		return withICloudWarning(m.deps.Home, cs, lines)
	}
	d := m.disk
	head := " " + accent.Render("Disk · "+model.HomePath(m.deps.Home, d.dir))
	if d.tree == nil {
		return []string{head}
	}
	p := d.tree.Progress()
	switch {
	case d.walking:
		head += faint.Render(fmt.Sprintf("   walking… %s files · %s", model.HumanCount(p.Files), model.HumanBytes(p.Bytes)))
	case p.Stopped:
		head += warn.Render("   walk stopped: r walks again")
	default:
		if e, ok := d.tree.Entry(d.dir); ok {
			head += faint.Render("   " + diskSize(e))
		}
	}
	if n := len(cs.items); n > 0 {
		head += faint.Render(fmt.Sprintf("   %d marked · %s", n, model.HumanBytes(model.TotalSize(cs.list()))))
	}
	lines := []string{head}
	if p.Unreadable > 0 {
		lines = append(lines, " "+warn.Render(fmt.Sprintf("%s couldn't be read, so sizes with ≥ are at least what's shown. To see %s, give your terminal Full Disk Access (System Settings › Privacy & Security › Full Disk Access).", folders(p.Unreadable), toSee(p.Unreadable))))
	}
	return lines
}

func toSee(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func folders(n int) string {
	if n == 1 {
		return "1 folder"
	}
	return fmt.Sprintf("%d folders", n)
}

func (diskScreen) rows(m *Model) []row {
	cs := m.checks[model.ScreenDisk]
	if rows, ok := flowRows(cs); ok {
		return rows
	}
	d := m.disk
	if d.tree == nil || d.listed != d.dir {
		return nil
	}
	var total uint64
	if e, ok := d.tree.Entry(d.dir); ok {
		total = e.Size
	}
	var rows []row
	for _, e := range model.DiskRows(d.entries, d.tree.Children(d.dir), d.hidden, m.sortIdx[m.tab] == 1) {
		mark := "  "
		if _, ok := cs.items[model.DiskID(m.deps.Home, e.Path)]; ok {
			mark = "✓ "
		}
		name := e.Name
		switch {
		case e.Package != "":
			name += " · " + e.Package
		case e.Dir:
			name += "/"
		}
		if model.InICloudDrive(m.deps.Home, e.Path) {
			name += " · in iCloud Drive"
		}
		rows = append(rows, row{cells: []string{mark + diskSize(e), shareBar(e.Size, total), name},
			styles: []lipgloss.Style{plain, accent, plain}, item: diskRow{e}})
	}
	return rows
}

// diskSize is a row's size: "–" when the walk has none, "≥ 1.2 GB" when it's a minimum.
func diskSize(e model.DiskEntry) string {
	switch {
	case e.Unmeasured:
		return "–"
	case e.Min:
		return "≥ " + model.HumanBytes(e.Size)
	}
	return model.HumanBytes(e.Size)
}

// shareBar shows size as a share of total, in up to eight blocks.
func shareBar(size, total uint64) string {
	if total == 0 {
		return ""
	}
	n := int(float64(size)/float64(total)*8 + 0.5)
	return strings.Repeat("█", max(0, min(n, 8)))
}

func (diskScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	if key == "u" && m.disk.walking && m.checks[model.ScreenDisk].mode == modeHistory {
		m.setMsg("Wait for the walk to finish, then put it back.", warn)
		return nil, true
	}
	if cmd, handled, inFlow := m.flowKey(model.ScreenDisk, key, r); inFlow {
		return cmd, handled
	}
	d := m.disk
	var e model.DiskEntry
	have := false
	if r != nil {
		if dr, ok := r.item.(diskRow); ok {
			e, have = dr.e, true
		}
	}
	switch key {
	case "enter":
		switch {
		case !have:
		case e.Package != "":
			m.setMsg(e.Name+" is a package: it moves to the Trash as one item, and soap doesn't open it.", faint)
		case e.Dir:
			return m.openDir(e.Path), true
		}
	case "backspace":
		if d.dir == "" || d.dir == m.deps.Home {
			return nil, true
		}
		d.focus = filepath.Base(d.dir)
		return m.openDir(filepath.Dir(d.dir)), true
	case "space":
		if have {
			m.mark(e)
		}
	case ".":
		d.hidden = !d.hidden
		if d.hidden {
			m.setMsg("Showing hidden files.", faint)
		} else {
			m.setMsg("Hiding hidden files.", faint)
		}
		m.clampSelection()
	case "c":
		return m.askClean(model.ScreenDisk), true
	case "h":
		return m.openHistory(model.ScreenDisk), true
	case "r":
		return m.startWalk(), true
	default:
		return nil, false
	}
	return nil, true
}

// anchorSelection keeps the Disk selection on the same path when rows re-sort under the cursor
// (sizes grow during a walk, and listings refresh). rows are the visible rows. When the path is
// gone the index stays as it is.
func (m *Model) anchorSelection(rows []row) {
	d := m.disk
	if d.selPath != "" {
		for i, r := range rows {
			if dr, ok := r.item.(diskRow); ok && dr.e.Path == d.selPath {
				m.sel[m.tab] = i
				return
			}
		}
		return
	}
	d.selPath = m.selectedDiskPath(rows)
}

// selectedDiskPath is the path of the selected row, or "" when it isn't a Disk row.
func (m *Model) selectedDiskPath(rows []row) string {
	if i := m.sel[m.tab]; i < len(rows) {
		if dr, ok := rows[i].item.(diskRow); ok {
			return dr.e.Path
		}
	}
	return ""
}

// mark marks a row to go to the Trash, or unmarks it. Marking a folder unmarks what's marked
// inside it.
func (m *Model) mark(e model.DiskEntry) {
	cs, home := m.checks[model.ScreenDisk], m.deps.Home
	id := model.DiskID(home, e.Path)
	if _, ok := cs.items[id]; ok {
		delete(cs.items, id)
		delete(cs.ticked, id)
		return
	}
	if e.Unmeasured {
		m.setMsg(e.Name+" wasn't measured. Press r to walk again first.", warn)
		return
	}
	if err := model.CheckPath(home, home, e.Path, model.MethodTrash, nil); err != nil {
		m.setMsg(err.Error()+".", warn)
		return
	}
	for _, it := range cs.items {
		if strings.HasPrefix(e.Path, it.Paths[0]+"/") {
			m.setMsg("It's inside "+it.Name+", which is marked already.", warn)
			return
		}
	}
	for other, it := range cs.items {
		if strings.HasPrefix(it.Paths[0], e.Path+"/") {
			delete(cs.items, other)
			delete(cs.ticked, other)
		}
	}
	it := model.DiskItem(home, e)
	cs.items[it.ID], cs.ticked[it.ID] = it, true
}

// startWalk walks the home folder in the background. It keeps running on other tabs.
func (m *Model) startWalk() tea.Cmd {
	d := m.disk
	switch {
	case m.deps.Walk == nil || m.deps.Home == "":
		return nil
	case d.walking:
		m.setMsg("The walk is still running. Press esc to stop it.", warn)
		return nil
	case m.cleaning():
		m.setMsg("A clean-up is running. Walk again when it's done.", warn)
		return nil
	}
	cs := m.checks[model.ScreenDisk]
	clear(cs.items) // marks hold the size and stamp from when they were made
	clear(cs.ticked)
	ctx, cancel := context.WithCancel(context.Background())
	d.gen++
	d.tree = model.NewDiskTree(m.deps.Home)
	d.walking, d.cancel, d.started = true, cancel, true
	if d.dir == "" {
		d.dir = m.deps.Home
	}
	walk, tree, gen := m.deps.Walk, d.tree, d.gen
	return tea.Batch(func() tea.Msg {
		walk(ctx, tree)
		return walkDoneMsg{gen: gen}
	}, m.listDir(d.dir))
}

// openDir opens a folder: its sub-folders come from the walk at once, its files from a listing.
func (m *Model) openDir(dir string) tea.Cmd {
	d := m.disk
	d.dir, d.listed, d.entries, d.listErr = dir, "", nil, ""
	m.resetSelection()
	return m.listDir(dir)
}

func (m *Model) listDir(dir string) tea.Cmd {
	if m.deps.List == nil || dir == "" {
		return nil
	}
	list := m.deps.List
	return func() tea.Msg {
		entries, err := list(dir)
		return diskListMsg{dir: dir, entries: entries, err: err}
	}
}

func (m *Model) onDiskList(msg diskListMsg) {
	d := m.disk
	if msg.dir != d.dir {
		return // the user has moved on to another folder
	}
	d.listed, d.entries, d.listErr = msg.dir, msg.entries, ""
	if msg.err != nil {
		d.entries, d.listErr = nil, msg.err.Error()
	}
	if !m.isCurrent(model.ScreenDisk) {
		return
	}
	if d.focus != "" {
		for i, r := range m.visibleRows() {
			if dr, ok := r.item.(diskRow); ok && dr.e.Name == d.focus {
				m.sel[m.tab], d.selPath = i, dr.e.Path
			}
		}
		d.focus = ""
	}
	m.clampSelection()
}

// diskAfterClean unmarks what went to the Trash and measures the folders that held it again.
func (m *Model) diskAfterClean() tea.Cmd {
	cs := m.checks[model.ScreenDisk]
	var moved []string
	for id, r := range cs.status {
		if r.Result == model.ResultDone {
			delete(cs.items, id)
			delete(cs.ticked, id)
			moved = append(moved, r.Item.Paths...)
		}
	}
	return m.remeasure(parents(moved))
}

// remeasure measures folders again in the background, then lists the open folder again.
func (m *Model) remeasure(dirs []string) tea.Cmd {
	d := m.disk
	if d.tree == nil || m.deps.Remeasure == nil || len(dirs) == 0 || d.walking {
		return m.listDir(d.dir) // a running walk sees the change itself
	}
	remeasure, tree, gen := m.deps.Remeasure, d.tree, d.gen
	d.measuring++
	return func() tea.Msg {
		remeasure(context.Background(), tree, dirs)
		return remeasuredMsg{gen: gen}
	}
}

// parents are the folders that hold paths, each once, sorted.
func parents(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if dir := filepath.Dir(p); !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}
