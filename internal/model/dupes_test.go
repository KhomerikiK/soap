package model

import (
	"strings"
	"testing"
	"time"
)

func TestKeepCopy(t *testing.T) {
	home := "/h"
	day := func(d int) time.Time { return time.Date(2025, 3, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name   string
		copies []Copy
		want   int
	}{
		{"outside Downloads and Desktop wins", []Copy{
			{Path: "/h/Downloads/trip.mov", Made: day(1)},
			{Path: "/h/Movies/Trips/trip.mov", Made: day(9)},
			{Path: "/h/Desktop/old/trip.mov", Made: day(2)},
		}, 1},
		{"then the one made first", []Copy{
			{Path: "/h/Movies/b.mov", Made: day(5)},
			{Path: "/h/Documents/a.mov", Made: day(3)},
		}, 1},
		{"then the shortest path", []Copy{
			{Path: "/h/Movies/long/name.mov", Made: day(3)},
			{Path: "/h/Movies/n.mov", Made: day(3)},
		}, 1},
		{"all in Downloads or Desktop", []Copy{
			{Path: "/h/Downloads/x.zip", Made: day(4)},
			{Path: "/h/Desktop/x.zip", Made: day(2)},
		}, 1},
		{"Downloads only as a folder name", []Copy{
			{Path: "/h/Downloads/x.zip", Made: day(1)},
			{Path: "/h/Documents/Downloads/x.zip", Made: day(8)},
		}, 1},
		{"none", nil, -1},
	}
	for _, tt := range tests {
		if got := KeepCopy(home, tt.copies); got != tt.want {
			t.Errorf("%s: KeepCopy = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// dupeItems is a set of three copies of trip.mov and a set of two clones of a.zip.
func dupeItems() []CleanItem {
	trip := []Copy{{Path: "/h/Movies/trip.mov", Keep: true}, {Path: "/h/Downloads/trip.mov"}, {Path: "/h/Desktop/trip.mov"}}
	zip := []Copy{{Path: "/h/Documents/a.zip", Keep: true}, {Path: "/h/Downloads/a.zip"}}
	item := func(set, path string, copies []Copy, keep bool, size, unshared uint64) CleanItem {
		return CleanItem{ID: "dupes/" + strings.TrimPrefix(path, "/h/"), Name: HomePath("/h", path), Set: set, Paths: []string{path},
			Copies: copies, Keep: keep, Size: size, Unshared: unshared, Method: MethodTrash, Root: "/h"}
	}
	return []CleanItem{
		item("zip", "/h/Downloads/a.zip", zip, false, 900, 0),
		item("trip", "/h/Downloads/trip.mov", trip, false, 400, 400),
		item("zip", "/h/Documents/a.zip", zip, true, 900, 0),
		item("trip", "/h/Movies/trip.mov", trip, true, 400, 400),
		item("trip", "/h/Desktop/trip.mov", trip, false, 400, 400),
		{ID: "junk/x", Name: "not a copy"},
	}
}

func TestDupeSets(t *testing.T) {
	sets := DupeSets(dupeItems(), false)
	if len(sets) != 2 {
		t.Fatalf("sets = %+v", sets)
	}
	trip, zip := sets[0], sets[1]
	if trip.Name != "trip.mov" || trip.Size != 400 || trip.Frees != 800 || len(trip.Items) != 3 {
		t.Errorf("trip = %+v", trip)
	}
	if !trip.Items[0].Keep || trip.Items[1].Name != "~/Desktop/trip.mov" || trip.Items[2].Name != "~/Downloads/trip.mov" {
		t.Errorf("the kept copy comes first, then by path: %+v", trip.Items)
	}
	if zip.Name != "a.zip" || zip.Frees != 0 {
		t.Errorf("zip = %+v; clones free nothing, so they come last although they're bigger", zip)
	}
	if byName := DupeSets(dupeItems(), true); byName[0].Name != "a.zip" {
		t.Errorf("by name: %+v", byName[0])
	}
}

func TestWholeSets(t *testing.T) {
	items := dupeItems()
	if got := WholeSets([]CleanItem{items[0], items[1], items[4]}); len(got) != 0 {
		t.Errorf("a copy of each set stays, so nothing is refused: %+v", got)
	}
	got := WholeSets([]CleanItem{items[1], items[0], items[2]})
	if len(got) != 1 || got[0].Set != "zip" || KeepMessage(got[0]) != "Keep at least one copy of a.zip." {
		t.Errorf("WholeSets = %+v", got)
	}
	if SetName(CleanItem{Paths: []string{"/h/x.bin"}}) != "x.bin" || SetName(CleanItem{Name: "n"}) != "n" {
		t.Error("SetName without copies")
	}
}
