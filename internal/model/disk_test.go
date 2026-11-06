package model

import (
	"strings"
	"sync"
	"testing"
)

// smallTree is /h with Projects (holding app and Tool.app) and Music, walked to the end.
func smallTree(t *testing.T) *DiskTree {
	t.Helper()
	tree := NewDiskTree("/h")
	kids := tree.Listed(tree.Top(), 10, 2, []DiskChild{{Name: "Projects"}, {Name: "Music"}})
	projects, music := kids[0], kids[1]
	inProjects := tree.Listed(projects, 100, 1, []DiskChild{{Name: "app"}, {Name: "Tool.app", Package: "app"}})
	tree.Listed(inProjects[0], 1000, 3, nil)
	tree.Measured(inProjects[1], 5000, false)
	tree.Listed(music, 7, 1, nil)
	tree.Finish(false)
	return tree
}

func TestDiskTreeAddsSizesUpwards(t *testing.T) {
	tree := smallTree(t)
	want := map[string]uint64{"/h": 6117, "/h/Projects": 6100, "/h/Projects/app": 1000, "/h/Projects/Tool.app": 5000, "/h/Music": 7}
	for path, size := range want {
		e, ok := tree.Entry(path)
		if !ok || e.Size != size || e.Min {
			t.Errorf("%s = %+v, %v; want %d, not a minimum", path, e, ok, size)
		}
	}
	if e, _ := tree.Entry("/h/Projects/Tool.app"); e.Package != "app" || !e.Dir {
		t.Errorf("package = %+v", e)
	}
	if _, ok := tree.Entry("/h/nope"); ok {
		t.Error("a path the walk didn't see has no entry")
	}
	if _, ok := tree.Entry("/elsewhere"); ok {
		t.Error("a path outside the root has no entry")
	}
	p := tree.Progress()
	if p.Files != 7 || p.Bytes != 6117 || !p.Finished || p.Stopped {
		t.Errorf("progress = %+v", p)
	}
	var names []string
	for _, c := range tree.Children("/h/Projects") {
		names = append(names, c.Name+"="+c.Path)
	}
	if got := strings.Join(names, ","); got != "app=/h/Projects/app,Tool.app=/h/Projects/Tool.app" {
		t.Errorf("children = %s", got)
	}
}

func TestDiskTreeSizesAreMinimumsUntilEverythingBelowIsDone(t *testing.T) {
	tree := NewDiskTree("/h")
	kids := tree.Listed(tree.Top(), 10, 0, []DiskChild{{Name: "a"}, {Name: "b"}})
	if e, _ := tree.Entry("/h"); !e.Min {
		t.Error("the top isn't finished while its kids aren't")
	}
	deeper := tree.Listed(kids[0], 5, 0, []DiskChild{{Name: "deep"}})
	tree.Listed(kids[1], 5, 0, nil)
	if e, _ := tree.Entry("/h/b"); e.Min {
		t.Error("b is finished")
	}
	if e, _ := tree.Entry("/h"); !e.Min {
		t.Error("the top still waits for a/deep")
	}
	tree.Listed(deeper[0], 1, 0, nil)
	if e, _ := tree.Entry("/h"); e.Min || e.Size != 21 {
		t.Errorf("top = %+v; everything is finished now", e)
	}
}

func TestDiskTreeUnreadableFoldersStayMinimums(t *testing.T) {
	tree := NewDiskTree("/h")
	kids := tree.Listed(tree.Top(), 10, 0, []DiskChild{{Name: "Mail"}, {Name: "ok"}})
	tree.Unreadable(kids[0])
	tree.Listed(kids[1], 5, 0, nil)
	if e, _ := tree.Entry("/h"); !e.Min || e.Size != 15 {
		t.Errorf("top = %+v; it holds a folder that couldn't be read", e)
	}
	if e, _ := tree.Entry("/h/ok"); e.Min {
		t.Error("a readable sibling is exact")
	}
	if p := tree.Progress(); p.Unreadable != 1 {
		t.Errorf("progress = %+v", p)
	}
	tree.Listed(kids[1], 999, 0, nil)
	if e, _ := tree.Entry("/h/ok"); e.Size != 5 {
		t.Error("a second Listed for the same folder must change nothing")
	}
}

func TestDiskTreeRefreshDropsWhatWasRemoved(t *testing.T) {
	tree := smallTree(t)
	if !tree.Refresh("/h/Projects", 40, []string{"app"}) {
		t.Fatal("Refresh found no folder")
	}
	if e, _ := tree.Entry("/h/Projects"); e.Size != 1040 {
		t.Errorf("Projects = %+v; Tool.app is gone and its own files shrank to 40", e)
	}
	if e, _ := tree.Entry("/h"); e.Size != 1057 {
		t.Errorf("top = %+v", e)
	}
	if _, ok := tree.Entry("/h/Projects/Tool.app"); ok {
		t.Error("the removed package is still in the tree")
	}
	if tree.Refresh("/h/nope", 0, nil) || tree.Refresh("/h/Projects/Tool.app", 0, nil) {
		t.Error("Refresh only works on listed folders")
	}
}

