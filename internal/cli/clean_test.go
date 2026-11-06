package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/model"
)

// cleanupFake stands in for scanning, cleaning and history in CLI tests.
type cleanupFake struct {
	items      []model.CleanItem
	notes      []model.Note
	cleaned    [][]model.CleanItem
	outcome    func(model.CleanItem) model.CleanResult
	history    []model.HistoryEntry
	restoreErr error
}

func (f *cleanupFake) install(d *Deps) {
	d.Scan = func(_ context.Context, s model.Screen) <-chan model.ScanUpdate {
		var mine []model.CleanItem
		for _, it := range f.items {
			if it.Screen == s {
				mine = append(mine, it)
			}
		}
		ch := make(chan model.ScanUpdate, 1)
		ch <- model.ScanUpdate{Screen: s, Finder: "all", Items: mine, Notes: f.notes, Done: 1, Total: 1}
		close(ch)
		return ch
	}
	d.Clean = func(_ context.Context, items []model.CleanItem, report func(model.CleanResult)) error {
		f.cleaned = append(f.cleaned, items)
		for _, it := range items {
			r := model.CleanResult{Item: it, Result: "done", Freed: it.Size}
			if f.outcome != nil {
				r = f.outcome(it)
			}
			report(r)
		}
		return nil
	}
	d.History = func() ([]model.HistoryEntry, error) { return f.history, nil }
	d.Restore = func(id string) (model.HistoryEntry, error) {
		if f.restoreErr != nil {
			return model.HistoryEntry{}, f.restoreErr
		}
		return model.HistoryEntry{ID: id, Name: "Kote's iPhone backup", Restored: true}, nil
	}
}

func cliItems() []model.CleanItem {
	j, dv := model.ScreenJunk, model.ScreenDev
	return []model.CleanItem{
		{ID: "junk/caches/JetBrains/PhpStorm2025.2", Screen: j, Group: "App caches", Name: "JetBrains PhpStorm2025.2", Size: 3 << 30, Safe: true, App: "PhpStorm", Method: model.MethodDelete},
		{ID: "junk/caches/Google", Screen: j, Group: "App caches", Name: "Google", Size: 2 << 30, Safe: true, App: "Google Chrome", Busy: true, Method: model.MethodDelete},
		{ID: "junk/backups/abc", Screen: j, Group: "iOS device backups", Name: "Kote's iPhone backup", Size: 5 << 30, Method: model.MethodTrash},
		{ID: "dev/docker/build-cache", Screen: dv, Group: "Docker", Name: "Build cache", Size: 4 << 30, Safe: true, Method: model.MethodCommand, Command: []string{"docker", "builder", "prune", "-a", "-f"}},
		{ID: "dev/projects/old-app/node_modules", Screen: dv, Group: "Projects", Name: "old-app · node_modules", Size: 1 << 30, Detail: "untouched 74 days · npm install", Method: model.MethodDelete},
	}
}

func newCleanupEnv(t *testing.T, stdin string) (*env, *cleanupFake) {
	t.Helper()
	e := newEnv(t, stdin)
	f := &cleanupFake{items: cliItems()}
	f.install(e.d)
	return e, f
}

func cleanedIDs(f *cleanupFake) []string {
	var out []string
	for _, batch := range f.cleaned {
		for _, it := range batch {
			out = append(out, it.ID)
		}
	}
	return out
}

