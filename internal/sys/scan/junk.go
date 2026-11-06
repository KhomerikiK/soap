//go:build darwin

package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// JunkFinders are the finders behind the Junk screen.
func JunkFinders() []Finder {
	return []Finder{
		{Group: "App caches", Screen: model.ScreenJunk, Find: findCaches},
		{Group: "Browser caches", Screen: model.ScreenJunk, Find: findBrowserCaches},
		{Group: "Logs and crash reports", Screen: model.ScreenJunk, Find: findLogs},
		{Group: "Trash", Screen: model.ScreenJunk, Find: findTrash},
		{Group: "iOS device backups", Screen: model.ScreenJunk, Find: findBackups},
	}
}

// devCaches are folders in ~/Library/Caches that the Dev screen lists instead.
var devCaches = map[string]bool{
	"com.apple.dt.Xcode": true, "Yarn": true, "composer": true, "CocoaPods": true,
	"pip": true, "go-build": true, "Homebrew": true,
}

// cacheApps maps cache folders to the process that uses them, so they're busy while it runs.
var cacheApps = map[string]string{
	"Google":                     "Google Chrome",
	"BraveSoftware":              "Brave Browser",
	"com.microsoft.edgemac":      "Microsoft Edge",
	"company.thebrowser.Browser": "Arc",
	"Firefox":                    "firefox",
	"com.apple.Safari":           "Safari",
}

func findCaches(ctx context.Context, env Env) Result {
	root := env.home("Library/Caches")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}
	}
	if err != nil {
		return Result{Err: err}
	}
	var items []model.CleanItem
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || devCaches[name] {
			continue
		}
		if name == "JetBrains" {
			items = append(items, jetBrainsCaches(root)...)
			continue
		}
		detail := "rebuilt automatically"
		if strings.HasSuffix(name, ".ShipIt") {
			detail = "updater leftovers"
		}
		items = append(items, model.CleanItem{
			ID: "junk/caches/" + name, Screen: model.ScreenJunk, Group: "App caches", Name: name, Detail: detail,
			Method: model.MethodDelete, Paths: []string{filepath.Join(root, name)}, Root: root, Safe: true, App: cacheApps[name],
		})
	}
	items = measure(ctx, env.Sizer, items)
	return Result{Items: mergeSmall(items, "junk/caches/small", "small caches")}
}

// jetBrainsCaches lists one item per IDE, such as PhpStorm2025.2, busy while that IDE runs.
func jetBrainsCaches(root string) []model.CleanItem {
	dir := filepath.Join(root, "JetBrains")
	var items []model.CleanItem
	for _, name := range dirs(dir) {
		items = append(items, model.CleanItem{
			ID: "junk/caches/JetBrains/" + name, Screen: model.ScreenJunk, Group: "App caches", Name: "JetBrains " + name,
			Detail: "rebuilt automatically", Method: model.MethodDelete, Paths: []string{filepath.Join(dir, name)}, Root: root,
			Safe: true, App: jetBrainsApp(name),
		})
	}
	return items
}

// jetBrainsApp gives the process name of the IDE that owns a cache folder such as PhpStorm2025.2.
// IntelliJ IDEA's process is "idea"; the other IDEs use their product name.
func jetBrainsApp(folder string) string {
	product := strings.TrimRight(folder, "0123456789.")
	switch product {
	case "IntelliJIdea", "IdeaIC":
		return "idea"
	}
	return product
}

// browsers are the Chromium browsers whose profile caches can be cleaned.
var browsers = []struct{ app, dir string }{
	{"Google Chrome", "Library/Application Support/Google/Chrome"},
	{"Brave Browser", "Library/Application Support/BraveSoftware/Brave-Browser"},
	{"Microsoft Edge", "Library/Application Support/Microsoft Edge"},
	{"Arc", "Library/Application Support/Arc/User Data"},
}

// profileCaches are the cache folders inside a Chromium profile. Everything else in a profile
// (history, logins, cookies, extensions) is never touched.
var profileCaches = []string{
	"Cache", "Code Cache", "GPUCache", "DawnGraphiteCache", "DawnWebGPUCache",
	"Service Worker/CacheStorage",
}

