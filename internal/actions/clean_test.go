package actions

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// cleanFixture is a fake home folder with a cleaner pointed at it.
type cleanFixture struct {
	home     string
	c        *Cleaner
	runs     [][]string
	runErr   error
	trashed  []string
	trashErr error
	results  []model.CleanResult
}

func newCleanFixture(t *testing.T) *cleanFixture {
	t.Helper()
	f := &cleanFixture{home: t.TempDir()}
	f.c = &Cleaner{
		Home:         f.home,
		ProjectRoots: []string{filepath.Join(f.home, "Projects")},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			f.runs = append(f.runs, append([]string{name}, args...))
			return nil, f.runErr
		},
		Trash: func(p string) (string, error) {
			if f.trashErr != nil {
				return "", f.trashErr
			}
			dest := filepath.Join(f.home, ".Trash", filepath.Base(p))
			os.MkdirAll(filepath.Dir(dest), 0o755)
			f.trashed = append(f.trashed, p)
			return dest, os.Rename(p, dest)
		},
		Measure: func(_ context.Context, p string) uint64 {
			var n uint64
			filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if info, err := d.Info(); err == nil {
						n += uint64(info.Size())
					}
				}
				return nil
			})
			return n
		},
		History: &History{Path: HistoryPath(f.home)},
		Now:     func() time.Time { return t0 },
	}
	return f
}

func (f *cleanFixture) put(t *testing.T, rel string, size int) string {
	t.Helper()
	p := filepath.Join(f.home, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *cleanFixture) clean(t *testing.T, items ...model.CleanItem) error {
	t.Helper()
	return f.c.Clean(context.Background(), items, map[string]bool{"xcode": true}, func(r model.CleanResult) { f.results = append(f.results, r) })
}

func (f *cleanFixture) cache(name string) model.CleanItem {
	root := filepath.Join(f.home, "Library/Caches")
	return model.CleanItem{ID: "junk/caches/" + name, Name: name, Method: model.MethodDelete, Paths: []string{filepath.Join(root, name)}, Root: root, Size: 1000}
}

func TestCleanDeletesExactlyTheItemPaths(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Caches/Google/x", 1000)
	keep := f.put(t, "Library/Caches/Other/x", 1000)
	if err := f.clean(t, f.cache("Google")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "Library/Caches/Google")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Google cache still there")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("a sibling folder was touched")
	}
	if len(f.results) != 1 || f.results[0].Result != "done" || f.results[0].Freed != 1000 {
		t.Errorf("results = %+v", f.results)
	}
	list, _ := f.c.History.List()
	if len(list) != 1 || list[0].ItemID != "junk/caches/Google" || list[0].Result != "done" {
		t.Errorf("history = %+v", list)
	}
}

func TestCleanRefusesUnsafePaths(t *testing.T) {
	f := newCleanFixture(t)
	caches := filepath.Join(f.home, "Library/Caches")
	docs := f.put(t, "Documents/thesis.pdf", 1000)
	f.put(t, "Library/Caches/real/x", 1000)
	if err := os.Symlink(filepath.Dir(docs), filepath.Join(caches, "link")); err != nil {
		t.Fatal(err)
	}
	items := []model.CleanItem{
		{ID: "root", Name: "root", Method: model.MethodDelete, Paths: []string{caches}, Root: caches},
		{ID: "outside", Name: "outside", Method: model.MethodDelete, Paths: []string{docs}, Root: caches},
		{ID: "dotdot", Name: "dotdot", Method: model.MethodDelete, Paths: []string{caches + "/../../Documents"}, Root: caches},
		{ID: "symlink", Name: "symlink", Method: model.MethodDelete, Paths: []string{filepath.Join(caches, "link")}, Root: caches},
		{ID: "bad-root", Name: "bad-root", Method: model.MethodDelete, Paths: []string{docs}, Root: filepath.Dir(docs)},
		{ID: "gone", Name: "gone", Method: model.MethodDelete, Paths: []string{filepath.Join(caches, "gone")}, Root: caches},
	}
	if err := f.clean(t, items...); err != nil {
		t.Fatal(err)
	}
	if len(f.results) != len(items) {
		t.Fatalf("got %d results, want %d", len(f.results), len(items))
	}
	for _, r := range f.results {
		if r.Result != "skipped" || r.Message == "" {
			t.Errorf("%s: %+v, want skipped with a reason", r.Item.ID, r)
		}
	}
	if _, err := os.Stat(docs); err != nil {
		t.Fatal("a document outside the allowed folders was deleted")
	}
	if _, err := os.Stat(caches); err != nil {
		t.Fatal("the allowed folder itself was deleted")
	}
}

func TestCleanSkipsBusyBlockedAndChangedProjects(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Developer/Xcode/DerivedData/App-abc/x", 1000)
	dd := filepath.Join(f.home, "Library/Developer/Xcode/DerivedData")
	nm := f.put(t, "Projects/app/node_modules/x", 1000)
	project := model.CleanItem{ID: "p", Name: "app · node_modules", Method: model.MethodDelete, Paths: []string{filepath.Dir(nm)},
		Root: filepath.Join(f.home, "Projects"), Marker: filepath.Join(f.home, "Projects/app/package.json")}
	items := []model.CleanItem{
		{ID: "busy", Name: "DerivedData", Method: model.MethodDelete, Paths: []string{filepath.Join(dd, "App-abc")}, Root: dd, App: "Xcode"},
		{ID: "blocked", Name: "Trash", Method: model.MethodDelete, Blocked: "needs Full Disk Access"},
		project,
	}
	if err := f.clean(t, items...); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"busy": "Quit Xcode first", "blocked": "needs Full Disk Access"}
	if len(f.results) != len(items) {
		t.Fatalf("got %d results, want %d", len(f.results), len(items))
	}
	for _, r := range f.results {
		if r.Result != "skipped" {
			t.Errorf("%s: %+v", r.Item.ID, r)
		}
		if w, ok := want[r.Item.ID]; ok && r.Message != w {
			t.Errorf("%s: message %q, want %q", r.Item.ID, r.Message, w)
		}
	}
	if _, err := os.Stat(nm); err != nil {
		t.Error("node_modules without its package.json must not be deleted")
	}
}

