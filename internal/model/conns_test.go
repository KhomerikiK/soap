package model

import (
	"net/netip"
	"testing"
)

func TestBuildConnGroups(t *testing.T) {
	ap := netip.MustParseAddrPort
	helper := proc(70396, 501, "Google Chrome Helper", chromeHelperPath)
	main := proc(70000, 501, "Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	slack := proc(500, 501, "Slack", "/Applications/Slack.app/Contents/MacOS/Slack")
	procs := map[int]Process{70396: helper, 70000: main, 500: slack}
	socks := []Socket{
		{Proto: "tcp4", Local: ap("10.0.0.2:51801"), Remote: ap("140.82.114.26:443"), State: "ESTABLISHED", PID: 70396},
		{Proto: "udp4", Local: ap("10.0.0.2:54248"), Remote: ap("142.250.185.78:443"), PID: 70000},
		{Proto: "tcp4", Local: ap("10.0.0.2:50000"), Remote: ap("3.120.44.10:443"), State: "ESTABLISHED", PID: 500},
		{Proto: "tcp4", Local: ap("127.0.0.1:50001"), Remote: ap("127.0.0.1:3306"), State: "ESTABLISHED", PID: 500},
		{Proto: "tcp4", Local: ap("127.0.0.1:3000"), State: "LISTEN", PID: 48121},
	}
	rates := map[SocketKey]Rate{
		KeyOf(socks[0]): {Rx: 1000, Tx: 10},
		KeyOf(socks[1]): {Rx: 500},
		KeyOf(socks[2]): {Rx: 200, Tx: 5},
	}
	gs := BuildConnGroups(socks, procs, rates, false)
	if len(gs) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(gs), gs)
	}
	c := gs[0]
	if c.App != "Google Chrome" || len(c.Conns) != 2 || c.Rate.Rx != 1500 || c.Rate.Tx != 10 || len(c.PIDs) != 2 || c.Main.PID != 70000 {
		t.Errorf("chrome = %+v", c)
	}
	if c.Conns[0].Remote != ap("140.82.114.26:443") {
		t.Errorf("busiest connection should come first: %+v", c.Conns)
	}
	if gs[1].App != "Slack" || len(gs[1].Conns) != 1 {
		t.Errorf("loopback should be hidden: %+v", gs[1])
	}
	withLoopback := BuildConnGroups(socks, procs, rates, true)
	if len(withLoopback[1].Conns) != 2 {
		t.Errorf("loopback included: got %d conns, want 2", len(withLoopback[1].Conns))
	}
}

func TestBuildConnGroupsUnknownProcess(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{{Proto: "tcp4", Local: ap("10.0.0.2:1"), Remote: ap("1.1.1.1:443"), State: "ESTABLISHED", PID: 77, Command: "curl"}}
	g := BuildConnGroups(socks, nil, nil, false)[0]
	if g.App != "curl" || g.UID != -1 || g.Main.PID != 77 {
		t.Errorf("got %+v", g)
	}
}

func TestBuildConnGroupsActsOnTheAppNotItsHelper(t *testing.T) {
	ap := netip.MustParseAddrPort
	helper := proc(70396, 501, "Google Chrome Helper", chromeHelperPath)
	main := proc(70000, 501, "Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	procs := map[int]Process{70396: helper, 70000: main}
	socks := []Socket{{Proto: "tcp4", Local: ap("10.0.0.2:51801"), Remote: ap("140.82.114.26:443"), State: "ESTABLISHED", PID: 70396}}
	g := BuildConnGroups(socks, procs, nil, false)[0]
	if g.Main.PID != 70000 || len(g.PIDs) != 1 || g.PIDs[0] != 70396 {
		t.Errorf("Main = %d, PIDs = %v; want Main 70000 (the app) and PIDs [70396] (the helper that owns the socket)", g.Main.PID, g.PIDs)
	}
}

func TestBaselineMarksNewApps(t *testing.T) {
	var b Baseline
	first := []ConnGroup{{App: "Slack"}}
	b.Mark(first)
	if first[0].New {
		t.Error("apps in the first snapshot aren't new")
	}
	later := []ConnGroup{{App: "Slack"}, {App: "rapportd"}}
	b.Mark(later)
	if later[0].New || !later[1].New {
		t.Errorf("got %+v", later)
	}
}
