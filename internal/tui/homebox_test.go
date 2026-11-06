package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/model"
)

// macInput is the user's Mac on 2026-11-06, as the spec describes it, with an example for Apps.
func macInput() model.HomeInput {
	mib := func(n uint64) uint64 { return n << 20 }
	j, d := model.ScreenJunk, model.ScreenDev
	at := func(h, m int) time.Time { return time.Date(2026, 11, 6, h, m, 0, 0, time.Local) }
	l := func(port uint16, process, bind string, system bool) model.Listener {
		return model.Listener{Port: port, Process: process, Bind: bind, System: system}
	}
	return model.HomeInput{
		CPU:        18,
		CPUHistory: []float64{20, 25, 40, 30, 18, 5, 18, 22, 30, 55, 85, 30, 18},
		Memory:     model.MemoryStats{Total: 18 * gib, Used: mib(14438), SwapUsed: 2 * gib, Compressed: 2 * gib, Pressure: model.PressureNormal},
		Top: []model.AppUsage{
			{Name: "Claude", Memory: mib(2765), CPU: 23}, {Name: "Brave Browser", Memory: mib(1843)}, {Name: "Visual Studio Code", Memory: mib(1126)},
			{Name: "Python", Memory: mib(553)}, {Name: "Spotlight", Memory: mib(310)}, {Name: "Finder", Memory: mib(100)},
		},
		Listeners: []model.Listener{
			l(53, "dnsmasq", "127.0.0.1", false), l(4040, "ngrok", "127.0.0.1", false), l(5000, "ControlCenter", "*", true),
			l(5432, "postgres", "127.0.0.1, ::1", false), l(7000, "ControlCenter", "*", true), l(8080, "node", "127.0.0.1", false),
			l(9000, "php-fpm", "127.0.0.1", false),
		},
		Groups: []model.ConnGroup{
			{App: "Brave Browser", Rate: model.Rate{Rx: 1843, Tx: 2970}, Conns: make([]model.Conn, 40)},
			{App: "Claude", Conns: make([]model.Conn, 17)},
		},
		Net:      model.Rate{Rx: 1843, Tx: 2970},
		DiskFree: 109 * gib, DiskTotal: 460 * gib,
		Junk: model.ScanState{Available: true, Started: true, Items: []model.CleanItem{
			{ID: "j1", Screen: j, Group: "App caches", Size: mib(11366), Safe: true},
			{ID: "j2", Screen: j, Group: "Browser caches", Size: mib(2150), Safe: true, Busy: true, App: "Brave Browser"},
		}, Notes: []model.Note{{Screen: j, Text: "no access", Code: model.NoteNoFullDiskAccess}}},
		Dev: model.ScanState{Available: true, Started: true, Items: []model.CleanItem{
			{ID: "d1", Screen: d, Group: "Simulators", Size: mib(23757)},
			{ID: "d2", Screen: d, Group: "Package caches", Size: mib(9011)},
			{ID: "d3", Screen: d, Group: "Projects", Size: mib(6758)},
		}},
		Apps: model.ScanState{Available: true, Started: true, Items: []model.CleanItem{
			{ID: "apps/a", Screen: model.ScreenApps, Group: "Apps", Size: mib(7373), Paths: []string{"/Applications/A.app"}, Bundle: "/Applications/A.app", UnusedDays: 30},
		}},
		Dupes: model.ScanState{Available: true},
		Disk:  model.DiskState{Available: true},
		History: []model.HistoryEntry{
			{ID: "3-1", Time: at(18, 22), Method: model.MethodTrash, Trashed: []string{"/x"}, Result: model.ResultDone, Size: mib(13000)},
			{ID: "3-0", Time: at(18, 22), Method: model.MethodTrash, Trashed: []string{"/y"}, Result: model.ResultDone, Size: mib(14443)},
			{ID: "2-0", Time: at(18, 20), Method: model.MethodDelete, Result: model.ResultDone, Size: mib(20992)},
			{ID: "1-0", Time: at(15, 5), Method: model.MethodCommand, Result: model.ResultDone, Size: mib(92672)},
		},
		HistoryAvailable: true, HistoryRead: true, NoFullDiskAccess: true,
	}
}

