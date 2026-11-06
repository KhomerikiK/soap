package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// dupeSet builds the items of one Dupes set: one per path, keeping the first.
func dupeSet(set string, size, unshared uint64, paths ...string) []model.CleanItem {
	made := time.Date(2025, 3, 2, 12, 0, 0, 0, time.UTC)
	var copies []model.Copy
	for i, p := range paths {
		copies = append(copies, model.Copy{Path: p, Made: made, Keep: i == 0})
	}
	var items []model.CleanItem
	for i, p := range paths {
		detail := "made 2 Mar 2025"
		if strings.Contains(p, "CloudDocs") {
			detail += " · in iCloud Drive"
		}
		u := unshared
		if i == 0 {
			u = size
		}
		items = append(items, model.CleanItem{ID: "dupes/" + strings.TrimPrefix(p, home+"/"), Screen: model.ScreenDupes, Group: "Copies",
			Name: model.HomePath(home, p), Detail: detail, Size: size, Unshared: u, Method: model.MethodTrash, Paths: []string{p},
			Root: home, Stamp: &model.Stamp{Inode: uint64(i + 1)}, Set: set, Copies: copies, Keep: i == 0})
	}
	return items
}

// dupesFixture streams two sets: three plain copies of trip.mov and two clones of a.zip.
func dupesFixture() *fakeCleanup {
	d := model.ScreenDupes
	trip := dupeSet("trip", 4*gib, 4*gib, home+"/Movies/Trips/trip.mov", home+"/Downloads/trip.mov", home+"/Library/Mobile Documents/com~apple~CloudDocs/trip.mov")
	zip := dupeSet("zip", 9*gib, 0, home+"/Documents/a.zip", home+"/Downloads/a.zip")
	return &fakeCleanup{updates: map[model.Screen][]model.ScanUpdate{d: {
		{Screen: d, Finder: "Copies", Total: 1},
		{Screen: d, Finder: "Copies", Add: true, Items: zip, Status: "Hashing 1 of 2 sets · 18.0 GB read", Total: 1},
		{Screen: d, Finder: "Copies", Add: true, Items: trip, Status: "Hashing 2 of 2 sets · 30.0 GB read", Total: 1},
		{Screen: d, Finder: "Copies", Add: true, Done: 1, Total: 1, Notes: []model.Note{{Screen: d, Text: "1 file couldn't be read."}}},
	}}}
}

func openDupes(t *testing.T, fc *fakeCleanup) *harness {
	t.Helper()
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) { d.Home = home })
	h.pump(h.press("7"))
	return h
}

func TestDupesShowsSetsAndTicksNothing(t *testing.T) {
	h := openDupes(t, dupesFixture())
	s := h.screen()
	for _, want := range []string{"7 Dupes", "8 Apps", "2 sets · frees up to 8.0 GB", "▾ trip.mov", "3 copies · 4.0 GB each · frees up to 8.0 GB",
		"[ ] keep  ~/Movies/Trips", "[ ]       ~/Downloads", "[ ]       iCloud Drive  ", "made 2 Mar 2025", "in iCloud Drive", "▾ a.zip", "frees up to 0 B", "1 file couldn't be read."} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "trip.mov") > strings.Index(s, "a.zip") {
		t.Errorf("sets that free almost nothing come last:\n%s", s)
	}
	for id, on := range h.m.checks[model.ScreenDupes].ticked {
		if on {
			t.Errorf("%s is ticked after a scan", id)
		}
	}
}

