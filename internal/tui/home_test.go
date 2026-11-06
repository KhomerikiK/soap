package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/model"
)

// launch is a harness that ran Init, as soap does when it starts: the first snapshot, the Junk,
// Dev and Apps scans and the history.
func launch(t *testing.T, fc *fakeCleanup, opts ...func(*Deps)) *harness {
	t.Helper()
	opts = append([]func(*Deps){fc.deps, func(d *Deps) { d.Interval = time.Millisecond }}, opts...)
	h := newHarness(t, portsSnapshot(), opts...)
	h.pump(h.m.Init())
	return h
}

func TestSoapOpensOnHome(t *testing.T) {
	h := newHarness(t, portsSnapshot())
	if h.m.tab != model.TabHome || !strings.Contains(h.screen(), "Needs attention") {
		t.Fatalf("soap opens on Home:\n%s", h.screen())
	}
	for key, name := range map[string]string{"1": "Ports", "2": "Connections", "3": "Memory", "4": "Junk", "5": "Dev", "6": "Disk", "7": "Dupes", "8": "Apps", "0": "Home"} {
		h.press(key)
		if got := h.m.screens[h.m.tab].name(); got != name {
			t.Errorf("%s opens %s, want %s", key, got, name)
		}
	}
	for tab, name := range map[int]string{model.TabPorts: "Ports", model.TabJunk: "Junk", model.TabDisk: "Disk", model.TabApps: "Apps"} {
		if got := h.m.screens[tab].name(); got != name {
			t.Errorf("model's tab %d is %s, want %s", tab, got, name)
		}
	}
	if !strings.Contains(h.screen(), " 0 Home ") {
		t.Errorf("the tab line starts with 0 Home:\n%s", h.screen())
	}
}

func TestHomeFillsAnEightyByTwentyWindow(t *testing.T) {
	h := launch(t, junkFixture())
	h.send(tea.WindowSizeMsg{Width: 80, Height: 20})
	lines := strings.Split(h.m.View().Content, "\n")
	if len(lines) != 20 {
		t.Fatalf("Home draws %d lines, want 20:\n%s", len(lines), h.screen())
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line %d is %d wide: %q", i, w, ansi.Strip(l))
		}
	}
	s := h.screen()
	plain := strings.Split(s, "\n")
	if !strings.HasPrefix(plain[2], "╭─ Needs attention") || !strings.HasPrefix(plain[17], "╰") {
		t.Errorf("the box takes the rules' place:\n%s", s)
	}
	if !strings.Contains(plain[1], " 2 Conns ") || strings.Contains(plain[1], "Connections") {
		t.Errorf("below 84 columns the tab line says 2 Conns: %q", plain[1])
	}
	if !strings.Contains(plain[19], "j/k move") || !strings.Contains(plain[19], "enter open") || !strings.Contains(plain[19], "r scan again") {
		t.Errorf("key line: %q", plain[19])
	}
	h.send(tea.WindowSizeMsg{Width: 84, Height: 20})
	if tabs := strings.Split(h.screen(), "\n")[1]; !strings.Contains(tabs, " 2 Connections ") {
		t.Errorf("at 84 columns the full name fits: %q", tabs)
	}
}

func TestHomeShowsTheScansNumbers(t *testing.T) {
	h := launch(t, junkFixture(), (&fakeDisk{}).deps)
	if len(h.m.home.view.findings) == 0 {
		t.Fatal("Home's findings are built when the data arrives, before any draw")
	}
	s := h.screen()
	for _, want := range []string{
		"│ ▸ ▲ Memory pressure is high", "4.2 GB of safe junk is ready to clean", "Quit Google Chrome to clean 2.0 GB of its caches", "The Simulators check failed: xcrun broke",
		"6.2 GB  4.2 GB safe", "1 check failed", "0 B  none unused", "not walked yet · enter walks", "not scanned yet · enter scans",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Home is missing %q:\n%s", want, s)
		}
	}
	h.press("4")
	if !strings.Contains(h.screen(), "found 6.2 GB · 4.2 GB ticked") {
		t.Errorf("Junk shows the same numbers:\n%s", h.screen())
	}
}

func TestCPUHistoryKeepsTwoMinutes(t *testing.T) {
	h := newHarness(t, portsSnapshot())
	for i := range 70 {
		snap := portsSnapshot()
		snap.CPU = float64(i)
		h.send(snapshotMsg{snap: snap})
	}
	if len(h.m.cpuHist) != 60 || h.m.cpuHist[0] != 10 {
		t.Errorf("history keeps the last 60 samples: %d, first %v", len(h.m.cpuHist), h.m.cpuHist[0])
	}
	status := strings.Split(h.screen(), "\n")[0]
	_, after, _ := strings.Cut(status, "CPU ")
	if graph, _, _ := strings.Cut(after, " "); len([]rune(graph)) != 8 {
		t.Errorf("the status bar shows 8 samples: %q", status)
	}
}

func TestHomeIgnoresTheFilterSortAndEsc(t *testing.T) {
	h := newHarness(t, portsSnapshot())
	h.press("/", "s", "esc")
	if h.m.editing || h.m.sortIdx[model.TabHome] != 0 || h.m.tab != model.TabHome {
		t.Errorf("editing %v, sort %d, tab %d", h.m.editing, h.m.sortIdx[model.TabHome], h.m.tab)
	}
}

func TestHelpSaysZeroToEight(t *testing.T) {
	h := newHarness(t, portsSnapshot())
	h.press("?")
	s := h.screen()
	for _, want := range []string{"0-8", "open the selected line's tab (Home)", "on Home, scan Junk, Dev and Apps again", "Press any key to close this help."} {
		if !strings.Contains(s, want) {
			t.Errorf("help is missing %q:\n%s", want, s)
		}
	}
}
