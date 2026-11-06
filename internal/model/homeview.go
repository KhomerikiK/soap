package model

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Tone is how a piece of a Home line looks. The TUI gives each tone one of its styles.
type Tone int

const (
	ToneText Tone = iota
	ToneAccent
	ToneGood
	ToneWarn
	ToneDanger
	ToneFaint
	ToneBold
)

// Seg is one piece of a Home line.
type Seg struct {
	Text string
	Tone Tone
}

// Line is one line of a Home panel, in pieces.
type Line []Seg

// String is the line's text without its tones.
func (l Line) String() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Forms are the long and the short form of a line. Home shows the long form when it fits.
type Forms struct{ Long, Short Line }

func one(l Line) Forms { return Forms{Long: l, Short: l} }

func faintLine(text string) Forms { return one(Line{{text, ToneFaint}}) }

// HomeLayout is how many lines each row of Home's box gets, borders left out.
type HomeLayout struct {
	Attention int // findings shown: 1 to 5 (with none, one line says nothing needs you)
	System    int // System | What you can free: always 5
	Extra     int // Top memory | Network: 0 when the row isn't shown
	Bottom    int // Ports | History: 2 to 5
}

// HomeRows lays out Home's box in a body of height lines (at least 16) with findings findings.
// The fixed rows take 11 lines plus one per finding shown; three spare lines or more add the Top
// memory | Network row, and what's left grows the Ports | History row by up to 3 lines.
func HomeRows(height, findings int) HomeLayout {
	l := HomeLayout{Attention: max(1, min(findings, 5)), System: 5, Bottom: 2}
	spare := height - (11 + l.Attention)
	if spare >= 3 {
		l.Extra = min(5, spare-1)
		spare -= l.Extra + 1
	}
	l.Bottom += max(0, min(3, spare))
	return l
}

// Lines is the box's height, borders included.
func (l HomeLayout) Lines() int {
	n := l.Attention + l.System + l.Bottom + 4
	if l.Extra > 0 {
		n += l.Extra + 1
	}
	return n
}

// CleanUp is one clean-up in the history.
type CleanUp struct {
	Time     time.Time // when it started
	Size     uint64    // what it freed or moved to the Trash
	Deleted  int       // items deleted for good
	Trashed  int       // items moved to the Trash
	Commands int       // tools' clean-up commands run
}

// What says what a clean-up did: "6 items deleted", "2 items to the Trash" or
// "3 deleted, 1 to the Trash".
func (c CleanUp) What() string {
	items := func(n int) string {
		if n == 1 {
			return "1 item"
		}
		return fmt.Sprintf("%d items", n)
	}
	commands := "1 command run"
	if c.Commands != 1 {
		commands = fmt.Sprintf("%d commands run", c.Commands)
	}
	switch {
	case c.Deleted > 0 && c.Trashed+c.Commands == 0:
		return items(c.Deleted) + " deleted"
	case c.Trashed > 0 && c.Deleted+c.Commands == 0:
		return items(c.Trashed) + " to the Trash"
	case c.Commands > 0 && c.Deleted+c.Trashed == 0:
		return commands
	}
	var parts []string
	if c.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", c.Deleted))
	}
	if c.Trashed > 0 {
		parts = append(parts, fmt.Sprintf("%d to the Trash", c.Trashed))
	}
	if c.Commands > 0 {
		parts = append(parts, commands)
	}
	return strings.Join(parts, ", ")
}

// shortWidth is the room the narrowest History panel (80 columns) leaves for a line.
const shortWidth = 37

// shortWhat is What in fewer words: "2 trashed", "3 deleted, 1 trashed, 2 cmd".
func (c CleanUp) shortWhat() string {
	var parts []string
	if c.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", c.Deleted))
	}
	if c.Trashed > 0 {
		parts = append(parts, fmt.Sprintf("%d trashed", c.Trashed))
	}
	if c.Commands > 0 {
		parts = append(parts, fmt.Sprintf("%d cmd", c.Commands))
	}
	return strings.Join(parts, ", ")
}

// fit cuts s to n columns, with "…" when it cuts.
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n-1)]) + "…"
}

