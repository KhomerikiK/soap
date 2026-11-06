package model

import (
	"errors"
	"slices"
	"testing"
)

const gib = uint64(1 << 30)

func scanned(items ...CleanItem) ScanState {
	return ScanState{Available: true, Started: true, Items: items}
}

func keys(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Key)
	}
	return out
}

func TestAttentionRules(t *testing.T) {
	claude := AppUsage{Name: "Claude", Memory: 2900 << 20, CPU: 12}
	xcode := AppUsage{Name: "Xcode", Memory: 900 << 20, CPU: 340}
	cpu := func(v float64, n int) []float64 { return slices.Repeat([]float64{v}, n) }
	app := func(name string, unused int, size uint64) CleanItem {
		b := "/Applications/" + name + ".app"
		return CleanItem{ID: "apps/" + name, Screen: ScreenApps, Group: "Apps", Name: name + ".app", Size: size, Method: MethodTrash,
			Paths: []string{b}, Root: "/Applications", Bundle: b, UnusedDays: unused}
	}
	leftover := func(bundle, name string, size uint64) CleanItem {
		return CleanItem{ID: "apps/x/" + name, Screen: ScreenApps, Group: "Apps", Name: name, Size: size, Method: MethodTrash,
			Paths: []string{"/Users/me/" + name}, Root: "/Users/me", Bundle: bundle}
	}
	copyOf := func(set, path string, keep bool, unshared uint64) CleanItem {
		return CleanItem{ID: "dupes/" + path, Screen: ScreenDupes, Group: "Copies", Name: path, Size: 2 * gib, Unshared: unshared,
			Method: MethodTrash, Paths: []string{path}, Set: set, Keep: keep}
	}
	trashed := func(size uint64) HistoryEntry {
		return HistoryEntry{ID: "1-0", Method: MethodTrash, Trashed: []string{"/Users/me/.Trash/x"}, Result: ResultDone, Size: size}
	}
	tests := []struct {
		name string
		in   HomeInput
		want []Finding
	}{
		{"nothing", HomeInput{DiskFree: 109 * gib, DiskTotal: 460 * gib, Memory: MemoryStats{Total: 18 * gib, Pressure: PressureNormal}}, nil},
		{"disk under 20 GB", HomeInput{DiskFree: 20*gib - 1, DiskTotal: 460 * gib},
			[]Finding{{Key: "disk", Level: Urgent, Text: "Only 20.0 GB free on your disk", Tab: TabDisk}}},
		{"disk at 20 GB", HomeInput{DiskFree: 20 * gib, DiskTotal: 460 * gib},
			[]Finding{{Key: "disk", Level: Warning, Text: "20.0 GB free on your disk, under 60 GB", Tab: TabDisk}}},
		{"disk at 60 GB", HomeInput{DiskFree: 60 * gib, DiskTotal: 460 * gib}, nil},
		{"disk size unknown", HomeInput{}, nil},
		{"pressure critical", HomeInput{Memory: MemoryStats{Pressure: PressureCritical}, Top: []AppUsage{xcode, claude}},
			[]Finding{{Key: "memory", Level: Urgent, Text: "Memory pressure is critical. Claude uses the most: 2.8 GB", Short: "Memory pressure is critical", Tab: TabMemory}}},
		{"pressure warning without processes", HomeInput{Memory: MemoryStats{Pressure: PressureWarning}, Top: []AppUsage{claude}, ProcessErr: errors.New("ps failed")},
			[]Finding{{Key: "memory", Level: Warning, Text: "Memory pressure is high", Tab: TabMemory}}},
		{"pressure unreadable", HomeInput{Memory: MemoryStats{Pressure: PressureCritical}, MemoryErr: errors.New("no")}, nil},
		{"CPU busy for a minute", HomeInput{CPUHistory: append(cpu(10, 5), cpu(81, 30)...), Top: []AppUsage{claude, xcode}},
			[]Finding{{Key: "cpu", Level: Warning, Text: "CPU has been over 80% for a minute. Xcode uses the most: 340%", Short: "CPU has been over 80% for a minute", Tab: TabMemory}}},
		{"CPU at 80%", HomeInput{CPUHistory: cpu(80, 30)}, nil},
		{"CPU busy for less than a minute", HomeInput{CPUHistory: cpu(100, 29)}, nil},
		{"swap over a quarter", HomeInput{Memory: MemoryStats{Total: 16 * gib, SwapUsed: 4*gib + 1}},
			[]Finding{{Key: "swap", Level: Warning, Text: "Swap is 4.0 GB. Quit apps you don't use", Short: "Swap is 4.0 GB", Tab: TabMemory}}},
		{"swap at a quarter", HomeInput{Memory: MemoryStats{Total: 16 * gib, SwapUsed: 4 * gib}}, nil},
		{"an app listens everywhere", HomeInput{Listeners: []Listener{
			{Port: 53, Bind: "127.0.0.1", Process: "dnsmasq"},
			{Port: 5432, Bind: "*", Process: "postgres"},
			{Port: 7000, Bind: "*", Process: "ControlCenter", System: true},
		}}, []Finding{{Key: "ports", Level: Warning, Text: "postgres listens on every network: :5432", Short: "postgres listens on every network", Tab: TabPorts, Port: 5432}}},
		{"two apps listen everywhere", HomeInput{Listeners: []Listener{
			{Port: 3306, Bind: "*", Process: "com.docker.backend", Container: "mysql-1"},
			{Port: 5432, Bind: "*", Process: "postgres"},
			{Port: 5432, Bind: "*", Process: "postgres", Proto: "udp4"},
		}}, []Finding{{Key: "ports", Level: Warning, Text: "mysql-1 and 1 more listen on every network: :3306, :5432", Short: "mysql-1 and 1 more listen on every network", Tab: TabPorts, Port: 3306}}},
		{"sockets unreadable", HomeInput{Listeners: []Listener{{Port: 5432, Bind: "*"}}, SocketErr: errors.New("no")}, nil},
		{"failed checks", HomeInput{
			Junk: ScanState{Errs: map[string]string{"Config": "bad\njson", "App caches": "permission denied"}},
			Dev:  ScanState{Errs: map[string]string{"Simulators": "xcrun timed out"}},
			Apps: ScanState{Errs: map[string]string{"Config": "bad json", "Apps": "mdfind broke"}},
		}, []Finding{
			{Key: "check/junk/App caches", Level: Warning, Text: "The App caches check failed: permission denied", Short: "The App caches check failed", Tab: TabJunk, Group: "App caches"},
			{Key: "check/config", Level: Warning, Text: "config.json can't be read: bad json", Short: "config.json can't be read", Tab: TabJunk, Group: "Config"},
			{Key: "check/dev/Simulators", Level: Warning, Text: "The Simulators check failed: xcrun timed out", Short: "The Simulators check failed", Tab: TabDev, Group: "Simulators"},
			{Key: "check/apps/Apps", Level: Warning, Text: "The Apps check failed: mdfind broke", Short: "The Apps check failed", Tab: TabApps, Group: "Apps"},
		}},
		{"safe junk and a big group that isn't safe", HomeInput{
			Junk: scanned(
				CleanItem{ID: "a", Group: "App caches", Size: 3 * gib, Safe: true},
				CleanItem{ID: "b", Group: "App caches", Size: 9 * gib, Safe: true, Busy: true, App: "Google Chrome"},
			),
			Dev: scanned(
				CleanItem{ID: "s", Group: "Simulators", Size: 5 * gib},
				CleanItem{ID: "x", Group: "Xcode", Size: 5 * gib},
				CleanItem{ID: "p", Group: "Projects", Size: 5*gib - 1},
				CleanItem{ID: "q", Group: "Projects", Size: 3 * gib, Blocked: "Couldn't check whether Xcode is running"},
			),
		}, []Finding{
			{Key: "space/busy/Google Chrome", Level: Space, Text: "Quit Google Chrome to clean 9.0 GB of its caches", Short: "Quit Google Chrome to clean 9.0 GB", Size: 9 * gib, Tab: TabJunk, Group: "App caches"},
			{Key: "space/dev/Simulators", Level: Space, Text: "Simulators hold 5.0 GB", Size: 5 * gib, Tab: TabDev, Group: "Simulators"},
			{Key: "space/dev/Xcode", Level: Space, Text: "Xcode holds 5.0 GB", Size: 5 * gib, Tab: TabDev, Group: "Xcode"},
			{Key: "space/junk/safe", Level: Space, Text: "3.0 GB of safe junk is ready to clean", Size: 3 * gib, Tab: TabJunk},
		}},
		{"safe Dev files", HomeInput{Dev: scanned(CleanItem{ID: "d", Group: "Xcode", Size: gib, Safe: true})},
			[]Finding{{Key: "space/dev/safe", Level: Space, Text: "1.0 GB of safe Dev files is ready to clean", Size: gib, Tab: TabDev}}},
		{"busy items under 1 GB", HomeInput{Junk: scanned(CleanItem{ID: "b", Group: "Browser caches", Size: gib - 1, Busy: true, App: "Brave Browser"})}, nil},
		{"unused apps and removed apps' leftovers", HomeInput{Apps: scanned(
			app("Notion", 30, gib), leftover("/Applications/Notion.app", "notion", gib),
			app("Pages", 30, 2*gib), app("Docker", 0, 9*gib),
			leftover("", "gone.a", gib/2), leftover("", "gone.b", gib/2),
		)}, []Finding{
			{Key: "space/apps/unused", Level: Space, Text: "2 apps not opened in 30 days use 4.0 GB", Size: 4 * gib, Tab: TabApps},
			{Key: "space/apps/removed", Level: Space, Text: "Leftovers of removed apps use 1.0 GB", Size: gib, Tab: TabApps, Group: RemovedApps},
		}},
		{"one unused app and small leftovers", HomeInput{Apps: scanned(app("Pages", 45, 2*gib), leftover("", "gone.a", gib-1))},
			[]Finding{{Key: "space/apps/unused", Level: Space, Text: "1 app not opened in 45 days uses 2.0 GB", Size: 2 * gib, Tab: TabApps}}},
		{"copies, once Dupes was opened", HomeInput{Dupes: scanned(
			copyOf("a", "/a1", true, 2*gib), copyOf("a", "/a2", false, 2*gib), copyOf("a", "/a3", false, 2*gib),
			copyOf("b", "/b1", true, gib), copyOf("b", "/b2", false, 0),
		)}, []Finding{{Key: "space/dupes", Level: Space, Text: "Copies of 2 files use 4.0 GB", Size: 4 * gib, Tab: TabDupes}}},
		{"copies before Dupes was opened", HomeInput{Dupes: ScanState{Items: []CleanItem{copyOf("a", "/a1", true, gib), copyOf("a", "/a2", false, gib)}}}, nil},
		{"the Trash, with Full Disk Access", HomeInput{History: []HistoryEntry{trashed(gib), trashed(gib / 2), {ID: "2-0", Method: MethodDelete, Result: ResultDone, Size: 9 * gib}}},
			[]Finding{{Key: "space/trash", Level: Space, Text: "1.5 GB that soap moved still sits in the Trash", Size: gib + gib/2, Tab: TabJunk, Group: "Trash"}}},
		{"the Trash, without Full Disk Access", HomeInput{History: []HistoryEntry{trashed(gib)}, NoFullDiskAccess: true}, []Finding{
			{Key: "space/trash", Level: Space, Text: "1.0 GB that soap moved still sits in the Trash", Size: gib, Tab: -1, Hint: "empty it in Finder"},
			{Key: "info/fda", Level: Info, Text: "Give your terminal Full Disk Access to see the Trash and iOS backups", Short: "Give your terminal Full Disk Access", Tab: TabJunk},
		}},
		{"the Trash under 1 GB", HomeInput{History: []HistoryEntry{trashed(gib - 1)}}, nil},
		{"the history unreadable", HomeInput{History: []HistoryEntry{trashed(9 * gib)}, HistoryErr: errors.New("no")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Attention(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestAttentionOrder is the user's Mac on 2026-11-06, with an example for Apps.
func TestAttentionOrder(t *testing.T) {
	in := HomeInput{
		DiskFree: 109 * gib, DiskTotal: 460 * gib,
		Memory: MemoryStats{Total: 18 * gib, Used: 14 * gib, SwapUsed: 2 * gib, Pressure: PressureNormal},
		Junk: scanned(
			CleanItem{ID: "j1", Group: "App caches", Size: 11100 << 20, Safe: true},
			CleanItem{ID: "j2", Group: "Browser caches", Size: 2 * gib, Safe: true, Busy: true, App: "Brave Browser"},
		),
		Dev: scanned(
			CleanItem{ID: "d1", Group: "Simulators", Size: 23200 << 20},
			CleanItem{ID: "d2", Group: "Package caches", Size: 8800 << 20},
			CleanItem{ID: "d3", Group: "Projects", Size: 6500 << 20},
		),
		Apps: scanned(CleanItem{ID: "apps/a", Screen: ScreenApps, Group: "Apps", Size: 7200 << 20, Paths: []string{"/Applications/A.app"},
			Bundle: "/Applications/A.app", UnusedDays: 30}),
		History:          []HistoryEntry{{ID: "1-0", Method: MethodTrash, Trashed: []string{"/x"}, Result: ResultDone, Size: 26800 << 20}},
		NoFullDiskAccess: true,
	}
	want := []string{"space/trash", "space/dev/Simulators", "space/junk/safe", "space/dev/Package caches", "space/apps/unused",
		"space/dev/Projects", "space/busy/Brave Browser", "info/fda"}
	if got := keys(Attention(in)); !slices.Equal(got, want) {
		t.Errorf("order = %v\nwant    %v", got, want)
	}
	in.DiskFree = 14 * gib
	in.Memory.Pressure = PressureWarning
	in.CPUHistory = slices.Repeat([]float64{95}, 30)
	got := keys(Attention(in))
	if want := []string{"disk", "memory", "cpu"}; !slices.Equal(got[:3], want) {
		t.Errorf("urgent first, then warnings in rule order: %v", got)
	}
	if got[len(got)-1] != "info/fda" {
		t.Errorf("info comes last: %v", got)
	}
}

func TestAttentionScroll(t *testing.T) {
	tests := []struct{ start, cursor, rows, total, want int }{
		{0, 0, 5, 8, 0},
		{0, 4, 5, 8, 0},
		{0, 5, 5, 8, 1},
		{0, 7, 5, 8, 3},
		{3, 4, 5, 8, 3},
		{3, 2, 5, 8, 2},
		{3, -1, 5, 8, 3},
		{6, -1, 5, 8, 3}, // findings went away
		{2, 1, 5, 3, 0},
	}
	for _, tt := range tests {
		if got := AttentionStart(tt.start, tt.cursor, tt.rows, tt.total); got != tt.want {
			t.Errorf("AttentionStart(%d, %d, %d, %d) = %d, want %d", tt.start, tt.cursor, tt.rows, tt.total, got, tt.want)
		}
	}
	for _, tt := range []struct {
		start, rows, total int
		want               string
	}{{3, 5, 8, "4–8 of 8"}, {0, 5, 8, "1–5 of 8"}, {0, 5, 5, "5 found"}, {0, 1, 1, "1 found"}, {0, 1, 0, ""}} {
		if got := AttentionCount(tt.start, tt.rows, tt.total); got != tt.want {
			t.Errorf("AttentionCount(%d, %d, %d) = %q, want %q", tt.start, tt.rows, tt.total, got, tt.want)
		}
	}
}

func TestLevelMarks(t *testing.T) {
	if got := Urgent.Mark() + Warning.Mark() + Space.Mark() + Info.Mark(); got != "●▲◆•" {
		t.Errorf("marks = %q", got)
	}
}

func TestAttentionShowsTheTrashOnce(t *testing.T) {
	trashRow := CleanItem{ID: "junk/trash", Screen: ScreenJunk, Group: "Trash", Name: "Trash", Size: 8 * gib, Method: MethodDelete}
	in := HomeInput{
		Junk:    scanned(trashRow),
		History: []HistoryEntry{{ID: "1-0", Method: MethodTrash, Trashed: []string{"/Users/me/.Trash/x"}, Result: ResultDone, Size: 2 * gib}},
	}
	var trash []string
	for _, f := range Attention(in) {
		if f.Tab == TabJunk && f.Group == "Trash" || f.Key == "space/trash" {
			trash = append(trash, f.Key)
		}
	}
	if len(trash) != 1 {
		t.Errorf("Trash lines = %v, want one", trash)
	}
}
