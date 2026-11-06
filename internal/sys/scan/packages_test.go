//go:build darwin

package scan

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func TestFindPackages(t *testing.T) {
	home := t.TempDir()
	put(t, home, ".npm/_cacache/index-v5/x", 11<<20)
	put(t, home, ".gradle/caches/modules-2/x", 5000)
	put(t, home, ".gradle/wrapper/dists/gradle-8/x", 5000)
	put(t, home, ".cache/huggingface/models/x", 11<<20)
	put(t, home, ".cache/a/x", 100)
	put(t, home, ".cache/b/x", 100)
	gocache := filepath.Join(home, "Library/Caches/go-build")
	put(t, home, "Library/Caches/go-build/00/x", 5000)
	run := &fakeRun{out: map[string]string{"go env GOCACHE": gocache + "\n", "go env GOMODCACHE": filepath.Join(home, "go/pkg/mod") + "\n"}}
	r := findPackages(context.Background(), testEnv(home, run, "go"))
	items := byID(r.Items)
	if npm := items["dev/packages/npm"]; npm.Safe || npm.Size < 11<<20 || npm.Root != filepath.Join(home, ".npm") {
		t.Errorf("npm = %+v", npm)
	}
	if g := items["dev/packages/gradle"]; len(g.Paths) != 2 {
		t.Errorf("gradle = %+v", g)
	}
	if hf := items["dev/packages/dotcache/huggingface"]; hf.Name != "~/.cache/huggingface" || hf.Detail != "may hold large downloads such as AI models" {
		t.Errorf("huggingface = %+v", hf)
	}
	if s := items["dev/packages/dotcache/small"]; s.Name != "2 small tool caches" {
		t.Errorf("small tool caches = %+v (all %v)", s, ids(r.Items))
	}
	gb := items["dev/packages/go-build"]
	if !slices.Equal(gb.Command, []string{"go", "clean", "-cache"}) || gb.Size == 0 {
		t.Errorf("go build cache = %+v", gb)
	}
	if !slices.Equal(gb.SizeOf, []string{gocache}) {
		t.Errorf("go build cache SizeOf = %v, want [%s]", gb.SizeOf, gocache)
	}
	if _, ok := items["dev/packages/go-modules"]; ok {
		t.Error("an empty module cache isn't offered")
	}
	if _, ok := items["dev/packages/homebrew"]; ok {
		t.Error("brew isn't installed in this test")
	}
	for _, it := range r.Items {
		if it.Safe {
			t.Errorf("%s: package caches aren't pre-ticked", it.ID)
		}
	}
	checkPaths(t, home, r.Items, nil)
}
