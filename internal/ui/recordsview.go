package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The Records view of the Achievements place: a line per record with its
// holder, grouped by what it ranks, and a panel with the top of the board
// of the record under the cursor. t switches between the periods.

// recordsState is what the Records view keeps between frames.
type recordsState struct {
	period period
	list   scrollList
	recs   []record
	key    string // what recs were worked out from
	width  int    // of the list the items were drawn for
}

// Widths of the columns of the list: the name takes its icon too.
const (
	recordNameWidth  = 30
	recordValueWidth = 14
)

// records are the records of the period on display, worked out again only
// when what they come from changed.
func (v *achievementsView) records(a *App) []record {
	st := &v.rec
	key := fmt.Sprint(st.period, a.st.stats.state.at, a.st.stats.done, a.st.times.state.at, len(a.st.times.runs), a.me, time.Now().Truncate(time.Hour))
	if key != st.key {
		st.key = key
		st.recs = records(recordsIn{stats: a.st.stats.snap, runs: a.st.times.runs, me: a.me, now: time.Now(), short: a.short}, st.period)
		st.list.setItems(v.recordItems(st.recs, st.width))
	}
	return st.recs
}

// loadRecords reads what Records shows when it is old, or when forced; the
// runs again too when they were read before they told their conclusion.
func (v *achievementsView) loadRecords(a *App, force bool) tea.Cmd {
	stale := false
	for _, rt := range a.st.times.runs {
		if rt.Conclusion == "" {
			stale = true
		}
		break
	}
	var repos []string
	for _, r := range a.ciRepos() {
		repos = append(repos, r.FullName)
	}
	return tea.Batch(a.st.loadStats(a, force), a.st.loadTimes(a, repos, force || stale))
}

func (v *achievementsView) selectedRecord() *record {
	it, ok := v.rec.list.selectedItem()
	if !ok {
		return nil
	}
	r, _ := it.data.(*record)
	return r
}

func (v *achievementsView) recordItems(recs []record, w int) []listItem {
	var items []listItem
	for _, g := range recordGroups {
		head := false
		for i := range recs {
			r := &recs[i]
			if r.group != g {
				continue
			}
			if !head {
				if len(items) > 0 {
					items = append(items, listItem{id: "gap " + g, lines: []string{""}})
				}
				items = append(items, listItem{id: "group " + g, lines: []string{groupHead(g, w-2)}})
				head = true
			}
			items = append(items, listItem{id: r.id, lines: []string{recordLine(r, w-2)}, selected: []string{withSelection(recordLine(r, w-2), false)},
				selectable: true, data: r})
		}
	}
	return items
}

// groupIcons are the icons of the groups of records.
var groupIcons = map[string]string{groupPeople: "user", groupRepos: "repo", groupWorkflows: "workflow", groupMoments: "clock", groupYou: "star"}

// groupHead is the line over a group of records: its icon and name, and a
// hairline to the edge.
func groupHead(g string, w int) string {
	head := icoIn(groupIcons[g], iconColor(groupIcons[g])) + sWhite.Bold(true).Render(g) + " "
	return head + hairline(max(w-width(head), 0), "─", cFrame)
}

// recordColors are the colours of the icons of the records, by what they count.
var recordColors = map[string]lipgloss.Color{
	"people-commits": cRef, "people-merged": cViolet, "people-repos": cGold, "people-runs": cCyan,
	"repo-commits": cRef, "repo-merged": cViolet, "repo-runs": cCyan, "repo-failed": cRed, "repo-time": cSteel,
	"workflow-failed": cRed, "workflow-reruns": cAmber, "run-longest": cSteel, "run-fastest": cGreen, "run-attempts": cAmber,
	"moment-hour": cSteel, "moment-night": cViolet, "moment-friday": cRed,
	"you-contributions": cTeal, "you-streak": cGold,
}

// recordLine is a record in the list, w cells wide: its icon and name, its
// figure and who or what holds it. A record with nothing to show is dim
// from end to end.
func recordLine(r *record, w int) string {
	value, holder := r.shown()
	name := padTo(clip(r.name, recordNameWidth-4), recordNameWidth-3)
	var line string
	switch {
	case r.missing != "":
		line = iconSlot(r.icon, cDim) + sDim.Render(name+padTo("—", recordValueWidth)+"  not counted for this period")
	case len(r.board) == 0:
		line = iconSlot(r.icon, cDim) + sDim.Render(name+padTo("—", recordValueWidth)+"  nothing yet")
	default:
		line = iconSlot(r.icon, recordColors[r.id]) + sText.Render(name) + sWhite.Bold(true).Render(padTo(value, recordValueWidth)) + holderText(r, holder)
	}
	return padTo(fit(line, w), w)
}

