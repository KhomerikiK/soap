package cli

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
	"github.com/KhomerikiK/soap/internal/sys/scan"
	"github.com/KhomerikiK/soap/internal/sys/trash"
	"github.com/KhomerikiK/soap/internal/tui"
)

// geteuid is the effective user ID; tests replace it.
var geteuid = os.Geteuid

// trashMove moves a path to the Trash. Tests replace it, so they never use the real Trash.
var trashMove = trash.Move

const rootRefusal = "Don't run clean-ups with sudo: soap would leave root-owned files in your home folder."

// processLister lists the running processes.
type processLister func(ctx context.Context) ([]model.Process, error)

// runningForClean returns the names of the running processes, or an error when the list can't be
// read, because then nothing can be known to be quit.
func runningForClean(ctx context.Context, list processLister) (map[string]bool, error) {
	procs, err := list(ctx)
	if err != nil {
		return nil, errors.New("Couldn't check which apps are running, so nothing was cleaned")
	}
	return model.RunningNames(procs), nil
}

// scanChecked runs a scan with the running process names. When the process list can't be read,
// every item that belongs to an app is blocked, so it can't be ticked. The returned channel
// always closes, and the scan's channel is always drained, even if nobody reads the result.
func scanChecked(ctx context.Context, list processLister, scanFn func(running map[string]bool) <-chan model.ScanUpdate) <-chan model.ScanUpdate {
	procs, err := list(ctx)
	if err == nil {
		return scanFn(model.RunningNames(procs))
	}
	in := scanFn(nil)
	out := make(chan model.ScanUpdate)
	go func() {
		defer close(out)
		sending := true
		for u := range in {
			items := make([]model.CleanItem, len(u.Items))
			copy(items, u.Items)
			for i := range items {
				if items[i].App != "" || items[i].Bundle != "" {
					items[i].Blocked = "Couldn't check whether " + items[i].BusyName() + " is running"
				}
			}
			u.Items = items
			if !sending {
				continue
			}
			select {
			case out <- u:
			case <-ctx.Done():
				sending = false
			}
		}
	}()
	return out
}

// RealDeps wires soap to the real system and the user's home folder.
func RealDeps() (*Deps, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return HomeDeps(home, true)
}