func TestDiskTreeRefreshAfterAStoppedWalk(t *testing.T) {
	tree := NewDiskTree("/h")
	kids := tree.Listed(tree.Top(), 1, 0, []DiskChild{{Name: "big"}, {Name: "done"}})
	tree.Listed(kids[1], 2, 0, nil)
	tree.Finish(true)
	tree.Refresh("/h", 1, []string{"done"})
	if e, _ := tree.Entry("/h"); e.Min || e.Size != 3 {
		t.Errorf("top = %+v; the unfinished folder is gone, so nothing is left to wait for", e)
	}
}

func TestDiskTreeIgnoresLateResultsForARemovedFolder(t *testing.T) {
	tree := NewDiskTree("/h")
	kids := tree.Listed(tree.Top(), 1, 0, []DiskChild{{Name: "big"}, {Name: "done"}})
	inner := tree.Listed(kids[0], 5, 0, []DiskChild{{Name: "deep"}}) // big is listed but still waiting for deep
	tree.Listed(kids[1], 2, 0, nil)
	tree.Finish(true)
	tree.Refresh("/h", 1, []string{"done"})
	tree.Listed(inner[0], 100, 0, nil) // a worker that still held big/deep reports late
	if e, _ := tree.Entry("/h"); e.Min || e.Size != 3 {
		t.Errorf("top = %+v; a removed folder's late result changed the tree", e)
	}
}

func TestDiskTreeIsSafeToReadWhileItGrows(t *testing.T) {
	tree := NewDiskTree("/h")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		n := tree.Top()
		for i := 0; i < 200; i++ {
			n = tree.Listed(n, 1, 1, []DiskChild{{Name: "d"}})[0]
		}
		tree.Listed(n, 1, 1, nil)
	}()
	for i := 0; i < 200; i++ {
		tree.Children("/h")
		tree.Progress()
	}
	wg.Wait()
	if e, _ := tree.Entry("/h"); e.Size != 201 || e.Min {
		t.Errorf("top = %+v", e)
	}
}

func TestDiskRows(t *testing.T) {
	listing := []DiskEntry{
		{Name: "a.mov", Path: "/h/a.mov", Size: 300},
		{Name: "Projects", Path: "/h/Projects", Dir: true},
		{Name: ".cache", Path: "/h/.cache", Dir: true},
		{Name: "new", Path: "/h/new", Dir: true},
		{Name: "B.txt", Path: "/h/B.txt", Size: 300},
	}
	kids := []DiskEntry{{Name: "Projects", Size: 900, Min: true, Dir: true}, {Name: ".cache", Size: 50, Dir: true}, {Name: "gone", Size: 5, Dir: true}}
	var got []string
	for _, r := range DiskRows(listing, kids, false, false) {
		s := r.Name
		if r.Min {
			s += "≥"
		}
		if r.Unmeasured {
			s += "?"
		}
		got = append(got, s)
	}
	if strings.Join(got, ",") != "Projects≥,a.mov,B.txt,new?" {
		t.Errorf("rows = %v; largest first, hidden left out, an unwalked folder unmeasured, gone kids dropped", got)
	}
	rows := DiskRows(listing, kids, true, true)
	if rows[0].Name != ".cache" || rows[0].Size != 50 || rows[1].Name != "a.mov" || rows[2].Name != "B.txt" {
		t.Errorf("by name with hidden files: %+v", rows)
	}
}

func TestDiskItem(t *testing.T) {
	e := DiskEntry{Name: "trip.mov", Path: "/h/Movies/trip.mov", Size: 4 << 30, Stamp: Stamp{Size: 5, Inode: 9}}
	it := DiskItem("/h", e)
	if it.ID != "disk/Movies/trip.mov" || it.Name != "~/Movies/trip.mov" || it.Method != MethodTrash || it.Root != "/h" ||
		it.Screen != ScreenDisk || it.Detail != "file" || it.Stamp == nil || it.Stamp.Inode != 9 || len(it.Paths) != 1 {
		t.Errorf("item = %+v", it)
	}
	if err := CheckPath("/h", it.Root, it.Paths[0], it.Method, nil); err != nil {
		t.Errorf("a Disk item must pass the path rule: %v", err)
	}
	if d := DiskItem("/h", DiskEntry{Path: "/h/x", Dir: true}); d.Detail != "folder" {
		t.Errorf("folder detail = %q", d.Detail)
	}
	if p := DiskItem("/h", DiskEntry{Path: "/h/X.app", Dir: true, Package: "app"}); p.Detail != "app" {
		t.Errorf("package detail = %q", p.Detail)
	}
	if HomePath("/h", "/h") != "~" || HomePath("/h", "/h/a/b") != "~/a/b" || HomePath("/h", "/other") != "/other" {
		t.Error("HomePath")
	}
}
