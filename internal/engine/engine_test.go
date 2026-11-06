package engine

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	ap  = netip.MustParseAddrPort
)

type fakeSockets struct {
	socks []model.Socket
	err   error
}

func (f *fakeSockets) Sockets(context.Context) ([]model.Socket, error) { return f.socks, f.err }

type fakeProcs struct {
	procs   []model.Process
	err     error
	argPIDs []int
}

func (f *fakeProcs) Processes(context.Context) ([]model.Process, error) { return f.procs, f.err }

func (f *fakeProcs) Args(_ context.Context, pids []int) (map[int]string, error) {
	f.argPIDs = pids
	return map[int]string{48121: "node server.js"}, nil
}

type fakeMemory struct {
	st  model.MemoryStats
	err error
}

func (f fakeMemory) Stats(context.Context) (model.MemoryStats, error) { return f.st, f.err }

type fakeDocker struct {
	calls int
	cs    []model.Container
	err   error
}

func (f *fakeDocker) Containers(context.Context) ([]model.Container, error) {
	f.calls++
	return f.cs, f.err
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fixture struct {
	socks  *fakeSockets
	procs  *fakeProcs
	docker *fakeDocker
	clock  *fakeClock
	eng    *Engine
}

func newFixture() *fixture {
	f := &fixture{
		socks: &fakeSockets{socks: []model.Socket{
			{Proto: "tcp4", Local: ap("127.0.0.1:3000"), State: "LISTEN", PID: 48121, Command: "node"},
			{Proto: "tcp4", Local: ap("10.0.0.2:50000"), Remote: ap("3.120.44.10:443"), State: "ESTABLISHED", PID: 500, RxBytes: 1000},
		}},
		procs: &fakeProcs{procs: []model.Process{
			{ProcessID: model.ProcessID{PID: 48121, Name: "node", StartTime: t0}, UID: 501, Path: "/usr/local/bin/node", Memory: 100},
			{ProcessID: model.ProcessID{PID: 500, Name: "Slack", StartTime: t0}, UID: 501, Path: "/Applications/Slack.app/Contents/MacOS/Slack", Memory: 900},
		}},
		docker: &fakeDocker{err: model.ErrUnavailable},
		clock:  &fakeClock{now: t0},
	}
	f.eng = New(Sources{
		Sockets:    f.socks,
		Processes:  f.procs,
		Memory:     fakeMemory{st: model.MemoryStats{Total: 18 << 30, Used: 11 << 30, Pressure: model.PressureNormal}},
		Containers: f.docker,
		CPUTicks:   func() (uint64, uint64, error) { return 0, 0, errors.New("no CPU in tests") },
		Interfaces: func() (map[string]model.IfCounters, error) { return nil, errors.New("no interfaces in tests") },
		Now:        f.clock.Now,
	})
	return f
}

func TestCollectBuildsEveryView(t *testing.T) {
	f := newFixture()
	snap := f.eng.Collect(ctx, Options{})
	if snap.SocketErr != nil || snap.ProcessErr != nil || snap.MemoryErr != nil || snap.DockerErr != nil {
		t.Fatalf("errors: %v %v %v %v", snap.SocketErr, snap.ProcessErr, snap.MemoryErr, snap.DockerErr)
	}
	if len(snap.Listeners) != 1 || snap.Listeners[0].Process != "node" || snap.Listeners[0].Note != "server" {
		t.Errorf("listeners = %+v", snap.Listeners)
	}
	if len(snap.Groups) != 1 || snap.Groups[0].App != "Slack" {
		t.Errorf("groups = %+v", snap.Groups)
	}
	if len(snap.Apps) != 2 || snap.Apps[0].Name != "Slack" {
		t.Errorf("apps = %+v", snap.Apps)
	}
	if snap.Memory.Total != 18<<30 {
		t.Errorf("memory = %+v", snap.Memory)
	}
	if !slices.Equal(f.procs.argPIDs, []int{48121}) {
		t.Errorf("asked for args of %v, want only the listener's PID", f.procs.argPIDs)
	}
}

func TestCollectSocketFailureKeepsOtherViews(t *testing.T) {
	f := newFixture()
	f.socks.err = errors.New("netstat broke")
	snap := f.eng.Collect(ctx, Options{})
	if snap.SocketErr == nil || snap.Listeners != nil || snap.Groups != nil {
		t.Errorf("socket views should be empty with an error: %+v", snap)
	}
	if len(snap.Apps) != 2 || snap.Memory.Total == 0 {
		t.Errorf("memory views should still work: %+v", snap)
	}
}

func TestCollectComputesRatesBetweenSnapshots(t *testing.T) {
	f := newFixture()
	f.eng.Collect(ctx, Options{})
	f.clock.now = t0.Add(2 * time.Second)
	f.socks.socks[1].RxBytes = 3048
	snap := f.eng.Collect(ctx, Options{})
	if got := snap.Groups[0].Rate.Rx; got != 1024 {
		t.Errorf("download rate = %v, want 1024", got)
	}
}

func TestCollectReadsDockerAtMostEvery10Seconds(t *testing.T) {
	f := newFixture()
	f.docker.err = nil
	f.docker.cs = []model.Container{{Name: "web-1", Ports: []model.PublishedPort{{HostPort: 3000, Proto: "tcp"}}}}
	if snap := f.eng.Collect(ctx, Options{}); snap.Listeners[0].Container != "web-1" {
		t.Fatalf("listener = %+v", snap.Listeners[0])
	}
	f.clock.now = t0.Add(2 * time.Second)
	if snap := f.eng.Collect(ctx, Options{}); snap.Listeners[0].Container != "web-1" {
		t.Errorf("cached containers should still apply: %+v", snap.Listeners[0])
	}
	if f.docker.calls != 1 {
		t.Errorf("docker read %d times within 10 seconds, want 1", f.docker.calls)
	}
	f.clock.now = t0.Add(11 * time.Second)
	f.eng.Collect(ctx, Options{})
	if f.docker.calls != 2 {
		t.Errorf("docker read %d times after 11 seconds, want 2", f.docker.calls)
	}
}

func TestCollectDockerErrors(t *testing.T) {
	if snap := newFixture().eng.Collect(ctx, Options{}); snap.DockerErr != nil {
		t.Errorf("missing Docker isn't an error, got %v", snap.DockerErr)
	}
	f := newFixture()
	f.docker.err = errors.New("docker timed out after 15s")
	if snap := f.eng.Collect(ctx, Options{}); snap.DockerErr == nil {
		t.Error("a Docker timeout should be reported")
	}
}

func TestCollectMarksNewApps(t *testing.T) {
	f := newFixture()
	f.eng.Collect(ctx, Options{})
	f.socks.socks = append(f.socks.socks, model.Socket{Proto: "tcp4", Local: ap("10.0.0.2:50001"), Remote: ap("1.1.1.1:443"), State: "ESTABLISHED", PID: 48121})
	snap := f.eng.Collect(ctx, Options{})
	for _, g := range snap.Groups {
		if (g.App == "node") != g.New {
			t.Errorf("%s: New = %v", g.App, g.New)
		}
	}
}

func TestCollectOptions(t *testing.T) {
	f := newFixture()
	f.socks.socks = append(f.socks.socks, model.Socket{Proto: "tcp4", Local: ap("127.0.0.1:50002"), Remote: ap("127.0.0.1:3306"), State: "ESTABLISHED", PID: 500})
	if n := len(f.eng.Collect(ctx, Options{}).Groups[0].Conns); n != 1 {
		t.Errorf("without loopback: %d conns, want 1", n)
	}
	if n := len(f.eng.Collect(ctx, Options{Loopback: true}).Groups[0].Conns); n != 2 {
		t.Errorf("with loopback: %d conns, want 2", n)
	}
	raw := f.eng.Collect(ctx, Options{RawProcesses: true})
	if len(raw.Apps) != 2 || raw.Apps[0].AppPath != "" {
		t.Errorf("raw apps = %+v", raw.Apps)
	}
}

func TestCollectReadsDiskSpace(t *testing.T) {
	f := newFixture()
	if snap := f.eng.Collect(ctx, Options{}); snap.DiskTotal != 0 {
		t.Errorf("no Disk source: %+v", snap)
	}
	f.eng.src.Disk = func() (uint64, uint64, error) { return 43 << 30, 460 << 30, nil }
	snap := f.eng.Collect(ctx, Options{})
	if snap.DiskFree != 43<<30 || snap.DiskTotal != 460<<30 {
		t.Errorf("disk = %d / %d", snap.DiskFree, snap.DiskTotal)
	}
}
