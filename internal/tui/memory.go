package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KhomerikiK/soap/internal/model"
)

const pressureBarWidth = 24

type memoryScreen struct{}

func (memoryScreen) name() string { return "Memory" }

func (memoryScreen) columns() []column {
	return []column{{"APP", 34}, {"", 14}, {"MEMORY", 10}, {"CPU", 0}}
}

func (memoryScreen) sortModes() []string { return []string{"memory", "cpu", "name"} }

func (memoryScreen) hints() []hint {
	return []hint{{"j/k", "move"}, {"x", "quit app"}, {"X", "force quit"}, {"g", "group"}, {"/", "filter"}, {"s", "sort"}, {"q", "quit"}}
}

func (memoryScreen) problem(m *Model) string {
	if err := m.snap.ProcessErr; err != nil {
		return "Couldn't read processes: " + err.Error()
	}
	return ""
}

func (memoryScreen) empty(*Model) string { return "No processes." }

func (memoryScreen) preamble(m *Model) []string {
	if err := m.snap.MemoryErr; err != nil {
		return []string{" " + danger.Render(oneLine("Couldn't read memory totals: "+err.Error()))}
	}
	st := m.snap.Memory
	filled := 0
	if st.Total > 0 {
		filled = min(int(float64(st.Used)/float64(st.Total)*pressureBarWidth+0.5), pressureBarWidth)
	}
	ps := pressureStyle(st.Pressure)
	bar := ps.Render(strings.Repeat("█", filled)) + faint.Render(strings.Repeat("░", pressureBarWidth-filled))
	mode := "grouped by app"
	if m.opt.RawProcesses {
		mode = "one row per process"
	}
	return []string{
		" " + accent.Render("Memory pressure") + "  " + bar + "  " + ps.Render(st.Pressure.String()),
		faint.Render(fmt.Sprintf(" used %s of %s · cached %s · compressed %s · swap %s · %s",
			model.HumanBytes(st.Used), model.HumanBytes(st.Total), model.HumanBytes(st.Cached),
			model.HumanBytes(st.Compressed), model.HumanBytes(st.SwapUsed), mode)),
	}
}

func (memoryScreen) rows(m *Model) []row {
	apps := slices.Clone(m.snap.Apps)
	switch m.sortIdx[m.tab] {
	case 1:
		sort.SliceStable(apps, func(i, j int) bool { return apps[i].CPU > apps[j].CPU })
	case 2:
		sort.SliceStable(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	}
	rows := make([]row, 0, len(apps))
	for _, a := range apps {
		detail, detailStyle := "", faint
		switch {
		case a.System:
			detail, detailStyle = "system", warn
		case len(a.PIDs) > 1:
			detail = fmt.Sprintf("%d processes", len(a.PIDs))
		}
		rows = append(rows, row{
			cells:  []string{a.Name, detail, model.HumanBytes(a.Memory), fmt.Sprintf("%.1f%%", a.CPU)},
			styles: []lipgloss.Style{plain, detailStyle, plain, plain},
			item:   a,
		})
	}
	return rows
}

func (memoryScreen) key(m *Model, key string, r *row) (tea.Cmd, bool) {
	switch key {
	case "g":
		m.opt.RawProcesses = !m.opt.RawProcesses
		m.resetSelection()
		return m.refresh(), true
	case "x", "X":
		if r != nil {
			a := r.item.(model.AppUsage)
			m.askQuit(quitTarget{name: a.Name, appPath: a.AppPath, main: a.Main, uid: a.UID}, key == "X")
		}
		return nil, true
	}
	return nil, false
}