// iconSlot is the icon for name in colour c, in two cells even when the
// icon set has none for it, so the columns line up.
func iconSlot(name string, c lipgloss.Color) string { return padTo(icoIn(name, c), 2) + " " }

// holderText is who or what holds a record, with the icon of what it is
// and, dim, a few words more.
func holderText(r *record, holder string) string {
	if r.lead != "" || len(r.board) == 0 {
		return "  " + sText.Render(holder)
	}
	s := r.board[0]
	text := standingText(s, holder)
	if s.detail != "" {
		text += sDim.Render(" · " + s.detail)
	}
	return text
}

// standingText is who or what s is, after the icon of what it is in two
// cells: a person, a repo, or a repo's workflow or run with its number in gold.
func standingText(s standing, text string) string {
	switch {
	case s.login != "":
		return ico("user") + sText.Render(text)
	case s.workflow != "" && text == s.name:
		name := ico("repo") + sText.Render(s.short) + sDim.Render(" › ") + sText.Render(s.workflow)
		if s.run != nil {
			name += " " + sGold.Render(fmt.Sprintf("#%d", s.run.RunNumber))
		}
		return name
	case s.repo != "":
		return ico("repo") + sText.Render(text)
	}
	return "  " + sText.Render(text)
}

func (v *achievementsView) recordKeys(a *App) []Binding {
	next := (v.rec.period + 1) % period(len(periodNames))
	bs := []Binding{
		moveBinding("move", func(k string) tea.Cmd { v.rec.list.handleNav(k); return nil }),
		{Keys: []string{"t"}, Desc: strings.ToLower(periodNames[next]), Help: "the period of the records: the last 24 hours, week, month or all time",
			Do: do(func() tea.Cmd {
				v.rec.period = next
				return nil
			})},
	}
	r := v.selectedRecord()
	if r == nil || r.missing != "" || len(r.board) == 0 {
		return bs
	}
	lead := r.board[0]
	if desc, open := v.openStanding(a, lead); open != nil {
		bs = append(bs, Binding{Keys: []string{"enter"}, Desc: desc, Help: "open what holds the record: a person's runs, a repo, a workflow or a run", Do: do(open)})
	}
	if url := standingURL(a, lead); url != "" {
		bs = append(bs, Binding{Keys: []string{"o"}, Help: "open what holds the record in the browser", Do: do(func() tea.Cmd { return a.openURL(url) })})
	}
	value, holder := r.shown()
	line := fmt.Sprintf("%s %s: %s, %s", r.name, v.rec.period.words(), holder, value)
	bs = append(bs, Binding{Keys: []string{"y"}, Help: "copy the record as a line of text", Do: do(func() tea.Cmd { return a.copyText(line, "the record") })})
	return bs
}

// openStanding is what enter does on s, and the words for it.
func (v *achievementsView) openStanding(a *App, s standing) (string, func() tea.Cmd) {
	switch {
	case s.login != "":
		return "their runs", func() tea.Cmd { return a.openActorRuns(s.login) }
	case s.run != nil:
		run := *s.run
		return "open run", func() tea.Cmd { return a.push(newRunView(a, s.repo, run)) }
	case s.wfID != 0:
		return "open workflow", func() tea.Cmd { return a.openRepo(s.repo, "", s.wfID) }
	case s.repo != "":
		return "open repo", func() tea.Cmd { return a.openRepo(s.repo, "", 0) }
	}
	return "", nil
}

// openActorRuns shows the runs of the last 7 days that login started.
func (a *App) openActorRuns(login string) tea.Cmd {
	cmd := a.setTab(tabActions)
	if v, ok := a.roots[tabActions].(*activityView); ok {
		v.setFilter(viewRecent)
		v.SetFilter(a, "actor:"+login)
	}
	return cmd
}

func standingURL(a *App, s standing) string {
	switch {
	case s.login != "":
		return a.client.WebURL(s.login)
	case s.run != nil:
		return a.client.WebURL(fmt.Sprintf("%s/actions/runs/%d", s.repo, s.run.ID))
	case s.repo != "":
		return a.client.WebURL(s.repo)
	}
	return ""
}