func TestCleanRunsCommands(t *testing.T) {
	f := newCleanFixture(t)
	item := model.CleanItem{ID: "dev/docker/build-cache", Name: "Build cache", Method: model.MethodCommand, Command: []string{"docker", "builder", "prune", "-a", "-f"}, Size: 3000}
	if err := f.clean(t, item); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 1 || !slices.Equal(f.runs[0], item.Command) || f.results[0].Freed != 0 {
		t.Errorf("runs = %v, results = %+v", f.runs, f.results)
	}
	f.runErr = errors.New("docker: exit status 1: Cannot connect to the Docker daemon\nmore detail")
	f.results = nil
	f.clean(t, item)
	if r := f.results[0]; r.Result != "failed" || r.Message != "docker: exit status 1: Cannot connect to the Docker daemon" {
		t.Errorf("failed command = %+v; want the first line of the error", r)
	}
}

func TestCleanCommandMeasuresSizeOf(t *testing.T) {
	f := newCleanFixture(t)
	big := f.put(t, "Library/pnpm/store/a", 3000)
	f.put(t, "Library/pnpm/store/b", 1000)
	f.c.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, os.Remove(big) }
	item := model.CleanItem{ID: "dev/packages/pnpm-store", Name: "pnpm store", Method: model.MethodCommand, Command: []string{"pnpm", "store", "prune"},
		Size: 9999, SizeOf: []string{filepath.Join(f.home, "Library/pnpm/store")}}
	if err := f.clean(t, item); err != nil {
		t.Fatal(err)
	}
	r := f.results[0]
	if r.Result != "done" || r.Freed != 3000 || r.Message != "" {
		t.Errorf("result = %+v; want 3000 measured, not the 9999 estimate", r)
	}
	if e, err := f.c.History.List(); err != nil || len(e) != 1 || e[0].Size != 3000 {
		t.Errorf("history = %+v, %v; want size 3000", e, err)
	}
}

func TestCleanCommandMeasuresDiskFree(t *testing.T) {
	f := newCleanFixture(t)
	free := uint64(10_000)
	f.c.DiskFree = func() (uint64, error) { return free, nil }
	f.c.Run = func(context.Context, string, ...string) ([]byte, error) { free += 4000; return nil, nil }
	item := model.CleanItem{ID: "dev/docker/build-cache", Name: "Build cache", Method: model.MethodCommand, Command: []string{"docker", "builder", "prune", "-a", "-f"}, Size: 9999}
	f.clean(t, item)
	if r := f.results[0]; r.Result != "done" || r.Freed != 4000 {
		t.Errorf("result = %+v; want 4000", r)
	}
}

func TestCleanCommandFreedNothing(t *testing.T) {
	f := newCleanFixture(t)
	f.c.DiskFree = func() (uint64, error) { return 10_000, nil }
	item := model.CleanItem{ID: "dev/docker/unused-images", Name: "Images", Method: model.MethodCommand, Command: []string{"docker", "image", "prune", "-a", "-f"}, Size: 9999}
	f.clean(t, item)
	if r := f.results[0]; r.Result != "done" || r.Freed != 0 || r.Message != "Free space didn't grow yet. Docker and Xcode can take a while to give it back." {
		t.Errorf("result = %+v", r)
	}
	f.c.DiskFree = func() (uint64, error) { return 10_000 - uint64(len(f.runs))*100, nil } // free space shrank: never below 0
	f.results = nil
	f.clean(t, item)
	if r := f.results[0]; r.Freed != 0 {
		t.Errorf("freed = %d, want 0", r.Freed)
	}

	// A folder that's measured and doesn't shrink: the tool really freed nothing.
	f.c.Measure = func(context.Context, string) uint64 { return 5000 }
	f.results = nil
	f.clean(t, model.CleanItem{ID: "dev/packages/pnpm", Name: "pnpm store", Method: model.MethodCommand, Command: []string{"pnpm", "store", "prune"},
		Size: 5000, SizeOf: []string{filepath.Join(f.home, "Library/pnpm/store")}})
	if r := f.results[0]; r.Result != "done" || r.Freed != 0 || r.Message != "The command ran but freed nothing." {
		t.Errorf("pnpm result = %+v", r)
	}
}

func TestCleanCommandCannotMeasure(t *testing.T) {
	f := newCleanFixture(t)
	item := model.CleanItem{ID: "dev/x", Name: "x", Method: model.MethodCommand, Command: []string{"tool"}, Size: 9999}
	f.clean(t, item)
	if r := f.results[0]; r.Result != "done" || r.Freed != 0 || r.Message != "Ran the command, but couldn't measure what it freed." {
		t.Errorf("result = %+v", r)
	}
}

func TestCleanTrashesAndNeverFallsBackToDeleting(t *testing.T) {
	f := newCleanFixture(t)
	backup := f.put(t, "Library/Application Support/MobileSync/Backup/abc/data", 1000)
	root := filepath.Join(f.home, "Library/Application Support/MobileSync/Backup")
	item := model.CleanItem{ID: "junk/backups/abc", Name: "iPhone backup", Method: model.MethodTrash, Paths: []string{filepath.Dir(backup)}, Root: root, Size: 1000}
	f.trashErr = errors.New("the volume has no Trash")
	f.clean(t, item)
	if r := f.results[0]; r.Result != "failed" {
		t.Errorf("result = %+v", r)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatal("a failed move to the Trash must leave the files alone")
	}
	f.trashErr, f.results = nil, nil
	f.clean(t, item)
	if r := f.results[0]; r.Result != "done" || r.Freed != 0 {
		t.Errorf("trashing frees nothing yet: %+v", r)
	}
	list, _ := f.c.History.List()
	if !list[0].CanRestore() || list[0].Size != 1000 || list[0].Trashed[0] != filepath.Join(f.home, ".Trash", "abc") {
		t.Errorf("history = %+v", list[0])
	}
}

func TestCleanReportsPartialDeletes(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Caches/Mixed/big", 100_000)
	stuck := f.put(t, "Library/Caches/Mixed/locked/stuck", 1000)
	if err := syscall.Chflags(stuck, 0x2); err != nil { // UF_IMMUTABLE
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(stuck, 0) })
	item := f.cache("Mixed")
	item.Size = 101_000
	f.clean(t, item)
	if len(f.results) != 1 {
		t.Fatalf("results = %+v", f.results)
	}
	r := f.results[0]
	want := "Partly deleted: " + model.HumanBytes(100_000) + " of " + model.HumanBytes(101_000) + " freed. 1 file couldn't be removed."
	if r.Result != "partial" || r.Freed != 100_000 || r.Message != want {
		t.Errorf("partial = %+v, want message %q", r, want)
	}
	list, _ := f.c.History.List()
	if len(list) != 1 || !strings.HasPrefix(list[0].Message, want+" First error: ") {
		t.Errorf("history = %+v", list)
	}
}

