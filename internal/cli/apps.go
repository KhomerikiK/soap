package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/model"
)

func appsCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "apps",
		Short: "List your apps, unused ones first, with their leftovers, and the leftovers of removed apps",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := gather(cmd.Context(), d, model.ScreenApps)
			if msg, bad := f.errs["Config"]; bad {
				return &ExitError{Code: 1, Msg: "Couldn't read the config: " + sentence(msg)}
			}
			groups := model.AppGroups(f.items, false)
			if asJSON {
				return writeJSON(d.Stdout, appsJSON(groups, f))
			}
			printApps(d.Stdout, d.Home, groups, f)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

func printApps(w io.Writer, home string, groups []model.AppGroup, f found) {
	apps := 0
	var total uint64
	for _, g := range groups {
		total += g.Size
		var info string
		switch {
		case g.App == nil && len(g.Leftovers) == 1:
			info = "1 item"
		case g.App == nil:
			info = fmt.Sprintf("%d items", len(g.Leftovers))
		default:
			apps++
			info = g.App.Detail
			if r := g.App.Reason(); r != "" {
				info += " · " + r
			}
		}
		fmt.Fprintf(w, "%s · %s · %s\n", g.Name, info, model.HumanBytes(g.Size))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		rows := g.Leftovers
		if g.App != nil {
			rows = append([]model.CleanItem{*g.App}, rows...)
		}
		for _, it := range rows {
			kind, where := "", it.Name
			switch {
			case it.IsApp():
				kind, where = "app", model.HomePath(home, it.Paths[0])
			case it.ByName && isSharedCopy(it.Detail):
				kind = "shared"
			case it.ByName:
				kind = "by name"
			case it.Bundle != "":
				kind = "with app"
			}
			if !it.IsApp() && it.Detail != "" {
				where += " · " + it.Detail
			}
			if !it.IsApp() && !it.Tickable() {
				kind = "blocked"
			}
			size := model.HumanBytes(it.Size)
			if it.SizeMin {
				size = "≥ " + size
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", kind, size, where, it.ID)
		}
		tw.Flush()
	}
	count := "1 app"
	if apps != 1 {
		count = fmt.Sprintf("%d apps", apps)
	}
	fmt.Fprintf(w, "%s · found %s.\n", count, model.HumanBytes(total))
	for _, n := range f.notes {
		fmt.Fprintln(w, n.Text)
	}
	for _, g := range sortedKeys(f.errs) {
		fmt.Fprintf(w, "Couldn't check %s: %s\n", g, f.errs[g])
	}
}

func appsJSON(groups []model.AppGroup, f found) any {
	type appJSON struct {
		Name      string            `json:"name"`
		Size      uint64            `json:"size"`
		App       model.CleanItem   `json:"app"`
		Leftovers []model.CleanItem `json:"leftovers"`
	}
	apps, removed := []appJSON{}, []model.CleanItem{}
	for _, g := range groups {
		if g.App == nil {
			removed = append(removed, g.Leftovers...)
			continue
		}
		apps = append(apps, appJSON{Name: g.Name, Size: g.Size, App: *g.App, Leftovers: nonNil(g.Leftovers)})
	}
	type scanErr struct {
		Group string `json:"group"`
		Error string `json:"error"`
	}
	errs := []scanErr{}
	for _, g := range sortedKeys(f.errs) {
		errs = append(errs, scanErr{Group: g, Error: f.errs[g]})
	}
	return struct {
		Apps    []appJSON         `json:"apps"`
		Removed []model.CleanItem `json:"removed"`
		Notes   []model.Note      `json:"notes"`
		Errors  []scanErr         `json:"errors"`
	}{apps, removed, nonNil(f.notes), errs}
}

// isSharedCopy tells a row shared by another copy of the app ("another copy of Notion uses this
// too") from a real name match ("matched by name"). The finder sets no separate marker, so the
// exact wording of its detail is the signal.
func isSharedCopy(detail string) bool {
	return strings.HasPrefix(detail, "another copy of ") && strings.HasSuffix(detail, " uses this too")
}