func TestDupesKTicksEveryCopyButTheKeptOne(t *testing.T) {
	fc := dupesFixture()
	h := openDupes(t, fc)
	h.press("down", "k")
	cs := h.m.checks[model.ScreenDupes]
	if cs.ticked["dupes/Movies/Trips/trip.mov"] || !cs.ticked["dupes/Downloads/trip.mov"] || cs.ticked["dupes/Downloads/a.zip"] {
		t.Errorf("ticked = %v", cs.ticked)
	}
	if h.m.sel[h.m.tab] != 1 {
		t.Error("k ticks on Dupes; it doesn't move the cursor")
	}
	h.press("K")
	if !cs.ticked["dupes/Downloads/a.zip"] || cs.ticked["dupes/Documents/a.zip"] {
		t.Errorf("K ticks every set: %v", cs.ticked)
	}
	h.press("c")
	if s := h.screen(); !strings.Contains(s, "Move 3 to the Trash?") {
		t.Fatalf("preview:\n%s", s)
	}
	h.pump(h.press("y"))
	var got []string
	for _, it := range fc.cleaned[0] {
		got = append(got, it.ID)
	}
	slices.Sort(got)
	want := []string{"dupes/Downloads/a.zip", "dupes/Downloads/trip.mov", "dupes/Library/Mobile Documents/com~apple~CloudDocs/trip.mov"}
	if !slices.Equal(got, want) {
		t.Errorf("cleaned %v, want %v", got, want)
	}
	if s := h.screen(); !strings.Contains(s, "No copies found.") {
		t.Errorf("each set has one copy left, so no set is shown:\n%s", s)
	}
	if len(fc.scans) != 1 {
		t.Errorf("a clean-up doesn't hash everything again (scans: %d)", len(fc.scans))
	}
}

func TestDupesCleanKeepsTheRestOfASet(t *testing.T) {
	fc := dupesFixture()
	h := openDupes(t, fc)
	h.press("down", "down", "space") // ~/Downloads/trip.mov
	h.press("c")
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 || len(fc.cleaned[0]) != 1 || fc.cleaned[0][0].ID != "dupes/Downloads/trip.mov" {
		t.Fatalf("cleaned %+v", fc.cleaned)
	}
	s := h.screen()
	if _, still := h.m.checks[model.ScreenDupes].items["dupes/Downloads/trip.mov"]; still || !strings.Contains(s, "2 copies · 4.0 GB each · frees up to 4.0 GB") {
		t.Errorf("the set keeps its other two copies:\n%s", s)
	}
	for _, it := range h.m.checks[model.ScreenDupes].items {
		if it.Set == "trip" && len(it.Copies) != 2 {
			t.Errorf("%s still lists %d copies; the one in the Trash is dropped", it.ID, len(it.Copies))
		}
	}
}

func TestDupesRefusesToTickEveryCopy(t *testing.T) {
	h := openDupes(t, dupesFixture())
	h.press("down", "k", "space") // the kept copy of trip.mov, the last one unticked
	if s := h.screen(); !strings.Contains(s, "Keep at least one copy of trip.mov.") {
		t.Errorf("screen:\n%s", s)
	}
	if h.m.checks[model.ScreenDupes].ticked["dupes/Movies/Trips/trip.mov"] {
		t.Error("the last copy must stay unticked")
	}
	h.press("down", "space", "up", "space") // untick one, then the kept copy may be ticked
	if !h.m.checks[model.ScreenDupes].ticked["dupes/Movies/Trips/trip.mov"] {
		t.Error("the kept copy can be ticked while another copy stays")
	}
}

func TestDupesCleanRefusesAWholeSet(t *testing.T) {
	fc := dupesFixture()
	h := openDupes(t, fc)
	cs := h.m.checks[model.ScreenDupes]
	cs.ticked["dupes/Documents/a.zip"], cs.ticked["dupes/Downloads/a.zip"] = true, true
	if cmd := h.press("c"); cmd != nil || h.m.confirm != nil {
		t.Error("a set with every copy ticked must not be offered")
	}
	if !strings.Contains(h.screen(), "Keep at least one copy of a.zip.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestDupesScanRunsOnOtherTabsAndBlocksCleanUps(t *testing.T) {
	fc := dupesFixture()
	for s, u := range junkFixture().updates {
		fc.updates[s] = u
	}
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) { d.Home = home })
	scan := h.press("7") // the scan's command isn't run, so it's still going
	h.pump(h.press("4"))
	if fc.scans[0].Err() != nil {
		t.Fatal("leaving Dupes must not stop its scan")
	}
	if !strings.Contains(h.screen(), "finding copies") {
		t.Errorf("the status bar shows the scan:\n%s", h.screen())
	}
	if cmd := h.press("c"); cmd != nil || h.m.confirm != nil || !strings.Contains(h.screen(), "Wait for the Dupes scan to finish") {
		t.Errorf("no clean-up starts during the scan:\n%s", h.screen())
	}
	h.press("7", "esc")
	if fc.scans[0].Err() == nil || !strings.Contains(h.screen(), "Stopped looking for copies.") {
		t.Errorf("esc stops the scan:\n%s", h.screen())
	}
	h.pump(scan)
	if h.m.checks[model.ScreenDupes].scanning {
		t.Error("the scan is over")
	}
}

