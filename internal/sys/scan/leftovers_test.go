//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mkdirs makes folders in home.
func mkdirs(t *testing.T, home string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadLibrary(t *testing.T) {
	home := t.TempDir()
	run := &fakeRun{out: map[string]string{}}
	lib := "Library/"
	mkdirs(t, home, lib+"Caches/notion.id", lib+"Caches/.hidden", lib+"Containers/ch.sudo.cyberduck",
		lib+"Containers/0A1B2C3D-0000-4000-8000-00000000AAAA", lib+"Containers/0A1B2C3D-0000-4000-8000-00000000BBBB", lib+"Cookies")
	put(t, home, lib+"Preferences/notion.id.plist", 100)
	put(t, home, lib+"Saved Application State/notion.id.savedState/data", 100)
	put(t, home, lib+"HTTPStorages/ch.sudo.cyberduck.binarycookies", 100)
	agent := put(t, home, lib+"LaunchAgents/com.example.agent.plist", 100)
	put(t, home, lib+"LaunchAgents/com.example.plain.plist", 100) // its label can't be read, so its name counts
	run.out["plutil -extract Label raw -o - "+agent] = "com.Example.Helper\n"
	rec := put(t, home, lib+"Containers/0A1B2C3D-0000-4000-8000-00000000AAAA/"+containerRecord, 100)
	run.out["plutil -extract MCMMetadataIdentifier raw -o - "+rec] = "com.example.Sandboxed\n"
	if err := os.Symlink(filepath.Join(home, "Documents"), filepath.Join(home, lib+"Caches/link")); err != nil {
		t.Fatal(err)
	}
	cookies := filepath.Join(home, lib+"Cookies")
	if err := os.Chmod(cookies, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cookies, 0o755) })

	got := readLibrary(context.Background(), testEnv(home, run))
	ids := map[string]string{}
	for _, e := range got.entries {
		ids[strings.TrimPrefix(e.path, home+"/"+lib)] = e.dir + " " + e.id
	}
	want := map[string]string{
		"Caches/notion.id":                                "Caches notion.id",
		"Containers/ch.sudo.cyberduck":                    "Containers ch.sudo.cyberduck",
		"Containers/0A1B2C3D-0000-4000-8000-00000000AAAA": "Containers com.example.sandboxed",
		"Preferences/notion.id.plist":                     "Preferences notion.id",
		"Saved Application State/notion.id.savedState":    "Saved Application State notion.id",
		"HTTPStorages/ch.sudo.cyberduck.binarycookies":    "HTTPStorages ch.sudo.cyberduck",
		"LaunchAgents/com.example.agent.plist":            "LaunchAgents com.example.helper",
		"LaunchAgents/com.example.plain.plist":            "LaunchAgents com.example.plain",
	}
	if len(ids) != len(want) {
		t.Errorf("entries = %v", ids)
	}
	for rel, w := range want {
		if ids[rel] != w {
			t.Errorf("%s = %q, want %q", rel, ids[rel], w)
		}
	}
	if got.uuidHidden != 1 {
		t.Errorf("a container named by a UUID without a readable record is left out and counted: %d", got.uuidHidden)
	}
	if !slices.Equal(got.unreadable, []string{"Cookies"}) {
		t.Errorf("unreadable = %v", got.unreadable)
	}
}

// libraryOf lists home's leftover folders with a runner that answers nothing.
func libraryOf(t *testing.T, home string) library {
	t.Helper()
	return readLibrary(context.Background(), testEnv(home, nil))
}

