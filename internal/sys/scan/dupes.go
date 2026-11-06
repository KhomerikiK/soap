//go:build darwin

package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

// DupesGroup is the group of every Dupes item. The Dupes screen shows each set on its own.
const DupesGroup = "Copies"

// DupesFinders are the finders behind the Dupes screen: one, which sends a set at a time.
func DupesFinders() []Finder {
	return []Finder{{Group: DupesGroup, Screen: model.ScreenDupes, Stream: findDupes}}
}

// iCloudDrive is the iCloud Drive folder, relative to the home folder.
const iCloudDrive = "Library/Mobile Documents/com~apple~CloudDocs"

// dupesRoots are the user's own folders, relative to the home folder. Nothing else is searched.
var dupesRoots = []string{"Desktop", "Documents", "Downloads", "Pictures", "Movies", "Music", iCloudDrive}

// edge is how much of each end of a file the quick comparison reads.
const edge = 64 << 10

// errChanged means a file changed between the walk and the hashing.
var errChanged = errors.New("changed since the walk")

type dupeFile struct {
	path  string
	size  int64  // length in bytes
	alloc uint64 // allocated bytes
	links uint16 // hard links to the file
	stamp model.Stamp
	made  time.Time
}

// dupeScan is one Dupes scan in progress.
type dupeScan struct {
	env        Env
	add        func([]model.CleanItem, string)
	min        int64
	files      []*dupeFile
	seen       map[[2]uint64]bool // inodes already found, so hard links count as one file
	looked     int                // files looked at
	skipped    int                // folders that couldn't be read
	unreadable int                // files that couldn't be read
	read       uint64             // bytes hashed
	last       time.Time          // when the last progress line was sent
}

// findDupes finds sets of identical files in the user's own folders: same size first, then the
// same first and last 64 KB, then the same SHA-256. It sends each set as soon as it's confirmed.
func findDupes(ctx context.Context, env Env, add func([]model.CleanItem, string)) Result {
	d := &dupeScan{env: env, add: add, min: int64(max(env.Config.DupesMinMB, 1)) << 20, seen: map[[2]uint64]bool{}}
	for _, rel := range dupesRoots {
		root := env.home(rel)
		if info, err := os.Lstat(root); err == nil && info.IsDir() { // a symlinked root is never followed
			d.walk(ctx, root)
		}
	}
	groups := d.sameEdges(ctx, d.sameSize())
	for i, g := range groups {
		if ctx.Err() != nil {
			break
		}
		d.sameContent(ctx, g, i+1, len(groups))
	}
	return Result{Notes: d.notes()}
}

// walk collects the files that could have copies: regular, not hidden, downloaded, and at least
// the minimum size. It skips symlinks, hidden folders, packages and project build folders.
func (d *dupeScan) walk(ctx context.Context, dir string) {
	if ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			d.skipped++
		}
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := e.Info()
		st, ok := statOf(info, err)
		if !ok || st.Flags&sfDataless != 0 {
			continue
		}
		p := filepath.Join(dir, name)
		if info.IsDir() {
			if !IsPackage(p) && !buildFolder(dir, name) {
				d.walk(ctx, p)
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() < d.min {
			continue
		}
		d.looked++
		d.progress(fmt.Sprintf("Looking for copies… %s files checked", model.HumanCount(uint64(d.looked))))
		key := [2]uint64{uint64(st.Dev), st.Ino}
		if d.seen[key] {
			continue
		}
		d.seen[key] = true
		d.files = append(d.files, &dupeFile{path: p, size: info.Size(), alloc: uint64(st.Blocks) * 512, links: st.Nlink, stamp: StampOf(info), made: madeTime(info)})
	}
}

// buildFolder reports whether name in dir is a project folder that a build tool rebuilds, such as
// node_modules next to package.json.
func buildFolder(dir, name string) bool {
	for _, k := range projectKinds {
		if k.folder == name && k.marker(dir, filepath.Join(dir, name)) != "" {
			return true
		}
	}
	return false
}

// sameSize groups the files by size, largest first, keeping sizes with more than one file.
func (d *dupeScan) sameSize() [][]*dupeFile {
	bySize := map[int64][]*dupeFile{}
	for _, f := range d.files {
		bySize[f.size] = append(bySize[f.size], f)
	}
	var out [][]*dupeFile
	for _, g := range bySize {
		if len(g) > 1 {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].size > out[j][0].size })
	return out
}

// sameEdges splits each group by a hash of the first and last 64 KB of each file.
func (d *dupeScan) sameEdges(ctx context.Context, groups [][]*dupeFile) [][]*dupeFile {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	d.progress(fmt.Sprintf("Comparing %d files of the same size…", n))
	var out [][]*dupeFile
	for _, g := range groups {
		out = append(out, d.split(ctx, g, true)...)
	}
	return out
}

// sameContent hashes every file of a group in full and sends each set of identical files.
func (d *dupeScan) sameContent(ctx context.Context, g []*dupeFile, n, total int) {
	status := func() string {
		return fmt.Sprintf("Hashing %d of %d sets · %s read", n, total, model.HumanBytes(d.read))
	}
	d.progress(status())
	for _, set := range d.split(ctx, g, false) {
		if ctx.Err() != nil {
			return
		}
		d.add(d.items(set), status())
	}
}

