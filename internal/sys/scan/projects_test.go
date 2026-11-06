//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// big is above the 1 MB line under which project folders aren't listed.
const big = 2 << 20

func TestFindProjectsHidesSmallFolders(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Projects/one/package.json", 10)
	put(t, home, "Projects/one/node_modules/x/index.js", 5000)
	put(t, home, "Projects/two/composer.json", 10)
	put(t, home, "Projects/two/vendor/x.php", 5000)
	put(t, home, "Projects/huge/package.json", 10)
	put(t, home, "Projects/huge/node_modules/x/index.js", big)
	r := findProjects(context.Background(), testEnv(home, nil))
	if got := strings.Join(ids(r.Items), ","); got != "dev/projects/Projects/huge/node_modules" {
		t.Errorf("items = %s", got)
	}
	want := model.Note{Screen: model.ScreenDev, Group: "Projects", Text: "2 small project folders under 1 MB aren't shown."}
	if len(r.Notes) != 1 || r.Notes[0] != want {
		t.Errorf("notes = %+v", r.Notes)
	}
	os.RemoveAll(filepath.Join(home, "Projects/two"))
	r = findProjects(context.Background(), testEnv(home, nil))
	want.Text = "1 small project folder under 1 MB isn't shown."
	if len(r.Notes) != 1 || r.Notes[0] != want {
		t.Errorf("notes = %+v", r.Notes)
	}
}

func TestFindProjects(t *testing.T) {
	home := t.TempDir()
	old := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // 128 days before now
	recent := now.Add(-48 * time.Hour)
	touch := func(rel string, at time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(home, rel), at, at); err != nil {
			t.Fatal(err)
		}
	}
	// A stale npm project that uses pnpm, with a nested node_modules that must not be listed apart.
	put(t, home, "Projects/old-app/package.json", 10)
	put(t, home, "Projects/old-app/pnpm-lock.yaml", 10)
	put(t, home, "Projects/old-app/node_modules/left-pad/index.js", big)
	put(t, home, "Projects/old-app/node_modules/dep/node_modules/x/index.js", big)
	touch("Projects/old-app/package.json", old)
	touch("Projects/old-app/pnpm-lock.yaml", old)
	// A worktree of old-app, recently active according to its git index.
	put(t, home, "Projects/old-app/.git/worktrees/wt/index", 10)
	touch("Projects/old-app/.git/worktrees/wt/index", recent)
	put(t, home, "Projects/old-app/.worktrees/wt/package.json", 10)
	touch("Projects/old-app/.worktrees/wt/package.json", old)
	gitdir := "gitdir: " + filepath.Join(home, "Projects/old-app/.git/worktrees/wt") + "\n"
	if err := os.WriteFile(filepath.Join(home, "Projects/old-app/.worktrees/wt/.git"), []byte(gitdir), 0o644); err != nil {
		t.Fatal(err)
	}
	put(t, home, "Projects/old-app/.worktrees/wt/node_modules/a/index.js", big)
	// A recent Laravel project.
	put(t, home, "Projects/api/composer.json", 10)
	put(t, home, "Projects/api/composer.lock", 10)
	put(t, home, "Projects/api/vendor/autoload.php", big)
	touch("Projects/api/composer.json", recent)
	touch("Projects/api/composer.lock", recent)
	// Not projects: Go's vendor folder and a symlinked node_modules.
	put(t, home, "Projects/goapp/go.mod", 10)
	put(t, home, "Projects/goapp/vendor/x.go", big)
	put(t, home, "Projects/linked/package.json", 10)
	if err := os.Symlink(filepath.Join(home, "Projects/old-app/node_modules"), filepath.Join(home, "Projects/linked/node_modules")); err != nil {
		t.Fatal(err)
	}
	// A Python virtualenv, and a project deeper than the scan goes.
	put(t, home, "Projects/py/.venv/pyvenv.cfg", 10)
	put(t, home, "Projects/py/.venv/lib/x.py", big)
	put(t, home, "Projects/a/b/c/d/e/f/g/package.json", 10)
	put(t, home, "Projects/a/b/c/d/e/f/g/node_modules/x/index.js", big)

	r := findProjects(context.Background(), testEnv(home, nil))
	items := byID(r.Items)
	if got := strings.Join(ids(r.Items), ","); got != "dev/projects/Projects/api/vendor,dev/projects/Projects/old-app/.worktrees/wt/node_modules,dev/projects/Projects/old-app/node_modules,dev/projects/Projects/py/.venv" {
		t.Fatalf("items = %s", got)
	}
	nm := items["dev/projects/Projects/old-app/node_modules"]
	if nm.Name != "old-app · node_modules" || nm.Detail != "untouched 128 days · pnpm install" || nm.Order != 0 || nm.Marker != filepath.Join(home, "Projects/old-app/package.json") {
		t.Errorf("old-app = %+v", nm)
	}
	if api := items["dev/projects/Projects/api/vendor"]; api.Detail != "in use · composer install" || api.Order != 1 {
		t.Errorf("api = %+v", api)
	}
	if wt := items["dev/projects/Projects/old-app/.worktrees/wt/node_modules"]; !strings.HasPrefix(wt.Detail, "in use") {
		t.Errorf("a worktree's activity comes from its git index: %+v", wt)
	}
	if py := items["dev/projects/Projects/py/.venv"]; py.Marker != filepath.Join(home, "Projects/py/.venv/pyvenv.cfg") || !strings.HasSuffix(py.Detail, "recreate the virtualenv") {
		t.Errorf("py = %+v", py)
	}
	for _, it := range r.Items {
		if it.Safe {
			t.Errorf("%s: project folders are never pre-ticked", it.ID)
		}
	}
	checkPaths(t, home, r.Items, testEnv(home, nil).Config.ProjectRoots)
}

