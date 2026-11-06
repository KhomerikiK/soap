//go:build darwin

package scan

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/KhomerikiK/soap/internal/model"
)

func findPackages(ctx context.Context, env Env) Result {
	caches := env.home("Library/Caches")
	again := "re-downloaded when needed"
	items := []model.CleanItem{
		pkg("npm", "npm cache", again, env.home(".npm"), env.home(".npm/_cacache")),
		pkg("yarn", "Yarn cache", again, caches, filepath.Join(caches, "Yarn")),
		pkg("composer", "Composer cache", again, caches, filepath.Join(caches, "composer")),
		pkg("composer-home", "Composer cache (~/.composer)", again, env.home(".composer"), env.home(".composer/cache")),
		pkg("gradle", "Gradle caches", again, env.home(".gradle"), env.home(".gradle/caches"), env.home(".gradle/wrapper/dists")),
		pkg("maven", "Maven repository", again, env.home(".m2"), env.home(".m2/repository")),
		pkg("cocoapods", "CocoaPods cache", again, caches, filepath.Join(caches, "CocoaPods")),
		pkg("pip", "pip cache", again, caches, filepath.Join(caches, "pip")),
	}
	var tools []model.CleanItem
	for _, name := range dirs(env.home(".cache")) {
		tools = append(tools, pkg("dotcache/"+name, "~/.cache/"+name, "may hold large downloads such as AI models",
			env.home(".cache"), env.home(".cache/"+name)))
	}
	items = measure(ctx, env.Sizer, items)
	items = append(items, mergeSmall(measure(ctx, env.Sizer, tools), "dev/packages/dotcache/small", "small tool caches")...)
	return Result{Items: append(items, toolCaches(ctx, env)...)}
}

func pkg(id, name, detail, root string, paths ...string) model.CleanItem {
	return model.CleanItem{ID: "dev/packages/" + id, Screen: model.ScreenDev, Group: "Package caches", Name: name,
		Detail: detail, Method: model.MethodDelete, Paths: paths, Root: root}
}

// toolCaches are caches that only their own tool should clear. Go marks its module cache
// read-only, and pnpm and Homebrew know what's still in use.
func toolCaches(ctx context.Context, env Env) []model.CleanItem {
	type toolCache struct {
		tool, id, name, detail string
		where, clean           []string
	}
	list := []toolCache{
		{"go", "go-build", "Go build cache", "rebuilt on the next build", []string{"go", "env", "GOCACHE"}, []string{"go", "clean", "-cache"}},
		{"go", "go-modules", "Go module cache", "re-downloaded when needed", []string{"go", "env", "GOMODCACHE"}, []string{"go", "clean", "-modcache"}},
		{"pnpm", "pnpm-store", "pnpm store", "removes only packages no project uses", []string{"pnpm", "store", "path"}, []string{"pnpm", "store", "prune"}},
		{"brew", "homebrew", "Homebrew cache and old versions", "brew cleanup", []string{"brew", "--cache"}, []string{"brew", "cleanup", "--prune=all"}},
	}
	var items []model.CleanItem
	for _, tc := range list {
		if !env.hasTool(tc.tool) {
			continue
		}
		out, err := env.Run(ctx, tc.where[0], tc.where[1:]...)
		if err != nil {
			continue
		}
		path := strings.TrimSpace(string(out))
		if !filepath.IsAbs(path) {
			continue
		}
		u := env.Sizer.Measure(ctx, path)
		if u.Bytes == 0 {
			continue
		}
		items = append(items, model.CleanItem{ID: "dev/packages/" + tc.id, Screen: model.ScreenDev, Group: "Package caches", Name: tc.name,
			Detail: tc.detail, Size: u.Bytes, SizeMin: u.Partial, Method: model.MethodCommand, Command: tc.clean, SizeOf: []string{path}})
	}
	return items
}
