//go:build darwin

package scan

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/KhomerikiK/soap/internal/model"
)

func findXcode(ctx context.Context, env Env) Result {
	var items []model.CleanItem
	add := func(it model.CleanItem) {
		it.Screen, it.Group = model.ScreenDev, "Xcode"
		items = append(items, it)
	}
	dd := env.home("Library/Developer/Xcode/DerivedData")
	for _, name := range dirs(dd) {
		project := name
		if i := strings.LastIndex(name, "-"); i > 0 {
			project = name[:i]
		}
		add(model.CleanItem{ID: "dev/xcode/deriveddata/" + name, Name: "DerivedData: " + project, Detail: "rebuilt on the next build",
			Method: model.MethodDelete, Paths: []string{filepath.Join(dd, name)}, Root: dd, Safe: true, App: "Xcode"})
	}
	caches := env.home("Library/Caches")
	add(model.CleanItem{ID: "dev/xcode/caches", Name: "Xcode caches", Detail: "rebuilt automatically",
		Method: model.MethodDelete, Paths: []string{filepath.Join(caches, "com.apple.dt.Xcode")}, Root: caches, Safe: true, App: "Xcode"})
	arch := env.home("Library/Developer/Xcode/Archives")
	for _, day := range dirs(arch) {
		for _, a := range dirs(filepath.Join(arch, day)) {
			if !strings.HasSuffix(a, ".xcarchive") {
				continue
			}
			add(model.CleanItem{ID: "dev/xcode/archives/" + day + "/" + a, Name: "Archive: " + strings.TrimSuffix(a, ".xcarchive"),
				Detail: "from " + day + " · goes to the Trash", Method: model.MethodTrash, Paths: []string{filepath.Join(arch, day, a)}, Root: arch})
		}
	}
	for _, kind := range []string{"iOS", "watchOS", "tvOS"} {
		ds := env.home("Library/Developer/Xcode/" + kind + " DeviceSupport")
		for _, v := range dirs(ds) {
			add(model.CleanItem{ID: "dev/xcode/devicesupport/" + kind + "/" + v, Name: kind + " DeviceSupport " + v,
				Detail: "rebuilt when a device with this version connects", Method: model.MethodDelete, Paths: []string{filepath.Join(ds, v)}, Root: ds})
		}
	}
	return Result{Items: measure(ctx, env.Sizer, items)}
}
