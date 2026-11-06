//go:build darwin

package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

// infoPlist is an app's Contents/Info.plist with this bundle ID.
func infoPlist(id string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + id + `</string></dict></plist>
`
}

// putApp makes the bundle dir/rel with an Info.plist for id, and teaches run what plutil says
// about it: the ID, or a failure when id is "". It returns the bundle's path.
func putApp(t *testing.T, run *fakeRun, dir, rel, id string) string {
	t.Helper()
	bundle := filepath.Join(dir, rel)
	plist := filepath.Join(bundle, "Contents/Info.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte(infoPlist(id)), 0o644); err != nil {
		t.Fatal(err)
	}
	if id != "" {
		if run.out == nil {
			run.out = map[string]string{}
		}
		run.out["plutil -extract CFBundleIdentifier raw -o - "+plist] = id + "\n"
	}
	return bundle
}

func TestListApps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Applications")
	run := &fakeRun{}
	for _, rel := range []string{"Notion.app", "Utilities/Tool.app", "Deep/A/Near.app", "Deep/A/B/Far.app", ".Hidden.app",
		"Notion.app/Contents/Helpers/Helper.app", "Plugins.bundle/Inner.app"} {
		putApp(t, run, root, rel, "com.example.x")
	}
	put(t, root, "Fake.app", 10) // a file, not a bundle
	if err := os.Symlink(filepath.Join(root, "Notion.app"), filepath.Join(root, "Link.app")); err != nil {
		t.Fatal(err)
	}
	got, err := listApps(root)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, p := range got {
		rels = append(rels, strings.TrimPrefix(p, root+"/"))
	}
	slices.Sort(rels)
	if want := []string{"Deep/A/Near.app", "Notion.app", "Utilities/Tool.app"}; !slices.Equal(rels, want) {
		t.Errorf("apps = %v, want %v", rels, want)
	}
	if _, err := listApps(filepath.Join(root, "missing")); err == nil {
		t.Error("a folder that isn't there is an error the finder can ignore")
	}
}

func TestReadApp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Applications")
	run := &fakeRun{}
	mine := putApp(t, run, root, "Notion.app", "notion.id")
	safari := putApp(t, run, root, "Safari.app", "com.apple.Safari")
	pages := putApp(t, run, root, "Pages.app", "com.apple.iWork.Pages")
	put(t, pages, "Contents/_MASReceipt/receipt", 10)
	noID := putApp(t, run, root, "Broken.app", "")
	ctx, uid := context.Background(), os.Getuid()

	a, err := readApp(ctx, run.run, uid, root, mine)
	if err != nil || a.id != "notion.id" || a.name != "Notion" || a.blocked != "" || a.apple() || a.receipt || a.root != root || !a.stamp.Dir {
		t.Errorf("Notion = %+v, %v", a, err)
	}
	if a, _ := readApp(ctx, run.run, uid, root, safari); !a.apple() {
		t.Errorf("Safari is an Apple app that comes with macOS: %+v", a)
	}
	if a, _ := readApp(ctx, run.run, uid, root, pages); a.apple() || !a.receipt {
		t.Errorf("Pages came from the App Store, so it's listed: %+v", a)
	}
	if a, _ := readApp(ctx, run.run, uid, root, noID); a.blocked != "has no bundle ID soap can read" || a.id != "" {
		t.Errorf("an app whose bundle ID can't be read can't be ticked: %+v", a)
	}
	if a, _ := readApp(ctx, run.run, uid+1, root, mine); a.blocked != "needs your password" {
		t.Errorf("an app someone else owns needs a password: %+v", a)
	}
	locked := filepath.Join(t.TempDir(), "Locked")
	inLocked := putApp(t, run, locked, "Tool.app", "com.example.tool")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if a, _ := readApp(ctx, run.run, uid, locked, inLocked); a.blocked != "needs your password" {
		t.Errorf("an app in a folder you can't write needs a password: %+v", a)
	}
	if _, err := readApp(ctx, run.run, uid, root, filepath.Join(root, "Gone.app")); err == nil {
		t.Error("a missing bundle is an error")
	}
	protect(t, mine)
	if a, _ := readApp(ctx, run.run, uid, root, mine); a.blocked != "needs App Management access" {
		t.Errorf("an app macOS protects from the terminal needs App Management: %+v", a)
	}
}

