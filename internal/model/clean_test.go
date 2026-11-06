package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMethodJSON(t *testing.T) {
	for _, m := range []Method{MethodDelete, MethodTrash, MethodCommand} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var back Method
		if err := json.Unmarshal(b, &back); err != nil || back != m {
			t.Errorf("%s: round trip gave %v, %v", b, back, err)
		}
	}
	var m Method
	if err := json.Unmarshal([]byte(`"shred"`), &m); err == nil {
		t.Error("an unknown method should be an error")
	}
}

func TestMethodZeroValueIsUnknown(t *testing.T) {
	var zero Method
	if zero.String() != "unknown" || Method(99).String() != "unknown" {
		t.Errorf("String() = %q, %q", zero.String(), Method(99).String())
	}
	if zero == MethodDelete {
		t.Fatal("the zero Method must not mean delete")
	}
	b, err := json.Marshal(zero)
	if err != nil || string(b) != `"unknown"` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
	back := MethodTrash
	if err := json.Unmarshal(b, &back); err != nil || back != 0 {
		t.Errorf("Unmarshal unknown = %v, %v", back, err)
	}
	if got := (CleanItem{}).How(); got == "delete" {
		t.Errorf("How() for an unknown method = %q", got)
	}
}

func TestCleanItemState(t *testing.T) {
	safe := CleanItem{Safe: true}
	if !safe.PreTicked() || !safe.Tickable() || safe.Reason() != "" {
		t.Errorf("safe item: %+v", safe)
	}
	busy := CleanItem{Safe: true, App: "Xcode", Busy: true}
	if busy.PreTicked() || busy.Tickable() || busy.Reason() != "Quit Xcode first" {
		t.Errorf("busy item: pre=%v tick=%v reason=%q", busy.PreTicked(), busy.Tickable(), busy.Reason())
	}
	blocked := CleanItem{Safe: true, Blocked: "needs Full Disk Access"}
	if blocked.Tickable() || blocked.Reason() != "needs Full Disk Access" {
		t.Errorf("blocked item: %+v", blocked)
	}
	if (CleanItem{Method: MethodTrash}).Frees() || !(CleanItem{Method: MethodCommand}).Frees() {
		t.Error("moving to the Trash frees nothing; commands and deletes do")
	}
	hows := map[string]CleanItem{
		"delete":                  {Method: MethodDelete},
		"to the Trash":            {Method: MethodTrash},
		"docker builder prune -a": {Method: MethodCommand, Command: []string{"docker", "builder", "prune", "-a"}},
	}
	for want, it := range hows {
		if got := it.How(); got != want {
			t.Errorf("How() = %q, want %q", got, want)
		}
	}
}

func TestCheckPath(t *testing.T) {
	home := "/Users/me"
	caches := "/Users/me/Library/Caches"
	projects := []string{"/Users/me/Projects", "/tmp/elsewhere"}
	tests := []struct {
		name, root, path string
		ok               bool
	}{
		{"inside caches", caches, caches + "/Google", true},
		{"deeper inside", caches, caches + "/JetBrains/PhpStorm2025.2", true},
		{"the root itself", caches, caches, false},
		{"outside the root", caches, "/Users/me/Documents/x", false},
		{"dot-dot", caches, caches + "/../Documents", false},
		{"trailing slash", caches, caches + "/x/", false},
		{"relative", caches, "Library/Caches/x", false},
		{"look-alike prefix", caches, "/Users/me/Library/CachesEvil/x", false},
		{"root not allowed", "/Users/me/Documents", "/Users/me/Documents/x", false},
		{"root outside home", "/Library/Caches", "/Library/Caches/x", false},
		{"project root", "/Users/me/Projects", "/Users/me/Projects/app/node_modules", true},
		{"project root outside home", "/tmp/elsewhere", "/tmp/elsewhere/node_modules", false},
		{"trash", "/Users/me/.Trash", "/Users/me/.Trash/old.zip", true},
	}
	for _, tt := range tests {
		err := CheckPath(home, tt.root, tt.path, MethodDelete, projects)
		if (err == nil) != tt.ok {
			t.Errorf("%s: CheckPath(%q, %q) = %v, want ok=%v", tt.name, tt.root, tt.path, err, tt.ok)
		}
	}
}

func TestGroupTick(t *testing.T) {
	items := []CleanItem{{ID: "a"}, {ID: "b"}, {ID: "busy", Busy: true}}
	if s := GroupTick(items, map[string]bool{}); s != TickNone {
		t.Errorf("none ticked = %v", s)
	}
	if s := GroupTick(items, map[string]bool{"a": true}); s != TickSome {
		t.Errorf("one ticked = %v", s)
	}
	if s := GroupTick(items, map[string]bool{"a": true, "b": true}); s != TickAll {
		t.Errorf("all tickable ticked = %v; busy items don't count", s)
	}
	if s := GroupTick([]CleanItem{{ID: "x", Busy: true}}, map[string]bool{"x": true}); s != TickNone {
		t.Errorf("a group with nothing tickable = %v", s)
	}
}