func TestCleanReportsFailedDeletes(t *testing.T) {
	f := newCleanFixture(t)
	stuck := f.put(t, "Library/Caches/Locked/locked/stuck", 1000)
	if err := syscall.Chflags(stuck, 0x2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(stuck, 0) })
	f.clean(t, f.cache("Locked"))
	if len(f.results) != 1 {
		t.Fatalf("results = %+v", f.results)
	}
	if r := f.results[0]; r.Result != "failed" || r.Freed != 0 || r.Message != "Nothing was deleted. 1 file couldn't be removed." {
		t.Errorf("failed = %+v", r)
	}
}

func TestCleanSkipsWholeItemWhenAnyPathChangedSinceTheScan(t *testing.T) {
	for _, method := range []model.Method{model.MethodDelete, model.MethodTrash} {
		f := newCleanFixture(t)
		first := f.put(t, "Library/Caches/A/x", 1000)
		outside := f.put(t, "Documents/keep", 10)
		link := filepath.Join(f.home, "Library/Caches/link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		item := f.cache("A")
		item.Method = method
		item.Paths = append(item.Paths, link)
		f.clean(t, item)
		if len(f.results) != 1 || f.results[0].Result != "skipped" || !strings.Contains(f.results[0].Message, "symlink") {
			t.Fatalf("%v: results = %+v", method, f.results)
		}
		if _, err := os.Stat(first); err != nil {
			t.Errorf("%v: the first path was removed although the item was skipped", method)
		}
	}
}

func TestCleanDeleteReportsPartialWhenLaterPathChangesMidRun(t *testing.T) {
	f := newCleanFixture(t)
	root := filepath.Join(f.home, "Projects")
	f.put(t, "Projects/alpha/package.json", 10)
	f.put(t, "Projects/alpha/big", 5000)
	beta := f.put(t, "Projects/beta/x", 1000)
	// The marker sits inside the first path, so removing it makes the second path fail its check.
	item := model.CleanItem{ID: "p", Name: "p", Method: model.MethodDelete, Root: root, Size: 6010,
		Paths:  []string{filepath.Join(root, "alpha"), filepath.Join(root, "beta")},
		Marker: filepath.Join(root, "alpha/package.json")}
	f.clean(t, item)
	if len(f.results) != 1 {
		t.Fatalf("results = %+v", f.results)
	}
	want := "Partly deleted: " + model.HumanBytes(5010) + " of " + model.HumanBytes(6010) + " freed. Stopped: beta no longer looks like a project folder (package.json is missing)."
	if r := f.results[0]; r.Result != "partial" || r.Freed != 5010 || r.Message != want {
		t.Errorf("result = %+v, want message %q", r, want)
	}
	if _, err := os.Stat(beta); err != nil {
		t.Error("the refused path was removed")
	}
}

func TestCleanTrashReportsPartialWhenLaterPathChangesMidRun(t *testing.T) {
	f := newCleanFixture(t)
	root := filepath.Join(f.home, "Library/Application Support/MobileSync/Backup")
	f.put(t, "Library/Application Support/MobileSync/Backup/a/x", 10)
	f.put(t, "Library/Application Support/MobileSync/Backup/b/x", 10)
	outside := t.TempDir()
	b := filepath.Join(root, "b")
	trash := f.c.Trash
	f.c.Trash = func(p string) (string, error) {
		dest, err := trash(p)
		os.RemoveAll(b)
		os.Symlink(outside, b)
		return dest, err
	}
	item := model.CleanItem{ID: "t", Name: "t", Method: model.MethodTrash, Root: root, Size: 20, Paths: []string{filepath.Join(root, "a"), b}}
	f.clean(t, item)
	if len(f.results) != 1 {
		t.Fatalf("results = %+v", f.results)
	}
	if r := f.results[0]; r.Result != "partial" || r.Message != "Moved part of it to the Trash. Stopped: b is now a symlink." {
		t.Errorf("result = %+v", r)
	}
	list, _ := f.c.History.List()
	if len(list) != 1 || len(list[0].Trashed) != 1 {
		t.Errorf("history = %+v; the one trashed path must be recorded", list)
	}
}

func TestRemoveInRootNeverLeavesTheRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.MkdirAll(outside, 0o755)
	victim := filepath.Join(outside, "x")
	os.WriteFile(victim, []byte("keep"), 0o644)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	os.RemoveAll(filepath.Join(root, "sub"))
	if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := removeInRoot(r, "sub/x"); err == nil {
		t.Error("removeInRoot followed a symlink out of the root without an error")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("a file outside the root was deleted")
	}
}

func TestCleanReachesAppendFailureAndStops(t *testing.T) {
	f := newCleanFixture(t)
	second := f.put(t, "Library/Caches/B/x", 10)
	f.c.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, os.Chmod(f.c.History.Path, 0o444)
	}
	t.Cleanup(func() { os.Chmod(f.c.History.Path, 0o644) })
	cmd := model.CleanItem{ID: "c", Name: "c", Method: model.MethodCommand, Command: []string{"tool"}, Size: 5}
	err := f.clean(t, cmd, f.cache("B"))
	if !errors.Is(err, ErrHistoryUnwritable) || err.Error() != "Can't write the history file any more, so nothing more was cleaned" {
		t.Fatalf("err = %v", err)
	}
	if len(f.results) != 1 || !strings.Contains(f.results[0].Message, "history entry wasn't saved") {
		t.Errorf("results = %+v", f.results)
	}
	if _, err := os.Stat(second); err != nil {
		t.Error("the second item was cleaned")
	}
}

