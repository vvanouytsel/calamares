package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// Palette (the "Cockpit" look): light text on the terminal's own dark
// background, grey for what matters less, one blue accent for where you are,
// and colour kept for meaning: green passed, red failed, amber running,
// orange waits for a person. Blue is only for focus; row data is light text. Gold is the brand.
var (
	cBg        = lipgloss.Color("#0b1119") // the base of every cell
	cMeterOff  = lipgloss.Color("#1b2a45")
	cGutter    = lipgloss.Color("#3b5170")
	cCodeBg    = lipgloss.Color("#16263d")
	cFrame     = lipgloss.Color("#2f3747") // a frame at rest
	cFrameDim  = lipgloss.Color("#2f3747") // a frame beside the one in focus
	cCyan      = lipgloss.Color("#58a6ff") // the accent: focus, links, where you are
	cRow       = lipgloss.Color("#c9d1d9")
	cWait      = lipgloss.Color("#ff9a3c") // waits for a person
	cGold      = lipgloss.Color("#f0b64a")
	cGreen     = lipgloss.Color("#3fd68b")
	cRed       = lipgloss.Color("#ff5d6c")
	cAmber     = lipgloss.Color("#f5b942")
	cTan       = lipgloss.Color("#e3b341")
	cRef       = lipgloss.Color("#9fd36b") // branches, commits and versions
	cViolet    = lipgloss.Color("#b392f0") // merged and done
	cTeal      = lipgloss.Color("#56d4c4") // people
	cPink      = lipgloss.Color("#f08cc0") // comments
	cSteel     = lipgloss.Color("#8495ad") // times and other quiet facts
	cBlue      = lipgloss.Color("#58a6ff")
	cDim       = lipgloss.Color("#657187")
	cGrey      = lipgloss.Color("#a3aebf")
	cText      = lipgloss.Color("#c9d1d9")
	cHint      = lipgloss.Color("#a3aebf")
	cWhite     = lipgloss.Color("#f0f3f6")
	cBand      = lipgloss.Color("#101826") // the header band
	cPanel     = lipgloss.Color("#0e1520") // a detail panel beside a list
	cAccentBg  = lipgloss.Color("#1d4177") // the view on display in a strip
	cSelBg     = lipgloss.Color("#16243d") // the row under the cursor
	cBlurSelBg = lipgloss.Color("#171c26") // the same, in a panel that isn't in focus
	cCrumbBg   = lipgloss.Color("#232a38") // key caps, badges and buttons at rest
	cCrumbFg   = lipgloss.Color("#c9d1d9")
	cOnAccent  = lipgloss.Color("#06101f") // text on an accent or status background
	cMatchBg   = lipgloss.Color("#5a4a12")
	cErrBg     = lipgloss.Color("#1a0d12")
	// cFailFrame borders what belongs to a failed job.
	cFailFrame = lipgloss.Color("#a3323f")
)

// nerd says whether icons come from a Nerd Font; otherwise ordinary symbols
// are used, or nothing where there is no fitting one.
var nerd bool

// icons maps what an icon shows to its Nerd Font glyph and its plain stand-in.
var icons = map[string][2]string{
	"brand":     {"", "◆"},
	"repo":      {"", ""},
	"branch":    {"", ""},
	"pr":        {"", ""},
	"actions":   {"", ""},
	"watching":  {"", "∿"},
	"tag":       {"", "◈"},
	"issue":     {"", "◎"},
	"commit":    {"", ""},
	"clock":     {"", ""},
	"user":      {"", ""},
	"workflow":  {"", ""},
	"search":    {"", "/"},
	"star":      {"", "★"},
	"lock":      {"", "⚿"},
	"gauge":     {"", ""},
	"bell":      {"", ""},
	"comment":   {"", ""},
	"comments":  {"", ""},
	"code":      {"", ""},
	"checks":    {"", ""},
	"pencil":    {"", ""},
	"at":        {"", "@"},
	"file":      {"", ""},
	"label":     {"", ""},
	"milestone": {"", ""},
	"eye":       {"", ""},
	"calendar":  {"", ""},
	"merge":     {"", ""},
	"history":   {"", ""},
	"shield":    {"", ""},
	"warning":   {"", "⚠"},
	"platform":  {"", "▦"},
	"server":    {"", ""},
	"key":       {"", ""},
	"rocket":    {"", ""},
	"flaky":     {"", "~"},
	"fail":      {"", "✗"},
	"trophy":    {"", "★"},
	"compass":   {"", "◈"},
	"question":  {"", "?"},
	"archive":   {"\U000f003c", "▤"},
}

