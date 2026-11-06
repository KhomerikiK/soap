package model

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AppPath returns the outermost .app bundle in path, or "" when the process isn't part of an app.
func AppPath(path string) string {
	// The Mac's disk ignores case, so "Old.APP" is a bundle too. ".app/" is ASCII, so comparing
	// byte by byte with ASCII case folding is exact.
	for i := 0; i+len(".app/") <= len(path); i++ {
		if strings.EqualFold(path[i:i+len(".app/")], ".app/") {
			return path[:i+len(".app")]
		}
	}
	return ""
}

// AppName returns a bundle's name without ".app" (in any case), for example "Google Chrome".
func AppName(appPath string) string {
	name := filepath.Base(appPath)
	if len(name) > len(".app") && strings.EqualFold(name[len(name)-len(".app"):], ".app") {
		return name[:len(name)-len(".app")]
	}
	return name
}

// IsSystem reports whether the process ships with macOS.
func IsSystem(p Process) bool {
	for _, prefix := range []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/"} {
		if strings.HasPrefix(p.Path, prefix) {
			return true
		}
	}
	return p.Path == "" && p.UID == 0
}

// IsContainerEngine reports whether the process belongs to Docker Desktop or OrbStack. Stopping
// it would stop every container.
func IsContainerEngine(p Process) bool {
	switch AppName(AppPath(p.Path)) {
	case "Docker", "OrbStack":
		return AppPath(p.Path) != ""
	}
	return false
}

// AppUsage is one row on the Memory screen: an app with all its helper processes, or one process.
type AppUsage struct {
	Name    string    `json:"name"`
	AppPath string    `json:"app_path,omitempty"`
	PIDs    []int     `json:"pids"`
	Memory  uint64    `json:"memory"`
	CPU     float64   `json:"cpu"`
	UID     int       `json:"uid"`
	System  bool      `json:"system"`
	Main    ProcessID `json:"-"` // the process that quit and kill act on
}

// GroupByApp sums memory and CPU per app, largest memory first. With raw set, every process is
// its own row, so quitting acts on that process alone.
func GroupByApp(procs []Process, raw bool) []AppUsage {
	idx := map[string]int{}
	var out []AppUsage
	hasMain := map[string]bool{} // groups whose Main is a main executable already
	for _, p := range procs {
		key, name, appPath := groupKey(p)
		if raw {
			key, name, appPath = fmt.Sprintf("pid:%d", p.PID), p.Name, ""
		}
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, AppUsage{Name: name, AppPath: appPath, UID: p.UID, System: IsSystem(p), Main: p.ProcessID})
		}
		u := &out[i]
		u.PIDs = append(u.PIDs, p.PID)
		u.Memory += p.Memory
		u.CPU += p.CPU
		// The lowest PID wins among main executables, as in mainProcesses, so both screens act
		// on the same process.
		if appPath != "" && isMainExecutable(p.Path, appPath) && (!hasMain[key] || p.PID < u.Main.PID) {
			u.Main, u.UID = p.ProcessID, p.UID
			hasMain[key] = true
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Memory > out[j].Memory })
	return out
}

// groupKey returns the grouping key, display name and app path for p.
func groupKey(p Process) (key, name, appPath string) {
	if a := AppPath(p.Path); a != "" {
		return "app:" + a, AppName(a), a
	}
	return fmt.Sprintf("pid:%d", p.PID), p.Name, ""
}

// isMainExecutable reports whether path is the app's own executable rather than a helper.
func isMainExecutable(path, appPath string) bool {
	rest, ok := strings.CutPrefix(path, appPath+"/Contents/MacOS/")
	return ok && !strings.Contains(rest, "/")
}

// IsAppName reports whether a file name ends in .app, in any case, with a name before it.
func IsAppName(name string) bool {
	return len(name) > len(".app") && strings.EqualFold(filepath.Ext(name), ".app")
}

