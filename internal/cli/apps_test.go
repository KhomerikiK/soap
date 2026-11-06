package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// appItems are Notion with its leftovers, Microsoft Teams (it needs a password), Docker (it runs)
// and one leftover of a removed app, in /Users/me.
func appItems() []model.CleanItem {
	a := model.ScreenApps
	app := func(id, name, detail string, size uint64, opened time.Time) model.CleanItem {
		b := "/Applications/" + name + ".app"
		return model.CleanItem{ID: id, Screen: a, Group: "Apps", Name: name + ".app", Detail: detail, Size: size, Method: model.MethodTrash,
			Paths: []string{b}, Root: "/Applications", Bundle: b, Opened: opened}
	}
	left := func(prefix, bundle, rel string, size uint64, byName bool) model.CleanItem {
		return model.CleanItem{ID: prefix + rel, Screen: a, Group: "Apps", Name: "~/" + rel, Size: size, Method: model.MethodTrash,
			Paths: []string{fakeHome + "/" + rel}, Root: fakeHome, Bundle: bundle, ByName: byName}
	}
	notion := app("apps/notion.id", "Notion", "never opened, as far as macOS knows", 320<<20, time.Time{})
	nameMatch := left("apps/notion.id/", notion.Bundle, "Library/Application Support/Notion", 3500<<20, true)
	nameMatch.Detail = "matched by name" // as the finder sets it
	shared := left("apps/notion.id/", notion.Bundle, "Library/Preferences/notion.id.plist", 1<<10, true)
	shared.Detail = "another copy of Notion uses this too"
	teams := app("apps/com.microsoft.teams2", "Microsoft Teams", "last opened 6 Nov", 1<<30, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	teams.Blocked = "needs your password"
	docker := app("apps/com.docker.docker", "Docker", "last opened 15 Oct", 2<<30, time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC))
	docker.Busy = true
	dockerCache := left("apps/com.docker.docker/", docker.Bundle, "Library/Caches/com.docker.docker", 100<<20, false)
	dockerCache.Busy = true
	return []model.CleanItem{
		notion,
		left("apps/notion.id/", notion.Bundle, "Library/Caches/notion.id", 80<<10, false),
		left("apps/notion.id/", notion.Bundle, "Library/LaunchAgents/notion.id.helper.plist", 4<<10, false),
		nameMatch,
		shared,
		teams,
		left("apps/com.microsoft.teams2/", teams.Bundle, "Library/Containers/com.microsoft.teams2", 2600<<20, false),
		docker, dockerCache,
		left("apps/removed/", "", "Library/Caches/com.gone.tool", 1<<30, false),
	}
}

func newAppsEnv(t *testing.T) (*env, *cleanupFake) {
	t.Helper()
	e, f := newCleanupEnv(t, "")
	e.d.Home = fakeHome
	f.items = append(f.items, appItems()...)
	f.notes = []model.Note{{Screen: model.ScreenApps, Text: "Spotlight is off, so soap can't tell when apps were last opened."}}
	return e, f
}

