package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

func killCmd(d *Deps) *cobra.Command {
	var force, yes bool
	c := &cobra.Command{
		Use:   "kill :<port> | <pid>",
		Short: "Stop the process on a port, or a process by PID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKill(cmd.Context(), d, bufio.NewReader(d.Stdin), args[0], force, yes)
		},
	}
	c.Flags().BoolVar(&force, "force", false, "send SIGKILL instead of SIGTERM")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return c
}

// killTarget is what kill acts on: a process, or the container behind a Docker-forwarded port.
type killTarget struct {
	id        model.ProcessID
	label     string
	container string
	port      uint16
	engine    bool // Docker or OrbStack, reached from a port row
}

func runKill(ctx context.Context, d *Deps, in *bufio.Reader, arg string, force, yes bool) error {
	t, err := resolveTarget(ctx, d, in, arg, yes)
	if err != nil {
		return err
	}
	if t.container != "" {
		return stopContainer(ctx, d, in, t, force, yes)
	}
	if t.engine {
		return &ExitError{Code: 1, Msg: model.EngineRefusal(t.port)}
	}
	if err := d.Actions.Refusal(t.id); err != nil {
		return &ExitError{Code: 1, Msg: err.Error()}
	}
	verb := "Kill"
	if force {
		verb = "Force kill"
	}
	if !yes && !confirm(in, d.Stdout, fmt.Sprintf("%s %s?", verb, t.label)) {
		return &ExitError{Code: 1, Msg: "Cancelled."}
	}
	cur, err := d.Actions.Verify(ctx, t.id)
	switch {
	case errors.Is(err, actions.ErrAlreadyExited):
		fmt.Fprintf(d.Stdout, "%s already exited.\n", t.label)
		return nil
	case err != nil:
		return &ExitError{Code: 1, Msg: err.Error()}
	case cur.UID != d.UID:
		return sudoKill(d, t, force)
	}
	outcome, err := d.Actions.Terminate(t.id.PID, force)
	switch {
	case errors.Is(err, actions.ErrNeedsSudo):
		return sudoKill(d, t, force)
	case err != nil:
		return err
	case outcome == actions.StillRunning:
		return &ExitError{Code: 1, Msg: fmt.Sprintf("%s is still running after 3 seconds. Run again with --force.", t.label)}
	}
	fmt.Fprintf(d.Stdout, "Stopped %s.\n", t.label)
	return nil
}

func stopContainer(ctx context.Context, d *Deps, in *bufio.Reader, t killTarget, force, yes bool) error {
	verb, done := "Stop", "Stopped"
	if force {
		verb, done = "Kill", "Killed"
	}
	question := fmt.Sprintf(":%d is forwarded by Docker to %s. %s that container?", t.port, t.container, verb)
	if !yes && !confirm(in, d.Stdout, question) {
		return &ExitError{Code: 1, Msg: "Cancelled."}
	}
	if err := d.Actions.StopContainer(ctx, t.container, force); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "%s container %s. Freed :%d.\n", done, t.container, t.port)
	return nil
}

func sudoKill(d *Deps, t killTarget, force bool) error {
	cmd, err := d.Actions.SudoKill(t.id.PID, force)
	if err != nil {
		return &ExitError{Code: 1, Msg: err.Error()}
	}
	fmt.Fprintf(d.Stdout, "%s belongs to another user, so sudo will ask for your password.\n", t.label)
	if err := d.Interactive(cmd); err != nil {
		return &ExitError{Code: 1, Msg: "Not killed: sudo was cancelled or failed."}
	}
	sig := "SIGTERM"
	if force {
		sig = "SIGKILL"
	}
	fmt.Fprintf(d.Stdout, "Sent %s to %s with sudo.\n", sig, t.label)
	return nil
}

// resolveTarget turns ":3000" or "48121" into a target.
func resolveTarget(ctx context.Context, d *Deps, in *bufio.Reader, arg string, yes bool) (killTarget, error) {
	if portStr, ok := strings.CutPrefix(arg, ":"); ok {
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return killTarget{}, &ExitError{Code: 1, Msg: fmt.Sprintf("%q isn't a port number.", portStr)}
		}
		return portTarget(ctx, d, in, uint16(port), yes)
	}
	pid, err := strconv.Atoi(arg)
	if err != nil || pid < 0 {
		return killTarget{}, &ExitError{Code: 1, Msg: fmt.Sprintf("%q isn't a PID or a :port.", arg)}
	}
	p, err := d.Actions.Procs.Lookup(ctx, pid)
	if errors.Is(err, model.ErrNoProcess) {
		return killTarget{}, &ExitError{Code: 2, Msg: fmt.Sprintf("No process with PID %d.", pid)}
	}
	if err != nil {
		return killTarget{}, err
	}
	return killTarget{id: p.ProcessID, label: fmt.Sprintf("%s (pid %d)", p.Name, p.PID)}, nil
}

func portTarget(ctx context.Context, d *Deps, in *bufio.Reader, port uint16, yes bool) (killTarget, error) {
	snap := d.Collect(ctx, engine.Options{})
	if snap.SocketErr != nil {
		return killTarget{}, fmt.Errorf("couldn't read sockets: %w", snap.SocketErr)
	}
	var matches []model.Listener
	for _, l := range snap.Listeners {
		if l.Port == port && !slices.ContainsFunc(matches, func(m model.Listener) bool { return m.PID == l.PID }) {
			matches = append(matches, l)
		}
	}
	switch {
	case len(matches) == 0:
		return killTarget{}, &ExitError{Code: 2, Msg: fmt.Sprintf("Nothing is listening on :%d.", port)}
	case len(matches) == 1:
		return listenerTarget(matches[0]), nil
	case yes:
		names := make([]string, len(matches))
		for i, l := range matches {
			names[i] = fmt.Sprintf("%s (pid %d)", l.Process, l.PID)
		}
		return killTarget{}, &ExitError{Code: 1, Msg: fmt.Sprintf("Several processes listen on :%d: %s. Pass a PID instead.", port, strings.Join(names, ", "))}
	}
	l, ok := choose(in, d.Stdout, port, matches)
	if !ok {
		return killTarget{}, &ExitError{Code: 1, Msg: "Cancelled."}
	}
	return listenerTarget(l), nil
}

func listenerTarget(l model.Listener) killTarget {
	return killTarget{id: l.ID, label: fmt.Sprintf("%s (pid %d) on :%d", l.Process, l.PID, l.Port), container: l.Container, port: l.Port, engine: l.Engine}
}

func choose(in *bufio.Reader, out io.Writer, port uint16, ls []model.Listener) (model.Listener, bool) {
	fmt.Fprintf(out, "Several processes listen on :%d:\n", port)
	for i, l := range ls {
		fmt.Fprintf(out, "  %d) %s (pid %d)\n", i+1, l.Process, l.PID)
	}
	fmt.Fprintf(out, "Which one? [1-%d] ", len(ls))
	line, _ := in.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(ls) {
		return model.Listener{}, false
	}
	return ls[n-1], true
}

func confirm(in *bufio.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := in.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