// icon returns the icon for name followed by a space, or "" when the plain
// set has none for it.
func icon(name string) string {
	g := icons[name][1]
	if nerd {
		g = icons[name][0]
	}
	if g == "" || ascii {
		return ""
	}
	return g + " "
}

// iconColor is the colour an icon carries by what it stands for: gold for
// repos and tags, the ref colour for branches and commits, violet for a
// merge, teal for people, pink for comments, steel for times and other quiet
// facts. The text beside an
// icon stays light; only the glyph is coloured.
func iconColor(name string) lipgloss.Color {
	switch name {
	case "repo", "tag", "star", "brand", "milestone":
		return cGold
	case "branch", "commit":
		return cRef
	case "merge":
		return cViolet
	case "user", "at":
		return cTeal
	case "comment", "comments":
		return cPink
	case "pr", "issue", "checks":
		return cGreen
	case "bell":
		return cAmber
	case "lock", "eye":
		return cDim
	case "actions", "watching", "search":
		return cCyan
	}
	return cSteel
}

// ico is the icon for name in its own colour, followed by a space.
func ico(name string) string { return icoIn(name, iconColor(name)) }

// icoIn is the icon for name in colour c, followed by a space.
func icoIn(name string, c lipgloss.Color) string {
	g := icon(name)
	if g == "" {
		return ""
	}
	// The glyph and its space are drawn together: a Nerd Font glyph is often
	// wider than its cell, and a space in a span of its own would paint over it.
	return fg(c).Render(g)
}

// fact is an icon in its colour and a piece of metadata in light text.
func fact(name, text string) string { return ico(name) + sText.Render(text) }

// iconCell is a table cell with an icon in colour c before text in st.
func iconCell(name, text string, st lipgloss.Style, c lipgloss.Color) Cell {
	return Cell{Text: icon(name) + text, Styled: icoIn(name, c) + st.Render(text)}
}

// labelChip is a GitHub label as a chip: its dot and its name on a tint of
// its own colour.
func labelChip(name, hex string) string {
	if hex == "" {
		hex = string(cGrey)
	}
	if !strings.HasPrefix(hex, "#") {
		hex = "#" + hex
	}
	col := hex
	// Very dark or very light label colours are brought towards the text so
	// they stay readable on the theme.
	r, g, b := rgb(col)
	lum := (299*r + 587*g + 114*b) / 1000
	switch {
	case lum < 70 && !currentTheme.Light:
		col = mix(col, string(cText), 0.5)
	case lum > 200 && currentTheme.Light:
		col = mix(col, string(cText), 0.5)
	}
	bg := mix(string(cBg), col, 0.2)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Background(lipgloss.Color(bg)).Render(" ● " + name + " ")
}

func fg(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	sBlueDigit = fg(lipgloss.Color("#8fb4e8"))
	sDim       = fg(cDim)
	sText      = fg(cText)
	sWhite     = fg(cWhite)
	sBold      = fg(cWhite).Bold(true)
	sCyan      = fg(cCyan)
	sWait      = fg(cWait)
	sGold      = fg(cGold)
	sGrey      = fg(cGrey)
	sTan       = fg(cTan)
	sGreen     = fg(cGreen)
	sRed       = fg(cRed)
	sAmber     = fg(cAmber)
	sBlue      = fg(cBlue)
	sHint      = fg(cHint)
	sRow       = fg(cRow)
	sSel       = lipgloss.NewStyle().Foreground(cWhite).Background(cSelBg)
	sBlurSel   = lipgloss.NewStyle().Foreground(cText).Background(cBlurSelBg)
	sKeycap    = lipgloss.NewStyle().Foreground(cWhite).Background(cCrumbBg).Bold(true)
	sHeader    = lipgloss.NewStyle().Foreground(cGrey).Bold(true)
)

// keycap draws a key as a small cap, as in the key bar.
func keycap(k string) string {
	if !pillsOn() {
		return sKeycap.Render(" " + k + " ")
	}
	return fg(cCrumbBg).Render(pillLeft) + sKeycap.Render(k) + fg(cCrumbBg).Render(pillRight)
}

// pill draws a short label on a background of its own.
func pill(label string, bg, fg lipgloss.Color) string {
	return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(true).Render(" " + label + " ")
}