// recordsTitle is the title line of Records: the chips count the commits,
// runs and failed runs of the period, and your longest streak.
func (v *achievementsView) recordsTitle(a *App, recs []record, w int) string {
	sum := func(id string) (int64, bool) {
		for _, r := range recs {
			if r.id != id || r.missing != "" {
				continue
			}
			var n int64
			for _, s := range r.board {
				n += s.n
			}
			return n, true
		}
		return 0, false
	}
	var chips []string
	if n, ok := sum("repo-commits"); ok && n > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s %s", count(n), plural("commit", int(n))), cGreen, tint[KOK]))
	}
	if n, ok := sum("repo-runs"); ok && n > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s %s", count(n), plural("run", int(n))), cCyan, lipgloss.Color(mix(string(cBg), string(cCyan), 0.18))))
	}
	if n, ok := sum("repo-failed"); ok && n > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s failed", count(n)), cRed, tint[KFail]))
	}
	for _, r := range recs {
		if r.id == "you-streak" && r.missing == "" && len(r.board) > 0 && r.board[0].n > 1 {
			chips = append(chips, roundPill(fmt.Sprintf("your streak %s", r.board[0].text), cGold, lipgloss.Color(mix(string(cBg), string(cGold), 0.2))))
		}
	}
	return screenTitle("trophy", "Records", "what your repos and the people on them did", -1, chips, w)
}

// recordsCoverage is the line under the strips: the period, what was read
// and when, or that it is being read.
func (v *achievementsView) recordsCoverage(a *App, w int) string {
	st := a.st.stats
	parts := []string{sText.Render(periodNames[v.rec.period])}
	if n := len(st.snap.Repos); n > 0 {
		parts = append(parts, sDim.Render(fmt.Sprintf("%d %s pushed to in the last 30 days", n, plural("repo", n))))
	}
	switch {
	case st.state.busy:
		parts = append(parts, sAmber.Render(fmt.Sprintf("reading… %d of %d", st.done, st.steps)))
	case a.st.times.state.busy:
		parts = append(parts, sAmber.Render("reading the runs of the last two weeks…"))
	case st.state.err != nil:
		parts = append(parts, sRed.Render("could not read: "+st.state.err.Error()))
	case !st.state.at.IsZero():
		parts = append(parts, sDim.Render("read "+age(st.state.at, time.Now())+" ago"))
	}
	if st.failed > 0 {
		parts = append(parts, sRed.Render(fmt.Sprintf("%d %s failed", st.failed, plural("read", st.failed)))+sDim.Render(", first "+st.err.Error()))
	}
	if n := len(st.snap.Counting); n > 0 && !st.state.busy {
		parts = append(parts, sDim.Render(fmt.Sprintf("GitHub was still counting %d %s", n, plural("repo", n))))
	}
	return fit(" "+strings.Join(parts, sDim.Render(" · ")), w)
}

func (v *achievementsView) renderRecords(a *App, w, h int) string {
	recs := v.records(a)
	head := []string{v.recordsTitle(a, recs, w), viewStrip(v.strip(a), v.view, w), v.periodStrip(w), v.recordsCoverage(a, w)}
	if w < panelMinWidth {
		if r := v.selectedRecord(); r != nil {
			value, holder := r.shown()
			head = append(head, fit(" "+sWhite.Bold(true).Render(r.name)+sDim.Render(" · ")+sText.Render(holder+" "+value), w))
		}
	}
	list := func(lw, lh int) string {
		cols := "  " + sGrey.Render(padTo("RECORD", recordNameWidth)+padTo("VALUE", recordValueWidth)+"HOLDER")
		if a.st.stats.snap.empty() && len(a.st.times.runs) == 0 {
			msg := "Not read yet; ctrl+r reads them."
			if a.st.stats.state.busy || a.st.times.state.busy {
				msg = "Reading the commits, pull requests and runs of your repos…"
			}
			return cols + "\n " + sDim.Render(msg)
		}
		if lw != v.rec.width {
			v.rec.width = lw
			v.rec.list.setItems(v.recordItems(v.rec.recs, lw))
		}
		return cols + "\n" + strings.Join(v.rec.list.render(lw, lh-1), "\n")
	}
	return listWithSteadyPanel(w, h, head, list, func(pw, ph int) []string {
		if r := v.selectedRecord(); r != nil {
			return v.recordPanel(a, r, pw, ph)
		}
		return emptyPanel(pw, ph, "Nothing selected.")
	})
}