// HistorySummary is what the clean-ups in the history did, for Home's History panel.
type HistorySummary struct {
	Freed    uint64    // deleted or cleaned by a command, with a result of done or partial
	CleanUps int       // clean-ups that cleaned something
	Since    time.Time // the oldest entry's time; zero without history
	Newest   time.Time // the newest entry's time
	InTrash  uint64    // moved to the Trash and can still be put back
	Recent   []CleanUp // the clean-ups that cleaned something, newest first
}

// SummarizeHistory sums up entries, given newest first as History.List gives them. The Cleaner
// numbers a clean-up's entries from 0 (IDs end in "-0", "-1", …), so a clean-up is an entry whose
// ID ends in "-0" and the entries after it, up to the next one. Since and Newest come from the
// clean-ups that cleaned something. Moves to the Trash don't count as
// freed, because they free nothing until the Trash is emptied.
func SummarizeHistory(entries []HistoryEntry) HistorySummary {
	s := HistorySummary{InTrash: InTrash(entries)}
	if len(entries) == 0 {
		return s
	}
	var all []CleanUp
	for i := len(entries) - 1; i >= 0; i-- { // oldest first
		e := entries[i]
		if _, n, ok := strings.Cut(e.ID, "-"); len(all) == 0 || !ok || n == "0" {
			all = append(all, CleanUp{Time: e.Time})
		}
		if e.Result != ResultDone && e.Result != ResultPartial {
			continue
		}
		c := &all[len(all)-1]
		c.Size += e.Size
		switch e.Method {
		case MethodDelete:
			c.Deleted++
			s.Freed += e.Size
		case MethodCommand:
			c.Commands++
			s.Freed += e.Size
		case MethodTrash:
			c.Trashed++
		}
	}
	for i := len(all) - 1; i >= 0; i-- { // newest first
		if c := all[i]; c.Deleted+c.Trashed+c.Commands > 0 {
			s.Recent = append(s.Recent, c)
		}
	}
	s.CleanUps = len(s.Recent)
	if s.CleanUps > 0 {
		s.Newest, s.Since = s.Recent[0].Time, s.Recent[s.CleanUps-1].Time
	}
	return s
}

// HistoryLines are the lines of Home's History panel, in order: what was freed, what's still in the
// Trash, then the recent clean-ups.
func HistoryLines(in HomeInput) []Forms {
	switch {
	case !in.HistoryAvailable:
		return []Forms{faintLine("not available")}
	case in.HistoryErr != nil:
		return []Forms{one(Line{{"Couldn't read the history file", ToneDanger}})}
	case !in.HistoryRead:
		return []Forms{faintLine("Reading the history…")}
	}
	s := SummarizeHistory(in.History)
	if s.CleanUps == 0 {
		return []Forms{faintLine("Nothing cleaned yet.")}
	}
	n := "1 clean-up"
	if s.CleanUps != 1 {
		n = fmt.Sprintf("%d clean-ups", s.CleanUps)
	}
	since := s.Since.Local().Format("2 Jan")
	if s.Since.Local().Year() != s.Newest.Local().Year() {
		since = s.Since.Local().Format("2 Jan 2006")
	}
	freed := Line{{HumanBytes(s.Freed), ToneGood}, {" freed in " + n, ToneText}}
	out := []Forms{{Long: append(freed[:2:2], Seg{" since " + since, ToneText}), Short: freed}}
	if s.InTrash > 0 {
		size := Seg{HumanBytes(s.InTrash), ToneWarn}
		out = append(out, Forms{Long: Line{size, {" in the Trash · you can put it back", ToneText}}, Short: Line{size, {" in the Trash · can put back", ToneText}}})
	}
	for _, c := range s.Recent {
		at, size := c.Time.Local(), HumanBytes(c.Size)
		short := at.Format("15:04") + "  "
		out = append(out, Forms{
			Long:  Line{{at.Format("2 Jan 15:04") + "  ", ToneFaint}, {size, ToneText}, {"  " + c.What(), ToneFaint}},
			Short: Line{{short, ToneFaint}, {size, ToneText}, {"  " + fit(c.shortWhat(), shortWidth-utf8.RuneCountInString(short+size)-2), ToneFaint}},
		})
	}
	return out
}