// statusPill is a status as a pill in its colour: " ✓ passed ".
func statusPill(k Kind, frame int, label string) string {
	if label == "" {
		label = k.Label()
	}
	return roundPill(k.Glyph(frame)+" "+label, cOnAccent, k.Color())
}

// mainTab is one of the top-level places in the top bar.
func mainTab(label string, active bool) string {
	if active {
		return sWhite.Background(cAccentBg).Bold(true).Render(" " + label + " ")
	}
	return sHint.Render(" " + label + " ")
}

// stripItem is one view in a strip: its icon, its label and its count. The
// count (and the icon) take St while something in the view waits on you.
type stripItem struct {
	Label string
	Icon  string
	Count string
	St    *lipgloss.Style
	// Quiet dims a view that holds nothing, such as a section that is empty.
	Quiet bool
}

// viewStrip is the third row of every screen with views: the views as one
// strip, the one on display filled and in brackets, and the key that moves
// between them at the right. Every view keeps its place, so the strip does
// not shift when you move. It changes at once: moving between views has no
// animation, so it never feels behind your keys.
func viewStrip(items []stripItem, active, w int) string {
	var sig string
	for _, it := range items {
		sig += it.Label + "|"
	}
	hint := sDim.Render("tab ⇥")
	if ascii {
		hint = sDim.Render("tab")
	}
	room := w - width(hint) - 2
	if pillsOn() {
		room -= len(items) + 2 // the dividers and the round ends
	}
	segs := stripSegs(items, active, sig, true)
	if stripWidth(segs) > room {
		segs = stripSegs(items, active, sig, false)
	}
	from, to := stripWindow(segs, active, room)
	line := " "
	if from > 0 {
		line = sDim.Render("‹")
	}
	if pillsOn() {
		line += segmented(segs[from:to], active-from)
	} else {
		line += strings.Join(segs[from:to], "")
	}
	if to < len(segs) {
		line += sDim.Render("›")
	}
	gap := w - width(line) - width(hint) - 1
	if gap < 1 {
		return fit(line, w)
	}
	return line + strings.Repeat(" ", gap) + hint
}

// stripBg is the surface of the views of a strip that are not on display.
func stripBg() lipgloss.Color { return lipgloss.Color(mix(string(cBg), string(cText), 0.08)) }

// segmented joins the views of a strip into one capsule: round ends, a thin
// divider between two views at rest, the view on display filled.
func segmented(segs []string, active int) string {
	end := func(i int) lipgloss.Color {
		if i == active {
			return cAccentBg
		}
		return stripBg()
	}
	var b strings.Builder
	b.WriteString(fg(end(0)).Render(pillLeft))
	for i, s := range segs {
		// A cell between every two views, so none moves when another is
		// put on display: a divider between two at rest.
		if divider := "│"; i > 0 {
			if i == active || i-1 == active {
				divider = " "
			}
			b.WriteString(lipgloss.NewStyle().Foreground(cBg).Background(stripBg()).Render(divider))
		}
		b.WriteString(s)
	}
	b.WriteString(fg(end(len(segs) - 1)).Render(pillRight))
	return b.String()
}

func stripWidth(segs []string) int {
	n := 0
	for _, s := range segs {
		n += width(s)
	}
	return n
}

// stripWindow is the run of views that fits in w cells and holds the active
// one: all of them when they fit, else as many as fit around it.
func stripWindow(segs []string, active, w int) (from, to int) {
	if stripWidth(segs) <= w || active < 0 || active >= len(segs) {
		return 0, len(segs)
	}
	from, to = active, active+1
	used := width(segs[active]) + 2
	for grew := true; grew; {
		grew = false
		if to < len(segs) && used+width(segs[to]) <= w {
			used += width(segs[to])
			to++
			grew = true
		}
		if from > 0 && used+width(segs[from-1]) <= w {
			from--
			used += width(segs[from])
			grew = true
		}
	}
	return from, to
}

// stripSegs draws each view of a strip, with or without its icon.
// activeEnds are the ends of what is on display in a strip or the header: the
// round ends of a pill with a Nerd Font, otherwise brackets that light up as
// it changes.
func activeEnds(p float64) (open, shut string) {
	if pillsOn() {
		return fg(cAccentBg).Render(pillLeft), fg(cAccentBg).Render(pillRight)
	}
	return glowBracket("[", p), glowBracket("]", p)
}