// appFoldersFor are the folders Apps lists: /Applications and <home>/Applications, or only the
// home folder's with tools off. No folder is listed twice, so an app is never found twice.
func appFoldersFor(home string, tools bool) []string {
	all := model.AppFolders(home)
	if !tools {
		all = []string{filepath.Join(home, "Applications")}
	}
	var out []string
	for _, f := range all {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// HomeDeps wires soap to the real system with the given home folder. With tools false no tool
// command (docker, xcrun, go, brew, pnpm, mdls, mdfind) is found or run, and Apps looks only in
// the home folder's Applications, so tests can use a fake home folder without touching anything
// real.
func HomeDeps(home string, tools bool) (*Deps, error) {
	home = filepath.Clean(home)
	run := sys.ExecRunner(5 * time.Second)
	dockerRun := sys.ExecRunner(15 * time.Second) // docker stop and docker kill only
	quitRun := sys.ExecRunner(2 * time.Minute)    // osascript waits on permission prompts and "Save changes?" dialogs
	scanRun := sys.ExecRunner(30 * time.Second)
	cleanRun := sys.ExecRunner(10 * time.Minute)
	var lookPath func(string) (string, error) // nil means exec.LookPath
	appFolders := appFoldersFor(home, tools)
	if !tools {
		lookPath = func(string) (string, error) { return "", errors.New("tools are turned off") }
		cleanRun = func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("tools are turned off")
		}
	}
	procs := &sys.PSSource{Run: run, Footprint: sys.Footprint}
	eng := engine.New(engine.Sources{
		Sockets:    sys.NetstatSource{Run: run},
		Processes:  procs,
		Memory:     sys.MachMemory{},
		Containers: sys.DockerSource{Run: run}, // 5 s: a hung Docker mustn't stall refreshes for long
		CPUTicks:   sys.CPUTicks,
		Interfaces: sys.InterfaceCounters,
		Now:        time.Now,
		Disk:       func() (uint64, uint64, error) { return sys.DiskFree(home) },
	})
	dbPath, err := sys.DefaultOwnerDBPath()
	if err != nil {
		return nil, err
	}
	history := &actions.History{Path: actions.HistoryPath(home), Home: home}
	d := &Deps{
		Collect: eng.Collect,
		Actions: &actions.Service{
			Procs: procs, Signal: syscall.Kill, Sleep: time.Sleep,
			Run: quitRun, DockerRun: dockerRun, Self: os.Getpid(),
		},
		UID: os.Getuid(),
		Interactive: func(c *exec.Cmd) error {
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
		OwnerDBPath: dbPath,
		OwnerDBURL:  sys.OwnerDBURL,
		HTTP:        &http.Client{Timeout: 2 * time.Minute},
		Sleep:       time.Sleep,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Scan: func(ctx context.Context, s model.Screen) <-chan model.ScanUpdate {
			cfg, err := scan.LoadConfig(home)
			if err != nil {
				ch := make(chan model.ScanUpdate, 1)
				ch <- model.ScanUpdate{Screen: s, Finder: "Config", Err: err, Done: 1, Total: 1}
				close(ch)
				return ch
			}
			env := scan.Env{Home: home, Run: scanRun, LookPath: lookPath, Sizer: scan.NewSizer(), Now: time.Now, Config: cfg,
				Apps: appFolders, UID: os.Getuid()}
			return scanChecked(ctx, procs.Processes, func(running map[string]bool) <-chan model.ScanUpdate {
				return scan.Run(ctx, env, scan.Finders(s), running)
			})
		},
		Clean: func(ctx context.Context, items []model.CleanItem, report func(model.CleanResult)) error {
			if geteuid() == 0 {
				return errors.New(rootRefusal)
			}
			cfg, err := scan.LoadConfig(home)
			if err != nil {
				return err
			}
			cleaner := &actions.Cleaner{
				Home: home, ProjectRoots: cfg.ProjectRoots, Run: cleanRun, Trash: trashMove,
				Measure:  func(ctx context.Context, p string) uint64 { return scan.NewSizer().Measure(ctx, p).Bytes },
				DiskFree: func() (uint64, error) { free, _, err := sys.DiskFree(home); return free, err },
				History:  history, Now: time.Now, IsPackage: scan.IsPackage, Stamp: scan.StampPath,
				AppCheck: scan.CheckApp(scanRun), // scanRun has a timeout, so a hung plutil can't stall the clean-up
			}
			running, err := runningForClean(ctx, procs.Processes)
			if err != nil {
				return err
			}
			return cleaner.Clean(ctx, items, running, report)
		},
		History: history.List,
		Restore: func(id string) (model.HistoryEntry, error) {
			if geteuid() == 0 {
				return model.HistoryEntry{}, errors.New(rootRefusal)
			}
			return history.Restore(id)
		},
		Home:      home,
		Walk:      scan.Walk,
		List:      scan.List,
		Remeasure: scan.Remeasure,
		DiskItem: func(ctx context.Context, id string) (model.CleanItem, error) {
			return scan.DiskItem(ctx, home, id)
		},
	}
	d.RunTUI = func() error { return runTUI(d, run) }
	return d, nil
}

func runTUI(d *Deps, run sys.Runner) error {
	resolver := sys.NewResolver()
	owners := &ownerLookup{}
	deps := tui.Deps{
		Collect:   d.Collect,
		Actions:   d.Actions,
		Hostname:  resolver.Name,
		UID:       d.UID,
		MacOS:     macOSVersion(run),
		Scan:      d.Scan,
		Clean:     d.Clean,
		History:   d.History,
		Restore:   d.Restore,
		Home:      d.Home,
		Walk:      d.Walk,
		List:      d.List,
		Remeasure: d.Remeasure,
		Reveal:    func(path string) error { return sys.Reveal(context.Background(), run, path) },
	}
	_, statErr := os.Stat(d.OwnerDBPath)
	haveDB := statErr == nil
	if haveDB {
		deps.Owner = owners.lookup
	}
	p := tea.NewProgram(tui.New(deps))
	resolver.OnResolve = func() { p.Send(tui.Redraw()) }
	owners.onLoad = func() { p.Send(tui.Redraw()) }
	if haveDB {
		go owners.load(d.OwnerDBPath)
	}
	_, err := p.Run()
	return err
}

// ownerLookup loads the IP owner database in the background. Lookups return "" until it's ready.
type ownerLookup struct {
	mu     sync.RWMutex
	db     *sys.OwnerDB
	onLoad func()
}

func (o *ownerLookup) load(path string) {
	db, err := sys.LoadOwnerDB(path)
	if err != nil {
		return
	}
	o.mu.Lock()
	o.db = db
	o.mu.Unlock()
	if o.onLoad != nil {
		o.onLoad()
	}
}

func (o *ownerLookup) lookup(ip netip.Addr) string {
	o.mu.RLock()
	db := o.db
	o.mu.RUnlock()
	if db == nil {
		return ""
	}
	owner, ok := db.Lookup(ip)
	if !ok {
		return ""
	}
	return owner.Name + " · " + owner.Country
}

func macOSVersion(run sys.Runner) string {
	out, err := run(context.Background(), "sw_vers", "-productVersion")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
