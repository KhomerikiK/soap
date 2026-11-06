package tui

import "charm.land/lipgloss/v2"

// Colors come from the terminal's own 16-color palette, so they follow the user's theme.
var (
	plain     = lipgloss.NewStyle()
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	good      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warn      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	danger    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	faint     = lipgloss.NewStyle().Faint(true)
	selected  = lipgloss.NewStyle().Reverse(true)
	activeTab = lipgloss.NewStyle().Reverse(true).Bold(true)
	bold      = lipgloss.NewStyle().Bold(true)
	chip      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Background(lipgloss.Color("8")) // a tab named on Home
)
