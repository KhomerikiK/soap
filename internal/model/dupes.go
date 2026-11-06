package model

import (
	"path/filepath"
	"sort"
	"strings"
)

// KeepCopy picks the copy of a set that k and K leave unticked: the copy outside Downloads and
// Desktop or, when several are outside, the one made first, then the one with the shortest path.
// When every copy is in Downloads or Desktop, the same order picks among all of them. It returns
// an index into copies, or -1 when there are none.
func KeepCopy(home string, copies []Copy) int {
	better := func(a, b Copy) bool {
		if ao, bo := outsideInbox(home, a.Path), outsideInbox(home, b.Path); ao != bo {
			return ao
		}
		if !a.Made.Equal(b.Made) {
			return a.Made.Before(b.Made)
		}
		if len(a.Path) != len(b.Path) {
			return len(a.Path) < len(b.Path)
		}
		return a.Path < b.Path
	}
	best := -1
	for i, c := range copies {
		if best < 0 || better(c, copies[best]) {
			best = i
		}
	}
	return best
}

// outsideInbox reports whether path is outside Downloads and Desktop, where copies pile up.
func outsideInbox(home, path string) bool {
	return !strings.HasPrefix(path, home+"/Downloads/") && !strings.HasPrefix(path, home+"/Desktop/")
}

// SetName is the name a Dupes set goes by: the file name of its kept copy.
func SetName(it CleanItem) string {
	for _, c := range it.Copies {
		if c.Keep {
			return filepath.Base(c.Path)
		}
	}
	if len(it.Paths) > 0 {
		return filepath.Base(it.Paths[0])
	}
	return it.Name
}

// KeepMessage is the refusal for a clean-up that would remove every copy of a set.
func KeepMessage(it CleanItem) string { return "Keep at least one copy of " + SetName(it) + "." }

// WholeSets finds the Dupes sets that items would remove every copy of. It returns the first
// item of each such set, in the order they come in items. soap never cleans those sets.
func WholeSets(items []CleanItem) []CleanItem {
	ticked := map[string]bool{}
	for _, it := range items {
		if it.Set != "" {
			for _, p := range it.Paths {
				ticked[p] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []CleanItem
	for _, it := range items {
		if it.Set == "" || seen[it.Set] {
			continue
		}
		seen[it.Set] = true
		all := true
		for _, c := range it.Copies {
			if !ticked[c.Path] {
				all = false
				break
			}
		}
		if all {
			out = append(out, it)
		}
	}
	return out
}

// DupeSet is one set of identical files, as the Dupes screen and soap dupes show it.
type DupeSet struct {
	ID    string
	Name  string      // the kept copy's file name
	Items []CleanItem // the copies found: the kept one first, then by path
	Size  uint64      // the size of one copy
	Frees uint64      // what removing every copy but the kept one frees once the Trash is emptied
}

// DupeSets groups Dupes items into their sets. The sets that free the most come first, so sets of
// APFS clones, which free almost nothing, come last. byName sorts them by name instead.
func DupeSets(items []CleanItem, byName bool) []DupeSet {
	bySet := map[string]*DupeSet{}
	var order []string
	for _, it := range items {
		if it.Set == "" {
			continue
		}
		s, ok := bySet[it.Set]
		if !ok {
			s = &DupeSet{ID: it.Set}
			bySet[it.Set] = s
			order = append(order, it.Set)
		}
		s.Items = append(s.Items, it)
	}
	out := make([]DupeSet, 0, len(order))
	for _, id := range order {
		s := bySet[id]
		sort.SliceStable(s.Items, func(i, j int) bool {
			a, b := s.Items[i], s.Items[j]
			if a.Keep != b.Keep {
				return a.Keep
			}
			return a.Name < b.Name
		})
		s.Name, s.Size = SetName(s.Items[0]), s.Items[0].Size
		for _, it := range s.Items {
			if !it.Keep {
				s.Frees += it.Unshared
			}
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !byName {
			if a.Frees != b.Frees {
				return a.Frees > b.Frees
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
		}
		if an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name); an != bn {
			return an < bn
		}
		return a.ID < b.ID
	})
	return out
}
