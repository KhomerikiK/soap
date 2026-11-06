//go:build darwin

package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

type simDevice struct {
	Name         string `json:"name"`
	UDID         string `json:"udid"`
	State        string `json:"state"`
	IsAvailable  bool   `json:"isAvailable"`
	LastBootedAt string `json:"lastBootedAt"`
	DataPathSize uint64 `json:"dataPathSize"`
}

type simRuntime struct {
	Identifier        string `json:"identifier"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	SizeBytes         uint64 `json:"sizeBytes"`
	Deletable         bool   `json:"deletable"`
	LastUsedAt        string `json:"lastUsedAt"`
}

func findSimulators(ctx context.Context, env Env) Result {
	if !env.hasTool("xcrun") {
		return Result{}
	}
	out, err := env.Run(ctx, "xcrun", "simctl", "list", "-j", "devices")
	if err != nil {
		if strings.Contains(err.Error(), "unable to find utility") {
			return Result{} // command-line tools without Xcode: no simulators
		}
		return Result{Err: err}
	}
	var list struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return Result{Err: fmt.Errorf("unexpected simctl output: %w", err)}
	}
	var items []model.CleanItem
	add := func(it model.CleanItem) {
		it.Screen, it.Group = model.ScreenDev, "Simulators"
		items = append(items, it)
	}
	runtimes := make([]string, 0, len(list.Devices))
	for rt := range list.Devices {
		runtimes = append(runtimes, rt)
	}
	sort.Strings(runtimes)
	var gone uint64
	nGone := 0
	for _, rt := range runtimes {
		for _, d := range list.Devices[rt] {
			if !d.IsAvailable {
				gone += d.DataPathSize
				nGone++
				continue
			}
			booted, err := time.Parse(time.RFC3339, d.LastBootedAt)
			if d.State != "Shutdown" || err != nil || d.DataPathSize == 0 {
				continue
			}
			days := int(env.Now().Sub(booted).Hours() / 24)
			if days < env.Config.StaleDays {
				continue
			}
			add(model.CleanItem{ID: "dev/simulators/device/" + d.UDID, Name: fmt.Sprintf("%s (%s)", d.Name, runtimeLabel(rt)),
				Detail: fmt.Sprintf("not started in %d days", days), Size: d.DataPathSize, Method: model.MethodCommand,
				Command: []string{"xcrun", "simctl", "delete", d.UDID},
				SizeOf:  []string{env.home("Library/Developer/CoreSimulator/Devices/" + d.UDID + "/data")}})
		}
	}
	if nGone > 0 {
		add(model.CleanItem{ID: "dev/simulators/unavailable", Name: "Unavailable simulators", Detail: goneDetail(nGone),
			Size: gone, Method: model.MethodCommand, Command: []string{"xcrun", "simctl", "delete", "unavailable"}, Safe: true})
	}
	core := env.home("Library/Developer/CoreSimulator")
	caches := measure(ctx, env.Sizer, []model.CleanItem{{ID: "dev/simulators/caches", Screen: model.ScreenDev, Group: "Simulators",
		Name: "Simulator caches", Detail: "rebuilt automatically", Method: model.MethodDelete, Paths: []string{filepath.Join(core, "Caches")},
		Root: core, Safe: true, App: "Simulator"}})
	items = append(items, caches...)
	runtimeItems, rtErr := simRuntimes(ctx, env)
	for _, r := range runtimeItems {
		add(r)
	}
	return Result{Items: items, Err: rtErr}
}

func simRuntimes(ctx context.Context, env Env) ([]model.CleanItem, error) {
	out, err := env.Run(ctx, "xcrun", "simctl", "runtime", "list", "-j")
	if err != nil {
		return nil, fmt.Errorf("couldn't list simulator runtimes: %w", err)
	}
	var rts map[string]simRuntime
	if err := json.Unmarshal(out, &rts); err != nil {
		return nil, fmt.Errorf("unexpected simctl runtime list output: %w", err)
	}
	keys := make([]string, 0, len(rts))
	for k := range rts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var items []model.CleanItem
	for _, k := range keys {
		r := rts[k]
		if !r.Deletable || r.SizeBytes == 0 {
			continue
		}
		detail := "Xcode downloads it again when needed"
		if used, err := time.Parse(time.RFC3339, r.LastUsedAt); err == nil {
			detail = "last used " + used.Local().Format("2 Jan 2006") + " · Xcode downloads it again"
		}
		items = append(items, model.CleanItem{ID: "dev/simulators/runtime/" + r.Identifier, Name: "Runtime " + runtimeLabel(r.RuntimeIdentifier),
			Detail: detail, Size: r.SizeBytes, Method: model.MethodCommand, Command: []string{"xcrun", "simctl", "runtime", "delete", r.Identifier}})
	}
	return items, nil
}

// goneDetail words the unavailable-simulators row for n simulators.
func goneDetail(n int) string {
	if n == 1 {
		return "1 simulator whose runtime is gone"
	}
	return fmt.Sprintf("%d simulators whose runtime is gone", n)
}

// runtimeLabel turns "com.apple.CoreSimulator.SimRuntime.iOS-26-0" into "iOS 26.0".
func runtimeLabel(id string) string {
	s := id[strings.LastIndex(id, ".")+1:]
	platform, version, ok := strings.Cut(s, "-")
	if !ok {
		return s
	}
	return platform + " " + strings.ReplaceAll(version, "-", ".")
}
