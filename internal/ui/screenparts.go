package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// A list screen is a title line with chips counting what matters, a filter
// line, the list, and, from panelMinWidth, a filled panel at the right about
// the entry under the cursor. These are the parts it is built from.

// screenTitle is the first line of a list screen: its icon and name, a few
// words about what it lists, and the chips at the right (left out when
// there is no room).
func screenTitle(iconName, name, scope string, count int, chips []string, w int) string {
	return screenTitleMore(iconName, name, scope, count, false, chips, w)
}

// screenTitleMore is screenTitle for a list GitHub holds more of than was
// read: the count says so with a +.
func screenTitleMore(iconName, name, scope string, count int, more bool, chips []string, w int) string {
	head := " " + icoIn(iconName, cCyan) + sWhite.Bold(true).Render(name)
	if scope != "" {
		head += sDim.Render("  " + scope)
	}
	if count >= 0 {
		head += sDim.Render(" · ") + sText.Render(countMore(count, more))
	}
	right := strings.Join(chips, " ") + " "
	if len(chips) == 0 || width(head)+width(right)+1 > w {
		return fit(head, w)
	}
	return head + strings.Repeat(" ", w-width(head)-width(right)) + right
}

// listWithPanel puts the panel (when the screen is wide enough) beside the
// list. list draws the list for the given width and height; panel draws the
// panel lines for its width and height, or is nil for a screen without one.
func listWithPanel(a *App, w, h int, head []string, list func(w, h int) string, panel func(w, h int) []string) string {
	return listBeside(w, h, head, list, panel, false)
}

// listWithSteadyPanel is listWithPanel with a panel whose width depends on
// the screen only, for the views of a place that you switch between: the
// line between list and panel stays where it is.
func listWithSteadyPanel(w, h int, head []string, list func(w, h int) string, panel func(w, h int) []string) string {
	return listBeside(w, h, head, list, panel, true)
}

// steadyPanelWidth is the width of a steady panel on a screen w cells wide.
func steadyPanelWidth(w int) int { return min(max(w*36/100, 36), maxPanelWidth) }

func listBeside(w, h int, head []string, list func(w, h int) string, panel func(w, h int) []string, steady bool) string {
	lh := h - len(head)
	if panel == nil || w < panelMinWidth {
		return strings.Join(head, "\n") + "\n" + list(w, lh)
	}
	panelW := min(max(w*3/10, 34), 46)
	if steady {
		panelW = steadyPanelWidth(w)
	}
	listW := w - panelW - 1
	rows := strings.Split(list(listW, lh), "\n")
	if grow := min(unusedRight(rows, listW)-2, maxPanelWidth-panelW); grow > 0 && !steady {
		panelW, listW = panelW+grow, listW-grow
		rows = strings.Split(list(listW, lh), "\n")
	}
	side := panel(panelW, lh)
	lines := append([]string(nil), head...)
	for i := 0; i < lh; i++ {
		l := ""
		if i < len(rows) {
			l = rows[i]
		}
		lines = append(lines, fit(l, listW)+" "+side[i])
	}
	return strings.Join(lines, "\n")
}

// maxPanelWidth is how wide the panel beside a list may grow when the list
// leaves room at its right.
const maxPanelWidth = 64

// unusedRight is how many cells at the right every line of a list leaves
// blank, out of w.
func unusedRight(lines []string, w int) int {
	room, seen := w, false
	for _, l := range lines {
		used := width(strings.TrimRight(ansi.Strip(l), " "))
		if used == 0 {
			continue
		}
		room, seen = min(room, w-used), true
	}
	if !seen {
		return 0
	}
	return room
}

// panelText collects the lines of a panel. Its sections are layers, not
// boxes: a label starts one, a title in white with a hairline that fades out
// beside it, and what follows goes under it until the next label. A panel of
// layers has plain titles without the hairline, and facts are labelled fields.
type panelText struct {
	w      int
	lines  []string
	card   *panelCard
	layers bool
}

// panelCard is a section of a panel as it is filled: its title, the colour
// of its border, the tint it is filled with (none when empty), its lines and
// whether its border is dashed, for what did not happen.
type panelCard struct {
	title  string
	border lipgloss.Color
	fill   lipgloss.Color
	dashed bool
	lines  []string
}

func newPanel(w int) *panelText { return &panelText{w: w} }

// newLayerPanel is a panel whose sections are titles instead of cards.
func newLayerPanel(w int) *panelText { return &panelText{w: w, layers: true} }

// cardInset is the cells a section takes of a line besides its text: the
// bar of a callout, a space after it and one at its end.
const cardInset = 3