func TestCleanRejectsEmptyAndUnknownItems(t *testing.T) {
	f := newCleanFixture(t)
	keep := f.put(t, "Library/Caches/Google/x", 1000)
	unknown := f.cache("Google")
	unknown.Method = model.Method(99)
	empty := f.cache("Google")
	empty.Paths = nil
	emptyTrash := empty
	emptyTrash.Method = model.MethodTrash
	if err := f.clean(t, unknown, empty, emptyTrash); err != nil {
		t.Fatal(err)
	}
	if len(f.results) != 3 {
		t.Fatalf("results = %+v", f.results)
	}
	wants := []string{"unknown clean method", "there's nothing to remove for this item", "there's nothing to remove for this item"}
	for i, r := range f.results {
		if r.Result != "failed" || r.Message != wants[i] {
			t.Errorf("result %d = %+v", i, r)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("nothing should have been removed")
	}
}

func TestCleanStopsWhenHistoryStopsWorking(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Caches/A/x", 10)
	second := f.put(t, "Library/Caches/B/x", 10)
	err := f.c.Clean(context.Background(), []model.CleanItem{f.cache("A"), f.cache("B")}, nil, func(r model.CleanResult) {
		f.results = append(f.results, r)
		os.Chmod(f.c.History.Path, 0o444)
	})
	t.Cleanup(func() { os.Chmod(f.c.History.Path, 0o644) })
	if !errors.Is(err, ErrHistoryUnwritable) {
		t.Fatalf("got %v, want ErrHistoryUnwritable", err)
	}
	if err.Error() != "Can't write the history file any more, so nothing more was cleaned" {
		t.Errorf("err text = %q", err)
	}
	if len(f.results) != 1 {
		t.Errorf("results = %+v", f.results)
	}
	if _, err := os.Stat(second); err != nil {
		t.Error("the second item was cleaned after the history stopped working")
	}
}

func TestCleanNeedsWritableHistory(t *testing.T) {
	f := newCleanFixture(t)
	keep := f.put(t, "Library/Caches/Google/x", 1000)
	locked := filepath.Join(f.home, "locked")
	os.MkdirAll(locked, 0o755)
	os.Chmod(locked, 0o500)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	f.c.History = &History{Path: filepath.Join(locked, "sub", "history.jsonl")}
	err := f.clean(t, f.cache("Google"))
	if !errors.Is(err, ErrHistoryUnwritable) {
		t.Fatalf("got %v, want ErrHistoryUnwritable", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("nothing may be cleaned without a history")
	}
}

func TestCleanStopsBetweenItemsWhenCancelled(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Caches/A/x", 10)
	second := f.put(t, "Library/Caches/B/x", 10)
	ctx, cancel := context.WithCancel(context.Background())
	err := f.c.Clean(ctx, []model.CleanItem{f.cache("A"), f.cache("B")}, nil, func(r model.CleanResult) {
		f.results = append(f.results, r)
		cancel()
	})
	if !errors.Is(err, context.Canceled) || len(f.results) != 1 {
		t.Errorf("err = %v, results = %d", err, len(f.results))
	}
	if _, err := os.Stat(second); err != nil {
		t.Error("the second item was cleaned after cancelling")
	}
}

func TestCleanRefusesFolderSwappedForSymlink(t *testing.T) {
	f := newCleanFixture(t)
	outside := t.TempDir()
	root := filepath.Join(f.home, "Projects")
	nm := f.put(t, "Projects/app/node_modules/x", 1000)
	f.put(t, "Projects/app/package.json", 10)
	item := model.CleanItem{ID: "p", Name: "app · node_modules", Method: model.MethodDelete, Paths: []string{filepath.Dir(nm)},
		Root: root, Marker: filepath.Join(root, "app/package.json")}
	kept := filepath.Join(outside, "app/node_modules/x")
	os.MkdirAll(filepath.Dir(kept), 0o755)
	os.WriteFile(kept, []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(outside, "app/package.json"), []byte("{}"), 0o644)
	if err := os.RemoveAll(filepath.Join(root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "app"), filepath.Join(root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := f.clean(t, item); err != nil {
		t.Fatal(err)
	}
	r := f.results[0]
	if r.Result != "skipped" || !strings.Contains(r.Message, "behind a symlink") {
		t.Errorf("result = %+v", r)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("a folder outside the home folder was deleted through a symlink")
	}
}

func TestCleanRefusesRootBehindSymlinkedAncestor(t *testing.T) {
	f := newCleanFixture(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "code/app/node_modules/x")
	os.MkdirAll(filepath.Dir(victim), 0o755)
	os.WriteFile(victim, []byte("keep"), 0o644)
	if err := os.Symlink(outside, filepath.Join(f.home, "dev")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.home, "dev/code")
	f.c.ProjectRoots = []string{root}
	item := model.CleanItem{ID: "p", Name: "app", Method: model.MethodDelete,
		Paths: []string{filepath.Join(root, "app/node_modules")}, Root: root}
	if err := f.clean(t, item); err != nil {
		t.Fatal(err)
	}
	r := f.results[0]
	if r.Result != "skipped" || !strings.Contains(r.Message, "leads outside your home folder") {
		t.Errorf("result = %+v", r)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("something outside the home folder was deleted")
	}
}

func TestCleanRefusesItemWithNoMethod(t *testing.T) {
	f := newCleanFixture(t)
	keep := f.put(t, "Library/Caches/Google/x", 1000)
	item := f.cache("Google")
	item.Method = 0
	if err := f.clean(t, item); err != nil {
		t.Fatal(err)
	}
	if r := f.results[0]; r.Result != "failed" || r.Message != "unknown clean method" {
		t.Errorf("result = %+v", r)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("nothing should have been removed")
	}
	list, _ := f.c.History.List()
	raw, _ := os.ReadFile(f.c.History.Path)
	if len(list) != 1 || !strings.Contains(string(raw), `"method":"unknown"`) || list[0].State() != "failed: unknown clean method" {
		t.Errorf("history = %+v\n%s", list, raw)
	}
}

func TestCleanSaysSomeFilesWhenTheLeftoverCantBeCounted(t *testing.T) {
	f := newCleanFixture(t)
	f.put(t, "Library/Caches/Hidden/locked/stuck", 1000)
	dir := filepath.Join(f.home, "Library/Caches/Hidden/locked")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Chflags(dir, 0x2); err != nil { // UF_IMMUTABLE
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(dir, 0); os.Chmod(dir, 0o755) })
	f.clean(t, f.cache("Hidden"))
	r := f.results[0]
	if !strings.HasSuffix(r.Message, " Some files couldn't be removed.") || strings.Contains(r.Message, "0 files") {
		t.Errorf("result = %+v", r)
	}
}

func TestCleanTrashWithNoLocationRecordsNoTrashedPath(t *testing.T) {
	f := newCleanFixture(t)
	backup := f.put(t, "Library/Application Support/MobileSync/Backup/abc/data", 1000)
	root := filepath.Join(f.home, "Library/Application Support/MobileSync/Backup")
	item := model.CleanItem{ID: "junk/backups/abc", Name: "iPhone backup", Method: model.MethodTrash, Paths: []string{filepath.Dir(backup)}, Root: root, Size: 1000}
	f.c.Trash = func(p string) (string, error) { return "", os.Rename(p, filepath.Join(f.home, "gone")) }
	f.clean(t, item)
	r := f.results[0]
	if r.Result != "done" || r.Message != "Moved to the Trash. Use Finder's Put Back to restore it." {
		t.Errorf("result = %+v", r)
	}
	list, _ := f.c.History.List()
	if len(list) != 1 || len(list[0].Trashed) != 0 || list[0].CanRestore() || list[0].State() != "in the Trash" {
		t.Errorf("history = %+v", list)
	}
}

// stampNow is what scan.StampPath reports, for tests: it can't be imported here.
func stampNow(p string) (model.Stamp, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return model.Stamp{}, err
	}
	s := model.Stamp{Dir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.Inode = st.Ino
	}
	return s, nil
}

// homeFixture is a clean fixture that can check Disk and Dupes items: names ending in .app or
// .photoslibrary are packages.
func homeFixture(t *testing.T) *cleanFixture {
	t.Helper()
	f := newCleanFixture(t)
	f.c.IsPackage = func(p string) bool { return strings.HasSuffix(p, ".app") || strings.HasSuffix(p, ".photoslibrary") }
	f.c.Stamp = stampNow
	return f
}

// homeItem is a Disk item for home/rel, stamped as it is now.
func (f *cleanFixture) homeItem(t *testing.T, rel string) model.CleanItem {
	t.Helper()
	p := filepath.Join(f.home, rel)
	s, err := stampNow(p)
	if err != nil {
		t.Fatal(err)
	}
	return model.CleanItem{ID: "disk/" + rel, Name: "~/" + rel, Method: model.MethodTrash, Paths: []string{p}, Root: f.home, Stamp: &s, Size: 1000}
}

func TestCleanTrashesHomeItems(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/big.iso", 1000)
	f.put(t, "Projects/old/x", 1000)
	if err := f.clean(t, f.homeItem(t, "Downloads/big.iso"), f.homeItem(t, "Projects/old")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.results {
		if r.Result != model.ResultDone {
			t.Errorf("%s: %+v", r.Item.ID, r)
		}
	}
	if len(f.trashed) != 2 {
		t.Errorf("trashed %v", f.trashed)
	}
	list, _ := f.c.History.List()
	if len(list) != 2 || !list[0].CanRestore() {
		t.Errorf("history = %+v", list)
	}
}

func TestCleanRefusesUnsafeHomeItems(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/a.iso", 1000)
	f.put(t, "Documents/thesis.pdf", 1000)
	f.put(t, "Applications/Tool.app/Contents/x", 1000)
	f.put(t, "Movies/changed.mov", 1000)
	f.put(t, "Movies/nostamp.mov", 1000)
	deleting := f.homeItem(t, "Downloads/a.iso")
	deleting.ID, deleting.Method = "delete", model.MethodDelete
	documents := f.homeItem(t, "Documents")
	inPackage := f.homeItem(t, "Applications/Tool.app/Contents/x")
	changed := f.homeItem(t, "Movies/changed.mov")
	f.put(t, "Movies/changed.mov", 2000)
	noStamp := f.homeItem(t, "Movies/nostamp.mov")
	noStamp.Stamp = nil
	want := map[string]string{
		deleting.ID:  "can only go to the Trash",
		documents.ID: "soap never removes ~/Documents",
		inPackage.ID: "inside Tool.app",
		changed.ID:   "changed.mov changed since the scan",
		noStamp.ID:   "no record of how nostamp.mov looked",
	}
	if err := f.clean(t, deleting, documents, inPackage, changed, noStamp); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.results {
		if r.Result != model.ResultSkipped || !strings.Contains(r.Message, want[r.Item.ID]) {
			t.Errorf("%s: %+v, want skipped with %q", r.Item.ID, r, want[r.Item.ID])
		}
	}
	if len(f.trashed) != 0 {
		t.Errorf("trashed %v", f.trashed)
	}
	if _, err := os.Stat(filepath.Join(f.home, "Downloads/a.iso")); err != nil {
		t.Error("a home item must never be deleted")
	}
}

func TestCleanSkipsHomeItemsWithoutThePackageCheck(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/a.iso", 1000)
	f.c.IsPackage = nil
	f.clean(t, f.homeItem(t, "Downloads/a.iso"))
	if r := f.results[0]; r.Result != model.ResultSkipped || !strings.Contains(r.Message, "can't be checked for packages") {
		t.Errorf("result = %+v", r)
	}
}

// dupeSet puts the same bytes at each rel path and returns one Dupes item per copy, keeping the
// first.
func (f *cleanFixture) dupeSet(t *testing.T, rels ...string) []model.CleanItem {
	t.Helper()
	var copies []model.Copy
	for i, rel := range rels {
		p := f.put(t, rel, 1000)
		s, err := stampNow(p)
		if err != nil {
			t.Fatal(err)
		}
		copies = append(copies, model.Copy{Path: p, Stamp: s, Keep: i == 0})
	}
	var items []model.CleanItem
	for i, c := range copies {
		s := c.Stamp
		items = append(items, model.CleanItem{ID: "dupes/" + rels[i], Name: "~/" + rels[i], Method: model.MethodTrash, Paths: []string{c.Path},
			Root: f.home, Stamp: &s, Set: "set1", Copies: copies, Keep: i == 0, Size: 1000})
	}
	return items
}

func TestCleanDupesLeavesTheKeptCopy(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov", "Desktop/trip 3.mov")
	if err := f.clean(t, set[1], set[2]); err != nil {
		t.Fatal(err)
	}
	if len(f.trashed) != 2 || f.results[0].Result != model.ResultDone || f.results[1].Result != model.ResultDone {
		t.Errorf("trashed %v, results %+v", f.trashed, f.results)
	}
	if _, err := os.Stat(set[0].Paths[0]); err != nil {
		t.Error("the kept copy is gone")
	}
}

func TestCleanRefusesEveryCopyOfASet(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov")
	if err := f.clean(t, set...); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.results {
		if r.Result != model.ResultSkipped || r.Message != "Keep at least one copy of trip.mov." {
			t.Errorf("%s: %+v", r.Item.ID, r)
		}
	}
	if len(f.trashed) != 0 {
		t.Errorf("trashed %v", f.trashed)
	}
}

func TestCleanSkipsASetWhoseKeptCopyChanged(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov", "Desktop/trip 3.mov")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(set[0].Paths[0], later, later); err != nil {
		t.Fatal(err)
	}
	if err := f.clean(t, set[1], set[2]); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.results {
		if r.Result != model.ResultSkipped || !strings.Contains(r.Message, "No copy of trip.mov outside this clean-up is still as scanned, so this one wasn't moved.") {
			t.Errorf("%s: %+v", r.Item.ID, r)
		}
	}
	if len(f.trashed) != 0 {
		t.Errorf("trashed %v; no copy may go while the kept one changed", f.trashed)
	}
	f.results, f.trashed = nil, nil
	if err := os.Remove(set[0].Paths[0]); err != nil {
		t.Fatal(err)
	}
	f.clean(t, set[1], set[2])
	if len(f.trashed) != 0 || len(f.results) != 2 || f.results[0].Result != model.ResultSkipped || f.results[1].Result != model.ResultSkipped {
		t.Errorf("with the kept copy gone: trashed %v, %+v", f.trashed, f.results)
	}
	f.results = nil
	f.clean(t, set[1])
	if len(f.trashed) != 1 || f.results[0].Result != model.ResultDone {
		t.Errorf("Desktop/trip 3.mov stays as scanned, so Downloads/trip 2.mov may go: trashed %v, %+v", f.trashed, f.results)
	}
}

func TestCleanSkipsAChangedCopy(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov")
	f.put(t, "Downloads/trip 2.mov", 1000) // written again: same size, new modified time
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(set[1].Paths[0], later, later); err != nil {
		t.Fatal(err)
	}
	f.clean(t, set[1])
	if r := f.results[0]; r.Result != model.ResultSkipped || !strings.Contains(r.Message, "changed since the scan") || len(f.trashed) != 0 {
		t.Errorf("result = %+v, trashed %v", r, f.trashed)
	}
}

func (f *cleanFixture) mustClean(t *testing.T, items ...model.CleanItem) {
	t.Helper()
	if err := f.clean(t, items...); err != nil {
		t.Fatal(err)
	}
}

func (f *cleanFixture) exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestCleanMixedBatchNeverMovesTheKeptCopyOfASet(t *testing.T) {
	for _, diskFirst := range []bool{false, true} {
		f := homeFixture(t)
		set := f.dupeSet(t, "Movies/old/trip.mov", "Downloads/trip 2.mov")
		disk := f.homeItem(t, "Movies/old") // holds the kept copy
		items := []model.CleanItem{set[1], disk}
		if diskFirst {
			items = []model.CleanItem{disk, set[1]}
		}
		f.mustClean(t, items...)
		if !f.exists(set[0].Paths[0]) && !f.exists(set[1].Paths[0]) {
			t.Errorf("diskFirst=%v: no copy is left outside the Trash; trashed %v, results %+v", diskFirst, f.trashed, f.results)
		}
	}
}

func TestCleanKeptCopyTickedWithAnotherCopyLeft(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov", "Desktop/trip 3.mov")
	f.mustClean(t, set[0], set[1])
	if !f.exists(set[2].Paths[0]) {
		t.Errorf("the last copy is gone: trashed %v, results %+v", f.trashed, f.results)
	}
}

func TestCleanSameItemTwiceMovesItOnce(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov")
	f.mustClean(t, set[1], set[1])
	if len(f.trashed) != 1 || f.results[0].Result != model.ResultDone || f.results[1].Result != model.ResultSkipped {
		t.Errorf("trashed %v, results %+v", f.trashed, f.results)
	}
	if !f.exists(set[0].Paths[0]) {
		t.Error("the kept copy is gone")
	}
}

func TestCleanKeptCopyBehindASymlinkDoesNotCount(t *testing.T) {
	f := homeFixture(t)
	set := f.dupeSet(t, "Movies/a/trip.mov", "Downloads/trip 2.mov")
	if err := os.Rename(filepath.Join(f.home, "Movies/a"), filepath.Join(f.home, "Movies/b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.home, "Movies/b"), filepath.Join(f.home, "Movies/a")); err != nil {
		t.Fatal(err)
	}
	f.mustClean(t, set[1])
	if len(f.trashed) != 0 || f.results[0].Result != model.ResultSkipped {
		t.Errorf("trashed %v, results %+v", f.trashed, f.results)
	}
}

func TestCleanSkipsHomeRootItemWithoutAStamp(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/a.iso", 1000)
	it := f.homeItem(t, "Downloads/a.iso")
	it.Stamp = nil
	f.mustClean(t, it)
	if r := f.results[0]; r.Result != model.ResultSkipped || !strings.Contains(r.Message, "no record of how a.iso looked") || len(f.trashed) != 0 {
		t.Errorf("result = %+v, trashed %v", r, f.trashed)
	}
}

func TestCleanSkipsHomeRootItemBehindASymlinkedParent(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/x/f.bin", 1000)
	it := f.homeItem(t, "Downloads/x/f.bin")
	if err := os.MkdirAll(filepath.Join(f.home, "Applications/Tool.app/Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.home, "Downloads/x"), filepath.Join(f.home, "Applications/Tool.app/Contents/x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.home, "Applications/Tool.app/Contents/x"), filepath.Join(f.home, "Downloads/x")); err != nil {
		t.Fatal(err)
	}
	f.mustClean(t, it)
	if r := f.results[0]; r.Result != model.ResultSkipped || len(f.trashed) != 0 {
		t.Errorf("result = %+v, trashed %v", r, f.trashed)
	}
}

func TestCleanRefusesASetItemThatIsNotATrashItemAtHome(t *testing.T) {
	deleting := func(it *model.CleanItem) { it.Method = model.MethodDelete }
	elsewhere := func(it *model.CleanItem) { it.Root = filepath.Join(it.Root, "Downloads") }
	for name, mutate := range map[string]func(*model.CleanItem){"delete": deleting, "other root": elsewhere} {
		f := homeFixture(t)
		set := f.dupeSet(t, "Movies/trip.mov", "Downloads/trip 2.mov")
		mutate(&set[1])
		f.mustClean(t, set[1])
		r := f.results[0]
		if r.Result != model.ResultFailed || r.Message != "Copies from Dupes can only go to the Trash." || !f.exists(set[1].Paths[0]) || len(f.trashed) != 0 {
			t.Errorf("%s: result = %+v, trashed %v", name, r, f.trashed)
		}
	}
}

// The disk ignores case and Unicode normalization, so a Disk item can name the kept copy, or a
// folder above it, with a different spelling than the Dupes item does.
func TestCleanMixedBatchMatchesPathsByFileIdentity(t *testing.T) {
	cases := map[string]struct{ kept, disk string }{
		"case-variant folder": {"Movies/old/trip.mov", "movies/old"},
		"case-variant file":   {"Movies/trip.mov", "Movies/TRIP.mov"},
		"NFD folder name":     {"Movies/caf\u00e9/trip.mov", "Movies/cafe\u0301"},
	}
	for name, c := range cases {
		f := homeFixture(t)
		set := f.dupeSet(t, c.kept, "Downloads/trip 2.mov")
		if _, err := os.Lstat(filepath.Join(f.home, c.disk)); err != nil {
			t.Skipf("%s: this volume tells spellings apart, so the variant doesn't exist", name)
		}
		disk := f.homeItem(t, c.disk)
		f.mustClean(t, set[1], disk)
		if !f.exists(set[0].Paths[0]) && !f.exists(set[1].Paths[0]) {
			t.Errorf("%s: no copy is left outside the Trash; trashed %v, results %+v", name, f.trashed, f.results)
		}
		if f.results[0].Result != model.ResultSkipped {
			t.Errorf("%s: the Dupes copy should be skipped: %+v", name, f.results[0])
		}
	}
}

func TestCleanSkipsHomeRootItemWhenTheCleanerHasNoStamp(t *testing.T) {
	f := homeFixture(t)
	f.put(t, "Downloads/a.iso", 1000)
	it := f.homeItem(t, "Downloads/a.iso")
	f.c.Stamp = nil
	f.mustClean(t, it)
	if r := f.results[0]; r.Result != model.ResultSkipped || !strings.Contains(r.Message, "can't be checked for changes") || len(f.trashed) != 0 {
		t.Errorf("result = %+v, trashed %v", r, f.trashed)
	}
}

// appFixture is a clean fixture that can check Apps items. Its apps are in the temp home's
// Applications folder, and names ending in .bundle are packages too. AppCheck refuses the apps
// named in refuse, and records what it was asked.
type appFixture struct {
	*cleanFixture
	refuse  map[string]string // bundle name → why AppCheck refuses it
	checked []string
}

func newAppFixture(t *testing.T) *appFixture {
	t.Helper()
	f := &appFixture{cleanFixture: homeFixture(t), refuse: map[string]string{}}
	isPackage := f.c.IsPackage
	f.c.IsPackage = func(p string) bool { return isPackage(p) || strings.HasSuffix(p, ".bundle") }
	f.c.AppCheck = func(p string) error {
		f.checked = append(f.checked, p)
		if why, ok := f.refuse[filepath.Base(p)]; ok {
			return errors.New(why)
		}
		return nil
	}
	return f
}

// app makes ~/Applications/<name>.app and returns its item, stamped as it is now.
func (f *appFixture) app(t *testing.T, name string) model.CleanItem {
	t.Helper()
	p := filepath.Dir(f.put(t, "Applications/"+name+".app/Contents/Info.plist", 500))
	p = filepath.Dir(p)
	s, err := stampNow(p)
	if err != nil {
		t.Fatal(err)
	}
	return model.CleanItem{ID: "apps/com.example." + name, Screen: model.ScreenApps, Name: name + ".app", Method: model.MethodTrash,
		Paths: []string{p}, Root: filepath.Join(f.home, "Applications"), Bundle: p, Stamp: &s, Size: 1000}
}

// leftover makes home/rel and returns its item as a leftover of app.
func (f *appFixture) leftover(t *testing.T, app model.CleanItem, rel string) model.CleanItem {
	t.Helper()
	f.put(t, rel, 100)
	it := f.homeItem(t, rel)
	it.ID, it.Screen, it.Bundle = "apps/com.example/"+rel, model.ScreenApps, app.Bundle
	return it
}

func TestCleanMovesAnAppWithItsLeftovers(t *testing.T) {
	f := newAppFixture(t)
	notion := f.app(t, "Notion")
	caches := f.leftover(t, notion, "Library/Caches/com.example.Notion")
	prefs := f.leftover(t, notion, "Library/Preferences/com.example.Notion.plist")
	f.mustClean(t, caches, notion, prefs) // the app comes after one of its leftovers
	for _, r := range f.results {
		if r.Result != model.ResultDone {
			t.Errorf("%s: %+v", r.Item.ID, r)
		}
	}
	if len(f.trashed) != 3 || f.trashed[0] != notion.Bundle {
		t.Errorf("trashed %v; the app goes first", f.trashed)
	}
	if len(f.checked) == 0 || f.checked[0] != notion.Bundle {
		t.Errorf("AppCheck was asked about %v", f.checked)
	}
	list, _ := f.c.History.List()
	if len(list) != 3 || !list[2].CanRestore() || list[2].Paths[0] != notion.Bundle {
		t.Errorf("history = %+v", list)
	}
}

func TestCleanKeepsTheLeftoversOfAnAppThatStays(t *testing.T) {
	cases := map[string]func(f *appFixture, app *model.CleanItem) map[string]bool{
		"busy": func(f *appFixture, app *model.CleanItem) map[string]bool {
			return model.RunningNames([]model.Process{{Path: app.Bundle + "/Contents/MacOS/Notion"}})
		},
		"refused": func(f *appFixture, app *model.CleanItem) map[string]bool {
			f.refuse["Notion.app"] = "Notion needs your password; move it to the Trash in Finder"
			return nil
		},
		"changed": func(f *appFixture, app *model.CleanItem) map[string]bool {
			app.Stamp.Inode++
			return nil
		},
		"blocked": func(f *appFixture, app *model.CleanItem) map[string]bool {
			app.Blocked = "needs your password"
			return nil
		},
		"failed move": func(f *appFixture, app *model.CleanItem) map[string]bool {
			move := f.c.Trash
			f.c.Trash = func(p string) (string, error) {
				if p == app.Bundle {
					return "", errors.New("couldn't move Notion.app to the Trash")
				}
				return move(p)
			}
			return nil
		},
	}
	for name, setUp := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAppFixture(t)
			notion := f.app(t, "Notion")
			caches := f.leftover(t, notion, "Library/Caches/com.example.Notion")
			data := f.leftover(t, notion, "Library/Application Support/Notion")
			running := setUp(f, &notion)
			if err := f.c.Clean(context.Background(), []model.CleanItem{caches, notion, data}, running, func(r model.CleanResult) { f.results = append(f.results, r) }); err != nil {
				t.Fatal(err)
			}
			if len(f.results) != 3 {
				t.Fatalf("results = %+v", f.results)
			}
			if len(f.trashed) != 0 {
				t.Errorf("trashed %v", f.trashed)
			}
			for _, r := range f.results {
				if r.Item.IsApp() {
					if r.Result == model.ResultDone {
						t.Errorf("the app moved: %+v", r)
					}
					if name == "busy" && r.Message != "Quit Notion first" {
						t.Errorf("the app row: %+v", r)
					}
					continue
				}
				want := "Notion wasn't moved, so its files stay."
				if name == "busy" {
					want = "Quit Notion first"
				}
				if r.Result != model.ResultSkipped || r.Message != want {
					t.Errorf("%s = %+v", r.Item.ID, r)
				}
			}
			if !f.exists(caches.Paths[0]) || !f.exists(data.Paths[0]) || !f.exists(notion.Bundle) {
				t.Error("something moved")
			}
		})
	}
}

