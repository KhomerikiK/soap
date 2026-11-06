//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestFindCaches(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Library/Caches/Big/data", 11<<20)
	put(t, home, "Library/Caches/Foo.ShipIt/update", 11<<20)
	put(t, home, "Library/Caches/com.apple.dt.Xcode/x", 11<<20)
	put(t, home, "Library/Caches/tiny1/x", 100)
	put(t, home, "Library/Caches/tiny2/x", 100)
	put(t, home, "Library/Caches/Google/x", 100)
	put(t, home, "Library/Caches/JetBrains/PhpStorm2025.2/x", 100)
	put(t, home, "Library/Caches/JetBrains/IntelliJIdea2025.2/x", 100)
	secret := put(t, home, "Documents/secret", 11<<20)
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(home, "Library/Caches/link")); err != nil {
		t.Fatal(err)
	}
	r := findCaches(context.Background(), testEnv(home, nil))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	items := byID(r.Items)
	root := filepath.Join(home, "Library/Caches")
	big, ok := items["junk/caches/Big"]
	if !ok || !big.Safe || big.Size < 11<<20 || big.Root != root || big.Detail != "rebuilt automatically" || big.Group != "App caches" {
		t.Errorf("Big = %+v", big)
	}
	if items["junk/caches/Foo.ShipIt"].Detail != "updater leftovers" {
		t.Errorf("ShipIt = %+v", items["junk/caches/Foo.ShipIt"])
	}
	if _, ok := items["junk/caches/com.apple.dt.Xcode"]; ok {
		t.Error("Xcode's cache belongs on Dev")
	}
	if _, ok := items["junk/caches/link"]; ok {
		t.Error("a symlinked folder must not be offered")
	}
	small := items["junk/caches/small"]
	if small.Name != "2 small caches" || len(small.Paths) != 2 || small.Detail != "each under 10 MB" {
		t.Errorf("small = %+v", small)
	}
	if g := items["junk/caches/Google"]; g.App != "Google Chrome" {
		t.Errorf("Google = %+v; it stays its own row so it can be busy", g)
	}
	if jb := items["junk/caches/JetBrains/PhpStorm2025.2"]; jb.App != "PhpStorm" || jb.Name != "JetBrains PhpStorm2025.2" {
		t.Errorf("JetBrains = %+v", jb)
	}
	if jb := items["junk/caches/JetBrains/IntelliJIdea2025.2"]; jb.App != "idea" {
		t.Errorf("IntelliJ IDEA busy name = %q, want idea", jb.App)
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindBrowserCaches(t *testing.T) {
	home := t.TempDir()
	chrome := "Library/Application Support/Google/Chrome"
	put(t, home, chrome+"/Default/GPUCache/data_0", 5000)
	put(t, home, chrome+"/Default/Service Worker/CacheStorage/abc/x", 5000)
	put(t, home, chrome+"/Profile 1/Code Cache/js/x", 5000)
	put(t, home, chrome+"/Default/History", 5000)
	put(t, home, chrome+"/System Profile/GPUCache/x", 5000)
	r := findBrowserCaches(context.Background(), testEnv(home, nil))
	if len(r.Items) != 1 {
		t.Fatalf("got %v", ids(r.Items))
	}
	it := r.Items[0]
	if it.Name != "Google Chrome profile caches" || it.App != "Google Chrome" || !it.Safe || len(it.Paths) != 3 {
		t.Errorf("item = %+v", it)
	}
	for _, p := range it.Paths {
		if strings.HasSuffix(p, "History") || strings.Contains(p, "System Profile") {
			t.Errorf("%s must never be cleaned", p)
		}
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindLogs(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Library/Logs/DiagnosticReports/crash.ips", 11<<20)
	put(t, home, "Library/Logs/a.log", 100)
	put(t, home, "Library/Logs/b.log", 100)
	r := findLogs(context.Background(), testEnv(home, nil))
	items := byID(r.Items)
	if d := items["junk/logs/DiagnosticReports"]; d.Detail != "crash reports" || !d.Safe {
		t.Errorf("DiagnosticReports = %+v", d)
	}
	if s := items["junk/logs/small"]; s.Name != "2 small logs" {
		t.Errorf("small logs = %+v (all: %v)", s, ids(r.Items))
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindTrash(t *testing.T) {
	home := t.TempDir()
	put(t, home, ".Trash/old.zip", 5000)
	put(t, home, ".Trash/folder/x", 5000)
	if err := os.Symlink(filepath.Join(home, "Documents"), filepath.Join(home, ".Trash/link")); err != nil {
		t.Fatal(err)
	}
	r := findTrash(context.Background(), testEnv(home, nil))
	if len(r.Items) != 1 || r.Items[0].Detail != "2 items" || r.Items[0].Safe || len(r.Items[0].Paths) != 2 || r.Items[0].Method != model.MethodDelete {
		t.Fatalf("items = %+v", r.Items)
	}
	checkPaths(t, home, r.Items, nil)

	trash := filepath.Join(home, ".Trash")
	if err := os.Chmod(trash, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(trash, 0o755) })
	r = findTrash(context.Background(), testEnv(home, nil))
	if len(r.Items) != 0 || len(r.Notes) != 1 || !strings.Contains(r.Notes[0].Text, "Full Disk Access") || r.Notes[0].Group != "" ||
		r.Notes[0].Code != model.NoteNoFullDiskAccess {
		t.Errorf("without access: %+v", r)
	}
}

func TestFindBackups(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library/Application Support/MobileSync/Backup/00008110-ABC")
	put(t, home, "Library/Application Support/MobileSync/Backup/00008110-ABC/Info.plist", 100)
	put(t, home, "Library/Application Support/MobileSync/Backup/00008110-ABC/00/data", 50_000)
	plist := filepath.Join(dir, "Info.plist")
	run := &fakeRun{out: map[string]string{
		"plutil -extract Device Name raw -o - " + plist:      "Kote's iPhone\n",
		"plutil -extract Last Backup Date raw -o - " + plist: "2026-03-02T10:00:00Z\n",
	}}
	r := findBackups(context.Background(), testEnv(home, run))
	if len(r.Items) != 1 {
		t.Fatalf("items = %v", ids(r.Items))
	}
	it := r.Items[0]
	if it.Name != "Kote's iPhone backup" || it.Method != model.MethodTrash || it.Safe || !strings.HasPrefix(it.Detail, "from ") || !strings.HasSuffix(it.Detail, "goes to the Trash") {
		t.Errorf("item = %+v", it)
	}
	checkPaths(t, home, r.Items, nil)
}

func TestJunkFinders(t *testing.T) {
	var names []string
	for _, f := range JunkFinders() {
		if f.Screen != model.ScreenJunk {
			t.Errorf("%s isn't on Junk", f.Group)
		}
		names = append(names, f.Group)
	}
	if got := strings.Join(names, ","); got != "App caches,Browser caches,Logs and crash reports,Trash,iOS device backups" {
		t.Errorf("finders = %s", got)
	}
}
