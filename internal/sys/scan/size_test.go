//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasureCountsAllocatedBytes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), 100_000)
	writeFile(t, filepath.Join(dir, "sub", "b"), 50_000)
	u := NewSizer().Measure(context.Background(), dir)
	if u.Partial || u.Bytes < 150_000 || u.Bytes > 200_000 {
		t.Errorf("Measure = %+v, want about 150 KB of allocated blocks", u)
	}
}

func TestMeasureCountsHardLinksOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one", "f"), 1_000_000)
	if err := os.MkdirAll(filepath.Join(dir, "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "one", "f"), filepath.Join(dir, "two", "f")); err != nil {
		t.Fatal(err)
	}
	s := NewSizer()
	first := s.Measure(context.Background(), filepath.Join(dir, "one"))
	second := s.Measure(context.Background(), filepath.Join(dir, "two"))
	if first.Bytes < 1_000_000 || second.Bytes >= 100_000 {
		t.Errorf("first = %d, second = %d; a hard-linked file counts once per Sizer", first.Bytes, second.Bytes)
	}
}

func TestMeasureDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "big")
	writeFile(t, outside, 2_000_000)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if u := NewSizer().Measure(context.Background(), dir); u.Bytes >= 2_000_000 {
		t.Errorf("Measure = %d; symlinked files must not be counted", u.Bytes)
	}
	if u := NewSizer().Measure(context.Background(), filepath.Join(dir, "dirlink")); u.Bytes != 0 {
		t.Errorf("measuring a symlink itself = %d, want 0", u.Bytes)
	}
}

func TestMeasureMissingAndUnreadable(t *testing.T) {
	if u := NewSizer().Measure(context.Background(), filepath.Join(t.TempDir(), "nope")); u.Bytes != 0 || u.Partial {
		t.Errorf("missing path = %+v, want zero and not partial", u)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok"), 10_000)
	locked := filepath.Join(dir, "locked")
	writeFile(t, filepath.Join(locked, "x"), 10_000)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if u := NewSizer().Measure(context.Background(), dir); !u.Partial || u.Bytes == 0 {
		t.Errorf("Measure = %+v, want a partial, non-zero size", u)
	}
}

func TestMeasureStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), 10_000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if u := NewSizer().Measure(ctx, dir); !u.Partial {
		t.Errorf("a cancelled measure should be partial: %+v", u)
	}
}

func TestMeasureDropsPathsThatDontExist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real", "a"), 100_000)
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	items := []model.CleanItem{
		{ID: "some", Paths: []string{filepath.Join(dir, "real"), filepath.Join(dir, "never"), filepath.Join(dir, "link")}},
		{ID: "none", Paths: []string{filepath.Join(dir, "never")}},
	}
	got := measure(context.Background(), NewSizer(), items)
	if len(got) != 1 || got[0].ID != "some" || len(got[0].Paths) != 1 || got[0].Paths[0] != filepath.Join(dir, "real") {
		t.Errorf("measure = %+v; want only the real path (no missing or symlinked one), and the all-missing item dropped", got)
	}
}