// periodStrip is the periods as a strip, the one on display in brackets.
func (v *achievementsView) periodStrip(w int) string {
	items := make([]stripItem, len(periodNames))
	for i, n := range periodNames {
		items[i] = stripItem{Label: n, Icon: "calendar"}
	}
	return viewStripKey(items, int(v.rec.period), w, "t")
}

// recordPanel is about one record: what it counts, the top of its board
// with a bar against the first, your place on a board of people, and the
// holder.
func (v *achievementsView) recordPanel(a *App, r *record, w, h int) []string {
	p := newLayerPanel(w)
	p.add(icoIn(r.icon, recordColors[r.id]) + sWhite.Bold(true).Render(r.name))
	p.wrap(r.about, sDim, 4)
	if r.missing != "" {
		p.add("")
		p.wrap(r.missing, sDim, 6)
		return p.render(h)
	}
	if len(r.board) == 0 {
		p.add("")
		p.add(sDim.Render("Nothing " + v.rec.period.words() + "."))
		return p.render(h)
	}
	if in := (recordsIn{stats: a.st.stats.snap, now: time.Now()}); r.group == groupYou {
		if bars := contributionBars(in, v.rec.period); len(bars) > 0 {
			title := "Contributions per day"
			if v.rec.period == periodAll {
				title = "Contributions per month"
			}
			p.label(title)
			p.add(sDim.Render(chartSummary(in, v.rec.period, bars)))
			p.put(contributionChart(bars, p.inner())...)
		}
	}
	p.label("Leaderboard")
	v.board(p, r)
	if r.people && a.me != "" {
		switch {
		case r.place > boardSize:
			p.field("You", fmt.Sprintf("#%d · %s", r.place, r.you.text))
		case r.place == 0:
			p.field("You", sDim.Render("not on the board"))
		}
	}
	if r.total > 0 && r.group == groupPeople {
		p.field("Everyone", fmt.Sprintf("%s %s by %s", count(int64(r.total)), plural("commit", r.total), people(r.entrants)))
	}
	lead := r.board[0]
	if lead.detail != "" {
		p.label("Holder")
		p.field("Who", standingText(lead, lead.name))
		p.field("Detail", sText.Render(lead.detail))
	}
	if desc, open := v.openStanding(a, lead); open != nil {
		p.callout(cCyan, keycap("enter")+" "+sText.Render(desc)+"  "+keycap("o")+" "+sText.Render("browser"))
	}
	return p.render(h)
}

func people(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

// board draws the places of r: a place number (gold for the first three),
// who or what, a bar against the first and the figure.
func (v *achievementsView) board(p *panelText, r *record) {
	figure := 0
	for _, s := range r.board {
		figure = max(figure, width(s.text))
	}
	barW := min(max(p.inner()/5, 6), 14)
	nameW := max(p.inner()-3-barW-figure-2, 8)
	first := r.board[0].n
	for i, s := range r.board {
		place := sDim.Render(fmt.Sprintf("%2d ", i+1))
		if i < 3 {
			place = sGold.Render(fmt.Sprintf("%2d ", i+1))
		}
		name := clip(s.name, nameW-2)
		st := sText
		if r.people && i+1 == r.place {
			st = sCyan
		}
		label := standingText(s, "") + st.Render(name)
		lit := barW
		if first > 0 && s.n != first {
			lit = max(int(int64(barW)*s.n/first), 1)
		}
		if r.id == "run-fastest" && s.n > 0 {
			lit = max(int(int64(barW)*first/s.n), 1)
		}
		bar := fg(cSteel).Render(strings.Repeat("━", lit)) + strings.Repeat(" ", barW-lit)
		p.put(place + padTo(label, nameW) + " " + bar + " " + strings.Repeat(" ", figure-width(s.text)) + sWhite.Render(s.text))
	}
}

// contributionBar is one bar of the contributions chart: what it counts,
// and the label under it ("" for none).
type contributionBar struct {
	count int
	label string
	day   string // the date of a bar of one day
}

// chartHeight is how many lines the contributions chart takes.
const chartHeight = 4

// contributionBars are the bars of period p: a bar per day for a week or a
// month, a bar per month of the last 24 for all time, none for 24 hours.
func contributionBars(in recordsIn, p period) []contributionBar {
	days := in.daysIn(p)
	var out []contributionBar
	switch p {
	case periodDay:
		return nil
	case periodWeek, periodMonth:
		for i, d := range days {
			b := contributionBar{count: d.Count, day: d.Date}
			if p == periodWeek {
				b.label = dayLabel(d.Date, "Mon")[:2]
			} else if (len(days)-1-i)%7 == 0 {
				b.label = dayLabel(d.Date, "2 Jan")
			}
			out = append(out, b)
		}
		return out
	}
	months := map[string]int{}
	for _, d := range days {
		months[d.Date[:7]] += d.Count
	}
	first := in.now.AddDate(0, -23, 0)
	first = time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, in.now.Location())
	for m := first; !m.After(in.now); m = m.AddDate(0, 1, 0) {
		b := contributionBar{count: months[m.Format("2006-01")]}
		if m == first || m.Month() == time.January {
			b.label = m.Format("Jan 2006")
		}
		out = append(out, b)
	}
	return out
}

