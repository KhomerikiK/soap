package model

import (
	"strings"
	"testing"
	"time"
)

func TestAppGroups(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	app := func(name string, opened time.Time, size uint64) CleanItem {
		b := "/Applications/" + name + ".app"
		return CleanItem{ID: "apps/" + name, Name: name + ".app", Bundle: b, Paths: []string{b}, Opened: opened, Size: size}
	}
	left := func(bundle, name string, byName bool, size uint64) CleanItem {
		return CleanItem{ID: "apps/x/" + name, Name: name, Bundle: bundle, Paths: []string{"/Users/me/" + name}, ByName: byName, Size: size}
	}
	items := []CleanItem{
		left("/Applications/Notion.app", "Library/Application Support/Notion", true, 3000),
		app("Docker", day(15), 2000),
		left("", "Library/Caches/com.gone.app", false, 50),
		app("Excel", day(2), 100),
		left("/Applications/Notion.app", "Library/Caches/notion.id", false, 80),
		app("Notion", time.Time{}, 320),
		left("/Applications/Notion.app", "Library/Preferences/notion.id.plist", false, 4),
	}
	var names []string
	gs := AppGroups(items, false)
	for _, g := range gs {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "Notion,Excel,Docker,Leftovers of removed apps" {
		t.Errorf("groups = %s; want no date first, then the longest unused, then the removed apps' leftovers", got)
	}
	notion := gs[0]
	if notion.App == nil || notion.App.ID != "apps/Notion" || notion.Size != 3404 || notion.Key != "/Applications/Notion.app" {
		t.Errorf("Notion = %+v", notion)
	}
	var lefts []string
	for _, it := range notion.Leftovers {
		lefts = append(lefts, it.Name)
	}
	if got := strings.Join(lefts, ","); got != "Library/Caches/notion.id,Library/Preferences/notion.id.plist,Library/Application Support/Notion" {
		t.Errorf("leftovers = %s; want bundle-ID matches first, then name matches, each largest first", got)
	}
	if r := gs[3]; r.App != nil || r.Key != "" || len(r.Leftovers) != 1 || r.Size != 50 {
		t.Errorf("removed = %+v", r)
	}
	names = nil
	for _, g := range AppGroups(items, true) {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "Notion,Docker,Excel,Leftovers of removed apps" {
		t.Errorf("by size = %s", got)
	}
}

func TestOpenedText(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		opened time.Time
		known  bool
		want   string
	}{
		{time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), true, "last opened 30 Oct"},
		{time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC), true, "last opened 7 Oct"},
		{time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), true, "not opened in 30 days"},
		{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), true, "not opened in 37 days"},
		{time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC), true, "not opened in 311 days"},
		{time.Time{}, true, "never opened, as far as macOS knows"},
		{time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC), false, "last opened unknown"},
	}
	for _, tt := range tests {
		if got := OpenedText(tt.opened, tt.known, now, 30); got != tt.want {
			t.Errorf("OpenedText(%v, %v) = %q, want %q", tt.opened, tt.known, got, tt.want)
		}
	}
	if got := OpenedText(time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC), true, now, 400); got != "last opened 30 Dec 2025" {
		t.Errorf("a date in another year shows the year: %q", got)
	}
	if got := OpenedText(now.Add(-25*time.Hour), true, now, 1); got != "not opened in 1 day" {
		t.Errorf("one day: %q", got)
	}
	if got := OpenedText(now.Add(-40*24*time.Hour), true, now, 0); got != "not opened in 40 days" {
		t.Errorf("unusedDays 0 falls back to 30: %q", got)
	}
	tbilisi := time.FixedZone("GET", 4*3600)
	late := time.Date(2026, 11, 6, 23, 30, 0, 0, tbilisi) // 19:30 UTC
	early := time.Date(2026, 11, 6, 19, 0, 0, 0, time.UTC)
	if got := OpenedText(early, true, late, 30); got != "last opened 6 Nov" {
		t.Errorf("a date is shown in now's time zone: %q", got)
	}
	if got := OpenedText(time.Date(2026, 11, 5, 21, 0, 0, 0, time.UTC), true, late, 30); got != "last opened 6 Nov" {
		t.Errorf("21:00 UTC on 5 Nov is 6 Nov in Tbilisi: %q", got)
	}
}

func TestUnusedForAgreesWithOpenedText(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		opened time.Time
		known  bool
		days   int
		want   int
	}{
		{time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), true, 30, 30},
		{time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC), true, 30, 0},
		{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), true, 0, 30},
		{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), true, 60, 0},
		{time.Time{}, true, 30, 0},
		{time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), false, 30, 0},
	} {
		got := UnusedFor(tt.opened, tt.known, now, tt.days)
		if got != tt.want {
			t.Errorf("UnusedFor(%v, %v, %d) = %d, want %d", tt.opened, tt.known, tt.days, got, tt.want)
		}
		if text := OpenedText(tt.opened, tt.known, now, tt.days); (got > 0) != strings.HasPrefix(text, "not opened in") {
			t.Errorf("UnusedFor = %d but OpenedText says %q", got, text)
		}
		// The same date from the settings files: known is true for UnusedFor, and the text says "about".
		if tt.opened.IsZero() {
			continue
		}
		got = UnusedFor(tt.opened, true, now, tt.days)
		if text := LastUsedText(tt.opened, true, tt.known, now, tt.days); (got > 0) != strings.HasPrefix(text, "not used in about") {
			t.Errorf("UnusedFor = %d but LastUsedText says %q", got, text)
		}
	}
}

func TestLastUsedText(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		opened    time.Time
		fromFiles bool
		known     bool
		want      string
	}{
		{time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), true, false, "last used about 27 Oct"},
		{time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), true, true, "last used about 27 Oct"},
		{time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC), true, true, "not used in about 311 days"},
		{now.Add(-25 * time.Hour), true, true, "last used about 5 Nov"},
		{time.Date(2025, 12, 30, 12, 0, 0, 0, time.UTC), false, true, "not opened in 311 days"},
		{time.Time{}, true, true, "never opened, as far as macOS knows"},
		{time.Time{}, true, false, "last opened unknown"},
	} {
		if got := LastUsedText(tt.opened, tt.fromFiles, tt.known, now, 30); got != tt.want {
			t.Errorf("LastUsedText(%v, %v, %v) = %q, want %q", tt.opened, tt.fromFiles, tt.known, got, tt.want)
		}
	}
	if got := LastUsedText(now.Add(-25*time.Hour), true, true, now, 1); got != "not used in about 1 day" {
		t.Errorf("one day: %q", got)
	}
	if got := LastUsedText(time.Date(2025, 8, 3, 12, 0, 0, 0, time.UTC), true, false, now, 500); got != "last used about 3 Aug 2025" {
		t.Errorf("another year shows the year: %q", got)
	}
}
