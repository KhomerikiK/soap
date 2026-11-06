package sys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestParsePS(t *testing.T) {
	procs, err := ParsePS(readFixture(t, "ps.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 4 {
		t.Fatalf("got %d processes, want 4", len(procs))
	}
	k := procs[0]
	if k.PID != 0 || k.Name != "kernel_task" || k.Path != "" || k.UID != 0 {
		t.Errorf("kernel_task = %+v", k)
	}
	chrome := procs[3]
	wantPath := "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/142.0.7444.60/Helpers/Google Chrome Helper.app/Contents/MacOS/Google Chrome Helper"
	if chrome.Path != wantPath {
		t.Errorf("Path = %q", chrome.Path)
	}
	if chrome.Name != "Google Chrome Helper" || chrome.PID != 70396 || chrome.PPID != 1234 || chrome.UID != 501 {
		t.Errorf("chrome = %+v", chrome)
	}
	if chrome.CPU != 6.1 || chrome.Memory != 307200*1024 {
		t.Errorf("CPU = %v, Memory = %v", chrome.CPU, chrome.Memory)
	}
	wantStart := time.Date(2026, 11, 3, 18, 22, 5, 0, time.Local)
	if !chrome.StartTime.Equal(wantStart) {
		t.Errorf("StartTime = %v, want %v", chrome.StartTime, wantStart)
	}
}

func TestParsePSRejectsShortRow(t *testing.T) {
	_, err := ParsePS([]byte("  12  1  501  0.0\n"))
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want *FormatError", err)
	}
}

func TestParsePSArgs(t *testing.T) {
	got := ParsePSArgs(readFixture(t, "ps_args.txt"))
	want := map[int]string{
		48121: "/usr/local/bin/node /Users/me/app/node_modules/.bin/next dev",
		48210: "node /Users/me/web/node_modules/vite/bin/vite.js --host ::1",
		901:   "php-fpm: master process (/opt/homebrew/etc/php/8.3/php-fpm.conf)",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for pid, args := range want {
		if got[pid] != args {
			t.Errorf("pid %d = %q, want %q", pid, got[pid], args)
		}
	}
}

func TestPSSourceUsesFootprintAndUserNames(t *testing.T) {
	fixture := readFixture(t, "ps.txt")
	src := &PSSource{
		Run: func(context.Context, string, ...string) ([]byte, error) { return fixture, nil },
		Footprint: func(pid int) (uint64, bool) {
			if pid == 714 {
				return 999, true
			}
			return 0, false
		},
	}
	procs, err := src.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if procs[2].Memory != 999 {
		t.Errorf("Finder memory = %d, want the footprint 999", procs[2].Memory)
	}
	if procs[1].Memory != 4784*1024 {
		t.Errorf("launchd memory = %d, want RSS", procs[1].Memory)
	}
	if procs[1].User != "root" {
		t.Errorf("uid 0 user = %q, want root", procs[1].User)
	}
}

func TestPSSourceLookupMissingProcess(t *testing.T) {
	src := &PSSource{Run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, &CommandError{Name: "ps", Err: errors.New("exit status 1")}
	}}
	if _, err := src.Lookup(context.Background(), 424242); !errors.Is(err, model.ErrNoProcess) {
		t.Fatalf("got %v, want ErrNoProcess", err)
	}
}

func TestPSSourceArgsAskForListedPIDs(t *testing.T) {
	var got []string
	src := &PSSource{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return readFixture(t, "ps_args.txt"), nil
	}}
	args, err := src.Args(context.Background(), []int{48121, 901})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "ps -ww -o pid=,args= -p 48121,901" {
		t.Errorf("ran %v", got)
	}
	if args[901] == "" {
		t.Error("missing args for 901")
	}
	if none, err := src.Args(context.Background(), nil); err != nil || len(none) != 0 {
		t.Errorf("Args(nil) = %v, %v", none, err)
	}
}

func TestPSSourceLive(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ps")
	}
	src := &PSSource{Run: ExecRunner(5 * time.Second), Footprint: Footprint}
	procs, err := src.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	found := false
	for _, p := range procs {
		if p.PID == self {
			found = true
			if p.Name != filepath.Base(os.Args[0]) {
				t.Errorf("own name = %q, want %q", p.Name, filepath.Base(os.Args[0]))
			}
		}
	}
	if !found {
		t.Fatal("own process not listed")
	}
	p, err := src.Lookup(context.Background(), self)
	if err != nil || p.PID != self {
		t.Fatalf("Lookup(self) = %+v, %v", p, err)
	}
}
