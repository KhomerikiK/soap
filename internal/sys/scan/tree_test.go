//go:build darwin

package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

func walked(t *testing.T, home string) *model.DiskTree {
	t.Helper()
	tree := model.NewDiskTree(home)
	Walk(context.Background(), tree)
	return tree
}

func entry(t *testing.T, tree *model.DiskTree, rel string) model.DiskEntry {
	t.Helper()
	e, ok := tree.Entry(filepath.Join(tree.Root, rel))
	if !ok {
		t.Fatalf("the tree has no %s", rel)
	}
	return e
}

func TestWalkMatchesTheSizer(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Projects/app/a.bin", 300_000)
	put(t, home, "Projects/app/src/b.bin", 200_000)
	put(t, home, "Movies/trip.mov", 1_000_000)
	put(t, home, ".hidden/x", 50_000)
	tree := walked(t, home)
	for _, rel := range []string{"Projects", "Projects/app", "Projects/app/src", "Movies", ".hidden"} {
		want := NewSizer().Measure(context.Background(), filepath.Join(home, rel)).Bytes
		if got := entry(t, tree, rel); got.Size != want || got.Min {
			t.Errorf("%s = %+v; want %d, like the Sizer, and not a minimum", rel, got, want)
		}
	}
	top, _ := tree.Entry(home)
	if want := NewSizer().Measure(context.Background(), home).Bytes; top.Size != want || top.Min {
		t.Errorf("top = %+v, want %d", top, want)
	}
	if p := tree.Progress(); p.Files != 4 || !p.Finished || p.Stopped || p.Unreadable != 0 {
		t.Errorf("progress = %+v", p)
	}
}

