package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

const gib = uint64(1 << 30)

// fakeCleanup stands in for scanning, cleaning and history.
type fakeCleanup struct {
	mu       sync.Mutex
	updates  map[model.Screen][]model.ScanUpdate
	scans    []context.Context
	screens  []model.Screen // the screen of each scan, in order
	cleaned  [][]model.CleanItem
	cleanCtx context.Context
	outcome  func(model.CleanItem) model.CleanResult
	history  []model.HistoryEntry
	restored []string
}

func (f *fakeCleanup) deps(d *Deps) {
	d.Scan = func(ctx context.Context, s model.Screen) <-chan model.ScanUpdate {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.scans = append(f.scans, ctx)
		f.screens = append(f.screens, s)
		ch := make(chan model.ScanUpdate, len(f.updates[s]))
		for _, u := range f.updates[s] {
			ch <- u
		}
		close(ch)
		return ch
	}
	d.Clean = func(ctx context.Context, items []model.CleanItem, report func(model.CleanResult)) error {
		f.mu.Lock()
		f.cleaned = append(f.cleaned, items)
		f.cleanCtx = ctx
		f.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, it := range items {
			r := model.CleanResult{Item: it, Result: "done", Freed: it.Size}
			if f.outcome != nil {
				r = f.outcome(it)
			}
			report(r)
		}
		return nil
	}
	d.History = func() ([]model.HistoryEntry, error) { return f.history, nil }
	d.Restore = func(id string) (model.HistoryEntry, error) {
		f.mu.Lock()
		f.restored = append(f.restored, id)
		f.mu.Unlock()
		for _, e := range f.history {
			if e.ID == id {
				e.Restored = true
				return e, nil
			}
		}
		return model.HistoryEntry{}, errors.New("no such entry")
	}
}

// pump runs cmd and every command that follows from it, the way Bubble Tea would, until nothing
// is left. Batches run one command at a time; refresh timers and spinner ticks are dropped.
func (h *harness) pump(cmd tea.Cmd) {
	h.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			h.t.Fatal("pump: too many steps")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil, tickMsg, spinMsg:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			queue = append(queue, h.send(msg))
		}
	}
}

func junkFixture() *fakeCleanup {
	j := model.ScreenJunk
	return &fakeCleanup{updates: map[model.Screen][]model.ScanUpdate{
		j: {
			{Screen: j, Finder: "App caches", Done: 1, Total: 3, Items: []model.CleanItem{
				{ID: "junk/caches/JetBrains/PhpStorm2025.2", Screen: j, Group: "App caches", Name: "JetBrains PhpStorm2025.2", Detail: "rebuilt automatically", Size: 3 * gib, Safe: true, App: "PhpStorm", Method: model.MethodDelete},
				{ID: "junk/caches/Google", Screen: j, Group: "App caches", Name: "Google", Size: 2 * gib, Safe: true, App: "Google Chrome", Busy: true, Method: model.MethodDelete},
				{ID: "junk/caches/trivy", Screen: j, Group: "App caches", Name: "trivy", Size: 1 * gib, Safe: true, Method: model.MethodDelete},
			}},
			{Screen: j, Finder: "Logs and crash reports", Done: 2, Total: 3, Items: []model.CleanItem{
				{ID: "junk/logs/DiagnosticReports", Screen: j, Group: "Logs and crash reports", Name: "DiagnosticReports", Detail: "crash reports", Size: 200 << 20, Safe: true, Method: model.MethodDelete},
			}},
			{Screen: j, Finder: "Trash", Done: 3, Total: 3, Notes: []model.Note{{Screen: j, Text: "Trash and iOS backups are hidden: give your terminal Full Disk Access."}}},
		},
		model.ScreenDev: {
			{Screen: model.ScreenDev, Finder: "Simulators", Done: 1, Total: 1, Err: errors.New("xcrun broke")},
		},
	}}
}

func openJunk(t *testing.T, fc *fakeCleanup) *harness {
	t.Helper()
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.pump(h.press("4"))
	return h
}

func TestJunkScanFillsTheScreen(t *testing.T) {
	h := openJunk(t, junkFixture())
	s := h.screen()
	for _, want := range []string{"4 Junk", "5 Dev", "6 Disk", "Junk", "found 6.2 GB · 4.2 GB ticked", "Full Disk Access", "▸ [x] App caches", "6.0 GB", "3 items", "Logs and crash reports"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "trivy") {
		t.Error("groups start closed")
	}
}