// ValidBundleID reports whether id can be an app's bundle ID: letters, digits, hyphens and dots,
// with a dot somewhere, as in Apple's reverse-DNS IDs. Apps IDs hold it, so a name without a dot,
// such as "removed", can't be mistaken for one.
func ValidBundleID(id string) bool {
	if !strings.Contains(id, ".") {
		return false
	}
	for _, part := range strings.Split(id, ".") {
		if part == "" { // ".", "a.", ".a" and "a..b" aren't bundle IDs
			return false
		}
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// AppsFirst puts the apps' own rows first and keeps the order otherwise. The Cleaner moves apps
// first, so it knows which apps stay before it gets to their leftovers.
func AppsFirst(items []CleanItem) []CleanItem {
	out := make([]CleanItem, 0, len(items))
	for _, it := range items {
		if it.IsApp() {
			out = append(out, it)
		}
	}
	for _, it := range items {
		if !it.IsApp() {
			out = append(out, it)
		}
	}
	return out
}

// InLaunchAgents reports whether path is in ~/Library/LaunchAgents.
func InLaunchAgents(home, path string) bool {
	return strings.HasPrefix(path, home+"/Library/LaunchAgents/")
}

// LaunchAgentWarning is the line a preview shows when it moves a LaunchAgent to the Trash.
const LaunchAgentWarning = "LaunchAgents keep running until you log out, even from the Trash."

// RemovedApps is the group of leftovers whose app isn't on the Mac any more.
const RemovedApps = "Leftovers of removed apps"

// AppGroup is one app on the Apps screen with its leftovers, or the leftovers of removed apps.
type AppGroup struct {
	Key       string      // the app's bundle, or "" for the leftovers of removed apps
	Name      string      // the app's name, such as "Notion", or RemovedApps
	App       *CleanItem  // the app's own row; nil for the leftovers of removed apps
	Leftovers []CleanItem // the bundle-ID matches, then the name matches, each largest first
	Size      uint64      // the app and all its leftovers
}

// AppGroups puts the Apps screen's items into groups by their Bundle: one for each app, then the
// leftovers of removed apps. Apps without a last-opened date come first, then the others from the
// one unused longest, so unused apps come before the ones in use. bySize puts the largest groups
// first instead.
func AppGroups(items []CleanItem, bySize bool) []AppGroup {
	byKey := map[string]*AppGroup{}
	var keys []string
	for _, it := range items {
		g, ok := byKey[it.Bundle]
		if !ok {
			g = &AppGroup{Key: it.Bundle, Name: RemovedApps}
			if it.Bundle != "" {
				g.Name = AppName(it.Bundle)
			}
			byKey[it.Bundle] = g
			keys = append(keys, it.Bundle)
		}
		g.Size += it.Size
		if it.IsApp() {
			app := it
			g.App = &app
		} else {
			g.Leftovers = append(g.Leftovers, it)
		}
	}
	out := make([]AppGroup, 0, len(keys))
	for _, k := range keys {
		g := byKey[k]
		sort.SliceStable(g.Leftovers, func(i, j int) bool {
			a, b := g.Leftovers[i], g.Leftovers[j]
			if a.ByName != b.ByName {
				return !a.ByName
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
			return a.Name < b.Name
		})
		if k != "" {
			out = append(out, *g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if bySize {
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		} else if ao, bo := lastOpened(a), lastOpened(b); !ao.Equal(bo) {
			return ao.Before(bo) // no date is the zero time, before every date
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.Key < b.Key
	})
	if g, ok := byKey[""]; ok {
		out = append(out, *g)
	}
	return out
}

func lastOpened(g AppGroup) time.Time {
	if g.App == nil {
		return time.Time{}
	}
	return g.App.Opened
}

// UnusedFor is unusedDays (30 when it's below 1) when an app counts as unused: its last-use date is
// known, and that was unusedDays or more ago, when OpenedText or LastUsedText says "not opened in N
// days" or "not used in about N days". It's 0 for every other app, including one without a date.
// known is true when Spotlight gave the date or the app's settings files did.
func UnusedFor(opened time.Time, known bool, now time.Time, unusedDays int) int {
	if unusedDays < 1 {
		unusedDays = 30
	}
	if !known || opened.IsZero() || int(now.Sub(opened).Hours()/24) < unusedDays {
		return 0
	}
	return unusedDays
}

// OpenedText says when an app was last opened, from Spotlight's date: "last opened 2 Oct", "not
// opened in 37 days" once that's unusedDays or more, or "never opened, as far as macOS knows"
// without a date. known is false when Spotlight couldn't be asked, and then it's "last opened
// unknown". Dates are in now's time zone.
func OpenedText(opened time.Time, known bool, now time.Time, unusedDays int) string {
	return LastUsedText(opened, false, known, now, unusedDays)
}

// LastUsedText is OpenedText for a date that may come from the app's settings files (fromFiles):
// macOS writes those when the app runs, but the date is only close to the last time it was opened,
// so the text says "last used about 27 Oct" or "not used in about 37 days". A file date is shown
// even when known (Spotlight) is false. Without a file date, it is OpenedText.
func LastUsedText(opened time.Time, fromFiles, known bool, now time.Time, unusedDays int) string {
	fromFiles = fromFiles && !opened.IsZero()
	switch {
	case !known && !fromFiles:
		return "last opened unknown"
	case opened.IsZero():
		return "never opened, as far as macOS knows"
	}
	if unusedDays < 1 {
		unusedDays = 30 // the config's default; LoadConfig never passes less than 1
	}
	verb, about := "opened", ""
	if fromFiles {
		verb, about = "used", "about "
	}
	// Days are whole 24-hour periods since the app was opened, so they can be one less than the
	// difference between two calendar dates.
	if days := int(now.Sub(opened).Hours() / 24); days >= unusedDays {
		if days == 1 {
			return fmt.Sprintf("not %s in %s1 day", verb, about)
		}
		return fmt.Sprintf("not %s in %s%d days", verb, about, days)
	}
	when := opened.In(now.Location())
	if when.Year() == now.Year() {
		return "last " + verb + " " + about + when.Format("2 Jan")
	}
	return "last " + verb + " " + about + when.Format("2 Jan 2006")
}