func findBrowserCaches(ctx context.Context, env Env) Result {
	var items []model.CleanItem
	for _, b := range browsers {
		root := env.home(b.dir)
		var paths []string
		for _, profile := range dirs(root) {
			if profile != "Default" && !strings.HasPrefix(profile, "Profile ") {
				continue
			}
			for _, c := range profileCaches {
				p := filepath.Join(root, profile, c)
				if info, err := os.Lstat(p); err == nil && info.IsDir() {
					paths = append(paths, p)
				}
			}
		}
		if len(paths) == 0 {
			continue
		}
		items = append(items, model.CleanItem{
			ID: "junk/browsers/" + b.app, Screen: model.ScreenJunk, Group: "Browser caches", Name: b.app + " profile caches",
			Detail: "history, logins and extensions stay", Method: model.MethodDelete, Paths: paths, Root: root, Safe: true, App: b.app,
		})
	}
	return Result{Items: measure(ctx, env.Sizer, items)}
}

func findLogs(ctx context.Context, env Env) Result {
	root := env.home("Library/Logs")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}
	}
	if err != nil {
		return Result{Err: err}
	}
	var items []model.CleanItem
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		name := e.Name()
		detail := "~/Library/Logs"
		if name == "DiagnosticReports" {
			detail = "crash reports"
		}
		items = append(items, model.CleanItem{
			ID: "junk/logs/" + name, Screen: model.ScreenJunk, Group: "Logs and crash reports", Name: name, Detail: detail,
			Method: model.MethodDelete, Paths: []string{filepath.Join(root, name)}, Root: root, Safe: true,
		})
	}
	items = measure(ctx, env.Sizer, items)
	return Result{Items: mergeSmall(items, "junk/logs/small", "small logs")}
}

// fdaNote is shown when the terminal lacks Full Disk Access, which macOS requires for the Trash
// and for iOS backups.
const fdaNote = "Trash and iOS backups are hidden: give your terminal Full Disk Access (System Settings › Privacy & Security › Full Disk Access)."

func findTrash(ctx context.Context, env Env) Result {
	root := env.home(".Trash")
	entries, err := os.ReadDir(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Result{}
	case errors.Is(err, fs.ErrPermission):
		return Result{Notes: []model.Note{{Screen: model.ScreenJunk, Text: fdaNote, Code: model.NoteNoFullDiskAccess}}}
	case err != nil:
		return Result{Err: err}
	}
	it := model.CleanItem{ID: "junk/trash", Screen: model.ScreenJunk, Group: "Trash", Name: "Trash", Method: model.MethodDelete, Root: root}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue // the cleaner never deletes through a symlink, so links stay in the Trash
		}
		it.Paths = append(it.Paths, filepath.Join(root, e.Name()))
	}
	switch len(it.Paths) {
	case 0:
		return Result{}
	case 1:
		it.Detail = "1 item"
	default:
		it.Detail = fmt.Sprintf("%d items", len(it.Paths))
	}
	return Result{Items: measure(ctx, env.Sizer, []model.CleanItem{it})}
}

func findBackups(ctx context.Context, env Env) Result {
	root := env.home("Library/Application Support/MobileSync/Backup")
	var items []model.CleanItem
	for _, id := range dirs(root) { // nothing when missing, or hidden without Full Disk Access
		dir := filepath.Join(root, id)
		name, date := backupInfo(ctx, env, dir)
		items = append(items, model.CleanItem{
			ID: "junk/backups/" + id, Screen: model.ScreenJunk, Group: "iOS device backups", Name: name + " backup",
			Detail: date + "goes to the Trash", Method: model.MethodTrash, Paths: []string{dir}, Root: root,
		})
	}
	return Result{Items: measure(ctx, env.Sizer, items)}
}

// backupInfo reads a backup's device name and date. plutil -convert json can't handle the data
// values in these files, so each key is read on its own.
func backupInfo(ctx context.Context, env Env, dir string) (name, date string) {
	plist := filepath.Join(dir, "Info.plist")
	name = "iOS device"
	if out, err := env.Run(ctx, "plutil", "-extract", "Device Name", "raw", "-o", "-", plist); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			name = s
		}
	}
	if out, err := env.Run(ctx, "plutil", "-extract", "Last Backup Date", "raw", "-o", "-", plist); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out))); err == nil {
			date = "from " + t.Local().Format("2 Jan 2006") + " · "
		}
	}
	return name, date
}
