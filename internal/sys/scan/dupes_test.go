//go:build darwin

package scan

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

// putData writes data to home/rel, creating folders, and returns the full path.
func putData(t *testing.T, home, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func random(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// dupes runs the Dupes finder on home and returns its sets, its notes and how many updates
// brought items.
func dupes(t *testing.T, home string) (sets []model.DupeSet, notes []model.Note, adds int) {
	t.Helper()
	var items []model.CleanItem
	r := findDupes(context.Background(), testEnv(home, nil), func(got []model.CleanItem, status string) {
		if len(got) > 0 {
			adds++
			items = append(items, got...)
		}
	})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	checkPaths(t, home, items, nil)
	return model.DupeSets(items, false), r.Notes, adds
}

func TestDupesFindsIdenticalFiles(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	putData(t, home, "Downloads/trip.mov", data)
	putData(t, home, "Movies/Trips/trip.mov", data)
	putData(t, home, "Desktop/old/trip copy.mov", data)
	other := random(2_000_000)
	putData(t, home, "Documents/a.zip", other)
	putData(t, home, "Library/Mobile Documents/com~apple~CloudDocs/a.zip", other)
	sets, notes, adds := dupes(t, home)
	if len(sets) != 2 || adds != 2 || len(notes) != 0 {
		t.Fatalf("sets = %+v, adds = %d, notes = %+v", sets, adds, notes)
	}
	var trip, zip model.DupeSet
	for _, s := range sets {
		if s.Name == "trip.mov" {
			trip = s
		} else {
			zip = s
		}
	}
	if len(trip.Items) != 3 || trip.Items[0].Name != "~/Movies/Trips/trip.mov" || !trip.Items[0].Keep {
		t.Fatalf("trip = %+v; the copy outside Downloads and Desktop is kept", trip)
	}
	it := trip.Items[1]
	if it.ID != "dupes/Desktop/old/trip copy.mov" || it.Method != model.MethodTrash || it.Root != home || it.Screen != model.ScreenDupes ||
		it.Group != DupesGroup || it.Stamp == nil || it.Stamp.Size != 1_500_000 || len(it.Copies) != 3 || it.Safe ||
		it.Unshared < 1_500_000 || !strings.HasPrefix(it.Detail, "made ") || strings.Contains(it.Detail, "clone") {
		t.Errorf("copy = %+v", it)
	}
	if trip.Frees < 3_000_000 {
		t.Errorf("trip frees %d; removing two plain copies frees both", trip.Frees)
	}
	if len(zip.Items) != 2 || !strings.Contains(zip.Items[1].Detail, "in iCloud Drive") {
		t.Errorf("zip = %+v", zip)
	}
}

func TestDupesNeedsTheSameContent(t *testing.T) {
	home := t.TempDir()
	putData(t, home, "Downloads/a.bin", random(1_500_000))
	putData(t, home, "Documents/b.bin", random(1_500_000)) // same size, different bytes
	middle := random(1_500_000)
	putData(t, home, "Downloads/c.bin", middle)
	changed := append([]byte{}, middle...)
	changed[750_000] ^= 0xff // the same first and last 64 KB, different in the middle
	putData(t, home, "Documents/d.bin", changed)
	if sets, _, _ := dupes(t, home); len(sets) != 0 {
		t.Errorf("sets = %+v; none of these files are identical", sets)
	}
}

func TestDupesCountsAHardLinkAsOneFile(t *testing.T) {
	home := t.TempDir()
	f := putData(t, home, "Documents/a.bin", random(1_500_000))
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(f, filepath.Join(home, "Downloads/a.bin")); err != nil {
		t.Fatal(err)
	}
	if sets, _, _ := dupes(t, home); len(sets) != 0 {
		t.Errorf("sets = %+v; two hard links are one file", sets)
	}
}

// TestDupesShowsThatACloneFreesNothing runs on the Mac's APFS disk.
func TestDupesShowsThatACloneFreesNothing(t *testing.T) {
	home := t.TempDir()
	a := putData(t, home, "Documents/a.bin", random(4_000_000))
	b := filepath.Join(home, "Downloads/a.bin")
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-c", a, b).CombinedOutput(); err != nil {
		t.Skipf("cp -c can't clone here: %v %s", err, out)
	}
	sets, _, _ := dupes(t, home)
	if len(sets) != 1 || len(sets[0].Items) != 2 {
		t.Fatalf("sets = %+v", sets)
	}
	clone := sets[0].Items[1]
	if clone.Name != "~/Downloads/a.bin" || clone.Unshared > 1<<20 || !strings.Contains(clone.Detail, "· APFS clone") ||
		!strings.Contains(clone.Detail, "frees ") || sets[0].Frees > 1<<20 {
		t.Errorf("clone = %+v, set frees %d; removing a clone frees almost nothing", clone, sets[0].Frees)
	}
}

// TestDupesNamesAHardLinkNotAClone: a copy that frees nothing because another name links to the
// same data (outside the searched folders) is a hard link, not an APFS clone.
func TestDupesAPlainHardLinkFreesNothing(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	a := putData(t, home, "Documents/a.bin", data)
	putData(t, home, "Downloads/a.bin", data)          // a real second copy
	other := filepath.Join(home, "Library/a-link.bin") // outside the searched folders
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, other); err != nil {
		t.Fatal(err)
	}
	sets, _, _ := dupes(t, home)
	if len(sets) != 1 || len(sets[0].Items) != 2 {
		t.Fatalf("sets = %+v", sets)
	}
	for _, it := range sets[0].Items {
		if it.Name != "~/Documents/a.bin" {
			continue
		}
		if it.Unshared != 0 || !strings.Contains(it.Detail, "frees 0 B · hard link") {
			t.Errorf("item = %+v, want a hard link that frees 0 B", it)
		}
	}
}