func TestCleanMovesLeftoversWithoutTheirApp(t *testing.T) {
	f := newAppFixture(t)
	notion := f.app(t, "Notion")
	caches := f.leftover(t, notion, "Library/Caches/com.example.Notion")
	removed := f.leftover(t, notion, "Library/Caches/com.gone.tool")
	removed.Bundle = ""
	f.mustClean(t, caches, removed)
	if len(f.trashed) != 2 || f.results[0].Result != model.ResultDone || f.results[1].Result != model.ResultDone {
		t.Errorf("trashed %v, results %+v", f.trashed, f.results)
	}
	if !f.exists(notion.Bundle) {
		t.Error("the app stays")
	}
}

func TestCleanSkipsABusyAppAndItsLeftovers(t *testing.T) {
	f := newAppFixture(t)
	notion := f.app(t, "Notion")
	caches := f.leftover(t, notion, "Library/Caches/com.example.Notion")
	running := model.RunningNames([]model.Process{{ProcessID: model.ProcessID{Name: "Notion Helper"},
		Path: notion.Bundle + "/Contents/Frameworks/Notion Helper.app/Contents/MacOS/Notion Helper"}})
	err := f.c.Clean(context.Background(), []model.CleanItem{caches}, running, func(r model.CleanResult) { f.results = append(f.results, r) })
	if err != nil || len(f.results) != 1 {
		t.Fatalf("err = %v, results = %+v", err, f.results)
	}
	if r := f.results[0]; r.Result != model.ResultSkipped || r.Message != "Quit Notion first" || len(f.trashed) != 0 {
		t.Errorf("a leftover of a running app stays: %+v", r)
	}
}

