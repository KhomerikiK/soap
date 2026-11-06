package model

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DiskTree holds the sizes from one walk of a folder: one node for each folder and package, with
// files left out to save memory. A walk fills it in while screens read it, so every method locks.
type DiskTree struct {
	Root string // the folder walked: absolute and clean

	mu         sync.RWMutex
	top        *DiskNode
	files      uint64
	unreadable int
	finished   bool
	stopped    bool
}

// DiskNode is one folder or package in a DiskTree. Only the tree's methods change it. It's kept
// small, about 80 bytes, because a home folder can hold 400,000 folders.
type DiskNode struct {
	name    string
	own     uint64 // a folder's files and its sub-folders' own entries; all of a package
	size    uint64 // own plus everything in the sub-folders
	parent  *DiskNode
	kids    []*DiskNode
	pending int32 // sub-folders and packages not finished yet
	kind    uint8 // an index into packageKinds; 0 is a plain folder
	partial bool  // something in it couldn't be read
	listed  bool  // its entries were read, or the package was measured
}

// packageKinds are the kinds of package a node can be. Any other kind is stored as "package".
var packageKinds = [...]string{"", "app", "library", "package"}

func kindOf(k string) uint8 {
	for i, s := range packageKinds {
		if s == k {
			return uint8(i)
		}
	}
	return uint8(len(packageKinds) - 1)
}

// DiskChild is a sub-folder or package that a walk found in a folder.
type DiskChild struct {
	Name    string
	Package string // the package's kind, or "" for a folder
}

// DiskEntry is one row of an open folder on the Disk screen: a file, folder or package.
type DiskEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       uint64 `json:"size"`                      // allocated bytes
	Min        bool   `json:"size_is_minimum,omitempty"` // still being walked, or partly unreadable
	Dir        bool   `json:"folder,omitempty"`
	Package    string `json:"package,omitempty"`    // the kind of package, such as "app"
	Unmeasured bool   `json:"unmeasured,omitempty"` // a folder the walk has no size for
	Stamp      Stamp  `json:"-"`
}

// DiskProgress is how far a walk has got.
type DiskProgress struct {
	Files      uint64 // files seen
	Bytes      uint64 // allocated bytes seen
	Unreadable int    // folders that couldn't be read
	Finished   bool   // the walk ended
	Stopped    bool   // it ended before it got through everything
}

// NewDiskTree returns an empty tree for a walk of root.
func NewDiskTree(root string) *DiskTree {
	return &DiskTree{Root: root, top: &DiskNode{name: filepath.Base(root)}}
}

// Top is the node for the folder walked.
func (t *DiskTree) Top() *DiskNode { return t.top }

// Listed records what a walk found directly in folder n: own is the allocated bytes of its files
// and of its sub-folders' own entries, files is how many files it holds, and kids are its
// sub-folders and packages. It returns a node for each kid, in the same order, for the walk to
// list or measure next. A second call for the same folder does nothing.
func (t *DiskTree) Listed(n *DiskNode, own, files uint64, kids []DiskChild) []*DiskNode {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n.listed {
		return nil
	}
	n.listed, n.own = true, own
	t.files += files
	grow(n, own)
	out := make([]*DiskNode, len(kids))
	for i, k := range kids {
		out[i] = &DiskNode{name: k.Name, kind: kindOf(k.Package), parent: n}
	}
	n.kids, n.pending = out, int32(len(out))
	if n.pending == 0 {
		finish(n)
	}
	return out
}

// Measured records the size of package n, which a walk measures as one item. partial means some
// of it couldn't be read.
func (t *DiskTree) Measured(n *DiskNode, size uint64, partial bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n.listed {
		return
	}
	n.listed, n.own = true, size
	grow(n, size)
	if partial {
		markPartial(n)
	}
	finish(n)
}

// Unreadable records that folder n couldn't be listed, so its size and its parents' sizes stay
// a minimum.
func (t *DiskTree) Unreadable(n *DiskNode) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n.listed {
		return
	}
	n.listed = true
	t.unreadable++
	markPartial(n)
	finish(n)
}

// Finish records that the walk ended. stopped means it ended before it got through everything.
func (t *DiskTree) Finish(stopped bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finished, t.stopped = true, stopped
}

// Refresh records what the folder at path holds after a clean-up: own is the allocated bytes of
// its files and sub-folders' own entries now, and present names the sub-folders and packages
// still there. Kids that are gone are dropped, and every size above them shrinks. It reports
// false when the tree has no listed folder at path.
func (t *DiskTree) Refresh(path string, own uint64, present []string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.find(path)
	if n == nil || !n.listed || n.kind != 0 {
		return false
	}
	keep := map[string]bool{}
	for _, name := range present {
		keep[name] = true
	}
	// A new slice, so a walker still ranging over the one Listed returned never sees it change.
	kids := make([]*DiskNode, 0, len(n.kids))
	for _, k := range n.kids {
		if keep[k.name] {
			kids = append(kids, k)
			continue
		}
		shrink(n, k.size)
		if !k.listed || k.pending > 0 { // n was still waiting for it
			n.pending--
			if n.pending == 0 {
				finish(n)
			}
		}
		k.parent = nil // late results for the removed folder must not reach the tree
	}
	n.kids = kids
	if own >= n.own {
		grow(n, own-n.own)
	} else {
		shrink(n, n.own-own)
	}
	n.own = own
	return true
}

