//go:build darwin

// Package scan finds things the Junk and Dev screens can clean. Finders read the home folder
// they're given, never the real one directly, so tests can use a fake home.
package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// sfDataless marks files whose contents iCloud hasn't downloaded. Reading one would start a
// download, so the walker skips them without opening them.
const sfDataless = 0x40000000

// Usage is how much disk space a folder takes.
type Usage struct {
	Bytes   uint64 // allocated bytes, hard links counted once
	Partial bool   // some entries couldn't be read, so Bytes is a minimum
}

// Sizer measures disk usage. Within one Sizer a hard-linked file is counted once, however many
// folders it shows up in. Use a new Sizer for each scan.
type Sizer struct {
	mu   sync.Mutex
	seen map[[2]uint64]bool
}

// NewSizer returns an empty Sizer.
func NewSizer() *Sizer { return &Sizer{seen: map[[2]uint64]bool{}} }

// Measure walks path without following symlinks. A missing path measures as zero; a symlink
// measures as zero too.
func (s *Sizer) Measure(ctx context.Context, path string) Usage {
	var u Usage
	info, err := os.Lstat(path)
	if err != nil {
		u.Partial = !errors.Is(err, fs.ErrNotExist)
		return u
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return u
	}
	n := 0
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			u.Partial = true
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if n++; n%512 == 0 && ctx.Err() != nil {
			u.Partial = true
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			u.Partial = true
			return nil
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			u.Bytes += uint64(fi.Size())
			return nil
		}
		if st.Flags&sfDataless != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() && st.Nlink > 1 {
			key := [2]uint64{uint64(st.Dev), st.Ino}
			s.mu.Lock()
			dup := s.seen[key]
			s.seen[key] = true
			s.mu.Unlock()
			if dup {
				return nil
			}
		}
		u.Bytes += uint64(st.Blocks) * 512
		return nil
	})
	if ctx.Err() != nil {
		u.Partial = true
	}
	return u
}