func TestTickingItemsAndGroups(t *testing.T) {
	h := openJunk(t, junkFixture())
	h.press("enter")
	s := h.screen()
	for _, want := range []string{"▾ [x] App caches", "[x] JetBrains PhpStorm2025.2", "[ ] Google", "Quit Google Chrome first", "[x] trivy"} {
		if !strings.Contains(s, want) {
			t.Errorf("open group is missing %q:\n%s", want, s)
		}
	}
	h.press("space")
	if s := h.screen(); !strings.Contains(s, "▾ [ ] App caches") || !strings.Contains(s, "200 MB ticked") {
		t.Errorf("space on a group unticks all of it:\n%s", s)
	}
	h.press("space")
	if !strings.Contains(h.screen(), "▾ [x] App caches") {
		t.Error("space again ticks it again")
	}
	h.press("j", "j", "space")
	if s := h.screen(); !strings.Contains(s, "Quit Google Chrome first.") || !strings.Contains(s, "[ ] Google") {
		t.Errorf("a busy item can't be ticked:\n%s", s)
	}
	h.press("k", "space")
	if !strings.Contains(h.screen(), "▾ [-] App caches") {
		t.Error("unticking one item makes the group partly ticked")
	}
	h.press("a")
	if s := h.screen(); !strings.Contains(s, "4.2 GB ticked") || !strings.Contains(s, "Ticked the safe items only.") {
		t.Errorf("a resets to the safe items:\n%s", s)
	}
}

func TestCleanShowsPreviewThenCleansExactlyTheTickedItems(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	h.press("c")
	s := h.screen()
	for _, want := range []string{"Clean-up preview", "3 items", "Delete 3 for good? Frees 4.2 GB.", "[y]", "delete"} {
		if !strings.Contains(s, want) {
			t.Errorf("preview is missing %q:\n%s", want, s)
		}
	}
	h.press("n")
	if s := h.screen(); !strings.Contains(s, "Cancelled.") || !strings.Contains(s, "found 6.2 GB") {
		t.Errorf("n goes back to the list:\n%s", s)
	}
	if len(fc.cleaned) != 0 {
		t.Fatal("nothing is cleaned without a yes")
	}
	h.press("c")
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 {
		t.Fatalf("cleaned %d times", len(fc.cleaned))
	}
	var got []string
	for _, it := range fc.cleaned[0] {
		got = append(got, it.ID)
	}
	want := []string{"junk/caches/JetBrains/PhpStorm2025.2", "junk/caches/trivy", "junk/logs/DiagnosticReports"}
	if !slices.Equal(got, want) {
		t.Errorf("cleaned %v, want %v", got, want)
	}
	if !strings.Contains(h.screen(), "Freed 4.2 GB.") {
		t.Errorf("no result:\n%s", h.screen())
	}
	if len(fc.scans) != 2 {
		t.Errorf("the screen should scan again after cleaning (scans: %d)", len(fc.scans))
	}
}

func TestCleanReportsSkippedAndFailedItems(t *testing.T) {
	fc := junkFixture()
	fc.outcome = func(it model.CleanItem) model.CleanResult {
		switch it.Name {
		case "trivy":
			return model.CleanResult{Item: it, Result: "skipped", Message: "trivy is gone since the scan"}
		case "DiagnosticReports":
			return model.CleanResult{Item: it, Result: "failed", Message: "permission denied"}
		}
		return model.CleanResult{Item: it, Result: "done", Freed: it.Size}
	}
	h := openJunk(t, fc)
	h.press("c")
	h.pump(h.press("y"))
	s := h.screen()
	for _, want := range []string{"Freed 3.0 GB.", "1 failed: DiagnosticReports (permission denied).", "1 skipped: trivy (trivy is gone since the scan)."} {
		if !strings.Contains(s, want) {
			t.Errorf("result is missing %q:\n%s", want, s)
		}
	}
}

