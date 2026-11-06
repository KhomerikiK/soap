package tui

import (
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

type connsScreen struct{}

func (connsScreen) name() string { return "Connections" }

func (connsScreen) shortName() string { return "Conns" }

func (connsScreen) columns() []column {
	return []column{{"APP", 28}, {"CONNS", 6}, {"↓", 11}, {"↑", 11}, {"", 0}}
}

func (connsScreen) sortModes() []string { return []string{"download", "upload", "connections", "name"} }

func (connsScreen) hints() []hint {
	return []hint{{"j/k", "move"}, {"enter", "open"}, {"x", "quit app"}, {"X", "force quit"}, {"l", "loopback"}, {"/", "filter"}, {"s", "sort"}, {"q", "quit"}}
}

func (connsScreen) problem(m *Model) string {
	if err := m.snap.SocketErr; err != nil {
		return "Couldn't read sockets: " + err.Error()
	}
	return ""
}

func (connsScreen) empty(*Model) string { return "No connections right now." }

func (connsScreen) preamble(m *Model) []string {
	total := 0
	for _, g := range m.snap.Groups {
		total += len(g.Conns)
	}
	line := " " + accent.Render("Connections") + faint.Render(fmt.Sprintf("  %d across %d apps", total, len(m.snap.Groups)))
	if m.opt.Loopback {
		line += faint.Render("  including loopback")
	}
	return []string{line}
}

func (connsScreen) onShow(m *Model) tea.Cmd {
	if m.deps.Owner == nil && !m.dbHintShown {
		m.dbHintShown = true
		m.setMsg("Run `soap update-db` to show who owns each IP.", faint)
	}
	return nil
}

func (connsScreen) rows(m *Model) []row {
	groups := slices.Clone(m.snap.Groups)
	switch m.sortIdx[m.tab] {
	case 1:
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].Rate.Tx > groups[j].Rate.Tx })
	case 2:
		sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].Conns) > len(groups[j].Conns) })
	case 3:
		sort.SliceStable(groups, func(i, j int) bool { return strings.ToLower(groups[i].App) < strings.ToLower(groups[j].App) })
	}
	var rows []row
	for _, g := range groups {
		open := m.expanded[groupID(g)]
		match := m.groupText(g)
		arrow := "▸ "
		if open {
			arrow = "▾ "
		}
		mark := ""
		if g.New {
			mark = "new"
		}
		rows = append(rows, row{
			cells:  []string{arrow + g.App, strconv.Itoa(len(g.Conns)), model.HumanRate(g.Rate.Rx), model.HumanRate(g.Rate.Tx), mark},
			styles: []lipgloss.Style{plain, plain, accent, plain, warn},
			match:  match,
			item:   g,
		})
		if !open {
			continue
		}
		for _, c := range g.Conns {
			rows = append(rows, row{text: m.connText(c), textStyle: faint, match: match, item: g})
		}
	}
	return rows
}

func (connsScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	switch key {
	case "l":
		m.opt.Loopback = !m.opt.Loopback
		return m.refresh(), true
	case "enter", "space":
		if r != nil {
			id := groupID(r.item.(model.ConnGroup))
			m.expanded[id] = !m.expanded[id]
		}
		return nil, true
	case "x", "X":
		if r != nil {
			g := r.item.(model.ConnGroup)
			m.askQuit(quitTarget{name: g.App, appPath: g.AppPath, main: g.Main, uid: g.UID}, key == "X")
		}
		return nil, true
	}
	return nil, false
}

func groupID(g model.ConnGroup) string { return g.App + "|" + g.AppPath }

// connText is one connection's detail line: remote address, hostname and owner.
func (m *Model) connText(c model.Conn) string {
	host := m.hostname(c.Remote.Addr())
	if host == "" {
		host = "—"
	}
	return fmt.Sprintf("   %s %s %s", fit(c.Remote.String(), 28), fit(host, 40), m.owner(c.Remote.Addr()))
}

// groupText is what the filter searches for an app: its name and every remote address and
// hostname.
func (m *Model) groupText(g model.ConnGroup) string {
	parts := []string{g.App}
	for _, c := range g.Conns {
		parts = append(parts, c.Remote.String(), m.hostname(c.Remote.Addr()))
	}
	return strings.Join(parts, " ")
}

func (m *Model) hostname(ip netip.Addr) string {
	if m.deps.Hostname == nil {
		return ""
	}
	return m.deps.Hostname(ip)
}

func (m *Model) owner(ip netip.Addr) string {
	if m.deps.Owner == nil {
		return ""
	}
	return m.deps.Owner(ip)
}
