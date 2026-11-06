package model

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
	"unicode/utf8"
)

func entry(id string, at time.Time, m Method, r Result, size uint64) HistoryEntry {
	e := HistoryEntry{ID: id, Time: at, Method: m, Result: r, Size: size}
	if m == MethodTrash {
		e.Trashed = []string{"/Users/me/.Trash/" + id}
	}
	return e
}

// history is three clean-ups, newest first: one that moved 2 items to the Trash (one put back
// since), one that deleted and ran a command (one item skipped), and one that only skipped.
func history() []HistoryEntry {
	day := func(h, m int) time.Time { return time.Date(2026, 11, 6, h, m, 0, 0, time.Local) }
	put := entry("300-1", day(18, 22), MethodTrash, ResultDone, 2*gib)
	put.Restored = true
	return []HistoryEntry{
		put,
		entry("299-0", day(18, 22), MethodTrash, ResultDone, 20*gib),
		entry("201-0", day(18, 21), MethodDelete, ResultSkipped, 0),
		entry("103-2", day(18, 20), MethodDelete, ResultSkipped, 0),
		entry("102-1", day(18, 20), MethodCommand, ResultDone, 5*gib),
		entry("101-0", day(18, 20), MethodDelete, ResultPartial, 3*gib),
	}
}

func TestSummarizeHistory(t *testing.T) {
	s := SummarizeHistory(history())
	if s.Freed != 8*gib {
		t.Errorf("freed = %s; moves to the Trash don't count", HumanBytes(s.Freed))
	}
	if s.CleanUps != 2 {
		t.Errorf("clean-ups = %d; a clean-up starts at each -0, and one that only skipped doesn't count", s.CleanUps)
	}
	if s.InTrash != 20*gib {
		t.Errorf("in the Trash = %s; only entries that can be put back count", HumanBytes(s.InTrash))
	}
	if want := time.Date(2026, 11, 6, 18, 20, 0, 0, time.Local); !s.Since.Equal(want) {
		t.Errorf("since = %v", s.Since)
	}
	want := []CleanUp{
		{Time: time.Date(2026, 11, 6, 18, 22, 0, 0, time.Local), Size: 22 * gib, Trashed: 2},
		{Time: time.Date(2026, 11, 6, 18, 20, 0, 0, time.Local), Size: 8 * gib, Deleted: 1, Commands: 1},
	}
	if !slices.EqualFunc(s.Recent, want, func(a, b CleanUp) bool { return a == b }) {
		t.Errorf("recent = %+v\nwant %+v", s.Recent, want)
	}
	if got := SummarizeHistory(nil); got.CleanUps != 0 || got.Freed != 0 || !got.Since.IsZero() {
		t.Errorf("no history = %+v", got)
	}
}