func TestJunkList(t *testing.T) {
	e, _ := newCleanupEnv(t, "")
	if code := e.run("junk"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	for _, want := range []string{"TICK", "SIZE", "JetBrains PhpStorm2025.2", "✓", "busy", "Quit Google Chrome first", "junk/backups/abc"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Build cache") {
		t.Error("junk lists only Junk items")
	}
}

func TestDevJSON(t *testing.T) {
	e, _ := newCleanupEnv(t, "")
	if code := e.run("dev", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Items []struct {
			ID     string `json:"id"`
			Ticked bool   `json:"ticked"`
		} `json:"items"`
		Notes  []model.Note `json:"notes"`
		Errors []any        `json:"errors"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	ticked := map[string]bool{}
	for _, it := range got.Items {
		ticked[it.ID] = it.Ticked
	}
	if len(got.Items) != 2 || !ticked["dev/docker/build-cache"] || ticked["dev/projects/old-app/node_modules"] || got.Errors == nil {
		t.Errorf("got %+v", got)
	}
}

func TestCleanSafeDryRun(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	if code := e.run("clean", "--safe", "--dry-run"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	if !strings.Contains(out, "Delete 1 for good and run 1 command? Frees 7.0 GB.") || !strings.Contains(out, "Dry run: nothing was cleaned.") {
		t.Errorf("output:\n%s", out)
	}
	if len(f.cleaned) != 0 {
		t.Error("a dry run cleans nothing")
	}
}

func TestCleanSafeWithYes(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	if code := e.run("clean", "--safe", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if got := cleanedIDs(f); !slices.Equal(got, []string{"junk/caches/JetBrains/PhpStorm2025.2", "dev/docker/build-cache"}) {
		t.Errorf("cleaned %v; want only the pre-ticked items, busy ones left out", got)
	}
	if out := e.out.String(); !strings.Contains(out, "✓ JetBrains PhpStorm2025.2") || !strings.Contains(out, "Freed 7.0 GB.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCleanAsksFirst(t *testing.T) {
	e, f := newCleanupEnv(t, "n\n")
	if code := e.run("clean", "--safe"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(f.cleaned) != 0 || !strings.Contains(e.out.String(), "[y/N]") || !strings.Contains(e.errOut.String(), "Cancelled.") {
		t.Errorf("stdout %q, stderr %q", e.out.String(), e.errOut.String())
	}
}

func TestCleanByID(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	if code := e.run("clean", "junk/backups/abc", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if got := cleanedIDs(f); !slices.Equal(got, []string{"junk/backups/abc"}) {
		t.Errorf("cleaned %v", got)
	}
	e2, f2 := newCleanupEnv(t, "")
	if code := e2.run("clean", "nope", "-y"); code != 2 || !strings.Contains(e2.errOut.String(), "No item with ID nope.") || len(f2.cleaned) != 0 {
		t.Errorf("unknown ID: exit %d, %q", code, e2.errOut.String())
	}
}

func TestCleanNeedsSafeOrIDs(t *testing.T) {
	for _, args := range [][]string{{"clean"}, {"clean", "--safe", "junk/backups/abc"}} {
		e, _ := newCleanupEnv(t, "")
		if code := e.run(args...); code != 1 || !strings.Contains(e.errOut.String(), "Pass --safe") {
			t.Errorf("%v: exit %d, %q", args, code, e.errOut.String())
		}
	}
}

func TestCleanNothingSafe(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.items = []model.CleanItem{{ID: "junk/backups/abc", Screen: model.ScreenJunk, Group: "iOS device backups", Name: "backup", Method: model.MethodTrash}}
	if code := e.run("clean", "--safe", "-y"); code != 2 || !strings.Contains(e.errOut.String(), "Nothing safe to clean.") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
}

func TestCleanFailuresExitOne(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.outcome = func(it model.CleanItem) model.CleanResult {
		if it.Method == model.MethodCommand {
			return model.CleanResult{Item: it, Result: "failed", Message: "Docker isn't running"}
		}
		return model.CleanResult{Item: it, Result: "done", Freed: it.Size}
	}
	if code := e.run("clean", "--safe", "-y"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.out.String(), "✗ Build cache: Docker isn't running") || !strings.Contains(e.out.String(), "Freed 3.0 GB.") {
		t.Errorf("output:\n%s", e.out.String())
	}
}

func TestHistoryAndRestore(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.history = []model.HistoryEntry{{ID: "t1", Time: t0, Name: "Kote's iPhone backup", Method: model.MethodTrash, Trashed: []string{"/x"}, Result: "done", Size: 5 << 30}}
	if code := e.run("history"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out := e.out.String(); !strings.Contains(out, "Kote's iPhone backup") || !strings.Contains(out, "in the Trash") || !strings.Contains(out, "t1") {
		t.Errorf("history:\n%s", out)
	}
	e.out.Reset()
	if code := e.run("restore", "t1"); code != 0 || !strings.Contains(e.out.String(), "Put back Kote's iPhone backup.") {
		t.Errorf("restore: exit %d, %q", code, e.out.String())
	}
	f.restoreErr = actions.ErrNoEntry
	if code := e.run("restore", "zzz"); code != 2 || !strings.Contains(e.errOut.String(), "No history entry with ID zzz.") {
		t.Errorf("unknown entry: exit %d, %q", code, e.errOut.String())
	}
}

func TestCleanRefusesBusyID(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	if code := e.run("clean", "junk/caches/Google", "-y"); code != 2 || len(f.cleaned) != 0 ||
		!strings.Contains(e.errOut.String(), "junk/caches/Google can't be cleaned: Quit Google Chrome first.") {
		t.Errorf("exit %d, cleaned %v, %q", code, f.cleaned, e.errOut.String())
	}
}

func TestCleanDropsDuplicateIDs(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	if code := e.run("clean", "junk/backups/abc", "junk/backups/abc", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if got := cleanedIDs(f); !slices.Equal(got, []string{"junk/backups/abc"}) || !strings.Contains(e.out.String(), "Freed 5.0 GB.") {
		t.Errorf("cleaned %v\n%s", got, e.out.String())
	}
}

func TestCleanPrintsFinderErrors(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.items = f.items[:1]
	inner := e.d.Scan
	e.d.Scan = func(ctx context.Context, s model.Screen) <-chan model.ScanUpdate {
		if s == model.ScreenDev {
			ch := make(chan model.ScanUpdate, 1)
			ch <- model.ScanUpdate{Screen: s, Finder: "Docker", Err: errors.New("Docker isn't running")}
			close(ch)
			return ch
		}
		return inner(ctx, s)
	}
	if code := e.run("clean", "--safe", "-y"); code != 0 || !strings.Contains(e.errOut.String(), "Docker: Docker isn't running") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
}

func TestRestoreUnsafeExitsOne(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.restoreErr = actions.ErrRestoreUnsafe
	if code := e.run("restore", "t1"); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{"nope": "Nope.", "nope.": "Nope.", "what?": "What?", "": ""} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHomeDepsLoadsConfigLazily(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "Library/Application Support/soap/config.json")
	if _, err := HomeDeps(home, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("HomeDeps wrote a config file")
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := HomeDeps(home, false)
	if err != nil {
		t.Fatalf("a malformed config must not fail HomeDeps: %v", err)
	}
	var ups []model.ScanUpdate
	for u := range d.Scan(context.Background(), model.ScreenJunk) {
		ups = append(ups, u)
	}
	if len(ups) != 1 || ups[0].Err == nil || ups[0].Finder != "Config" {
		t.Errorf("scan updates %+v", ups)
	}
	called := false
	err = d.Clean(context.Background(), []model.CleanItem{{ID: "x"}}, func(model.CleanResult) { called = true })
	if err == nil || called {
		t.Errorf("clean: err %v, reported %v", err, called)
	}
}

func TestCleanExitsOneWhenTheConfigIsBroken(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.items = nil
	e.d.Scan = func(_ context.Context, s model.Screen) <-chan model.ScanUpdate {
		ch := make(chan model.ScanUpdate, 1)
		ch <- model.ScanUpdate{Screen: s, Finder: "Config", Err: errors.New("bad config line"), Done: 1, Total: 1}
		close(ch)
		return ch
	}
	if code := e.run("clean", "--safe", "-y"); code != 1 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !strings.Contains(e.errOut.String(), "Config: bad config line") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestRestoreAlreadyPutBackExitsOne(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	f.restoreErr = actions.ErrAlreadyRestored
	if code := e.run("restore", "x"); code != 1 || !strings.Contains(e.errOut.String(), "This item was already put back.") {
		t.Errorf("exit %d: %q", code, e.errOut.String())
	}
}
