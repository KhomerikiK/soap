package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndCleanSafe runs the real finders and cleaner against a fake home folder, with every
// tool turned off, so nothing outside the temp folder can be touched.
func TestEndToEndCleanSafe(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real cleaner on a fake home folder")
	}
	home := t.TempDir()
	write := func(rel string, size int) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cache := write("Library/Caches/com.example.app/blob", 11<<20)
	write("Projects/old/package.json", 10)
	nm := write("Projects/old/node_modules/x/index.js", 5000)

	d, err := HomeDeps(home, false)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d.Stdin, d.Stdout, d.Stderr = strings.NewReader(""), &out, &out

	if code := Run(d, []string{"clean", "--safe", "--dry-run"}); code != 0 {
		t.Fatalf("dry run: exit %d\n%s", code, out.String())
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("a dry run deleted something")
	}
	if code := Run(d, []string{"clean", "--safe", "-y"}); code != 0 {
		t.Fatalf("clean: exit %d\n%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Dir(cache)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the safe cache is still there")
	}
	if _, err := os.Stat(nm); err != nil {
		t.Error("node_modules isn't pre-ticked and must stay")
	}
	b, err := os.ReadFile(filepath.Join(home, "Library/Application Support/soap/history.jsonl"))
	if err != nil || !strings.Contains(string(b), "junk/caches/com.example.app") {
		t.Errorf("history = %s, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library/Application Support/soap/config.json")); err != nil {
		t.Errorf("the default config should be written: %v", err)
	}
}
