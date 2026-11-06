//go:build darwin

package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/KhomerikiK/soap/internal/sys"
)

// leftoverDirs are the folders in ~/Library where apps leave files.
var leftoverDirs = []string{"Application Support", "Caches", "Preferences", "Containers", "Group Containers",
	"Saved Application State", "HTTPStorages", "WebKit", "Cookies", "Logs", "LaunchAgents"}

// nameDirs are the leftover folders where an app's data can be named after the app rather than its
// bundle ID, such as Application Support/Notion.
var nameDirs = []string{"Application Support", "Caches", "Logs"}

// idSuffixes are the endings that leftover files add to a bundle ID.
var idSuffixes = []string{".plist", ".savedState", ".binarycookies"}

// libEntry is one file or folder directly inside a leftover folder.
type libEntry struct {
	dir   string // the leftover folder, such as "Caches"
	path  string
	name  string
	id    string // the bundle ID its name, its label or its container's record gives, in lowercase
	isDir bool
}

// library is what readLibrary found in the leftover folders.
type library struct {
	entries    []libEntry
	uuidHidden int      // containers named by a UUID whose record can't be read without Full Disk Access
	unreadable []string // leftover folders that can't be read, such as Cookies without Full Disk Access
}

// uuidName is a container named by a UUID, as newer macOS names some of them.
var uuidName = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// containerRecord is the file in a container that names the app it belongs to.
const containerRecord = ".com.apple.containermanagerd.metadata.plist"

// readLibrary lists the leftover folders in ~/Library, leaving out hidden names and symlinks, folders that are symlinks too. A
// LaunchAgent's ID is the label inside it. A container named by a UUID gets its ID from its record;
// when that can't be read (it needs Full Disk Access), the container is left out and counted.
func readLibrary(ctx context.Context, env Env) library {
	var lib library
	for _, dir := range leftoverDirs {
		root := env.home("Library/" + dir)
		if info, err := os.Lstat(root); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			continue // a folder that is a link leads somewhere else, so it's not listed
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			lib.unreadable = append(lib.unreadable, dir)
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			le := libEntry{dir: dir, path: filepath.Join(root, name), name: name, isDir: e.IsDir()}
			switch {
			case dir == "LaunchAgents":
				le.id = launchLabel(ctx, env, le.path)
			case dir == "Containers" && uuidName.MatchString(name):
				id, ok := containerID(ctx, env, le.path)
				if !ok {
					lib.uuidHidden++
					continue
				}
				le.id = id
			default:
				le.id = strings.ToLower(trimIDSuffix(name))
			}
			lib.entries = append(lib.entries, le)
		}
	}
	return lib
}

// trimIDSuffix drops .plist, .savedState or .binarycookies from the end of a name.
func trimIDSuffix(name string) string {
	for _, s := range idSuffixes {
		if len(name) > len(s) && strings.EqualFold(name[len(name)-len(s):], s) {
			return name[:len(name)-len(s)]
		}
	}
	return name
}

// launchLabel is a LaunchAgent's label in lowercase, read from the plist, or its file name without
// .plist when the label can't be read.
func launchLabel(ctx context.Context, env Env, path string) string {
	if out, err := env.Run(ctx, "plutil", "-extract", "Label", "raw", "-o", "-", path); err == nil {
		if label := strings.TrimSpace(string(out)); label != "" {
			return strings.ToLower(label)
		}
	}
	return strings.ToLower(trimIDSuffix(filepath.Base(path)))
}

// containerID reads the bundle ID, in lowercase, from a container's record. ok is false when the
// record can't be read.
func containerID(ctx context.Context, env Env, dir string) (string, bool) {
	rec := filepath.Join(dir, containerRecord)
	f, err := os.Open(rec)
	if err != nil {
		return "", false
	}
	f.Close()
	out, err := env.Run(ctx, "plutil", "-extract", "MCMMetadataIdentifier", "raw", "-o", "-", rec)
	id := strings.TrimSpace(string(out))
	if err != nil || id == "" {
		return "", false
	}
	return strings.ToLower(id), true
}

