package actions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestHistoryAppendAndList(t *testing.T) {
	h := &History{Path: filepath.Join(t.TempDir(), "soap", "history.jsonl")}
	if err := h.CheckWritable(); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"first", "second"} {
		if err := h.Append(model.HistoryEntry{ID: name, Time: t0.Add(time.Duration(i) * time.Minute), Name: name, Method: model.MethodDelete, Result: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.OpenFile(h.Path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{damaged\n")
	f.Close()
	got, err := h.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "second" || got[1].ID != "first" {
		t.Errorf("List = %+v; want newest first, damaged lines skipped", got)
	}
	empty := &History{Path: filepath.Join(t.TempDir(), "none.jsonl")}
	if got, err := empty.List(); err != nil || len(got) != 0 {
		t.Errorf("no file: %v, %v", got, err)
	}
}

func TestHistoryRestore(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "Backup", "abc")
	trashed := filepath.Join(dir, ".Trash", "abc")
	if err := os.MkdirAll(trashed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &History{Home: dir, Path: filepath.Join(dir, "history.jsonl")}
	h.Append(model.HistoryEntry{ID: "t1", Name: "iPhone backup", Method: model.MethodTrash, Paths: []string{orig}, Trashed: []string{trashed}, Result: "done"})
	h.Append(model.HistoryEntry{ID: "d1", Name: "Caches", Method: model.MethodDelete, Result: "done"})

	if _, err := h.Restore("nope"); !errors.Is(err, ErrNoEntry) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := h.Restore("d1"); !errors.Is(err, ErrCantRestore) {
		t.Errorf("deleted entry: %v", err)
	}
	e, err := h.Restore("t1")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Restored {
		t.Error("the entry should be marked restored")
	}
	if _, err := os.Stat(orig); err != nil {
		t.Errorf("not put back: %v", err)
	}
	list, _ := h.List()
	if !list[1].Restored || list[1].ID != "t1" || len(list) != 2 {
		t.Errorf("history after restore = %+v", list)
	}
	if _, err := h.Restore("t1"); !errors.Is(err, ErrAlreadyRestored) || err.Error() != "this item was already put back" {
		t.Errorf("restoring twice: %v", err)
	}
}

func TestHistoryRestoreRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "file")
	trashed := filepath.Join(dir, ".Trash", "trashed-file")
	os.MkdirAll(filepath.Dir(trashed), 0o755)
	os.WriteFile(orig, []byte("new"), 0o644)
	os.WriteFile(trashed, []byte("old"), 0o644)
	h := &History{Home: dir, Path: filepath.Join(dir, "history.jsonl")}
	h.Append(model.HistoryEntry{ID: "t1", Method: model.MethodTrash, Paths: []string{orig}, Trashed: []string{trashed}, Result: "done"})
	if _, err := h.Restore("t1"); !errors.Is(err, ErrRestoreTaken) {
		t.Fatalf("got %v, want ErrRestoreTaken", err)
	}
	if b, _ := os.ReadFile(orig); string(b) != "new" {
		t.Error("the newer file was overwritten")
	}
}

func TestHistoryRestoreRefusesTamperedEntries(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	trashDir := filepath.Join(home, ".Trash")
	os.MkdirAll(trashDir, 0o755)
	goodOrig := filepath.Join(home, "Library/Caches/abc")
	os.MkdirAll(filepath.Dir(goodOrig), 0o755)
	newTrashed := func(name string) string {
		p := filepath.Join(trashDir, name)
		os.WriteFile(p, []byte("data"), 0o644)
		return p
	}
	target := filepath.Join(outside, "target")
	os.WriteFile(target, []byte("data"), 0o644)
	link := filepath.Join(trashDir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(outside, "elsewhere")
	os.WriteFile(elsewhere, []byte("data"), 0o644)
	cases := map[string]model.HistoryEntry{
		"path outside home":    {ID: "a", Method: model.MethodTrash, Paths: []string{filepath.Join(outside, "dest")}, Trashed: []string{newTrashed("a")}, Result: "done"},
		"path not clean":       {ID: "b", Method: model.MethodTrash, Paths: []string{home + "/Library/../Library/Caches/b"}, Trashed: []string{newTrashed("b")}, Result: "done"},
		"trashed not in Trash": {ID: "c", Method: model.MethodTrash, Paths: []string{goodOrig}, Trashed: []string{elsewhere}, Result: "done"},
		"trashed is a symlink": {ID: "d", Method: model.MethodTrash, Paths: []string{goodOrig}, Trashed: []string{link}, Result: "done"},
	}
	unclean := trashDir + "/./e"
	os.WriteFile(unclean, []byte("data"), 0o644)
	cases["trashed not clean"] = model.HistoryEntry{ID: "e", Method: model.MethodTrash, Paths: []string{goodOrig}, Trashed: []string{unclean}, Result: "done"}
	h := &History{Home: home, Path: filepath.Join(home, "history.jsonl")}
	for name, e := range cases {
		if err := h.Append(e); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Restore(e.ID); !errors.Is(err, ErrRestoreUnsafe) {
			t.Errorf("%s: got %v, want ErrRestoreUnsafe", name, err)
		}
	}
	if _, err := os.Lstat(goodOrig); err == nil {
		t.Error("a tampered entry moved a file")
	}
	if _, err := os.Lstat(filepath.Join(outside, "dest")); err == nil {
		t.Error("a tampered entry moved a file outside home")
	}
	for _, p := range []string{elsewhere, target, link, filepath.Join(trashDir, "a"), filepath.Join(trashDir, "b")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was moved", p)
		}
	}
	empty := &History{Path: h.Path}
	if _, err := empty.Restore("a"); !errors.Is(err, ErrRestoreUnsafe) {
		t.Errorf("empty Home: got %v, want ErrRestoreUnsafe", err)
	}
}

func TestHistoryAppendRepairsATornLastLine(t *testing.T) {
	h := &History{Path: filepath.Join(t.TempDir(), "history.jsonl")}
	if err := os.WriteFile(h.Path, []byte(`{"id":"torn","na`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Append(model.HistoryEntry{ID: "next", Result: "done"}); err != nil {
		t.Fatal(err)
	}
	got, _ := h.List()
	if len(got) != 1 || got[0].ID != "next" {
		t.Errorf("List = %+v; the new entry must survive a torn line before it", got)
	}
}

func TestHistoryRestoreRefusesSymlinkedTrashFolder(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".Trash")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(outside, "f")
	os.WriteFile(moved, []byte("data"), 0o644)
	orig := filepath.Join(home, "f")
	h := &History{Home: home, Path: filepath.Join(home, "history.jsonl")}
	h.Append(model.HistoryEntry{ID: "s", Method: model.MethodTrash, Paths: []string{orig}, Trashed: []string{filepath.Join(home, ".Trash", "f")}, Result: "done"})
	if _, err := h.Restore("s"); !errors.Is(err, ErrRestoreUnsafe) {
		t.Fatalf("got %v, want ErrRestoreUnsafe", err)
	}
	if _, err := os.Lstat(orig); err == nil {
		t.Error("a file was moved out of a symlinked Trash")
	}
}

func TestRestorePutsAnAppBack(t *testing.T) {
	home := t.TempDir()
	h := &History{Path: HistoryPath(home), Home: home}
	orig := filepath.Join(home, "Applications/Notion.app")
	trashed := filepath.Join(home, ".Trash/Notion.app")
	if err := os.MkdirAll(filepath.Join(trashed, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.CheckWritable(); err != nil {
		t.Fatal(err)
	}
	if err := h.Append(model.HistoryEntry{ID: "a1", Name: "Notion.app", Method: model.MethodTrash, Paths: []string{orig}, Trashed: []string{trashed}, Result: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Restore("a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(orig, "Contents")); err != nil {
		t.Errorf("the app isn't back: %v", err)
	}
}

func TestRestoreRefusesPathsOutsideHomeThatAreNotApps(t *testing.T) {
	home := t.TempDir()
	h := &History{Path: HistoryPath(home), Home: home}
	trashed := filepath.Join(home, ".Trash/x")
	for _, p := range []string{"/Applications/Notion.app/Contents/MacOS/Notion", "/Applications/notes.txt", "/Library/Foo.app", "/System/Applications/Mail.app"} {
		e := model.HistoryEntry{ID: "x", Method: model.MethodTrash, Paths: []string{p}, Trashed: []string{trashed}, Result: "done"}
		if err := h.checkRestorable(e); !errors.Is(err, ErrRestoreUnsafe) {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}

func TestRestoreTop(t *testing.T) {
	home := "/Users/me"
	for path, want := range map[string]string{
		"/Applications/Notion.app":           "/Applications",
		"/Applications/Utilities/Tool.app":   "/Applications",
		"/Users/me/Applications/zoom.us.app": "/Users/me",
		"/Users/me/Downloads/x.iso":          "/Users/me",
		"/Applications/Notion.app/Contents":  "/Users/me",
		"/Library/Foo.app":                   "/Users/me",
	} {
		if got := restoreTop(home, path); got != want {
			t.Errorf("restoreTop(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRestoreIntoApplicationsNeedsARealAppFolder(t *testing.T) {
	home := t.TempDir()
	h := &History{Path: HistoryPath(home), Home: home}
	trashDir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(filepath.Join(trashDir, "real/Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trashDir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(trashDir, "real"), filepath.Join(trashDir, "link")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ dest, trashed string }{
		"a file":       {"/Applications/Notion.app", "file"},
		"a symlink":    {"/Applications/Notion.app", "link"},
		"missing":      {"/Applications/Notion.app", "nothing"},
		"not app name": {"/Applications/Utilities/.app", "real"},
	}
	for name, c := range cases {
		e := model.HistoryEntry{ID: "x", Method: model.MethodTrash, Paths: []string{c.dest}, Trashed: []string{filepath.Join(trashDir, c.trashed)}, Result: "done"}
		if err := h.checkRestorable(e); !errors.Is(err, ErrRestoreUnsafe) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	ok := model.HistoryEntry{ID: "x", Method: model.MethodTrash, Paths: []string{"/Applications/Notion.app"}, Trashed: []string{filepath.Join(trashDir, "real")}, Result: "done"}
	if err := h.checkRestorable(ok); err != nil {
		t.Errorf("a real folder is fine: %v", err)
	}
}
