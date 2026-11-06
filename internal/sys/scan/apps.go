//go:build darwin

package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

// AppsGroup is the group of every Apps item. The Apps screen groups them by app instead.
const AppsGroup = "Apps"

// Why an app can't be ticked.
const (
	needsPassword = "needs your password"
	needsAppMgmt  = "needs App Management access"
	noBundleID    = "has no bundle ID soap can read"
)

// wOK is W_OK from <unistd.h>: access(2) asks about write permission.
const wOK = 2

// access is access(2). Tests replace it, because macOS's App Management protection can't be set up
// on a temp folder.
var access = syscall.Access

// app is one app bundle in an Applications folder.
type app struct {
	path    string // the bundle
	root    string // the Applications folder it was found in
	name    string // "Notion" for Notion.app
	id      string // the bundle ID from its Info.plist; "" when it can't be read or isn't valid
	receipt bool   // it came from the App Store, so it has Contents/_MASReceipt
	stamp   model.Stamp
	blocked string // why it can't be moved, or ""
}

// apple reports whether the app is one of Apple's that comes with macOS: an Apple bundle ID and no
// App Store receipt. soap never lists those.
func (a app) apple() bool {
	return strings.HasPrefix(strings.ToLower(a.id), "com.apple.") && !a.receipt
}

// listApps finds the .app bundles in root and in its sub-folders, two levels down at most. It never
// goes into a bundle or another package, never follows a symlink, and skips hidden names.
func listApps(root string) ([]string, error) {
	var out []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name, p := e.Name(), filepath.Join(dir, e.Name())
			switch {
			case strings.HasPrefix(name, ".") || !e.IsDir(): // a symlink isn't a folder here
			case model.IsAppName(name):
				out = append(out, p)
			case depth < 2 && !IsPackage(p):
				_ = walk(p, depth+1) // a sub-folder that can't be read has no apps to list
			}
		}
		return nil
	}
	err := walk(root, 0) // walk fills out, so call it before out is read
	return out, err
}

// readApp reads what Apps needs to know about a bundle: its bundle ID (plutil reads both XML and
// binary Info.plist files), whether it came from the App Store, and whether the user uid can move
// it. A bundle that anyone else owns, or that it or its folder doesn't let uid write, needs an
// admin password, which soap never asks for. A bundle the user owns but can't write because
// macOS refuses it (EPERM) is protected by App Management, which the terminal needs to be allowed.
func readApp(ctx context.Context, run sys.Runner, uid int, root, path string) (app, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return app{}, err
	}
	if !info.IsDir() {
		return app{}, fmt.Errorf("%s isn't an app folder", filepath.Base(path))
	}
	a := app{path: path, root: root, name: model.AppName(path), stamp: StampOf(info)}
	out, err := run(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(path, "Contents/Info.plist"))
	if id := strings.TrimSpace(string(out)); err == nil && model.ValidBundleID(id) {
		a.id = id
	}
	a.receipt = exists(filepath.Join(path, "Contents/_MASReceipt"))
	writable := access(path, wOK)
	switch {
	case !ownedBy(info, uid) || access(filepath.Dir(path), wOK) != nil:
		a.blocked = needsPassword
	case errors.Is(writable, syscall.EPERM):
		a.blocked = needsAppMgmt
	case writable != nil:
		a.blocked = needsPassword
	case a.id == "":
		a.blocked = noBundleID
	}
	return a, nil
}

