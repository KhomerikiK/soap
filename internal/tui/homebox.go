package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/model"
)

// homeView is what Home draws. It's built when data arrives, not in every draw.
type homeView struct {
	in        model.HomeInput
	findings  []model.Finding
	free      []model.FreeLine
	history   []model.Forms
	portsHead model.Line
	ports     []model.Listener // the ports of your apps, lowest first
}

func buildHomeView(in model.HomeInput) homeView {
	head, ports := model.PortsHead(in)
	return homeView{in: in, findings: model.Attention(in), free: model.FreeLines(in), history: model.HistoryLines(in), portsHead: head, ports: ports}
}

// homeSel is the selection as the box draws it.
type homeSel struct {
	cursor string // the selected line's key
	start  int    // the first finding shown
	frame  int    // the spinner's frame
}

var spinFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Keys of the lines j and k select besides the findings and the "What you can free" lines.
const (
	keyTop     = "panel/top"
	keyNetwork = "panel/network"
	keyPorts   = "panel/ports"
	keyHistory = "panel/history"
)

// diskGaugeMin is the narrowest gauge the System panel accepts for the long texts after it.
const diskGaugeMin = 18

// drawHome draws Home's box in width columns and height lines: one rounded box with shared borders
// and two columns, the middle border at column width/2. It needs at least 80×20 (the frame refuses
// smaller windows).
func drawHome(v homeView, width, height int, sel homeSel) []string {
	lay := model.HomeRows(height, len(v.findings))
	mid := width / 2
	left, right := mid-1, width-mid-2
	out := []string{topBorder(width, "Needs attention", model.AttentionCount(sel.start, lay.Attention, len(v.findings)))}
	for _, l := range attentionLines(v, width-2, lay.Attention, sel) {
		out = append(out, faint.Render("│")+l+faint.Render("│"))
	}
	row := func(l, r []string) {
		for i := range l {
			out = append(out, faint.Render("│")+fit(l[i], left)+faint.Render("│")+fit(r[i], right)+faint.Render("│"))
		}
	}
	out = append(out, splitBorder(width, mid, "System", "What you can free", "┬"))
	row(systemLines(v.in, left), freeLines(v.free, right, sel))
	if lay.Extra > 0 {
		out = append(out, splitBorder(width, mid, "Top memory", "Network", "┼"))
		row(selectFirst(topLines(v.in, left, lay.Extra), sel.cursor == keyTop, left), selectFirst(networkLines(v.in, right, lay.Extra), sel.cursor == keyNetwork, right))
	}
	out = append(out, splitBorder(width, mid, "Ports", "History", "┼"))
	row(selectFirst(portLines(v, left, lay.Bottom), sel.cursor == keyPorts, left), selectFirst(historyLines(v.history, right, lay.Bottom), sel.cursor == keyHistory, right))
	out = append(out, faint.Render("╰"+strings.Repeat("─", mid-1)+"┴"+strings.Repeat("─", width-mid-2)+"╯"))
	return out
}

// topBorder is the box's first line, with the attention title and, at the right, its count.
func topBorder(width int, title, count string) string {
	end := faint.Render("╮")
	used := 4 + ansi.StringWidth(title) // "╭─ " + title + " "
	if count != "" {
		end = faint.Render(" ") + accent.Render(count) + faint.Render(" ─╮")
		used += ansi.StringWidth(count) + 4
	} else {
		used++
	}
	return faint.Render("╭─ ") + accent.Render(title) + faint.Render(" "+strings.Repeat("─", max(0, width-used))) + end
}

// splitBorder is a border between two rows, with a title over each column and junction at column mid.
func splitBorder(width, mid int, left, right, junction string) string {
	l := "├─ " + left + " "
	r := "─ " + right + " "
	return faint.Render("├─ ") + accent.Render(left) + faint.Render(" "+strings.Repeat("─", max(0, mid-ansi.StringWidth(l)))+junction+"─ ") +
		accent.Render(right) + faint.Render(" "+strings.Repeat("─", max(0, width-mid-2-ansi.StringWidth(r)))+"┤")
}

// renderLine draws a line with its tones.
func renderLine(l model.Line) string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(toneStyle(s.Tone).Render(s.Text))
	}
	return b.String()
}

func toneStyle(t model.Tone) lipgloss.Style {
	switch t {
	case model.ToneAccent:
		return accent
	case model.ToneGood:
		return good
	case model.ToneWarn:
		return warn
	case model.ToneDanger:
		return danger
	case model.ToneFaint:
		return faint
	case model.ToneBold:
		return bold
	}
	return plain
}

// pick is the long form when it fits in width, otherwise the short one.
func pick(f model.Forms, width int) model.Line {
	if ansi.StringWidth(f.Long.String()) <= width {
		return f.Long
	}
	return f.Short
}

// selectFirst draws the first of a panel's lines selected, when its panel is.
func selectFirst(lines []string, on bool, width int) []string {
	if on && len(lines) > 0 {
		lines[0] = selected.Render(fit(ansi.Strip(lines[0]), width))
	}
	return lines
}