func TestAppsListsAppsWithTheirLeftovers(t *testing.T) {
	e, _ := newAppsEnv(t)
	if code := e.run("apps"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	for _, want := range []string{
		"Notion · never opened, as far as macOS knows · 3.7 GB", "app       320 MB   /Applications/Notion.app", "apps/notion.id\n",
		"with app", "~/Library/Caches/notion.id", "another copy of Notion uses this too", "apps/notion.id/Library/Preferences/notion.id.plist", "apps/notion.id/Library/Caches/notion.id", "by name", "apps/notion.id/Library/Application Support/Notion",
		"Docker · last opened 15 Oct · Quit Docker first", "blocked", "Microsoft Teams · last opened 6 Nov · needs your password",
		"Leftovers of removed apps · 1 item · 1.0 GB", "apps/removed/Library/Caches/com.gone.tool", "3 apps · found 10.4 GB.", "Spotlight is off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	foundShared := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "apps/notion.id/Library/Application Support/Notion") &&
			(!strings.Contains(line, "by name") || strings.Contains(line, "shared")) {
			t.Errorf("a real name match must say by name, not shared: %q", line)
		}
		if strings.Contains(line, "apps/notion.id/Library/Preferences/notion.id.plist") {
			foundShared = true
			if !strings.Contains(line, "shared") || !strings.Contains(line, "another copy of Notion uses this too") || strings.Contains(line, "by name") {
				t.Errorf("the shared row = %q", line)
			}
		}
	}
	if !foundShared {
		t.Error("the shared row is missing")
	}
	if strings.Contains(out, "PhpStorm") {
		t.Error("apps lists only Apps items")
	}
	if strings.Index(out, "Notion") > strings.Index(out, "Docker") || strings.Index(out, "Microsoft Teams") > strings.Index(out, "Leftovers of removed apps") {
		t.Errorf("order:\n%s", out)
	}
	e.out.Reset()
	if code := e.run("apps", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Apps []struct {
			Name      string            `json:"name"`
			App       model.CleanItem   `json:"app"`
			Leftovers []model.CleanItem `json:"leftovers"`
		} `json:"apps"`
		Removed []model.CleanItem `json:"removed"`
		Notes   []model.Note      `json:"notes"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if len(got.Apps) != 3 || got.Apps[0].App.ID != "apps/notion.id" || len(got.Apps[0].Leftovers) != 4 || !got.Apps[0].Leftovers[2].ByName ||
		got.Apps[0].Leftovers[3].Detail != "another copy of Notion uses this too" ||
		len(got.Removed) != 1 || len(got.Notes) != 1 {
		t.Errorf("json = %+v", got)
	}
}

func TestCleanAnAppTakesItsBundleIDLeftovers(t *testing.T) {
	e, f := newAppsEnv(t)
	var scanned []model.Screen
	inner := e.d.Scan
	e.d.Scan = func(ctx context.Context, s model.Screen) <-chan model.ScanUpdate {
		scanned = append(scanned, s)
		return inner(ctx, s)
	}
	if code := e.run("clean", "apps/notion.id/Library/Caches/notion.id", "apps/notion.id", "--dry-run"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	out := e.out.String()
	for _, want := range []string{"Move 3 to the Trash?", "Notion.app", "~/Library/LaunchAgents/notion.id.helper.plist", "LaunchAgents keep running until you log out, even from the Trash."} {
		if !strings.Contains(out, want) {
			t.Errorf("preview is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Application Support/Notion") || strings.Contains(out, "Preferences/notion.id.plist") {
		t.Errorf("a name match never comes with the app:\n%s", out)
	}
	if !slices.Equal(scanned, []model.Screen{model.ScreenApps}) {
		t.Errorf("scanned %v", scanned)
	}
	if code := e.run("clean", "apps/notion.id/Library/Caches/notion.id", "apps/notion.id", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	want := []string{"apps/notion.id", "apps/notion.id/Library/Caches/notion.id", "apps/notion.id/Library/LaunchAgents/notion.id.helper.plist"}
	if got := cleanedIDs(f); !slices.Equal(got, want) {
		t.Errorf("cleaned %v, want %v; the app goes first", got, want)
	}
}

func TestCleanRefusesAppsThatNeedAPasswordOrRun(t *testing.T) {
	for id, why := range map[string]string{
		"apps/com.microsoft.teams2": "apps/com.microsoft.teams2 can't be cleaned: needs your password.",
		"apps/com.docker.docker":    "apps/com.docker.docker can't be cleaned: Quit Docker first.",
	} {
		e, f := newAppsEnv(t)
		if code := e.run("clean", id, "-y"); code != 2 || len(f.cleaned) != 0 || !strings.Contains(e.errOut.String(), why) {
			t.Errorf("%s: exit %d, cleaned %v, %q", id, code, f.cleaned, e.errOut.String())
		}
	}
	e, f := newAppsEnv(t)
	if code := e.run("clean", "apps/com.microsoft.teams2/Library/Containers/com.microsoft.teams2", "-y"); code != 0 {
		t.Fatalf("a leftover of an app that needs a password: exit %d: %s", code, e.errOut.String())
	}
	if got := cleanedIDs(f); !slices.Equal(got, []string{"apps/com.microsoft.teams2/Library/Containers/com.microsoft.teams2"}) {
		t.Errorf("cleaned %v", got)
	}
}

func TestCleanRefusesAnAppWithADiskFolderCoveringItsLeftover(t *testing.T) {
	e, f := newAppsEnv(t)
	diskIDResolver(e)
	if code := e.run("clean", "disk/Library/Caches", "apps/notion.id", "-y"); code != 2 || len(f.cleaned) != 0 ||
		!strings.Contains(e.errOut.String(), "apps/notion.id is inside disk/Library/Caches, which is already in this clean-up.") {
		t.Errorf("exit %d, cleaned %v, %q", code, f.cleaned, e.errOut.String())
	}
}
