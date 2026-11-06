package model

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The tabs, by the key that opens them. Home is tab 0.
const (
	TabHome = iota
	TabPorts
	TabConnections
	TabMemory
	TabJunk
	TabDev
	TabDisk
	TabDupes
	TabApps
)

// Level is how much a finding on Home needs the user. Findings are sorted by level, Urgent first.
type Level int

const (
	Urgent  Level = iota // act now
	Warning              // something isn't right
	Space                // space the user can free
	Info                 // good to know
)

// Mark is the level's mark on Home.
func (l Level) Mark() string {
	switch l {
	case Urgent:
		return "●"
	case Warning:
		return "▲"
	case Space:
		return "◆"
	}
	return "•"
}

// Finding is one line of Home's "Needs attention" list.
type Finding struct {
	Key   string // stable, such as "space/dev/Simulators"; the cursor follows it
	Level Level
	Text  string // "Simulators hold 23.2 GB"
	Short string // a shorter text for narrow windows; "" when Text is short already
	Size  uint64 // orders Space findings
	Tab   int    // the tab enter opens; -1 when enter does nothing
	Group string // a group to put the cursor on, or ""
	Port  uint16 // a Ports row to put the cursor on, or 0
	Hint  string // shown instead of a tab chip, such as "empty it in Finder"
}

// ScanState is what a clean-up tab knows about its scan, as Home reads it.
type ScanState struct {
	Available bool // the TUI can scan; false when scanning isn't wired, as in some tests
	Started   bool
	Scanning  bool
	Done      int // checks finished
	Total     int
	Progress  string // a streaming finder's latest progress line (Dupes)
	Items     []CleanItem
	Notes     []Note
	Errs      map[string]string // by group
}

// DiskState is what Home shows of the Disk walk.
type DiskState struct {
	Available   bool // the TUI can walk
	Started     bool
	Walking     bool
	Stopped     bool   // the last walk was stopped before it got through everything
	Files       uint64 // files seen so far
	Size        uint64 // the home folder
	SizeMin     bool   // Size is a minimum
	Biggest     string // the largest folder or package in the home folder
	BiggestSize uint64
}

// HomeInput is everything Home shows. The TUI fills it from the state the other tabs already have.
type HomeInput struct {
	CPU        float64   // percent of the whole machine
	CPUHistory []float64 // the last samples, oldest first, one every refresh
	Memory     MemoryStats
	MemoryErr  error
	Top        []AppUsage // apps by memory, as the Memory tab has them
	ProcessErr error
	Listeners  []Listener
	Groups     []ConnGroup // apps with connections, busiest download first
	SocketErr  error
	Net        Rate
	DiskFree   uint64
	DiskTotal  uint64 // 0 when unknown

	Junk, Dev, Apps, Dupes ScanState
	Disk                   DiskState

	History          []HistoryEntry // newest first, as History.List gives them
	HistoryErr       error
	HistoryAvailable bool // the TUI can read the history
	HistoryRead      bool // it has been read at least once
	NoFullDiskAccess bool // Junk's scan said so (NoteNoFullDiskAccess)
}

const (
	diskUrgent   = 20 << 30 // rule 1
	diskLow      = 60 << 30 // rule 3
	bigGroup     = 5 << 30  // rule 10
	bigBusy      = 1 << 30  // rule 11
	bigLeftovers = 1 << 30  // rule 13
	bigTrash     = 1 << 30  // rule 15
	cpuSamples   = 30       // rule 5: a minute of samples, one every 2 s
	cpuBusy      = 80.0
)