func TestDupesNamesAHardLinkNotAClone(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	a := putData(t, home, "Documents/a.bin", data)
	b := filepath.Join(home, "Downloads/a.bin")
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cp", "-c", a, b).CombinedOutput(); err != nil {
		t.Skipf("cp -c can't clone here: %v %s", err, out)
	}
	other := filepath.Join(home, "Library/a-link.bin") // outside the searched folders
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, other); err != nil {
		t.Fatal(err)
	}
	sets, _, _ := dupes(t, home)
	if len(sets) != 1 || len(sets[0].Items) != 2 {
		t.Fatalf("sets = %+v", sets)
	}
	for _, it := range sets[0].Items {
		if it.Name != "~/Documents/a.bin" {
			continue
		}
		if !strings.Contains(it.Detail, "· hard link") || strings.Contains(it.Detail, "APFS clone") || !strings.Contains(it.Detail, "frees 0 B") {
			t.Errorf("detail = %q, want a hard link that frees 0 B", it.Detail)
		}
	}
}

// TestDupesHashMustCoverTheWholeFile: a file that is shorter than the walk saw is left out.
func TestDupesHashMustCoverTheWholeFile(t *testing.T) {
	dir := t.TempDir()
	p := putData(t, dir, "a.bin", random(1000))
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	f := &dupeFile{path: p, size: info.Size() + 500, stamp: StampOf(info)} // the walk "saw" 500 bytes more
	d := &dupeScan{}
	if _, err := d.hash(context.Background(), f, false); !errors.Is(err, errChanged) {
		t.Errorf("err = %v, want errChanged for a file shorter than the walk saw", err)
	}
	f.size = info.Size()
	if _, err := d.hash(context.Background(), f, false); err != nil {
		t.Errorf("err = %v for a file of the size the walk saw", err)
	}
}

func TestDupesLooksOnlyWhereItShould(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	putData(t, home, "Documents/keep.bin", data)
	putData(t, home, "Documents/.hidden.bin", data)
	putData(t, home, "Documents/.secret/x.bin", data)
	putData(t, home, "Documents/Tool.app/Contents/x.bin", data)
	putData(t, home, "Documents/site/package.json", []byte("{}"))
	putData(t, home, "Documents/site/node_modules/x.bin", data)
	putData(t, home, "Library/Caches/x.bin", data)
	putData(t, home, "Projects/x.bin", data)
	if err := os.Symlink(filepath.Join(home, "Documents/keep.bin"), filepath.Join(home, "Documents/link.bin")); err != nil {
		t.Fatal(err)
	}
	small := random(500_000)
	putData(t, home, "Documents/small1", small)
	putData(t, home, "Downloads/small2", small)
	if sets, _, _ := dupes(t, home); len(sets) != 0 {
		t.Errorf("sets = %+v; every other copy is hidden, in a package, in a build folder, outside the user's folders, a symlink or too small", sets)
	}
}

func TestDupesSaysWhatItCouldntRead(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	putData(t, home, "Documents/a.bin", data)
	locked := putData(t, home, "Downloads/b.bin", data)
	putData(t, home, "Pictures/private/c.bin", data)
	dir := filepath.Join(home, "Pictures/private")
	for _, p := range []string{locked, dir} {
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0o755)
		os.Chmod(locked, 0o644)
	})
	sets, notes, _ := dupes(t, home)
	if len(sets) != 0 {
		t.Errorf("sets = %+v", sets)
	}
	var texts []string
	for _, n := range notes {
		texts = append(texts, n.Text)
	}
	got := strings.Join(texts, " | ")
	if !strings.Contains(got, "1 folder couldn't be read, so it was skipped.") || !strings.Contains(got, "1 file couldn't be read.") {
		t.Errorf("notes = %s", got)
	}
}

func TestDupesUsesTheConfiguredMinimum(t *testing.T) {
	home := t.TempDir()
	data := random(1_500_000)
	putData(t, home, "Documents/a.bin", data)
	putData(t, home, "Downloads/a.bin", data)
	env := testEnv(home, nil)
	env.Config.DupesMinMB = 2
	found := 0
	findDupes(context.Background(), env, func(items []model.CleanItem, _ string) { found += len(items) })
	if found != 0 {
		t.Errorf("found %d copies under the 2 MB minimum", found)
	}
}

func TestDupesFinders(t *testing.T) {
	fs := Finders(model.ScreenDupes)
	if len(fs) != 1 || fs[0].Group != DupesGroup || fs[0].Stream == nil || fs[0].Screen != model.ScreenDupes {
		t.Errorf("finders = %+v", fs)
	}
	if Finders(model.ScreenDisk) != nil {
		t.Error("Disk has no finders")
	}
}
