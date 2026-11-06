package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

const fakeHome = "/Users/me"

// installDisk gives the CLI a fake walk of /Users/me: Projects (8 GiB, partly unreadable),
// Tool.app (1 GiB) and big.iso (4 GiB).
func installDisk(e *env) {
	e.d.Home = fakeHome
	e.d.Walk = func(_ context.Context, tree *model.DiskTree) {
		kids := tree.Listed(tree.Top(), 4<<30, 1, []model.DiskChild{{Name: "Projects"}, {Name: "Tool.app", Package: "app"}})
		inside := tree.Listed(kids[0], 8<<30, 3, []model.DiskChild{{Name: "Mail"}})
		tree.Unreadable(inside[0])
		tree.Measured(kids[1], 1<<30, false)
		tree.Finish(false)
	}
	e.d.List = func(dir string) ([]model.DiskEntry, error) {
		if dir != fakeHome {
			return nil, fs.ErrNotExist
		}
		return []model.DiskEntry{
			{Name: "Projects", Path: fakeHome + "/Projects", Dir: true},
			{Name: "Tool.app", Path: fakeHome + "/Tool.app", Dir: true, Package: "app"},
			{Name: "big.iso", Path: fakeHome + "/big.iso", Size: 4 << 30},
		}, nil
	}
}

func TestDiskListsTheLargestItems(t *testing.T) {
	e := newEnv(t, "")
	installDisk(e)
	if code := e.run("disk"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	for _, want := range []string{"~  ≥ 13.0 GB", "SIZE", "≥ 8.0 GB", "62%", "Projects/", "disk/Projects", "Tool.app · app", "big.iso", "disk/big.iso",
		"1 folder couldn't be read, so sizes with ≥ are at least what's shown."} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Projects/") > strings.Index(out, "big.iso") {
		t.Errorf("largest first:\n%s", out)
	}
}

func TestDiskJSON(t *testing.T) {
	e := newEnv(t, "")
	installDisk(e)
	if code := e.run("disk", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Path       string `json:"path"`
		Unreadable int    `json:"unreadable_folders"`
		Items      []struct {
			ID      string `json:"id"`
			Size    uint64 `json:"size"`
			Package string `json:"package"`
		} `json:"items"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if got.Path != fakeHome || got.Unreadable != 1 || len(got.Items) != 3 || got.Items[0].ID != "disk/Projects" || got.Items[2].Package != "app" {
		t.Errorf("got %+v", got)
	}
}

func TestDiskRefusesFoldersOutsideHome(t *testing.T) {
	e := newEnv(t, "")
	installDisk(e)
	if code := e.run("disk", "/Applications"); code != 1 || !strings.Contains(e.errOut.String(), "soap disk looks only inside your home folder.") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
	e.errOut.Reset()
	if code := e.run("disk", "~/Nope"); code != 1 || !strings.Contains(e.errOut.String(), "Couldn't read ~/Nope") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
}

// dupeItems is a set of two copies of trip.mov, the one in Movies kept.
func dupeItems() []model.CleanItem {
	copies := []model.Copy{{Path: fakeHome + "/Movies/trip.mov", Keep: true}, {Path: fakeHome + "/Downloads/trip.mov"}}
	var items []model.CleanItem
	for i, c := range copies {
		items = append(items, model.CleanItem{ID: "dupes/" + strings.TrimPrefix(c.Path, fakeHome+"/"), Screen: model.ScreenDupes, Group: "Copies",
			Name: model.HomePath(fakeHome, c.Path), Detail: "made 2 Mar 2025", Size: 4 << 30, Unshared: 4 << 30, Method: model.MethodTrash,
			Paths: []string{c.Path}, Root: fakeHome, Set: "s1", Copies: copies, Keep: i == 0})
	}
	return items
}

func newDupesEnv(t *testing.T) (*env, *cleanupFake) {
	t.Helper()
	e, f := newCleanupEnv(t, "")
	e.d.Home = fakeHome
	f.items = append(f.items, dupeItems()...)
	f.notes = nil
	return e, f
}

func TestDupesListsSets(t *testing.T) {
	e, _ := newDupesEnv(t)
	if code := e.run("dupes"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	for _, want := range []string{"trip.mov · 2 copies · 4.0 GB each · frees up to 4.0 GB", "keep  ~/Movies/trip.mov", "dupes/Downloads/trip.mov", "1 set · frees up to 4.0 GB."} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PhpStorm") {
		t.Error("dupes lists only Dupes items")
	}
	e.out.Reset()
	if code := e.run("dupes", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Sets []struct {
			Name   string `json:"name"`
			Copies []struct {
				ID   string `json:"id"`
				Keep bool   `json:"keep"`
			} `json:"copies"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sets) != 1 || got.Sets[0].Name != "trip.mov" || !got.Sets[0].Copies[0].Keep || got.Sets[0].Copies[1].ID != "dupes/Downloads/trip.mov" {
		t.Errorf("got %+v", got)
	}
}