// checkBox fails unless lines are exactly height lines, each exactly width columns, each starting
// with a border.
func checkBox(t *testing.T, lines []string, width, height int) {
	t.Helper()
	if len(lines) != height {
		t.Fatalf("%d lines, want %d", len(lines), height)
	}
	for i, l := range lines {
		plain := ansi.Strip(l)
		if w := ansi.StringWidth(plain); w != width {
			t.Errorf("line %d is %d wide, want %d: %q", i, w, width, plain)
		}
		if r := []rune(plain); i > 0 && !strings.HasPrefix(plain, "│") && !strings.HasPrefix(plain, "├") && !strings.HasPrefix(plain, "╰") {
			t.Errorf("line %d doesn't start with a border: %q", i, string(r))
		}
	}
}

func TestHomeBoxAt80x20(t *testing.T) {
	v := buildHomeView(macInput())
	lines := drawHome(v, 80, 16, homeSel{cursor: "space/junk/safe"})
	t.Log("\n" + ansi.Strip(strings.Join(lines, "\n")))
	if len(lines) != 16 {
		t.Fatalf("%d lines, want 16", len(lines))
	}
	checkBox(t, lines, 80, 16)
	s := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{
		"╭─ Needs attention ", " 1–5 of 8 ─╮",
		"   ◆ 26.8 GB that soap moved still sits in the Trash", "empty it in Finder │",
		"◆ Simulators hold 23.2 GB", "5 Dev │",
		"│ ▸ ◆ 11.1 GB of safe junk is ready to clean", "4 Junk │",
		"├─ System ", "┬─ What you can free ",
		"│ CPU  ", " 18%", "│ Mem  ━", " 14.1/18.0 GB", "normal · swap 2.0 GB", "│ Net  ↓ 1.8 KB/s   ↑ 2.9 KB/s", "│ Disk ━", " 109 GB free ",
		"│ 4 Junk  ━", "13.2 GB  11.1 safe", "│ 5 Dev   ━", "38.6 GB  none safe", "│ 8 Apps  ━", "7.2 GB  1 unused",
		"│ 6 Disk  not walked yet · enter walks", "│ 7 Dupes not scanned yet · enter scans│",
		"├─ Ports ", "┼─ History ",
		"│ 7 listening · 5 from your apps", "│ :53 dnsmasq  :4040 ngrok  +3 more",
		"111 GB freed in 3 clean-ups ", "26.8 GB in the Trash · can put back",
		"╰───", "┴───", "───╯",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("box is missing %q", want)
		}
	}
	for _, unwanted := range []string{"compressed", "free of 460", "Top memory", "since 6 Nov"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("at 80×20 the box has no room for %q", unwanted)
		}
	}
	if mid := []rune(ansi.Strip(lines[6])); string(mid[40]) != "┬" {
		t.Errorf("the middle border is at column 40: %q", string(mid))
	}
	for i, l := range lines[1:6] {
		if on := strings.Contains(l, "\x1b[7m"); on != (i == 2) {
			t.Errorf("attention line %d selected = %v: %q", i, on, l)
		}
	}
}

func TestHomeBoxInALargerWindow(t *testing.T) {
	v := buildHomeView(macInput())
	lines := drawHome(v, 110, 24, homeSel{cursor: keyPorts, start: 3})
	t.Log("\n" + ansi.Strip(strings.Join(lines, "\n")))
	checkBox(t, lines, 110, 24)
	s := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{
		" 4–8 of 8 ─╮", "Give your terminal Full Disk Access to see the Trash and iOS backups",
		"normal · swap 2.0 GB · compressed 2.0 GB", "109 GB free of 460",
		"11.1 GB safe", "Simulators 23.2 GB",
		"├─ Top memory ", "┼─ Network ", "│ Claude               2.7 GB   23%", "│ 2 apps · 57 connections",
		"│ Brave Browser        ↓ 1.8 KB/s   ↑ 2.9 KB/s",
		"│ :53    dnsmasq        127.0.0.1", "│ :5432  postgres       127.0.0.1, ::1",
		"│ 4 Junk  ━━━━━", "13.2 GB  11.1 GB safe", "38.6 GB  Simulators 23.2 GB",
		"111 GB freed in 3 clean-ups since 6 Nov", "26.8 GB in the Trash · you can put it back", "6 Nov 18:22  26.8 GB  2 items to the Trash",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("box is missing %q", want)
		}
	}
}

