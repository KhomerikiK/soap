package model

import (
	"net/netip"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

func proc(pid, uid int, name, path string) Process {
	return Process{
		ProcessID: ProcessID{PID: pid, Name: name, StartTime: t0},
		UID:       uid,
		User:      map[int]string{0: "root", 501: "me"}[uid],
		Path:      path,
	}
}

func TestBuildListenersMergesIPv4AndIPv6(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{
		{Proto: "tcp4", Local: ap("127.0.0.1:631"), State: "LISTEN", PID: 90823, Command: "cupsd"},
		{Proto: "tcp6", Local: ap("[::1]:631"), State: "LISTEN", PID: 90823, Command: "cupsd"},
	}
	procs := map[int]Process{90823: proc(90823, 0, "cupsd", "/usr/sbin/cupsd")}
	ls := BuildListeners(socks, procs, nil, nil)
	if len(ls) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(ls), ls)
	}
	l := ls[0]
	if l.Proto != "tcp46" || l.Bind != "127.0.0.1, ::1" || l.Port != 631 || l.User != "root" || l.UID != 0 {
		t.Errorf("got %+v", l)
	}
	if l.ID != procs[90823].ProcessID {
		t.Errorf("ID = %+v, want the process identity from ps", l.ID)
	}
}

func TestBuildListenersWildcardsAndSorting(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{
		{Proto: "tcp46", Local: ap("[::]:60050"), State: "LISTEN", PID: 638, Command: "rapportd"},
		{Proto: "tcp4", Local: ap("127.0.0.1:3000"), State: "LISTEN", PID: 48121, Command: "node"},
		{Proto: "udp4", Local: ap("0.0.0.0:5353"), PID: 402, Command: "mDNSResponder"},
		{Proto: "tcp4", Local: ap("10.0.0.2:51727"), Remote: ap("104.18.18.125:443"), State: "ESTABLISHED", PID: 83931, Command: "Grok Bot"},
	}
	ls := BuildListeners(socks, nil, nil, nil)
	if len(ls) != 3 {
		t.Fatalf("got %d rows, want 3 (connections are left out)", len(ls))
	}
	if ls[0].Port != 3000 || ls[1].Port != 5353 || ls[2].Port != 60050 {
		t.Errorf("not sorted by port: %+v", ls)
	}
	if ls[1].Proto != "udp4" || ls[1].Bind != "*" {
		t.Errorf("udp row = %+v", ls[1])
	}
	if ls[2].Proto != "tcp46" || ls[2].Bind != "*" {
		t.Errorf("wildcard row = %+v", ls[2])
	}
}

func TestBuildListenersUnknownProcess(t *testing.T) {
	socks := []Socket{{Proto: "tcp4", Local: netip.MustParseAddrPort("127.0.0.1:47413"), State: "LISTEN", PID: 87752, Command: "ArtCraft Launche"}}
	l := BuildListeners(socks, map[int]Process{}, nil, nil)[0]
	if l.Process != "ArtCraft Launche" || l.UID != -1 || l.ID.PID != 87752 {
		t.Errorf("got %+v", l)
	}
}

func TestBuildListenersDockerAndNotes(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{
		{Proto: "tcp46", Local: ap("[::]:3306"), State: "LISTEN", PID: 912, Command: "com.docker.backe"},
		{Proto: "udp46", Local: ap("[::]:3306"), PID: 912, Command: "com.docker.backe"},
		{Proto: "tcp4", Local: ap("127.0.0.1:3000"), State: "LISTEN", PID: 48121, Command: "node"},
	}
	containers := []Container{{Name: "store-backend-mysql-1", Ports: []PublishedPort{{HostPort: 3306, Proto: "tcp"}}}}
	args := map[int]string{
		48121: "/usr/local/bin/node /Users/me/app/node_modules/.bin/next dev",
		912:   "/Applications/Docker.app/Contents/MacOS/com.docker.backend",
	}
	ls := BuildListeners(socks, nil, containers, args)
	if len(ls) != 3 {
		t.Fatalf("got %d rows, want 3", len(ls))
	}
	if ls[0].Port != 3000 || ls[0].Note != "next dev" {
		t.Errorf("node row = %+v", ls[0])
	}
	var tcpRow, udpRow Listener
	for _, l := range ls[1:] {
		if l.Proto == "tcp46" {
			tcpRow = l
		} else {
			udpRow = l
		}
	}
	if tcpRow.Container != "store-backend-mysql-1" || tcpRow.Note != "" {
		t.Errorf("tcp 3306 = %+v", tcpRow)
	}
	if udpRow.Container != "" {
		t.Errorf("udp 3306 isn't published, got %+v", udpRow)
	}
}