func TestCleanRefusesEveryCopyOfASet(t *testing.T) {
	e, f := newDupesEnv(t)
	if code := e.run("clean", "dupes/Movies/trip.mov", "dupes/Downloads/trip.mov", "-y"); code != 2 || len(f.cleaned) != 0 ||
		!strings.Contains(e.errOut.String(), "Keep at least one copy of trip.mov.") {
		t.Errorf("exit %d, cleaned %v, %q", code, f.cleaned, e.errOut.String())
	}
	if code := e.run("clean", "dupes/Downloads/trip.mov", "-y"); code != 0 || !slices.Equal(cleanedIDs(f), []string{"dupes/Downloads/trip.mov"}) {
		t.Errorf("one copy: exit %d, cleaned %v, %s", code, cleanedIDs(f), e.errOut.String())
	}
}

func TestCleanResolvesDiskIDs(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	e.d.DiskItem = func(_ context.Context, id string) (model.CleanItem, error) {
		switch id {
		case "disk/Movies/trip.mov":
			return model.CleanItem{ID: id, Screen: model.ScreenDisk, Name: "~/Movies/trip.mov", Size: 4 << 30, Method: model.MethodTrash}, nil
		case "disk/Documents":
			return model.CleanItem{}, errors.New("soap never removes ~/Documents")
		}
		return model.CleanItem{}, fmt.Errorf("%s: %w", id, fs.ErrNotExist)
	}
	if code := e.run("clean", "disk/Movies/trip.mov", "-y"); code != 0 || !slices.Equal(cleanedIDs(f), []string{"disk/Movies/trip.mov"}) {
		t.Fatalf("exit %d, cleaned %v: %s", code, cleanedIDs(f), e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "to the Trash") {
		t.Errorf("the preview says where it goes:\n%s", e.out.String())
	}
	if code := e.run("clean", "disk/Documents", "-y"); code != 2 || !strings.Contains(e.errOut.String(), "disk/Documents can't be cleaned: soap never removes ~/Documents.") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
	if code := e.run("clean", "disk/nope", "-y"); code != 2 || !strings.Contains(e.errOut.String(), "No item with ID disk/nope.") {
		t.Errorf("exit %d, %q", code, e.errOut.String())
	}
}

func TestCleanWarnsAboutICloudDrive(t *testing.T) {
	e, _ := newCleanupEnv(t, "")
	e.d.DiskItem = func(_ context.Context, id string) (model.CleanItem, error) {
		p := "/Users/me/Library/Mobile Documents/com~apple~CloudDocs/old.key"
		return model.CleanItem{ID: id, Screen: model.ScreenDisk, Name: "old.key", Size: 1 << 20, Method: model.MethodTrash,
			Paths: []string{p}, Root: "/Users/me"}, nil
	}
	if code := e.run("clean", "disk/Library/Mobile Documents/com~apple~CloudDocs/old.key", "--dry-run"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), model.ICloudWarning) {
		t.Errorf("the preview doesn't warn about iCloud Drive:\n%s", e.out.String())
	}
}