// protect makes access(2) refuse to let anyone write path, as macOS's App Management does: EPERM,
// not EACCES.
func protect(t *testing.T, path string) {
	t.Helper()
	old := access
	access = func(p string, mode uint32) error {
		if p == path {
			return syscall.EPERM
		}
		return old(p, mode)
	}
	t.Cleanup(func() { access = old })
}

func TestCheckApp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Applications")
	run := &fakeRun{}
	mine := putApp(t, run, root, "Notion.app", "notion.id")
	safari := putApp(t, run, root, "Safari.app", "com.apple.Safari")
	noID := putApp(t, run, root, "Broken.app", "")
	ctx, uid := context.Background(), os.Getuid()
	if err := checkApp(ctx, run.run, uid, mine); err != nil {
		t.Errorf("your own app may go: %v", err)
	}
	for path, want := range map[string]string{
		safari:                          "Safari comes with macOS",
		noID:                            "Broken's bundle ID can't be read",
		filepath.Join(root, "Gone.app"): "no such file",
	} {
		if err := checkApp(ctx, run.run, uid, path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", filepath.Base(path), err, want)
		}
	}
	if err := checkApp(ctx, run.run, uid+1, mine); err == nil || err.Error() != "Notion needs your password; move it to the Trash in Finder" {
		t.Errorf("someone else's app: %v", err)
	}
	protect(t, mine)
	if err := checkApp(ctx, run.run, uid, mine); err == nil || err.Error() != "Notion needs App Management access; move it to the Trash in Finder" {
		t.Errorf("a protected app: %v", err)
	}
}

// TestCheckAppReadsRealInfoPlists runs the real plutil, on temp files only, as CheckApp does in
// production: it reads XML and binary Info.plist files alike.
func TestCheckAppReadsRealInfoPlists(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Applications")
	fake := &fakeRun{}
	xml := putApp(t, fake, root, "Notion.app", "notion.id")
	binary := putApp(t, fake, root, "Safari.app", "com.apple.Safari")
	run := sys.ExecRunner(5 * time.Second)
	if _, err := run(context.Background(), "plutil", "-convert", "binary1", filepath.Join(binary, "Contents/Info.plist")); err != nil {
		t.Fatal(err)
	}
	if err := CheckApp(run)(xml); err != nil {
		t.Errorf("XML Info.plist: %v", err)
	}
	if err := CheckApp(run)(binary); err == nil || err.Error() != "Safari comes with macOS" {
		t.Errorf("binary Info.plist: %v", err)
	}
}

func TestRunMarksAppsBusyByTheirBundle(t *testing.T) {
	bundle := "/Applications/Notion.app"
	finders := []Finder{{Group: AppsGroup, Screen: model.ScreenApps, Find: func(context.Context, Env) Result {
		return Result{Items: []model.CleanItem{
			{ID: "apps/notion.id", Bundle: bundle, Paths: []string{bundle}},
			{ID: "apps/notion.id/Library/Caches/notion.id", Bundle: bundle, Paths: []string{"/Users/me/Library/Caches/notion.id"}},
			{ID: "apps/removed/Library/Caches/com.gone.app", Paths: []string{"/Users/me/Library/Caches/com.gone.app"}},
		}}
	}}}
	running := model.RunningNames([]model.Process{{ProcessID: model.ProcessID{Name: "Notion Helper"},
		Path: bundle + "/Contents/Frameworks/Notion Helper.app/Contents/MacOS/Notion Helper"}})
	u := <-Run(context.Background(), Env{}, finders, running)
	if !u.Items[0].Busy || !u.Items[1].Busy || u.Items[2].Busy {
		t.Errorf("items = %+v", u.Items)
	}
	if r := u.Items[0].Reason(); r != "Quit Notion first" {
		t.Errorf("reason = %q", r)
	}
}

const mdfindApps = "mdfind -attr kMDItemCFBundleIdentifier kMDItemContentType == 'com.apple.application-bundle'"

