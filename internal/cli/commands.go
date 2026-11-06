package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

// memTop is how many apps `soap mem` lists.
const memTop = 15

func portsCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "ports",
		Short: "List listening ports",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap := d.Collect(cmd.Context(), engine.Options{})
			if snap.SocketErr != nil {
				return fmt.Errorf("couldn't read sockets: %w", snap.SocketErr)
			}
			if snap.DockerErr != nil {
				fmt.Fprintf(d.Stderr, "soap: couldn't read Docker containers, so container names are missing: %v\n", snap.DockerErr)
			}
			if asJSON {
				return writeJSON(d.Stdout, nonNil(snap.Listeners))
			}
			tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PORT\tPROTO\tPID\tPROCESS\tUSER\tBIND\tNOTE")
			for _, l := range snap.Listeners {
				note := l.Note
				if l.Container != "" {
					note = "→ " + l.Container
				}
				fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\t%s\n", l.Port, l.Proto, l.PID, l.Process, l.User, l.Bind, note)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

func connsCmd(d *Deps) *cobra.Command {
	var asJSON, loopback bool
	c := &cobra.Command{
		Use:   "conns",
		Short: "List connections grouped by app",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opt := engine.Options{Loopback: loopback}
			d.Collect(cmd.Context(), opt) // the first sample only sets the starting point for rates
			d.Sleep(time.Second)
			snap := d.Collect(cmd.Context(), opt)
			if snap.SocketErr != nil {
				return fmt.Errorf("couldn't read sockets: %w", snap.SocketErr)
			}
			if asJSON {
				return writeJSON(d.Stdout, nonNil(snap.Groups))
			}
			tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "APP\tCONNS\tDOWN\tUP\tREMOTE")
			for _, g := range snap.Groups {
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", g.App, len(g.Conns), model.HumanRate(g.Rate.Rx), model.HumanRate(g.Rate.Tx), remotes(g.Conns, 3))
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	c.Flags().BoolVar(&loopback, "loopback", false, "include connections to this Mac (127.0.0.1, ::1)")
	return c
}

// remotes lists up to n remote addresses, then how many more there are.
func remotes(conns []model.Conn, n int) string {
	var parts []string
	for i, c := range conns {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d more", len(conns)-n))
			break
		}
		parts = append(parts, c.Remote.String())
	}
	return strings.Join(parts, ", ")
}

func memCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "mem",
		Short: "Show memory use and the apps using the most",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snap := d.Collect(cmd.Context(), engine.Options{})
			if snap.ProcessErr != nil && snap.MemoryErr != nil {
				return fmt.Errorf("couldn't read memory: %w", snap.MemoryErr)
			}
			if asJSON {
				return writeJSON(d.Stdout, struct {
					Memory model.MemoryStats `json:"memory"`
					Apps   []model.AppUsage  `json:"apps"`
				}{snap.Memory, nonNil(snap.Apps)})
			}
			if snap.MemoryErr != nil {
				fmt.Fprintf(d.Stdout, "Couldn't read memory totals: %v\n\n", snap.MemoryErr)
			} else {
				st := snap.Memory
				fmt.Fprintf(d.Stdout, "Memory pressure: %s\nUsed %s of %s · cached %s · compressed %s · swap %s\n\n",
					st.Pressure, model.HumanBytes(st.Used), model.HumanBytes(st.Total), model.HumanBytes(st.Cached),
					model.HumanBytes(st.Compressed), model.HumanBytes(st.SwapUsed))
			}
			tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "APP\tPROCESSES\tMEMORY\tCPU")
			for i, a := range snap.Apps {
				if i == memTop {
					break
				}
				fmt.Fprintf(tw, "%s\t%d\t%s\t%.1f%%\n", a.Name, len(a.PIDs), model.HumanBytes(a.Memory), a.CPU)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

func updateDBCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "update-db",
		Short: "Download or refresh the IP owner database (about 11 MB)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(d.Stdout, "Downloading %s…\n", d.OwnerDBURL)
			n, err := sys.UpdateOwnerDB(cmd.Context(), d.HTTP, d.OwnerDBURL, d.OwnerDBPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(d.Stdout, "Saved %d IP ranges to %s.\n", n, d.OwnerDBPath)
			return nil
		},
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// nonNil makes empty lists print as [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