func TestGroupItems(t *testing.T) {
	items := []CleanItem{
		{ID: "c1", Group: "App caches", Name: "small", Size: 1},
		{ID: "c2", Group: "App caches", Name: "big", Size: 9},
		{ID: "x1", Group: "Xcode", Name: "DerivedData", Size: 20},
		{ID: "p1", Group: "Projects", Name: "in use", Size: 50, Order: 1},
		{ID: "p2", Group: "Projects", Name: "stale", Size: 5},
	}
	notes := []Note{{Group: "Docker", Text: "Docker isn't running."}, {Text: "screen note"}}
	errs := map[string]string{"Simulators": "boom"}
	gs := GroupItems(items, notes, errs, false)
	var names []string
	for _, g := range gs {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "Projects,Xcode,App caches,Docker,Simulators" {
		t.Errorf("groups = %s; want biggest first, then empty groups by name", got)
	}
	if gs[0].Items[0].ID != "p2" {
		t.Errorf("Order comes before size: %+v", gs[0].Items)
	}
	if gs[2].Items[0].ID != "c2" || gs[2].Size != 10 {
		t.Errorf("app caches = %+v", gs[2])
	}
	if len(gs[3].Notes) != 1 || gs[4].Err != "boom" {
		t.Errorf("notes and errors: %+v %+v", gs[3], gs[4])
	}
	byName := GroupItems(items, nil, nil, true)
	if byName[0].Name != "App caches" || byName[0].Items[0].Name != "big" {
		t.Errorf("by name: %+v", byName[0])
	}
}

func TestSummary(t *testing.T) {
	gb := uint64(1 << 30)
	d := func(n int) []CleanItem {
		var out []CleanItem
		for range n {
			out = append(out, CleanItem{Method: MethodDelete, Size: gb})
		}
		return out
	}
	all := append(d(7), CleanItem{Method: MethodTrash, Size: gb}, CleanItem{Method: MethodCommand, Size: gb}, CleanItem{Method: MethodCommand, Size: gb})
	tests := map[string][]CleanItem{
		"Delete 7 for good, move 1 to the Trash and run 2 commands? Frees 9.0 GB.": all,
		"Delete 2 for good? Frees 2.0 GB.":                                         d(2),
		"Move 1 to the Trash?":                                                     {{Method: MethodTrash, Size: gb}},
		"Run 1 command? Frees 1.0 GB.":                                             {{Method: MethodCommand, Size: gb}},
		"Nothing is ticked.":                                                       nil,
	}
	for want, items := range tests {
		if got := Summary(items); got != want {
			t.Errorf("Summary = %q, want %q", got, want)
		}
	}
}

func TestHistoryEntryState(t *testing.T) {
	tests := map[string]HistoryEntry{
		"in the Trash · u put back":     {Method: MethodTrash, Result: "done", Trashed: []string{"/x"}},
		"put back":                      {Method: MethodTrash, Result: "done", Trashed: []string{"/x"}, Restored: true},
		"deleted":                       {Method: MethodDelete, Result: "done"},
		"ran docker image prune":        {Method: MethodCommand, Result: "done", Command: []string{"docker", "image", "prune"}},
		"skipped: Quit Xcode first":     {Method: MethodDelete, Result: "skipped", Message: "Quit Xcode first"},
		"failed: permission denied":     {Method: MethodDelete, Result: "failed", Message: "permission denied"},
		"partly deleted: 1 file stayed": {Method: MethodDelete, Result: "partial", Message: "1 file stayed"},
		"in the Trash":                  {Method: MethodTrash, Result: "done"},
	}
	for want, e := range tests {
		if got := e.State(); got != want {
			t.Errorf("State() = %q, want %q", got, want)
		}
	}
	if !(HistoryEntry{Method: MethodTrash, Trashed: []string{"/x"}}).CanRestore() {
		t.Error("a trashed entry can be put back")
	}
	if (HistoryEntry{Method: MethodDelete}).CanRestore() {
		t.Error("a deleted entry can't be put back")
	}
}

func TestTotalAndRunningNames(t *testing.T) {
	if TotalSize([]CleanItem{{Size: 2}, {Size: 3}}) != 5 {
		t.Error("TotalSize")
	}
	names := RunningNames([]Process{{ProcessID: ProcessID{Name: "Xcode"}}, {ProcessID: ProcessID{Name: "phpstorm"}}})
	if !names["xcode"] || !names["phpstorm"] || names["simulator"] {
		t.Errorf("RunningNames = %v", names)
	}
}

func TestCheckPathHomeRoot(t *testing.T) {
	home := "/Users/me"
	tests := []struct {
		name, path string
		method     Method
		ok         bool
	}{
		{"a file in Downloads", "/Users/me/Downloads/trip.mov", MethodTrash, true},
		{"a folder in Projects", "/Users/me/Projects/old", MethodTrash, true},
		{"inside iCloud Drive", "/Users/me/Library/Mobile Documents/com~apple~CloudDocs/a.pdf", MethodTrash, true},
		{"inside the Photos library folder's parent", "/Users/me/Pictures/holiday.jpg", MethodTrash, true},
		{"inside the Music library", "/Users/me/Music/Music/Media/song.m4a", MethodTrash, true},
		{"deleting from the home root", "/Users/me/Downloads/trip.mov", MethodDelete, false},
		{"a command", "/Users/me/Downloads/trip.mov", MethodCommand, false},
		{"no method", "/Users/me/Downloads/trip.mov", 0, false},
		{"the home folder", "/Users/me", MethodTrash, false},
		{"outside home", "/Users/other/x", MethodTrash, false},
		{"look-alike home", "/Users/meEvil/x", MethodTrash, false},
		{"dot-dot", "/Users/me/Downloads/../Documents", MethodTrash, false},
		{"Documents", "/Users/me/Documents", MethodTrash, false},
		{"Documents in other case", "/Users/me/DOCUMENTS", MethodTrash, false},
		{"Documents with a long s", "/Users/me/Document\u017f", MethodTrash, false},
		{"Desktop with a Kelvin sign", "/Users/me/Des\u212atop", MethodTrash, false},
		{"soap's folder in mixed case", "/Users/me/Library/application support/SOAP", MethodTrash, false},
		{"a file next to Documents", "/Users/me/Documents2", MethodTrash, true},
		{"Library", "/Users/me/Library", MethodTrash, false},
		{"the Trash", "/Users/me/.Trash", MethodTrash, false},
		{"Mobile Documents", "/Users/me/Library/Mobile Documents", MethodTrash, false},
		{"iCloud Drive", "/Users/me/Library/Mobile Documents/com~apple~CloudDocs", MethodTrash, false},
		{"soap's folder", "/Users/me/Library/Application Support/soap", MethodTrash, false},
		{"the folder that holds soap's", "/Users/me/Library/Application Support", MethodTrash, false},
		{"the Photos library", "/Users/me/Pictures/Photos Library.photoslibrary", MethodTrash, false},
		{"the Music library", "/Users/me/Music/Music", MethodTrash, false},
	}
	for _, tt := range tests {
		err := CheckPath(home, home, tt.path, tt.method, nil)
		if (err == nil) != tt.ok {
			t.Errorf("%s: CheckPath(%q, %v) = %v, want ok=%v", tt.name, tt.path, tt.method, err, tt.ok)
		}
	}
	if err := CheckPath(home, home, "/Users/me/Documents", MethodTrash, nil); err == nil || err.Error() != "soap never removes ~/Documents" {
		t.Errorf("message = %v", err)
	}
	if err := CheckPath(home, home, "/Users/me/Library/Application Support", MethodTrash, nil); err == nil ||
		err.Error() != "~/Library/Application Support holds ~/Library/Application Support/soap, which soap never removes" {
		t.Errorf("message = %v", err)
	}
	if err := CheckPath(home+"/", home+"/", "/Users/me/Downloads/x", MethodTrash, nil); err == nil {
		t.Error("a home folder that isn't clean is never a root")
	}
}

func TestStampSame(t *testing.T) {
	when := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	file := Stamp{Size: 100, ModTime: when, Inode: 7}
	if !file.Same(Stamp{Size: 100, ModTime: when.In(time.Local), Inode: 7}) {
		t.Error("the same file in another time zone is the same")
	}
	for name, now := range map[string]Stamp{
		"bigger":     {Size: 101, ModTime: when, Inode: 7},
		"touched":    {Size: 100, ModTime: when.Add(time.Second), Inode: 7},
		"replaced":   {Size: 100, ModTime: when, Inode: 8},
		"now a dir":  {Dir: true, Inode: 7},
		"empty stat": {},
	} {
		if file.Same(now) {
			t.Errorf("%s: should count as changed", name)
		}
	}
	dir := Stamp{Dir: true, Inode: 9, ModTime: when}
	if !dir.Same(Stamp{Dir: true, Inode: 9, ModTime: when.Add(time.Hour), Size: 64}) {
		t.Error("a folder whose contents changed is still the same folder")
	}
	if dir.Same(Stamp{Dir: true, Inode: 10}) {
		t.Error("a folder that was replaced has changed")
	}
}

func TestResultJSON(t *testing.T) {
	b, err := json.Marshal(CleanResult{Result: ResultSkipped})
	if err != nil || !strings.Contains(string(b), `"result":"skipped"`) {
		t.Errorf("Marshal = %s, %v", b, err)
	}
	var back HistoryEntry
	if err := json.Unmarshal([]byte(`{"result":"partial"}`), &back); err != nil || back.Result != ResultPartial {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
}

func TestLacksFullDiskAccess(t *testing.T) {
	plain := []Note{{Screen: ScreenJunk, Text: "Trash and iOS backups are hidden: give your terminal Full Disk Access."}}
	if LacksFullDiskAccess(plain) {
		t.Error("the note's text doesn't count, only its code")
	}
	coded := append(plain, Note{Screen: ScreenJunk, Text: "anything", Code: NoteNoFullDiskAccess})
	if !LacksFullDiskAccess(coded) {
		t.Error("the code says Full Disk Access is missing")
	}
}