// appsHome is a fake home with an Applications folder and leftovers in ~/Library. Spotlight knows
// Docker and Safari but not Notion, as it missed Cyberduck on the user's Mac.
func appsHome(t *testing.T) (string, *fakeRun) {
	t.Helper()
	home := t.TempDir()
	run := &fakeRun{out: map[string]string{}}
	apps := filepath.Join(home, "Applications")
	putApp(t, run, apps, "Notion.app", "notion.id")
	put(t, apps, "Notion.app/Contents/MacOS/Notion", 5000)
	putApp(t, run, apps, "Docker.app", "com.docker.docker")
	putApp(t, run, apps, "Safari.app", "com.apple.Safari")
	pages := putApp(t, run, apps, "Utilities/Pages.app", "com.apple.iWork.Pages")
	put(t, pages, "Contents/_MASReceipt/receipt", 100)
	putApp(t, run, apps, "Broken.app", "")
	for rel, size := range map[string]int{
		"Library/Caches/notion.id/data":                  2000,
		"Library/Preferences/notion.id.plist":            100,
		"Library/Application Support/Notion/notion.db":   3000,
		"Library/Caches/com.docker.docker/x":             100,
		"Library/Group Containers/group.com.docker/x":    100,
		"Library/Application Support/com.gone.tool/data": 700,
		"Library/Preferences/com.apple.Something.plist":  100,
		"Library/Application Support/Broken/data":        100,
	} {
		put(t, home, rel, size)
	}
	age(t, filepath.Join(home, "Library/Preferences/notion.id.plist"), 17*day) // Notion was used on 20 Oct
	run.out[mdfindApps] = "/Applications/Safari.app   kMDItemCFBundleIdentifier = com.apple.Safari\n" +
		"/Applications/Docker.app   kMDItemCFBundleIdentifier = com.docker.docker\n"
	listed := []string{"Broken.app", "Docker.app", "Notion.app", "Utilities/Pages.app"} // by path, Safari left out
	for i, rel := range listed {
		listed[i] = filepath.Join(apps, rel)
	}
	run.out["mdls -raw -nullMarker (null) -name kMDItemLastUsedDate "+strings.Join(listed, " ")] =
		"(null)\x002026-11-01 10:00:00 +0000\x00(null)\x002026-09-01 10:00:00 +0000"
	return home, run
}

func TestFindApps(t *testing.T) {
	home, run := appsHome(t)
	apps := filepath.Join(home, "Applications")
	r := findApps(context.Background(), testEnv(home, run, "mdls", "mdfind"))
	if r.Err != nil || len(r.Notes) != 0 {
		t.Fatalf("err %v, notes %v", r.Err, r.Notes)
	}
	items := byID(r.Items)
	notion := items["apps/notion.id"]
	if notion.Detail != "last used about 20 Oct" || notion.Root != apps || notion.Bundle != apps+"/Notion.app" ||
		!notion.IsApp() || notion.Stamp == nil || notion.Size < 5000 || notion.Method != model.MethodTrash || notion.Name != "Notion.app" || notion.Blocked != "" ||
		notion.UnusedDays != 0 {
		t.Errorf("Notion = %+v", notion)
	}
	if d := items["apps/com.docker.docker"]; d.Detail != "last opened 1 Nov" || d.Opened.IsZero() || d.UnusedDays != 0 {
		t.Errorf("Docker = %+v", d)
	}
	if p := items["apps/com.apple.iWork.Pages"]; p.Detail != "not opened in 66 days" || p.Root != apps || p.UnusedDays != 30 {
		t.Errorf("Pages, from the App Store, is listed: %+v", p)
	}
	if b := items["apps/unknown"+apps+"/Broken.app"]; b.Blocked != "has no bundle ID soap can read" || b.Tickable() {
		t.Errorf("Broken = %+v", b)
	}
	if _, ok := items["apps/com.apple.Safari"]; ok {
		t.Error("Safari comes with macOS and is never listed")
	}
	caches := items["apps/notion.id/Library/Caches/notion.id"]
	if caches.Bundle != notion.Bundle || caches.ByName || caches.Root != home || caches.Stamp == nil || caches.Name != "~/Library/Caches/notion.id" {
		t.Errorf("Notion's caches = %+v", caches)
	}
	data := items["apps/notion.id/Library/Application Support/Notion"]
	if !data.ByName || data.Detail != "matched by name" || data.Bundle != notion.Bundle {
		t.Errorf("Notion's data = %+v", data)
	}
	if _, ok := items["apps/notion.id/Library/Preferences/notion.id.plist"]; !ok {
		t.Errorf("Notion's preferences are missing: %v", ids(r.Items))
	}
	if _, ok := items["apps/unknown"+apps+"/Broken.app/Library/Application Support/Broken"]; ok {
		t.Error("an app without a bundle ID has no leftovers")
	}
	var removed []string
	for _, it := range r.Items {
		if it.Bundle == "" {
			removed = append(removed, it.ID)
		}
	}
	if want := []string{"apps/removed/Library/Application Support/com.gone.tool"}; !slices.Equal(removed, want) {
		t.Errorf("removed apps' leftovers = %v, want %v; Notion is installed, Docker's vendor is known, Apple's are never listed", removed, want)
	}
	for _, it := range r.Items {
		if it.PreTicked() || it.Screen != model.ScreenApps || it.Group != AppsGroup {
			t.Errorf("%s: %+v", it.ID, it)
		}
	}
	checkPaths(t, home, r.Items, nil)
	if fs := Finders(model.ScreenApps); len(fs) != 1 || fs[0].Group != AppsGroup {
		t.Errorf("Finders(apps) = %+v", fs)
	}
}

