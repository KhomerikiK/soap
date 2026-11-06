package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/model"
)

func junkCmd(d *Deps) *cobra.Command {
	return listCmd(d, model.ScreenJunk, "junk", "List junk soap can clean")
}

func devCmd(d *Deps) *cobra.Command {
	return listCmd(d, model.ScreenDev, "dev", "List developer junk soap can clean")
}

func listCmd(d *Deps, screen model.Screen, use, short string) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := gather(cmd.Context(), d, screen)
			if asJSON {
				return writeJSON(d.Stdout, f.json())
			}
			f.print(d.Stdout)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

// found is everything a scan turned up.
type found struct {
	items []model.CleanItem
	notes []model.Note
	errs  map[string]string
}

func gather(ctx context.Context, d *Deps, screens ...model.Screen) found {
	f := found{errs: map[string]string{}}
	for _, s := range screens {
		for u := range d.Scan(ctx, s) {
			f.items = append(f.items, u.Items...)
			f.notes = append(f.notes, u.Notes...)
			if u.Err != nil {
				f.errs[u.Finder] = u.Err.Error()
			}
		}
	}
	return f
}

// ordered is the items in the order the screens show them.
func (f found) ordered() []model.CleanItem {
	var out []model.CleanItem
	for _, g := range model.GroupItems(f.items, nil, nil, false) {
		out = append(out, g.Items...)
	}
	return out
}