// attentionLines are the findings shown, rows of them, each width wide.
func attentionLines(v homeView, width, rows int, sel homeSel) []string {
	if len(v.findings) == 0 {
		return []string{fit(" "+good.Render("✓ Nothing needs you now."), width)}
	}
	var out []string
	for i := sel.start; i < len(v.findings) && i < sel.start+rows; i++ {
		f := v.findings[i]
		tag, tagStyle := f.Hint, faint
		if f.Hint == "" && f.Tab >= 0 {
			tag, tagStyle = tabLabel(f.Tab), chip
		}
		room := width - 5 - ansi.StringWidth(tag) - 2
		text := f.Text
		if ansi.StringWidth(text) > room && f.Short != "" {
			text = f.Short
		}
		text = fit(text, room)
		on := f.Key == sel.cursor
		arrow := " "
		if on {
			arrow = "▸"
		}
		if on {
			out = append(out, selected.Render(" "+arrow+" "+f.Level.Mark()+" "+text+" "+tag+" "))
			continue
		}
		out = append(out, "   "+markStyle(f.Level).Render(f.Level.Mark())+" "+text+" "+tagStyle.Render(tag)+" ")
	}
	return out
}

func markStyle(l model.Level) lipgloss.Style {
	switch l {
	case model.Urgent:
		return danger
	case model.Warning:
		return warn
	case model.Space:
		return accent
	}
	return faint
}

// tabLabel is a tab's number and name, as the tab line shows them: "5 Dev".
func tabLabel(tab int) string {
	return fmt.Sprintf("%d %s", tab, []string{"Home", "Ports", "Connections", "Memory", "Junk", "Dev", "Disk", "Dupes", "Apps"}[tab])
}

// systemLines are the System panel's five lines: CPU, memory (two lines), network and disk.
func systemLines(in model.HomeInput, width int) []string {
	label := func(s string) string { return " " + faint.Render(fit(s, 5)) }
	pct := bold.Render(fmt.Sprintf("%3.0f%%", in.CPU))
	graphWidth := max(1, width-12)
	cpu := label("CPU") + cpuGraph(in.CPUHistory, graphWidth) + "  " + pct

	disk := model.DiskText(in.DiskFree, in.DiskTotal)
	diskText := disk.Short
	if width-8-ansi.StringWidth(disk.Long.String()) >= diskGaugeMin {
		diskText = disk.Long
	}
	memText := model.MemoryText(in.Memory)
	textWidth := max(ansi.StringWidth(memText), ansi.StringWidth(diskText.String()))
	gaugeWidth := max(1, width-8-textWidth)

	mem, memNote := label("Mem")+danger.Render("Couldn't read memory"), ""
	if in.MemoryErr == nil {
		used := 0.0
		if in.Memory.Total > 0 {
			used = float64(in.Memory.Used) / float64(in.Memory.Total)
		}
		mem = label("Mem") + gauge(gaugeWidth, used, pressureStyle(in.Memory.Pressure)) + " " + memText
		memNote = label("") + renderLine(pick(model.MemoryNote(in.Memory), width-6))
	}
	net := label("Net") + good.Render("↓ "+model.HumanRate(in.Net.Rx)) + "   " + accent.Render("↑ "+model.HumanRate(in.Net.Tx))
	diskLine := label("Disk") + faint.Render("free space unknown")
	if in.DiskTotal > 0 {
		style := good
		switch {
		case in.DiskFree < 20<<30:
			style = danger
		case in.DiskFree < 60<<30:
			style = warn
		}
		used := float64(in.DiskTotal-min(in.DiskFree, in.DiskTotal)) / float64(in.DiskTotal)
		diskLine = label("Disk") + gauge(gaugeWidth, used, style) + " " + renderLine(diskText)
	}
	return []string{cpu, mem, memNote, net, diskLine}
}

// cpuGraph is a sparkline of the last width samples: green under 50%, amber under 80%, red above.
func cpuGraph(hist []float64, width int) string {
	hist = hist[max(0, len(hist)-width):]
	var b strings.Builder
	for _, v := range hist {
		style := good
		switch {
		case v >= 80:
			style = danger
		case v >= 50:
			style = warn
		}
		b.WriteString(style.Render(sparkline([]float64{v})))
	}
	return b.String()
}

// gauge is a colored ━ part on a faint ─ track, width wide, filled to share (0 to 1).
func gauge(width int, share float64, style lipgloss.Style) string {
	n := max(0, min(width, int(share*float64(width)+0.5)))
	return style.Render(strings.Repeat("━", n)) + faint.Render(strings.Repeat("─", width-n))
}