func TestFindAppsWithSpotlightOff(t *testing.T) {
	home, run := appsHome(t)
	check := func(name string, r Result) {
		t.Helper()
		if len(r.Notes) != 1 || r.Notes[0].Text != "Spotlight is off, so soap can't tell when apps were last opened." {
			t.Errorf("%s: notes = %+v", name, r.Notes)
		}
		items := byID(r.Items)
		if n := items["apps/notion.id"]; n.Detail != "last used about 20 Oct" {
			t.Errorf("%s: Notion = %+v", name, n)
		}
		if _, ok := items["apps/notion.id/Library/Caches/notion.id"]; !ok {
			t.Errorf("%s: leftovers by bundle ID don't need Spotlight", name)
		}
		for _, it := range r.Items {
			if strings.HasPrefix(it.ID, "apps/removed/") {
				t.Errorf("%s: without Spotlight nothing counts as a removed app's leftover: %s", name, it.ID)
			}
		}
	}
	check("no mdfind", findApps(context.Background(), testEnv(home, run)))
	for _, c := range run.calls {
		if strings.HasPrefix(c, "codesign") {
			t.Errorf("a scan without Spotlight must not run codesign: %s", c)
		}
	}
	run.out[mdfindApps] = ""
	run.calls = nil
	check("no apps indexed", findApps(context.Background(), testEnv(home, run, "mdls", "mdfind")))
	for _, c := range run.calls {
		if strings.HasPrefix(c, "codesign") {
			t.Errorf("a scan where Spotlight knows no apps must not run codesign: %s", c)
		}
	}
}

func TestFindAppsKnowsNestedHelperApps(t *testing.T) {
	home, run := appsHome(t)
	docker := filepath.Join(home, "Applications/Docker.app")
	for _, rel := range []string{"Contents/MacOS/Docker Desktop.app", "Contents/Library/LoginItems/Login Helper.app", "Contents/Helpers/Tool.app"} {
		id := map[string]string{"Docker Desktop.app": "com.electron.dockerdesktop", "Login Helper.app": "org.helper.login", "Tool.app": ""}[filepath.Base(rel)]
		putApp(t, run, docker, rel, id)
	}
	put(t, home, "Library/Preferences/com.electron.dockerdesktop.plist", 100)
	put(t, home, "Library/Preferences/org.helper.login.plist", 100)
	r := findApps(context.Background(), testEnv(home, run, "mdls", "mdfind"))
	checkPaths(t, home, r.Items, nil)
	for _, it := range r.Items {
		if it.Bundle == "" && (strings.Contains(it.ID, "electron") || strings.Contains(it.ID, "helper")) {
			t.Errorf("a helper app inside an installed app is installed too: %s", it.ID)
		}
	}
	if _, ok := byID(r.Items)["apps/removed/Library/Application Support/com.gone.tool"]; !ok {
		t.Errorf("real removed leftovers are still listed: %v", ids(r.Items))
	}
}