func (f found) print(w io.Writer) {
	for _, n := range f.notes {
		if n.Group == "" {
			fmt.Fprintln(w, n.Text)
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TICK\tSIZE\tGROUP\tNAME\tDETAIL\tID")
	for _, it := range f.ordered() {
		tick, detail := "", it.Detail
		switch {
		case it.PreTicked():
			tick = "✓"
		case it.Busy:
			tick, detail = "busy", it.Reason()
		case it.Blocked != "":
			tick, detail = "blocked", it.Reason()
		}
		size := model.HumanBytes(it.Size)
		if it.SizeMin {
			size = "≥ " + size
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", tick, size, it.Group, it.Name, detail, it.ID)
	}
	tw.Flush()
	for _, n := range f.notes {
		if n.Group != "" {
			fmt.Fprintf(w, "%s: %s\n", n.Group, n.Text)
		}
	}
	groups := make([]string, 0, len(f.errs))
	for g := range f.errs {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		fmt.Fprintf(w, "Couldn't check %s: %s\n", g, f.errs[g])
	}
}

func (f found) json() any {
	type item struct {
		model.CleanItem
		Ticked bool `json:"ticked"`
	}
	type scanErr struct {
		Group string `json:"group"`
		Error string `json:"error"`
	}
	items := []item{}
	for _, it := range f.ordered() {
		items = append(items, item{CleanItem: it, Ticked: it.PreTicked()})
	}
	errs := []scanErr{}
	for g, e := range f.errs {
		errs = append(errs, scanErr{Group: g, Error: e})
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Group < errs[j].Group })
	return struct {
		Items  []item       `json:"items"`
		Notes  []model.Note `json:"notes"`
		Errors []scanErr    `json:"errors"`
	}{items, nonNil(f.notes), errs}
}

func cleanCmd(d *Deps) *cobra.Command {
	var safe, dryRun, yes bool
	c := &cobra.Command{
		Use:   "clean (--safe | <id>...)",
		Short: "Clean the safe items, or the items with these IDs (from soap junk, dev, disk, dupes or apps)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClean(cmd.Context(), d, args, safe, dryRun, yes)
		},
	}
	c.Flags().BoolVar(&safe, "safe", false, "clean what the TUI ticks by default")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be cleaned, and stop")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return c
}

func runClean(ctx context.Context, d *Deps, ids []string, safe, dryRun, yes bool) error {
	if safe == (len(ids) > 0) {
		return &ExitError{Code: 1, Msg: "Pass --safe, or the IDs of the items to clean (see soap junk, dev, disk, dupes and apps)."}
	}
	screens := []model.Screen{model.ScreenJunk, model.ScreenDev}
	if !safe {
		screens = screensFor(ids)
	}
	f := gather(ctx, d, screens...)
	for _, g := range sortedKeys(f.errs) {
		fmt.Fprintf(d.Stderr, "%s: %s\n", g, f.errs[g])
	}
	if msg, bad := f.errs["Config"]; bad {
		return &ExitError{Code: 1, Msg: "Couldn't read the config, so nothing was cleaned: " + sentence(msg)}
	}
	var picked []model.CleanItem
	if safe {
		for _, it := range f.ordered() {
			if it.PreTicked() {
				picked = append(picked, it)
			}
		}
		if len(picked) == 0 {
			return &ExitError{Code: 2, Msg: "Nothing safe to clean."}
		}
	} else {
		byID := map[string]model.CleanItem{}
		for _, it := range f.items {
			byID[it.ID] = it
		}
		seen := map[string]bool{}
		pickedID := map[string]bool{}
		var typed []string // the ID as typed, for each picked item
		refused := false
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			it, ok := byID[id]
			if !ok && strings.HasPrefix(id, "disk/") && d.DiskItem != nil {
				found, err := d.DiskItem(ctx, id)
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					fmt.Fprintf(d.Stderr, "%s can't be cleaned: %s.\n", id, strings.TrimSuffix(err.Error(), "."))
					refused = true
					continue
				default:
					it, ok = found, true
				}
			}
			if !ok {
				return &ExitError{Code: 2, Msg: fmt.Sprintf("No item with ID %s.", id)}
			}
			if !it.Tickable() {
				fmt.Fprintf(d.Stderr, "%s can't be cleaned: %s.\n", id, strings.TrimSuffix(it.Reason(), "."))
				refused = true
				continue
			}
			if pickedID[it.ID] {
				continue
			}
			pickedID[it.ID] = true
			picked = append(picked, it)
			typed = append(typed, id)
			if it.IsApp() { // an app comes with the leftovers matched by its bundle ID
				for _, l := range f.items {
					if l.Bundle == it.Bundle && !l.IsApp() && !l.ByName && l.Tickable() && !pickedID[l.ID] {
						pickedID[l.ID] = true
						picked = append(picked, l)
						typed = append(typed, id)
					}
				}
			}
		}
		for i, it := range picked {
			if it.Screen != model.ScreenDisk || len(it.Paths) == 0 {
				continue
			}
			for j, in := range picked {
				if i != j && len(in.Paths) > 0 && strings.HasPrefix(in.Paths[0], it.Paths[0]+"/") {
					fmt.Fprintf(d.Stderr, "%s is inside %s, which is already in this clean-up.\n", typed[j], typed[i])
					refused = true
				}
			}
		}
		for _, it := range wholeSets(picked) {
			fmt.Fprintln(d.Stderr, model.KeepMessage(it))
			refused = true
		}
		if refused {
			return &ExitError{Code: 2}
		}
		picked = model.AppsFirst(picked) // as the Cleaner orders them
	}
	tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
	for _, it := range picked {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", it.How(), model.HumanBytes(it.Size), it.Name)
	}
	tw.Flush()
	for _, it := range picked {
		if it.Root != "" && len(it.Paths) > 0 && model.InICloudDrive(it.Root, it.Paths[0]) {
			fmt.Fprintln(d.Stdout, model.ICloudWarning)
			break
		}
	}
	for _, it := range picked {
		if it.Root != "" && len(it.Paths) > 0 && model.InLaunchAgents(it.Root, it.Paths[0]) {
			fmt.Fprintln(d.Stdout, model.LaunchAgentWarning)
			break
		}
	}
	question := model.Summary(picked)
	if dryRun {
		fmt.Fprintln(d.Stdout, question)
		fmt.Fprintln(d.Stdout, "Dry run: nothing was cleaned.")
		return nil
	}
	if !yes && !confirm(bufio.NewReader(d.Stdin), d.Stdout, question) {
		return &ExitError{Code: 1, Msg: "Cancelled."}
	}
	var freed uint64
	failed := 0
	err := d.Clean(ctx, picked, func(r model.CleanResult) {
		freed += r.Freed
		switch r.Result {
		case "done":
			fmt.Fprintf(d.Stdout, "✓ %s\n", r.Item.Name)
		case "skipped":
			fmt.Fprintf(d.Stdout, "– %s: %s\n", r.Item.Name, r.Message)
		case "partial":
			failed++
			fmt.Fprintf(d.Stdout, "~ %s: %s\n", r.Item.Name, r.Message)
		default:
			failed++
			fmt.Fprintf(d.Stdout, "✗ %s: %s\n", r.Item.Name, r.Message)
		}
	})
	if err != nil {
		return &ExitError{Code: 1, Msg: sentence(err.Error())}
	}
	fmt.Fprintf(d.Stdout, "Freed %s.\n", model.HumanBytes(freed))
	if failed > 0 {
		return &ExitError{Code: 1, Msg: fmt.Sprintf("%d of %d items couldn't be fully cleaned.", failed, len(picked))}
	}
	return nil
}