func TestEscStopsCleaningAfterTheCurrentItem(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	h.press("c")
	start := h.send(h.press("y")())
	if !strings.Contains(h.screen(), "Cleaning") {
		t.Fatalf("not cleaning:\n%s", h.screen())
	}
	h.press("esc")
	if !strings.Contains(h.screen(), "Stopping after this item…") {
		t.Errorf("no stopping message:\n%s", h.screen())
	}
	h.pump(start)
	if fc.cleanCtx == nil || fc.cleanCtx.Err() == nil {
		t.Error("esc should cancel the clean-up")
	}
	if !strings.Contains(h.screen(), "Stopped. Freed 0 B.") {
		t.Errorf("result after stopping:\n%s", h.screen())
	}
}

func TestNothingTicked(t *testing.T) {
	h := openJunk(t, junkFixture())
	h.press("space", "j", "space")
	if cmd := h.press("c"); cmd != nil {
		t.Error("nothing to clean, so nothing should run")
	}
	if !strings.Contains(h.screen(), "Nothing is ticked.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestHistoryAndPutBack(t *testing.T) {
	fc := junkFixture()
	fc.history = []model.HistoryEntry{
		{ID: "t1", Name: "Kote's iPhone backup", Method: model.MethodTrash, Trashed: []string{"/x"}, Result: "done", Size: 5 * gib},
		{ID: "d1", Name: "JetBrains PhpStorm2025.2", Method: model.MethodDelete, Result: "done", Size: 3 * gib},
	}
	h := openJunk(t, fc)
	h.pump(h.press("h"))
	s := h.screen()
	for _, want := range []string{"History", "2 entries", "Kote's iPhone backup", "in the Trash · u put back", "deleted", "u put back"} {
		if !strings.Contains(s, want) {
			t.Errorf("history is missing %q:\n%s", want, s)
		}
	}
	h.pump(h.press("u"))
	if !slices.Equal(fc.restored, []string{"t1"}) || !strings.Contains(h.screen(), "Put back Kote's iPhone backup.") {
		t.Errorf("restored %v:\n%s", fc.restored, h.screen())
	}
	h.press("j")
	if cmd := h.press("u"); cmd != nil || !strings.Contains(h.screen(), "Only items moved to the Trash can be put back.") {
		t.Errorf("a deleted entry can't be put back:\n%s", h.screen())
	}
	h.press("esc")
	if !strings.Contains(h.screen(), "found 6.2 GB") {
		t.Errorf("esc goes back to the list:\n%s", h.screen())
	}
}

func TestScanErrorShowsOnItsGroup(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.pump(h.press("5"))
	if s := h.screen(); !strings.Contains(s, "Simulators") || !strings.Contains(s, "Couldn't check: xcrun broke") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestLeavingTheTabKeepsTheScan(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	scan := h.press("4") // the scan command isn't run, so the scan is still going
	h.press("1")
	if len(fc.scans) != 1 || fc.scans[0].Err() != nil {
		t.Fatal("leaving Junk must not cancel its scan")
	}
	h.pump(scan)
	if cs := h.m.checks[model.ScreenJunk]; cs.scanning || len(cs.items) != 4 {
		t.Errorf("the scan finished on another tab: scanning %v, %d items", cs.scanning, len(cs.items))
	}
	h.pump(h.press("4"))
	if len(fc.scans) != 1 {
		t.Errorf("opening Junk again doesn't scan again (scans: %d)", len(fc.scans))
	}
}

func TestStatusBarShowsFreeDiskSpace(t *testing.T) {
	snap := portsSnapshot()
	snap.DiskFree, snap.DiskTotal = 43*gib, 460*gib
	h := newHarness(t, snap)
	if !strings.Contains(h.screen(), "disk 43.0 GB free") {
		t.Errorf("status bar:\n%s", h.screen())
	}
}

func TestOnlyOneCleanRunsAcrossScreens(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	h.pump(h.press("5"))
	h.press("4", "c")
	h.send(h.press("y")()) // the clean-up on Junk is running; its command isn't run
	h.press("5")
	if cmd := h.press("c"); cmd != nil {
		t.Error("nothing should start while another clean-up runs")
	}
	if s := h.screen(); !strings.Contains(s, "A clean-up is already running.") {
		t.Errorf("no message:\n%s", s)
	}
	if h.m.checks[model.ScreenDev].mode != modeList || h.m.confirm != nil {
		t.Error("Dev should stay on its list, with no question asked")
	}
}

func TestStaleScanUpdatesAreDrainedNotApplied(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.press("4") // generation 1
	ch := make(chan model.ScanUpdate)
	close(ch)
	stale := scanMsg{screen: model.ScreenJunk, gen: 0, ch: ch, update: model.ScanUpdate{Screen: model.ScreenJunk, Finder: "App caches",
		Items: []model.CleanItem{{ID: "junk/old", Screen: model.ScreenJunk, Group: "App caches", Name: "old", Size: gib, Safe: true}}}}
	cmd := h.send(stale)
	if len(h.m.checks[model.ScreenJunk].items) != 0 {
		t.Error("a stale update must not change the items")
	}
	if cmd == nil {
		t.Fatal("the stale channel must still be read, so its sender never blocks")
	}
	if _, ok := cmd().(scanDoneMsg); !ok {
		t.Error("the next read should be scheduled on the same channel")
	}
}

func TestFinishingACleanOnAnotherTabScansAtOnce(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	h.press("1", "j")
	h.press("4", "c")
	start := h.send(h.press("y")())
	h.press("1")
	sel := h.m.sel[h.m.tab]
	h.pump(start)
	if len(fc.scans) != 2 {
		t.Errorf("Junk scans again at once, on whatever tab is open (scans: %d)", len(fc.scans))
	}
	if h.m.sel[h.m.tab] != sel {
		t.Error("finishing a clean must not move the selection of another tab")
	}
	h.pump(h.press("4"))
	if len(fc.scans) != 2 {
		t.Errorf("opening Junk again doesn't scan a third time (scans: %d)", len(fc.scans))
	}
}

func TestPDoesNothingOnChecklistScreens(t *testing.T) {
	h := openJunk(t, junkFixture())
	h.press("p")
	if h.m.paused || strings.Contains(h.screen(), "Paused") {
		t.Error("p has no effect on Junk")
	}
	h.press("1", "p")
	if !h.m.paused {
		t.Error("p still pauses on the part 1 screens")
	}
}

func TestCleanShowsPartialReasons(t *testing.T) {
	fc := junkFixture()
	fc.outcome = func(it model.CleanItem) model.CleanResult {
		if it.Name == "trivy" {
			return model.CleanResult{Item: it, Result: "partial", Freed: 1 << 20, Message: "Partly deleted: 1.1 GB of 3.3 GB freed. 12 files couldn't be removed."}
		}
		return model.CleanResult{Item: it, Result: "done", Freed: it.Size}
	}
	h := openJunk(t, fc)
	h.press("c")
	h.pump(h.press("y"))
	want := "1 partly deleted: trivy (Partly deleted: 1.1 GB of 3.3 GB freed. 12 files couldn't be removed.)."
	if !strings.Contains(h.screen(), want) {
		t.Errorf("missing %q:\n%s", want, h.screen())
	}
}

func TestCleanErrorKeepsTheSummaryOfReportedItems(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) {
		d.Clean = func(_ context.Context, items []model.CleanItem, report func(model.CleanResult)) error {
			report(model.CleanResult{Item: items[0], Result: "done", Freed: items[0].Size})
			return errors.New("history file broke")
		}
	})
	h.pump(h.press("4"))
	h.press("c")
	h.pump(h.press("y"))
	s := h.screen()
	if !strings.Contains(s, "Freed 3.0 GB.") || !strings.Contains(s, "History file broke.") {
		t.Errorf("summary and error should both show:\n%s", s)
	}
	if strings.Contains(s, "Freed 4.2 GB") {
		t.Errorf("unreported items must not count as cleaned:\n%s", s)
	}
}