// FreeLine is one line of Home's "What you can free" panel.
type FreeLine struct {
	Key   string // "free/junk"; the cursor follows it
	Tab   int
	Label string // "4 Junk"
	Bar   bool   // a bar and Size come before Note
	Size  uint64
	Safe  uint64 // the bar's green part: what the tab ticks by itself
	Note  Forms  // after the size; without a bar, the whole line after the label
	Spin  bool   // the spinner goes before Note
}

// FreeLines are the lines of "What you can free", in order: Junk, Dev, Apps, Disk and Dupes.
func FreeLines(in HomeInput) []FreeLine {
	junk := scanLine(in.Junk, "free/junk", TabJunk, "4 Junk", func(l *FreeLine) {
		safeNote(l, in.Junk.Items)
	})
	dev := scanLine(in.Dev, "free/dev", TabDev, "5 Dev", func(l *FreeLine) {
		safeNote(l, in.Dev.Items)
	})
	apps := scanLine(in.Apps, "free/apps", TabApps, "8 Apps", func(l *FreeLine) {
		size, count, _ := unusedApps(in.Apps.Items)
		l.Size, l.Note = size, faintLine("none unused")
		if count > 0 {
			l.Note = one(Line{{fmt.Sprintf("%d unused", count), ToneText}})
		}
	})
	dupes := FreeLine{Key: "free/dupes", Tab: TabDupes, Label: "7 Dupes"}
	switch st := in.Dupes; {
	case !st.Available:
		dupes.Note = faintLine("not available")
	case !st.Started:
		dupes.Note = faintLine("not scanned yet · enter scans")
	case st.Scanning:
		dupes.Spin = true
		dupes.Note = Forms{Long: Line{{"finding copies", ToneFaint}}, Short: Line{{"finding copies", ToneFaint}}}
		if st.Progress != "" {
			dupes.Note.Long = Line{{lowerFirst(st.Progress), ToneFaint}}
		}
	default:
		sets := DupeSets(st.Items, false)
		dupes.Bar, dupes.Note = true, faintLine("no copies")
		for _, s := range sets {
			dupes.Size += s.Frees
		}
		switch len(sets) {
		case 0:
		case 1:
			dupes.Note = one(Line{{"1 set", ToneText}})
		default:
			dupes.Note = one(Line{{fmt.Sprintf("%d sets", len(sets)), ToneText}})
		}
		withFailed(&dupes, st)
	}
	return []FreeLine{junk, dev, apps, diskLine(in.Disk), dupes}
}

// scanLine is the line of a tab that scans: Junk, Dev or Apps. done fills in a finished scan.
func scanLine(st ScanState, key string, tab int, label string, done func(*FreeLine)) FreeLine {
	l := FreeLine{Key: key, Tab: tab, Label: label}
	switch {
	case !st.Available:
		l.Note = faintLine("not available")
	case !st.Started:
		l.Note = faintLine("not scanned yet · enter scans")
	case st.Scanning:
		text := "scanning"
		if st.Total > 1 {
			text = fmt.Sprintf("scanning · %d of %d checks", st.Done, st.Total)
		}
		l.Spin, l.Note = true, faintLine(text)
	default:
		l.Bar, l.Size = true, TotalSize(st.Items)
		done(&l)
		withFailed(&l, st)
	}
	return l
}

// safeNote is the note of Junk and Dev: the safe amount, or the largest group when nothing is safe.
func safeNote(l *FreeLine, items []CleanItem) {
	l.Safe = safeSize(items)
	if l.Safe > 0 {
		safe := HumanBytes(l.Safe)
		l.Note = Forms{Long: Line{{safe + " safe", ToneGood}}, Short: Line{{dropUnit(safe, HumanBytes(l.Size)) + " safe", ToneGood}}}
		return
	}
	l.Note = faintLine("none safe")
	if gs := GroupItems(items, nil, nil, false); len(gs) > 0 && gs[0].Size > 0 {
		l.Note.Long = Line{{gs[0].Name + " " + HumanBytes(gs[0].Size), ToneText}}
	}
}