// screensFor are the screens to scan for the items with these IDs, from the first part of each:
// junk/, dev/, dupes/ or apps/. Disk IDs need no scan.
func screensFor(ids []string) []model.Screen {
	var out []model.Screen
	for _, s := range []model.Screen{model.ScreenJunk, model.ScreenDev, model.ScreenDupes, model.ScreenApps} {
		for _, id := range ids {
			if strings.HasPrefix(id, string(s)+"/") {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// wholeSets returns the first item of each Dupes set that picked would remove every copy of.
// A copy counts as removed when a picked path is the copy or a folder above it. That's decided by
// file identity (os.SameFile), not path text, because the Mac's disk ignores case. Paths that
// can't be read fall back to a plain path comparison. The Cleaner checks again.
func wholeSets(picked []model.CleanItem) []model.CleanItem {
	out := model.WholeSets(picked)
	seen := map[string]bool{}
	for _, it := range out {
		seen[it.Set] = true
	}
	var targets []string
	for _, it := range picked {
		targets = append(targets, it.Paths...)
	}
	infos := make([]os.FileInfo, len(targets))
	for i, p := range targets {
		infos[i], _ = os.Stat(p)
	}
	covered := func(copyPath string) bool {
		for dir := copyPath; ; dir = filepath.Dir(dir) {
			fi, err := os.Stat(dir)
			for i, t := range targets {
				if dir == t || (err == nil && infos[i] != nil && os.SameFile(fi, infos[i])) {
					return true
				}
			}
			if filepath.Dir(dir) == dir {
				return false
			}
		}
	}
	for _, it := range picked {
		if it.Set == "" || seen[it.Set] {
			continue
		}
		seen[it.Set] = true
		all := len(it.Copies) > 0
		for _, c := range it.Copies {
			if !covered(c.Path) {
				all = false
				break
			}
		}
		if all {
			out = append(out, it)
		}
	}
	return out
}

func historyCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "history",
		Short: "List past clean-ups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries, err := d.History()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(d.Stdout, nonNil(entries))
			}
			tw := tabwriter.NewWriter(d.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "WHEN\tNAME\tSIZE\tWHAT HAPPENED\tID")
			for _, e := range entries {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format("2 Jan 2006 15:04"), e.Name, model.HumanBytes(e.Size), e.State(), e.ID)
			}
			return tw.Flush()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

func restoreCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <history-id>",
		Short: "Put an item that was moved to the Trash back where it was",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := d.Restore(args[0])
			switch {
			case errors.Is(err, actions.ErrNoEntry):
				return &ExitError{Code: 2, Msg: fmt.Sprintf("No history entry with ID %s.", args[0])}
			case err != nil:
				return &ExitError{Code: 1, Msg: sentence(err.Error())}
			}
			fmt.Fprintf(d.Stdout, "Put back %s.\n", e.Name)
			return nil
		},
	}
}

// sentence starts s with a capital letter and ends it with a full stop unless it has end punctuation.
func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
