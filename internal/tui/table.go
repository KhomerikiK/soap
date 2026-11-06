package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// column is one table column. A width of 0 takes whatever is left of the line.
type column struct {
	title string
	width int
}

// row is one line of a screen's table: cells, or free-form text for detail lines.
type row struct {
	cells     []string
	styles    []lipgloss.Style // per cell; missing entries render plain
	text      string           // when set, the row is this one line instead of cells
	textStyle lipgloss.Style
	match     string // what the filter searches; the row's own text when empty
	item      any    // what x, X and enter act on
}

func (r row) search() string {
	switch {
	case r.match != "":
		return r.match
	case r.text != "":
		return r.text
	}
	return strings.Join(r.cells, " ")
}

func (r row) render(ws []int, isSelected bool, width int) string {
	if r.text != "" {
		line := fit(" "+r.text, width)
		if isSelected {
			return selected.Render(line)
		}
		return r.textStyle.Render(line)
	}
	return renderCells(r.cells, r.styles, ws, isSelected)
}

// widths gives every column its width; flexible columns share what's left of total.
func widths(cols []column, total int) []int {
	out := make([]int, len(cols))
	used, flex := 1, 0 // the leading space
	for i, c := range cols {
		out[i] = c.width
		used += c.width
		if i > 0 {
			used++ // the gap before this column
		}
		if c.width == 0 {
			flex++
		}
	}
	if flex > 0 {
		share := max((total-used)/flex, 4)
		for i, c := range cols {
			if c.width == 0 {
				out[i] = share
			}
		}
	}
	return out
}

// fit cuts s to w cells, ending with "…" when it's too long, and pads it to exactly w cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func renderCells(cells []string, styles []lipgloss.Style, ws []int, isSelected bool) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		w := 0
		if i < len(ws) {
			w = ws[i]
		}
		parts[i] = fit(c, w)
		if !isSelected && i < len(styles) {
			parts[i] = styles[i].Render(parts[i])
		}
	}
	line := " " + strings.Join(parts, " ")
	if isSelected {
		return selected.Render(line)
	}
	return line
}