// panelInner is the cells a line of a panel can use: its width less a
// margin on each side, and in a section less the room of a callout's bar.
func (p *panelText) inner() int {
	if p.card != nil {
		return max(p.w-3-cardInset, 8)
	}
	return max(p.w-3, 8)
}

// put appends finished lines: to the section that is open, or to the panel.
func (p *panelText) put(lines ...string) {
	if p.card != nil {
		p.card.lines = append(p.card.lines, lines...)
		return
	}
	for _, l := range lines {
		p.lines = append(p.lines, " "+l)
	}
}

// tone gives the open section the colour c, as for one that holds something
// failing: its hairline takes it.
func (p *panelText) tone(c lipgloss.Color) {
	if p.card != nil {
		p.card.border = c
	}
}

// dash draws the open section's hairline dashed, as for something that did
// not run.
func (p *panelText) dash() {
	if p.card != nil {
		p.card.dashed = true
	}
}

// closeCard draws the open section into the panel's lines, a blank line
// apart from what is above it.
func (p *panelText) closeCard() {
	c := p.card
	if c == nil {
		return
	}
	p.card = nil
	if n := len(p.lines); n > 0 && strings.TrimSpace(ansi.Strip(p.lines[n-1])) != "" {
		p.lines = append(p.lines, "")
	}
	p.lines = append(p.lines, cardLines(c, max(p.w-3, 8))...)
}

// cardStroke is the stroke of a section's hairline: dashed for one that did
// not happen.
func cardStroke(c *panelCard) string {
	if c.dashed {
		return "┄"
	}
	return "─"
}

// hairline is n cells of stroke in colour c that fade into the panel.
func hairline(n int, stroke string, c lipgloss.Color) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(fg(lipgloss.Color(mix(string(c), string(cPanel), float64(i)/float64(max(n, 1))))).Render(stroke))
	}
	return b.String()
}

// cardLines draws a section w cells wide as a layer, not a box: its title in
// white with a hairline that fades out beside it, and its lines under it. A
// callout is a bar in its colour on a tint of it.
func cardLines(c *panelCard, w int) []string {
	var out []string
	if c.title != "" {
		t := clip(c.title, max(w-4, 1))
		line := c.border
		if line == cFrame {
			line = lipgloss.Color(mix(string(cFrame), string(cText), 0.25))
		}
		// A title that draws its own edge to its end (a job and its time)
		// needs no hairline after it.
		rest := max(w-width(t)-1, 0)
		tail := hairline(rest, cardStroke(c), line)
		if rest <= 6 {
			tail = strings.Repeat(" ", rest)
		}
		out = append(out, " "+t+" "+tail)
	}
	for _, l := range c.lines {
		if c.fill != "" {
			out = append(out, " "+withBg(fg(c.border).Render("▌")+" "+fit(l, w-cardInset)+" ", c.fill))
			continue
		}
		out = append(out, " "+l)
	}
	return out
}

// lines are the panel's lines, with the card that is open drawn.
func (p *panelText) all() []string {
	p.closeCard()
	return p.lines
}

// wrapLine breaks s into lines of at most w cells; the lines after the first
// are indented, so a long title reads as one thing.
func wrapLine(s string, w int) []string {
	if width(s) <= w {
		return []string{s}
	}
	parts := strings.Split(ansi.Wrap(s, max(w-2, 4), ""), "\n")
	for i := 1; i < len(parts); i++ {
		parts[i] = "  " + parts[i]
	}
	return parts
}

// add appends a line, wrapping it when it is wider than the panel.
func (p *panelText) add(s string) {
	parts := wrapLine(s, p.inner())
	if sig := titleSig(parts[0]); len(p.lines) == 0 && p.card == nil && sig != panelTitle {
		panelTitle, panelScroll = sig, 0
	}
	p.put(parts...)
}

// label starts a section titled s: a rounded card with the title in its top
// edge, or in a panel of layers the title on a line of its own.
func (p *panelText) label(s string) {
	p.closeCard()
	if p.layers {
		if len(p.lines) > 0 && strings.TrimSpace(ansi.Strip(p.lines[len(p.lines)-1])) != "" {
			p.put("")
		}
		p.put(sWhite.Bold(true).Render(s))
		return
	}
	p.card = &panelCard{title: sWhite.Bold(true).Render(s), border: cFrame}
}

// fieldWidth is how many cells the label of a field takes.
const fieldWidth = 13

// field is a fact with its label: the label in grey, the value beside it,
// wrapping under itself when it is long.
func (p *panelText) field(label, value string) {
	pad := fieldWidth - width(label)
	if pad < 1 {
		pad = 1
	}
	parts := strings.Split(ansi.Wrap(value, max(p.inner()-width(label)-pad, 8), ""), "\n")
	p.put(sDim.Render(label) + strings.Repeat(" ", pad) + parts[0])
	for _, l := range parts[1:] {
		p.put(strings.Repeat(" ", width(label)+pad) + l)
	}
}