func stripSegs(items []stripItem, active int, sig string, icons bool) []string {
	var out []string
	for i, it := range items {
		bg := cBg
		if pillsOn() {
			bg = stripBg()
		}
		if i == active {
			bg = cAccentBg
		}
		on := lipgloss.NewStyle().Background(bg)
		iconC, labelSt, countSt := cSteel, sText, sDim
		if it.St != nil {
			countSt = *it.St
			if c, ok := it.St.GetForeground().(lipgloss.Color); ok {
				iconC = c
			}
		}
		switch {
		case i == active:
			iconC, labelSt = cWhite, sWhite.Bold(true)
		case it.Quiet:
			iconC, labelSt, countSt = cDim, sDim, sDim
		}
		g := ""
		if icons {
			g = icon(it.Icon)
		}
		open, shut := " ", " "
		if pillsOn() {
			open, shut = "", ""
		}
		if i == active {
			p := glowProgress(changedAt("strip:"+sig, active), stripGlowMs)
			if !pillsOn() {
				open, shut = activeEnds(p)
			}
			if p < 1 {
				units := []glowUnit{{" ", on}}
				if g != "" {
					units = append(units, glowUnit{g, on.Foreground(iconC)})
				}
				units = append(units, glowUnit{it.Label, labelSt})
				if it.Count != "" {
					units = append(units, glowUnit{" ", on}, glowUnit{it.Count, countSt.Bold(true)})
				}
				units = append(units, glowUnit{" ", on})
				out = append(out, open+glowSeg(units, cAccentBg, cCyan, p)+shut)
				continue
			}
		}
		seg := on.Render(" ")
		if g != "" {
			seg += on.Foreground(iconC).Render(g)
		}
		seg += labelSt.Background(bg).Render(it.Label)
		if it.Count != "" {
			seg += on.Render(" ") + countSt.Background(bg).Bold(i == active).Render(it.Count)
		}
		seg += on.Render(" ")
		out = append(out, open+seg+shut)
	}
	return out
}

// chip is a filter with the key that changes it: a key cap and what it is
// set to, bright while it narrows the list.
func chip(key, text string, on bool) string {
	st := sHint
	if on {
		st = lipgloss.NewStyle().Foreground(cCyan).Bold(true)
	}
	return keycap(key) + " " + st.Render(text)
}

// accentBar marks the row under the cursor at its left edge.
func accentBar() string {
	if ascii {
		return fg(cCyan).Render(">")
	}
	return fg(cCyan).Render("▌")
}

// Kind is the visual status of a run, job or step.
type Kind int

const (
	KNone Kind = iota
	KOK
	KFail
	KRun
	KQueue
	KWait
	KCancel
	KSkip
)

// ascii switches all glyphs to plain characters for terminals without good Unicode.
var ascii bool

// StatusKind maps a GitHub status/conclusion pair to a Kind.
func StatusKind(status, conclusion string) Kind {
	switch status {
	case "in_progress":
		return KRun
	case "queued", "requested", "pending":
		return KQueue
	case "waiting":
		return KWait
	case "completed", "":
	default:
		return KQueue
	}
	switch conclusion {
	case "success":
		return KOK
	case "failure", "timed_out", "startup_failure":
		return KFail
	case "cancelled":
		return KCancel
	case "action_required":
		return KWait
	case "skipped", "neutral", "stale":
		return KSkip
	case "":
		if status == "completed" {
			return KSkip
		}
		return KNone
	}
	return KSkip
}

func (k Kind) Color() lipgloss.Color {
	switch k {
	case KOK:
		return cGreen
	case KFail:
		return cRed
	case KRun:
		return cAmber
	case KQueue:
		return cGrey
	case KWait:
		return cWait
	}
	return cDim
}

// RowColor is the text colour of a table row with this status. Only the
// status cell carries the status colour; rows stay calm, and only finished-
// without-result rows (cancelled, skipped, no CI) are dimmed.
func (k Kind) RowColor() lipgloss.Color {
	switch k {
	case KCancel, KSkip, KNone:
		return cDim
	}
	return cText
}

var spinFrames = []string{"◐", "◓", "◑", "◒"}

// animated is set whenever something is drawn that moves with the animation
// frame. Screen clears it before drawing and reads it after, to know whether
// the next frame needs drawing at all. Drawing happens on one goroutine.
var animated bool

// spin picks the frame of an animation, and notes that one is on display.
func spin(frames []string, frame int) string {
	animated = true
	return frames[frame%len(frames)]
}

