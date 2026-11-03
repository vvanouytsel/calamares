package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The Achievements place: your level and what it is worth at the top, then
// a trophy case of medals by area (or the ones you are close to, or a
// timeline of what you unlocked), and a panel about the one under the cursor.

const (
	achCase = iota
	achAlmost
	achTimeline
	achRecords
)

var achViews = []string{"Trophy case", "Almost there", "Timeline", "Records"}

// medalCell is the cells a medal takes in the trophy case, with the room
// around it: a cell of margin on each side.
const medalCell = 12

// heroMinHeight is the height from which the top shows the trophy.
const heroMinHeight = 30

type achievementsView struct {
	id       int
	view     int
	sel      string // the id of the achievement under the cursor
	offset   int    // the first line of the case on display
	slots    []medalSlot
	timeline *Table
	rec      recordsState
}

// medalSlot is where a medal was drawn: its row of medals, its column and
// its first line, and whether the heading of its area is right above it.
type medalSlot struct {
	id             string
	row, col, line int
	underHead      bool
}

func newAchievementsView(a *App) *achievementsView {
	return &achievementsView{id: a.newID(), sel: achievements[0].id, timeline: newTable(
		Column{Title: "WHEN", Min: 10, Help: "when you reached it"},
		Column{Title: "ACHIEVEMENT", Max: 40, Flex: true},
		Column{Title: "TIER", Min: 8, Help: "bronze, silver, gold or platinum: what is counted has up to four, the rest one"},
		Column{Title: "AREA", Max: 22, Flex: true, Drop: 1, Help: "the part of Calamares it belongs to"},
		Column{Title: "XP", Right: true, Help: "what reaching it was worth"},
	)}
}

func (v *achievementsView) ID() int             { return v.id }
func (v *achievementsView) Crumb() string       { return "Achievements" }
func (v *achievementsView) ViewName() string    { return achViews[v.view] }
func (v *achievementsView) Init(a *App) tea.Cmd { return nil }

func (v *achievementsView) Refresh(a *App, force bool) tea.Cmd {
	if v.view == achRecords {
		return v.loadRecords(a, force)
	}
	return nil
}

// shown are the achievements of the view on display, in their order.
func (v *achievementsView) shown(a *App) []*achievement {
	switch v.view {
	case achAlmost:
		var out []*achievement
		for _, x := range achievements {
			if a.ach.almost(x) {
				out = append(out, x)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return a.ach.fraction(out[i]) > a.ach.fraction(out[j]) })
		return out
	case achTimeline, achRecords:
		return nil
	}
	return achievements
}

// selected is the achievement under the cursor, if any.
func (v *achievementsView) selected(a *App) *achievement {
	switch v.view {
	case achRecords:
		return nil
	case achTimeline:
		r := v.timeline.Selected()
		if r == nil {
			return nil
		}
		id, _, _ := strings.Cut(r.Key, "/")
		return achievementByID(id)
	}
	shown := v.shown(a)
	for _, x := range shown {
		if x.id == v.sel {
			return x
		}
	}
	if len(shown) > 0 {
		v.sel = shown[0].id
		return shown[0]
	}
	return nil
}

func (v *achievementsView) sync(a *App) {
	type event struct {
		x  *achievement
		t  tier
		at time.Time
	}
	var events []event
	for _, x := range achievements {
		for i, at := range a.ach.Earned[x.id] {
			t := tier(i + 1)
			if x.oneOff() {
				t = x.medal
			}
			events = append(events, event{x, t, at})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.After(events[j].at) })
	now := time.Now()
	rows := make([]Row, 0, len(events))
	for _, e := range events {
		rows = append(rows, Row{Key: e.x.id + "/" + e.t.String(), Kind: KNone, Cells: []Cell{
			steelCell(age(e.at, now)+" ago", false),
			{Text: e.x.glyphText() + " " + e.x.name, Styled: fg(e.x.area.color()).Render(e.x.glyphText()+" ") + sWhite.Render(e.x.name)},
			{Text: e.t.String(), Styled: fg(tierColor(e.t)).Render(e.t.String())},
			{Text: areaNames[e.x.area], Styled: sText.Render(areaNames[e.x.area])},
			{Text: fmt.Sprintf("+%d", tierXP[e.t]), Styled: sCyan.Render(fmt.Sprintf("+%d", tierXP[e.t]))},
		}})
	}
	v.timeline.Empty = "Nothing unlocked yet. Watch a run with w to start."
	v.timeline.SetRows(rows)
}

