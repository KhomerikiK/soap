//go:build darwin

package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// projectKind is a folder a project can rebuild. It's recognised only by its marker: a file next
// to it, or a file inside it.
type projectKind struct {
	folder  string
	markers []string
	inside  string
	restore string // "" means pick npm, pnpm, yarn or bun from the lockfile
}

var projectKinds = []projectKind{
	{folder: "node_modules", markers: []string{"package.json"}},
	{folder: "vendor", markers: []string{"composer.json"}, restore: "composer install"},
	{folder: ".venv", inside: "pyvenv.cfg", restore: "recreate the virtualenv"},
	{folder: "venv", inside: "pyvenv.cfg", restore: "recreate the virtualenv"},
	{folder: "Pods", markers: []string{"Podfile"}, restore: "pod install"},
	{folder: "target", markers: []string{"Cargo.toml"}, restore: "cargo build"},
	{folder: ".next", markers: []string{"next.config.js", "next.config.mjs", "next.config.ts"}, restore: "rebuilt on the next run"},
	{folder: ".nuxt", markers: []string{"nuxt.config.js", "nuxt.config.ts"}, restore: "rebuilt on the next run"},
	{folder: ".turbo", markers: []string{"turbo.json"}, restore: "rebuilt on the next run"},
	{folder: ".svelte-kit", markers: []string{"svelte.config.js", "svelte.config.ts"}, restore: "rebuilt on the next run"},
	{folder: "build", markers: []string{"build.gradle", "build.gradle.kts"}, restore: "gradle build"},
}

const maxProjectDepth = 6

// activityFiles change whenever a project is worked on.
var activityFiles = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock", "composer.lock",
	"Cargo.lock", "Podfile.lock", "poetry.lock", "requirements.txt", "go.sum",
}

func findProjects(ctx context.Context, env Env) Result {
	var items []model.CleanItem
	var notes []model.Note
	note := func(text string) {
		notes = append(notes, model.Note{Screen: model.ScreenDev, Group: "Projects", Text: text})
	}
	for _, s := range env.Config.Skipped {
		note(s)
	}
	for _, root := range env.Config.ProjectRoots {
		info, err := os.Lstat(root)
		switch {
		case err != nil:
		case info.Mode()&os.ModeSymlink != 0:
			shown := root
			if rel, err := filepath.Rel(env.Home, root); err == nil && !strings.HasPrefix(rel, "..") {
				shown = "~/" + filepath.ToSlash(rel)
			}
			note(fmt.Sprintf("Skipped project root %q: it's a symlink.", shown))
		case info.IsDir():
			walkProjects(ctx, env, root, root, 0, &items)
		}
	}
	items = measure(ctx, env.Sizer, items)
	kept := items[:0]
	small := 0
	for _, it := range items {
		if it.Size < minProjectSize {
			small++
			continue
		}
		kept = append(kept, it)
	}
	switch {
	case small == 1:
		note("1 small project folder under 1 MB isn't shown.")
	case small > 1:
		note(fmt.Sprintf("%d small project folders under 1 MB aren't shown.", small))
	}
	return Result{Items: kept, Notes: notes}
}

// minProjectSize is the size under which a project folder isn't worth listing.
const minProjectSize = 1 << 20

func walkProjects(ctx context.Context, env Env, root, dir string, depth int, items *[]model.CleanItem) {
	if depth > maxProjectDepth || ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	matched := map[string]bool{}
	for _, k := range projectKinds {
		folder := filepath.Join(dir, k.folder)
		info, err := os.Lstat(folder)
		if err != nil || !info.IsDir() {
			continue
		}
		if marker := k.marker(dir, folder); marker != "" {
			matched[k.folder] = true
			*items = append(*items, projectItem(env, root, dir, folder, marker, k))
		}
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || matched[name] || name == ".git" || name == "node_modules" {
			continue
		}
		if strings.HasPrefix(name, ".") && name != ".worktrees" {
			continue
		}
		walkProjects(ctx, env, root, filepath.Join(dir, name), depth+1, items)
	}
}

func (k projectKind) marker(project, folder string) string {
	if k.inside != "" {
		if p := filepath.Join(folder, k.inside); exists(p) {
			return p
		}
		return ""
	}
	for _, m := range k.markers {
		if p := filepath.Join(project, m); exists(p) {
			return p
		}
	}
	return ""
}

func projectItem(env Env, root, project, folder, marker string, k projectKind) model.CleanItem {
	rel, _ := filepath.Rel(root, project)
	name := rel
	if rel == "." {
		name = filepath.Base(root)
	}
	restore := k.restore
	if restore == "" {
		restore = nodeInstall(project)
	}
	it := model.CleanItem{
		ID: "dev/projects/" + filepath.ToSlash(homeRel(env.Home, folder)), Screen: model.ScreenDev, Group: "Projects",
		Name: name + " · " + k.folder, Method: model.MethodDelete, Paths: []string{folder}, Root: root, Marker: marker,
	}
	if days := int(env.Now().Sub(lastActivity(project, marker)).Hours() / 24); days >= env.Config.StaleDays {
		it.Detail = fmt.Sprintf("untouched %d days · %s", days, restore)
	} else {
		it.Detail, it.Order = "in use · "+restore, 1
	}
	return it
}

// homeRel is a path relative to the home folder, so items from different roots never share an ID.
func homeRel(home, p string) string {
	rel, err := filepath.Rel(home, p)
	if err != nil {
		return p
	}
	return rel
}

func nodeInstall(project string) string {
	switch {
	case exists(filepath.Join(project, "pnpm-lock.yaml")):
		return "pnpm install"
	case exists(filepath.Join(project, "yarn.lock")):
		return "yarn install"
	case exists(filepath.Join(project, "bun.lockb")), exists(filepath.Join(project, "bun.lock")):
		return "bun install"
	}
	return "npm install"
}

// lastActivity is the newest of the project's git index, lockfiles and marker.
func lastActivity(project, marker string) time.Time {
	var newest time.Time
	consider := func(p string) {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	consider(marker)
	for _, f := range activityFiles {
		consider(filepath.Join(project, f))
	}
	if idx := gitIndex(project); idx != "" {
		consider(idx)
	}
	if newest.IsZero() {
		consider(project)
	}
	return newest
}

// gitIndex is a checkout's git index file. A worktree's .git is a file that points at its gitdir.
func gitIndex(project string) string {
	dotgit := filepath.Join(project, ".git")
	info, err := os.Lstat(dotgit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(dotgit, "index")
	}
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return ""
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(project, dir)
	}
	return filepath.Join(dir, "index")
}
