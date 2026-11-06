// Package tui is soap's terminal UI, built on Bubble Tea.
package tui

import (
	"context"
	"net/netip"
	"os/exec"
	"time"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

// Actions are the system changes the TUI can ask for. *actions.Service implements them.
type Actions interface {
	Refusal(target model.ProcessID) error
	Verify(ctx context.Context, target model.ProcessID) (model.Process, error)
	Terminate(pid int, force bool) (actions.Outcome, error)
	SudoKill(pid int, force bool) (*exec.Cmd, error)
	StopContainer(ctx context.Context, name string, force bool) error
	QuitApp(ctx context.Context, appPath string) error
}

// Deps are everything the TUI needs from the rest of soap.
type Deps struct {
	Collect  func(ctx context.Context, opt engine.Options) engine.Snapshot
	Actions  Actions
	Hostname func(ip netip.Addr) string // "" while unknown; nil turns hostnames off
	Owner    func(ip netip.Addr) string // such as "GOOGLE · US"; nil when the IP database isn't installed
	UID      int                        // the current user's UID
	MacOS    string                     // macOS version, shown with source errors
	Interval time.Duration              // refresh interval; 2 seconds when zero

	// Scan, Clean, History and Restore back the clean-up screens. With Scan nil Junk, Dev and
	// Dupes stay empty.
	Scan    func(ctx context.Context, screen model.Screen) <-chan model.ScanUpdate
	Clean   func(ctx context.Context, items []model.CleanItem, report func(model.CleanResult)) error
	History func() ([]model.HistoryEntry, error)
	Restore func(id string) (model.HistoryEntry, error)

	// Home is the folder Disk walks. Walk, List and Remeasure back the Disk screen: Walk fills a
	// tree and returns when it's done or ctx is cancelled, List reads one folder's entries, and
	// Remeasure measures folders again after a clean-up. With Walk nil Disk stays empty.
	Home      string
	Walk      func(ctx context.Context, tree *model.DiskTree)
	List      func(dir string) ([]model.DiskEntry, error)
	Remeasure func(ctx context.Context, tree *model.DiskTree, dirs []string)

	// Reveal shows a file or folder in Finder (open -R). Apps uses it for f. With Reveal nil f does
	// nothing.
	Reveal func(path string) error
}
