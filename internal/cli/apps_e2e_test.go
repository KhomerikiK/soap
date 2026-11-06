package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEndApps runs the real Apps finder and cleaner against a fake home folder with its own
// Applications folder. Tools are off, so Spotlight isn't asked and /Applications isn't read; the
// only command that runs on the app is plutil, on the temp Info.plist. The Trash is a folder in the
// fake home.
func TestEndToEndApps(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real cleaner on a fake home folder")
	}
	home := t.TempDir()
	write := func(rel, data string) string {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("Applications/Soaptest.app/Contents/Info.plist", `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.soaptest</string></dict></plist>
`)
	write("Applications/Soaptest.app/Contents/MacOS/Soaptest", strings.Repeat("x", 50_000))
	caches := filepath.Dir(write("Library/Caches/com.example.soaptest/blob", strings.Repeat("x", 20_000)))
	prefs := write("Library/Preferences/com.example.soaptest.plist", "<plist/>")
	data := filepath.Dir(write("Library/Application Support/Soaptest/data", strings.Repeat("x", 30_000)))
	app := filepath.Join(home, "Applications/Soaptest.app")
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
	if code := run("apps"); code != 0 {
		t.Fatalf("apps: exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"Soaptest · last used about ", "apps/com.example.soaptest\n", "apps/com.example.soaptest/Library/Caches/com.example.soaptest",
		"by name", "apps/com.example.soaptest/Library/Application Support/Soaptest", "Spotlight is off, so soap can't tell when apps were last opened."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("apps is missing %q:\n%s", want, out.String())
		}
	}
	if code := run("clean", "apps/com.example.soaptest", "-y"); code != 0 {
		t.Fatalf("clean: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	for _, p := range []string{app, caches, prefs} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s should be in the Trash", p)
		}
	}
	if _, err := os.Stat(data); err != nil {
		t.Error("the folder matched by name isn't ticked with the app, so it stays")
	}
	entries, err := d.History()
	if err != nil || len(entries) != 3 {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	var appEntry string
	for _, e := range entries {
		if e.ItemID == "apps/com.example.soaptest" && e.CanRestore() {
			appEntry = e.ID
		}
	}
	if code := run("restore", appEntry); code != 0 {
		t.Fatalf("restore: exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(app, "Contents/Info.plist")); err != nil {
		t.Errorf("the app should be back: %v", err)
	}
}
