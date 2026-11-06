package model

import (
	"slices"
	"testing"
)

func TestCheckPathApps(t *testing.T) {
	home := "/Users/me"
	tests := []struct {
		name, root, path string
		method           Method
		ok               bool
	}{
		{"an app in /Applications", "/Applications", "/Applications/Notion.app", MethodTrash, true},
		{"an app in a sub-folder", "/Applications", "/Applications/Utilities/Tool.app", MethodTrash, true},
		{"an app in ~/Applications", "/Users/me/Applications", "/Users/me/Applications/zoom.us.app", MethodTrash, true},
		{"an app named in upper case", "/Applications", "/Applications/Old.APP", MethodTrash, true},
		{"deleting an app", "/Applications", "/Applications/Notion.app", MethodDelete, false},
		{"a command", "/Applications", "/Applications/Notion.app", MethodCommand, false},
		{"no method", "/Applications", "/Applications/Notion.app", 0, false},
		{"a file inside an app", "/Applications", "/Applications/Notion.app/Contents/Info.plist", MethodTrash, false},
		{"an app inside an app", "/Applications", "/Applications/Xcode.app/Contents/Developer/Applications/Simulator.app", MethodTrash, false},
		{"the Applications folder", "/Applications", "/Applications", MethodTrash, false},
		{"a folder of apps", "/Applications", "/Applications/Utilities", MethodTrash, false},
		{"a file next to the apps", "/Applications", "/Applications/notes.txt", MethodTrash, false},
		{"a bare .app name", "/Applications", "/Applications/.app", MethodTrash, false},
		{"dot-dot", "/Applications", "/Applications/../Users/me/x.app", MethodTrash, false},
		{"Utilities as the root", "/Applications/Utilities", "/Applications/Utilities/Tool.app", MethodTrash, false},
		{"Apple's system apps", "/System/Applications", "/System/Applications/Mail.app", MethodTrash, false},
		{"another user's apps", "/Users/other/Applications", "/Users/other/Applications/x.app", MethodTrash, false},
		{"an app under /Library", "/Library", "/Library/Foo.app", MethodTrash, false},
		{"an app from ~/Applications under /Applications", "/Applications", "/Users/me/Applications/zoom.us.app", MethodTrash, false},
	}
	for _, tt := range tests {
		err := CheckPath(home, tt.root, tt.path, tt.method, nil)
		if (err == nil) != tt.ok {
			t.Errorf("%s: CheckPath(%q, %q, %v) = %v, want ok=%v", tt.name, tt.root, tt.path, tt.method, err, tt.ok)
		}
	}
	err := CheckPath(home, "/Applications", "/Applications/Notion.app/Contents/Helper.app", MethodTrash, nil)
	if err == nil || err.Error() != "Helper.app is inside Notion.app, which soap moves only as a whole" {
		t.Errorf("message = %v", err)
	}
	for path, want := range map[string]string{
		"/Applications/Utilities/Tool.app":   "/Applications",
		"/Users/me/Applications/zoom.us.app": "/Users/me/Applications",
		"/Users/me/Downloads/Tool.app":       "",
		"/Applications/Notion.app/Contents":  "",
	} {
		if got := AppRoot(home, path); got != want {
			t.Errorf("AppRoot(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRunningNamesHasBundleKeys(t *testing.T) {
	names := RunningNames([]Process{
		{ProcessID: ProcessID{Name: "Notion Helper"}, Path: "/Applications/Notion.app/Contents/Frameworks/Notion Helper.app/Contents/MacOS/Notion Helper"},
		{ProcessID: ProcessID{Name: "node"}, Path: "/usr/local/bin/node"},
	})
	if !names["notion helper"] || !names["node"] {
		t.Errorf("process names are still there: %v", names)
	}
	if !names[BundleKey("/Applications/Notion.app")] || !names[BundleKey("/System/Volumes/Data/Applications/NOTION.app")] {
		t.Errorf("a helper inside Notion.app makes Notion busy, by the bundle's name: %v", names)
	}
	if names[BundleKey("/Applications/Notion Helper.app")] || names[BundleKey("/Applications/node.app")] {
		t.Errorf("only the outermost bundle counts: %v", names)
	}
}

func TestAppItemRules(t *testing.T) {
	bundle := "/Applications/Notion.app"
	app := CleanItem{ID: "apps/notion.id", Bundle: bundle, Paths: []string{bundle}, Busy: true}
	leftover := CleanItem{ID: "apps/notion.id/Library/Caches/notion.id", Bundle: bundle, Paths: []string{"/Users/me/Library/Caches/notion.id"}}
	if !app.IsApp() || leftover.IsApp() || (CleanItem{Paths: []string{bundle}}).IsApp() {
		t.Errorf("IsApp: app %v, leftover %v", app.IsApp(), leftover.IsApp())
	}
	if app.Tickable() || app.Reason() != "Quit Notion first" || app.BusyName() != "Notion" {
		t.Errorf("a busy app: reason %q, busy name %q", app.Reason(), app.BusyName())
	}
	if r := (CleanItem{App: "Xcode", Busy: true}).Reason(); r != "Quit Xcode first" {
		t.Errorf("an item with an App keeps its reason: %q", r)
	}
	var order []string
	for _, it := range AppsFirst([]CleanItem{{ID: "a"}, leftover, app, {ID: "b"}}) {
		order = append(order, it.ID)
	}
	if want := []string{"apps/notion.id", "a", "apps/notion.id/Library/Caches/notion.id", "b"}; !slices.Equal(order, want) {
		t.Errorf("AppsFirst = %v, want %v", order, want)
	}
	for id, want := range map[string]bool{
		"notion.id": true, "com.google.android.studio": true, "us.zoom.xos": true, "com.Ababe.UnzipAnyFile": true,
		"removed": false, "unknown": false, "": false, "com.foo/bar": false, "com foo.bar": false, "com.foo_bar": false,
	} {
		if got := ValidBundleID(id); got != want {
			t.Errorf("ValidBundleID(%q) = %v", id, got)
		}
	}
	for name, want := range map[string]bool{"Notion.app": true, "Old.APP": true, ".app": false, "app": false, "Notion.app.zip": false} {
		if got := IsAppName(name); got != want {
			t.Errorf("IsAppName(%q) = %v", name, got)
		}
	}
	if !InLaunchAgents("/Users/me", "/Users/me/Library/LaunchAgents/com.foo.plist") || InLaunchAgents("/Users/me", "/Users/me/Library/LaunchAgentsX/a.plist") {
		t.Error("InLaunchAgents")
	}
}

func TestAppPathIgnoresCase(t *testing.T) {
	if got := AppPath("/Applications/Old.APP/Contents/MacOS/old"); got != "/Applications/Old.APP" {
		t.Errorf("AppPath = %q", got)
	}
	if got := AppName("/Applications/Old.APP"); got != "Old" {
		t.Errorf("AppName = %q", got)
	}
	if got := AppPath("/usr/bin/application/tool"); got != "" {
		t.Errorf("AppPath of a non-bundle = %q", got)
	}
}

func TestValidBundleIDNeedsEveryPart(t *testing.T) {
	for _, id := range []string{".", "..", "a.", ".a", "a..b"} {
		if ValidBundleID(id) {
			t.Errorf("ValidBundleID(%q) = true", id)
		}
	}
	if !ValidBundleID("notion.id") {
		t.Error("notion.id should be valid")
	}
}

func TestCheckPathRefusesAppLookAlikes(t *testing.T) {
	home := "/Users/me"
	for _, c := range []struct{ root, path string }{
		{"/Applications", "/Applications2/X.app"},
		{"/Applications", "/ApplicationsX.app"},
		{"/Applications", "/Applications/"},
		{"/Applications", "/applications/X.app"},
		{"/Applications", "/Applications/X.APP/Contents/Y.app"},
		{"/Applications/", "/Applications/X.app"},
		{"/applications", "/applications/X.app"},
	} {
		if err := CheckPath(home, c.root, c.path, MethodTrash, nil); err == nil {
			t.Errorf("CheckPath(root %q, %q) passed", c.root, c.path)
		}
	}
}