func TestGroupWithItemsAndAnErrorShowsBoth(t *testing.T) {
	d := model.ScreenDev
	fc := &fakeCleanup{updates: map[model.Screen][]model.ScanUpdate{d: {
		{Screen: d, Finder: "Simulators", Done: 1, Total: 1, Err: errors.New("xcrun broke"), Items: []model.CleanItem{
			{ID: "dev/sim/a", Screen: d, Group: "Simulators", Name: "iPhone 15", Size: gib, Safe: true, Method: model.MethodDelete},
		}},
	}}}
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.pump(h.press("5"))
	h.press("enter")
	s := h.screen()
	for _, want := range []string{"Couldn't check: xcrun broke", "iPhone 15"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
}

// trashFixture is the Junk fixture plus a Trash item that is never pre-ticked.
func trashFixture() *fakeCleanup {
	fc := junkFixture()
	j := model.ScreenJunk
	fc.updates[j][2] = model.ScanUpdate{Screen: j, Finder: "Trash", Done: 3, Total: 3, Items: []model.CleanItem{
		{ID: "junk/trash", Screen: j, Group: "Trash", Name: "Trash", Size: gib, Method: model.MethodDelete},
	}}
	return fc
}

func TestCleanedItemsComeBackUnticked(t *testing.T) {
	fc := trashFixture()
	h := openJunk(t, fc)
	cs := h.m.checks[model.ScreenJunk]
	if cs.ticked["junk/trash"] {
		t.Fatal("the Trash starts unticked")
	}
	cs.ticked["junk/trash"] = true
	h.press("c")
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 || !slices.ContainsFunc(fc.cleaned[0], func(it model.CleanItem) bool { return it.ID == "junk/trash" }) {
		t.Fatalf("the Trash should have been cleaned: %v", fc.cleaned)
	}
	if h.m.checks[model.ScreenJunk].ticked["junk/trash"] {
		t.Error("a cleaned item came back ticked after the rescan")
	}
	if !strings.Contains(h.screen(), "4.2 GB ticked") {
		t.Errorf("only the safe items are ticked again:\n%s", h.screen())
	}
}

func TestFailedItemsStayTicked(t *testing.T) {
	fc := trashFixture()
	fc.outcome = func(it model.CleanItem) model.CleanResult {
		if it.ID == "junk/trash" {
			return model.CleanResult{Item: it, Result: "failed", Message: "nope"}
		}
		return model.CleanResult{Item: it, Result: "done", Freed: it.Size}
	}
	h := openJunk(t, fc)
	h.m.checks[model.ScreenJunk].ticked["junk/trash"] = true
	h.press("c")
	h.pump(h.press("y"))
	if !h.m.checks[model.ScreenJunk].ticked["junk/trash"] {
		t.Error("an item that failed keeps the user's tick")
	}
}

func TestSafeOnlyClearsManualTicks(t *testing.T) {
	h := openJunk(t, trashFixture())
	cs := h.m.checks[model.ScreenJunk]
	cs.ticked["junk/trash"] = true
	cs.ticked["junk/old"] = true // an item that is no longer listed
	h.press("a")
	for id, on := range cs.ticked {
		it, listed := cs.items[id]
		if on && (!listed || !it.PreTicked()) {
			t.Errorf("%s is still ticked after a", id)
		}
	}
}

func TestFinderUpdateDropsTicksOfItemsThatAreGone(t *testing.T) {
	h := openJunk(t, junkFixture())
	cs := h.m.checks[model.ScreenJunk]
	cs.items["junk/caches/gone"] = model.CleanItem{ID: "junk/caches/gone", Group: "App caches", Name: "gone"}
	cs.ticked["junk/caches/gone"] = true
	ch := make(chan model.ScanUpdate)
	close(ch)
	h.send(scanMsg{screen: model.ScreenJunk, gen: cs.gen, ch: ch, update: model.ScanUpdate{Screen: model.ScreenJunk, Finder: "App caches", Items: []model.CleanItem{
		{ID: "junk/caches/trivy", Screen: model.ScreenJunk, Group: "App caches", Name: "trivy", Size: gib, Safe: true, Method: model.MethodDelete},
	}}})
	if _, ok := cs.ticked["junk/caches/gone"]; ok {
		t.Error("a tick for an item the finder no longer reports should be dropped")
	}
	if !cs.ticked["junk/caches/trivy"] {
		t.Error("the remaining item keeps its tick")
	}
}

func TestQDoesNotQuitWhileCleaning(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	h.press("c")
	start := h.send(h.press("y")())
	cmd := h.press("q")
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q must not quit during a clean-up")
		}
	}
	if !strings.Contains(h.screen(), "A clean-up is running. Press esc to stop it after the current item.") {
		t.Errorf("no warning:\n%s", h.screen())
	}
	if _, quit := h.press("ctrl+c")().(tea.QuitMsg); !quit {
		t.Error("ctrl+c still quits")
	}
	h.pump(start)
	if _, quit := h.press("q")().(tea.QuitMsg); !quit {
		t.Error("q quits again once the clean-up is over")
	}
}