// row is a line with a short value at the right; a long left side wraps
// under itself instead of being cut.
func (p *panelText) row(left, right string) {
	lw := max(p.inner()-width(right)-1, 6)
	parts := wrapLine(left, lw)
	first := fit(parts[0], lw)
	if right == "" {
		first = parts[0]
	}
	p.put(first + " " + right)
	p.put(parts[1:]...)
}

// callout is a block that stands out, as for what a pull request or an issue
// wants from you: a card in colour c, filled with a tint of it. Right under a
// label it is that section's card; elsewhere a card of its own.
func (p *panelText) callout(c lipgloss.Color, lines ...string) {
	if p.card == nil || len(p.card.lines) > 0 {
		p.closeCard()
		p.card = &panelCard{}
	}
	p.card.border, p.card.fill = c, lipgloss.Color(mix(string(cPanel), string(c), 0.14))
	for _, l := range lines {
		p.put(wrapLine(l, p.inner())...)
	}
	p.closeCard()
}

// chips lays out chips side by side, wrapping onto a new line when the next
// one doesn't fit.
func (p *panelText) chips(cs []string) {
	line := ""
	for _, c := range cs {
		switch {
		case line == "":
			line = c
		case width(line)+1+width(c) <= p.inner():
			line += " " + c
		default:
			p.put(line)
			line = c
		}
	}
	if line != "" {
		p.put(line)
	}
}

// panelScroll is how far the panel on display is scrolled, and panelTitle
// its first line when that was set, so a new selection starts at the top.
var (
	panelScroll int
	panelTitle  string
)

// scrollPanelBy moves the panel by n lines; render clamps it.
func scrollPanelBy(n int) { panelScroll = max(panelScroll+n, 0) }

// render fits the panel to h lines on the panel background. A panel taller
// than h is scrolled, with a last line saying how much is out of sight.
func (p *panelText) render(h int) []string {
	lines := p.all()
	var foot string
	if len(lines) > h && h > 2 {
		room := h - 1
		panelScroll = min(panelScroll, len(lines)-room)
		above, below := panelScroll, len(lines)-room-panelScroll
		lines = lines[panelScroll : panelScroll+room]
		var parts []string
		if above > 0 {
			parts = append(parts, fmt.Sprintf("↑ %d above", above))
		}
		if below > 0 {
			parts = append(parts, fmt.Sprintf("↓ %d more", below))
		}
		foot = " " + sDim.Render(strings.Join(parts, " · ")+" · alt+↑↓")
	} else {
		panelScroll = 0
	}
	edge := " "
	out := make([]string, h)
	for i := range out {
		l := ""
		if i < len(lines) {
			l = lines[i]
		} else if foot != "" && i == h-1 {
			l = foot
		}
		out[i] = onPanel(edge+clip(l, p.w-1), p.w)
	}
	return out
}

// emptyPanel is a panel with nothing selected.
func emptyPanel(w, h int, msg string) []string {
	p := newPanel(w)
	p.add(sDim.Render(msg))
	return p.render(h)
}

// workflowRuns are the runs of one workflow of a repo that we already hold,
// newest first.
func (a *App) workflowRuns(repo, workflow string) []gh.Run {
	var out []gh.Run
	for _, r := range a.st.status[repo] {
		if r.Name == workflow {
			out = append(out, r)
		}
	}
	return out
}

// Overrunning is a run still going after more than overrunFactor times its
// usual time, and at least overrunMargin more: a job that hangs holds a runner
// until its timeout, six hours by default.
const (
	overrunFactor = 2
	overrunMargin = 10 * time.Minute
)

// overrunning says a running run has taken far longer than its usual time,
// the median of the successful runs it is compared with, and returns that.
func (a *App) overrunning(x repoRun, now time.Time) (time.Duration, bool) {
	if x.run.Status != "in_progress" {
		return 0, false
	}
	usual := medianDuration(a.comparable(x.repo, x.run, a.workflowRuns(x.repo, x.run.Name)))
	took := x.run.Duration(now)
	return usual, usual > 0 && took > overrunFactor*usual && took-usual >= overrunMargin
}

// medianDuration is the median wall-clock time of the finished, successful
// runs, or 0 when fewer than three finished.
func medianDuration(runs []gh.Run) time.Duration {
	var ds []time.Duration
	for _, r := range runs {
		if r.Status == "completed" && r.Conclusion == "success" {
			ds = append(ds, r.Duration(time.Now()))
		}
	}
	return median(ds)
}