// Attention is Home's "Needs attention" list: every rule's findings, worst first. Inside Space the
// largest comes first; inside the other levels the rules keep their order.
func Attention(in HomeInput) []Finding {
	var out []Finding
	add := func(f Finding) { out = append(out, f) }

	freeText := HumanBytes(in.DiskFree)
	diskKnown := in.DiskTotal > 0
	if diskKnown && in.DiskFree < diskUrgent { // 1
		add(Finding{Key: "disk", Level: Urgent, Text: "Only " + freeText + " free on your disk", Tab: TabDisk})
	}
	memory := func(level Level, text string) Finding {
		f := Finding{Key: "memory", Level: level, Text: text, Tab: TabMemory}
		if a, ok := topBy(in, func(a AppUsage) float64 { return float64(a.Memory) }); ok {
			f.Text, f.Short = text+". "+a.Name+" uses the most: "+HumanBytes(a.Memory), text
		}
		return f
	}
	if in.MemoryErr == nil && in.Memory.Pressure == PressureCritical { // 2
		add(memory(Urgent, "Memory pressure is critical"))
	}
	if diskKnown && in.DiskFree >= diskUrgent && in.DiskFree < diskLow { // 3
		add(Finding{Key: "disk", Level: Warning, Text: freeText + " free on your disk, under 60 GB", Tab: TabDisk})
	}
	if in.MemoryErr == nil && in.Memory.Pressure == PressureWarning { // 4
		add(memory(Warning, "Memory pressure is high"))
	}
	if n := len(in.CPUHistory); n >= cpuSamples { // 5
		sum := 0.0
		for _, v := range in.CPUHistory[n-cpuSamples:] {
			sum += v
		}
		if sum/cpuSamples > cpuBusy {
			f := Finding{Key: "cpu", Level: Warning, Text: "CPU has been over 80% for a minute", Tab: TabMemory}
			if a, ok := topBy(in, func(a AppUsage) float64 { return a.CPU }); ok {
				f.Text, f.Short = f.Text+". "+a.Name+fmt.Sprintf(" uses the most: %.0f%%", a.CPU), f.Text
			}
			add(f)
		}
	}
	if st := in.Memory; in.MemoryErr == nil && st.Total > 0 && st.SwapUsed > st.Total/4 { // 6
		swap := "Swap is " + HumanBytes(st.SwapUsed)
		add(Finding{Key: "swap", Level: Warning, Text: swap + ". Quit apps you don't use", Short: swap, Tab: TabMemory})
	}
	if f, ok := openPorts(in); ok { // 7
		add(f)
	}
	for _, f := range failedChecks(in) { // 8
		add(f)
	}
	for _, t := range []struct {
		state *ScanState
		tab   int
		id    string
		what  string
	}{{&in.Junk, TabJunk, "junk", "junk"}, {&in.Dev, TabDev, "dev", "Dev files"}} {
		if safe := safeSize(t.state.Items); safe > 0 { // 9
			add(Finding{Key: "space/" + t.id + "/safe", Level: Space, Text: HumanBytes(safe) + " of safe " + t.what + " is ready to clean",
				Size: safe, Tab: t.tab})
		}
		unsafe := map[string]uint64{} // 10
		for _, it := range t.state.Items {
			if !it.PreTicked() && it.Tickable() {
				unsafe[it.Group] += it.Size
			}
		}
		for _, g := range sortedKeys(unsafe) {
			if size := unsafe[g]; size >= bigGroup {
				add(Finding{Key: "space/" + t.id + "/" + g, Level: Space, Text: g + " " + holds(g) + " " + HumanBytes(size), Size: size, Tab: t.tab, Group: g})
			}
		}
	}
	for _, f := range busyApps(in.Junk.Items) { // 11
		add(f)
	}
	if unused, count, days := unusedApps(in.Apps.Items); count > 0 { // 12
		text := fmt.Sprintf("%d apps not opened in %d days use %s", count, days, HumanBytes(unused))
		if count == 1 {
			text = fmt.Sprintf("1 app not opened in %d days uses %s", days, HumanBytes(unused))
		}
		add(Finding{Key: "space/apps/unused", Level: Space, Text: text, Size: unused, Tab: TabApps})
	}
	var removed uint64 // 13
	for _, it := range in.Apps.Items {
		if it.Bundle == "" {
			removed += it.Size
		}
	}
	if removed >= bigLeftovers {
		add(Finding{Key: "space/apps/removed", Level: Space, Text: "Leftovers of removed apps use " + HumanBytes(removed), Size: removed,
			Tab: TabApps, Group: RemovedApps})
	}
	if in.Dupes.Started { // 14
		sets := DupeSets(in.Dupes.Items, false)
		var frees uint64
		for _, s := range sets {
			frees += s.Frees
		}
		if len(sets) > 0 && frees > 0 {
			files := "1 file"
			if len(sets) != 1 {
				files = fmt.Sprintf("%d files", len(sets))
			}
			add(Finding{Key: "space/dupes", Level: Space, Text: "Copies of " + files + " use " + HumanBytes(frees), Size: frees, Tab: TabDupes})
		}
	}
	trashLine := slices.ContainsFunc(out, func(f Finding) bool { return f.Tab == TabJunk && f.Group == "Trash" })
	if in.HistoryErr == nil && !trashLine { // 15; rule 10's Trash line already covers what soap moved there
		if trash := InTrash(in.History); trash >= bigTrash {
			f := Finding{Key: "space/trash", Level: Space, Text: HumanBytes(trash) + " that soap moved still sits in the Trash", Size: trash,
				Tab: TabJunk, Group: "Trash"}
			if in.NoFullDiskAccess {
				f.Tab, f.Group, f.Hint = -1, "", "empty it in Finder"
			}
			add(f)
		}
	}
	if in.NoFullDiskAccess { // 16
		add(Finding{Key: "info/fda", Level: Info, Text: "Give your terminal Full Disk Access to see the Trash and iOS backups",
			Short: "Give your terminal Full Disk Access", Tab: TabJunk})
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level < b.Level
		}
		return a.Level == Space && a.Size > b.Size
	})
	return out
}

// topBy is the app with the most of what by says, as the Memory tab has them.
func topBy(in HomeInput, by func(AppUsage) float64) (AppUsage, bool) {
	if in.ProcessErr != nil || len(in.Top) == 0 {
		return AppUsage{}, false
	}
	best := in.Top[0]
	for _, a := range in.Top[1:] {
		if by(a) > by(best) {
			best = a
		}
	}
	return best, true
}

