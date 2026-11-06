package model

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Screen is the tab a clean item belongs to.
type Screen string

const (
	ScreenJunk  Screen = "junk"
	ScreenDev   Screen = "dev"
	ScreenDisk  Screen = "disk"
	ScreenDupes Screen = "dupes"
	ScreenApps  Screen = "apps"
)

// Method is how an item is removed.
type Method int

const (
	_             Method = iota // the zero value is not a method, so a forgotten Method never deletes
	MethodDelete                // removed for good; it rebuilds itself
	MethodTrash                 // moved to the Trash, so it can be put back
	MethodCommand               // the owning tool's own clean-up command
)

func (m Method) String() string {
	switch m {
	case MethodTrash:
		return "trash"
	case MethodCommand:
		return "command"
	case MethodDelete:
		return "delete"
	}
	return "unknown"
}

// MarshalText writes the method as a word in JSON.
func (m Method) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

// UnmarshalText reads a method written by MarshalText.
func (m *Method) UnmarshalText(b []byte) error {
	switch string(b) {
	case "unknown":
		*m = 0
	case "delete":
		*m = MethodDelete
	case "trash":
		*m = MethodTrash
	case "command":
		*m = MethodCommand
	default:
		return fmt.Errorf("unknown clean method %q", b)
	}
	return nil
}

// Stamp is what a file or folder looked like when it was scanned, so a clean-up can tell whether
// it changed since. Size and ModTime only count for files.
type Stamp struct {
	Dir     bool      `json:"dir,omitempty"`
	Size    int64     `json:"size"`  // a file's length in bytes
	ModTime time.Time `json:"mtime"` // when a file last changed
	Inode   uint64    `json:"inode"`
}

// Same reports whether now is still the scanned file or folder: the same one on disk and, for a
// file, the same size and modified time.
func (s Stamp) Same(now Stamp) bool {
	if s.Dir != now.Dir || s.Inode != now.Inode {
		return false
	}
	return s.Dir || (s.Size == now.Size && s.ModTime.Equal(now.ModTime))
}

// Copy is one file of a Dupes set, as the scan saw it.
type Copy struct {
	Path  string    `json:"path"`
	Made  time.Time `json:"made"` // when the file was created
	Stamp Stamp     `json:"stamp"`
	Keep  bool      `json:"keep,omitempty"` // the copy that k and K leave unticked
}

// CleanItem is one thing a clean-up screen can remove.
type CleanItem struct {
	ID      string   `json:"id"` // stable across scans
	Screen  Screen   `json:"screen"`
	Group   string   `json:"group"`
	Name    string   `json:"name"`
	Detail  string   `json:"detail,omitempty"`
	Size    uint64   `json:"size"`                      // bytes freed by cleaning (an estimate for commands)
	SizeMin bool     `json:"size_is_minimum,omitempty"` // some folders couldn't be read
	Method  Method   `json:"method"`
	Paths   []string `json:"paths,omitempty"`   // for Delete and Trash
	Root    string   `json:"root,omitempty"`    // every path must sit strictly inside it
	Marker  string   `json:"marker,omitempty"`  // a file that must still exist when cleaning (project folders)
	Command []string `json:"command,omitempty"` // for Command
	SizeOf  []string `json:"size_of,omitempty"` // for Command: folders whose size, measured before and after, shows what it freed
	Safe    bool     `json:"safe"`              // pre-ticked unless busy or blocked
	App     string   `json:"app,omitempty"`     // busy while a process with this name runs
	Busy    bool     `json:"busy,omitempty"`
	Blocked string   `json:"blocked,omitempty"` // why it can't be cleaned at all
	Order   int      `json:"-"`                 // lower comes first in its group, before size

	Stamp    *Stamp `json:"stamp,omitempty"`    // Disk and Dupes: the path as scanned; the item is skipped if it changed
	Set      string `json:"set,omitempty"`      // Dupes: the set of identical files this copy belongs to
	Copies   []Copy `json:"copies,omitempty"`   // Dupes: every copy in the set, so the cleaner can check one stays
	Keep     bool   `json:"keep,omitempty"`     // Dupes: this is the copy that k and K leave unticked
	Unshared uint64 `json:"unshared,omitempty"` // Dupes: the bytes only this copy holds, which removing it frees

	Bundle string    `json:"bundle,omitempty"`  // Apps: the app's .app bundle, on its own row and its leftovers; busy while a process from inside it runs
	ByName bool      `json:"by_name,omitempty"` // Apps: a leftover not ticked with the app: matched by name, or shared with another copy
	Opened time.Time `json:"opened,omitzero"`   // Apps: when the app was last opened (Spotlight) or used (its settings files, if newer); zero without a date
	// Apps: on the row of an app that counts as unused, the unused_days setting it wasn't opened for
	// (UnusedFor); 0 for any other item.
	UnusedDays int `json:"unused_days,omitempty"`
}