func TestFindAppsWithPasswordAppsAndCopies(t *testing.T) {
	home, run := appsHome(t)
	putApp(t, run, filepath.Join(home, "Applications"), "Old/Notion.app", "notion.id")
	env := testEnv(home, run)
	env.UID = os.Getuid() + 1 // every app belongs to someone else
	res := findApps(context.Background(), env)
	checkPaths(t, home, res.Items, nil)
	items := byID(res.Items)
	if n := items["apps/notion.id"]; n.Blocked != "needs your password" || n.Tickable() {
		t.Errorf("Notion = %+v", n)
	}
	second := items["apps/notion.id@2"]
	if second.Bundle != filepath.Join(home, "Applications/Old/Notion.app") {
		t.Errorf("the second copy = %+v", second)
	}
	caches := items["apps/notion.id/Library/Caches/notion.id"]
	if caches.Bundle != filepath.Join(home, "Applications/Notion.app") || !caches.Tickable() || !caches.ByName ||
		caches.Detail != "another copy of Notion uses this too" {
		t.Errorf("leftovers go with the first copy, unticked with the reason, and can still be ticked: %+v", caches)
	}
}

func TestFindAppsNotesAppManagement(t *testing.T) {
	home, run := appsHome(t)
	protect(t, filepath.Join(home, "Applications/Docker.app"))
	r := findApps(context.Background(), testEnv(home, run))
	if d := byID(r.Items)["apps/com.docker.docker"]; d.Blocked != "needs App Management access" {
		t.Errorf("Docker = %+v", d)
	}
	var texts []string
	for _, n := range r.Notes {
		texts = append(texts, n.Text)
	}
	if !slices.Contains(texts, "To remove apps that need App Management access, allow your terminal in System Settings › Privacy & Security › App Management, or move them to the Trash in Finder.") {
		t.Errorf("notes = %q", texts)
	}
}