// openPorts is rule 7: apps that aren't part of macOS and listen on every address, in one finding.
func openPorts(in HomeInput) (Finding, bool) {
	if in.SocketErr != nil {
		return Finding{}, false
	}
	var names, ports []string
	var first uint16
	for _, l := range in.Listeners {
		if l.Bind != "*" || l.System {
			continue
		}
		name := l.Process
		if l.Container != "" {
			name = l.Container
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
		if p := fmt.Sprintf(":%d", l.Port); !slices.Contains(ports, p) {
			ports = append(ports, p)
		}
		if first == 0 {
			first = l.Port
		}
	}
	if len(names) == 0 {
		return Finding{}, false
	}
	f := Finding{Key: "ports", Level: Warning, Tab: TabPorts, Port: first}
	if len(names) == 1 {
		f.Short = names[0] + " listens on every network"
	} else {
		f.Short = fmt.Sprintf("%s and %d more listen on every network", names[0], len(names)-1)
	}
	f.Text = f.Short + ": " + strings.Join(ports, ", ")
	return f, true
}

// failedChecks is rule 8: the checks of Junk, Dev and Apps that failed, and a config file that
// can't be read (shown once, though every scan reports it).
func failedChecks(in HomeInput) []Finding {
	var out []Finding
	config := false
	for _, t := range []struct {
		state *ScanState
		tab   int
		id    string
	}{{&in.Junk, TabJunk, "junk"}, {&in.Dev, TabDev, "dev"}, {&in.Apps, TabApps, "apps"}} {
		for _, g := range sortedKeys(t.state.Errs) {
			why := strings.Join(strings.Fields(t.state.Errs[g]), " ")
			if g == "Config" {
				if !config {
					config = true
					out = append(out, Finding{Key: "check/config", Level: Warning, Text: "config.json can't be read: " + why,
						Short: "config.json can't be read", Tab: t.tab, Group: g})
				}
				continue
			}
			out = append(out, Finding{Key: "check/" + t.id + "/" + g, Level: Warning, Text: "The " + g + " check failed: " + why,
				Short: "The " + g + " check failed", Tab: t.tab, Group: g})
		}
	}
	return out
}

// InTrash is what soap moved to the Trash and can still put back, as the history says.
func InTrash(entries []HistoryEntry) uint64 {
	var n uint64
	for _, e := range entries {
		if e.CanRestore() {
			n += e.Size
		}
	}
	return n
}

// unusedApps is what the apps that count as unused take, with their leftovers, how many there are,
// and the unused_days setting they were judged by.
func unusedApps(items []CleanItem) (size uint64, count, days int) {
	for _, g := range AppGroups(items, false) {
		if g.App != nil && g.App.UnusedDays > 0 {
			size, count, days = size+g.Size, count+1, g.App.UnusedDays
		}
	}
	return size, count, days
}

// safeSize is what a tab ticks by itself.
func safeSize(items []CleanItem) uint64 {
	var n uint64
	for _, it := range items {
		if it.PreTicked() {
			n += it.Size
		}
	}
	return n
}

// busyApps is rule 11: Junk items that wait for an app to quit, 1 GB or more per app.
func busyApps(items []CleanItem) []Finding {
	size := map[string]uint64{}
	largest := map[string]CleanItem{}
	for _, it := range items {
		if !it.Busy {
			continue
		}
		app := it.BusyName()
		size[app] += it.Size
		if l, ok := largest[app]; !ok || it.Size > l.Size {
			largest[app] = it
		}
	}
	var out []Finding
	for _, app := range sortedKeys(size) {
		if s := size[app]; s >= bigBusy {
			out = append(out, Finding{Key: "space/busy/" + app, Level: Space, Text: "Quit " + app + " to clean " + HumanBytes(s) + " of its caches",
				Short: "Quit " + app + " to clean " + HumanBytes(s), Size: s, Tab: TabJunk, Group: largest[app].Group})
		}
	}
	return out
}

// holds is "hold" after a plural group name such as "Simulators", and "holds" after "Xcode".
func holds(name string) string {
	if strings.HasSuffix(name, "s") {
		return "hold"
	}
	return "holds"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AttentionStart is the first finding to show when rows lines show findings, the cursor is on
// finding cursor (-1 when it's on another line) and start was shown first before. The list
// scrolls only as the cursor moves past its first or last line.
func AttentionStart(start, cursor, rows, total int) int {
	if rows <= 0 || total <= rows {
		return 0
	}
	if cursor >= 0 {
		if cursor < start {
			start = cursor
		}
		if cursor >= start+rows {
			start = cursor - rows + 1
		}
	}
	return max(0, min(start, total-rows))
}

// AttentionCount ends the attention title: "8 found", or "4–8 of 8" when they don't all fit. It's
// "" when nothing was found.
func AttentionCount(start, rows, total int) string {
	switch {
	case total == 0:
		return ""
	case total <= rows:
		return fmt.Sprintf("%d found", total)
	}
	return fmt.Sprintf("%d–%d of %d", start+1, min(start+rows, total), total)
}