func (v *achievementsView) Keys(a *App) []Binding {
	var bs []Binding
	switch v.view {
	case achRecords:
		bs = append(bs, v.recordKeys(a)...)
	case achTimeline:
		bs = append(bs, v.timeline.moveKeys())
	default:
		bs = append(bs, Binding{Keys: []string{"left", "right", "up", "k", "down", "j", "home", "end", "pgup", "pgdown"},
			Label: "←↑↓→", Desc: "move", Help: "move through the medals · g G first / last", Group: groupMove,
			Do: func(k string) tea.Cmd { v.move(a, k); return nil }})
	}
	bs = append(bs, viewKeys("next view", "the next view: trophy case, almost there, timeline, records", "ACHIEVEMENTS", func(d int) tea.Cmd {
		v.view = (v.view + d + len(achViews)) % len(achViews)
		v.offset = 0
		if v.view == achRecords {
			return v.loadRecords(a, false)
		}
		return nil
	}))
	if x := v.selected(a); x != nil && areaPlaces[x.area] >= 0 {
		place := areaPlaces[x.area]
		bs = append(bs, Binding{Keys: []string{"enter"}, Desc: "go to " + tabNames[place], Help: "go to the place where it is earned",
			Do: do(func() tea.Cmd { return a.setTab(place) })})
	}
	return grouped("ACHIEVEMENTS", bs...)
}

func (v *achievementsView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

// move moves the cursor through the medals as they were last drawn: left
// and right along the case, up and down to the nearest medal in the row
// above or below.
func (v *achievementsView) move(a *App, key string) {
	shown := v.shown(a)
	if len(shown) == 0 {
		return
	}
	i := slices.IndexFunc(shown, func(x *achievement) bool { return x.id == v.sel })
	at := slices.IndexFunc(v.slots, func(s medalSlot) bool { return s.id == v.sel })
	switch key {
	case "left", "h":
		i = max(i-1, 0)
	case "right", "l":
		i = min(i+1, len(shown)-1)
	case "home":
		i = 0
	case "end":
		i = len(shown) - 1
	case "up", "k", "down", "j", "pgup", "pgdown":
		if at < 0 {
			return
		}
		rows := map[string]int{"up": -1, "k": -1, "down": 1, "j": 1, "pgup": -3, "pgdown": 3}[key]
		if id := v.nearest(v.slots[at], rows); id != "" {
			v.sel = id
		}
		return
	}
	v.sel = shown[max(i, 0)].id
}

// nearest is the medal rows rows away from s (or as far as there are) that
// is closest to its column.
func (v *achievementsView) nearest(s medalSlot, rows int) string {
	last := 0
	for _, o := range v.slots {
		last = max(last, o.row)
	}
	want := min(max(s.row+rows, 0), last)
	best, gap := "", -1
	for _, o := range v.slots {
		if o.row != want {
			continue
		}
		if d := max(o.col-s.col, s.col-o.col); gap < 0 || d < gap {
			best, gap = o.id, d
		}
	}
	return best
}

func (v *achievementsView) Render(a *App, w, h int) string {
	if v.view == achRecords {
		return v.renderRecords(a, w, h)
	}
	head := []string{screenTitle("trophy", "Achievements", "what you did with Calamares", -1, v.chips(a), w)}
	head = append(head, v.hero(a, w, h)...)
	head = append(head, viewStrip(v.strip(a), v.view, w))
	if w < panelMinWidth {
		if x := v.selected(a); x != nil {
			head = append(head, " "+v.oneLine(a, x, w-1))
		}
	}
	list := func(lw, lh int) string { return v.renderCase(a, lw, lh) }
	if v.view == achTimeline {
		list = func(lw, lh int) string { return v.timeline.Render(lw, lh) }
	}
	return listWithPanel(a, w, h, head, list, func(pw, ph int) []string {
		if x := v.selected(a); x != nil {
			return v.panel(a, x, pw, ph)
		}
		return emptyPanel(pw, ph, "Nothing selected.")
	})
}

func (v *achievementsView) chips(a *App) []string {
	got := a.ach.unlocked()
	chips := []string{roundPill(fmt.Sprintf("%s%d of %d", icon("trophy"), got, len(achievements)), cGreen, tint[KOK])}
	close := 0
	for _, x := range achievements {
		if a.ach.almost(x) && a.ach.fraction(x) >= 0.6 {
			close++
		}
	}
	if close > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%d almost there", close), cAmber, tint[KRun]))
	}
	if s := a.ach.Streak; s > 1 {
		chips = append(chips, roundPill(fmt.Sprintf("%d day streak", s), cWait, lipgloss.Color(mix(string(cBg), string(cWait), 0.2))))
	}
	return chips
}

