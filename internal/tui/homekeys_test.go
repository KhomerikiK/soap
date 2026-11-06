package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

// devFixture is junkFixture with Dev items: Simulators and Package caches aren't safe and hold
// 5 GB or more, Xcode is safe.
func devFixture() *fakeCleanup {
	fc := junkFixture()
	d := model.ScreenDev
	fc.updates[d] = []model.ScanUpdate{{Screen: d, Finder: "Simulators", Done: 1, Total: 1, Items: []model.CleanItem{
		{ID: "dev/sim/a", Screen: d, Group: "Simulators", Name: "iOS 17.0", Size: 23 * gib, Method: model.MethodDelete},
		{ID: "dev/xcode/dd", Screen: d, Group: "Xcode", Name: "DerivedData", Size: 9 * gib, Safe: true, Method: model.MethodDelete},
		{ID: "dev/pkg/npm", Screen: d, Group: "Package caches", Name: "npm", Size: 8 * gib, Method: model.MethodDelete},
	}}}
	return fc
}

// selectLine presses j until the cursor is on the line with this key.
func selectLine(t *testing.T, h *harness, key string) {
	t.Helper()
	for range 40 {
		if h.m.home.cursor == key {
			return
		}
		h.press("j")
	}
	t.Fatalf("no line %q among %v", key, h.m.homeKeys())
}

func TestHomeCursorMovesThroughEveryLine(t *testing.T) {
	h := launch(t, devFixture())
	want := []string{"memory", "ports", "space/dev/Simulators", "space/dev/safe", "space/dev/Package caches", "space/junk/safe", "space/busy/Google Chrome",
		"free/junk", "free/dev", "free/apps", "free/disk", "free/dupes", keyTop, keyNetwork, keyPorts, keyHistory}
	if got := h.m.homeKeys(); !slices.Equal(got, want) {
		t.Fatalf("lines = %v\nwant    %v", got, want)
	}
	h.press("k")
	if h.m.home.cursor != "memory" {
		t.Errorf("k on the first line stays: %q", h.m.home.cursor)
	}
	for _, key := range want[1:] {
		h.press("j")
		if h.m.home.cursor != key {
			t.Fatalf("cursor = %q, want %q", h.m.home.cursor, key)
		}
	}
	h.press("down")
	if h.m.home.cursor != keyHistory {
		t.Errorf("j on the last line stays: %q", h.m.home.cursor)
	}
	h.press("up")
	if h.m.home.cursor != keyPorts || strings.Contains(h.screen(), "▸") {
		t.Errorf("cursor = %q; the arrow marks only a selected finding:\n%s", h.m.home.cursor, h.screen())
	}
}

func TestHomeCursorFollowsItsLine(t *testing.T) {
	h := launch(t, devFixture())
	selectLine(t, h, "space/junk/safe")
	snap := portsSnapshot()
	snap.Memory.Pressure = model.PressureNormal // the first finding goes away
	h.send(snapshotMsg{snap: snap})
	if h.m.home.cursor != "space/junk/safe" || h.m.home.index != 4 {
		t.Errorf("the cursor stays on its line: %q at %d", h.m.home.cursor, h.m.home.index)
	}
	cs := h.m.checks[model.ScreenJunk]
	for id, it := range cs.items {
		it.Safe = false
		cs.items[id] = it
	}
	h.send(redrawMsg{})
	if h.m.home.index != 4 || h.m.home.cursor != "space/busy/Google Chrome" {
		t.Errorf("when its line goes, the cursor keeps its place: %q at %d", h.m.home.cursor, h.m.home.index)
	}
}

func TestHomeEnterOpensTheGroupOfAFinding(t *testing.T) {
	h := launch(t, devFixture())
	selectLine(t, h, "space/dev/Package caches")
	h.pump(h.press("enter"))
	if h.m.tab != model.TabDev {
		t.Fatalf("enter opens tab %d, want Dev", h.m.tab)
	}
	if !h.m.checks[model.ScreenDev].open["Package caches"] || h.m.sel[model.TabDev] != 2 {
		t.Errorf("open %v, cursor on row %d", h.m.checks[model.ScreenDev].open, h.m.sel[model.TabDev])
	}
	if s := h.screen(); !strings.Contains(s, "▾ [ ] Package caches") || !strings.Contains(s, "npm") {
		t.Errorf("the group is open:\n%s", s)
	}
}

func TestHomeEnterPutsTheCursorOnAPort(t *testing.T) {
	h := launch(t, junkFixture())
	selectLine(t, h, "ports")
	h.press("enter")
	if h.m.tab != model.TabPorts || h.m.sel[model.TabPorts] != 1 {
		t.Errorf("tab %d, cursor on row %d; :3306 is the second row", h.m.tab, h.m.sel[model.TabPorts])
	}
}

func TestJumpToAGroupOrPortThatIsGone(t *testing.T) {
	h := launch(t, devFixture())
	h.m.sel[model.TabDev] = 2
	h.m.jump(model.TabDev, "Gone", 0)
	if h.m.tab != model.TabDev || h.m.sel[model.TabDev] != 0 || h.m.checks[model.ScreenDev].open["Gone"] {
		t.Errorf("tab %d, cursor %d, open %v", h.m.tab, h.m.sel[model.TabDev], h.m.checks[model.ScreenDev].open)
	}
	h.m.sel[model.TabPorts] = 2
	h.m.jump(model.TabPorts, "", 8888)
	if h.m.sel[model.TabPorts] != 0 {
		t.Errorf("cursor %d", h.m.sel[model.TabPorts])
	}
}