func TestCleanScansOnlyTheScreensItsIDsNeed(t *testing.T) {
	e, _ := newCleanupEnv(t, "")
	var scanned []model.Screen
	inner := e.d.Scan
	e.d.Scan = func(ctx context.Context, s model.Screen) <-chan model.ScanUpdate {
		scanned = append(scanned, s)
		return inner(ctx, s)
	}
	if code := e.run("clean", "junk/backups/abc", "--dry-run"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !slices.Equal(scanned, []model.Screen{model.ScreenJunk}) {
		t.Errorf("scanned %v", scanned)
	}
	scanned = nil
	if code := e.run("clean", "--safe", "--dry-run"); code != 0 || !slices.Equal(scanned, []model.Screen{model.ScreenJunk, model.ScreenDev}) {
		t.Errorf("--safe scans Junk and Dev only: exit %d, scanned %v", code, scanned)
	}
}

// TestCleanRefusesADiskFolderThatHoldsTheLastCopy: one copy is a Dupes ID and the other is inside
// a Disk folder in the same command. The folder is named in another case, as the Mac's disk allows.
func TestCleanRefusesADiskFolderThatHoldsTheLastCopy(t *testing.T) {
	home := t.TempDir()
	a, b := home+"/Movies/trip.mov", home+"/Downloads/trip.mov"
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e, f := newCleanupEnv(t, "")
	e.d.Home = home
	copies := []model.Copy{{Path: a, Keep: true}, {Path: b}}
	f.items = append(f.items, model.CleanItem{ID: "dupes/Downloads/trip.mov", Screen: model.ScreenDupes, Name: "~/Downloads/trip.mov", Method: model.MethodTrash,
		Paths: []string{b}, Root: home, Set: "s1", Copies: copies})
	e.d.DiskItem = func(_ context.Context, id string) (model.CleanItem, error) {
		return model.CleanItem{ID: id, Screen: model.ScreenDisk, Name: "~/movies", Method: model.MethodTrash, Paths: []string{home + "/movies"}, Root: home}, nil
	}
	if _, err := os.Stat(home + "/movies"); err != nil {
		t.Skip("this disk is case-sensitive")
	}
	if code := e.run("clean", "dupes/Downloads/trip.mov", "disk/movies", "-y"); code != 2 || len(f.cleaned) != 0 ||
		!strings.Contains(e.errOut.String(), "Keep at least one copy of trip.mov.") {
		t.Errorf("exit %d, cleaned %v, %q", code, f.cleaned, e.errOut.String())
	}
}

// diskIDResolver resolves Disk IDs the way scan.DiskItem does: the path is cleaned.
func diskIDResolver(e *env) {
	e.d.DiskItem = func(_ context.Context, id string) (model.CleanItem, error) {
		rel := path.Clean(strings.TrimPrefix(id, "disk/"))
		return model.CleanItem{ID: "disk/" + rel, Screen: model.ScreenDisk, Name: "~/" + rel, Size: 1 << 20, Method: model.MethodTrash,
			Paths: []string{fakeHome + "/" + rel}, Root: fakeHome}, nil
	}
}

func TestCleanDropsDuplicatesByResolvedID(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	diskIDResolver(e)
	if code := e.run("clean", "disk/a", "disk/a/", "disk/./a", "-y"); code != 0 || !slices.Equal(cleanedIDs(f), []string{"disk/a"}) {
		t.Errorf("exit %d, cleaned %v, %s", code, cleanedIDs(f), e.errOut.String())
	}
}

func TestCleanRefusesAnItemInsideAnotherPickedFolder(t *testing.T) {
	e, f := newCleanupEnv(t, "")
	diskIDResolver(e)
	if code := e.run("clean", "disk/Projects", "disk/Projects/x", "-y"); code != 2 || len(f.cleaned) != 0 ||
		!strings.Contains(e.errOut.String(), "disk/Projects/x is inside disk/Projects, which is already in this clean-up.") {
		t.Errorf("exit %d, cleaned %v, %q", code, f.cleaned, e.errOut.String())
	}
	// A sibling that only shares a name prefix is fine.
	if code := e.run("clean", "disk/Projects", "disk/Projects2", "-y"); code != 0 {
		t.Errorf("siblings: exit %d, %s", code, e.errOut.String())
	}
}

func TestDiskRowsInICloudDriveSaySo(t *testing.T) {
	e := newEnv(t, "")
	installDisk(e)
	dir := fakeHome + "/Library/Mobile Documents/com~apple~CloudDocs"
	e.d.List = func(string) ([]model.DiskEntry, error) {
		return []model.DiskEntry{{Name: "scan.pdf", Path: dir + "/scan.pdf", Size: 1 << 20}}, nil
	}
	if code := e.run("disk", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if out := e.out.String(); !strings.Contains(out, "scan.pdf · in iCloud Drive") {
		t.Errorf("output:\n%s", out)
	}
}
