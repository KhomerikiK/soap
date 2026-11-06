package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/model"
)

func failingProcs(context.Context) ([]model.Process, error) { return nil, errors.New("ps failed") }

func TestScanFailsClosedWhenProcessListIsUnreadable(t *testing.T) {
	in := func(map[string]bool) <-chan model.ScanUpdate {
		ch := make(chan model.ScanUpdate, 2)
		ch <- model.ScanUpdate{Finder: "Browser caches", Items: []model.CleanItem{
			{ID: "a", App: "Google Chrome", Safe: true},
			{ID: "b", Safe: true},
		}, Done: 1, Total: 2}
		ch <- model.ScanUpdate{Finder: "Trash", Done: 2, Total: 2}
		close(ch)
		return ch
	}
	var got []model.ScanUpdate
	for u := range scanChecked(context.Background(), failingProcs, in) {
		got = append(got, u)
	}
	if len(got) != 2 {
		t.Fatalf("updates = %+v", got)
	}
	a, b := got[0].Items[0], got[0].Items[1]
	if a.Blocked != "Couldn't check whether Google Chrome is running" || a.Tickable() || a.PreTicked() {
		t.Errorf("an app item must be blocked: %+v", a)
	}
	if b.Blocked != "" || !b.PreTicked() {
		t.Errorf("an item with no app is unaffected: %+v", b)
	}
}

func TestScanCheckedPassesRunningNamesThrough(t *testing.T) {
	list := func(context.Context) ([]model.Process, error) {
		return []model.Process{{ProcessID: model.ProcessID{Name: "Xcode"}}}, nil
	}
	var seen map[string]bool
	in := func(running map[string]bool) <-chan model.ScanUpdate {
		seen = running
		ch := make(chan model.ScanUpdate)
		close(ch)
		return ch
	}
	for range scanChecked(context.Background(), list, in) {
	}
	if !seen["xcode"] {
		t.Errorf("running = %v", seen)
	}
}

func TestScanCheckedDrainsWhenNobodyReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	in := func(map[string]bool) <-chan model.ScanUpdate {
		ch := make(chan model.ScanUpdate)
		go func() {
			defer close(done)
			for i := 0; i < 3; i++ {
				ch <- model.ScanUpdate{Done: i}
			}
			close(ch)
		}()
		return ch
	}
	scanChecked(ctx, failingProcs, in) // never read
	cancel()
	<-done // the source finished, so the relay drained it
}

func TestCleanChecksProcessListFirst(t *testing.T) {
	_, err := runningForClean(context.Background(), failingProcs)
	if err == nil || err.Error() != "Couldn't check which apps are running, so nothing was cleaned" {
		t.Errorf("err = %v", err)
	}
}

func TestCleanAndRestoreRefuseToRunAsRoot(t *testing.T) {
	old := geteuid
	geteuid = func() int { return 0 }
	t.Cleanup(func() { geteuid = old })
	home := t.TempDir()
	d, err := HomeDeps(home, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "Don't run clean-ups with sudo: soap would leave root-owned files in your home folder."
	err = d.Clean(context.Background(), []model.CleanItem{{ID: "x", Method: model.MethodDelete}}, func(model.CleanResult) { t.Error("nothing should be reported") })
	if err == nil || err.Error() != want {
		t.Errorf("Clean err = %v", err)
	}
	if _, err := d.Restore("x"); err == nil || err.Error() != want {
		t.Errorf("Restore err = %v", err)
	}
	geteuid = func() int { return 501 }
	if _, err := d.Restore("x"); err == nil || strings.Contains(err.Error(), "sudo") {
		t.Errorf("a normal user gets the usual error: %v", err)
	}
}

func TestHomeDepsCleansATrailingSlash(t *testing.T) {
	home := t.TempDir()
	orig := filepath.Join(home, "Library/Application Support/MobileSync/Backup/abc")
	trashed := filepath.Join(home, ".Trash/abc")
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(trashed, 0o755); err != nil {
		t.Fatal(err)
	}
	h := &actions.History{Path: actions.HistoryPath(home), Home: home}
	if err := h.CheckWritable(); err != nil {
		t.Fatal(err)
	}
	if err := h.Append(model.HistoryEntry{ID: "t1", Name: "backup", Method: model.MethodTrash, Paths: []string{orig}, Trashed: []string{trashed}, Result: "done"}); err != nil {
		t.Fatal(err)
	}
	d, err := HomeDeps(home+"/", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Restore("t1"); err != nil {
		t.Errorf("a trailing slash in the home folder broke Restore: %v", err)
	}
}

func TestScanFailsClosedForAppsWhenProcessListIsUnreadable(t *testing.T) {
	in := func(map[string]bool) <-chan model.ScanUpdate {
		ch := make(chan model.ScanUpdate, 1)
		ch <- model.ScanUpdate{Finder: "Apps", Items: []model.CleanItem{
			{ID: "apps/notion.id", Bundle: "/Applications/Notion.app", Paths: []string{"/Applications/Notion.app"}},
			{ID: "apps/removed/Library/Caches/com.gone.tool"},
		}, Done: 1, Total: 1}
		close(ch)
		return ch
	}
	var got []model.CleanItem
	for u := range scanChecked(context.Background(), failingProcs, in) {
		got = append(got, u.Items...)
	}
	if got[0].Blocked != "Couldn't check whether Notion is running" || got[0].Tickable() {
		t.Errorf("an app must be blocked: %+v", got[0])
	}
	if got[1].Blocked != "" {
		t.Errorf("a removed app's leftover has no app to check: %+v", got[1])
	}
}

func TestAppFoldersNeverOverlap(t *testing.T) {
	if got := appFoldersFor("/Users/me", true); !slices.Equal(got, []string{"/Applications", "/Users/me/Applications"}) {
		t.Errorf("tools on: %v", got)
	}
	if got := appFoldersFor("/Users/me", false); !slices.Equal(got, []string{"/Users/me/Applications"}) {
		t.Errorf("tools off: %v", got)
	}
	if got := appFoldersFor("/", true); !slices.Equal(got, []string{"/Applications"}) {
		t.Errorf("a home folder of / must not list /Applications twice: %v", got)
	}
}

func TestHomeDepsCleanStopsWhenTheProcessListIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("clean-ups refuse root")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	d, err := HomeDeps(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	reported := false
	err = d.Clean(context.Background(), []model.CleanItem{{ID: "apps/notion.id", Bundle: "/Applications/Notion.app"}}, func(model.CleanResult) { reported = true })
	if err == nil || !strings.Contains(err.Error(), "Couldn't check which apps are running") || reported {
		t.Errorf("err = %v, reported %v", err, reported)
	}
}