func TestCleanRefusesUnsafeApps(t *testing.T) {
	f := newAppFixture(t)
	deleting := f.app(t, "Deleting")
	deleting.Method = model.MethodDelete
	inside := f.app(t, "Outer")
	inside.Paths = []string{filepath.Join(inside.Bundle, "Contents")}
	inside.Bundle = inside.Paths[0]
	noStamp := f.app(t, "NoStamp")
	noStamp.Stamp = nil
	nested := f.app(t, "Tools.bundle/Inner")
	notHere := f.app(t, "Elsewhere")
	notHere.Root = filepath.Join(f.home, "Downloads")
	want := map[string]string{
		deleting.ID: "Apps moves things only to the Trash.",
		inside.ID:   "Contents is inside Outer.app, which soap moves only as a whole",
		noStamp.ID:  "there's no record of how NoStamp.app looked in the scan",
		nested.ID:   "Inner.app is inside Tools.bundle, which soap moves only as a whole",
		notHere.ID:  "isn't a folder soap cleans inside",
	}
	f.mustClean(t, deleting, inside, noStamp, nested, notHere)
	for _, r := range f.results {
		if r.Result == model.ResultDone || !strings.Contains(r.Message, want[r.Item.ID]) {
			t.Errorf("%s: %+v, want %q", r.Item.ID, r, want[r.Item.ID])
		}
	}
	if len(f.trashed) != 0 {
		t.Errorf("trashed %v", f.trashed)
	}
}