func TestFindAppsNeverOffersProtectedPaths(t *testing.T) {
	home, run := appsHome(t)
	putApp(t, run, filepath.Join(home, "Applications"), "soap.app", "io.example.soap")
	put(t, home, "Library/Application Support/soap/history.json", 100)
	r := findApps(context.Background(), testEnv(home, run))
	for _, it := range r.Items {
		if strings.HasSuffix(it.ID, "Library/Application Support/soap") {
			t.Errorf("soap's own folder is protected and must not be offered: %s", it.ID)
		}
	}
	if _, ok := byID(r.Items)["apps/io.example.soap"]; !ok {
		t.Error("the app itself is still listed")
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindAppsWhenLastOpenedFails(t *testing.T) {
	home, run := appsHome(t)
	for k := range run.out {
		if strings.HasPrefix(k, "mdls ") {
			delete(run.out, k)
		}
	}
	r := findApps(context.Background(), testEnv(home, run, "mdls", "mdfind"))
	if r.Err != nil {
		t.Fatalf("err = %v", r.Err)
	}
	if len(r.Notes) != 1 || r.Notes[0].Text != "Spotlight didn't say when apps were last opened." {
		t.Errorf("notes = %+v", r.Notes)
	}
	items := byID(r.Items)
	for id, want := range map[string]string{"apps/notion.id": "last used about 20 Oct", "apps/com.docker.docker": "last opened unknown", "apps/com.apple.iWork.Pages": "last opened unknown"} {
		if d := items[id].Detail; d != want {
			t.Errorf("%s says %q, want %q", id, d, want)
		}
	}
	if _, ok := items["apps/removed/Library/Application Support/com.gone.tool"]; !ok {
		t.Errorf("the removed apps' group is still shown: %v", ids(r.Items))
	}
}

// lastUsedApp is a fake home with one app, com.jgraph.drawio.desktop. spotlight is the date mdls
// gives for it ("" for none); with spotlightOn false, no Spotlight tool is installed.
func lastUsedApp(t *testing.T, spotlight string, spotlightOn bool) (string, Env) {
	t.Helper()
	home := t.TempDir()
	run := &fakeRun{out: map[string]string{}}
	bundle := putApp(t, run, filepath.Join(home, "Applications"), "draw.io.app", "com.jgraph.drawio.desktop")
	if spotlight == "" {
		spotlight = "(null)"
	}
	run.out[mdfindApps] = bundle + "   kMDItemCFBundleIdentifier = com.jgraph.drawio.desktop\n"
	run.out["mdls -raw -nullMarker (null) -name kMDItemLastUsedDate "+bundle] = spotlight
	tools := []string{}
	if spotlightOn {
		tools = []string{"mdls", "mdfind"}
	}
	return home, testEnv(home, run, tools...)
}

// age sets the modification time of path to d before now.
func age(t *testing.T, path string, d time.Duration) time.Time {
	t.Helper()
	when := now.Add(-d)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return when
}

const day = 24 * time.Hour

func drawio(t *testing.T, env Env) model.CleanItem {
	t.Helper()
	r := findApps(context.Background(), env)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	it, ok := byID(r.Items)["apps/com.jgraph.drawio.desktop"]
	if !ok {
		t.Fatalf("draw.io isn't listed: %v", ids(r.Items))
	}
	return it
}

func TestLastUsedFromPreferences(t *testing.T) {
	home, env := lastUsedApp(t, "", true)
	when := age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 10*day)
	it := drawio(t, env)
	if it.Detail != "last used about 27 Oct" || !it.Opened.Equal(when) || it.UnusedDays != 0 {
		t.Errorf("recent: %+v", it)
	}

	home, env = lastUsedApp(t, "", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 65*day)
	it = drawio(t, env)
	if it.Detail != "not used in about 65 days" || it.UnusedDays != 30 {
		t.Errorf("old: %+v", it)
	}
}

func TestLastUsedNewestOfSpotlightAndFiles(t *testing.T) {
	// Spotlight older than the plist: the plist wins.
	home, env := lastUsedApp(t, "2026-10-01 10:00:00 +0000", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 10*day)
	if it := drawio(t, env); it.Detail != "last used about 27 Oct" {
		t.Errorf("plist newer: %+v", it)
	}
	// Spotlight newer than the plist: Spotlight wins.
	home, env = lastUsedApp(t, "2026-11-01 10:00:00 +0000", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 10*day)
	if it := drawio(t, env); it.Detail != "last opened 1 Nov" || it.UnusedDays != 0 {
		t.Errorf("Spotlight newer: %+v", it)
	}
}

func TestLastUsedFromContainerAndSavedState(t *testing.T) {
	// A sandboxed app, with the bundle ID in another case, has only a container plist.
	home, env := lastUsedApp(t, "", true)
	age(t, put(t, home, "Library/Containers/com.JGraph.drawio.desktop/Data/Library/Preferences/com.JGraph.drawio.desktop.plist", 10), 12*day)
	if it := drawio(t, env); it.Detail != "last used about 25 Oct" {
		t.Errorf("container: %+v", it)
	}

	// Saved state: the newest of the folder and the files directly in it; deeper files don't count.
	home, env = lastUsedApp(t, "", true)
	dir := filepath.Join(home, "Library/Saved Application State/com.jgraph.drawio.desktop.savedState")
	age(t, put(t, dir, "windows.plist", 10), 20*day)
	age(t, put(t, dir, "sub/deep.data", 10), 1*day)
	age(t, filepath.Join(dir, "sub"), 1*day)
	age(t, dir, 15*day)
	if it := drawio(t, env); it.Detail != "last used about 22 Oct" {
		t.Errorf("saved state: %+v", it)
	}
}

func TestLastUsedNeverOpenedWithoutDates(t *testing.T) {
	home, env := lastUsedApp(t, "", true)
	// Updaters write here without the user opening the app.
	for _, rel := range []string{
		"Library/Caches/com.jgraph.drawio.desktop/x",
		"Library/Application Support/com.jgraph.drawio.desktop/x",
		"Library/HTTPStorages/com.jgraph.drawio.desktop/x",
		"Library/Logs/com.jgraph.drawio.desktop/x",
	} {
		age(t, put(t, home, rel, 10), 2*day)
	}
	it := drawio(t, env)
	if it.Detail != "never opened, as far as macOS knows" || !it.Opened.IsZero() || it.UnusedDays != 0 {
		t.Errorf("no dates: %+v", it)
	}
}

func TestLastUsedWhenSpotlightIsOff(t *testing.T) {
	home, env := lastUsedApp(t, "", false)
	if it := drawio(t, env); it.Detail != "last opened unknown" {
		t.Errorf("no file date: %+v", it)
	}
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 10*day)
	if it := drawio(t, env); it.Detail != "last used about 27 Oct" || it.UnusedDays != 0 {
		t.Errorf("file date: %+v", it)
	}
}