// Tickable reports whether the user may tick the item.
func (c CleanItem) Tickable() bool { return !c.Busy && c.Blocked == "" }

// PreTicked reports whether the item starts ticked.
func (c CleanItem) PreTicked() bool { return c.Safe && c.Tickable() }

// Reason is why the item can't be ticked, or "".
func (c CleanItem) Reason() string {
	if c.Blocked != "" {
		return c.Blocked
	}
	if c.Busy {
		return "Quit " + c.BusyName() + " first"
	}
	return ""
}

// BusyName is the app whose running makes the item busy: its App, or else the name of its Bundle.
func (c CleanItem) BusyName() string {
	if c.App == "" && c.Bundle != "" {
		return AppName(c.Bundle)
	}
	return c.App
}

// IsApp reports whether the item is an app's own row on the Apps screen: its one path is its bundle.
func (c CleanItem) IsApp() bool { return c.Bundle != "" && len(c.Paths) == 1 && c.Paths[0] == c.Bundle }

// Frees reports whether cleaning the item frees space now. Moving to the Trash doesn't.
func (c CleanItem) Frees() bool { return c.Method != MethodTrash }

// How says what cleaning the item does: "delete", "to the Trash", or the command.
func (c CleanItem) How() string {
	switch c.Method {
	case MethodTrash:
		return "to the Trash"
	case MethodCommand:
		return strings.Join(c.Command, " ")
	case MethodDelete:
		return "delete"
	}
	return "unknown"
}

// Note is a line a finder wants shown, for a whole screen (Group "") or for one group.
type Note struct {
	Screen Screen `json:"screen"`
	Group  string `json:"group,omitempty"`
	Text   string `json:"text"`
	Code   string `json:"code,omitempty"` // what the note means, for code that reacts to it; "" for most notes
}

// NoteNoFullDiskAccess is the code of Junk's note that the terminal has no Full Disk Access, so
// the Trash and iOS backups can't be seen.
const NoteNoFullDiskAccess = "no-full-disk-access"

// LacksFullDiskAccess reports whether notes say the terminal has no Full Disk Access.
func LacksFullDiskAccess(notes []Note) bool {
	for _, n := range notes {
		if n.Code == NoteNoFullDiskAccess {
			return true
		}
	}
	return false
}

// ScanUpdate is one finder's result, delivered while a scan runs. Most finders send one update.
// A finder that streams sends many: each one with Add set adds items to what it sent before.
type ScanUpdate struct {
	Screen Screen
	Finder string // the finder's group name
	Items  []CleanItem
	Notes  []Note
	Err    error  // the finder failed as a whole
	Add    bool   // the items add to the finder's earlier ones in this scan, instead of replacing them
	Status string // how far a streaming finder has got, such as "Hashing 12 of 40 sets · 3.1 GB read"
	Done   int    // finders finished so far, including this one
	Total  int
}

// Result is what happened to one item during a clean-up.
type Result string

const (
	ResultDone    Result = "done"
	ResultPartial Result = "partial"
	ResultFailed  Result = "failed"
	ResultSkipped Result = "skipped"
)