// ownedBy reports whether uid owns the file.
func ownedBy(info fs.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

// CheckApp is the Cleaner's last check on an app, just before it goes to the Trash: the bundle is
// still a folder the user owns and can move, its bundle ID can be read, and it isn't an Apple app
// that comes with macOS. Anything it can't tell keeps the app where it is.
func CheckApp(run sys.Runner) func(path string) error {
	return func(path string) error { return checkApp(context.Background(), run, os.Getuid(), path) }
}

func checkApp(ctx context.Context, run sys.Runner, uid int, path string) error {
	a, err := readApp(ctx, run, uid, filepath.Dir(path), path)
	switch {
	case err != nil:
		return err
	case a.blocked == needsPassword, a.blocked == needsAppMgmt:
		return fmt.Errorf("%s %s; move it to the Trash in Finder", a.name, a.blocked)
	case a.blocked != "":
		return fmt.Errorf("%s's bundle ID can't be read", a.name)
	case a.apple():
		return fmt.Errorf("%s comes with macOS", a.name)
	}
	return nil
}

// AppsFinders are the finders behind the Apps screen: one, for the apps and their leftovers.
func AppsFinders() []Finder {
	return []Finder{{Group: AppsGroup, Screen: model.ScreenApps, Find: findApps}}
}

// spotlightOff is the note when Spotlight can't be asked.
const spotlightOff = "Spotlight is off, so soap can't tell when apps were last opened."

// helperDirs are the folders inside an app that can hold helper apps of their own.
var helperDirs = []string{"Contents/MacOS", "Contents/Library/LoginItems", "Contents/Helpers"}

// nestedIDs are the bundle IDs, in lowercase, of the .app bundles one level inside an app's helper
// folders, such as Docker.app/Contents/MacOS/Docker Desktop.app. A bundle whose ID can't be read adds nothing.
func nestedIDs(ctx context.Context, run sys.Runner, a app) []string {
	var out []string
	for _, dir := range helperDirs {
		entries, err := os.ReadDir(filepath.Join(a.path, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !model.IsAppName(e.Name()) {
				continue
			}
			plist := filepath.Join(a.path, dir, e.Name(), "Contents/Info.plist")
			raw, err := run(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist)
			if id := strings.TrimSpace(string(raw)); err == nil && model.ValidBundleID(id) {
				out = append(out, strings.ToLower(id))
			}
		}
	}
	return out
}

// findApps lists the apps in env.Apps with their sizes, when they were last opened and their
// leftovers in ~/Library, and the leftovers of removed apps. Apple's apps that come with macOS are
// left out. Nothing is safe to tick for everyone, so nothing starts ticked.
func findApps(ctx context.Context, env Env) Result {
	var apps []app
	for _, root := range env.Apps {
		paths, err := listApps(root)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Result{Err: err}
		}
		for _, p := range paths {
			if a, err := readApp(ctx, env.Run, env.UID, root, p); err == nil && !a.apple() {
				apps = append(apps, a)
			}
		}
	}
	// By path, so the first copy of an app and the app that wins a shared leftover don't depend on the order of env.Apps.
	sort.Slice(apps, func(i, j int) bool { return apps[i].path < apps[j].path })

	var notes []model.Note
	note := func(text string) { notes = append(notes, model.Note{Screen: model.ScreenApps, Text: text}) }
	known := map[string]bool{} // every bundle ID on the Mac, in lowercase
	spotlight := env.hasTool("mdfind") && env.hasTool("mdls")
	if spotlight {
		ids, err := sys.SpotlightApps(ctx, env.Run)
		spotlight = err == nil && len(ids) > 0
		for id := range ids {
			known[id] = true
		}
	}
	opened, dated := make([]time.Time, len(apps)), false
	if spotlight {
		paths := make([]string, len(apps))
		for i, a := range apps {
			paths[i] = a.path
		}
		if dates, err := sys.LastOpened(ctx, env.Run, paths); err == nil {
			copy(opened, dates)
			dated = true
		} else {
			note("Spotlight didn't say when apps were last opened.")
		}
		for _, a := range apps {
			for _, id := range nestedIDs(ctx, env.Run, a) {
				known[id] = true
			}
		}
	} else {
		note(spotlightOff)
	}

	days := env.Config.UnusedDays
	if days <= 0 {
		days = 30
	}
	lib := readLibrary(ctx, env)
	// Spotlight keeps its date per bundle file, so an updated app loses it, and some apps never get
	// one. The app's own settings files, which macOS writes when it runs, can be newer.
	fromFiles := make([]bool, len(apps))
	for i, a := range apps {
		used := lastUsedFiles(lib, a.id, env.Now())
		if used.After(opened[i]) {
			opened[i], fromFiles[i] = used, true
		}
	}
	var items []model.CleanItem
	copies := map[string]int{} // apps seen so far with each bundle ID
	for i, a := range apps {
		id := "apps/unknown" + a.path
		if a.id != "" {
			key := strings.ToLower(a.id)
			known[key] = true
			copies[key]++
			id = "apps/" + a.id
			if n := copies[key]; n > 1 {
				id = fmt.Sprintf("apps/%s@%d", a.id, n)
			}
		}
		stamp := a.stamp
		items = append(items, model.CleanItem{
			ID: id, Screen: model.ScreenApps, Group: AppsGroup, Name: filepath.Base(a.path),
			Detail: model.LastUsedText(opened[i], fromFiles[i], dated, env.Now(), days), Method: model.MethodTrash, Paths: []string{a.path},
			Root: a.root, Bundle: a.path, Opened: opened[i], Stamp: &stamp, Blocked: a.blocked,
			UnusedDays: model.UnusedFor(opened[i], dated || fromFiles[i], env.Now(), days),
		})
	}

	taken := map[string]bool{}
	for _, l := range matchLeftovers(apps, lib) {
		taken[l.path] = true
		a := apps[l.app]
		if it, ok := leftoverItem(env, "apps/"+a.id+"/", l.path, a.path, l.byName, l.detail); ok {
			items = append(items, it)
		}
	}
	if spotlight {
		// codesign is slow (about a second for 34 apps), so it runs only when the result is shown.
		groups := appGroups(ctx, env, apps)
		for _, p := range removedLeftovers(ctx, env, lib, known, taken, groups) {
			if it, ok := leftoverItem(env, "apps/removed/", p, "", false, ""); ok {
				items = append(items, it)
			}
		}
	}
	for _, a := range apps {
		if a.blocked == needsAppMgmt {
			note("To remove apps that need App Management access, allow your terminal in System Settings › Privacy & Security › App Management, or move them to the Trash in Finder.")
			break
		}
	}
	switch lib.uuidHidden {
	case 0:
	case 1:
		note("1 app container is named by a number, and which app it belongs to can't be read without Full Disk Access, so it's left out.")
	default:
		note(fmt.Sprintf("%d app containers are named by a number, and which app each belongs to can't be read without Full Disk Access, so they're left out.", lib.uuidHidden))
	}
	for _, dir := range lib.unreadable {
		note("~/Library/" + dir + " can't be read without Full Disk Access, so leftovers in it aren't listed.")
	}

	items = measure(ctx, env.Sizer, items)
	present := map[string]bool{}
	for _, it := range items {
		if it.IsApp() {
			present[it.Bundle] = true
		}
	}
	kept := items[:0]
	for _, it := range items {
		if it.Bundle != "" && !present[it.Bundle] { // a leftover never outlives its app's row
			continue
		}
		if !allowed(env.Home, it) { // fail closed: a path CheckPath refuses is never offered
			continue
		}
		kept = append(kept, it)
	}
	return Result{Items: kept, Notes: notes}
}

// leftoverItem is the Apps item for one leftover in the home folder: ID prefix plus the path
// relative to home, to the Trash only, with the path's stamp. detail is the row text for a leftover
// that isn't ticked with its app (see leftover). ok is false when the path is gone.
func leftoverItem(env Env, prefix, path, bundle string, byName bool, detail string) (model.CleanItem, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return model.CleanItem{}, false
	}
	stamp := StampOf(info)
	return model.CleanItem{
		ID: prefix + strings.TrimPrefix(path, env.Home+"/"), Screen: model.ScreenApps, Group: AppsGroup,
		Name: model.HomePath(env.Home, path), Detail: detail, Method: model.MethodTrash, Paths: []string{path}, Root: env.Home,
		Bundle: bundle, ByName: byName, Stamp: &stamp,
	}, true
}