func TestCleanUpWhat(t *testing.T) {
	for _, tt := range []struct {
		c    CleanUp
		want string
	}{
		{CleanUp{Deleted: 6}, "6 items deleted"},
		{CleanUp{Deleted: 1}, "1 item deleted"},
		{CleanUp{Trashed: 2}, "2 items to the Trash"},
		{CleanUp{Commands: 1}, "1 command run"},
		{CleanUp{Deleted: 3, Trashed: 1}, "3 deleted, 1 to the Trash"},
		{CleanUp{Deleted: 1, Commands: 2}, "1 deleted, 2 commands run"},
	} {
		if got := tt.c.What(); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestHistoryLines(t *testing.T) {
	in := HomeInput{HistoryAvailable: true, HistoryRead: true, History: history()}
	var long, short []string
	for _, f := range HistoryLines(in) {
		long, short = append(long, f.Long.String()), append(short, f.Short.String())
	}
	wantLong := []string{"8.0 GB freed in 2 clean-ups since 6 Nov", "20.0 GB in the Trash · you can put it back",
		"6 Nov 18:22  22.0 GB  2 items to the Trash", "6 Nov 18:20  8.0 GB  1 deleted, 1 command run"}
	wantShort := []string{"8.0 GB freed in 2 clean-ups", "20.0 GB in the Trash · can put back",
		"18:22  22.0 GB  2 trashed", "18:20  8.0 GB  1 deleted, 1 cmd"}
	if !slices.Equal(long, wantLong) || !slices.Equal(short, wantShort) {
		t.Errorf("long  %q\nshort %q", long, short)
	}
	if f := HistoryLines(in)[0]; f.Long[0] != (Seg{"8.0 GB", ToneGood}) {
		t.Errorf("freed sizes are green: %+v", f.Long)
	}
	if f := HistoryLines(in)[1]; f.Long[0] != (Seg{"20.0 GB", ToneWarn}) {
		t.Errorf("sizes in the Trash are amber: %+v", f.Long)
	}
	for _, tt := range []struct {
		in   HomeInput
		want string
	}{
		{HomeInput{HistoryAvailable: true, HistoryRead: true}, "Nothing cleaned yet."},
		{HomeInput{HistoryAvailable: true, HistoryRead: true, History: history(), HistoryErr: errors.New("no")}, "Couldn't read the history file"},
		{HomeInput{HistoryAvailable: true}, "Reading the history…"},
		{HomeInput{}, "not available"},
	} {
		if got := HistoryLines(tt.in); len(got) != 1 || got[0].Long.String() != tt.want {
			t.Errorf("%+v: %+v", tt.in, got)
		}
	}
}

func TestHomeRows(t *testing.T) {
	tests := []struct {
		height, findings int
		want             HomeLayout
	}{
		{16, 0, HomeLayout{Attention: 1, System: 5, Extra: 3, Bottom: 2}},
		{16, 2, HomeLayout{Attention: 2, System: 5, Extra: 2, Bottom: 2}},
		{16, 8, HomeLayout{Attention: 5, System: 5, Extra: 0, Bottom: 2}},
		{20, 0, HomeLayout{Attention: 1, System: 5, Extra: 5, Bottom: 4}},
		{20, 2, HomeLayout{Attention: 2, System: 5, Extra: 5, Bottom: 3}},
		{20, 8, HomeLayout{Attention: 5, System: 5, Extra: 3, Bottom: 2}},
		{24, 0, HomeLayout{Attention: 1, System: 5, Extra: 5, Bottom: 5}},
		{24, 2, HomeLayout{Attention: 2, System: 5, Extra: 5, Bottom: 5}},
		{24, 8, HomeLayout{Attention: 5, System: 5, Extra: 5, Bottom: 4}},
		{36, 0, HomeLayout{Attention: 1, System: 5, Extra: 5, Bottom: 5}},
		{36, 2, HomeLayout{Attention: 2, System: 5, Extra: 5, Bottom: 5}},
		{36, 8, HomeLayout{Attention: 5, System: 5, Extra: 5, Bottom: 5}},
	}
	for _, tt := range tests {
		got := HomeRows(tt.height, tt.findings)
		if got != tt.want {
			t.Errorf("HomeRows(%d, %d) = %+v, want %+v", tt.height, tt.findings, got, tt.want)
		}
		if n := got.Lines(); n > tt.height || (tt.height <= 20 && n != tt.height) {
			t.Errorf("HomeRows(%d, %d) takes %d lines; up to 20 the box fills the body", tt.height, tt.findings, n)
		}
	}
}

func notes(l FreeLine) (long, short string) { return l.Note.Long.String(), l.Note.Short.String() }

func TestFreeLines(t *testing.T) {
	in := HomeInput{
		Junk: scanned(
			CleanItem{ID: "j1", Group: "App caches", Size: 11100 << 20, Safe: true},
			CleanItem{ID: "j2", Group: "Trash", Size: 2100 << 20},
		),
		Dev: scanned(
			CleanItem{ID: "d1", Group: "Simulators", Size: 23200 << 20},
			CleanItem{ID: "d2", Group: "Package caches", Size: 8800 << 20},
		),
		Apps: scanned(CleanItem{ID: "apps/a", Screen: ScreenApps, Group: "Apps", Size: 7200 << 20, Paths: []string{"/Applications/A.app"},
			Bundle: "/Applications/A.app", UnusedDays: 30}),
		Dupes: ScanState{Available: true},
		Disk:  DiskState{Available: true},
	}
	in.Dev.Errs = map[string]string{"Docker": "docker timed out"}
	lines := FreeLines(in)
	var labels []string
	for _, l := range lines {
		labels = append(labels, l.Label)
	}
	if !slices.Equal(labels, []string{"4 Junk", "5 Dev", "8 Apps", "6 Disk", "7 Dupes"}) {
		t.Fatalf("labels = %v", labels)
	}
	junk, dev, apps, disk, dupes := lines[0], lines[1], lines[2], lines[3], lines[4]
	if !junk.Bar || junk.Size != 13200<<20 || junk.Safe != 11100<<20 || junk.Tab != TabJunk || junk.Key != "free/junk" {
		t.Errorf("junk = %+v", junk)
	}
	if l, s := notes(junk); l != "10.8 GB safe" || s != "10.8 safe" {
		t.Errorf("junk notes %q / %q", l, s)
	}
	if l, s := notes(dev); dev.Safe != 0 || l != "Simulators 22.7 GB · 1 check failed" || s != "1 check failed" || dev.Note.Short[0].Tone != ToneWarn {
		t.Errorf("dev = %+v (%q / %q)", dev, l, s)
	}
	if l, _ := notes(apps); apps.Size != 7200<<20 || !apps.Bar || l != "1 unused" {
		t.Errorf("apps = %+v", apps)
	}
	if l, _ := notes(disk); disk.Bar || l != "not walked yet · enter walks" {
		t.Errorf("disk = %+v", disk)
	}
	if l, _ := notes(dupes); dupes.Bar || l != "not scanned yet · enter scans" {
		t.Errorf("dupes = %+v", dupes)
	}

	in.Junk = ScanState{Available: true, Started: true, Scanning: true, Done: 2, Total: 5}
	in.Disk = DiskState{Available: true, Started: true, Walking: true, Files: 1_234_567}
	in.Dupes = ScanState{Available: true, Started: true, Scanning: true, Progress: "Hashing 12 of 40 sets · 3.1 GB read"}
	in.Apps = ScanState{}
	lines = FreeLines(in)
	for i, want := range [][2]string{
		{"scanning · 2 of 5 checks", "scanning · 2 of 5 checks"},
		{"Simulators 22.7 GB · 1 check failed", "1 check failed"},
		{"not available", "not available"},
		{"walking 1.2M files", "walking 1.2M files"},
		{"hashing 12 of 40 sets · 3.1 GB read", "finding copies"},
	} {
		if l, s := notes(lines[i]); l != want[0] || s != want[1] {
			t.Errorf("%s: %q / %q, want %q / %q", lines[i].Label, l, s, want[0], want[1])
		}
	}
	if !lines[0].Spin || lines[0].Bar || !lines[3].Spin || !lines[4].Spin || lines[2].Spin {
		t.Errorf("the spinner goes on running scans only: %+v", lines)
	}

	in.Disk = DiskState{Available: true, Started: true, Size: 193 * gib, Biggest: "Library", BiggestSize: 131 * gib}
	set := func(id string, keep bool) CleanItem {
		return CleanItem{ID: id, Group: "Copies", Size: 2 * gib, Unshared: 2 * gib, Paths: []string{"/" + id}, Set: "s", Keep: keep}
	}
	in.Dupes = scanned(set("a", true), set("b", false))
	lines = FreeLines(in)
	if l, s := notes(lines[3]); l != "home 193 GB · biggest: Library 131 GB" || s != "home 193 GB · Library 131 GB" {
		t.Errorf("disk %q / %q", l, s)
	}
	if l, _ := notes(lines[4]); !lines[4].Bar || lines[4].Size != 2*gib || l != "1 set" {
		t.Errorf("dupes = %+v", lines[4])
	}
}

func TestSystemTexts(t *testing.T) {
	st := MemoryStats{Total: 18 * gib, Used: 14438 << 20, SwapUsed: 2 * gib, Compressed: 2 * gib, Pressure: PressureNormal}
	if got := MemoryText(st); got != "14.1/18.0 GB" {
		t.Errorf("MemoryText = %q", got)
	}
	n := MemoryNote(st)
	if n.Long.String() != "normal · swap 2.0 GB · compressed 2.0 GB" || n.Short.String() != "normal · swap 2.0 GB" || n.Long[0].Tone != ToneGood {
		t.Errorf("MemoryNote = %+v", n)
	}
	d := DiskText(109*gib, 460*gib)
	if d.Long.String() != "109 GB free of 460" || d.Short.String() != "109 GB free" {
		t.Errorf("DiskText = %+v", d)
	}
	if d := DiskText(900<<20, 460*gib); d.Long.String() != "900 MB free of 460 GB" {
		t.Errorf("another unit keeps it: %q", d.Long.String())
	}
}

func TestPortsHead(t *testing.T) {
	in := HomeInput{Listeners: []Listener{
		{Port: 53, Process: "dnsmasq"},
		{Port: 53, Process: "dnsmasq", Proto: "udp4"},
		{Port: 4040, Process: "ngrok"},
		{Port: 5000, Process: "ControlCenter", System: true},
	}}
	head, ports := PortsHead(in)
	if head.String() != "4 listening · 3 from your apps" || len(ports) != 2 || ports[0].Port != 53 || ports[1].Port != 4040 {
		t.Errorf("head %q, ports %+v", head.String(), ports)
	}
	if head, _ := PortsHead(HomeInput{SocketErr: errors.New("no")}); head.String() != "Couldn't read sockets" || head[0].Tone != ToneDanger {
		t.Errorf("head = %+v", head)
	}
}

func TestSummarizeHistoryEdges(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.Local) }
	// The oldest clean-up only skipped; the next one's "-0" item was skipped but "-1" was done.
	s := SummarizeHistory([]HistoryEntry{
		entry("20-1", at(6, 10), MethodDelete, ResultDone, gib),
		entry("20-0", at(6, 10), MethodDelete, ResultSkipped, 0),
		entry("10-0", at(1, 10), MethodDelete, ResultSkipped, 0),
	})
	if s.CleanUps != 1 || s.Freed != gib || !s.Since.Equal(at(6, 10)) {
		t.Errorf("summary = %+v; Since is the oldest counted clean-up", s)
	}
}