func TestMatchLeftovers(t *testing.T) {
	home := t.TempDir()
	lib := "Library/"
	mkdirs(t, home, lib+"Caches/notion.id", lib+"Caches/notion.idx", lib+"HTTPStorages/notion.id", lib+"Containers/notion.id",
		lib+"Application Support/Notion/Partitions", lib+"Logs/Notion", lib+"Application Support/Broken",
		lib+"Application Support/Google/AndroidStudio2025.1", lib+"Application Support/Google/Chrome", lib+"Application Support/Google/GoogleUpdater",
		lib+"Caches/Google/Chrome", lib+"Application Support/BraveSoftware/Brave-Browser",
		lib+"Group Containers/G69SCX94XU.ch.sudo.cyberduck", lib+"Group Containers/group.ch.sudo.cyberduckling")
	put(t, home, lib+"Preferences/notion.id.plist", 100)
	put(t, home, lib+"Preferences/com.google.Chrome.plist", 100)
	put(t, home, lib+"Preferences/com.google.Chrome.helper.plist", 100)
	put(t, home, lib+"Saved Application State/notion.id.savedState/x", 100)
	put(t, home, lib+"LaunchAgents/ch.sudo.cyberduck.helper.plist", 100)
	put(t, home, lib+"LaunchAgents/com.google.keystone.agent.plist", 100)
	apps := []app{
		{path: "/Applications/Android Studio.app", name: "Android Studio", id: "com.google.android.studio"},
		{path: "/Applications/Brave Browser.app", name: "Brave Browser", id: "com.brave.Browser"},
		{path: "/Applications/Broken.app", name: "Broken"},
		{path: "/Applications/Cyberduck.app", name: "Cyberduck", id: "ch.sudo.cyberduck"},
		{path: "/Applications/Google Chrome.app", name: "Google Chrome", id: "com.google.Chrome"},
		{path: "/Applications/Notion.app", name: "Notion", id: "notion.id"},
		{path: home + "/Applications/Notion.app", name: "Notion", id: "notion.id"},
	}
	got := map[string]string{}
	for _, l := range matchLeftovers(apps, libraryOf(t, home)) {
		how := "id"
		if l.byName {
			how = l.detail
		}
		got[strings.TrimPrefix(l.path, home+"/"+lib)] = apps[l.app].path + " " + how
	}
	want := map[string]string{
		"Caches/notion.id":                                "/Applications/Notion.app another copy of Notion uses this too",
		"HTTPStorages/notion.id":                          "/Applications/Notion.app another copy of Notion uses this too",
		"Containers/notion.id":                            "/Applications/Notion.app another copy of Notion uses this too",
		"Preferences/notion.id.plist":                     "/Applications/Notion.app another copy of Notion uses this too",
		"Saved Application State/notion.id.savedState":    "/Applications/Notion.app another copy of Notion uses this too",
		"Application Support/Notion":                      "/Applications/Notion.app matched by name",
		"Logs/Notion":                                     "/Applications/Notion.app matched by name",
		"Preferences/com.google.Chrome.plist":             "/Applications/Google Chrome.app id",
		"Application Support/Google/Chrome":               "/Applications/Google Chrome.app matched by name",
		"Caches/Google/Chrome":                            "/Applications/Google Chrome.app matched by name",
		"Application Support/Google/AndroidStudio2025.1":  "/Applications/Android Studio.app matched by name",
		"Application Support/BraveSoftware/Brave-Browser": "/Applications/Brave Browser.app matched by name",
		"Group Containers/G69SCX94XU.ch.sudo.cyberduck":   "/Applications/Cyberduck.app id",
		"LaunchAgents/ch.sudo.cyberduck.helper.plist":     "/Applications/Cyberduck.app id",
	}
	for rel, w := range want {
		if got[rel] != w {
			t.Errorf("%s = %q, want %q", rel, got[rel], w)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("%s shouldn't match: %s", rel, got[rel])
		}
	}
}