// allowed reports whether CheckPath accepts every path of the item, so a protected path (such as
// ~/Library/Application Support/soap, which an app named soap would match by name) is never offered.
func allowed(home string, it model.CleanItem) bool {
	for _, p := range it.Paths {
		if model.CheckPath(home, it.Root, p, it.Method, nil) != nil {
			return false
		}
	}
	return true
}

// lastUsedFiles is the newest modification time of the settings files macOS writes when the app
// with bundle ID id runs: its Preferences plist, its Saved Application State (the folder and the
// files directly in it), and a sandboxed app's own Preferences plist. Caches, Application Support,
// HTTPStorages and Logs are left out, because updaters write there without the app being opened.
// Symlinks and files that can't be read add nothing, and neither does a date more than an hour
// after now (a wrong clock); a date a little after now counts as now. The zero time means no date.
func lastUsedFiles(lib library, id string, now time.Time) time.Time {
	if id == "" {
		return time.Time{}
	}
	id = strings.ToLower(id)
	var newest time.Time
	see := func(info fs.FileInfo) {
		t := info.ModTime()
		if t.After(now.Add(time.Hour)) {
			return
		}
		if t.After(now) {
			t = now
		}
		if t.After(newest) {
			newest = t
		}
	}
	for _, e := range lib.entries {
		if e.id != id {
			continue
		}
		switch {
		case e.dir == "Preferences" && !e.isDir && strings.EqualFold(filepath.Ext(e.name), ".plist"):
			if info, err := os.Lstat(e.path); err == nil && info.Mode().IsRegular() {
				see(info)
			}
		case e.dir == "Saved Application State" && e.isDir && strings.EqualFold(filepath.Ext(e.name), ".savedState"):
			if info, err := os.Lstat(e.path); err == nil && info.IsDir() {
				see(info)
			}
			files, _ := os.ReadDir(e.path) // an unreadable folder adds nothing more
			for _, f := range files {
				if info, err := os.Lstat(filepath.Join(e.path, f.Name())); err == nil && info.Mode().IsRegular() {
					see(info)
				}
			}
		case e.dir == "Containers" && e.isDir:
			prefs := filepath.Join(e.path, "Data/Library/Preferences")
			if !plainDirs(e.path, "Data", "Library", "Preferences") {
				continue
			}
			files, _ := os.ReadDir(prefs)
			for _, f := range files {
				if !strings.EqualFold(f.Name(), id+".plist") {
					continue
				}
				if info, err := os.Lstat(filepath.Join(prefs, f.Name())); err == nil && info.Mode().IsRegular() {
					see(info)
				}
			}
		}
	}
	return newest
}

// plainDirs reports whether base/parts[0]/parts[1]/... are all folders and none is a symlink.
func plainDirs(base string, parts ...string) bool {
	p := base
	for _, part := range parts {
		p = filepath.Join(p, part)
		if info, err := os.Lstat(p); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}