func TestDupesEnterClosesASet(t *testing.T) {
	h := openDupes(t, dupesFixture())
	h.press("enter")
	if s := h.screen(); !strings.Contains(s, "▸ trip.mov") || strings.Contains(s, "~/Movies/Trips") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDupesEmptyTextWhenTheScanWaitsForACleanUp(t *testing.T) {
	fc := dupesFixture()
	for s, u := range junkFixture().updates {
		fc.updates[s] = u
	}
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) { d.Home = home })
	h.pump(h.press("4"))
	h.press("c")
	h.run(h.press("y")) // the clean-up is running; its events aren't delivered
	if !h.m.cleaning() {
		t.Fatal("a clean-up should be running")
	}
	h.press("7")
	cs := h.m.checks[model.ScreenDupes]
	if cs.started || cs.scanning {
		t.Fatal("the scan must not start during a clean-up")
	}
	if s := h.screen(); !strings.Contains(s, "The scan starts when the clean-up is done. Press r then.") || strings.Contains(s, "Looking for copies…") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDupesErrorLinesAreSorted(t *testing.T) {
	h := openDupes(t, dupesFixture())
	cs := h.m.checks[model.ScreenDupes]
	cs.errs["Zed"], cs.errs["Alpha"], cs.errs["Mid"] = "z broke", "a broke", "m broke"
	s := h.screen()
	a, m, z := strings.Index(s, "Couldn't check Alpha"), strings.Index(s, "Couldn't check Mid"), strings.Index(s, "Couldn't check Zed")
	if a < 0 || m < a || z < m {
		t.Errorf("error lines must be in key order:\n%s", s)
	}
}

func TestDupesRescanLeavesNothingTicked(t *testing.T) {
	fc := dupesFixture()
	h := openDupes(t, fc)
	h.press("K")
	cs := h.m.checks[model.ScreenDupes]
	if !cs.ticked["dupes/Downloads/trip.mov"] {
		t.Fatal("K should tick")
	}
	h.pump(h.press("r"))
	if len(fc.scans) != 2 {
		t.Fatalf("scans = %d", len(fc.scans))
	}
	for id, on := range cs.ticked {
		if on {
			t.Errorf("%s is still ticked after a rescan", id)
		}
	}
	if len(model.WholeSets(cs.tickedItems(false))) > 0 {
		t.Error("a set is fully ticked")
	}
}

func TestDupesRescanWhileScanningIsRefused(t *testing.T) {
	fc := dupesFixture()
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) { d.Home = home })
	h.press("7") // the scan's command isn't run, so it's still going
	if cmd := h.press("r"); cmd != nil {
		t.Error("r while scanning must not start another scan")
	}
	if len(fc.scans) != 1 || fc.scans[0].Err() != nil {
		t.Errorf("the first scan keeps running (scans: %d)", len(fc.scans))
	}
	if !strings.Contains(h.screen(), "Still looking for copies.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestDupesPreviewWarnsAboutICloudDrive(t *testing.T) {
	h := openDupes(t, dupesFixture())
	h.press("down", "down", "space") // ~/Downloads/trip.mov
	h.press("c")
	if s := h.screen(); !strings.Contains(s, "Move 1 to the Trash?") || strings.Contains(s, model.ICloudWarning) {
		t.Fatalf("no warning without a copy in iCloud Drive:\n%s", s)
	}
	h.press("n")
	h.press("up", "up", "up", "up", "up")
	h.press("down", "down", "down", "space") // the copy in iCloud Drive
	h.press("c")
	if s := h.screen(); !strings.Contains(s, "Move 2 to the Trash?") || !strings.Contains(s, model.ICloudWarning) {
		t.Errorf("the preview warns about iCloud Drive:\n%s", s)
	}
}