func TestWalkCountsHardLinksOnceAndSkipsSymlinks(t *testing.T) {
	home := t.TempDir()
	f := put(t, home, "one/big.bin", 1_000_000)
	if err := os.MkdirAll(filepath.Join(home, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(f, filepath.Join(home, "two/big.bin")); err != nil {
		t.Fatal(err)
	}
	outside := put(t, t.TempDir(), "elsewhere.bin", 2_000_000)
	if err := os.Symlink(outside, filepath.Join(home, "link.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(home, "linkdir")); err != nil {
		t.Fatal(err)
	}
	tree := walked(t, home)
	one, two := entry(t, tree, "one"), entry(t, tree, "two")
	if one.Size+two.Size < 1_000_000 || one.Size+two.Size > 1_100_000 {
		t.Errorf("one = %d, two = %d; the hard-linked file counts once", one.Size, two.Size)
	}
	if top, _ := tree.Entry(home); top.Size > 1_200_000 {
		t.Errorf("top = %d; symlinks must not be followed", top.Size)
	}
	if _, ok := tree.Entry(filepath.Join(home, "linkdir")); ok {
		t.Error("a symlinked folder must not become a node")
	}
}

func TestWalkMeasuresPackagesAsOneItem(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Applications/Tool.app/Contents/MacOS/tool", 500_000)
	put(t, home, "Applications/Tool.app/Contents/Resources/r.bin", 500_000)
	tree := walked(t, home)
	app := entry(t, tree, "Applications/Tool.app")
	if app.Package != "app" || app.Size < 1_000_000 || app.Min {
		t.Errorf("Tool.app = %+v", app)
	}
	if kids := tree.Children(filepath.Join(home, "Applications/Tool.app")); len(kids) != 0 {
		t.Errorf("the walk kept nodes inside a package: %+v", kids)
	}
	if apps, want := entry(t, tree, "Applications"), NewSizer().Measure(context.Background(), filepath.Join(home, "Applications")).Bytes; apps.Size != want {
		t.Errorf("Applications = %+v, want %d", apps, want)
	}
}

func TestWalkMarksUnreadableFolders(t *testing.T) {
	home := t.TempDir()
	put(t, home, "ok/a", 10_000)
	put(t, home, "Mail/secret", 10_000)
	locked := filepath.Join(home, "Mail")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	tree := walked(t, home)
	if e := entry(t, tree, "Mail"); !e.Min {
		t.Errorf("Mail = %+v; it couldn't be read, so its size is a minimum", e)
	}
	if top, _ := tree.Entry(home); !top.Min {
		t.Error("the top holds an unreadable folder, so it's a minimum too")
	}
	if e := entry(t, tree, "ok"); e.Min {
		t.Error("a readable sibling is exact")
	}
	if p := tree.Progress(); p.Unreadable != 1 {
		t.Errorf("progress = %+v", p)
	}
}

func TestWalkStopsWhenCancelled(t *testing.T) {
	home := t.TempDir()
	put(t, home, "a/b/c", 1000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tree := model.NewDiskTree(home)
	Walk(ctx, tree)
	if p := tree.Progress(); !p.Finished || !p.Stopped {
		t.Errorf("progress = %+v", p)
	}
	if top, _ := tree.Entry(home); !top.Min {
		t.Error("a stopped walk leaves sizes as minimums")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a.bin", 100_000)
	put(t, dir, ".dot", 10)
	put(t, dir, "sub/x", 10)
	put(t, dir, "Tool.app/Contents/x", 10)
	if err := os.Symlink(filepath.Join(dir, "a.bin"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.DiskEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if len(got) != 4 {
		t.Errorf("entries = %+v; the symlink is left out", entries)
	}
	if a := got["a.bin"]; a.Dir || a.Size < 100_000 || a.Path != filepath.Join(dir, "a.bin") || a.Stamp.Size != 100_000 || a.Stamp.Inode == 0 {
		t.Errorf("a.bin = %+v", a)
	}
	if s := got["sub"]; !s.Dir || s.Package != "" || !s.Stamp.Dir {
		t.Errorf("sub = %+v", s)
	}
	if p := got["Tool.app"]; !p.Dir || p.Package != "app" {
		t.Errorf("Tool.app = %+v", p)
	}
	if _, err := List(filepath.Join(dir, "a.bin")); err == nil {
		t.Error("listing a file should be an error")
	}
}

func TestRemeasureAfterARemoval(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Downloads/big.iso", 2_000_000)
	put(t, home, "Downloads/keep.txt", 10_000)
	put(t, home, "Downloads/old/x.bin", 1_000_000)
	put(t, home, "Documents/a", 10_000)
	tree := walked(t, home)
	if err := os.Remove(filepath.Join(home, "Downloads/big.iso")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "Downloads/old")); err != nil {
		t.Fatal(err)
	}
	Remeasure(context.Background(), tree, []string{filepath.Join(home, "Downloads"), filepath.Join(home, "gone")})
	want := NewSizer().Measure(context.Background(), filepath.Join(home, "Downloads")).Bytes
	if d := entry(t, tree, "Downloads"); d.Size != want {
		t.Errorf("Downloads = %d, want %d after measuring it again", d.Size, want)
	}
	if _, ok := tree.Entry(filepath.Join(home, "Downloads/old")); ok {
		t.Error("the removed folder is still in the tree")
	}
	if top, _ := tree.Entry(home); top.Size != NewSizer().Measure(context.Background(), home).Bytes {
		t.Errorf("top = %d; the parents follow", top.Size)
	}
}

func TestDiskItem(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Movies/trip.mov", 100_000)
	put(t, home, "Projects/old/x", 100_000)
	put(t, home, "Pictures/Lib.photoslibrary/originals/a.jpg", 10)
	if err := os.Symlink(filepath.Join(home, "Movies"), filepath.Join(home, "Movies/link")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	it, err := DiskItem(ctx, home, "disk/Movies/trip.mov")
	if err != nil {
		t.Fatal(err)
	}
	if it.Paths[0] != filepath.Join(home, "Movies/trip.mov") || it.Size < 100_000 || it.Stamp == nil || it.Stamp.Size != 100_000 || it.Method != model.MethodTrash {
		t.Errorf("item = %+v", it)
	}
	if dir, err := DiskItem(ctx, home, "disk/Projects/old"); err != nil || dir.Detail != "folder" || dir.Size < 100_000 || !dir.Stamp.Dir {
		t.Errorf("folder item = %+v, %v", dir, err)
	}
	if _, err := DiskItem(ctx, home, "disk/Movies/nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := DiskItem(ctx, home, "junk/caches/x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("not a Disk ID: %v", err)
	}
	for id, want := range map[string]string{
		"disk/Documents":    "never removes",
		"disk/../elsewhere": "isn't inside your home folder",
		"disk/Pictures/Lib.photoslibrary/originals/a.jpg": "moves only as a whole",
		"disk/Movies/link": "symlink",
	} {
		if _, err := DiskItem(ctx, home, id); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error with %q", id, err, want)
		}
	}
}

func TestWalkHasStoppedAllWorkersWhenItReturns(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 40; i++ {
		put(t, home, filepath.Join("d", string(rune('a'+i%26)), string(rune('a'+i/26)), "x", "f"), 10_000)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tree := model.NewDiskTree(home)
	go cancel()
	Walk(ctx, tree)
	before, after := tree.Progress(), model.DiskProgress{}
	top, _ := tree.Entry(home)
	time.Sleep(50 * time.Millisecond)
	after = tree.Progress()
	if again, _ := tree.Entry(home); before != after || again != top {
		t.Errorf("the tree changed after Walk returned: %+v then %+v", before, after)
	}
}
