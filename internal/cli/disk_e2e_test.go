package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndDiskAndDupes runs the real walker, Dupes finder and cleaner against a fake home
// folder. The Trash is a folder in it, so the real Trash is never touched.
func TestEndToEndDiskAndDupes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real cleaner on a fake home folder")
	}
	home := t.TempDir()
	write := func(rel string, data []byte) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	movie := make([]byte, 1_500_000)
	rand.Read(movie)
	kept := write("Movies/trip.mov", movie)
	copied := write("Downloads/trip.mov", movie)
	report := write("Documents/report.pdf", make([]byte, 500_000))
	old := trashMove
	trashMove = func(p string) (string, error) {
		dest := filepath.Join(home, ".Trash", filepath.Base(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		return dest, os.Rename(p, dest)
	}
	t.Cleanup(func() { trashMove = old })

	d, err := HomeDeps(home, false)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	d.Stdin, d.Stdout, d.Stderr = strings.NewReader(""), &out, &errOut
	run := func(args ...string) int {
		out.Reset()
		errOut.Reset()
		return Run(d, args)
	}

	if code := run("disk", "--json"); code != 0 {
		t.Fatalf("disk: exit %d: %s", code, errOut.String())
	}
	var disk struct {
		Items []struct {
			ID   string `json:"id"`
			Size uint64 `json:"size"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &disk); err != nil {
		t.Fatal(err)
	}
	sizes := map[string]uint64{}
	for _, it := range disk.Items {
		sizes[it.ID] = it.Size
	}
	if sizes["disk/Movies"] < 1_500_000 || sizes["disk/Documents"] < 500_000 {
		t.Errorf("disk items = %+v", disk.Items)
	}

	if code := run("dupes"); code != 0 || !strings.Contains(out.String(), "trip.mov · 2 copies") || !strings.Contains(out.String(), "dupes/Downloads/trip.mov") {
		t.Fatalf("dupes: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	if code := run("clean", "dupes/Downloads/trip.mov", "dupes/Movies/trip.mov", "-y"); code != 2 || !strings.Contains(errOut.String(), "Keep at least one copy of trip.mov.") {
		t.Errorf("both copies: exit %d, %s", code, errOut.String())
	}
	if code := run("clean", "dupes/Downloads/trip.mov", "-y"); code != 0 {
		t.Fatalf("one copy: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(copied); err == nil {
		t.Error("the copy in Downloads should be in the Trash")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("the kept copy must stay")
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash/trip.mov")); err != nil {
		t.Errorf("the copy isn't in the Trash: %v", err)
	}

	if code := run("clean", "disk/Documents", "-y"); code != 2 || !strings.Contains(errOut.String(), "soap never removes ~/Documents") {
		t.Errorf("Documents: exit %d, %s", code, errOut.String())
	}
	if code := run("clean", "disk/../x", "-y"); code != 2 {
		t.Errorf("disk/../x: exit %d, %s", code, errOut.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Error("nothing should move for disk/../x")
	}
	if code := run("clean", "disk/Documents/report.pdf", "-y"); code != 0 {
		t.Fatalf("report: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(report); err == nil {
		t.Error("the report should be in the Trash")
	}
	entries, err := d.History()
	if err != nil || len(entries) != 2 || entries[0].ItemID != "disk/Documents/report.pdf" || !entries[0].CanRestore() {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	if code := run("restore", entries[0].ID); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(report); err != nil {
		t.Error("the report should be back")
	}
}
