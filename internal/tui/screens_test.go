package tui

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

func fullSnapshot() engine.Snapshot {
	snap := portsSnapshot()
	ap := netip.MustParseAddrPort
	chrome := model.ProcessID{PID: 70000, Name: "Google Chrome", StartTime: t0}
	snap.Groups = []model.ConnGroup{
		{
			App: "Google Chrome", AppPath: "/Applications/Google Chrome.app", UID: me, Main: chrome, PIDs: []int{70000, 70396},
			Rate: model.Rate{Rx: 839680, Tx: 32768},
			Conns: []model.Conn{
				{Proto: "tcp4", Remote: ap("142.250.185.78:443"), State: "ESTABLISHED", PID: 70396},
				{Proto: "udp4", Remote: ap("151.101.1.69:443"), PID: 70000},
			},
		},
		{
			App: "rapportd", UID: me, Main: model.ProcessID{PID: 638, Name: "rapportd", StartTime: t0}, PIDs: []int{638}, New: true,
			Conns: []model.Conn{{Proto: "tcp4", Remote: ap("192.168.1.24:49152"), State: "ESTABLISHED", PID: 638}},
		},
	}
	snap.Apps = []model.AppUsage{
		{Name: "Google Chrome", AppPath: "/Applications/Google Chrome.app", PIDs: []int{70000, 70396, 70397}, Memory: 3650722201, CPU: 6.1, UID: me, Main: chrome},
		{Name: "Slack", AppPath: "/Applications/Slack.app", PIDs: []int{500}, Memory: 817889280, CPU: 0.8, UID: me, Main: model.ProcessID{PID: 500, Name: "Slack", StartTime: t0}},
		{Name: "WindowServer", PIDs: []int{400}, Memory: 429916160, CPU: 4, UID: 88, System: true, Main: model.ProcessID{PID: 400, Name: "WindowServer", StartTime: t0}},
		{Name: "node", PIDs: []int{48121}, Memory: 104857600, CPU: 1.5, UID: me, Main: model.ProcessID{PID: 48121, Name: "node", StartTime: t0}},
	}
	return snap
}

func TestTabLineShowsEveryScreen(t *testing.T) {
	s := newHarness(t, fullSnapshot()).screen()
	for _, want := range []string{"0 Home", "1 Ports", "2 Connections", "3 Memory", "4 Junk", "8 Apps"} {
		if !strings.Contains(s, want) {
			t.Errorf("tab line is missing %q:\n%s", want, s)
		}
	}
}

func TestConnectionsScreen(t *testing.T) {
	h := newHarness(t, fullSnapshot(), func(d *Deps) {
		d.Hostname = func(ip netip.Addr) string {
			if ip.String() == "142.250.185.78" {
				return "lhr48s29-in-f14.1e100.net"
			}
			return ""
		}
		d.Owner = func(ip netip.Addr) string {
			if ip.String() == "142.250.185.78" {
				return "GOOGLE · US"
			}
			return ""
		}
	})
	h.press("2")
	s := h.screen()
	for _, want := range []string{"3 across 2 apps", "▸ Google Chrome", "820 KB/s", "rapportd", "new"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "142.250.185.78") {
		t.Error("connections stay hidden until the app is opened")
	}
	h.press("enter")
	s = h.screen()
	for _, want := range []string{"▾ Google Chrome", "142.250.185.78:443", "lhr48s29-in-f14.1e100.net", "GOOGLE · US", "151.101.1.69:443", "—"} {
		if !strings.Contains(s, want) {
			t.Errorf("opened app is missing %q:\n%s", want, s)
		}
	}
}

func TestConnectionsDatabaseHint(t *testing.T) {
	h := newHarness(t, fullSnapshot()) // no Owner: the database isn't installed
	h.press("2")
	if !strings.Contains(h.screen(), "Run `soap update-db` to show who owns each IP.") {
		t.Error("no database hint")
	}
}

func TestConnectionsLoopbackToggle(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("2")
	h.run(h.press("l"))
	if got := h.opts[len(h.opts)-1]; !got.Loopback {
		t.Errorf("Collect got %+v, want Loopback", got)
	}
	if !strings.Contains(h.screen(), "including loopback") {
		t.Error("the screen should say loopback is shown")
	}
}

