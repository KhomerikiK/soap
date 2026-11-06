//go:build darwin

package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFindXcode(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Library/Developer/Xcode/DerivedData/MotoMetrics-abcdefgh/Build/x", 5000)
	put(t, home, "Library/Developer/Xcode/DerivedData/ModuleCache.noindex/x", 5000)
	put(t, home, "Library/Caches/com.apple.dt.Xcode/x", 5000)
	put(t, home, "Library/Developer/Xcode/Archives/2026-08-12/MotoMetrics 1.4.2.xcarchive/Info.plist", 5000)
	put(t, home, "Library/Developer/Xcode/iOS DeviceSupport/17.2 (21C62)/Symbols/x", 5000)
	r := findXcode(context.Background(), testEnv(home, nil))
	items := byID(r.Items)
	dd := items["dev/xcode/deriveddata/MotoMetrics-abcdefgh"]
	if dd.Name != "DerivedData: MotoMetrics" || !dd.Safe || dd.App != "Xcode" || dd.Method != model.MethodDelete {
		t.Errorf("DerivedData = %+v", dd)
	}
	if _, ok := items["dev/xcode/deriveddata/ModuleCache.noindex"]; !ok {
		t.Errorf("module cache missing: %v", ids(r.Items))
	}
	if c := items["dev/xcode/caches"]; !c.Safe || c.App != "Xcode" {
		t.Errorf("Xcode caches = %+v", c)
	}
	a := items["dev/xcode/archives/2026-08-12/MotoMetrics 1.4.2.xcarchive"]
	if a.Method != model.MethodTrash || a.Safe || a.Name != "Archive: MotoMetrics 1.4.2" {
		t.Errorf("archive = %+v", a)
	}
	if ds := items["dev/xcode/devicesupport/iOS/17.2 (21C62)"]; ds.Safe || ds.Name != "iOS DeviceSupport 17.2 (21C62)" {
		t.Errorf("DeviceSupport = %+v", ds)
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindSimulators(t *testing.T) {
	home := t.TempDir()
	put(t, home, "Library/Developer/CoreSimulator/Caches/dyld/x", 5000)
	run := &fakeRun{out: map[string]string{
		"xcrun simctl list -j devices": fixture(t, "simctl_devices.json"),
		"xcrun simctl runtime list -j": fixture(t, "simctl_runtimes.json"),
	}}
	r := findSimulators(context.Background(), testEnv(home, run, "xcrun"))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	items := byID(r.Items)
	u := items["dev/simulators/unavailable"]
	if !u.Safe || u.Size != 2_000_000_000 || !slices.Equal(u.Command, []string{"xcrun", "simctl", "delete", "unavailable"}) || u.Detail != "2 simulators whose runtime is gone" {
		t.Errorf("unavailable = %+v", u)
	}
	old := items["dev/simulators/device/BBB"]
	if old.Name != "iPhone 14 (iOS 26.0)" || old.Detail != "not started in 214 days" || old.Safe || !slices.Equal(old.Command, []string{"xcrun", "simctl", "delete", "BBB"}) {
		t.Errorf("old simulator = %+v", old)
	}
	if want := []string{filepath.Join(home, "Library/Developer/CoreSimulator/Devices/BBB/data")}; !slices.Equal(old.SizeOf, want) {
		t.Errorf("old simulator SizeOf = %v, want %v", old.SizeOf, want)
	}
	if len(u.SizeOf) != 0 || len(items["dev/simulators/runtime/7F6838BE"].SizeOf) != 0 {
		t.Error("unavailable simulators and runtimes are measured by free disk space, not SizeOf")
	}
	for _, id := range []string{"dev/simulators/device/AAA", "dev/simulators/device/CCC", "dev/simulators/device/FFF"} {
		if _, ok := items[id]; ok {
			t.Errorf("%s is recent, running or never started, so it isn't offered", id)
		}
	}
	if c := items["dev/simulators/caches"]; !c.Safe || c.App != "Simulator" {
		t.Errorf("caches = %+v", c)
	}
	rt := items["dev/simulators/runtime/7F6838BE"]
	if rt.Name != "Runtime watchOS 26.5" || rt.Size != 3935033546 || rt.Safe || !strings.Contains(rt.Detail, "Xcode downloads it again") {
		t.Errorf("runtime = %+v", rt)
	}
	if _, ok := items["dev/simulators/runtime/12345678"]; ok {
		t.Error("a runtime simctl won't delete isn't offered")
	}
	checkPaths(t, home, r.Items, nil)
}

func TestFindSimulatorsWithoutXcode(t *testing.T) {
	if r := findSimulators(context.Background(), testEnv(t.TempDir(), nil)); len(r.Items) != 0 || r.Err != nil {
		t.Errorf("no xcrun: %+v", r)
	}
	run := &fakeRun{errs: map[string]error{"xcrun simctl list -j devices": &sys.CommandError{Name: "xcrun", Err: errors.New("exit status 72"), Stderr: "xcrun: error: unable to find utility \"simctl\""}}}
	if r := findSimulators(context.Background(), testEnv(t.TempDir(), run, "xcrun")); len(r.Items) != 0 || r.Err != nil {
		t.Errorf("command-line tools only: %+v", r)
	}
}

func TestFindSimulatorsRuntimeListFails(t *testing.T) {
	run := &fakeRun{
		out:  map[string]string{"xcrun simctl list -j devices": fixture(t, "simctl_devices.json")},
		errs: map[string]error{"xcrun simctl runtime list -j": &sys.CommandError{Name: "xcrun", Err: errors.New("exit status 1"), Stderr: "runtime service down"}},
	}
	r := findSimulators(context.Background(), testEnv(t.TempDir(), run, "xcrun"))
	if r.Err == nil || !strings.Contains(r.Err.Error(), "simulator runtimes") {
		t.Fatalf("Err = %v, want one that mentions simulator runtimes", r.Err)
	}
	items := byID(r.Items)
	if _, ok := items["dev/simulators/device/BBB"]; !ok {
		t.Errorf("device items were lost: %v", ids(r.Items))
	}
	if _, ok := items["dev/simulators/unavailable"]; !ok {
		t.Errorf("unavailable item was lost: %v", ids(r.Items))
	}
}

func TestFindSimulatorsOneUnavailable(t *testing.T) {
	run := &fakeRun{out: map[string]string{
		"xcrun simctl list -j devices": `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-0":[{"name":"iPhone 17","udid":"ZZZ","state":"Shutdown","isAvailable":false,"dataPathSize":1000}]}}`,
		"xcrun simctl runtime list -j": `{}`,
	}}
	r := findSimulators(context.Background(), testEnv(t.TempDir(), run, "xcrun"))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if u := byID(r.Items)["dev/simulators/unavailable"]; u.Detail != "1 simulator whose runtime is gone" {
		t.Errorf("detail = %q", u.Detail)
	}
}

func TestRuntimeLabel(t *testing.T) {
	tests := map[string]string{
		"com.apple.CoreSimulator.SimRuntime.iOS-26-0":     "iOS 26.0",
		"com.apple.CoreSimulator.SimRuntime.watchOS-26-5": "watchOS 26.5",
		"com.apple.CoreSimulator.SimRuntime.xrOS-2-0":     "xrOS 2.0",
	}
	for in, want := range tests {
		if got := runtimeLabel(in); got != want {
			t.Errorf("runtimeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDockerSize(t *testing.T) {
	tests := map[string]uint64{"0B": 0, "512MB": 512_000_000, "3.749GB": uint64(3.749 * 1e9), "470.2MB (51%)": uint64(470.2 * 1e6), "12kB": 12_000, "1.5TB": 1_500_000_000_000}
	for in, want := range tests {
		if got, err := parseDockerSize(in); err != nil || got != want {
			t.Errorf("parseDockerSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"lots", "-5B", "NaNB", "InfB"} {
		if _, err := parseDockerSize(in); err == nil {
			t.Errorf("parseDockerSize(%q) should be an error", in)
		}
	}
}

func TestFindDocker(t *testing.T) {
	run := &fakeRun{out: map[string]string{
		"docker system df --format {{json .}}":                     fixture(t, "docker_df.jsonl"),
		"docker images --filter dangling=true --format {{json .}}": fixture(t, "docker_dangling.jsonl"),
		"docker images --format {{json .}}":                        fixture(t, "docker_images_mixed.jsonl"),
	}}
	r := findDocker(context.Background(), testEnv(t.TempDir(), run, "docker"))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	items := byID(r.Items)
	bc := items["dev/docker/build-cache"]
	if !bc.Safe || bc.Size != uint64(3.749*1e9) || !slices.Equal(bc.Command, []string{"docker", "builder", "prune", "-a", "-f"}) {
		t.Errorf("build cache = %+v", bc)
	}
	ut := items["dev/docker/untagged-images"]
	if !ut.Safe || ut.Size != 512_000_000 || !slices.Equal(ut.Command, []string{"docker", "image", "prune", "-f"}) {
		t.Errorf("untagged = %+v; images still used by a container don't count", ut)
	}
	un := items["dev/docker/unused-images"]
	if un.Safe || un.Size != 1_000_000 || !slices.Equal(un.Command, []string{"docker", "image", "prune", "-a", "-f"}) {
		t.Errorf("unused = %+v", un)
	}
	if sc := items["dev/docker/stopped-containers"]; sc.Safe || sc.Size != uint64(470.2*1e6) {
		t.Errorf("stopped containers = %+v", sc)
	}
	for _, it := range r.Items {
		if strings.Contains(strings.Join(it.Command, " "), "volume") {
			t.Errorf("%s touches volumes", it.ID)
		}
	}
	if len(r.Notes) != 1 || r.Notes[0].Group != "Docker" || !strings.Contains(r.Notes[0].Text, "Volumes") {
		t.Errorf("notes = %+v", r.Notes)
	}
}

func TestFindDockerEveryImageUsed(t *testing.T) {
	run := &fakeRun{out: map[string]string{
		"docker system df --format {{json .}}":                     fixture(t, "docker_df.jsonl"),
		"docker images --filter dangling=true --format {{json .}}": "",
		"docker images --format {{json .}}":                        fixture(t, "docker_images_used.jsonl"),
	}}
	r := findDocker(context.Background(), testEnv(t.TempDir(), run, "docker"))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, ok := byID(r.Items)["dev/docker/unused-images"]; ok {
		t.Errorf("unused images offered although every image has a container: %v", ids(r.Items))
	}
	want := "Every image is used by a container. Remove stopped containers first, then the images they used can go."
	found := false
	for _, n := range r.Notes {
		if n.Group == "Docker" && n.Text == want {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %+v, want %q", r.Notes, want)
	}
}

func TestFindDockerImagesListFails(t *testing.T) {
	run := &fakeRun{
		out:  map[string]string{"docker system df --format {{json .}}": fixture(t, "docker_df.jsonl"), "docker images --filter dangling=true --format {{json .}}": ""},
		errs: map[string]error{"docker images --format {{json .}}": &sys.CommandError{Name: "docker", Err: errors.New("exit status 1"), Stderr: "daemon busy"}},
	}
	r := findDocker(context.Background(), testEnv(t.TempDir(), run, "docker"))
	if r.Err == nil || !strings.Contains(r.Err.Error(), "docker images") {
		t.Fatalf("Err = %v", r.Err)
	}
}

func TestFindDockerDanglingFails(t *testing.T) {
	run := &fakeRun{
		out:  map[string]string{"docker system df --format {{json .}}": fixture(t, "docker_df.jsonl")},
		errs: map[string]error{"docker images --filter dangling=true --format {{json .}}": &sys.CommandError{Name: "docker", Err: errors.New("exit status 1"), Stderr: "daemon busy"}},
	}
	r := findDocker(context.Background(), testEnv(t.TempDir(), run, "docker"))
	if r.Err == nil || !strings.Contains(r.Err.Error(), "untagged docker images") {
		t.Fatalf("Err = %v, want one that mentions untagged docker images", r.Err)
	}
	items := byID(r.Items)
	for _, id := range []string{"dev/docker/untagged-images", "dev/docker/unused-images"} {
		if _, ok := items[id]; ok {
			t.Errorf("%s is offered although its split is unknown", id)
		}
	}
	if _, ok := items["dev/docker/build-cache"]; !ok {
		t.Errorf("build cache was lost: %v", ids(r.Items))
	}
}

func TestFindDockerNotRunning(t *testing.T) {
	run := &fakeRun{errs: map[string]error{"docker system df --format {{json .}}": &sys.CommandError{Name: "docker", Err: errors.New("exit status 1"), Stderr: "Cannot connect to the Docker daemon"}}}
	r := findDocker(context.Background(), testEnv(t.TempDir(), run, "docker"))
	if len(r.Items) != 0 || r.Err != nil || len(r.Notes) != 1 || r.Notes[0].Text != "Docker isn't running." {
		t.Errorf("not running: %+v", r)
	}
	if r := findDocker(context.Background(), testEnv(t.TempDir(), nil)); len(r.Items)+len(r.Notes) != 0 {
		t.Errorf("not installed: %+v", r)
	}
}
