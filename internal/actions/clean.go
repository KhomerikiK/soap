package actions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// ErrHistoryUnwritable means nothing was cleaned, because there'd be no record of it.
var ErrHistoryUnwritable = errors.New("Can't write the history file, so nothing was cleaned")

// historyStopped is returned when the history file stops working after cleaning has started.
// It matches ErrHistoryUnwritable, but says that earlier items were cleaned.
type historyStopped struct{ err error }

func (e *historyStopped) Error() string {
	return "Can't write the history file any more, so nothing more was cleaned"
}
func (e *historyStopped) Unwrap() error        { return e.err }
func (e *historyStopped) Is(target error) bool { return target == ErrHistoryUnwritable }

// Cleaner removes clean items. It's the only code in soap that deletes files.
type Cleaner struct {
	Home         string
	ProjectRoots []string
	Run          Runner                            // tool commands; 10-minute timeout in production
	Trash        func(path string) (string, error) // trash.Move in production
	Measure      func(ctx context.Context, path string) uint64
	DiskFree     func() (uint64, error) // free bytes on the home volume
	History      *History
	Now          func() time.Time
	// IsPackage and Stamp check Disk and Dupes items (whose root is the home folder) and Apps items:
	// scan.IsPackage and scan.StampPath in production. Without them those items, and apps, are skipped.
	IsPackage func(path string) bool
	Stamp     func(path string) (model.Stamp, error)
	// AppCheck is the last check on an app before it moves: scan.CheckApp in production, which
	// refuses an app the user doesn't own or can't move, and Apple's apps that come with macOS.
	// Without it apps are skipped.
	AppCheck func(path string) error
}

// batch is what a clean-up knows about all of its items at once: the paths it removes, and the
// sets it must leave alone.
type batch struct {
	removing     map[string]bool   // paths of every Trash and Delete item in this clean-up, Dupes or not
	removingInfo []os.FileInfo     // the same paths as the disk sees them, to match other spellings
	whole        map[string]bool   // sets that this clean-up would remove every copy of
	lost         map[string]string // sets skipped because no kept copy is still as scanned, with why
	apps         map[string]bool   // the bundles of the apps in this clean-up
	gone         map[string]bool   // the apps of this clean-up that went to the Trash
}

func newBatch(items []model.CleanItem) batch {
	b := batch{removing: map[string]bool{}, whole: map[string]bool{}, lost: map[string]string{}, apps: map[string]bool{}, gone: map[string]bool{}}
	for _, it := range items {
		if it.IsApp() {
			b.apps[it.Bundle] = true
		}
		if it.Method == model.MethodTrash || it.Method == model.MethodDelete {
			for _, p := range it.Paths {
				b.removing[p] = true
				if info, err := os.Lstat(p); err == nil { // a path that can't be read is skipped by its own check
					b.removingInfo = append(b.removingInfo, info)
				}
			}
		}
	}
	for _, it := range model.WholeSets(items) {
		b.whole[it.Set] = true
	}
	return b
}

// Clean removes items one at a time, in order, and calls report after each one. Apps' own rows go
// first, so an app that stays is known before its leftovers come up: they stay too. running holds
// the lowercase names of processes running now, and model.RunningNames's bundle keys; an item
// whose app is among them is skipped. Once ctx is cancelled Clean stops before the next item and
// returns ctx.Err(); the item in progress always finishes.
func (c *Cleaner) Clean(ctx context.Context, items []model.CleanItem, running map[string]bool, report func(model.CleanResult)) error {
	if err := c.History.CheckWritable(); err != nil {
		return fmt.Errorf("%w: %v", ErrHistoryUnwritable, err)
	}
	items = model.AppsFirst(items)
	b := newBatch(items)
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 { // the first item was covered by the check above
			if err := c.History.CheckWritable(); err != nil {
				return &historyStopped{err}
			}
		}
		res, entry := c.cleanOne(context.WithoutCancel(ctx), it, running, b)
		if it.IsApp() && res.Result == model.ResultDone {
			b.gone[it.Bundle] = true
		}
		entry.ID = fmt.Sprintf("%d-%d", c.Now().UnixNano(), i)
		if err := c.History.Append(entry); err != nil {
			res.Message = strings.TrimSpace(res.Message + " The history entry wasn't saved: " + err.Error())
			report(res)
			return &historyStopped{err}
		}
		report(res)
	}
	return nil
}

