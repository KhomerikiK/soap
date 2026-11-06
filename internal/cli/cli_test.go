package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

const me = 501

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

func id(pid int, name string) model.ProcessID {
	return model.ProcessID{PID: pid, Name: name, StartTime: t0}
}

type fakeLookup map[int]model.Process

func (f fakeLookup) Lookup(_ context.Context, pid int) (model.Process, error) {
	p, ok := f[pid]
	if !ok {
		return model.Process{}, model.ErrNoProcess
	}
	return p, nil
}

func testSnapshot() engine.Snapshot {
	return engine.Snapshot{
		Listeners: []model.Listener{
			{Port: 3000, Proto: "tcp4", PID: 48121, Process: "node", User: "me", UID: me, Bind: "127.0.0.1", Note: "next dev", ID: id(48121, "node")},
			{Port: 3306, Proto: "tcp46", PID: 912, Process: "com.docker.backend", User: "me", UID: me, Bind: "*", Container: "store-backend-mysql-1", ID: id(912, "com.docker.backend")},
			{Port: 8080, Proto: "tcp4", PID: 700, Process: "python3", User: "me", UID: me, Bind: "*", ID: id(700, "python3")},
			{Port: 8080, Proto: "tcp6", PID: 701, Process: "ruby", User: "me", UID: me, Bind: "::1", ID: id(701, "ruby")},
			{Port: 9000, Proto: "tcp4", PID: 901, Process: "php-fpm", User: "root", UID: 0, Bind: "127.0.0.1", ID: id(901, "php-fpm")},
		},
		Groups: []model.ConnGroup{{
			App: "Slack", PIDs: []int{500}, Rate: model.Rate{Rx: 2048},
			Conns: []model.Conn{{Proto: "tcp4", Remote: netip.MustParseAddrPort("3.120.44.10:443"), State: "ESTABLISHED", PID: 500}},
		}},
		Apps:   []model.AppUsage{{Name: "Slack", PIDs: []int{500}, Memory: 817889280, CPU: 0.8}},
		Memory: model.MemoryStats{Total: 18 << 30, Used: 11 << 30, Pressure: model.PressureWarning},
	}
}

// env is the CLI with fake system access. It records what it was asked to do.
type env struct {
	d           *Deps
	snap        engine.Snapshot
	out, errOut bytes.Buffer
	alive       bool     // whether signalled processes keep running
	signals     []string // "pid:signal number"
	docker      [][]string
	interactive [][]string
}

func newEnv(t *testing.T, stdin string) *env {
	t.Helper()
	e := &env{snap: testSnapshot()}
	procs := fakeLookup{
		1:     {ProcessID: id(1, "launchd"), UID: 0},
		700:   {ProcessID: id(700, "python3"), UID: me},
		901:   {ProcessID: id(901, "php-fpm"), UID: 0},
		48121: {ProcessID: id(48121, "node"), UID: me},
	}
	svc := &actions.Service{
		Procs: procs,
		Signal: func(pid int, sig syscall.Signal) error {
			if sig == 0 {
				if e.alive {
					return nil
				}
				return syscall.ESRCH
			}
			e.signals = append(e.signals, fmt.Sprintf("%d:%d", pid, int(sig)))
			return nil
		},
		Sleep: func(time.Duration) {},
		DockerRun: func(_ context.Context, name string, args ...string) ([]byte, error) {
			e.docker = append(e.docker, append([]string{name}, args...))
			return nil, nil
		},
		Self: 99999,
	}
	e.d = &Deps{
		Collect: func(context.Context, engine.Options) engine.Snapshot { return e.snap },
		Actions: svc,
		UID:     me,
		RunTUI:  func() error { return errors.New("no TUI in tests") },
		Interactive: func(c *exec.Cmd) error {
			e.interactive = append(e.interactive, c.Args)
			return nil
		},
		Sleep:  func(time.Duration) {},
		Stdin:  strings.NewReader(stdin),
		Stdout: &e.out,
		Stderr: &e.errOut,
	}
	return e
}

func (e *env) run(args ...string) int { return Run(e.d, args) }

func TestPortsTable(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("ports"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	for _, want := range []string{"PORT", "3000", "node", "next dev", "→ store-backend-mysql-1", "php-fpm", "root"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, e.out.String())
		}
	}
}

func TestPortsJSON(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("ports", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var ls []model.Listener
	if err := json.Unmarshal(e.out.Bytes(), &ls); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if len(ls) != 5 || ls[0].Port != 3000 || ls[1].Container != "store-backend-mysql-1" {
		t.Errorf("got %+v", ls)
	}
}

func TestPortsSocketError(t *testing.T) {
	e := newEnv(t, "")
	e.snap.SocketErr = errors.New("netstat broke")
	if code := e.run("ports"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "couldn't read sockets: netstat broke") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestConnsTable(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("conns"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"APP", "Slack", "2.0 KB/s", "3.120.44.10:443"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, e.out.String())
		}
	}
}

func TestMemTable(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("mem"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Memory pressure: warning", "Used 11.0 GB of 18.0 GB", "Slack", "780 MB"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, e.out.String())
		}
	}
}