func TestHistoryLinesDates(t *testing.T) {
	old := entry("1-0", time.Date(2025, 12, 30, 9, 5, 0, 0, time.Local), MethodDelete, ResultDone, gib)
	now := entry("2-0", time.Date(2026, 1, 2, 18, 22, 0, 0, time.Local), MethodDelete, ResultDone, gib)
	in := HomeInput{HistoryAvailable: true, HistoryRead: true, History: []HistoryEntry{now, old}}
	got := HistoryLines(in)
	if l := got[0].Long.String(); l != "2.0 GB freed in 2 clean-ups since 30 Dec 2025" {
		t.Errorf("year boundary: %q", l)
	}
	if l := got[1].Long.String(); l != "2 Jan 18:22  1.0 GB  1 item deleted" {
		t.Errorf("single-digit day: %q", l)
	}
	in.History = []HistoryEntry{now}
	if l := HistoryLines(in)[0].Long.String(); l != "1.0 GB freed in 1 clean-up since 2 Jan" {
		t.Errorf("singular: %q", l)
	}
}

func TestChecksFailedPlural(t *testing.T) {
	in := HomeInput{Dev: scanned(CleanItem{ID: "d1", Group: "Simulators", Size: gib})}
	in.Dev.Errs = map[string]string{"Docker": "x", "Xcode": "y"}
	for _, l := range FreeLines(in) {
		if l.Key == "free/dev" {
			if long, short := notes(l); long != "Simulators 1.0 GB · 2 checks failed" || short != "2 checks failed" {
				t.Errorf("%q / %q", long, short)
			}
		}
	}
}

