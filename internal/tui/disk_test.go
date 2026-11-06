package tui

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/model"
)

const home = "/Users/me"

// fakeDisk stands in for the Disk walk, folder listings and measuring again. The home folder
// holds Projects (with app inside), Documents, Tool.app, .cache and big.iso.
type fakeDisk struct {
	mu         sync.Mutex
	walks      []context.Context
	block      chan struct{} // when set, a walk waits for it or for ctx to end
	remeasured [][]string
	tree       *model.DiskTree
}

func (f *fakeDisk) deps(d *Deps) {
	d.Home = home
	d.Walk = func(ctx context.Context, tree *model.DiskTree) {
		f.mu.Lock()
		f.walks = append(f.walks, ctx)
		f.tree = tree
		block := f.block
		f.mu.Unlock()
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				tree.Finish(true)
				return
			}
		}
		kids := tree.Listed(tree.Top(), 4*gib, 1, []model.DiskChild{{Name: "Projects"}, {Name: "Documents"}, {Name: "Tool.app", Package: "app"}, {Name: ".cache"}})
		inProjects := tree.Listed(kids[0], 2*gib, 3, []model.DiskChild{{Name: "app"}})
		tree.Listed(inProjects[0], 6*gib, 10, nil)
		tree.Listed(kids[1], gib, 2, nil)
		tree.Measured(kids[2], gib, false)
		tree.Listed(kids[3], gib/2, 4, nil)
		tree.Finish(false)
	}
	d.List = func(dir string) ([]model.DiskEntry, error) {
		folder := func(name string) model.DiskEntry {
			return model.DiskEntry{Name: name, Path: dir + "/" + name, Dir: true, Stamp: model.Stamp{Dir: true, Inode: 1}}
		}
		switch dir {
		case home:
			app := folder("Tool.app")
			app.Package = "app"
			return []model.DiskEntry{folder("Projects"), folder("Documents"), app, folder(".cache"),
				{Name: "big.iso", Path: home + "/big.iso", Size: 4 * gib, Stamp: model.Stamp{Size: 4 << 30, Inode: 2}}}, nil
		case home + "/Projects":
			return []model.DiskEntry{folder("app"), {Name: "notes.txt", Path: dir + "/notes.txt", Size: 2 * gib, Stamp: model.Stamp{Inode: 3}}}, nil
		}
		return nil, nil
	}
	d.Remeasure = func(_ context.Context, _ *model.DiskTree, dirs []string) {
		f.mu.Lock()
		f.remeasured = append(f.remeasured, dirs)
		f.mu.Unlock()
	}
}

func openDisk(t *testing.T, fd *fakeDisk, fc *fakeCleanup) *harness {
	t.Helper()
	if fc == nil {
		fc = &fakeCleanup{}
	}
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	h.pump(h.press("6"))
	return h
}

func TestDiskWalksWhenOpenedAndShowsTheHomeFolder(t *testing.T) {
	fd := &fakeDisk{}
	h := openDisk(t, fd, nil)
	s := h.screen()
	for _, want := range []string{"6 Disk", "Disk · ~   14.5 GB", "8.0 GB", "Projects/", "4.0 GB", "big.iso", "Tool.app · app", "Documents/", "██"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, ".cache") {
		t.Error("hidden folders start hidden")
	}
	if len(fd.walks) != 1 {
		t.Errorf("walks = %d", len(fd.walks))
	}
	if strings.Index(s, "Projects/") > strings.Index(s, "big.iso") {
		t.Errorf("rows are sorted by size:\n%s", s)
	}
	h.pump(h.press("1"))
	h.pump(h.press("6"))
	if len(fd.walks) != 1 {
		t.Error("coming back to Disk doesn't walk again; every folder opens at once")
	}
}

func TestDiskOpensFoldersAndGoesUp(t *testing.T) {
	h := openDisk(t, &fakeDisk{}, nil)
	h.pump(h.press("enter")) // Projects is the largest, so it's first
	s := h.screen()
	if !strings.Contains(s, "Disk · ~/Projects") || !strings.Contains(s, "app/") || !strings.Contains(s, "notes.txt") {
		t.Fatalf("Projects isn't open:\n%s", s)
	}
	if !strings.Contains(s, "Disk · ~/Projects   8.0 GB") {
		t.Errorf("the header shows the open folder and its size:\n%s", s)
	}
	h.pump(h.press("j", "j", "backspace"))
	if s := h.screen(); !strings.Contains(s, "Disk · ~   14.5 GB") {
		t.Errorf("backspace goes up:\n%s", s)
	}
	rows := h.m.visibleRows()
	if dr, _ := rows[h.m.sel[h.m.tab]].item.(diskRow); dr.e.Name != "Projects" {
		t.Errorf("the folder you came from is selected, got %+v", dr.e)
	}
	h.press("backspace")
	if !strings.Contains(h.screen(), "Projects/") {
		t.Error("the home folder is the top")
	}
}

