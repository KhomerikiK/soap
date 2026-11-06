package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// appItem is the row of the app /Applications/<name>.app.
func appItem(id, name, detail string, size uint64, opened time.Time) model.CleanItem {
	b := "/Applications/" + name + ".app"
	return model.CleanItem{ID: id, Screen: model.ScreenApps, Group: "Apps", Name: name + ".app", Detail: detail, Size: size,
		Method: model.MethodTrash, Paths: []string{b}, Root: "/Applications", Bundle: b, Opened: opened, Stamp: &model.Stamp{Dir: true, Inode: 1}}
}

// leftoverOf is a leftover at home/rel; bundle is "" for a removed app's.
func leftoverOf(prefix, bundle, rel string, size uint64, byName bool) model.CleanItem {
	detail := ""
	if byName {
		detail = "matched by name"
	}
	return model.CleanItem{ID: prefix + rel, Screen: model.ScreenApps, Group: "Apps", Name: "~/" + rel, Detail: detail, Size: size,
		Method: model.MethodTrash, Paths: []string{home + "/" + rel}, Root: home, Bundle: bundle, ByName: byName, Stamp: &model.Stamp{Inode: 2}}
}

// appsFixture scans Notion (never opened), Teams (needs a password), Docker (running) and one
// leftover of a removed app.
func appsFixture() *fakeCleanup {
	a := model.ScreenApps
	notion := appItem("apps/notion.id", "Notion", "never opened, as far as macOS knows", 320<<20, time.Time{})
	teams := appItem("apps/com.microsoft.teams2", "Microsoft Teams", "last opened 6 Nov", 1*gib, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	teams.Blocked = "needs your password"
	docker := appItem("apps/com.docker.docker", "Docker", "last opened 15 Oct", 2*gib, time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	docker.Busy = true
	nb, tb, db := notion.Bundle, teams.Bundle, docker.Bundle
	items := []model.CleanItem{
		notion,
		leftoverOf("apps/notion.id/", nb, "Library/Caches/notion.id", 80<<10, false),
		leftoverOf("apps/notion.id/", nb, "Library/Preferences/notion.id.plist", 4<<10, false),
		leftoverOf("apps/notion.id/", nb, "Library/Application Support/Notion", 3500<<20, true),
		leftoverOf("apps/notion.id/", nb, "Library/LaunchAgents/notion.id.helper.plist", 4<<10, false),
		teams,
		leftoverOf("apps/com.microsoft.teams2/", tb, "Library/Containers/com.microsoft.teams2", 2600<<20, false),
		docker,
		leftoverOf("apps/com.docker.docker/", db, "Library/Caches/com.docker.docker", 100<<20, false),
		leftoverOf("apps/removed/", "", "Library/Caches/com.gone.tool", 1*gib, false),
	}
	items[8].Busy = true // a running app's leftovers are busy too
	return &fakeCleanup{updates: map[model.Screen][]model.ScanUpdate{a: {
		{Screen: a, Finder: "Apps", Items: items, Done: 1, Total: 1, Notes: []model.Note{{Screen: a, Text: "Spotlight is off, so soap can't tell when apps were last opened."}}},
	}}}
}

func openApps(t *testing.T, fc *fakeCleanup) (*harness, *[]string) {
	t.Helper()
	var revealed []string
	h := newHarness(t, portsSnapshot(), fc.deps, func(d *Deps) {
		d.Home = home
		d.Reveal = func(p string) error { revealed = append(revealed, p); return nil }
	})
	h.pump(h.press("8"))
	return h, &revealed
}

func TestAppsListsAppsUnusedFirstAndTicksNothing(t *testing.T) {
	h, _ := openApps(t, appsFixture())
	s := h.screen()
	for _, want := range []string{"8 Apps", "3 apps", "▸ [ ] Notion", "never opened, as far as macOS knows", "▸ [ ] Docker", "Quit Docker first",
		"Microsoft Teams", "needs your password", "▸ [ ] Leftovers of removed apps", "1 item", "Spotlight is off"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	if !(strings.Index(s, "Notion") < strings.Index(s, "Docker") && strings.Index(s, "Docker") < strings.Index(s, "Microsoft Teams") &&
		strings.Index(s, "Microsoft Teams") < strings.Index(s, "Leftovers of removed apps")) {
		t.Errorf("never opened first, then the longest unused, then the removed apps' leftovers:\n%s", s)
	}
	if strings.Contains(s, "notion.id.plist") {
		t.Error("groups start closed")
	}
	for id, on := range h.m.checks[model.ScreenApps].ticked {
		if on {
			t.Errorf("%s is ticked after a scan", id)
		}
	}
}

func TestAppsSpaceTicksAnAppWithItsBundleIDLeftovers(t *testing.T) {
	fc := appsFixture()
	h, _ := openApps(t, fc)
	h.press("space") // on Notion's group
	cs := h.m.checks[model.ScreenApps]
	for id, want := range map[string]bool{
		"apps/notion.id": true, "apps/notion.id/Library/Caches/notion.id": true, "apps/notion.id/Library/Preferences/notion.id.plist": true,
		"apps/notion.id/Library/LaunchAgents/notion.id.helper.plist": true, "apps/notion.id/Library/Application Support/Notion": false,
	} {
		if cs.ticked[id] != want {
			t.Errorf("%s ticked = %v, want %v", id, cs.ticked[id], want)
		}
	}
	h.press("enter")
	s := h.screen()
	for _, want := range []string{"▾ [-] Notion", "[x] app  /Applications/Notion.app", "[x]      ~/Library/Caches/notion.id", "[ ]      ~/Library/Application Support/Notion", "matched by name"} {
		if !strings.Contains(s, want) {
			t.Errorf("open group is missing %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "notion.id.plist") > strings.Index(s, "Application Support/Notion") {
		t.Errorf("name matches come after the bundle-ID matches:\n%s", s)
	}
	h.press("down", "space") // the app's own row unticks the app with its bundle-ID leftovers
	if cs.ticked["apps/notion.id"] || cs.ticked["apps/notion.id/Library/Caches/notion.id"] {
		t.Errorf("ticked = %v", cs.ticked)
	}
	h.press("space")
	h.press("down", "down", "down", "down", "space") // the folder matched by name, the last row
	if !cs.ticked["apps/notion.id/Library/Application Support/Notion"] {
		t.Fatalf("a name match is ticked on its own: %v\n%s", cs.ticked, h.screen())
	}
	h.press("c")
	s = h.screen()
	for _, want := range []string{"Move 5 to the Trash?", "LaunchAgents keep running until you log out, even from the Trash."} {
		if !strings.Contains(s, want) {
			t.Errorf("preview is missing %q:\n%s", want, s)
		}
	}
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 || fc.cleaned[0][0].ID != "apps/notion.id" || len(fc.cleaned[0]) != 5 {
		t.Fatalf("cleaned %+v; the app goes first", fc.cleaned)
	}
	if len(fc.scans) != 2 {
		t.Errorf("Apps scans again after a clean-up (scans: %d)", len(fc.scans))
	}
}

func TestAppsThatNeedAPasswordTickOnlyTheirFiles(t *testing.T) {
	h, revealed := openApps(t, appsFixture())
	h.press("down", "down", "space") // Microsoft Teams
	cs := h.m.checks[model.ScreenApps]
	if cs.ticked["apps/com.microsoft.teams2"] || !cs.ticked["apps/com.microsoft.teams2/Library/Containers/com.microsoft.teams2"] {
		t.Errorf("ticked = %v", cs.ticked)
	}
	if s := h.screen(); !strings.Contains(s, "Microsoft Teams needs your password, so only its files are ticked. f shows it in Finder.") {
		t.Errorf("screen:\n%s", s)
	}
	h.pump(h.press("f"))
	if !slices.Equal(*revealed, []string{"/Applications/Microsoft Teams.app"}) || !strings.Contains(h.screen(), "Showing /Applications/Microsoft Teams.app in Finder.") {
		t.Errorf("revealed %v:\n%s", *revealed, h.screen())
	}
}

func TestAppsBusyAppCantBeTicked(t *testing.T) {
	h, _ := openApps(t, appsFixture())
	h.press("down", "space") // Docker
	if s := h.screen(); !strings.Contains(s, "Quit Docker first.") {
		t.Errorf("screen:\n%s", s)
	}
	for id, on := range h.m.checks[model.ScreenApps].ticked {
		if on {
			t.Errorf("%s is ticked", id)
		}
	}
}

func TestAppsRemovedAppsLeftoversAreTickedOneAtATime(t *testing.T) {
	h, revealed := openApps(t, appsFixture())
	h.press("down", "down", "down", "space")
	if s := h.screen(); !strings.Contains(s, "Tick these one at a time: each one could still belong to an app Spotlight can't see.") {
		t.Errorf("screen:\n%s", s)
	}
	h.press("enter", "down", "space")
	if !h.m.checks[model.ScreenApps].ticked["apps/removed/Library/Caches/com.gone.tool"] {
		t.Errorf("ticked = %v", h.m.checks[model.ScreenApps].ticked)
	}
	h.pump(h.press("f"))
	if !slices.Equal(*revealed, []string{home + "/Library/Caches/com.gone.tool"}) {
		t.Errorf("revealed %v", *revealed)
	}
}

func TestLeavingAppsKeepsItsScan(t *testing.T) {
	fc := appsFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.press("8") // the scan command isn't run, so the scan is still going
	h.press("1")
	if len(fc.scans) != 1 || fc.scans[0].Err() != nil {
		t.Error("leaving Apps must not cancel its scan")
	}
}

func TestAppsPreviewListsTheAppFirstEvenWhenSortedBySize(t *testing.T) {
	fc := appsFixture()
	h, _ := openApps(t, fc)
	h.press("s") // by size: Notion's 3.5 GB leftover is larger than the app
	h.press("space")
	h.press("c")
	s := h.screen()
	app, left := strings.Index(s, "Notion.app"), strings.Index(s, "Library/Caches/notion.id")
	if app < 0 || left < 0 || app > left {
		t.Errorf("the preview lists the app first:\n%s", s)
	}
	h.pump(h.press("y"))
	if len(fc.cleaned) != 1 || fc.cleaned[0][0].ID != "apps/notion.id" {
		t.Errorf("cleaned %+v; the app goes first", fc.cleaned)
	}
}

func TestAppsSharedCopyRowShowsItsDetail(t *testing.T) {
	fc := appsFixture()
	shared := leftoverOf("apps/notion.id/", "/Applications/Notion.app", "Library/Application Support/Notion", 3500<<20, true)
	shared.Detail = "another copy of Notion uses this too"
	for i, it := range fc.updates[model.ScreenApps][0].Items {
		if it.ID == shared.ID {
			fc.updates[model.ScreenApps][0].Items[i] = shared
		}
	}
	h, _ := openApps(t, fc)
	h.press("enter")
	s := h.screen()
	if !strings.Contains(s, "another copy of Notion uses this too") || strings.Contains(s, "matched by name") {
		t.Errorf("screen:\n%s", s)
	}
}
