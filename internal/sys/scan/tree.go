//go:build darwin

package scan

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/KhomerikiK/soap/internal/model"
)

// walkers is how many folders a walk reads at once.
const walkers = 8

// Walk fills tree with the size of everything under tree.Root. It follows part 2's size rules:
// allocated blocks, a hard-linked file counted once, iCloud files that aren't downloaded skipped
// without being opened, and symlinks never followed. It stays on tree.Root's volume, and it
// measures each package as one item, keeping no nodes for what's inside. It returns when the
// walk is done or ctx is cancelled, after calling tree.Finish.
func Walk(ctx context.Context, tree *model.DiskTree) {
	w := &walker{tree: tree, sz: NewSizer(), sem: make(chan struct{}, walkers)}
	info, err := os.Lstat(tree.Root)
	st, ok := statOf(info, err)
	if !ok || !info.IsDir() {
		tree.Unreadable(tree.Top())
		tree.Finish(false)
		return
	}
	w.dev = uint64(st.Dev)
	w.folder(ctx, tree.Top(), tree.Root, uint64(st.Blocks)*512)
	w.wg.Wait()
	tree.Finish(ctx.Err() != nil)
}

type walker struct {
	tree *model.DiskTree
	sz   *Sizer
	dev  uint64
	sem  chan struct{}
	wg   sync.WaitGroup
}

// folder lists one folder, records it in the tree, then goes into each sub-folder and package:
// in a new goroutine while fewer than eight run, otherwise in this one. self is the folder's own
// entry, counted only for the top: a sub-folder's entry is counted with its parent's files.
func (w *walker) folder(ctx context.Context, n *model.DiskNode, path string, self uint64) {
	if ctx.Err() != nil {
		return
	}
	entries, own, err := list(path, w.dev, w.sz)
	if err != nil {
		w.tree.Unreadable(n)
		return
	}
	var files uint64
	var kids []model.DiskChild
	var paths []string
	for _, e := range entries {
		if !e.Dir {
			files++
			continue
		}
		kids = append(kids, model.DiskChild{Name: e.Name, Package: e.Package})
		paths = append(paths, e.Path)
	}
	nodes := w.tree.Listed(n, self+own, files, kids)
	for i, kid := range nodes {
		path, pkg := paths[i], kids[i].Package != ""
		job := func() {
			if pkg {
				u := w.sz.Measure(ctx, path)
				w.tree.Measured(kid, u.Bytes, u.Partial)
				return
			}
			w.folder(ctx, kid, path, 0)
		}
		select {
		case w.sem <- struct{}{}:
			w.wg.Add(1)
			go func() {
				defer w.wg.Done()
				defer func() { <-w.sem }()
				job()
			}()
		default:
			job()
		}
	}
}

// List reads the entries directly in dir for the Disk screen: files with their allocated size,
// and folders and packages, each with a stamp. Symlinks, special files, iCloud files that aren't
// downloaded and entries on another volume are left out.
func List(dir string) ([]model.DiskEntry, error) {
	info, err := os.Lstat(dir)
	st, ok := statOf(info, err)
	if err != nil {
		return nil, err
	}
	if !ok || !info.IsDir() {
		return nil, fmt.Errorf("%s isn't a folder", dir)
	}
	entries, _, err := list(dir, uint64(st.Dev), NewSizer())
	return entries, err
}

// list reads dir's entries for List and Walk. own is dir's size as a walk counts it: the files'
// allocated bytes, with sz counting each hard-linked file once, plus the sub-folders' own
// entries. Packages aren't in own; a walk measures each one whole.
func list(dir string, dev uint64, sz *Sizer) (out []model.DiskEntry, own uint64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := e.Info()
		st, ok := statOf(info, err)
		if !ok || uint64(st.Dev) != dev || st.Flags&sfDataless != 0 {
			continue // gone since the listing, on another volume, or not downloaded
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		de := model.DiskEntry{Name: e.Name(), Path: p, Dir: info.IsDir(), Stamp: StampOf(info)}
		switch {
		case de.Dir && IsPackage(p):
			de.Package = PackageKind(e.Name())
		case de.Dir:
			own += uint64(st.Blocks) * 512
		default:
			de.Size = sz.allocated(st)
			own += de.Size
		}
		out = append(out, de)
	}
	return out, own, nil
}