func (c *Cleaner) cleanOne(ctx context.Context, it model.CleanItem, running map[string]bool, b batch) (model.CleanResult, model.HistoryEntry) {
	entry := model.HistoryEntry{Time: c.Now(), ItemID: it.ID, Name: it.Name, Method: it.Method, Paths: it.Paths, Command: it.Command}
	done := func(result model.Result, msg string, freed uint64) (model.CleanResult, model.HistoryEntry) {
		entry.Result, entry.Message, entry.Size = result, msg, freed
		return model.CleanResult{Item: it, Result: result, Freed: freed, Message: msg}, entry
	}
	if it.Blocked != "" {
		return done("skipped", it.Blocked, 0)
	}
	if it.App != "" && running[strings.ToLower(it.App)] {
		return done("skipped", "Quit "+it.App+" first", 0)
	}
	if it.Bundle != "" && running[model.BundleKey(it.Bundle)] {
		return done(model.ResultSkipped, "Quit "+it.BusyName()+" first", 0)
	}
	if it.Screen == model.ScreenApps && it.Method != model.MethodTrash {
		return done(model.ResultFailed, "Apps moves things only to the Trash.", 0)
	}
	if it.Bundle != "" && !it.IsApp() && b.apps[it.Bundle] && !b.gone[it.Bundle] {
		return done(model.ResultSkipped, model.AppName(it.Bundle)+" wasn't moved, so its files stay.", 0)
	}
	if it.Set != "" {
		if it.Method != model.MethodTrash || it.Root != c.Home {
			return done(model.ResultFailed, "Copies from Dupes can only go to the Trash.", 0)
		}
		if b.whole[it.Set] {
			return done(model.ResultSkipped, model.KeepMessage(it), 0)
		}
		if why, ok := b.lost[it.Set]; ok {
			return done(model.ResultSkipped, why, 0)
		}
	}
	switch it.Method {
	case model.MethodCommand:
		if len(it.Command) == 0 {
			return done("failed", "there's no command for this item", 0)
		}
		before, canMeasure := c.measureCommand(ctx, it)
		if _, err := c.Run(ctx, it.Command[0], it.Command[1:]...); err != nil {
			return done("failed", firstLine(err.Error()), 0)
		}
		if !canMeasure {
			return done("done", "Ran the command, but couldn't measure what it freed.", 0)
		}
		after, ok := c.measureCommand(ctx, it)
		if !ok {
			return done("done", "Ran the command, but couldn't measure what it freed.", 0)
		}
		var freed uint64
		if len(it.SizeOf) > 0 && before > after {
			freed = before - after
		} else if len(it.SizeOf) == 0 && after > before {
			freed = after - before
		}
		if freed == 0 && len(it.SizeOf) == 0 {
			// Measured as free disk space: Docker and simulator runtimes can give space back later.
			return done("done", "Free space didn't grow yet. Docker and Xcode can take a while to give it back.", 0)
		}
		if freed == 0 {
			return done("done", "The command ran but freed nothing.", 0)
		}
		return done("done", "", freed)
	case model.MethodTrash, model.MethodDelete:
		if len(it.Paths) == 0 {
			return done("failed", "there's nothing to remove for this item", 0)
		}
	default:
		return done("failed", "unknown clean method", 0)
	}
	// An item that changed since the scan is skipped whole. Each path is checked again just
	// before it's removed, in case something changes while the item is being cleaned.
	for _, p := range it.Paths {
		if err := c.check(it, p); err != nil {
			return done("skipped", err.Error(), 0)
		}
	}
	if it.Method == model.MethodTrash {
		moved, lost := 0, false // lost: a move gave no location, so the Trashed list couldn't match Paths
		for _, p := range it.Paths {
			err := c.keepsACopy(it, b)
			if err == nil {
				err = c.check(it, p)
			}
			if err != nil {
				if moved == 0 {
					return done("skipped", err.Error(), 0)
				}
				if lost {
					entry.Trashed = nil
				}
				res, e := done("partial", "Moved part of it to the Trash. "+stopped(err), 0)
				e.Size = it.Size
				return res, e
			}
			dest, err := c.Trash(p)
			if err != nil {
				result := model.ResultFailed
				if moved > 0 {
					result = "partial"
				}
				if lost {
					entry.Trashed = nil
				}
				res, e := done(result, err.Error(), 0)
				e.Size = it.Size
				return res, e
			}
			moved++
			if dest == "" {
				lost = true
			} else {
				entry.Trashed = append(entry.Trashed, dest)
			}
		}
		msg := ""
		if lost {
			entry.Trashed = nil
			msg = "Moved to the Trash. Use Finder's Put Back to restore it."
		}
		res, e := done("done", msg, 0) // moving to the Trash frees nothing until it's emptied
		e.Size = it.Size
		return res, e
	}
	var firstErr, refused error
	tried := 0 // paths whose removal was tried; the refused path and those after it aren't counted
	for i, p := range it.Paths {
		r, err := c.checkAndOpen(it, p)
		if err != nil {
			if i == 0 {
				return done("skipped", err.Error(), 0)
			}
			refused = err
			break
		}
		tried++
		err = removeInRoot(r, strings.TrimPrefix(p, it.Root+"/"))
		r.Close()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil && refused == nil {
		return done("done", "", it.Size)
	}
	var left uint64
	for _, p := range it.Paths {
		left += c.Measure(ctx, p)
	}
	var files int
	for _, p := range it.Paths[:tried] {
		files += countFiles(p)
	}
	var tail string
	switch {
	case refused != nil:
		tail = stopped(refused)
	case files == 0:
		tail = "Some files couldn't be removed."
	case files == 1:
		tail = "1 file couldn't be removed."
	default:
		tail = fmt.Sprintf("%d files couldn't be removed.", files)
	}
	result, freed, msg := model.ResultFailed, uint64(0), "Nothing was deleted. "+tail
	if left < it.Size {
		result, freed = "partial", it.Size-left
		msg = fmt.Sprintf("Partly deleted: %s of %s freed. %s", model.HumanBytes(freed), model.HumanBytes(it.Size), tail)
	}
	res, e := done(result, msg, freed)
	if firstErr != nil {
		e.Message = msg + " First error: " + firstLine(firstErr.Error())
	}
	return res, e
}

// measureCommand reads the number that shows what a command frees: the total size of the item's
// SizeOf folders, or else the free space on the home volume. ok is false when neither is available.
func (c *Cleaner) measureCommand(ctx context.Context, it model.CleanItem) (n uint64, ok bool) {
	if len(it.SizeOf) > 0 && c.Measure != nil {
		for _, p := range it.SizeOf {
			n += c.Measure(ctx, p)
		}
		return n, true
	}
	if len(it.SizeOf) == 0 && c.DiskFree != nil {
		n, err := c.DiskFree()
		return n, err == nil
	}
	return 0, false
}

// stopped says why a clean stopped partway, without changing the case of a file name in it.
func stopped(err error) string {
	return "Stopped: " + firstLine(err.Error()) + "."
}

// countFiles counts the entries under path that aren't folders, without following symlinks.
// A path that's gone counts 0.
func countFiles(path string) int {
	n := 0
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// checkAndOpen runs check on path, then opens it.Root and confirms it's the folder check looked at.
// The caller closes the Root.
func (c *Cleaner) checkAndOpen(it model.CleanItem, path string) (*os.Root, error) {
	if err := c.check(it, path); err != nil {
		return nil, err
	}
	changed := fmt.Errorf("%s's folder changed since the scan", filepath.Base(path))
	rel, ok := strings.CutPrefix(it.Root, c.Home+"/")
	if !ok {
		return nil, changed
	}
	h, err := os.OpenRoot(c.Home)
	if err != nil {
		return nil, err
	}
	r, err := h.OpenRoot(rel) // walks down from home without leaving it, so a swapped folder can't redirect it
	h.Close()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, changed
	}
	if err != nil {
		return nil, err
	}
	opened, err1 := r.Stat(".")
	onDisk, err2 := os.Lstat(it.Root)
	if err1 != nil || err2 != nil || onDisk.Mode()&fs.ModeSymlink != 0 || !os.SameFile(opened, onDisk) {
		r.Close()
		return nil, changed
	}
	return r, nil
}

// removeInRoot deletes rel inside r. If permissions stop it, it makes the folders inside writable
// and tries once more, because some package managers write read-only folders.
func removeInRoot(r *os.Root, rel string) error {
	err := r.RemoveAll(rel)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	_ = fs.WalkDir(r.FS(), rel, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil {
				_ = r.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return r.RemoveAll(rel)
}

// check re-checks a path just before it's removed.
func (c *Cleaner) check(it model.CleanItem, path string) error {
	if err := model.CheckPath(c.Home, it.Root, path, it.Method, c.ProjectRoots); err != nil {
		return err
	}
	if err := noSymlinkBetween(it.Root, path); err != nil {
		return err
	}
	if it.Root != model.SystemApps { // CheckPath allows /Applications only for an app, by the app rule
		if err := insideHome(c.Home, it.Root, path); err != nil {
			return err
		}
	}
	if it.Root == c.Home {
		if it.Stamp == nil {
			return fmt.Errorf("there's no record of how %s looked in the scan", filepath.Base(path))
		}
		if err := c.notInPackage(path); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is gone since the scan", filepath.Base(path))
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is now a symlink", filepath.Base(path))
	}
	if it.Marker != "" {
		if _, err := os.Lstat(it.Marker); err != nil {
			return fmt.Errorf("%s no longer looks like a project folder (%s is missing)", filepath.Base(path), filepath.Base(it.Marker))
		}
	}
	if it.Stamp != nil {
		if c.Stamp == nil {
			return fmt.Errorf("%s can't be checked for changes", filepath.Base(path))
		}
		now, err := c.Stamp(path)
		if err != nil {
			return err
		}
		if !it.Stamp.Same(now) {
			return fmt.Errorf("%s changed since the scan", filepath.Base(path))
		}
	}
	if it.IsApp() || model.IsAppFolder(c.Home, it.Root) {
		return c.checkApp(it, path)
	}
	return nil
}

// checkApp checks an app just before it moves: the scan's record of the bundle, no package around
// it inside its Applications folder, and AppCheck, which looks at who owns it and whether it comes
// with macOS. Without AppCheck, IsPackage or the record, the app stays.
func (c *Cleaner) checkApp(it model.CleanItem, path string) error {
	name := filepath.Base(path)
	if it.Stamp == nil {
		return fmt.Errorf("there's no record of how %s looked in the scan", name)
	}
	if c.IsPackage == nil || c.AppCheck == nil {
		return fmt.Errorf("%s can't be checked", name)
	}
	if !it.Stamp.Dir { // a bundle is a folder; a file or symlink named X.app is not an app
		return fmt.Errorf("%s isn't an app folder", name)
	}
	for dir := filepath.Dir(path); strings.HasPrefix(dir, it.Root+"/"); dir = filepath.Dir(dir) {
		if c.IsPackage(dir) {
			return fmt.Errorf("%s is inside %s, which soap moves only as a whole", name, filepath.Base(dir))
		}
	}
	if err := c.AppCheck(path); err != nil {
		return err
	}
	// AppCheck runs plutil, so the bundle could be swapped meanwhile: compare the stamp again.
	now, err := c.Stamp(path)
	if err != nil {
		return err
	}
	if !it.Stamp.Same(now) {
		return fmt.Errorf("%s changed since the scan", name)
	}
	return nil
}

// notInPackage refuses a path inside a package, such as a file in an app or in the Photos
// library: soap moves a package only as a whole. scan.IsPackage says false both for "not a
// package" and for a failure, so each parent is Lstatted first and any failure stops the item.
func (c *Cleaner) notInPackage(path string) error {
	if c.IsPackage == nil {
		return fmt.Errorf("%s can't be checked for packages", filepath.Base(path))
	}
	for dir := filepath.Dir(path); strings.HasPrefix(dir, c.Home+"/"); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err != nil {
			return fmt.Errorf("%s can't be checked for packages", filepath.Base(path))
		}
		if c.IsPackage(dir) {
			return fmt.Errorf("%s is inside %s, which soap moves only as a whole", filepath.Base(path), filepath.Base(dir))
		}
	}
	return nil
}

// removes reports whether this clean-up removes path, or a folder that holds it. The disk ignores
// case and Unicode normalization, so besides the spelling it compares file identity, for the copy
// and for each folder above it inside the home folder.
func (c *Cleaner) removes(b batch, path string) bool {
	for q := range b.removing {
		if path == q || strings.HasPrefix(path, q+"/") {
			return true
		}
	}
	for p := path; strings.HasPrefix(p, c.Home+"/"); p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return true // can't tell, so count it as removed
		}
		for _, r := range b.removingInfo {
			if os.SameFile(info, r) {
				return true
			}
		}
	}
	return false
}