// CleanResult is what happened to one item during a clean-up.
type CleanResult struct {
	Item    CleanItem `json:"item"`
	Result  Result    `json:"result"`
	Freed   uint64    `json:"freed"`
	Message string    `json:"message,omitempty"`
}

// HistoryEntry is one cleaned item, as recorded in the history file.
type HistoryEntry struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	ItemID   string    `json:"item_id"`
	Name     string    `json:"name"`
	Method   Method    `json:"method"`
	Size     uint64    `json:"size"` // bytes freed, or moved for the Trash
	Paths    []string  `json:"paths,omitempty"`
	Trashed  []string  `json:"trashed,omitempty"` // where each path went in the Trash, in the order of Paths
	Command  []string  `json:"command,omitempty"`
	Result   Result    `json:"result"`
	Message  string    `json:"message,omitempty"`
	Restored bool      `json:"restored,omitempty"`
}

// CanRestore reports whether the entry's files are in the Trash and can be put back.
func (e HistoryEntry) CanRestore() bool {
	return e.Method == MethodTrash && len(e.Trashed) > 0 && !e.Restored
}

// State describes the entry for the history list.
func (e HistoryEntry) State() string {
	switch {
	case e.Restored:
		return "put back"
	case e.Result == "skipped":
		return "skipped: " + e.Message
	case e.Result == "failed":
		return "failed: " + e.Message
	case e.Result == "partial" && e.Method != MethodTrash:
		return "partly deleted: " + e.Message
	case e.CanRestore():
		return "in the Trash · u put back"
	case e.Method == MethodCommand:
		return "ran " + strings.Join(e.Command, " ")
	case e.Method == MethodTrash:
		return "in the Trash"
	case e.Method != MethodDelete:
		return "unknown"
	}
	return "deleted"
}

// allowedRoots are the folders, relative to the home folder, that clean-up may delete inside.
// Configured project roots inside the home folder are allowed as well.
var allowedRoots = []string{
	"Library/Caches",
	"Library/Logs",
	".Trash",
	"Library/Application Support/MobileSync/Backup",
	"Library/Application Support/Google/Chrome",
	"Library/Application Support/BraveSoftware/Brave-Browser",
	"Library/Application Support/Microsoft Edge",
	"Library/Application Support/Arc/User Data",
	"Library/Developer/Xcode/DerivedData",
	"Library/Developer/Xcode/Archives",
	"Library/Developer/Xcode/iOS DeviceSupport",
	"Library/Developer/Xcode/watchOS DeviceSupport",
	"Library/Developer/Xcode/tvOS DeviceSupport",
	"Library/Developer/CoreSimulator",
	".npm",
	".gradle",
	".m2",
	".cache",
	".composer",
}

// protectedPaths are folders, relative to the home folder, that are never removed, nor any folder
// that holds one. What's inside them may be removed, except inside packages.
var protectedPaths = []string{
	"Applications", "Desktop", "Documents", "Downloads", "Library", "Movies", "Music", "Pictures", "Public", ".Trash",
	"Library/Mobile Documents",
	"Library/Mobile Documents/com~apple~CloudDocs",
	"Library/Application Support/soap",
	"Pictures/Photos Library.photoslibrary",
	"Music/Music",
}

// CheckPath reports why path may not be removed as part of an item whose allowed folder is root
// and whose method is method, or nil when it may. It only looks at the strings; the cleaner also
// checks the file itself. The home folder may be the root only for items that go to the Trash,
// and then the protected folders, and the folders that hold them, are refused.
func CheckPath(home, root, path string, method Method, projectRoots []string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s isn't a clean absolute path", path)
	}
	if IsAppFolder(home, root) {
		return checkAppPath(root, path, method)
	}
	if root == home && filepath.IsAbs(home) && filepath.Clean(home) == home {
		if method != MethodTrash {
			return fmt.Errorf("%s can only go to the Trash", path)
		}
		if !strings.HasPrefix(path, home+"/") {
			return fmt.Errorf("%s isn't inside your home folder", path)
		}
		return checkProtected(home, path)
	}
	if !allowedRoot(home, root, projectRoots) {
		return fmt.Errorf("%s isn't a folder soap cleans inside", root)
	}
	if !strings.HasPrefix(path, root+"/") {
		return fmt.Errorf("%s isn't inside %s", path, root)
	}
	return nil
}