func TestKillPortWithYes(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", ":3000", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !slices.Equal(e.signals, []string{"48121:15"}) {
		t.Errorf("signals = %v, want SIGTERM to 48121", e.signals)
	}
	if !strings.Contains(e.out.String(), "Stopped node (pid 48121) on :3000.") {
		t.Errorf("stdout = %q", e.out.String())
	}
}

func TestKillAsksFirst(t *testing.T) {
	e := newEnv(t, "n\n")
	if code := e.run("kill", ":3000"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(e.signals) != 0 {
		t.Errorf("signals = %v, want none", e.signals)
	}
	if !strings.Contains(e.out.String(), "Kill node (pid 48121) on :3000? [y/N]") || !strings.Contains(e.errOut.String(), "Cancelled.") {
		t.Errorf("stdout = %q, stderr = %q", e.out.String(), e.errOut.String())
	}
	yes := newEnv(t, "y\n")
	if code := yes.run("kill", ":3000"); code != 0 || len(yes.signals) != 1 {
		t.Errorf("answering y: exit %d, signals %v", code, yes.signals)
	}
}

func TestKillNothingListening(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", ":4444", "-y"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(e.errOut.String(), "Nothing is listening on :4444.") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestKillDockerPortStopsContainer(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", ":3306", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if len(e.docker) != 1 || !slices.Equal(e.docker[0], []string{"docker", "stop", "store-backend-mysql-1"}) || len(e.signals) != 0 {
		t.Errorf("docker = %v, signals = %v", e.docker, e.signals)
	}
}

func TestKillOtherUserUsesSudo(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", ":9000", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if len(e.interactive) != 1 || !slices.Equal(e.interactive[0], []string{"sudo", "kill", "-TERM", "901"}) || len(e.signals) != 0 {
		t.Errorf("interactive = %v, signals = %v", e.interactive, e.signals)
	}
	if !strings.Contains(e.out.String(), "Sent SIGTERM to php-fpm (pid 901) on :9000 with sudo.") || strings.Contains(e.out.String(), "Stopped") {
		t.Errorf("stdout = %q", e.out.String())
	}
}

func TestKillProtected(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", "1", "-y"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "soap won't stop launchd") || len(e.signals) != 0 {
		t.Errorf("stderr = %q, signals = %v", e.errOut.String(), e.signals)
	}
}

func TestKillStillRunning(t *testing.T) {
	e := newEnv(t, "")
	e.alive = true
	if code := e.run("kill", ":3000", "-y"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "still running after 3 seconds. Run again with --force.") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestKillForceByPID(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", "48121", "--force", "-y"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !slices.Equal(e.signals, []string{"48121:9"}) {
		t.Errorf("signals = %v, want SIGKILL", e.signals)
	}
}

func TestKillUnknownPID(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", "4242", "-y"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(e.errOut.String(), "No process with PID 4242.") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
}

func TestKillSeveralProcessesOnOnePort(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("kill", ":8080", "-y"); code != 1 || !strings.Contains(e.errOut.String(), "Several processes listen on :8080") {
		t.Fatalf("exit %d, stderr = %q", code, e.errOut.String())
	}
	pick := newEnv(t, "1\ny\n")
	if code := pick.run("kill", ":8080"); code != 0 {
		t.Fatalf("exit %d: %s", code, pick.errOut.String())
	}
	if !slices.Equal(pick.signals, []string{"700:15"}) {
		t.Errorf("signals = %v, want SIGTERM to python3", pick.signals)
	}
}

func TestUpdateDB(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n"))
	zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(gz.Bytes()) }))
	defer srv.Close()
	e := newEnv(t, "")
	e.d.HTTP, e.d.OwnerDBURL = srv.Client(), srv.URL
	e.d.OwnerDBPath = filepath.Join(t.TempDir(), "soap", "ip2asn-combined.tsv.gz")
	if code := e.run("update-db"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !strings.Contains(e.out.String(), "Saved 2 IP ranges") {
		t.Errorf("stdout = %q", e.out.String())
	}
	if _, err := os.Stat(e.d.OwnerDBPath); err != nil {
		t.Errorf("database not saved: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	e := newEnv(t, "")
	if code := e.run("frobnicate"); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestKillPortOfEngineWithoutContainerIsRefused(t *testing.T) {
	e := newEnv(t, "")
	e.snap.Listeners[1].Container = ""
	e.snap.Listeners[1].Engine = true
	if code := e.run("kill", ":3306", "-y"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	want := "Docker owns :3306 but no container is known for it. Nothing was stopped."
	if !strings.Contains(e.errOut.String(), want) {
		t.Errorf("stderr = %q", e.errOut.String())
	}
	if len(e.signals) != 0 || len(e.docker) != 0 {
		t.Errorf("signals = %v, docker = %v", e.signals, e.docker)
	}
}

func TestPortsWarnsWhenDockerCannotBeRead(t *testing.T) {
	for _, args := range [][]string{{"ports"}, {"ports", "--json"}} {
		e := newEnv(t, "")
		e.snap.DockerErr = errors.New("docker timed out")
		if code := e.run(args...); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		want := "soap: couldn't read Docker containers, so container names are missing: docker timed out"
		if !strings.Contains(e.errOut.String(), want) {
			t.Errorf("%v: stderr = %q", args, e.errOut.String())
		}
	}
}
