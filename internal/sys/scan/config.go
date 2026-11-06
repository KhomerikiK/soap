//go:build darwin

package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ConfigPath is where soap keeps its settings.
func ConfigPath(home string) string {
	return filepath.Join(home, "Library/Application Support/soap/config.json")
}

// LoadConfig reads the settings, writing the defaults first when there are none. "~/" in a
// project root means the home folder.
func LoadConfig(home string) (Config, error) {
	path := ConfigPath(home)
	cfg := Config{ProjectRoots: []string{"~/Projects"}, StaleDays: 30, DupesMinMB: 1, UnusedDays: 30}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Config{}, err
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return Config{}, err
		}
	case err != nil:
		return Config{}, err
	default:
		if err := json.Unmarshal(b, &cfg); err != nil {
			return Config{}, fmt.Errorf("can't read %s: %w", path, err)
		}
	}
	if cfg.StaleDays <= 0 {
		cfg.StaleDays = 30
	}
	if cfg.DupesMinMB <= 0 {
		cfg.DupesMinMB = 1
	}
	if cfg.UnusedDays <= 0 {
		cfg.UnusedDays = 30
	}
	cfg.ProjectRoots, cfg.Skipped = checkRoots(home, cfg.ProjectRoots)
	return cfg, nil
}

// checkRoots expands and cleans the project roots and keeps those that are safe to scan: a full
// path strictly inside the home folder, and not inside (or equal to) another root. The sentences
// for the dropped roots show them as the user wrote them.
func checkRoots(home string, written []string) (roots, skipped []string) {
	type root struct{ written, path string }
	var valid []root
	for _, w := range written {
		r := w
		if r == "~" {
			r = home
		} else if rest, ok := strings.CutPrefix(r, "~/"); ok {
			r = filepath.Join(home, rest)
		}
		r = filepath.Clean(r)
		switch {
		case !filepath.IsAbs(r):
			skipped = append(skipped, fmt.Sprintf("Skipped project root %q: it isn't a full path.", w))
		case !strings.HasPrefix(r, filepath.Clean(home)+"/"):
			skipped = append(skipped, fmt.Sprintf("Skipped project root %q: it isn't inside your home folder.", w))
		default:
			valid = append(valid, root{w, r})
		}
	}
	for i, r := range valid {
		outer := -1
		for j, o := range valid {
			if j == i {
				continue
			}
			if (o.path == r.path && j < i) || strings.HasPrefix(r.path, o.path+"/") {
				outer = j
				break
			}
		}
		if outer >= 0 {
			skipped = append(skipped, fmt.Sprintf("Skipped project root %q: it's inside %q, which is already scanned.", r.written, valid[outer].written))
			continue
		}
		roots = append(roots, r.path)
	}
	return roots, skipped
}