// idMatch reports whether an entry is named by the bundle ID id (in lowercase): exactly, or with
// .plist, .savedState or .binarycookies after it. A LaunchAgent matches when its label starts with
// the ID, and a Group Container when its group ID holds the ID as whole parts, as group.com.foo.app
// and 2BUA8C4S2C.com.foo.app hold com.foo.app.
func idMatch(e libEntry, id string) bool {
	switch e.dir {
	case "LaunchAgents":
		return e.id == id || strings.HasPrefix(e.id, id+".")
	case "Group Containers":
		g := e.id
		return g == id || strings.HasPrefix(g, id+".") || strings.HasSuffix(g, "."+id) || strings.Contains(g, "."+id+".")
	}
	return e.id == id
}

// nameDetail is the row text for a leftover matched by the app's name.
const nameDetail = "matched by name"

// leftover is a file or folder that belongs to an installed app.
type leftover struct {
	path   string
	app    int // an index into the apps
	byName bool
	// detail is what the row says when it can't be ticked together with the app: "matched by name",
	// or that another copy of the app uses it too. byName is true whenever detail isn't empty.
	detail string
}

// matchLeftovers finds the installed apps' leftovers in lib. First the bundle-ID matches; the longest
// matching ID wins an entry, so com.foo.app.helper's files don't go to com.foo.app. When an ID has more
// than one listed copy, its matches are rows with their own detail (not ticked with the app), because
// removing one copy must not take what the other uses. Then the
// name matches in Application Support, Caches and Logs: a folder named exactly like the app, or a
// sub-folder of a vendor folder named like the app (Google/AndroidStudio2025.1 for Android Studio,
// Google/Chrome for Google Chrome). A folder that holds such a sub-folder for another app is a
// vendor folder, and it's never matched as a whole. Each path belongs to one app at most: a
// bundle-ID match beats a name match, and an earlier app beats a later one. Apps without a bundle
// ID are left out.
func matchLeftovers(apps []app, lib library) []leftover {
	taken := map[string]bool{}
	var out []leftover
	add := func(path string, i int, detail string) {
		if !taken[path] {
			taken[path] = true
			out = append(out, leftover{path: path, app: i, byName: detail != "", detail: detail})
		}
	}
	copies := map[string]int{} // how many listed apps have each bundle ID
	for _, a := range apps {
		if a.id != "" {
			copies[strings.ToLower(a.id)]++
		}
	}
	for _, e := range lib.entries {
		best := -1
		for i, a := range apps {
			// The longest matching ID wins, so an app's own files don't go to an app whose ID is
			// the start of its ID. At the same length the earlier app wins.
			if a.id != "" && idMatch(e, strings.ToLower(a.id)) && (best < 0 || len(a.id) > len(apps[best].id)) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		detail := ""
		if copies[strings.ToLower(apps[best].id)] > 1 { // removing one copy must not take what the other uses
			detail = "another copy of " + apps[best].name + " uses this too"
		}
		add(e.path, best, detail)
	}
	for _, dir := range nameDirs {
		for _, e := range lib.entries {
			if e.dir != dir || !e.isDir || taken[e.path] {
				continue
			}
			vendor := false
			if !strings.Contains(e.name, ".") {
				for _, sub := range dirs(e.path) {
					for i, a := range apps {
						if a.id != "" && !strings.HasPrefix(sub, ".") && !strings.EqualFold(e.name, a.name) && vendorMatch(e.name, sub, a.name) {
							add(filepath.Join(e.path, sub), i, nameDetail)
							vendor = true
							break
						}
					}
				}
			}
			if vendor {
				continue
			}
			for i, a := range apps {
				if a.id != "" && strings.EqualFold(e.name, a.name) {
					add(e.path, i, nameDetail)
					break
				}
			}
		}
	}
	return out
}

// vendorMatch reports whether sub, a folder in the vendor folder vendor, is named like the app
// appName: the same letters and digits once a version at the end is dropped, with or without the
// vendor's name in front (Google + Chrome is Google Chrome).
func vendorMatch(vendor, sub, appName string) bool {
	a, s := squash(trimVersion(appName)), squash(trimVersion(sub))
	return a != "" && s != "" && (s == a || squash(vendor)+s == a)
}

// trimVersion drops a version from the end of a name, such as 2025.1 from AndroidStudio2025.1.
func trimVersion(name string) string { return strings.TrimRight(name, "0123456789. ") }

// squash keeps only the letters and digits of a name, in lowercase.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// removedShape is the bundle ID a leftover of a removed app must be named by: three parts or more,
// the first a domain such as com, org or io.
var removedShape = regexp.MustCompile(`^[a-z]{2,6}(\.[a-z0-9-]+){2,}$`)

// teamPrefix is the developer team ID that some Group Containers start with, such as 2BUA8C4S2C.
var teamPrefix = regexp.MustCompile(`^[0-9A-Z]{10}\.`)

// groupBase is the bundle ID a Group Container is named by, in lowercase: group.com.foo.app and
// 2BUA8C4S2C.com.foo.app both give com.foo.app.
func groupBase(name string) string {
	g := strings.ToLower(teamPrefix.ReplaceAllString(name, ""))
	g = strings.TrimPrefix(g, "group.")
	return strings.TrimPrefix(g, "groups.")
}

// vendorOf is the first two parts of a bundle ID: com.google for com.google.Chrome.
func vendorOf(id string) string {
	parts := strings.SplitN(id, ".", 3)
	if len(parts) < 2 {
		return id
	}
	return parts[0] + "." + parts[1]
}

// neverRemoved are the bundle ID starts of Apple's own domains: macOS and its apps use them, so
// they're never listed as a removed app's leftovers, whether the name has a team ID or a group. prefix before them or not.
var neverRemoved = []string{"com.apple.", "is.workflow.", "org.cups."}

// neverListed reports whether id, in lowercase, is in one of neverRemoved.
func neverListed(id string) bool {
	for _, p := range neverRemoved {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// appGroups are the app groups the apps declare, read with codesign. An app whose entitlements
// can't be read adds nothing.
func appGroups(ctx context.Context, env Env, apps []app) []string {
	var out []string
	for _, a := range apps {
		if groups, err := sys.AppGroups(ctx, env.Run, a.path); err == nil {
			out = append(out, groups...)
		}
	}
	return out
}

// declared reports whether a Group Container's name is a group in groups, or starts with one
// followed by a dot (UBF8T346G9.Office.Excel.WidgetExtension for UBF8T346G9.Office).
func declared(name string, groups []string) bool {
	n := strings.ToLower(name)
	for _, g := range groups {
		g = strings.ToLower(g)
		if g != "" && (n == g || strings.HasPrefix(n, g+".")) {
			return true
		}
	}
	return false
}

// launchProgram is the program a LaunchAgent runs, read from its plist with plutil: the first
// ProgramArguments item, or Program. ok is false when it can't be read or isn't a full path.
func launchProgram(ctx context.Context, env Env, path string) (string, bool) {
	for _, key := range []string{"ProgramArguments.0", "Program"} {
		out, err := env.Run(ctx, "plutil", "-extract", key, "raw", "-o", "-", path)
		if p := strings.TrimSpace(string(out)); err == nil && filepath.IsAbs(p) {
			return p, true
		}
	}
	return "", false
}

// removedLeftovers are the paths of entries named by the bundle ID of an app that isn't on the Mac:
// an ID shaped like one, never in neverRemoved, from a vendor that none of the known apps (Spotlight's
// and the ones found here, in lowercase) comes from, and not in a group an installed app declares
// (groups). A LaunchAgent counts only when the program it runs is gone. taken holds the installed
// apps' leftovers.
func removedLeftovers(ctx context.Context, env Env, lib library, known, taken map[string]bool, groups []string) []string {
	vendors := map[string]bool{}
	for id := range known {
		vendors[vendorOf(id)] = true
	}
	var out []string
	for _, e := range lib.entries {
		id := e.id
		if e.dir == "Group Containers" {
			id = groupBase(e.name)
			if declared(e.name, groups) {
				continue
			}
		}
		if taken[e.path] || !removedShape.MatchString(id) || neverListed(id) || vendors[vendorOf(id)] {
			continue
		}
		if e.dir == "LaunchAgents" {
			prog, ok := launchProgram(ctx, env, e.path)
			if _, err := os.Stat(prog); !ok || !errors.Is(err, fs.ErrNotExist) {
				continue
			}
		}
		out = append(out, e.path)
	}
	return out
}
