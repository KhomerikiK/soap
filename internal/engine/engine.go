// Package engine reads every data source once per refresh and builds the views that the TUI and
// the CLI show. It keeps the state that rates need between refreshes.
package engine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// dockerInterval is how often Docker is asked for containers. docker ps is slower than the other
// readers, so it runs at most this often.
const dockerInterval = 10 * time.Second

// Sources are the readers the engine collects from. Every field must be set.
type Sources struct {
	Sockets interface {
		Sockets(ctx context.Context) ([]model.Socket, error)
	}
	Processes interface {
		Processes(ctx context.Context) ([]model.Process, error)
		Args(ctx context.Context, pids []int) (map[int]string, error)
	}
	Memory interface {
		Stats(ctx context.Context) (model.MemoryStats, error)
	}
	Containers interface {
		Containers(ctx context.Context) ([]model.Container, error)
	}
	CPUTicks   func() (busy, total uint64, err error)
	Interfaces func() (map[string]model.IfCounters, error)
	Now        func() time.Time
	// Disk reports free and total bytes of the home folder's volume. Optional: nil means no figure.
	Disk func() (free, total uint64, err error)
}

// Options change what Collect builds.
type Options struct {
	Loopback     bool // include loopback connections
	RawProcesses bool // one row per process instead of grouping by app
}

// Snapshot is everything the screens and CLI commands show, read at one moment.
type Snapshot struct {
	Taken     time.Time
	Listeners []model.Listener
	Groups    []model.ConnGroup
	Apps      []model.AppUsage
	Memory    model.MemoryStats
	CPU       float64    // percent of the whole machine
	Net       model.Rate // all interfaces except loopback
	DiskFree  uint64     // 0 with DiskTotal 0 when unknown
	DiskTotal uint64

	SocketErr  error
	ProcessErr error
	MemoryErr  error
	DockerErr  error // set only when Docker is installed but failed; nil when it's simply absent
}

// Engine collects snapshots.
type Engine struct {
	src Sources

	mu         sync.Mutex
	traffic    model.TrafficTracker
	cpu        model.CPUTracker
	net        model.NetTracker
	baseline   model.Baseline
	containers []model.Container
	dockerErr  error
	dockerAt   time.Time
}

// New returns an engine that reads from src.
func New(src Sources) *Engine { return &Engine{src: src} }

// Collect reads every source once. A failing source only sets its own error field; the rest of
// the snapshot is still filled in.
func (e *Engine) Collect(ctx context.Context, opt Options) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.src.Now()
	snap := Snapshot{Taken: now}

	procs, err := e.src.Processes.Processes(ctx)
	snap.ProcessErr = err
	byPID := make(map[int]model.Process, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	snap.Apps = model.GroupByApp(procs, opt.RawProcesses)

	e.refreshContainers(ctx, now)
	snap.DockerErr = e.dockerErr

	socks, err := e.src.Sockets.Sockets(ctx)
	snap.SocketErr = err
	if err == nil {
		args, _ := e.src.Processes.Args(ctx, listenerPIDs(socks))
		rates := e.traffic.Update(socks, now)
		snap.Listeners = model.BuildListeners(socks, byPID, e.containers, args)
		snap.Groups = model.BuildConnGroups(socks, byPID, rates, opt.Loopback)
		e.baseline.Mark(snap.Groups)
	}

	snap.Memory, snap.MemoryErr = e.src.Memory.Stats(ctx)
	if busy, total, err := e.src.CPUTicks(); err == nil {
		snap.CPU = e.cpu.Update(busy, total)
	}
	if ifs, err := e.src.Interfaces(); err == nil {
		snap.Net = e.net.Update(ifs, now)
	}
	if e.src.Disk != nil {
		if free, total, err := e.src.Disk(); err == nil {
			snap.DiskFree, snap.DiskTotal = free, total
		}
	}
	return snap
}

func (e *Engine) refreshContainers(ctx context.Context, now time.Time) {
	if !e.dockerAt.IsZero() && now.Sub(e.dockerAt) < dockerInterval {
		return
	}
	e.dockerAt = now
	cs, err := e.src.Containers.Containers(ctx)
	switch {
	case err == nil:
		e.containers, e.dockerErr = cs, nil
	case errors.Is(err, model.ErrUnavailable):
		e.containers, e.dockerErr = nil, nil
	default:
		e.containers, e.dockerErr = nil, err
	}
}

func listenerPIDs(socks []model.Socket) []int {
	var pids []int
	for _, s := range socks {
		if s.IsListener() && !slices.Contains(pids, s.PID) {
			pids = append(pids, s.PID)
		}
	}
	return pids
}
