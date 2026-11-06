package tui

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

type portsScreen struct{}

func (portsScreen) name() string { return "Ports" }

func (portsScreen) columns() []column {
	return []column{{"PORT", 6}, {"PROTO", 6}, {"PID", 7}, {"PROCESS", 20}, {"USER", 10}, {"BIND", 16}, {"NOTE", 0}}
}

func (portsScreen) sortModes() []string { return []string{"port", "process", "pid"} }

func (portsScreen) hints() []hint {
	return []hint{{"j/k", "move"}, {"x", "kill"}, {"X", "force kill"}, {"/", "filter"}, {"s", "sort"}, {"p", "pause"}, {"?", "help"}, {"q", "quit"}}
}

func (portsScreen) problem(m *Model) string {
	if err := m.snap.SocketErr; err != nil {
		return "Couldn't read sockets: " + err.Error()
	}
	return ""
}

func (portsScreen) empty(*Model) string { return "Nothing is listening." }

func (portsScreen) preamble(m *Model) []string {
	lines := []string{" " + accent.Render("Listening ports") + faint.Render(fmt.Sprintf("  %d", len(m.snap.Listeners)))}
	if err := m.snap.DockerErr; err != nil {
		lines = append(lines, " "+warn.Render(oneLine("Couldn't read Docker containers, so container names are missing: "+err.Error())))
	}
	return lines
}

func (portsScreen) rows(m *Model) []row {
	ls := slices.Clone(m.snap.Listeners)
	switch m.sortIdx[m.tab] {
	case 1:
		sort.SliceStable(ls, func(i, j int) bool { return strings.ToLower(ls[i].Process) < strings.ToLower(ls[j].Process) })
	case 2:
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].PID < ls[j].PID })
	}
	rows := make([]row, 0, len(ls))
	for _, l := range ls {
		note, noteStyle := l.Note, faint
		if l.Container != "" {
			note, noteStyle = "→ "+l.Container, accent
		}
		user, userStyle := l.User, plain
		if user == "" {
			user = "?"
		}
		if l.UID != m.deps.UID {
			userStyle = warn
		}
		rows = append(rows, row{
			cells:  []string{strconv.Itoa(int(l.Port)), l.Proto, strconv.Itoa(l.PID), l.Process, user, l.Bind, note},
			styles: []lipgloss.Style{plain, plain, plain, plain, userStyle, plain, noteStyle},
			item:   l,
		})
	}
	return rows
}

// focusPort puts the cursor on the first row of port, or at the top when nothing listens on it now.
func (m *Model) focusPort(port uint16) {
	m.focusRow(func(item any) bool { l, ok := item.(model.Listener); return ok && l.Port == port })
}

func (portsScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	if key != "x" && key != "X" {
		return nil, false
	}
	if r == nil {
		return nil, true
	}
	l := r.item.(model.Listener)
	force := key == "X"
	if l.Container != "" {
		m.askContainer(l.Container, l.Port, force)
		return nil, true
	}
	if l.Engine {
		m.setMsg(model.EngineRefusal(l.Port), warn)
		return nil, true
	}
	m.askKill(l.ID, l.UID, force, fmt.Sprintf("%s (pid %d) on :%d", l.Process, l.PID, l.Port), l.User)
	return nil, true
}
