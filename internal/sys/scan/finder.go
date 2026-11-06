//go:build darwin

package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

// smallItem is the size below which rows are merged, so long lists stay readable.
const smallItem = 10 << 20

// Config is the user's settings for the project scan and the Dupes scan.
type Config struct {
	ProjectRoots []string `json:"project_roots"` // absolute once loaded
	StaleDays    int      `json:"stale_days"`
	DupesMinMB   int      `json:"dupes_min_mb"` // Dupes leaves out files smaller than this
	UnusedDays   int      `json:"unused_days"`  // Apps calls an app unused when it wasn't opened for this many days
	Skipped      []string `json:"-"`            // one sentence for each project root that was left out
}

// Env is everything a finder needs. Finders read Home, never the real home folder directly.
type Env struct {
	Home     string
	Run      sys.Runner
	LookPath func(file string) (string, error) // exec.LookPath when nil
	Sizer    *Sizer
	Now      func() time.Time
	Config   Config
	Apps     []string // the folders Apps lists apps from: model.AppFolders in production
	UID      int      // the user's ID: Apps says an app anyone else owns needs a password
}

func (e Env) home(rel string) string { return filepath.Join(e.Home, rel) }

// hasTool reports whether a command-line tool is installed.
func (e Env) hasTool(name string) bool {
	look := e.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(name)
	return err == nil
}

// Result is one finder's output. Err means the finder failed as a whole.
type Result struct {
	Items []model.CleanItem
	Notes []model.Note
	Err   error
}

// Finder looks for one kind of item. Its items, notes and error all belong to its Group. A finder
// has Find, or Stream when it reports items as it goes: each call to add sends the items found
// since the last call, with a line saying how far it has got, and the Result it returns at the
// end adds its last items, notes and error.
type Finder struct {
	Group  string
	Screen model.Screen
	Find   func(ctx context.Context, env Env) Result
	Stream func(ctx context.Context, env Env, add func(items []model.CleanItem, status string)) Result
}

// measure fills in each item's size from its paths, eight at a time, and drops items that turn
// out to be empty or gone.
func measure(ctx context.Context, sz *Sizer, items []model.CleanItem) []model.CleanItem {
	// A path that doesn't exist, or is a symlink, can't be cleaned, so it doesn't stay in the item.
	kept := items[:0]
	for _, it := range items {
		var paths []string
		for _, p := range it.Paths {
			if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink == 0 {
				paths = append(paths, p)
			}
		}
		if len(it.Paths) > 0 && len(paths) == 0 {
			continue
		}
		if len(it.Paths) > 0 {
			it.Paths = paths
		}
		kept = append(kept, it)
	}
	items = kept
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range items {
		wg.Add(1)
		go func(it *model.CleanItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, p := range it.Paths {
				u := sz.Measure(ctx, p)
				it.Size += u.Bytes
				it.SizeMin = it.SizeMin || u.Partial
			}
		}(&items[i])
	}
	wg.Wait()
	out := items[:0]
	for _, it := range items {
		if it.Size > 0 {
			out = append(out, it)
		}
	}
	return out
}

// mergeSmall folds items under 10 MB into one row with the given ID and label. Items tied to an
// app stay on their own, so they can still be marked busy. The merged row takes its group, root,
// method and safety from the first small item.
func mergeSmall(items []model.CleanItem, id, label string) []model.CleanItem {
	var keep, small []model.CleanItem
	for _, it := range items {
		if it.Size < smallItem && it.App == "" {
			small = append(small, it)
		} else {
			keep = append(keep, it)
		}
	}
	if len(small) < 2 {
		return items
	}
	m := small[0]
	m.ID, m.Name, m.Detail = id, fmt.Sprintf("%d %s", len(small), label), "each under 10 MB"
	m.Paths, m.Size, m.SizeMin = nil, 0, false
	for _, it := range small {
		m.Paths = append(m.Paths, it.Paths...)
		m.Size += it.Size
		m.SizeMin = m.SizeMin || it.SizeMin
	}
	return append(keep, m)
}

// dirs lists the folders directly inside dir, leaving out files and symlinks.
func dirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// exists reports whether path exists, without following a final symlink.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