// Entry is the row for the folder or package at path. ok is false when the tree has no node for
// it.
func (t *DiskTree) Entry(path string) (e DiskEntry, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.find(path)
	if n == nil {
		return DiskEntry{}, false
	}
	return entryOf(n, path), true
}

// Children are the rows for the sub-folders and packages of the folder at path, in the order the
// walk found them.
func (t *DiskTree) Children(path string) []DiskEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := t.find(path)
	if n == nil {
		return nil
	}
	out := make([]DiskEntry, 0, len(n.kids))
	for _, k := range n.kids {
		out = append(out, entryOf(k, filepath.Join(path, k.name)))
	}
	return out
}

// Progress is how far the walk has got.
func (t *DiskTree) Progress() DiskProgress {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return DiskProgress{Files: t.files, Bytes: t.top.size, Unreadable: t.unreadable, Finished: t.finished, Stopped: t.stopped}
}

func (t *DiskTree) find(path string) *DiskNode {
	if path == t.Root {
		return t.top
	}
	rel, ok := strings.CutPrefix(path, t.Root+"/")
	if !ok {
		return nil
	}
	n := t.top
	for _, part := range strings.Split(rel, "/") {
		var next *DiskNode
		for _, k := range n.kids {
			if k.name == part {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

func entryOf(n *DiskNode, path string) DiskEntry {
	return DiskEntry{Name: n.name, Path: path, Size: n.size, Min: n.partial || !n.listed || n.pending > 0, Dir: true, Package: packageKinds[n.kind]}
}

func grow(n *DiskNode, b uint64) {
	for ; n != nil; n = n.parent {
		n.size += b
	}
}

func shrink(n *DiskNode, b uint64) {
	for ; n != nil; n = n.parent {
		n.size -= min(b, n.size)
	}
}

func markPartial(n *DiskNode) {
	for ; n != nil; n = n.parent {
		n.partial = true
	}
}

// finish is called once, when n is complete: listed, with every kid finished. It finishes the
// parents that were waiting only for n.
func finish(n *DiskNode) {
	for p := n.parent; p != nil; p = p.parent {
		p.pending--
		if !p.listed || p.pending > 0 {
			return
		}
	}
}

// DiskRows merges a folder's listing, which has its files and the stamps of everything in it,
// with kids, the walk's rows for its sub-folders and packages. A folder the walk has no size for
// is marked Unmeasured. Names starting with "." are left out unless hidden is true. Rows are
// sorted by size, largest first, or by name.
func DiskRows(listing, kids []DiskEntry, hidden, byName bool) []DiskEntry {
	walked := map[string]DiskEntry{}
	for _, k := range kids {
		walked[k.Name] = k
	}
	var out []DiskEntry
	for _, e := range listing {
		if !hidden && strings.HasPrefix(e.Name, ".") {
			continue
		}
		if e.Dir {
			if k, ok := walked[e.Name]; ok {
				e.Size, e.Min = k.Size, k.Min
				if e.Package == "" {
					e.Package = k.Package
				}
			} else {
				e.Size, e.Min, e.Unmeasured = 0, false, true
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !byName && a.Size != b.Size {
			return a.Size > b.Size
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// DiskItem is the clean item for a row marked on the Disk screen. It goes to the Trash, and the
// cleaner skips it if it changed since it was listed.
func DiskItem(home string, e DiskEntry) CleanItem {
	detail := "file"
	switch {
	case e.Package != "":
		detail = e.Package
	case e.Dir:
		detail = "folder"
	}
	stamp := e.Stamp
	return CleanItem{
		ID: DiskID(home, e.Path), Screen: ScreenDisk, Group: "Disk", Name: HomePath(home, e.Path),
		Detail: detail, Size: e.Size, SizeMin: e.Min, Method: MethodTrash, Paths: []string{e.Path}, Root: home, Stamp: &stamp,
	}
}

// DiskID is the ID of the Disk item for path, such as "disk/Movies/trip.mov".
func DiskID(home, path string) string { return "disk/" + strings.TrimPrefix(path, home+"/") }

// HomePath shows path with the home folder written as "~".
func HomePath(home, path string) string {
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rel
	}
	return path
}

// InICloudDrive reports whether path is inside the iCloud Drive storage in home, the
// Library/Mobile Documents folder. Removing such an item removes it from iCloud on every device.
func InICloudDrive(home, path string) bool {
	return strings.HasPrefix(path, home+"/Library/Mobile Documents/")
}

// ICloudWarning is the line a preview shows when something it removes is in iCloud Drive.
const ICloudWarning = "Items in iCloud Drive are removed from iCloud on every device."