func TestHomeEnterOnTheOtherLines(t *testing.T) {
	fc := devFixture()
	fc.history = []model.HistoryEntry{{ID: "1-0", Name: "trivy", Method: model.MethodDelete, Result: model.ResultDone, Size: gib}}
	fd := &fakeDisk{}
	for _, tt := range []struct {
		key string
		tab int
	}{{"free/junk", model.TabJunk}, {"free/apps", model.TabApps}, {keyTop, model.TabMemory}, {keyNetwork, model.TabConnections}, {keyPorts, model.TabPorts}} {
		h := launch(t, fc, fd.deps)
		selectLine(t, h, tt.key)
		h.pump(h.press("enter"))
		if h.m.tab != tt.tab {
			t.Errorf("%s opens tab %d, want %d", tt.key, h.m.tab, tt.tab)
		}
	}
	h := launch(t, fc, fd.deps)
	selectLine(t, h, "free/disk")
	h.pump(h.press("enter"))
	if h.m.tab != model.TabDisk || len(fd.walks) != 1 {
		t.Errorf("6 Disk opens Disk and starts the walk (tab %d, walks %d)", h.m.tab, len(fd.walks))
	}
	h = launch(t, fc, fd.deps)
	selectLine(t, h, keyHistory)
	h.pump(h.press("enter"))
	if s := h.screen(); h.m.tab != model.TabJunk || h.m.checks[model.ScreenJunk].mode != modeHistory || !strings.Contains(s, "trivy") {
		t.Errorf("History opens Junk's history list:\n%s", s)
	}
}

func TestHomeEnterOnAHintDoesNothing(t *testing.T) {
	fc := junkFixture()
	fc.history = []model.HistoryEntry{{ID: "1-0", Name: "backup", Method: model.MethodTrash, Trashed: []string{"/x"}, Result: model.ResultDone, Size: 5 * gib}}
	fc.updates[model.ScreenJunk][2].Notes[0].Code = model.NoteNoFullDiskAccess
	h := launch(t, fc)
	selectLine(t, h, "space/trash")
	if cmd := h.press("enter"); cmd != nil || h.m.tab != model.TabHome {
		t.Errorf("without Full Disk Access the Trash line says what to do instead (tab %d)", h.m.tab)
	}
	if s := h.screen(); !strings.Contains(s, "5.0 GB that soap moved still sits in the Trash") || !strings.Contains(s, "empty it in Finder") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestHomeRScansAgain(t *testing.T) {
	fc := junkFixture()
	h := launch(t, fc)
	h.pump(h.press("r"))
	if want := slices.Repeat(lightScans, 2); !slices.Equal(fc.screens, want) {
		t.Errorf("scans = %v, want %v", fc.screens, want)
	}
	if !strings.Contains(h.screen(), "Scanning Junk, Dev and Apps again…") {
		t.Errorf("screen:\n%s", h.screen())
	}
	h.press("4", "c")
	h.send(h.press("y")()) // the clean-up runs; its events aren't delivered
	h.press("0")
	if cmd := h.press("r"); cmd != nil || len(fc.scans) != 6 {
		t.Errorf("r is refused during a clean-up (scans: %d)", len(fc.scans))
	}
	if !strings.Contains(h.screen(), "A clean-up is running. Scan again when it's done.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestSpinnerRunsOnlyWhileHomeShowsAJob(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	if h.m.home.spinning {
		t.Fatal("nothing runs, so nothing spins")
	}
	if cmd := h.send(h.m.Init()()); cmd == nil || !h.m.home.spinning { // Init's first command; the scans run
		t.Fatal("the spinner starts while Home shows a scan")
	}
	h.send(spinMsg{})
	if h.m.home.frame != 1 || !h.m.home.spinning {
		t.Errorf("a tick moves the spinner and asks for the next: frame %d, spinning %v", h.m.home.frame, h.m.home.spinning)
	}
	h.press("1")
	h.send(spinMsg{})
	if h.m.home.spinning {
		t.Error("no tick is asked for off Home")
	}
	h.press("0")
	if !h.m.home.spinning {
		t.Error("back on Home, it spins again")
	}
	for _, cs := range h.m.checks {
		cs.scanning = false
	}
	h.send(spinMsg{})
	if h.m.home.spinning {
		t.Error("with nothing running, the spinner stops")
	}
}

func TestHomeAttentionScrolls(t *testing.T) {
	snap := portsSnapshot()
	snap.DiskFree, snap.DiskTotal = 30*gib, 460*gib
	snap.Memory.SwapUsed = 5 * gib
	h := launch(t, devFixture())
	h.send(snapshotMsg{snap: snap})
	n := len(h.m.home.view.findings)
	if n < 7 {
		t.Fatalf("%d findings, want 7 or more", n)
	}
	h.press("j", "j", "j", "j")
	if s := h.screen(); !strings.Contains(s, "1–5 of "+strconv.Itoa(n)) {
		t.Errorf("the fifth finding is still in the window:\n%s", s)
	}
	h.press("j")
	if s := h.screen(); h.m.home.start != 1 || !strings.Contains(s, "2–6 of "+strconv.Itoa(n)) {
		t.Errorf("the list scrolls as the cursor moves past its last line (start %d):\n%s", h.m.home.start, s)
	}
	for range n {
		h.press("j")
	}
	if h.m.home.start != n-5 {
		t.Errorf("on the free lines the window keeps its place: start %d", h.m.home.start)
	}
	for range 2 * n {
		h.press("k")
	}
	if h.m.home.start != 0 {
		t.Errorf("back at the top: start %d", h.m.home.start)
	}
}