func TestRemovedLeftovers(t *testing.T) {
	home := t.TempDir()
	run := &fakeRun{out: map[string]string{}}
	lib := "Library/"
	mkdirs(t, home, lib+"Application Support/com.gone.tool", lib+"Caches/com.google.Keystone", lib+"Caches/notion.id",
		lib+"Group Containers/group.com.gone.tool", lib+"Group Containers/UBF8T346G9.Office", lib+"Group Containers/2BUA8C4S2C.com.agilebits.onepassword",
		lib+"Group Containers/group.com.apple.notes", lib+"Containers/0A1B2C3D-0000-4000-8000-00000000AAAA")
	for _, rel := range []string{"Preferences/com.gone.tool.plist", "Preferences/com.apple.Something.plist", "Preferences/loginwindow.plist",
		"Preferences/homebrew.mxcl.postgresql@14.plist", "Logs/install.log", "LaunchAgents/us.zoom.updater.plist", "Preferences/ch.sudo.cyberduck.plist"} {
		put(t, home, lib+rel, 100)
	}
	rec := put(t, home, lib+"Containers/0A1B2C3D-0000-4000-8000-00000000AAAA/"+containerRecord, 100)
	run.out["plutil -extract MCMMetadataIdentifier raw -o - "+rec] = "com.gone.Sandboxed\n"
	known := map[string]bool{"com.google.chrome": true, "notion.id": true, "us.zoom.xos": true, "com.apple.safari": true}
	taken := map[string]bool{filepath.Join(home, lib+"Preferences/ch.sudo.cyberduck.plist"): true}
	var got []string
	for _, p := range removedLeftovers(context.Background(), testEnv(home, run), readLibrary(context.Background(), testEnv(home, run)), known, taken, nil) {
		got = append(got, strings.TrimPrefix(p, home+"/"+lib))
	}
	slices.Sort(got)
	want := []string{"Application Support/com.gone.tool", "Containers/0A1B2C3D-0000-4000-8000-00000000AAAA",
		"Group Containers/2BUA8C4S2C.com.agilebits.onepassword", "Group Containers/group.com.gone.tool", "Preferences/com.gone.tool.plist"}
	if !slices.Equal(got, want) {
		t.Errorf("removed = %v\nwant      %v", got, want)
	}
}

func TestMatchLeftoversLongestIDWins(t *testing.T) {
	home := t.TempDir()
	lib := "Library/"
	mkdirs(t, home, lib+"Group Containers/group.com.jetbrains.intellij.ce", lib+"Group Containers/group.com.jetbrains.intellij", lib+"Caches/com.jetbrains.intellij.ce")
	put(t, home, lib+"LaunchAgents/com.jetbrains.intellij.ce.plist", 100)
	put(t, home, lib+"LaunchAgents/com.jetbrains.intellij.plist", 100)
	parent := app{path: "/Applications/IntelliJ IDEA.app", name: "IntelliJ IDEA", id: "com.jetbrains.intellij"}
	child := app{path: home + "/Applications/IntelliJ IDEA CE.app", name: "IntelliJ IDEA CE", id: "com.jetbrains.intellij.ce"}
	want := map[string]string{
		"LaunchAgents/com.jetbrains.intellij.ce.plist":     child.path,
		"LaunchAgents/com.jetbrains.intellij.plist":        parent.path,
		"Group Containers/group.com.jetbrains.intellij.ce": child.path,
		"Group Containers/group.com.jetbrains.intellij":    parent.path,
		"Caches/com.jetbrains.intellij.ce":                 child.path,
	}
	for name, apps := range map[string][]app{"parent first": {parent, child}, "child first": {child, parent}} {
		got := map[string]string{}
		for _, l := range matchLeftovers(apps, libraryOf(t, home)) {
			if l.byName {
				t.Errorf("%s: %s shouldn't be unticked: %s", name, l.path, l.detail)
			}
			got[strings.TrimPrefix(l.path, home+"/"+lib)] = apps[l.app].path
		}
		for rel, w := range want {
			if got[rel] != w {
				t.Errorf("%s: %s = %q, want %q", name, rel, got[rel], w)
			}
		}
	}
}

func TestMatchLeftoversSameIDCopies(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, "Library/Caches/notion.id")
	apps := []app{{path: "/Applications/Notion.app", name: "Notion", id: "notion.id"}, {path: home + "/Applications/Notion.app", name: "Notion", id: "Notion.ID"}}
	got := matchLeftovers(apps, libraryOf(t, home))
	if len(got) != 1 || !got[0].byName || got[0].detail != "another copy of Notion uses this too" {
		t.Errorf("leftovers of two copies are never ticked with one: %+v", got)
	}
}

