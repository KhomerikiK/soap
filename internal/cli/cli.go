// Package cli is soap's command-line mode. It also wires the real system readers together.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

// Deps are what the commands need. RealDeps builds the production set; tests build fakes.
type Deps struct {
	Collect     func(ctx context.Context, opt engine.Options) engine.Snapshot
	Actions     *actions.Service
	UID         int
	RunTUI      func() error
	Interactive func(cmd *exec.Cmd) error // runs a command attached to the terminal, for sudo
	OwnerDBPath string
	OwnerDBURL  string
	HTTP        *http.Client
	Sleep       func(time.Duration)
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer

	// Scan, Clean, History and Restore back junk, dev, dupes, apps, clean, history and restore.
	Scan    func(ctx context.Context, screen model.Screen) <-chan model.ScanUpdate
	Clean   func(ctx context.Context, items []model.CleanItem, report func(model.CleanResult)) error
	History func() ([]model.HistoryEntry, error)
	Restore func(id string) (model.HistoryEntry, error)

	// Home, Walk, List and Remeasure back disk and the Disk screen. DiskItem turns an ID that
	// disk printed back into its item for clean; a path that's gone matches fs.ErrNotExist.
	Home      string
	Walk      func(ctx context.Context, tree *model.DiskTree)
	List      func(dir string) ([]model.DiskEntry, error)
	Remeasure func(ctx context.Context, tree *model.DiskTree, dirs []string)
	DiskItem  func(ctx context.Context, id string) (model.CleanItem, error)
}

// ExitError ends soap with a specific exit code and message.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Run executes soap with args and returns the exit code: 0 for success, 1 for an error
// (including a cancelled confirmation), 2 when nothing matched.
func Run(d *Deps, args []string) int {
	root := newRoot(d)
	root.SetArgs(args)
	root.SetIn(d.Stdin)
	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		if ee.Msg != "" {
			fmt.Fprintln(d.Stderr, ee.Msg)
		}
		return ee.Code
	}
	fmt.Fprintln(d.Stderr, "soap:", err)
	return 1
}

func newRoot(d *Deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "soap",
		Short:         "See what's running on your Mac: ports, connections and memory",
		Long:          "Run soap with no arguments to open the terminal UI. The subcommands print the same data for scripts.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return d.RunTUI() },
	}
	root.AddCommand(portsCmd(d), connsCmd(d), memCmd(d), killCmd(d), updateDBCmd(d),
		junkCmd(d), devCmd(d), diskCmd(d), dupesCmd(d), appsCmd(d), cleanCmd(d), historyCmd(d), restoreCmd(d))
	return root
}
