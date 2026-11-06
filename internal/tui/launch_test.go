package tui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

func cleanedEntry(id string, size uint64) model.HistoryEntry {
	return model.HistoryEntry{ID: id, Time: t0, Name: "trivy", Method: model.MethodDelete, Result: model.ResultDone, Size: size}
}

func TestInitStartsTheLightScansAndReadsTheHistory(t *testing.T) {
	fc := junkFixture()
	fc.history = []model.HistoryEntry{cleanedEntry("1-0", gib)}
	fd := &fakeDisk{}
	h := newHarness(t, portsSnapshot(), fc.deps, fd.deps, func(d *Deps) { d.Interval = time.Millisecond })
	cmd := h.m.Init()
	if want := []model.Screen{model.ScreenJunk, model.ScreenDev, model.ScreenApps}; !slices.Equal(fc.screens, want) {
		t.Fatalf("Init scanned %v, want %v", fc.screens, want)
	}
	h.pump(cmd)
	if len(fd.walks) != 0 || h.m.disk.started {
		t.Error("Init must not start the Disk walk")
	}
	if cs := h.m.checks[model.ScreenDupes]; cs.started || slices.Contains(fc.screens, model.ScreenDupes) {
		t.Error("Init must not start the Dupes scan")
	}
	if cs := h.m.checks[model.ScreenJunk]; cs.scanning || len(cs.items) != 4 {
		t.Errorf("Junk's scan ran: scanning %v, %d items", cs.scanning, len(cs.items))
	}
	if hs := h.m.home; !hs.historyRead || hs.historyErr != nil || len(hs.history) != 1 {
		t.Errorf("history = %+v", hs)
	}
}

func TestQuitStopsTheLaunchScans(t *testing.T) {
	fc := junkFixture()
	h := newHarness(t, portsSnapshot(), fc.deps)
	h.m.Init() // the scans' commands aren't run, so they're still going
	h.press("1")
	if _, quit := h.press("q")().(tea.QuitMsg); !quit {
		t.Fatal("q quits")
	}
	for i, ctx := range fc.scans {
		if ctx.Err() == nil {
			t.Errorf("the %s scan still runs after quitting", fc.screens[i])
		}
	}
}

func TestHistoryIsReadAgainAfterACleanUpAndAPutBack(t *testing.T) {
	fc := junkFixture()
	h := openJunk(t, fc)
	fc.history = []model.HistoryEntry{cleanedEntry("1-0", gib)}
	h.press("c")
	h.pump(h.press("y"))
	if hs := h.m.home; !hs.historyRead || len(hs.history) != 1 {
		t.Fatalf("after a clean-up, history = %+v", hs)
	}
	fc.history = append(fc.history, model.HistoryEntry{ID: "2-0", Name: "backup", Method: model.MethodTrash, Trashed: []string{"/x"}, Result: model.ResultDone})
	h.pump(h.press("h"))
	h.press("j")
	h.pump(h.press("u"))
	if len(fc.restored) != 1 || len(h.m.home.history) != 2 {
		t.Errorf("after a put-back, restored %v, history %d entries", fc.restored, len(h.m.home.history))
	}
}