func TestReadLibrarySkipsSymlinkedFolder(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	mkdirs(t, elsewhere, "com.other.app")
	mkdirs(t, home, "Library/Caches/com.real.app")
	if err := os.Symlink(elsewhere, filepath.Join(home, "Library/Logs")); err != nil {
		t.Fatal(err)
	}
	lib := libraryOf(t, home)
	if len(lib.entries) != 1 || lib.entries[0].name != "com.real.app" {
		t.Errorf("entries = %+v", lib.entries)
	}
}

func TestAppGroups(t *testing.T) {
	run := &fakeRun{
		out: map[string]string{"codesign -d --entitlements - --xml /Applications/Excel.app": `<?xml version="1.0"?><plist version="1.0"><dict>` +
			`<key>com.apple.security.application-groups</key><array><string>UBF8T346G9.Office</string><string>UBF8T346G9.ms</string></array></dict></plist>`,
			"codesign -d --entitlements - --xml /Applications/Plain.app": ""},
	}
	apps := []app{{path: "/Applications/Excel.app"}, {path: "/Applications/Plain.app"}, {path: "/Applications/Unsigned.app"}}
	got := appGroups(context.Background(), testEnv(t.TempDir(), run), apps)
	if !slices.Equal(got, []string{"UBF8T346G9.Office", "UBF8T346G9.ms"}) {
		t.Errorf("groups = %v; an app that codesign fails for adds nothing", got)
	}
}

func TestRemovedLeftoversStricter(t *testing.T) {
	home := t.TempDir()
	run := &fakeRun{out: map[string]string{}}
	lib := "Library/"
	mkdirs(t, home, lib+"Group Containers/UBF8T346G9.Office.Excel.WidgetExtension", lib+"Group Containers/UBF8T346G9.Office",
		lib+"Group Containers/group.com.facebook.family", lib+"Group Containers/group.is.workflow.my.app", lib+"Group Containers/group.is.workflow.shortcuts",
		lib+"Group Containers/ABCDE12345.com.apple.something", lib+"Group Containers/group.com.gone.tool", lib+"Group Containers/group.com.facebook.familyx")
	for _, rel := range []string{"Preferences/org.cups.PrintingPrefs.plist", "Preferences/is.workflow.my.app.plist",
		"LaunchAgents/com.gone.gonehelper.plist", "LaunchAgents/com.gone.livehelper.plist", "LaunchAgents/com.gone.unreadable.plist", "LaunchAgents/com.gone.relative.plist"} {
		put(t, home, lib+rel, 100)
	}
	live := put(t, home, "bin/live", 10)
	agents := filepath.Join(home, lib, "LaunchAgents")
	run.out["plutil -extract ProgramArguments.0 raw -o - "+agents+"/com.gone.gonehelper.plist"] = home + "/bin/gone\n"
	run.out["plutil -extract ProgramArguments.0 raw -o - "+agents+"/com.gone.livehelper.plist"] = live + "\n"
	run.out["plutil -extract ProgramArguments.0 raw -o - "+agents+"/com.gone.relative.plist"] = "bin/gone\n"
	run.out["plutil -extract Program raw -o - "+agents+"/com.gone.relative.plist"] = "gone\n"
	known := map[string]bool{}
	groups := []string{"UBF8T346G9.Office", "group.com.facebook.family"}
	env := testEnv(home, run)
	var got []string
	for _, p := range removedLeftovers(context.Background(), env, readLibrary(context.Background(), env), known, map[string]bool{}, groups) {
		got = append(got, strings.TrimPrefix(p, home+"/"+lib))
	}
	slices.Sort(got)
	want := []string{"Group Containers/group.com.facebook.familyx", "Group Containers/group.com.gone.tool", "LaunchAgents/com.gone.gonehelper.plist"}
	if !slices.Equal(got, want) {
		t.Errorf("removed = %v\nwant      %v", got, want)
	}
}