// contributionChart draws bars as columns chartHeight lines tall in w
// cells, with their labels on a line under them. The highest bar is white,
// the others green; an empty one is a stub in the frame colour.
func contributionChart(bars []contributionBar, w int) []string {
	if len(bars) == 0 {
		return nil
	}
	gap := 1
	bw := min(max((w+gap)/len(bars)-gap, 1), 3)
	if (bw+gap)*len(bars)-gap > w {
		gap = 0
	}
	most := 0
	for _, b := range bars {
		most = max(most, b.count)
	}
	steps := []rune(" ▁▂▃▄▅▆▇█")
	rows := make([]string, chartHeight)
	labels := []rune(strings.Repeat(" ", (bw+gap)*len(bars)))
	for i, b := range bars {
		level := 0
		if most > 0 {
			level = (b.count*8*chartHeight + most - 1) / most
		}
		c := cGreen
		if b.count == most && most > 0 {
			c = cWhite
		}
		for r := range rows {
			fill := min(max(level-(chartHeight-1-r)*8, 0), 8)
			cell := fg(c).Render(strings.Repeat(string(steps[fill]), bw))
			if b.count == 0 && r == chartHeight-1 {
				cell = fg(cFrame).Render(strings.Repeat("▁", bw))
			}
			rows[r] += cell + strings.Repeat(" ", gap)
		}
		at := i * (bw + gap)
		if b.label != "" && at+len([]rune(b.label)) <= len(labels) && strings.TrimSpace(string(labels[max(at-1, 0):at+len([]rune(b.label))])) == "" {
			copy(labels[at:], []rune(b.label))
		}
	}
	return append(rows, sDim.Render(strings.TrimRight(string(labels), " ")))
}

// chartSummary is the line over the contributions chart: how many there
// were, and the busiest day or since when.
func chartSummary(in recordsIn, p period, bars []contributionBar) string {
	total := 0
	busiest := contributionBar{}
	for _, b := range bars {
		total += b.count
		if b.count > busiest.count {
			busiest = b
		}
	}
	text := fmt.Sprintf("%s %s", count(int64(total)), plural("contribution", total))
	switch {
	case p == periodAll:
		days := in.daysIn(p)
		all := 0
		for _, d := range days {
			all += d.Count
		}
		text = fmt.Sprintf("%s %s in the last 24 months", count(int64(total)), plural("contribution", total))
		if len(days) > 0 {
			text += fmt.Sprintf(" · %s since %s", count(int64(all)), days[0].Date[:4])
		}
	case busiest.day != "":
		text += fmt.Sprintf(" · busiest %s, %d", dayLabel(busiest.day, "Mon 2 Jan"), busiest.count)
	}
	return text
}

func recordsExplained() explained {
	return explained{title: "What Records shows", items: []explanation{
		{"Records", "Leaderboards of what your repos and the people on them did: the list shows who or what holds each record, the panel the top 10 and your own place."},
		{"Period", "t switches between the last 24 hours, week, month and all time. A record that can't be counted for a period shows a dash and says why."},
		{"People and repos", "Commits on the default branches and merged pull requests of the repos pushed to in the last 30 days; for all time, the commits GitHub counts per person. Apps such as dependabot are left out of the people."},
		{"Runs", "The runs of the last two weeks, read one by one for the Time view; for the month and all time, how many runs and failed runs GitHub counts per repo."},
		{"You", "Your contributions as GitHub counts them: commits, pull requests, reviews and issues, per day."},
		{"Reading", "Everything is read once a day in the background and kept for the next start; ctrl+r reads it again."},
	}}
}