func TestNoteFromArgs(t *testing.T) {
	tests := map[string]string{
		"/usr/local/bin/node /Users/me/app/node_modules/.bin/next dev": "next dev",
		"node /Users/me/web/node_modules/vite/bin/vite.js --host ::1":  "vite",
		"php artisan serve --port=8000":                                "artisan serve",
		"python3 -m http.server 8000":                                  "http.server 8000",
		"npx vite":                                                     "vite",
		"/usr/sbin/cupsd -l":                                           "",
		"node":                                                         "",
		"":                                                             "",
	}
	for args, want := range tests {
		if got := NoteFromArgs(args); got != want {
			t.Errorf("NoteFromArgs(%q) = %q, want %q", args, got, want)
		}
	}
}

func TestIsContainerEngine(t *testing.T) {
	if !IsContainerEngine(proc(912, 501, "com.docker.backend", "/Applications/Docker.app/Contents/MacOS/com.docker.backend")) {
		t.Error("Docker Desktop's backend is a container engine")
	}
	if !IsContainerEngine(proc(913, 501, "OrbStack Helper", "/Applications/OrbStack.app/Contents/MacOS/OrbStack Helper")) {
		t.Error("OrbStack is a container engine")
	}
	if IsContainerEngine(proc(48121, 501, "node", "/usr/local/bin/node")) {
		t.Error("node isn't a container engine")
	}
}

func TestBuildListenersContainerOnlyOnEngineRows(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{
		{Proto: "tcp4", Local: ap("127.0.0.1:5432"), State: "LISTEN", PID: 700, Command: "postgres"},
		{Proto: "tcp46", Local: ap("[::]:5432"), State: "LISTEN", PID: 912, Command: "com.docker.backe"},
	}
	procs := map[int]Process{
		700: proc(700, 501, "postgres", "/opt/homebrew/bin/postgres"),
		912: proc(912, 501, "com.docker.backend", "/Applications/Docker.app/Contents/MacOS/com.docker.backend"),
	}
	containers := []Container{{Name: "pg-1", Ports: []PublishedPort{{HostPort: 5432, Proto: "tcp"}}}}
	ls := BuildListeners(socks, procs, containers, map[int]string{700: "node /x/server.js"})
	if len(ls) != 2 {
		t.Fatalf("got %d rows: %+v", len(ls), ls)
	}
	native, engine := ls[0], ls[1]
	if native.PID != 700 || native.Container != "" || native.Engine {
		t.Errorf("native row = %+v", native)
	}
	if engine.PID != 912 || engine.Container != "pg-1" || !engine.Engine {
		t.Errorf("engine row = %+v", engine)
	}

	// Without an engine row on the port, every row keeps getting the container.
	ls = BuildListeners(socks[:1], procs, containers, nil)
	if ls[0].Container != "pg-1" || ls[0].Engine {
		t.Errorf("no engine on the port: %+v", ls[0])
	}
}

func TestBuildListenersMarksMacOSProcesses(t *testing.T) {
	ap := netip.MustParseAddrPort
	socks := []Socket{
		{Proto: "tcp46", Local: ap("[::]:7000"), State: "LISTEN", PID: 600, Command: "ControlCenter"},
		{Proto: "tcp4", Local: ap("127.0.0.1:5432"), State: "LISTEN", PID: 700, Command: "postgres"},
		{Proto: "tcp4", Local: ap("127.0.0.1:9999"), State: "LISTEN", PID: 800, Command: "gone"},
	}
	procs := map[int]Process{
		600: proc(600, 501, "ControlCenter", "/System/Library/CoreServices/ControlCenter.app/Contents/MacOS/ControlCenter"),
		700: proc(700, 501, "postgres", "/opt/homebrew/opt/postgresql@14/bin/postgres"),
	}
	system := map[uint16]bool{}
	for _, l := range BuildListeners(socks, procs, nil, nil) {
		system[l.Port] = l.System
	}
	if !system[7000] || system[5432] || system[9999] {
		t.Errorf("System = %v; only ControlCenter ships with macOS, and a process ps didn't see isn't known to", system)
	}
}
