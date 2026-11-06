package sys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// answer is a Runner that gives out for one command line and fails for any other.
func answer(t *testing.T, want, out string) (Runner, *[]string) {
	t.Helper()
	var calls []string
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, line)
		if line != want {
			return nil, errors.New("unexpected command: " + line)
		}
		return []byte(out), nil
	}, &calls
}

func TestLastOpened(t *testing.T) {
	apps := []string{"/Applications/Notion.app", "/Applications/Docker.app", "/Users/me/Applications/zoom.us.app"}
	run, calls := answer(t, "mdls -raw -nullMarker (null) -name kMDItemLastUsedDate "+strings.Join(apps, " "),
		"(null)\x002026-10-15 10:53:44 +0000\x002026-10-28 14:09:11 +0400")
	dates, err := LastOpened(context.Background(), run, apps)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Errorf("one mdls call for every app, got %v", *calls)
	}
	if !dates[0].IsZero() || !dates[1].Equal(time.Date(2026, 10, 15, 10, 53, 44, 0, time.UTC)) || !dates[2].Equal(time.Date(2026, 10, 28, 10, 9, 11, 0, time.UTC)) {
		t.Errorf("dates = %v", dates)
	}
}

func TestLastOpenedRefusesAnAnswerThatDoesNotFit(t *testing.T) {
	apps := []string{"/Applications/Notion.app", "/Applications/Docker.app"}
	line := "mdls -raw -nullMarker (null) -name kMDItemLastUsedDate " + strings.Join(apps, " ")
	for name, out := range map[string]string{
		"one value short": "(null)",
		"an error inside": "(null)Docker.app: could not find /Applications/Docker.app.\n",
		"not a date":      "(null)\x00yesterday",
	} {
		run, _ := answer(t, line, out)
		if _, err := LastOpened(context.Background(), run, apps); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if dates, err := LastOpened(context.Background(), nil, nil); err != nil || dates != nil {
		t.Errorf("no apps: %v, %v", dates, err)
	}
}

func TestSpotlightApps(t *testing.T) {
	out := "/Applications/Safari.app   kMDItemCFBundleIdentifier = com.apple.Safari\n" +
		"/Applications/My   Tools/Odd.app   kMDItemCFBundleIdentifier = com.example.Odd\n" +
		"/Users/me/Library/HTTPStorages/com.ecswizard.app   kMDItemCFBundleIdentifier = (null)\n" +
		"garbage\n"
	run, _ := answer(t, "mdfind -attr kMDItemCFBundleIdentifier kMDItemContentType == 'com.apple.application-bundle'", out)
	ids, err := SpotlightApps(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || !ids["com.apple.safari"] || !ids["com.example.odd"] {
		t.Errorf("ids = %v", ids)
	}
}

func TestReveal(t *testing.T) {
	run, calls := answer(t, "open -R /Applications/Microsoft Teams.app", "")
	if err := Reveal(context.Background(), run, "/Applications/Microsoft Teams.app"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Errorf("calls = %v", *calls)
	}
	if err := Reveal(context.Background(), run, "Teams.app"); err == nil || len(*calls) != 1 {
		t.Errorf("a relative path is refused without running open: %v, %v", err, *calls)
	}
}

func TestLastOpenedRefusesRelativePaths(t *testing.T) {
	if _, err := LastOpened(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("mdls ran")
		return nil, nil
	}, []string{"-rf.app"}); err == nil {
		t.Error("a relative path was passed to mdls")
	}
}

func TestSpotlightAppsRefusesAnUnknownFormat(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("/Applications/Notion.app\tnotion.id\n/Applications/Xcode.app\tcom.apple.dt.Xcode\n"), nil
	}
	if _, err := SpotlightApps(context.Background(), run); err == nil {
		t.Error("output in another format looked like Spotlight is off")
	}
	empty := func(context.Context, string, ...string) ([]byte, error) { return []byte("\n"), nil }
	if ids, err := SpotlightApps(context.Background(), empty); err != nil || len(ids) != 0 {
		t.Errorf("an empty answer: %v %v", ids, err)
	}
}