// keepsACopy checks, just before a Dupes copy goes to the Trash, that a copy of its set outside
// this clean-up is still there as the scan saw it. When none is, the rest of the set is skipped too.
func (c *Cleaner) keepsACopy(it model.CleanItem, b batch) error {
	if it.Set == "" {
		return nil
	}
	for _, other := range it.Copies {
		if !c.removes(b, other.Path) && c.unchanged(other) {
			return nil
		}
	}
	why := fmt.Sprintf("No copy of %s outside this clean-up is still as scanned, so this one wasn't moved.", model.SetName(it))
	b.lost[it.Set] = why
	return errors.New(why)
}

// unchanged reports whether a copy is still a file inside the home folder, not behind a symlink,
// with the size, modified time and inode the scan saw. Any failure counts as changed.
func (c *Cleaner) unchanged(cp model.Copy) bool {
	if c.Stamp == nil || model.CheckPath(c.Home, c.Home, cp.Path, model.MethodTrash, nil) != nil || noSymlinkBetween(c.Home, cp.Path) != nil {
		return false
	}
	now, err := c.Stamp(cp.Path)
	return err == nil && !now.Dir && cp.Stamp.Same(now)
}

// noSymlinkBetween Lstats root and every folder below it down to path's parent. A missing folder
// or a symlink stops the clean, so a folder swapped for a symlink after the scan is never followed.
func noSymlinkBetween(root, path string) error {
	dir := root
	rest := strings.TrimPrefix(filepath.Dir(path), root)
	for _, part := range append([]string{""}, strings.Split(strings.Trim(rest, "/"), "/")...) {
		if part != "" {
			dir = filepath.Join(dir, part)
		}
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s is gone since the scan", filepath.Base(path))
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is now behind a symlink", filepath.Base(path))
		}
	}
	return nil
}

// insideHome checks that root, with symlinks resolved, is strictly inside the resolved home
// folder, or is the home folder itself when root is home.
func insideHome(home, root, path string) error {
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if (root != home || realRoot != realHome) && !strings.HasPrefix(realRoot, realHome+"/") {
		return fmt.Errorf("%s's folder now leads outside your home folder", filepath.Base(path))
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