func TestLastUsedWhenSpotlightAnswerDoesNotFit(t *testing.T) {
	home, env := lastUsedApp(t, "2026-11-01 10:00:00 +0000\x002026-11-01 10:00:00 +0000", true) // two values for one app
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 10*day)
	if it := drawio(t, env); it.Detail != "last used about 27 Oct" {
		t.Errorf("file date: %+v", it)
	}
}

func TestLastUsedIgnoresSymlinks(t *testing.T) {
	home, env := lastUsedApp(t, "", true)
	target := put(t, home, "elsewhere/real.plist", 10)
	age(t, target, 1*day)
	prefs := filepath.Join(home, "Library/Preferences")
	if err := os.MkdirAll(prefs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(prefs, "com.jgraph.drawio.desktop.plist")); err != nil {
		t.Fatal(err)
	}
	// A symlink inside a saved state folder doesn't count either.
	dir := filepath.Join(home, "Library/Saved Application State/com.jgraph.drawio.desktop.savedState")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	age(t, dir, 40*day)
	it := drawio(t, env)
	if it.Detail != "not used in about 40 days" || it.UnusedDays != 30 {
		t.Errorf("symlinks: %+v", it)
	}
}

func TestLastUsedIgnoresFutureFileDates(t *testing.T) {
	home, env := lastUsedApp(t, "", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), -30*day)
	if it := drawio(t, env); it.Detail != "never opened, as far as macOS knows" || !it.Opened.IsZero() {
		t.Errorf("future date, no Spotlight: %+v", it)
	}
	home, env = lastUsedApp(t, "2026-11-01 10:00:00 +0000", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), -30*day)
	if it := drawio(t, env); it.Detail != "last opened 1 Nov" {
		t.Errorf("future date, Spotlight: %+v", it)
	}
	home, env = lastUsedApp(t, "", true)
	age(t, put(t, home, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), -10*time.Minute)
	if it := drawio(t, env); it.Detail != "last used about 6 Nov" || it.Opened.After(now) {
		t.Errorf("a slightly fast clock is capped at now: %+v", it)
	}
}

func TestLastUsedIgnoresSymlinkedContainerFolders(t *testing.T) {
	for _, link := range []string{"Data", "Data/Library/Preferences"} {
		home, env := lastUsedApp(t, "", true)
		real := filepath.Join(home, "elsewhere")
		age(t, put(t, real, "com.jgraph.drawio.desktop.plist", 10), 1*day)
		c := filepath.Join(home, "Library/Containers/com.jgraph.drawio.desktop")
		path := filepath.Join(c, link)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		target := real
		if link == "Data" { // Data -> a folder that holds Library/Preferences/<id>.plist
			target = filepath.Join(home, "fakedata")
			age(t, put(t, target, "Library/Preferences/com.jgraph.drawio.desktop.plist", 10), 1*day)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if it := drawio(t, env); it.Detail != "never opened, as far as macOS knows" {
			t.Errorf("symlinked %s: %+v", link, it)
		}
	}
}