func TestHomeBoxSpinsAndSaysWhenNothingNeedsYou(t *testing.T) {
	in := model.HomeInput{
		Junk:     model.ScanState{Available: true, Started: true, Scanning: true, Done: 2, Total: 5},
		Disk:     model.DiskState{Available: true, Started: true, Walking: true, Files: 1_200_000},
		Dupes:    model.ScanState{Available: true, Started: true, Scanning: true, Progress: "Hashing 12 of 40 sets · 3.1 GB read"},
		DiskFree: 109 * gib, DiskTotal: 460 * gib, MemoryErr: errorf("no"), SocketErr: errorf("no"), HistoryAvailable: true, HistoryRead: true,
	}
	lines := drawHome(buildHomeView(in), 80, 16, homeSel{cursor: "free/dev", frame: 2})
	checkBox(t, lines, 80, 16)
	s := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{
		"│ ✓ Nothing needs you now.", "╭─ Needs attention ───", "│ 4 Junk  ⠹ scanning · 2 of 5 checks", "│ 6 Disk  ⠹ walking 1.2M files", "│ 7 Dupes ⠹ finding copies",
		"│ Mem  Couldn't read memory", "│ Couldn't read sockets", "│ Nothing cleaned yet.", "│ 8 Apps  not available",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("box is missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(lines[4], "\x1b[7m") {
		t.Errorf("the selected line is reversed: %q", lines[4])
	}
	if strings.Contains(lines[0], "found") {
		t.Errorf("no count without findings: %q", ansi.Strip(lines[0]))
	}
}

func errorf(s string) error { return errors.New(s) }

func TestHomeBoxFitsEveryWindow(t *testing.T) {
	many := macInput()
	many.Top = nil
	many.Listeners = nil
	for i := range 12 {
		name := strings.Repeat("長いアプリの名前", 3) + strings.Repeat("x", 30)
		many.Top = append(many.Top, model.AppUsage{Name: name, Memory: uint64(i+1) << 28, CPU: 99})
		many.Listeners = append(many.Listeners, model.Listener{Port: uint16(3000 + i), Process: name, Bind: "127.0.0.1, ::1, 192.168.100.200"})
		many.Groups = append(many.Groups, model.ConnGroup{App: name, Rate: model.Rate{Rx: 1 << 30, Tx: 1 << 30}, Conns: make([]model.Conn, 9)})
		many.Junk.Items = append(many.Junk.Items, model.CleanItem{ID: "x" + name, Screen: model.ScreenJunk, Group: name, Size: uint64(i+1) << 30, Safe: true})
		many.Dev.Items = append(many.Dev.Items, model.CleanItem{ID: "y" + name, Screen: model.ScreenDev, Group: name, Size: uint64(i+1) << 30})
	}
	failing := model.HomeInput{MemoryErr: errorf("no"), SocketErr: errorf("no"), ProcessErr: errorf("no"), HistoryAvailable: true}
	inputs := map[string]model.HomeInput{"mac": macInput(), "many": many, "errors": failing}
	for name, in := range inputs {
		v := buildHomeView(in)
		for w := 80; w <= 130; w += 5 {
			for h := 16; h <= 30; h++ {
				lines := drawHome(v, w, h, homeSel{cursor: keyHistory, frame: -3})
				if want := model.HomeRows(h, len(v.findings)).Lines(); len(lines) != want {
					t.Fatalf("%s %d×%d: %d lines, want %d", name, w, h, len(lines), want)
				}
				checkBox(t, lines, w, len(lines))
			}
		}
	}
}