// split hashes each file of g, the edges only or in full, and returns the groups of more than one
// file with the same hash. Files that can't be read are counted and left out.
func (d *dupeScan) split(ctx context.Context, g []*dupeFile, edges bool) [][]*dupeFile {
	byHash := map[[32]byte][]*dupeFile{}
	var order [][32]byte
	for _, f := range g {
		if ctx.Err() != nil {
			return nil
		}
		sum, err := d.hash(ctx, f, edges)
		if err != nil {
			if ctx.Err() == nil {
				d.unreadable++
			}
			continue
		}
		if _, ok := byHash[sum]; !ok {
			order = append(order, sum)
		}
		byHash[sum] = append(byHash[sum], f)
	}
	var out [][]*dupeFile
	for _, sum := range order {
		if len(byHash[sum]) > 1 {
			out = append(out, byHash[sum])
		}
	}
	return out
}

// hash checks that f is still as the walk saw it and downloaded, then returns the SHA-256 of its
// first and last 64 KB (edges) or of all of it.
func (d *dupeScan) hash(ctx context.Context, f *dupeFile, edges bool) (sum [32]byte, err error) {
	info, err := os.Lstat(f.path)
	st, ok := statOf(info, err)
	if err != nil {
		return sum, err
	}
	if !ok || st.Flags&sfDataless != 0 || !f.stamp.Same(StampOf(info)) {
		return sum, errChanged
	}
	file, err := os.OpenFile(f.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	h := sha256.New()
	if edges && f.size > 2*edge {
		if _, err := io.CopyN(h, file, edge); err != nil {
			return sum, err
		}
		if _, err := file.Seek(-edge, io.SeekEnd); err != nil {
			return sum, err
		}
		if _, err := io.CopyN(h, file, edge); err != nil {
			return sum, err
		}
		d.read += 2 * edge
	} else {
		n, err := io.Copy(h, ctxReader{ctx, file})
		d.read += uint64(n)
		if err != nil {
			return sum, err
		}
		if n != f.size { // the file shrank or grew since the walk, so the hash isn't of the file we listed
			return sum, errChanged
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// items turns a set of identical files into one clean item per copy. Each carries the whole set,
// so the cleaner can check that a copy stays.
func (d *dupeScan) items(files []*dupeFile) []model.CleanItem {
	home := d.env.Home
	copies := make([]model.Copy, len(files))
	for i, f := range files {
		copies[i] = model.Copy{Path: f.path, Made: f.made, Stamp: f.stamp}
	}
	keep := model.KeepCopy(home, copies)
	copies[keep].Keep = true
	ids := sha256.New() // the set's ID comes from its copies' paths, so it stays the same across scans
	for _, f := range files {
		ids.Write([]byte(f.path + "\x00"))
	}
	set := hex.EncodeToString(ids.Sum(nil)[:8])
	out := make([]model.CleanItem, len(files))
	for i, f := range files {
		unshared, err := sys.PrivateSize(f.path)
		if err != nil || unshared > f.alloc {
			unshared = f.alloc
		}
		detail := "made " + f.made.Local().Format("2 Jan 2006")
		why := ""
		switch {
		case f.links > 1: // another name for the same data stays, so removing this one frees nothing
			unshared, why = 0, "hard link"
		case unshared < f.alloc:
			why = "APFS clone"
		}
		if why != "" {
			detail += " · frees " + model.HumanBytes(unshared) + " · " + why
		}
		if strings.HasPrefix(f.path, d.env.home(iCloudDrive)+"/") {
			detail += " · in iCloud Drive"
		}
		stamp := f.stamp
		out[i] = model.CleanItem{
			ID: "dupes/" + homeRel(home, f.path), Screen: model.ScreenDupes, Group: DupesGroup, Name: model.HomePath(home, f.path),
			Detail: detail, Size: f.alloc, Method: model.MethodTrash, Paths: []string{f.path}, Root: home,
			Stamp: &stamp, Set: set, Copies: copies, Keep: i == keep, Unshared: unshared,
		}
	}
	return out
}

// progress sends a progress line, at most five times a second.
func (d *dupeScan) progress(status string) {
	if time.Since(d.last) < 200*time.Millisecond {
		return
	}
	d.last = time.Now()
	d.add(nil, status)
}

func (d *dupeScan) notes() []model.Note {
	var notes []model.Note
	note := func(text string) { notes = append(notes, model.Note{Screen: model.ScreenDupes, Text: text}) }
	switch {
	case d.skipped == 1:
		note("1 folder couldn't be read, so it was skipped. Give your terminal Full Disk Access to include it.")
	case d.skipped > 1:
		note(fmt.Sprintf("%d folders couldn't be read, so they were skipped. Give your terminal Full Disk Access to include them.", d.skipped))
	}
	switch {
	case d.unreadable == 1:
		note("1 file couldn't be read.")
	case d.unreadable > 1:
		note(fmt.Sprintf("%d files couldn't be read.", d.unreadable))
	}
	return notes
}

// ctxReader stops a long read once ctx is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