// Glyph returns the status symbol; frame animates running items.
func (k Kind) Glyph(frame int) string {
	if ascii {
		return map[Kind]string{KOK: "v", KFail: "x", KRun: `*`, KQueue: "o", KWait: "!", KCancel: "/", KSkip: "-", KNone: "."}[k]
	}
	if nerd {
		switch k {
		case KOK:
			return "\uf058"
		case KFail:
			return "\uf057"
		case KQueue:
			return "\uf10c"
		case KWait:
			return "\uf06a"
		case KCancel:
			return "\uf05e"
		}
	}
	switch k {
	case KOK:
		return "✓"
	case KFail:
		return "✗"
	case KRun:
		return spin(spinFrames, frame)
	case KQueue:
		return "○"
	case KWait:
		return "◆"
	case KCancel:
		return "⊘"
	case KSkip:
		return "–"
	}
	return "·"
}

func (k Kind) Label() string {
	return map[Kind]string{KOK: "passed", KFail: "failed", KRun: "running", KQueue: "queued", KWait: "waiting", KCancel: "canceled", KSkip: "skipped", KNone: "no runs"}[k]
}

// badge renders "✓ passed" in the status colour.
func badge(k Kind, frame int, label string) string {
	if label == "" {
		label = k.Label()
	}
	return fg(k.Color()).Render(k.Glyph(frame) + " " + label)
}

func glyph(k Kind, frame int) string { return fg(k.Color()).Render(k.Glyph(frame)) }

// spark renders one block per result, oldest first.
func spark(kinds []Kind, frame int) (styled, plain string) {
	block := "■"
	if ascii {
		block = "#"
	}
	for _, k := range kinds {
		g := block
		if k == KRun {
			animated = true
			if frame%2 == 1 {
				g = "□"
				if ascii {
					g = "+"
				}
			}
		}
		styled += fg(k.Color()).Render(g)
		plain += g
	}
	return
}

// progress renders "▰▰▰▱▱ 3/5".
func progress(done, total, failed int) (styled, plain string) {
	on, off := "▰", "▱"
	if ascii {
		on, off = "#", "-"
	}
	for i := 0; i < total && i < 10; i++ {
		switch {
		case i < done-failed:
			styled += sAmber.Render(on)
			plain += on
		case i < done:
			styled += sRed.Render(on)
			plain += on
		default:
			styled += sDim.Render(off)
			plain += off
		}
	}
	tail := fmt.Sprintf(" %d/%d", done, total)
	return styled + sAmber.Render(tail), plain + tail
}

// checkMeter is a bar of w cells split between passed, failed and pending, in
// their colours; a part that is there never rounds away to nothing.
func checkMeter(passed, failed, pending, w int) string {
	total := passed + failed + pending
	if total == 0 || w <= 0 {
		return ""
	}
	w = min(w, 30)
	on, off := "━", "╍"
	if ascii {
		on, off = "=", "-"
	}
	cells := func(n int) int {
		if n == 0 {
			return 0
		}
		return max(n*w/total, 1)
	}
	f, r := cells(failed), cells(pending)
	g := max(w-f-r, 0)
	if passed == 0 {
		g = 0
		if failed > 0 {
			f = w - r
		} else {
			r = w
		}
	}
	return sGreen.Render(strings.Repeat(on, g)) + sRed.Render(strings.Repeat(on, f)) + sAmber.Render(strings.Repeat(off, r))
}

// age formats how long ago t was: 12s, 3m, 4h, 2d, 5mo, 1y.
func age(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}

// dur formats a duration: 4s, 2:22, 1:02:03.
// dur formats a duration as "45s", "21m 35s" or "1h 02m".
func dur(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	s := int(d.Round(time.Second).Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh %02dm", s/3600, s/60%60)
}

// stepDur formats a step's duration; steps that never started show nothing.
func stepDur(s gh.Step, now time.Time) string {
	if s.StartedAt.IsZero() {
		return ""
	}
	return max0(dur(s.Duration(now)))
}

// jobDur formats a job's duration; jobs that never started show nothing.
func jobDur(j gh.Job, now time.Time) string {
	if j.StartedAt.IsZero() {
		return ""
	}
	return max0(dur(j.Duration(now)))
}

// max0 shows sub-second (rounded to zero) durations as "0s" instead of "—".
func max0(s string) string {
	if s == "—" {
		return "0s"
	}
	return s
}

// withSelectionBg is withSelection on a background of your choice.
func withSelectionBg(s string, bg lipgloss.Color, bold bool) string {
	st := lipgloss.NewStyle().Background(bg).Bold(bold)
	const marker = "\x00"
	start, _, found := strings.Cut(st.Render(marker), marker)
	if !found || start == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+start)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+start)
	return start + s + "\x1b[0m"
}

