//go:build darwin

package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// put writes size bytes to home/rel, creating folders, and returns the full path.
func put(t *testing.T, home, rel string, size int) string {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRun answers commands from a table keyed by the whole command line.
type fakeRun struct {
	out   map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	if err, ok := f.errs[line]; ok {
		return nil, err
	}
	if out, ok := f.out[line]; ok {
		return []byte(out), nil
	}
	return nil, &sys.CommandError{Name: name, Err: errors.New("exit status 1"), Stderr: "not expected in this test: " + line}
}

// testEnv is an Env on a fake home folder. Only the named tools count as installed.
func testEnv(home string, run *fakeRun, tools ...string) Env {
	if run == nil {
		run = &fakeRun{}
	}
	have := map[string]bool{}
	for _, tool := range tools {
		have[tool] = true
	}
	return Env{
		Home:  home,
		Run:   run.run,
		Sizer: NewSizer(),
		Now:   func() time.Time { return now },
		LookPath: func(f string) (string, error) {
			if have[f] {
				return "/usr/bin/" + f, nil
			}
			return "", errors.New("not installed")
		},
		Config: Config{ProjectRoots: []string{filepath.Join(home, "Projects")}, StaleDays: 30},
		Apps:   []string{filepath.Join(home, "Applications")},
		UID:    os.Getuid(),
	}
}

func byID(items []model.CleanItem) map[string]model.CleanItem {
	m := map[string]model.CleanItem{}
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

func ids(items []model.CleanItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	sort.Strings(out)
	return out
}

// checkPaths fails the test if the cleaner's path rules would refuse any item path.
func checkPaths(t *testing.T, home string, items []model.CleanItem, projectRoots []string) {
	t.Helper()
	for _, it := range items {
		for _, p := range it.Paths {
			if err := model.CheckPath(home, it.Root, p, it.Method, projectRoots); err != nil {
				t.Errorf("%s: %v", it.ID, err)
			}
		}
	}
}