// freeLines are the "What you can free" lines. Their bars share one scale, the largest size.
func freeLines(lines []model.FreeLine, width int, sel homeSel) []string {
	var scale uint64
	longest, shortest := 0, 0
	for _, l := range lines {
		if l.Bar {
			scale = max(scale, l.Size)
			longest = max(longest, ansi.StringWidth(l.Note.Long.String()))
			shortest = max(shortest, ansi.StringWidth(l.Note.Short.String()))
		}
	}
	// " 4 Junk  " is 9 wide, then the bar, " ", the size in 7, "  ", the note and a last space.
	long := width-20-longest >= 9
	noteWidth := shortest
	if long {
		noteWidth = longest
	}
	barWidth := max(1, width-20-noteWidth)
	var out []string
	for _, l := range lines {
		num, name, _ := strings.Cut(l.Label, " ")
		line := " " + accent.Render(num) + " " + fit(name, 5) + " "
		switch {
		case l.Bar:
			note := l.Note.Short
			if long {
				note = l.Note.Long
			}
			line += freeBar(l, scale, barWidth) + " " + bold.Render(fmt.Sprintf("%7s", model.HumanBytes(l.Size))) + "  " + renderLine(note)
		case l.Spin:
			line += accent.Render(string(spinFrames[(sel.frame%len(spinFrames)+len(spinFrames))%len(spinFrames)])) + " " + renderLine(pick(l.Note, width-11))
		default:
			line += renderLine(pick(l.Note, width-9))
		}
		if l.Key == sel.cursor {
			line = selected.Render(fit(ansi.Strip(line), width))
		}
		out = append(out, line)
	}
	return out
}

// freeBar is a line's bar: the safe part green, the rest in the accent color, on a faint track.
func freeBar(l model.FreeLine, scale uint64, width int) string {
	if scale == 0 {
		return faint.Render(strings.Repeat("─", width))
	}
	n := max(0, min(width, int(float64(l.Size)/float64(scale)*float64(width)+0.5)))
	safe := min(n, int(float64(l.Safe)/float64(scale)*float64(width)+0.5))
	return good.Render(strings.Repeat("━", safe)) + accent.Render(strings.Repeat("━", n-safe)) + faint.Render(strings.Repeat("─", width-n))
}

// portLines are the Ports panel's lines: the counts, then the ports of your apps. With two lines
// they share the second; with more each gets its own, with its process and bind address.
func portLines(v homeView, width, rows int) []string {
	out := []string{" " + renderLine(v.portsHead)}
	if v.in.SocketErr != nil {
		return padLines(out, rows)
	}
	if rows == 2 {
		line, used := "", 1
		for i, l := range v.ports {
			part := fmt.Sprintf(":%d %s", l.Port, l.Process)
			more := ""
			if rest := len(v.ports) - i - 1; rest > 0 {
				more = fmt.Sprintf("  +%d more", rest)
			}
			if used+ansi.StringWidth(part)+2+ansi.StringWidth(more) > width && i > 0 {
				line += faint.Render(fmt.Sprintf("  +%d more", len(v.ports)-i))
				break
			}
			if i > 0 {
				line += "  "
				used += 2
			}
			line += accent.Render(fmt.Sprintf(":%d", l.Port)) + " " + l.Process
			used += ansi.StringWidth(part)
		}
		return append(out, " "+line)
	}
	for _, l := range v.ports {
		if len(out) == rows {
			break
		}
		out = append(out, " "+accent.Render(fit(fmt.Sprintf(":%d", l.Port), 6))+" "+fit(l.Process, 14)+" "+faint.Render(l.Bind))
	}
	return padLines(out, rows)
}

// historyLines are the History panel's lines.
func historyLines(lines []model.Forms, width, rows int) []string {
	var out []string
	for _, f := range lines {
		if len(out) == rows {
			break
		}
		out = append(out, " "+renderLine(pick(f, width-1)))
	}
	return padLines(out, rows)
}

// topLines are the apps with the most memory, with their memory and CPU.
func topLines(in model.HomeInput, width, rows int) []string {
	if in.ProcessErr != nil {
		return padLines([]string{" " + danger.Render("Couldn't read processes")}, rows)
	}
	var out []string
	nameWidth := min(20, width-14)
	for _, a := range in.Top {
		if len(out) == rows {
			break
		}
		out = append(out, " "+fit(a.Name, nameWidth)+fmt.Sprintf("%7s %5s", model.HumanBytes(a.Memory), fmt.Sprintf("%.0f%%", a.CPU)))
	}
	return padLines(out, rows)
}

// networkLines are the apps with connections and the ones with the highest download rate.
func networkLines(in model.HomeInput, width, rows int) []string {
	if in.SocketErr != nil {
		return padLines([]string{" " + danger.Render("Couldn't read sockets")}, rows)
	}
	conns := 0
	for _, g := range in.Groups {
		conns += len(g.Conns)
	}
	out := []string{" " + fmt.Sprintf("%s · %s", counted(len(in.Groups), "app", "apps"), counted(conns, "connection", "connections"))}
	nameWidth := min(20, width-28)
	for _, g := range in.Groups {
		if len(out) == rows {
			break
		}
		out = append(out, " "+fit(g.App, nameWidth)+" "+good.Render(fit("↓ "+model.HumanRate(g.Rate.Rx), 12))+" "+accent.Render("↑ "+model.HumanRate(g.Rate.Tx)))
	}
	return padLines(out, rows)
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// padLines makes lines exactly rows long.
func padLines(lines []string, rows int) []string {
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines[:rows]
}