// SystemApps is the Mac's main Applications folder. Apps can go to the Trash from it, or from the
// Applications folder in the home folder, and from nowhere else outside the home folder.
const SystemApps = "/Applications"

// AppFolders are the folders the Apps screen lists apps from.
func AppFolders(home string) []string {
	return []string{SystemApps, filepath.Join(home, "Applications")}
}

// IsAppFolder reports whether root is /Applications or ~/Applications.
func IsAppFolder(home, root string) bool {
	if root == SystemApps {
		return true
	}
	return filepath.IsAbs(home) && filepath.Clean(home) == home && root == filepath.Join(home, "Applications")
}

// checkAppPath is the path rule for apps: a .app bundle inside /Applications or ~/Applications,
// directly or in a sub-folder such as Utilities, never a path inside a bundle, and only to the
// Trash. Who owns the bundle, and whether it comes with macOS, need I/O, so the Apps finder and the
// Cleaner check those.
func checkAppPath(root, path string, method Method) error {
	if method != MethodTrash {
		return fmt.Errorf("%s can only go to the Trash", path)
	}
	rel, ok := strings.CutPrefix(path, root+"/")
	if !ok {
		return fmt.Errorf("%s isn't inside %s", path, root)
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if IsAppName(p) {
			return fmt.Errorf("%s is inside %s, which soap moves only as a whole", filepath.Base(path), p)
		}
	}
	if !IsAppName(parts[len(parts)-1]) {
		return fmt.Errorf("%s isn't an app", path)
	}
	return nil
}

// AppRoot is the Applications folder that path is an app in, as the path rule sees it, or "".
func AppRoot(home, path string) string {
	for _, root := range AppFolders(home) {
		if strings.HasPrefix(path, root+"/") && CheckPath(home, root, path, MethodTrash, nil) == nil {
			return root
		}
	}
	return ""
}

// checkProtected refuses a protected folder, or a folder that holds one. Names are compared without
// regard to case, as the Mac's disk does, including letters such as ſ (long s) and K (Kelvin) that
// fold to plain ASCII.
func checkProtected(home, path string) error {
	rel := foldKey(strings.TrimPrefix(path, home+"/"))
	for _, p := range protectedPaths {
		lp := foldKey(p)
		switch {
		case rel == lp:
			return fmt.Errorf("soap never removes ~/%s", p)
		case strings.HasPrefix(lp, rel+"/"):
			return fmt.Errorf("~/%s holds ~/%s, which soap never removes", strings.TrimPrefix(path, home+"/"), p)
		}
	}
	return nil
}

// foldKey maps every letter to the smallest letter it folds with, so two names that differ only in
// case compare equal. strings.ToLower misses folds such as ſ to s.
func foldKey(s string) string {
	return strings.Map(func(r rune) rune {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		return low
	}, s)
}

func allowedRoot(home, root string, projectRoots []string) bool {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Clean(root) != root {
		return false
	}
	for _, r := range allowedRoots {
		if root == filepath.Join(home, r) {
			return true
		}
	}
	for _, r := range projectRoots {
		if root == filepath.Clean(r) && strings.HasPrefix(root, home+"/") {
			return true
		}
	}
	return false
}

// TickState is a group's tick box.
type TickState int

const (
	TickNone TickState = iota
	TickSome
	TickAll
)