func TestCleanSkipsAppsWithoutTheAppCheck(t *testing.T) {
	f := newAppFixture(t)
	notion := f.app(t, "Notion")
	f.c.AppCheck = nil
	f.mustClean(t, notion)
	if r := f.results[0]; r.Result != model.ResultSkipped || r.Message != "Notion.app can't be checked" || len(f.trashed) != 0 {
		t.Errorf("result = %+v", r)
	}
	f.results = nil
	f.c.AppCheck = func(string) error { return errors.New("Notion comes with macOS") }
	f.mustClean(t, notion)
	if r := f.results[0]; r.Result != model.ResultSkipped || r.Message != "Notion comes with macOS" || len(f.trashed) != 0 {
		t.Errorf("result = %+v", r)
	}
}

func TestCleanRefusesAnAppThatIsNotAFolder(t *testing.T) {
	f := newAppFixture(t)
	file := f.put(t, "Applications/Plain.app", 10)
	s, err := stampNow(file)
	if err != nil {
		t.Fatal(err)
	}
	it := model.CleanItem{ID: "apps/com.example.Plain", Screen: model.ScreenApps, Name: "Plain.app", Method: model.MethodTrash,
		Paths: []string{file}, Root: filepath.Join(f.home, "Applications"), Bundle: file, Stamp: &s, Size: 10}
	f.mustClean(t, it)
	if r := f.results[0]; r.Result == model.ResultDone || len(f.trashed) != 0 || !strings.Contains(r.Message, "isn't an app folder") {
		t.Errorf("result = %+v", r)
	}
}

func TestCleanSkipsAnAppSwappedWhileItIsChecked(t *testing.T) {
	f := newAppFixture(t)
	notion := f.app(t, "Notion")
	check := f.c.AppCheck
	f.c.AppCheck = func(p string) error {
		err := check(p)
		// swap the bundle while the check runs: a new folder with the same name has a new inode
		if e := os.Rename(p, p+".old"); e != nil {
			t.Fatal(e)
		}
		if e := os.MkdirAll(filepath.Join(p, "Contents"), 0o755); e != nil {
			t.Fatal(e)
		}
		return err
	}
	f.mustClean(t, notion)
	if r := f.results[0]; r.Result != model.ResultSkipped || r.Message != "Notion.app changed since the scan" || len(f.trashed) != 0 {
		t.Errorf("result = %+v", r)
	}
}