func (v *achievementsView) strip(a *App) []stripItem {
	almost := 0
	for _, x := range achievements {
		if a.ach.almost(x) {
			almost++
		}
	}
	events := 0
	for _, x := range achievements {
		events += len(a.ach.Earned[x.id])
	}
	return []stripItem{
		{Label: achViews[achCase], Icon: "trophy", Count: fmt.Sprint(len(achievements))},
		{Label: achViews[achAlmost], Icon: "gauge", Count: fmt.Sprint(almost), Quiet: almost == 0},
		{Label: achViews[achTimeline], Icon: "history", Count: fmt.Sprint(events), Quiet: events == 0},
		{Label: achViews[achRecords], Icon: "star"},
	}
}

// hero is the band under the title: the trophy beside your level, the XP
// to the next one and your medals by tier; a single line on a short screen.
func (v *achievementsView) hero(a *App, w, h int) []string {
	xp := a.ach.xp()
	l := levelOf(xp)
	level := sWhite.Bold(true).Render(fmt.Sprintf("Level %d", l)) + sGold.Render(" · "+levelTitle(l))
	var tally []string
	for t := tierPlatinum; t >= tierBronze; t-- {
		n := 0
		for _, x := range achievements {
			if a.ach.tierOf(x) == t {
				n++
			}
		}
		tally = append(tally, fg(tierColor(t)).Render("●")+" "+sText.Render(fmt.Sprint(n))+" "+sDim.Render(t.String()))
	}
	if h < heroMinHeight {
		return []string{withBg(fit(" "+level+"  "+xpBar(xp, 20)+"   "+strings.Join(tally, "  "), w), cPanel)}
	}
	text := []string{
		level,
		xpBar(xp, 30) + sDim.Render(fmt.Sprintf(" to level %d", min(l+1, maxLevel))),
		strings.Join(tally, "   "),
	}
	if title, at := nextTitle(l); title != "" {
		text = append(text, sDim.Render(fmt.Sprintf("Next title at level %d: ", at))+sText.Render(title))
	}
	if !a.ach.First.IsZero() {
		since := sDim.Render("With Calamares since ") + fg(cSteel).Render(a.ach.First.Local().Format("2 Jan 2006"))
		if a.ach.Streak > 1 {
			since += sDim.Render(" · opened ") + sText.Render(fmt.Sprintf("%d working days", a.ach.Streak)) + sDim.Render(" in a row")
		}
		text = append(text, since)
	}
	art := trophy(cPanel)
	out := make([]string, len(art))
	for i, l := range art {
		t := ""
		if i < len(text) {
			t = text[i]
		}
		out[i] = withBg(fit(" "+l+"   "+t, w), cPanel)
	}
	return out
}

// oneLine is the achievement under the cursor on a line, for a screen too
// narrow for the panel.
func (v *achievementsView) oneLine(a *App, x *achievement, w int) string {
	name, text := x.name, x.text
	if x.secret() && a.ach.tierOf(x) == tierNone {
		name, text = "??????????", "Hint: "+x.hint
	}
	return fit(sWhite.Bold(true).Render(name)+sDim.Render(" · ")+sText.Render(text), w)
}