// withSelection lays the selection background under s (which may contain ANSI
// codes) and keeps its own colours, so a selected row still shows its status.
func withSelection(s string, blur bool) string {
	bg := cSelBg
	if blur {
		bg = cBlurSelBg
	}
	st := lipgloss.NewStyle().Background(bg)
	if !blur {
		st = st.Bold(true)
	}
	const marker = "\x00"
	start, _, found := strings.Cut(st.Render(marker), marker)
	if !found || start == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+start)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+start)
	s = start + s + "\x1b[0m"
	if blur {
		return s
	}
	return fadeAcross(s, cSelBg, lipgloss.Color(mix(string(cSelBg), string(cCyan), 0.14)), lipgloss.Color(mix(string(cSelBg), string(cBg), 0.8)))
}

// fadeAcross lights the cells of s that are on bg from hot at the left to
// cool at the right, as light that falls from the cursor and fades out along
// the row. It needs true colour; elsewhere s stays as it is.
func fadeAcross(s string, bg, hot, cool lipgloss.Color) string {
	if lipgloss.ColorProfile() != termenv.TrueColor {
		return s
	}
	n := width(s)
	if n < 2 {
		return s
	}
	p := newPatch([]string{s}, n)
	for i := range n {
		if c := p.at(i, 0); nearColor(c.bg, string(bg)) {
			c.bg = mix(string(hot), string(cool), math.Pow(float64(i)/float64(n-1), 1.4))
		}
	}
	return p.lines()[0]
}

// onBand draws s on the header band, padded to w cells.
func onBand(s string, w int) string { return withBg(fit(s, w), cBand) }

// onPanel draws s on the background of a detail panel, padded to w cells.
func onPanel(s string, w int) string { return withBg(fit(s, w), cPanel) }

// withBg gives s a background, also behind the parts that have a style of
// their own.
func withBg(s string, bg lipgloss.Color) string {
	const marker = "\x00"
	start, _, found := strings.Cut(lipgloss.NewStyle().Background(bg).Foreground(cText).Render(marker), marker)
	if !found || start == "" {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+start)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+start)
	return start + s + "\x1b[0m"
}

// tint is the background of a status chip: its colour, very dark.
var tint = map[Kind]lipgloss.Color{
	KOK: "#12301f", KFail: "#3a161c", KRun: "#3a2e10", KWait: "#3a2410",
}

// statusChip is a status as a chip: its colour on a tint of it, with its
// glyph. label "" uses the status's own word.
func statusChip(k Kind, frame int, label string) string {
	if label == "" {
		label = k.Label()
	}
	text := k.Glyph(frame) + " " + label
	bg, ok := tint[k]
	if !ok {
		bg = lipgloss.Color(mix(string(cBg), string(k.Color()), 0.14))
	}
	if k == KRun {
		bg = breathe(bg)
	}
	return roundPill(text, k.Color(), bg)
}

// countChip is a small count on a tint of its colour, or nothing for zero.
func countChip(n int, c lipgloss.Color) string { return flareChip("", n, c) }

// counts remembers the count each chip with a key showed, and when it went
// up, so the chip can flare.
var counts = map[string]struct {
	n  int
	at int64
}{}

// flareChip is a count chip that flares for a moment when its count goes up.
func flareChip(key string, n int, c lipgloss.Color) string {
	at := int64(0)
	if key != "" {
		prev, seen := counts[key]
		at = prev.at
		if seen && n > prev.n {
			at = nowMs()
		}
		counts[key] = struct {
			n  int
			at int64
		}{n, at}
	}
	if n <= 0 {
		return ""
	}
	bg := cCrumbBg
	switch c {
	case cWait:
		bg = tint[KWait]
	case cAmber:
		bg = tint[KRun]
	}
	st := lipgloss.NewStyle().Foreground(c)
	if at != 0 && animLevel >= animFull {
		if p := progress01(at, 900); p < 1 {
			bg = lipgloss.Color(mix(string(bg), string(c), 0.7*(1-ease(p))))
			st = st.Bold(true)
			if p < 0.5 {
				st = st.Foreground(cOnAccent)
			}
		}
	}
	if !pillsOn() {
		return st.Background(bg).Render(fmt.Sprintf(" %d ", n))
	}
	return fg(bg).Render(pillLeft) + st.Background(bg).Render(fmt.Sprint(n)) + fg(bg).Render(pillRight)
}
