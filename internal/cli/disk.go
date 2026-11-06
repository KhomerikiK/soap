package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/KhomerikiK/soap/internal/model"
)

// diskTop is how many items soap disk lists in its table. --json lists them all.
const diskTop = 25

func diskCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "disk [path]",
		Short: "Show the largest items in a folder, your home folder by default",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := diskDir(d.Home, args)
			if err != nil {
				return err
			}
			tree := model.NewDiskTree(dir)
			d.Walk(cmd.Context(), tree)
			entries, err := d.List(dir)
			if err != nil {
				return &ExitError{Code: 1, Msg: fmt.Sprintf("Couldn't read %s: %v.", model.HomePath(d.Home, dir), err)}
			}
			rows := model.DiskRows(entries, tree.Children(dir), true, false)
			top, _ := tree.Entry(dir)
			p := tree.Progress()
			if asJSON {
				type item struct {
					ID string `json:"id"`
					model.DiskEntry
				}
				items := []item{}
				for _, e := range rows {
					items = append(items, item{ID: model.DiskID(d.Home, e.Path), DiskEntry: e})
				}
				return writeJSON(d.Stdout, struct {
					Path       string `json:"path"`
					Size       uint64 `json:"size"`
					Min        bool   `json:"size_is_minimum,omitempty"`
					Unreadable int    `json:"unreadable_folders"`
					Items      []item `json:"items"`
				}{dir, top.Size, top.Min, p.Unreadable, items})
			}
			printDisk(d.Stdout, d.Home, dir, top, rows, p)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

// diskDir is the folder soap disk walks: the home folder, or a path inside it.
func diskDir(home string, args []string) (string, error) {
	if len(args) == 0 {
		return home, nil
	}
	dir := args[0]
	switch {
	case dir == "~":
		dir = home
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[2:])
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if dir != home && !strings.HasPrefix(dir, home+"/") {
		return "", &ExitError{Code: 1, Msg: "soap disk looks only inside your home folder."}
	}
	return dir, nil
}

func printDisk(w io.Writer, home, dir string, top model.DiskEntry, rows []model.DiskEntry, p model.DiskProgress) {
	fmt.Fprintf(w, "%s  %s\n", model.HomePath(home, dir), sizeText(top))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SIZE\tSHARE\tNAME\tID")
	for i, e := range rows {
		if i == diskTop {
			break
		}
		name := e.Name
		switch {
		case e.Package != "":
			name += " · " + e.Package
		case e.Dir:
			name += "/"
		}
		if model.InICloudDrive(home, e.Path) {
			name += " · in iCloud Drive"
		}
		share := ""
		if top.Size > 0 && !e.Unmeasured {
			share = fmt.Sprintf("%.0f%%", float64(e.Size)/float64(top.Size)*100)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sizeText(e), share, name, model.DiskID(home, e.Path))
	}
	tw.Flush()
	if n := len(rows) - diskTop; n > 0 {
		var rest uint64
		for _, e := range rows[diskTop:] {
			rest += e.Size
		}
		fmt.Fprintf(w, "… and %d smaller items (%s).\n", n, model.HumanBytes(rest))
	}
	switch {
	case p.Unreadable == 1:
		fmt.Fprintln(w, "1 folder couldn't be read, so sizes with ≥ are at least what's shown. To see it, give your terminal Full Disk Access (System Settings › Privacy & Security › Full Disk Access).")
	case p.Unreadable > 1:
		fmt.Fprintf(w, "%d folders couldn't be read, so sizes with ≥ are at least what's shown. To see them, give your terminal Full Disk Access (System Settings › Privacy & Security › Full Disk Access).\n", p.Unreadable)
	}
}

// sizeText is a size as soap disk prints it: "–" when unmeasured, "≥ 1.2 GB" for a minimum.
func sizeText(e model.DiskEntry) string {
	switch {
	case e.Unmeasured:
		return "–"
	case e.Min:
		return "≥ " + model.HumanBytes(e.Size)
	}
	return model.HumanBytes(e.Size)
}

func dupesCmd(d *Deps) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "dupes",
		Short: "List sets of identical files in your own folders, with the copy to keep",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := gather(cmd.Context(), d, model.ScreenDupes)
			if msg, bad := f.errs["Config"]; bad {
				return &ExitError{Code: 1, Msg: "Couldn't read the config: " + sentence(msg)}
			}
			sets := model.DupeSets(f.items, false)
			if asJSON {
				return writeJSON(d.Stdout, dupesJSON(sets, f))
			}
			printDupes(d.Stdout, d.Home, sets, f)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of a table")
	return c
}

func printDupes(w io.Writer, home string, sets []model.DupeSet, f found) {
	var frees uint64
	for _, s := range sets {
		frees += s.Frees
		fmt.Fprintf(w, "%s · %d copies · %s each · frees up to %s\n", s.Name, len(s.Items), model.HumanBytes(s.Size), model.HumanBytes(s.Frees))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, it := range s.Items {
			keep := ""
			if it.Keep {
				keep = "keep"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", keep, model.HomePath(home, it.Paths[0]), it.Detail, it.ID)
		}
		tw.Flush()
	}
	switch len(sets) {
	case 0:
		fmt.Fprintln(w, "No copies found.")
	case 1:
		fmt.Fprintf(w, "1 set · frees up to %s.\n", model.HumanBytes(frees))
	default:
		fmt.Fprintf(w, "%d sets · frees up to %s.\n", len(sets), model.HumanBytes(frees))
	}
	for _, n := range f.notes {
		fmt.Fprintln(w, n.Text)
	}
	for _, g := range sortedKeys(f.errs) {
		fmt.Fprintf(w, "Couldn't check %s: %s\n", g, f.errs[g])
	}
}

func dupesJSON(sets []model.DupeSet, f found) any {
	type copyJSON struct {
		ID     string `json:"id"`
		Path   string `json:"path"`
		Size   uint64 `json:"size"`
		Frees  uint64 `json:"frees"`
		Keep   bool   `json:"keep"`
		Detail string `json:"detail"`
	}
	type setJSON struct {
		Set    string     `json:"set"`
		Name   string     `json:"name"`
		Size   uint64     `json:"size"`
		Frees  uint64     `json:"frees"`
		Copies []copyJSON `json:"copies"`
	}
	out := []setJSON{}
	for _, s := range sets {
		sj := setJSON{Set: s.ID, Name: s.Name, Size: s.Size, Frees: s.Frees}
		for _, it := range s.Items {
			sj.Copies = append(sj.Copies, copyJSON{ID: it.ID, Path: it.Paths[0], Size: it.Size, Frees: it.Unshared, Keep: it.Keep, Detail: it.Detail})
		}
		out = append(out, sj)
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
		Sets   []setJSON    `json:"sets"`
		Notes  []model.Note `json:"notes"`
		Errors []scanErr    `json:"errors"`
	}{out, nonNil(f.notes), errs}
}