// renderCase draws the medals of the view on display, grouped by area in
// the trophy case, and keeps the one under the cursor in sight.
func (v *achievementsView) renderCase(a *App, w, h int) string {
	shown := v.shown(a)
	v.slots = v.slots[:0]
	if len(shown) == 0 {
		return " " + sDim.Render("Nothing in progress yet. Every medal you start on shows here.")
	}
	cols := max((w-1)/medalCell, 1)
	var lines []string
	row := 0
	place := func(group []*achievement, headed bool) {
		for start := 0; start < len(group); start += cols {
			end := min(start+cols, len(group))
			block := make([]string, 6)
			for c, x := range group[start:end] {
				v.slots = append(v.slots, medalSlot{id: x.id, row: row, col: c, line: len(lines), underHead: headed && start == 0})
				for i, l := range v.medalCellLines(a, x) {
					block[i] += l
				}
			}
			for _, l := range block {
				lines = append(lines, " "+l)
			}
			row++
		}
	}
	if v.view == achAlmost {
		place(shown, false)
	} else {
		for ar := areaActions; ar <= areaSecret; ar++ {
			var group []*achievement
			for _, x := range shown {
				if x.area == ar {
					group = append(group, x)
				}
			}
			lines = append(lines, v.areaHead(a, ar, group, w))
			place(group, true)
			lines = append(lines, "")
		}
	}
	if at := slices.IndexFunc(v.slots, func(s medalSlot) bool { return s.id == v.sel }); at >= 0 {
		s := v.slots[at]
		top := s.line
		if s.underHead {
			top--
		}
		switch {
		case top < v.offset:
			v.offset = top
		case s.line+6 > v.offset+h:
			v.offset = s.line + 6 - h
		}
	}
	v.offset = min(max(v.offset, 0), max(len(lines)-h, 0))
	end := min(v.offset+h, len(lines))
	return strings.Join(lines[v.offset:end], "\n")
}

// areaHead is the line over an area's medals: its icon and name, how many
// you have of them, and a rule to the edge.
func (v *achievementsView) areaHead(a *App, ar area, group []*achievement, w int) string {
	got := 0
	for _, x := range group {
		if a.ach.tierOf(x) > tierNone {
			got++
		}
	}
	count := fmt.Sprintf("%d/%d", got, len(group))
	if ar == areaSecret {
		count = fmt.Sprintf("%d found", got)
	}
	head := " " + icoIn(areaIcons[ar], ar.color()) + sWhite.Bold(true).Render(areaNames[ar]) + "  " + sDim.Render(count) + " "
	return head + fg(cFrame).Render(strings.Repeat("─", max(w-width(head)-1, 0)))
}

// medalCellLines are the six lines of a medal in the case: the medal on a
// margin of a cell each side, and under it how far it has come. The one
// under the cursor sits on the selection.
func (v *achievementsView) medalCellLines(a *App, x *achievement) []string {
	under := cBg
	if x.id == v.sel {
		under = cSelBg
	}
	m, glyph, gc := a.medalLook(x)
	t := a.ach.tierOf(x)
	pad := withBg(" ", under)
	var out []string
	for _, l := range medal(10, m, t >= tierGold, glyph, gc, under) {
		out = append(out, pad+l+pad)
	}
	return append(out, withBg(" "+v.caption(a, x)+" ", under))
}

// caption is the line under a medal, ten cells: the tier you have at the
// top of it, a bar to the next tier, or dots for a medal not started.
func (v *achievementsView) caption(a *App, x *achievement) string {
	t := a.ach.tierOf(x)
	n := a.ach.Counts[x.id]
	need, _ := x.next(n)
	switch {
	case x.secret() && t == tierNone:
		return centre(sDim.Render("? ? ?"), 10)
	case need == 0:
		return centre(fg(tierColor(t)).Render(t.String()), 10)
	case n == 0:
		return centre(fg(cFrame).Render("· · · ·"), 10)
	}
	lit := min(8*n/need, 7)
	c := cSteel
	if t > tierNone {
		c = tierColor(t)
	}
	return " " + fg(c).Render(strings.Repeat("━", max(lit, 1))) + fg(cFrame).Render(strings.Repeat("━", 8-max(lit, 1))) + " "
}

// centre puts s in the middle of w cells.
func centre(s string, w int) string {
	left := max((w-width(s))/2, 0)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", max(w-left-width(s), 0))
}