func TestDiskPackagesDontOpenAndHiddenFilesToggle(t *testing.T) {
	h := openDisk(t, &fakeDisk{}, nil)
	h.press("j", "j", "j", "enter")
	if s := h.screen(); !strings.Contains(s, "Tool.app is a package: it moves to the Trash as one item, and soap doesn't open it.") {
		t.Errorf("screen:\n%s", s)
	}
	h.press(".")
	if s := h.screen(); !strings.Contains(s, ".cache/") || !strings.Contains(s, "Showing hidden files.") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDiskMarksAndMovesToTheTrash(t *testing.T) {
	fd, fc := &fakeDisk{}, &fakeCleanup{}
	h := openDisk(t, fd, fc)
	h.press("j", "space") // big.iso
	if s := h.screen(); !strings.Contains(s, "✓ 4.0 GB") || !strings.Contains(s, "1 marked · 4.0 GB") {
		t.Fatalf("not marked:\n%s", s)
	}
	h.press("c")
	if s := h.screen(); !strings.Contains(s, "Move 1 to the Trash?") || !strings.Contains(s, "~/big.iso") || !strings.Contains(s, "to the Trash") {
		t.Fatalf("preview:\n%s", s)
	}
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 || len(fc.cleaned[0]) != 1 {
		t.Fatalf("cleaned %+v", fc.cleaned)
	}
	it := fc.cleaned[0][0]
	if it.ID != "disk/big.iso" || it.Method != model.MethodTrash || it.Root != home || it.Stamp == nil || it.Stamp.Inode != 2 {
		t.Errorf("item = %+v", it)
	}
	if len(fd.remeasured) != 1 || !slices.Equal(fd.remeasured[0], []string{home}) {
		t.Errorf("remeasured %v; only the folder that held it", fd.remeasured)
	}
	if len(h.m.checks[model.ScreenDisk].items) != 0 || strings.Contains(h.screen(), "✓ 4.0 GB") {
		t.Error("what went to the Trash is no longer marked")
	}
	if !strings.Contains(h.screen(), "Moved 1 to the Trash; press h to put it back.") {
		t.Errorf("result:\n%s", h.screen())
	}
}

func TestDiskRefusesProtectedFoldersAndNestedMarks(t *testing.T) {
	h := openDisk(t, &fakeDisk{}, nil)
	h.press("j", "j", "space") // Documents
	if s := h.screen(); !strings.Contains(s, "soap never removes ~/Documents.") {
		t.Errorf("screen:\n%s", s)
	}
	if len(h.m.checks[model.ScreenDisk].items) != 0 {
		t.Error("Documents must not be marked")
	}
	h.press("k", "k")
	h.pump(h.press("enter")) // Projects
	h.press("j", "space")    // notes.txt
	h.pump(h.press("backspace"))
	h.press("space") // Projects itself: it now covers notes.txt
	cs := h.m.checks[model.ScreenDisk]
	if _, ok := cs.items["disk/Projects"]; !ok || len(cs.items) != 1 {
		t.Errorf("marks = %v; marking a folder unmarks what's inside it", cs.items)
	}
	h.pump(h.press("enter"))
	h.press("j", "space")
	if s := h.screen(); !strings.Contains(s, "It's inside ~/Projects, which is marked already.") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDiskWalkRunsOnOtherTabsAndBlocksCleanUps(t *testing.T) {
	fd, fc := &fakeDisk{block: make(chan struct{})}, junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	walk := h.press("6") // the walk starts but its command isn't run, so it's still going
	if !strings.Contains(h.screen(), "walking") {
		t.Fatalf("not walking:\n%s", h.screen())
	}
	h.pump(h.press("4"))
	if !h.m.disk.walking {
		t.Error("leaving Disk must not stop the walk")
	}
	if cmd := h.press("c"); cmd != nil || h.m.confirm != nil {
		t.Error("no clean-up may start while the walk runs")
	}
	if !strings.Contains(h.screen(), "Wait for the Disk walk to finish, or press esc on its tab to stop it.") {
		t.Errorf("screen:\n%s", h.screen())
	}
	if !strings.Contains(h.screen(), "walking") {
		t.Errorf("the status bar shows the walk:\n%s", h.screen())
	}
	h.press("6", "esc")
	if !strings.Contains(h.screen(), "Stopped the walk.") {
		t.Errorf("screen:\n%s", h.screen())
	}
	h.pump(walk)
	if len(fd.walks) != 1 || fd.walks[0].Err() == nil {
		t.Error("esc on Disk stops the walk")
	}
	if h.m.disk.walking {
		t.Error("the walk is over")
	}
}

func TestDiskDoesNotWalkDuringACleanUp(t *testing.T) {
	fd, fc := &fakeDisk{}, junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	h.pump(h.press("4"))
	h.press("c")
	h.send(h.press("y")()) // the Junk clean-up runs; its command isn't run
	h.press("6")
	if len(fd.walks) != 0 || h.m.disk.walking {
		t.Error("no walk may start during a clean-up")
	}
	if !strings.Contains(h.screen(), "A clean-up is running. Walk again when it's done.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestQuitStopsTheWalk(t *testing.T) {
	fd := &fakeDisk{block: make(chan struct{})}
	h := newHarness(t, portsSnapshot(), fd.deps)
	walk := h.press("6")
	if _, quit := h.press("q")().(tea.QuitMsg); !quit {
		t.Fatal("q quits")
	}
	h.pump(walk)
	if len(fd.walks) != 1 || fd.walks[0].Err() == nil {
		t.Error("quitting stops the walk")
	}
}

func trashedHistory() *fakeCleanup {
	return &fakeCleanup{history: []model.HistoryEntry{{ID: "h1", Name: "~/big.iso", Method: model.MethodTrash,
		Paths: []string{home + "/big.iso"}, Trashed: []string{"/x"}, Result: model.ResultDone, Size: 4 * gib}}}
}

func TestDiskPutBackIsRefusedDuringTheWalk(t *testing.T) {
	fd, fc := &fakeDisk{block: make(chan struct{})}, trashedHistory()
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	h.press("6") // the walk's command isn't run, so it's still going
	h.pump(h.press("h"))
	if cmd := h.press("u"); cmd != nil || len(fc.restored) != 0 {
		t.Error("no put-back may start during the walk")
	}
	if !strings.Contains(h.screen(), "Wait for the walk to finish, then put it back.") {
		t.Errorf("screen:\n%s", h.screen())
	}
}

func TestRestoreFinishingDuringAWalkDoesNotMeasure(t *testing.T) {
	fd, fc := &fakeDisk{block: make(chan struct{})}, trashedHistory()
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	h.press("6")
	h.pump(h.press("4"))
	h.pump(h.send(restoredMsg{screen: model.ScreenDisk, entry: fc.history[0]}))
	if len(fd.remeasured) != 0 {
		t.Errorf("remeasured %v while the walk runs; the walk sees the change itself", fd.remeasured)
	}
}

func TestNoCleanUpStartsWhileMeasuring(t *testing.T) {
	fd := &fakeDisk{}
	h := openDisk(t, fd, nil)
	h.press("j", "space")
	measure := h.m.remeasure([]string{home})
	if cmd := h.press("c"); cmd != nil || h.m.confirm != nil {
		t.Error("no clean-up may start while folders are measured again")
	}
	if !strings.Contains(h.screen(), "Wait for the Disk size update to finish.") {
		t.Errorf("screen:\n%s", h.screen())
	}
	h.pump(measure)
	h.press("c")
	if h.m.confirm == nil {
		t.Error("the clean-up can start once measuring is done")
	}
}

func TestDiskSelectionFollowsThePathWhenRowsReorder(t *testing.T) {
	fd := &fakeDisk{}
	h := openDisk(t, fd, nil)
	h.press("j") // big.iso
	fd.tree.Refresh(home+"/Documents", 20*gib, nil)
	h.press("space")
	if _, ok := h.m.checks[model.ScreenDisk].items["disk/big.iso"]; !ok || len(h.m.checks[model.ScreenDisk].items) != 1 {
		t.Errorf("marks = %v; the row that was selected is big.iso", h.m.checks[model.ScreenDisk].items)
	}
}

func TestWalkingAgainDropsTheMarks(t *testing.T) {
	h := openDisk(t, &fakeDisk{}, nil)
	h.press("j", "space")
	h.pump(h.press("r"))
	if n := len(h.m.checks[model.ScreenDisk].items); n != 0 {
		t.Errorf("%d marks kept; they hold sizes from before the new walk", n)
	}
}

func TestDiskTabExplainsAWalkBlockedByACleanUp(t *testing.T) {
	fd, fc := &fakeDisk{}, junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps)
	h.pump(h.press("4"))
	h.press("c")
	h.send(h.press("y")())
	h.press("6")
	if s := h.screen(); !strings.Contains(s, "The walk starts when the clean-up is done. Press r then.") || strings.Contains(s, "This folder is empty.") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDiskHintsFitEightyColumnsDuringAWalk(t *testing.T) {
	h := newHarness(t, portsSnapshot(), (&fakeDisk{block: make(chan struct{})}).deps)
	h.press("6")
	line := h.m.keyLine()
	if w := ansi.StringWidth(line); w > 80 {
		t.Errorf("hints are %d columns wide: %q", w, ansi.Strip(line))
	}
	if !strings.Contains(ansi.Strip(line), "q quit") {
		t.Errorf("hints: %q", ansi.Strip(line))
	}
}

func TestDiskGoingUpSelectsTheFolderYouLeftEvenWhenItIsNotFirst(t *testing.T) {
	h := openDisk(t, &fakeDisk{}, nil)
	for i, r := range h.m.visibleRows() {
		if dr, _ := r.item.(diskRow); dr.e.Name == "Documents" {
			h.m.sel[h.m.tab] = i
			h.m.disk.selPath = dr.e.Path
		}
	}
	h.pump(h.press("enter"))
	if !strings.Contains(h.screen(), "Disk · ~/Documents") {
		t.Fatalf("Documents isn't open:\n%s", h.screen())
	}
	h.pump(h.press("backspace"))
	rows := h.m.visibleRows()
	if dr, _ := rows[h.m.sel[h.m.tab]].item.(diskRow); dr.e.Name != "Documents" {
		t.Errorf("selected %+v; the folder you came from is selected, and stays so", dr.e)
	}
	h.press("j", "k")
	if dr, _ := h.m.visibleRows()[h.m.sel[h.m.tab]].item.(diskRow); dr.e.Name != "Documents" {
		t.Errorf("after j and k the selection is %+v", dr.e)
	}
}

// withICloudFile adds a file inside iCloud Drive to the home folder's listing.
func withICloudFile(d *Deps) {
	inner := d.List
	d.List = func(dir string) ([]model.DiskEntry, error) {
		es, err := inner(dir)
		if dir == home {
			es = append(es, model.DiskEntry{Name: "scan.pdf", Path: home + "/Library/Mobile Documents/com~apple~CloudDocs/scan.pdf",
				Size: gib, Stamp: model.Stamp{Size: 1 << 30, Inode: 9}})
		}
		return es, err
	}
}

// markRow moves to the row with this name and marks it.
func markRow(t *testing.T, h *harness, name string) {
	t.Helper()
	for i, r := range h.m.visibleRows() {
		if dr, _ := r.item.(diskRow); dr.e.Name == name {
			h.press("k", "k", "k", "k", "k", "k", "k", "k")
			for range i {
				h.press("j")
			}
			h.press("space")
			return
		}
	}
	t.Fatalf("no row named %s", name)
}

func TestDiskMarksICloudRowsAndWarnsInThePreview(t *testing.T) {
	fc := &fakeCleanup{}
	h := newHarness(t, portsSnapshot(), fc.deps, (&fakeDisk{}).deps, withICloudFile)
	h.pump(h.press("6"))
	if s := h.screen(); !strings.Contains(s, "scan.pdf · in iCloud Drive") || strings.Contains(s, "big.iso · in iCloud Drive") {
		t.Fatalf("only the file in iCloud Drive says so:\n%s", s)
	}
	markRow(t, h, "big.iso")
	h.press("c")
	if s := h.screen(); strings.Contains(s, model.ICloudWarning) {
		t.Fatalf("a file outside iCloud Drive gets no warning:\n%s", s)
	}
	h.press("n")
	markRow(t, h, "scan.pdf")
	h.press("c")
	if s := h.screen(); !strings.Contains(s, model.ICloudWarning) {
		t.Errorf("the preview warns about iCloud Drive:\n%s", s)
	}
}

func TestDiskUnreadableNoteSaysItForOneFolder(t *testing.T) {
	for n, want := range map[int]string{
		1: "1 folder couldn't be read, so sizes with ≥ are at least what's shown. To see it, give",
		2: "2 folders couldn't be read, so sizes with ≥ are at least what's shown. To see them, give",
	} {
		h := newHarness(t, portsSnapshot(), (&fakeCleanup{}).deps, (&fakeDisk{}).deps, func(d *Deps) {
			d.Walk = func(_ context.Context, tree *model.DiskTree) {
				names := []model.DiskChild{{Name: "a"}, {Name: "b"}}[:n]
				for _, k := range tree.Listed(tree.Top(), gib, 1, names) {
					tree.Unreadable(k)
				}
				tree.Finish(false)
			}
		})
		h.pump(h.press("6"))
		if s := strings.Join(strings.Fields(h.screen()), " "); !strings.Contains(s, want) {
			t.Errorf("%d unreadable: missing %q:\n%s", n, want, s)
		}
	}
}