func TestQuitAppFromConnections(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("2", "x")
	if s := h.screen(); !strings.Contains(s, "Quit Google Chrome?") || !strings.Contains(s, "like ⌘Q") {
		t.Fatalf("no quit confirmation:\n%s", s)
	}
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Asking Google Chrome to quit…") {
		t.Errorf("no working text before the quit finishes:\n%s", h.screen())
	}
	if len(h.act.quit) != 0 {
		t.Error("the quit shouldn't have run yet")
	}
	h.run(cmd)
	if !slices.Equal(h.act.quit, []string{"/Applications/Google Chrome.app"}) || len(h.act.terminated) != 0 {
		t.Errorf("quit %v, terminated %v", h.act.quit, h.act.terminated)
	}
	if !strings.Contains(h.screen(), "Asked Google Chrome to quit.") {
		t.Error("no result message")
	}
}

func TestQuitDeniedOffersSIGTERM(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.act.quitErr = actions.ErrAutomationDenied
	h.press("3")
	h.run(h.press("x", "y"))
	if s := h.screen(); !strings.Contains(s, "macOS blocked the quit request for Google Chrome. Send SIGTERM instead?") {
		t.Fatalf("no SIGTERM offer:\n%s", s)
	}
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Stopping Google Chrome…") {
		t.Errorf("no working text for the SIGTERM follow-up:\n%s", h.screen())
	}
	h.run(cmd)
	if !slices.Equal(h.act.terminated, []termCall{{70000, false}}) {
		t.Errorf("terminated %v", h.act.terminated)
	}
}

func TestQuitResultsForCancelledAndNotRegular(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.act.quitErr = actions.ErrQuitCancelled
	h.press("3")
	h.run(h.press("x", "y"))
	if !strings.Contains(h.screen(), "Google Chrome stayed open.") {
		t.Errorf("no cancelled result:\n%s", h.screen())
	}
	h.act.quitErr = actions.ErrNotRegularApp
	h.run(h.press("x", "y"))
	if !strings.Contains(h.screen(), "Google Chrome isn't running as a regular app. Press X to force quit it.") {
		t.Errorf("no not-regular result:\n%s", h.screen())
	}
}

func TestForceQuitApp(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("3", "j", "X")
	if !strings.Contains(h.screen(), "Force quit Slack (pid 500)? Unsaved work is lost.") {
		t.Fatalf("no force quit confirmation:\n%s", h.screen())
	}
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Killing Slack…") {
		t.Errorf("no working text:\n%s", h.screen())
	}
	h.run(cmd)
	if !slices.Equal(h.act.terminated, []termCall{{500, true}}) {
		t.Errorf("terminated %v", h.act.terminated)
	}
}

func TestMemoryScreen(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("3")
	s := h.screen()
	for _, want := range []string{"Memory pressure", "warning", "used 11.0 GB of 18.0 GB", "grouped by app", "Google Chrome", "3 processes", "3.4 GB", "780 MB", "system"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
}

func TestMemoryRefusesWindowServer(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("3", "j", "j")
	if cmd := h.press("x"); cmd != nil {
		t.Error("a refusal shouldn't run anything")
	}
	if s := h.screen(); !strings.Contains(s, "soap won't stop WindowServer: quitting it would log you out") {
		t.Errorf("no refusal:\n%s", s)
	}
}

func TestMemoryLoneProcessIsKilled(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("3", "j", "j", "j", "x")
	if s := h.screen(); !strings.Contains(s, "Kill node (pid 48121)?") {
		t.Errorf("a process outside an app gets a kill confirmation:\n%s", s)
	}
}

func TestMemoryGroupToggle(t *testing.T) {
	h := newHarness(t, fullSnapshot())
	h.press("3")
	h.run(h.press("g"))
	if got := h.opts[len(h.opts)-1]; !got.RawProcesses {
		t.Errorf("Collect got %+v, want RawProcesses", got)
	}
	if !strings.Contains(h.screen(), "one row per process") {
		t.Error("the screen should say it lists processes")
	}
}