// Remeasure brings tree up to date after a clean-up. Each folder in dirs is listed again: its
// own files are measured again and sub-folders that are gone are dropped, and the sizes of the
// folders above it change to match. Folders that are gone themselves are left alone.
func Remeasure(ctx context.Context, tree *model.DiskTree, dirs []string) {
	for _, dir := range dirs {
		if ctx.Err() != nil {
			return
		}
		info, err := os.Lstat(dir)
		st, ok := statOf(info, err)
		if !ok || !info.IsDir() {
			continue
		}
		entries, own, err := list(dir, uint64(st.Dev), NewSizer())
		if err != nil {
			continue
		}
		var present []string
		for _, e := range entries {
			if e.Dir {
				present = append(present, e.Name)
			}
		}
		if dir == tree.Root {
			own += uint64(st.Blocks) * 512
		}
		tree.Refresh(dir, own, present)
	}
}

// DiskItem turns an ID that soap disk printed, such as "disk/Movies/trip.mov", back into the
// clean item for that path as it is now. It refuses what the Disk screen would never offer. A
// path that doesn't exist gives an error that matches fs.ErrNotExist.
func DiskItem(ctx context.Context, home, id string) (model.CleanItem, error) {
	rel, ok := strings.CutPrefix(id, "disk/")
	if !ok || rel == "" {
		return model.CleanItem{}, fmt.Errorf("%s isn't a Disk ID: %w", id, fs.ErrNotExist)
	}
	path := filepath.Join(home, rel)
	if err := model.CheckPath(home, home, path, model.MethodTrash, nil); err != nil {
		return model.CleanItem{}, err
	}
	if pkg := packageAbove(home, path); pkg != "" {
		return model.CleanItem{}, fmt.Errorf("%s is inside %s, which soap moves only as a whole", model.HomePath(home, path), model.HomePath(home, pkg))
	}
	info, err := os.Lstat(path)
	if err != nil {
		return model.CleanItem{}, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return model.CleanItem{}, fmt.Errorf("%s is a symlink", model.HomePath(home, path))
	case !info.IsDir() && !info.Mode().IsRegular():
		return model.CleanItem{}, fmt.Errorf("%s isn't a file or folder", model.HomePath(home, path))
	}
	e := model.DiskEntry{Name: info.Name(), Path: path, Dir: info.IsDir(), Stamp: StampOf(info)}
	if e.Dir {
		if IsPackage(path) {
			e.Package = PackageKind(info.Name())
		}
		u := NewSizer().Measure(ctx, path)
		e.Size, e.Min = u.Bytes, u.Partial
	} else if st, ok := statOf(info, nil); ok {
		e.Size = uint64(st.Blocks) * 512
	}
	return model.DiskItem(home, e), nil
}

// packageAbove is the closest folder above path, inside home, that is a package, or "".
func packageAbove(home, path string) string {
	for dir := filepath.Dir(path); strings.HasPrefix(dir, home+"/"); dir = filepath.Dir(dir) {
		if IsPackage(dir) {
			return dir
		}
	}
	return ""
}

// allocated is the space a file takes, or 0 when it's a hard link this Sizer has counted already.
func (s *Sizer) allocated(st *syscall.Stat_t) uint64 {
	if st.Nlink > 1 {
		key := [2]uint64{uint64(st.Dev), st.Ino}
		s.mu.Lock()
		dup := s.seen[key]
		s.seen[key] = true
		s.mu.Unlock()
		if dup {
			return 0
		}
	}
	return uint64(st.Blocks) * 512
}

// statOf is the Stat_t behind an Lstat result, and false when there's none.
func statOf(info fs.FileInfo, err error) (*syscall.Stat_t, bool) {
	if err != nil || info == nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}