// GroupTick says whether none, some or all of the group's tickable items are ticked.
func GroupTick(items []CleanItem, ticked map[string]bool) TickState {
	n, on := 0, 0
	for _, it := range items {
		if !it.Tickable() {
			continue
		}
		n++
		if ticked[it.ID] {
			on++
		}
	}
	switch {
	case on == 0:
		return TickNone
	case on == n:
		return TickAll
	}
	return TickSome
}

// CleanGroup is a group of items as a screen shows it.
type CleanGroup struct {
	Name  string
	Items []CleanItem
	Size  uint64
	Notes []string
	Err   string
}

// GroupItems groups items for display. Groups and their items are sorted by size, largest first
// (or by name), with an item's Order coming first. Groups that only have notes or an error are
// kept. Notes for the whole screen (Group "") are left out.
func GroupItems(items []CleanItem, notes []Note, errs map[string]string, byName bool) []CleanGroup {
	byGroup := map[string]*CleanGroup{}
	get := func(name string) *CleanGroup {
		g, ok := byGroup[name]
		if !ok {
			g = &CleanGroup{Name: name}
			byGroup[name] = g
		}
		return g
	}
	for _, it := range items {
		g := get(it.Group)
		g.Items = append(g.Items, it)
		g.Size += it.Size
	}
	for _, n := range notes {
		if n.Group != "" {
			g := get(n.Group)
			g.Notes = append(g.Notes, n.Text)
		}
	}
	for name, e := range errs {
		get(name).Err = e
	}
	out := make([]CleanGroup, 0, len(byGroup))
	for _, g := range byGroup {
		SortItems(g.Items, byName)
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !byName && out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// SortItems orders items by Order, then by size (largest first) or by name, then by ID.
func SortItems(items []CleanItem, byName bool) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if !byName && a.Size != b.Size {
			return a.Size > b.Size
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.ID < b.ID
	})
}

// Summary is the question asked before cleaning, for example
// "Delete 7 for good, move 1 to the Trash and run 2 commands? Frees 18.1 GB."
func Summary(items []CleanItem) string {
	var del, trash, cmd int
	var frees uint64
	for _, it := range items {
		switch it.Method {
		case MethodDelete:
			del++
		case MethodTrash:
			trash++
		case MethodCommand:
			cmd++
		}
		if it.Frees() {
			frees += it.Size
		}
	}
	var parts []string
	if del > 0 {
		parts = append(parts, fmt.Sprintf("delete %d for good", del))
	}
	if trash > 0 {
		parts = append(parts, fmt.Sprintf("move %d to the Trash", trash))
	}
	if cmd == 1 {
		parts = append(parts, "run 1 command")
	} else if cmd > 1 {
		parts = append(parts, fmt.Sprintf("run %d commands", cmd))
	}
	if len(parts) == 0 {
		return "Nothing is ticked."
	}
	s := parts[0]
	if len(parts) > 1 {
		s = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	s = strings.ToUpper(s[:1]) + s[1:] + "?"
	if frees > 0 {
		s += " Frees " + HumanBytes(frees) + "."
	}
	return s
}

// TotalSize adds up the items' sizes.
func TotalSize(items []CleanItem) uint64 {
	var n uint64
	for _, it := range items {
		n += it.Size
	}
	return n
}

// RunningNames is the set of lowercase process names, used to mark items busy. Process names
// rather than app names are used because Simulator runs from inside Xcode.app. A process from
// inside an app bundle also adds the bundle's BundleKey, for the Apps screen.
func RunningNames(procs []Process) map[string]bool {
	names := map[string]bool{}
	for _, p := range procs {
		names[strings.ToLower(p.Name)] = true
		if a := AppPath(p.Path); a != "" {
			names[BundleKey(a)] = true
		}
	}
	return names
}

// BundleKey is the key in RunningNames that says a process from inside an app bundle with this
// file name runs. It goes by the bundle's name, not its whole path, so a process that macOS shows
// under another path for the same app (a translocated copy, or /System/Volumes/Data) still counts.
func BundleKey(bundle string) string { return "bundle:" + foldKey(filepath.Base(bundle)) }