func TestUnknownTotals(t *testing.T) {
	if got := MemoryText(MemoryStats{}); got != "Couldn't read memory" {
		t.Errorf("MemoryText = %q", got)
	}
	if d := DiskText(0, 0); d.Long.String() != "free space unknown" || d.Short.String() != "free space unknown" {
		t.Errorf("DiskText = %+v", d)
	}
}

// Every Short form fits the 37 columns the History panel has at 80 columns.
func TestShortFormsFit(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 18, 22, 0, 0, time.Local) }
	var hist []HistoryEntry
	for i := 0; i < 4; i++ {
		d := 28 - i
		hist = append(hist,
			entry(fmt.Sprintf("%d%d-3", d, i), day(d), MethodCommand, ResultDone, 123*gib),
			entry(fmt.Sprintf("%d%d-2", d, i), day(d), MethodTrash, ResultDone, 999*gib),
			entry(fmt.Sprintf("%d%d-1", d, i), day(d), MethodDelete, ResultDone, 100*gib),
			entry(fmt.Sprintf("%d%d-0", d, i), day(d), MethodDelete, ResultDone, 100*gib),
		)
	}
	hist[0].Restored = false
	in := HomeInput{HistoryAvailable: true, HistoryRead: true, History: hist}
	for _, f := range HistoryLines(in) {
		if n := utf8.RuneCountInString(f.Short.String()); n > 37 {
			t.Errorf("%q takes %d columns", f.Short.String(), n)
		}
	}
	c := CleanUp{Time: day(28), Size: 999 * gib, Deleted: 100, Trashed: 100, Commands: 100}
	in.History = nil
	big := HistoryLines(HomeInput{HistoryAvailable: true, HistoryRead: true, History: manyItems(c)})
	for _, f := range big {
		if n := utf8.RuneCountInString(f.Short.String()); n > 37 {
			t.Errorf("%q takes %d columns", f.Short.String(), n)
		}
	}
}

// manyItems is the entries of one clean-up with c's counts.
func manyItems(c CleanUp) []HistoryEntry {
	var out []HistoryEntry
	add := func(n int, m Method) {
		for i := 0; i < n; i++ {
			out = append(out, entry(fmt.Sprintf("1-%d", len(out)), c.Time, m, ResultDone, 3*gib))
		}
	}
	add(c.Commands, MethodCommand)
	add(c.Trashed, MethodTrash)
	add(c.Deleted, MethodDelete)
	// numbering must end at -0 for the oldest: reverse so "-0" is the last (oldest) entry
	slices.Reverse(out)
	for i := range out {
		out[i].ID = fmt.Sprintf("1-%d", len(out)-1-i)
	}
	return out
}