// withFailed adds "1 check failed" in amber: after the note in the long form, instead of it in the
// short one.
func withFailed(l *FreeLine, st ScanState) {
	n := len(st.Errs)
	if n == 0 {
		return
	}
	failed := Seg{"1 check failed", ToneWarn}
	if n > 1 {
		failed.Text = fmt.Sprintf("%d checks failed", n)
	}
	l.Note = Forms{Long: append(append(Line{}, l.Note.Long...), Seg{" · ", ToneFaint}, failed), Short: Line{failed}}
}

// diskLine is the Disk line: the home folder's size and its largest folder once a walk is done.
func diskLine(st DiskState) FreeLine {
	l := FreeLine{Key: "free/disk", Tab: TabDisk, Label: "6 Disk"}
	size := HumanBytes(st.Size)
	if st.SizeMin || st.Stopped {
		size = "≥ " + size
	}
	switch {
	case !st.Available:
		l.Note = faintLine("not available")
	case !st.Started:
		l.Note = faintLine("not walked yet · enter walks")
	case st.Walking:
		l.Spin, l.Note = true, faintLine("walking "+HumanCount(st.Files)+" files")
	case st.Stopped:
		l.Note = Forms{Long: Line{{"home " + size + " · the walk was stopped", ToneText}}, Short: Line{{"home " + size + " · stopped", ToneText}}}
	case st.Biggest == "":
		l.Note = one(Line{{"home " + size, ToneText}})
	default:
		biggest := st.Biggest + " " + HumanBytes(st.BiggestSize)
		l.Note = Forms{Long: Line{{"home " + size + " · biggest: " + biggest, ToneText}}, Short: Line{{"home " + size + " · " + biggest, ToneText}}}
	}
	return l
}

// dropUnit is s without its unit when it has the same unit as of: "11.1" for "11.1 GB" of "13.2 GB".
func dropUnit(s, of string) string {
	num, unit, ok := strings.Cut(s, " ")
	if _, ofUnit, _ := strings.Cut(of, " "); ok && unit == ofUnit {
		return num
	}
	return s
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// MemoryText follows the memory gauge: "14.1/18.0 GB".
func MemoryText(st MemoryStats) string {
	if st.Total == 0 {
		return "Couldn't read memory"
	}
	total := HumanBytes(st.Total)
	return dropUnit(HumanBytes(st.Used), total) + "/" + total
}

// MemoryNote is the second line of Mem: the pressure in its color and the swap, and compressed
// memory in the long form.
func MemoryNote(st MemoryStats) Forms {
	tone := ToneFaint
	switch st.Pressure {
	case PressureNormal:
		tone = ToneGood
	case PressureWarning:
		tone = ToneWarn
	case PressureCritical:
		tone = ToneDanger
	}
	short := Line{{st.Pressure.String(), tone}, {" · swap " + HumanBytes(st.SwapUsed), ToneFaint}}
	return Forms{Long: append(short[:2:2], Seg{" · compressed " + HumanBytes(st.Compressed), ToneFaint}), Short: short}
}

// DiskText follows the disk gauge: "109 GB free", or "109 GB free of 460" in the long form.
func DiskText(free, total uint64) Forms {
	if total == 0 {
		return faintLine("free space unknown")
	}
	f := HumanBytes(free) + " free"
	return Forms{Long: Line{{f + " of " + dropUnit(HumanBytes(total), HumanBytes(free)), ToneText}}, Short: Line{{f, ToneText}}}
}

// PortsHead is the first line of Home's Ports panel, and the ports of apps that aren't part of
// macOS, lowest first, each port once.
func PortsHead(in HomeInput) (Line, []Listener) {
	if in.SocketErr != nil {
		return Line{{"Couldn't read sockets", ToneDanger}}, nil
	}
	var yours []Listener
	n := 0
	seen := map[uint16]bool{}
	for _, l := range in.Listeners {
		if l.System {
			continue
		}
		n++
		if !seen[l.Port] {
			seen[l.Port] = true
			yours = append(yours, l)
		}
	}
	sort.SliceStable(yours, func(i, j int) bool { return yours[i].Port < yours[j].Port })
	from := fmt.Sprintf("%d from your apps", n)
	if n == 0 {
		from = "none from your apps"
	}
	return Line{{fmt.Sprintf("%d listening · ", len(in.Listeners)), ToneText}, {from, ToneText}}, yours
}
