//go:build darwin

package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLoadConfigWritesDefaults(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.ProjectRoots, []string{filepath.Join(home, "Projects")}) || cfg.StaleDays != 30 || cfg.DupesMinMB != 1 || cfg.UnusedDays != 30 {
		t.Errorf("defaults = %+v", cfg)
	}
	b, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("the default config should be written: %v", err)
	}
	var got, want any
	_ = json.Unmarshal(b, &got)
	_ = json.Unmarshal([]byte(`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 1, "unused_days": 30}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default file = %s", b)
	}
	if len(cfg.Skipped) != 0 {
		t.Errorf("skipped = %q", cfg.Skipped)
	}
}

func TestLoadConfigReadsUserSettings(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Library/Application Support/soap/config.json", 0)
	body := `{"project_roots": ["~/Code", "/Volumes/Work/src/"], "stale_days": 0}`
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.ProjectRoots, []string{filepath.Join(home, "Code")}) || cfg.StaleDays != 30 {
		t.Errorf("config = %+v", cfg)
	}
	if want := []string{`Skipped project root "/Volumes/Work/src/": it isn't inside your home folder.`}; !slices.Equal(cfg.Skipped, want) {
		t.Errorf("skipped = %q", cfg.Skipped)
	}
	if err := os.WriteFile(ConfigPath(home), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(home); err == nil {
		t.Error("a broken config should be an error")
	}
}

func TestLoadConfigValidatesRoots(t *testing.T) {
	cases := []struct {
		name    string
		roots   []string // "<home>" is replaced by the temp home
		want    []string
		skipped []string
	}{
		{"relative", []string{"Code"}, nil, []string{`Skipped project root "Code": it isn't a full path.`}},
		{"outside home", []string{"/Volumes/Work/src"}, nil, []string{`Skipped project root "/Volumes/Work/src": it isn't inside your home folder.`}},
		{"bare tilde", []string{"~"}, nil, []string{`Skipped project root "~": it isn't inside your home folder.`}},
		{"home itself", []string{"<home>"}, nil, []string{`Skipped project root "<home>": it isn't inside your home folder.`}},
		{"inner after outer", []string{"~/Projects", "~/Projects/work"}, []string{"<home>/Projects"},
			[]string{`Skipped project root "~/Projects/work": it's inside "~/Projects", which is already scanned.`}},
		{"inner before outer", []string{"~/Projects/work", "~/Projects"}, []string{"<home>/Projects"},
			[]string{`Skipped project root "~/Projects/work": it's inside "~/Projects", which is already scanned.`}},
		{"duplicate", []string{"~/Projects", "~/Projects/"}, []string{"<home>/Projects"},
			[]string{`Skipped project root "~/Projects/": it's inside "~/Projects", which is already scanned.`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			fix := func(in []string) []string {
				var out []string
				for _, s := range in {
					out = append(out, strings.ReplaceAll(s, "<home>", home))
				}
				return out
			}
			body, _ := json.Marshal(map[string]any{"project_roots": fix(c.roots), "stale_days": 30})
			put(t, home, "Library/Application Support/soap/config.json", 0)
			if err := os.WriteFile(ConfigPath(home), body, 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(home)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cfg.ProjectRoots, fix(c.want)) {
				t.Errorf("roots = %q, want %q", cfg.ProjectRoots, fix(c.want))
			}
			if !slices.Equal(cfg.Skipped, fix(c.skipped)) {
				t.Errorf("skipped = %q, want %q", cfg.Skipped, fix(c.skipped))
			}
		})
	}
}

func TestLoadConfigDupesMinMB(t *testing.T) {
	for body, want := range map[string]int{
		`{"project_roots": ["~/Projects"], "stale_days": 30}`:                     1, // written before part 3
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 5}`:  5,
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": -2}`: 1,
	} {
		home := t.TempDir()
		put(t, home, "Library/Application Support/soap/config.json", 0)
		if err := os.WriteFile(ConfigPath(home), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(home)
		if err != nil || cfg.DupesMinMB != want {
			t.Errorf("%s: DupesMinMB = %d, %v; want %d", body, cfg.DupesMinMB, err, want)
		}
	}
}

func TestLoadConfigUnusedDays(t *testing.T) {
	for body, want := range map[string]int{
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 1}`:                    30, // written before part 4
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 1, "unused_days": 90}`: 90,
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 1, "unused_days": 0}`:  30,
		`{"project_roots": ["~/Projects"], "stale_days": 30, "dupes_min_mb": 1, "unused_days": -5}`: 30,
	} {
		home := t.TempDir()
		put(t, home, "Library/Application Support/soap/config.json", 0)
		if err := os.WriteFile(ConfigPath(home), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(home)
		if err != nil || cfg.UnusedDays != want {
			t.Errorf("%s: UnusedDays = %d, %v; want %d", body, cfg.UnusedDays, err, want)
		}
	}
}