func TestFindProjectsIDsDifferAcrossRoots(t *testing.T) {
	home := t.TempDir()
	for _, root := range []string{"Projects", "Code"} {
		put(t, home, root+"/app/package.json", 10)
		put(t, home, root+"/app/node_modules/x/index.js", big)
	}
	env := testEnv(home, nil)
	env.Config.ProjectRoots = []string{filepath.Join(home, "Projects"), filepath.Join(home, "Code")}
	r := findProjects(context.Background(), env)
	got := strings.Join(ids(r.Items), ",")
	if got != "dev/projects/Code/app/node_modules,dev/projects/Projects/app/node_modules" {
		t.Errorf("items = %s", got)
	}
}

func TestFindProjectsSkipsSymlinkedRoot(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	put(t, other, "app/package.json", 10)
	put(t, other, "app/node_modules/x/index.js", big)
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "Code")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	env := testEnv(home, nil)
	env.Config.ProjectRoots = []string{link, filepath.Join(home, "Missing")}
	r := findProjects(context.Background(), env)
	if len(r.Items) != 0 {
		t.Errorf("items = %v", ids(r.Items))
	}
	want := model.Note{Screen: model.ScreenDev, Group: "Projects", Text: `Skipped project root "~/Code": it's a symlink.`}
	if len(r.Notes) != 1 || r.Notes[0] != want {
		t.Errorf("notes = %+v", r.Notes)
	}
}

func TestFindProjectsReportsConfigSkips(t *testing.T) {
	home := t.TempDir()
	env := testEnv(home, nil)
	env.Config.Skipped = []string{"Skipped project root \"Code\": it isn't a full path."}
	r := findProjects(context.Background(), env)
	if len(r.Notes) != 1 || r.Notes[0].Text != env.Config.Skipped[0] || r.Notes[0].Screen != model.ScreenDev || r.Notes[0].Group != "Projects" {
		t.Errorf("notes = %+v", r.Notes)
	}
}