// median is the middle of ds, or 0 for fewer than three.
func median(ds []time.Duration) time.Duration {
	if len(ds) < 3 {
		return 0
	}
	slices.Sort(ds)
	return ds[len(ds)/2]
}

// runKinds are the results of runs, oldest first, for history bars.
func runKinds(runs []gh.Run) []Kind {
	var out []Kind
	for i := len(runs) - 1; i >= 0; i-- {
		out = append(out, StatusKind(runs[i].Status, runs[i].Conclusion))
	}
	return out
}

// jobSkeleton stands in for the jobs of a run while they are read: a dot
// and a bar for each, the bars shimmering as light sweeps over them.
func jobSkeleton(w int) []string {
	var out []string
	for i, share := range []float64{0.55, 0.38, 0.47} {
		name := max(int(float64(w-12)*share), 4)
		line := fg(cMeterOff).Render("●") + " " + shimmerBar(name, float64(i)*0.12)
		out = append(out, line+strings.Repeat(" ", max(w-width(line)-6, 1))+shimmerBar(6, float64(i)*0.12+0.3))
	}
	return out
}

// jobLines lists a run's jobs from the cache, a chip-less glyph and a
// duration each, with the failing or running steps under their job.
func (a *App) jobLines(p *panelText, repo string, run gh.Run, now time.Time) {
	jobs, cached := a.client.CachedJobs(repo, run.ID)
	if !cached {
		for _, l := range jobSkeleton(p.inner()) {
			p.add(l)
		}
	}
	for _, j := range jobs {
		jk := jobKind(j)
		name := fg(jk.RowColor()).Render(j.Name)
		if jk == KFail {
			name = sRed.Render(j.Name)
		}
		p.row(glyph(jk, a.frame)+" "+name, sText.Render(jobDur(j, now)))
		if jk != KFail && jk != KRun {
			continue
		}
		for _, st := range j.Steps {
			if sk := stepKind(st); sk == KFail || sk == KRun {
				p.row("   "+glyph(sk, a.frame)+" "+fg(sk.Color()).Render(st.Name), sDim.Render(stepDur(st, now)))
			}
		}
	}
}

// jobProgress counts the finished and all jobs of a run from the cache.
func (a *App) jobProgress(repo string, run gh.Run) (done, total int, ok bool) {
	jobs, cached := a.client.CachedJobs(repo, run.ID)
	if !cached || len(jobs) == 0 {
		return 0, 0, false
	}
	for _, j := range jobs {
		if k := jobKind(j); k != KRun && k != KQueue && k != KWait {
			done++
		}
	}
	return done, len(jobs), true
}

// peeker reads the jobs of the run under the cursor once the cursor has
// rested on it, so a panel can show them. Each finished run is read once.
type peeker struct {
	seq   int
	asked map[int64]bool
}

type peekMsg struct {
	id, seq int
	repo    string
	runID   int64
}

type peekedMsg struct{ id int }

func (m peekMsg) viewID() int   { return m.id }
func (m peekedMsg) viewID() int { return m.id }

// ask schedules the read for run, when it is worth one.
func (p *peeker) ask(a *App, id int, repo string, run *gh.Run) tea.Cmd {
	if run == nil || a.w < panelMinWidth {
		return nil
	}
	if p.asked == nil {
		p.asked = map[int64]bool{}
	}
	if p.asked[run.ID] && !run.Active() {
		return nil
	}
	if _, cached := a.client.CachedJobs(repo, run.ID); cached && !run.Active() {
		return nil
	}
	p.seq++
	seq, runID := p.seq, run.ID
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return peekMsg{id, seq, repo, runID} })
}

// handle runs the read when the cursor stayed; ok says the message was one of its own.
func (p *peeker) handle(a *App, id int, msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch m := msg.(type) {
	case peekMsg:
		if m.seq != p.seq {
			return nil, true
		}
		if p.asked == nil {
			p.asked = map[int64]bool{}
		}
		p.asked[m.runID] = true
		return func() tea.Msg {
			_, _ = a.client.ListJobs(context.Background(), m.repo, m.runID)
			return peekedMsg{id}
		}, true
	case peekedMsg:
		return nil, true
	}
	return nil, false
}

// titleSig is what a panel's first line says, without its styling, glyphs
// and spinners, so a panel is only taken for a new one when what it is about changes.
func titleSig(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '/' || r == '#' {
			return r
		}
		return -1
	}, ansi.Strip(s))
}

// countMore is a count with a + when there are more than were read: "100+".
func countMore(n int, more bool) string {
	if more {
		return fmt.Sprintf("%d+", n)
	}
	return fmt.Sprint(n)
}