// panel is about one achievement: its medal big, its name, area and tier,
// what it is for, how far you are and its tiers, and where to earn it.
func (v *achievementsView) panel(a *App, x *achievement, w, h int) []string {
	p := newPanel(w)
	t := a.ach.tierOf(x)
	hidden := x.secret() && t == tierNone
	m, glyph, gc := a.medalLook(x)
	art := medal(16, m, t >= tierGold, glyph, gc, cPanel)

	name := x.name
	if hidden {
		name = "??????????"
	}
	side := []string{"", sWhite.Bold(true).Render(name), icoIn(areaIcons[x.area], x.area.color()) + fg(x.area.color()).Render(areaNames[x.area])}
	switch {
	case t > tierNone:
		side = append(side, tierPill(t)+" "+roundPill(fmt.Sprintf("+%d XP", x.xpUpTo(t)), cCyan, lipgloss.Color(mix(string(cBg), string(cCyan), 0.18))))
		if at := a.ach.Earned[x.id]; len(at) > 0 {
			side = append(side, sDim.Render("earned ")+fg(cSteel).Render(at[len(at)-1].Local().Format("2 Jan 2006")))
		}
	case hidden:
		side = append(side, roundPill("secret", lipgloss.Color("#d9ccff"), secretMedal.face))
	default:
		side = append(side, roundPill("locked", cHint, cCrumbBg))
	}
	if p.inner() >= width(art[0])+20 {
		for i, l := range art {
			s := ""
			if i < len(side) {
				s = side[i]
			}
			p.put(l + "  " + s)
		}
	} else {
		p.put(art...)
		p.put(side[1:]...)
	}

	p.add("")
	if hidden {
		p.add(sDim.Render("Hint: ") + sText.Render(x.hint))
	} else {
		p.add(sText.Render(x.text))
		if x.quip != "" {
			p.add(sDim.Italic(true).Render("“" + x.quip + "”"))
		}
	}
	if !hidden {
		v.progressCards(a, p, x)
	}
	tip := []string{sText.Render(areaTips[x.area])}
	if place := areaPlaces[x.area]; place >= 0 {
		tip = append(tip, keycap("enter")+" "+sText.Render("go to "+tabNames[place]))
	}
	p.callout(cCyan, tip...)
	return p.render(h)
}

// progressCards are how far you are with x and, for what has tiers, the
// tiers with when you reached each.
func (v *achievementsView) progressCards(a *App, p *panelText, x *achievement) {
	n := a.ach.Counts[x.id]
	need, next := x.next(n)
	if !x.oneOff() || x.tiers[0] > 1 {
		p.label("Progress")
		if need == 0 {
			p.add(sGreen.Render("Every tier earned.") + sDim.Render(fmt.Sprintf(" %d in all", n)))
		} else {
			bw := max(p.inner()-2, 8)
			lit := min(bw*n/need, bw)
			p.add(fg(tierColor(next)).Render(strings.Repeat("━", lit)) + fg(cFrame).Render(strings.Repeat("━", bw-lit)))
			p.add(sGold.Render(fmt.Sprint(n)) + sDim.Render(fmt.Sprintf(" of %d for %s", need, next)))
		}
		if seen := a.ach.Seen[x.id]; x.how == countDistinct && len(seen) > 0 && len(seen) <= 12 {
			p.add(sDim.Render("so far ") + sText.Render(strings.Join(seen, " · ")))
		}
	}
	if x.oneOff() {
		return
	}
	p.label("Tiers")
	earned := a.ach.Earned[x.id]
	for i, at := range x.tiers {
		t := tier(i + 1)
		mark, words := sDim.Render("○"), sDim
		detail := fmt.Sprintf("%d · +%d XP", at, tierXP[t])
		switch {
		case i < len(earned):
			mark, words = sGreen.Render("✓"), fg(tierColor(t))
			detail = fmt.Sprintf("%d · %s", at, earned[i].Local().Format("2 Jan"))
		case at == need:
			mark, words = sAmber.Render("◐"), fg(tierColor(t))
			detail = fmt.Sprintf("%d · %d to go", at, at-n)
		}
		p.row(mark+" "+words.Render(t.String()), sDim.Render(detail))
	}
}

func (v *achievementsView) Explain(a *App) explained {
	if v.view == achRecords {
		return recordsExplained()
	}
	return explained{title: "What Achievements shows", items: []explanation{
		{"Level", "XP adds up to a level; every few levels bring a new title. Each tier you reach is worth XP: bronze 10, silver 25, gold 50, platinum 100."},
		{"Medals", "What is counted has up to four tiers, bronze, silver, gold and platinum; the rest is earned once, at one tier. A grey medal is not earned yet; a violet one with a question mark is a secret until you find it."},
		{"Under a medal", "The tier you have once it is at its top, a bar to its next tier, or dots for one you have not started."},
		{"Almost there", "The medals you have started on that have a tier left, the closest first."},
		{"Timeline", "Every tier you reached, the newest first."},
		{"Counting", "Counting started the day achievements came to Calamares; nothing is worked out from the past. What is counted is kept in achievements.json next to the config. achievements: off in the config hides them."},
	}}
}
