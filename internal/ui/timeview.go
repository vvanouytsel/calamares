package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// timeView is the Time view of the Actions tab: where the runner time of
// the last week went, by the workflow each job ran in (a shared workflow
// sums up all its callers), with its jobs under it, against the week before.
type timeView struct {
	t      *Table
	repos  func(a *App) []string
	rows   []*timeRow
	total  time.Duration // this week
	prior  time.Duration // the week before
	open   map[string]bool
	byRepo bool
	order  string
}

func newTimeView(repos func(a *App) []string) *timeView {
	return &timeView{repos: repos, open: map[string]bool{}, order: "hours", t: newTable(
		Column{Title: "WORKFLOW", Max: 44, Flex: true, Help: "a workflow; a shared one sums up the jobs it ran for every repo that calls it"},
		Column{Title: "SOURCE", Max: 26, Flex: true, MidCut: true, Drop: 2},
		Column{Title: "HOURS", Min: 16, Help: "the runner time of its jobs in the last 7 days, with a bar against the biggest"},
		Column{Title: "CHANGE", Min: 8, Help: "how that compares with the week before; ▲ 6× runs when it suddenly runs far more often"},
		Column{Title: "RUNS", Right: true, Help: "its runs in the last 7 days"},
		Column{Title: "MEDIAN", Right: true, Min: 7, Drop: 1, Help: "the median runner time of one run"},
	)}
}

func (v *timeView) table() *Table                   { return v.t }
func (v *timeView) count(a *App) int                { return len(v.rows) }
func (v *timeView) busy(a *App) bool                { return a.st.times.state.busy }
func (v *timeView) load(a *App, force bool) tea.Cmd { return a.st.loadTimes(a, v.repos(a), force) }
func (v *timeView) SetFilter(a *App, q string)      { v.t.SetFilter(q, a.me) }
func (v *timeView) FilterValue() string             { return v.t.Filter() }
func (v *timeView) FilterInfo(a *App) filterInfo    { return v.t.Info() }

// stripCount is what the strip says of the view: the hours of the week.
func (v *timeView) stripCount() string {
	if v.total == 0 {
		return "0"
	}
	return hours(v.total)
}

func (v *timeView) sync(a *App) {
	now := time.Now()
	v.rows = timeRows(a.st.times.runs, a.st.uses, v.byRepo, now)
	sortTimeRows(v.rows, v.order)
	v.total, v.prior = 0, 0
	widest := time.Duration(0)
	for _, r := range v.rows {
		v.total += r.week
		v.prior += r.prior
		widest = max(widest, r.week)
	}
	v.t.Cols[0].Title = "WORKFLOW"
	if v.byRepo {
		v.t.Cols[0].Title = "REPO"
	}
	var rows []Row
	for _, r := range v.rows {
		rows = append(rows, v.row(a, r, widest))
		if !v.open[r.key] {
			continue
		}
		if v.byRepo {
			for i, c := range r.children {
				rows = append(rows, v.childRow(a, r, c, i == len(r.children)-1, widest))
			}
			continue
		}
		jobs := r.sortedJobs()
		for i, j := range jobs {
			rows = append(rows, v.jobRow(a, r, j, i == len(jobs)-1, widest))
		}
	}
	v.t.Empty = "No finished runs in the last two weeks."
	switch l := a.st.times.state; {
	case len(a.st.times.runs) == 0 && l.busy:
		v.t.Empty = "Reading two weeks of runs and how long their jobs took…"
	case len(a.st.times.runs) == 0 && l.err != nil:
		v.t.Empty = "Could not read the runs: " + l.err.Error()
	case len(a.st.times.runs) == 0 && l.at.IsZero():
		v.t.Empty = "Not read yet; ctrl+r reads two weeks of runs."
	}
	v.t.SetRows(rows)
}

const timeBarWidth = 10

// hoursCell is a time and a bar of it against the biggest row.
func hoursCell(d, widest time.Duration, quiet bool) Cell {
	n := 0
	if widest > 0 {
		n = max(int(int64(d)*timeBarWidth/int64(widest)), min(int(d/time.Minute), 1))
	}
	t := fmt.Sprintf("%5s ", hours(d))
	st := sWhite
	if quiet {
		st = sText
	}
	return Cell{Text: t + strings.Repeat("▉", n), Styled: st.Render(t) + fg(cSteel).Render(barCells(n))}
}

// weekCell is how the time compares with the week before: a pill when it
// grew or shrank clearly, a run count that went up steeply, or a quiet share.
func weekCell(week, prior time.Duration, runs, priorRuns int, least time.Duration) Cell {
	var styled string
	switch pct, ok := growth(week, prior, least); {
	case prior == 0 && week > 0:
		styled = sDim.Render("new")
	case func() bool { _, ok := moreRuns(runs, priorRuns); return ok }():
		n, _ := moreRuns(runs, priorRuns)
		styled = roundPill(fmt.Sprintf("▲ %d× runs", n), cAmber, tint[KRun])
	case ok && pct > 0:
		styled = roundPill(fmt.Sprintf("▲ %d%%", pct), cRed, tint[KFail])
	case ok:
		styled = roundPill(fmt.Sprintf("▼ %d%%", -pct), cGreen, tint[KOK])
	case week > 0:
		styled = sDim.Render(fmt.Sprintf("%d%%", abs(pct)))
	}
	return Cell{Text: ansi.Strip(styled), Styled: styled}
}

func (v *timeView) fold(key string) string {
	switch {
	case v.open[key]:
		return "▾"
	}
	return "▸"
}

func (v *timeView) row(a *App, r *timeRow, widest time.Duration) Row {
	name := sDim.Render(v.fold(r.key)) + "  "
	source := Cell{}
	if v.byRepo {
		name += icoIn("repo", cGold) + sWhite.Bold(true).Render(a.short(r.repo))
	} else {
		name += sWhite.Bold(true).Render(r.name())
		source = iconText("repo", a.short(r.repo), false)
	}
	return Row{Key: "w\x00" + r.key, Kind: KOK, Cells: []Cell{
		{Text: ansi.Strip(name), Styled: name},
		source,
		hoursCell(r.week, widest, false),
		weekCell(r.week, r.prior, len(r.runs), len(r.before), time.Hour),
		numberText(len(r.runs)),
		steelCell(dur(r.median()), false),
	}}
}

func numberText(n int) Cell {
	t := fmt.Sprint(n)
	return Cell{Text: t, Styled: sText.Render(t)}
}

func guide(last bool) string {
	if last {
		return "└─ "
	}
	return "├─ "
}

func (v *timeView) jobRow(a *App, r *timeRow, j *timeJob, last bool, widest time.Duration) Row {
	name := "   " + sDim.Render(guide(last)) + sText.Render(j.name)
	if n := len(j.legs); n > 1 {
		name += sDim.Render(fmt.Sprintf(" (%d legs)", n))
	}
	return Row{Key: "j\x00" + r.key + "\x00" + j.name, Kind: KOK, Cells: []Cell{
		{Text: ansi.Strip(name), Styled: name},
		{},
		hoursCell(j.week, widest, true),
		weekCell(j.week, j.prior, len(j.runs), len(j.before), 10*time.Minute),
		{},
		steelCell(dur(medianOf(j.runs)), false),
	}}
}

func (v *timeView) childRow(a *App, r, c *timeRow, last bool, widest time.Duration) Row {
	name := "   " + sDim.Render(guide(last)) + sText.Render(c.name())
	source := Cell{}
	if !strings.EqualFold(c.repo, r.repo) {
		source = iconText("repo", a.short(c.repo), false)
	}
	return Row{Key: "c\x00" + r.key + "\x00" + c.key, Kind: KOK, Cells: []Cell{
		{Text: ansi.Strip(name), Styled: name},
		source,
		hoursCell(c.week, widest, true),
		weekCell(c.week, c.prior, len(c.runs), len(c.before), 10*time.Minute),
		numberText(len(c.runs)),
		steelCell(dur(c.median()), false),
	}}
}

// selected is the row under the cursor, and the workflow of a repo or the
// job of a workflow when the cursor is on one.
func (v *timeView) selected() (r, child *timeRow, job *timeJob) {
	sel := v.t.Selected()
	if sel == nil {
		return nil, nil, nil
	}
	parts := strings.Split(sel.Key, "\x00")
	if len(parts) < 2 {
		return nil, nil, nil
	}
	for _, x := range v.rows {
		if x.key != parts[1] {
			continue
		}
		switch {
		case parts[0] == "j" && len(parts) > 2:
			return x, nil, x.jobs[parts[2]]
		case parts[0] == "c" && len(parts) > 2:
			for _, c := range x.children {
				if c.key == parts[2] {
					return x, c, nil
				}
			}
		}
		return x, nil, nil
	}
	return nil, nil, nil
}

// workflowOf is the workflow under the cursor: the row, or the workflow of a
// repo when the cursor is on one.
func (v *timeView) workflowOf() *timeRow {
	r, c, _ := v.selected()
	if c != nil {
		return c
	}
	if v.byRepo {
		return nil
	}
	return r
}

func (v *timeView) keys(a *App) []Binding {
	var bs []Binding
	r, _, _ := v.selected()
	if wf := v.workflowOf(); wf != nil {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "its runs", Help: "the runs of the workflow",
			Do: do(func() tea.Cmd { return v.openRuns(a, wf) })},
			Binding{Keys: []string{"s"}, Desc: "its repo", Help: "open the repo the workflow lives in",
				Do: do(func() tea.Cmd { return a.push(newRepoView(a, wf.repo, "")) })})
	} else if r != nil {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open repo",
			Do: do(func() tea.Cmd { return a.push(newRepoView(a, r.repo, "")) })})
	}
	if r != nil {
		bs = append(bs, Binding{Keys: []string{" "}, Label: "space", Desc: "fold", Help: "show / hide what is under it",
			Do: do(func() tea.Cmd { v.toggle(r.key); return nil })})
	}
	what := "by repo"
	if v.byRepo {
		what = "by workflow"
	}
	return append(bs,
		Binding{Keys: []string{"v"}, Desc: "fold all", Help: "show / hide what is under every row", Do: do(func() tea.Cmd { v.toggleAll(); return nil })},
		Binding{Keys: []string{"g"}, Desc: what, Help: "by the workflow the time went to / by the repo whose runs took it",
			Do: do(func() tea.Cmd { v.byRepo = !v.byRepo; v.t.Top(); return nil })},
		Binding{Keys: []string{"S"}, Desc: "sort", Help: "sort by hours, growth, runs or name", Do: do(func() tea.Cmd { return v.pickOrder() })},
	)
}

// openRuns lists the runs of a workflow: of a shared one, the runs that
// called it; of a repo's own, its runs in the repo.
func (v *timeView) openRuns(a *App, wf *timeRow) tea.Cmd {
	if it, ok := heldShared(a).byKey[wf.key]; ok && !it.action {
		return a.push(newSharedRunsView(a, it, nil))
	}
	rv := newRepoView(a, wf.repo, "")
	if wf.workflowID != 0 {
		rv.wantWF, rv.focus = wf.workflowID, paneRuns
	}
	return a.push(rv)
}

func (v *timeView) toggle(key string) {
	v.open[key] = !v.open[key]
	for i := v.t.Cursor(); i >= 0; i-- {
		v.t.SetCursor(i)
		if r := v.t.Selected(); r != nil && r.Key == "w\x00"+key {
			break
		}
	}
}

func (v *timeView) toggleAll() {
	all := len(v.rows) > 0
	for _, r := range v.rows {
		all = all && v.open[r.key]
	}
	for _, r := range v.rows {
		v.open[r.key] = !all
	}
	if r, _, _ := v.selected(); r != nil {
		v.toggle(r.key)
		v.open[r.key] = !all
	}
}

func (v *timeView) pickOrder() tea.Cmd {
	var items []pickItem
	for _, o := range timeOrders {
		items = append(items, pickItem{value: o.key, label: o.label})
	}
	return openModal(newPicker("time-order", "Sort by", v.order, items, func(key string) tea.Cmd {
		v.order = key
		v.t.Top()
		return nil
	}))
}

func (v *timeView) orderLabel() string {
	for _, o := range timeOrders {
		if o.key == v.order {
			return o.label
		}
	}
	return timeOrders[0].label
}

func (v *timeView) chips(a *App) []string {
	if v.total == 0 {
		return nil
	}
	chips := []string{sWhite.Bold(true).Render(hours(v.total)) + sDim.Render(" this week")}
	if pct, ok := growth(v.total, v.prior, time.Hour); ok {
		if pct > 0 {
			chips = append(chips, roundPill(fmt.Sprintf("▲ %d%% on last week", pct), cRed, tint[KFail]))
		} else {
			chips = append(chips, roundPill(fmt.Sprintf("▼ %d%% on last week", -pct), cGreen, tint[KOK]))
		}
	}
	return chips
}

func (v *timeView) coverage(a *App) string {
	d := &a.st.times
	s := "runner time of the last 7 days"
	if d.repos > 0 {
		s += " in " + repoCount(d.repos)
	}
	if !d.state.at.IsZero() {
		s += " · read " + age(d.state.at, time.Now()) + " ago"
	}
	if n := len(d.partial); n > 0 {
		s += fmt.Sprintf(" · the %d busiest cover less", n)
	}
	if d.state.busy {
		s += " · reading…"
	}
	return s
}

func (v *timeView) listHead(a *App) string {
	return strings.Join([]string{
		chip("g", "by repo", v.byRepo),
		chip("S", "sort: "+v.orderLabel()+" ▾", v.order != timeOrders[0].key),
		a.filterLine(v.t.Filter(), v.FilterInfo(a)),
	}, "   ")
}

// panel tells about the workflow or the repo under the cursor: its time per
// day, where it goes, why it grew, who runs it most and what a minute off its
// biggest job would save.
func (v *timeView) panel(a *App, w, h int) []string {
	r, c, _ := v.selected()
	if c != nil {
		r = c
	}
	if r == nil {
		return emptyPanel(w, h, "Nothing selected.")
	}
	p := newLayerPanel(w)
	repoRow := r.path == ""
	if repoRow {
		p.add(icoIn("repo", cGold) + sGold.Bold(true).Render(a.short(r.repo)))
	} else {
		p.add(sWhite.Bold(true).Render(r.name()) + sDim.Render(" from ") + fact("repo", a.short(r.repo)))
	}
	head := sWhite.Bold(true).Render(hours(r.week)) + sDim.Render(" this week")
	if pill := weekCell(r.week, r.prior, len(r.runs), len(r.before), time.Hour); pill.Text != "" {
		head += "  " + pill.Styled
	}
	p.add(head)

	p.label("Daily usage")
	peak := time.Duration(0)
	for _, d := range r.days {
		peak = max(peak, d)
	}
	p.row(daySpark(r.days[:7], peak)+sDim.Render(" last week"), "")
	p.row(daySpark(r.days[7:], peak)+sDim.Render(" this week"), "")

	if repoRow {
		v.repoSections(a, p, r)
	} else {
		v.workflowSections(a, p, r)
	}
	if r.rerun > 0 {
		p.label("Reruns")
		p.add(sWhite.Render(hours(r.rerun)) + sDim.Render(" of it in runs that ran again"))
	}
	return p.render(h)
}

func (v *timeView) workflowSections(a *App, p *panelText, r *timeRow) {
	jobs := r.sortedJobs()
	if len(jobs) > 0 && r.week > 0 {
		p.label("Time per job")
		var parts []string
		for i, j := range jobs {
			if i == 3 || j.week == 0 {
				break
			}
			parts = append(parts, sWhite.Render(j.name)+" "+fg(cSteel).Render(fmt.Sprintf("%d%%", int(j.week*100/r.week))))
		}
		p.add(strings.Join(parts, "  "))
	}
	if why := whyItGrew(r); why != "" {
		p.label("Cause of increase")
		p.add(why)
	}
	if len(r.users) > 1 {
		p.label("Top repositories")
		p.add(topUsers(a, r.users, 3))
	}
	if len(jobs) > 0 && len(jobs[0].runs) > 0 {
		top := jobs[0]
		saved := time.Duration(len(top.runs)) * time.Minute
		about := fmt.Sprintf("One minute off %s saves about %s a week", top.name, hours(saved))
		if n := len(r.users); n > 1 {
			about += fmt.Sprintf(" across %d repos", n)
		}
		p.add("")
		p.callout(cBlue, sWhite.Render(about+"."),
			keycap("enter")+sText.Render(" its runs  ")+keycap("s")+sText.Render(" its repo"))
	}
}

func (v *timeView) repoSections(a *App, p *panelText, r *timeRow) {
	if len(r.children) == 0 {
		return
	}
	p.label("Workflows")
	for i, c := range r.children {
		if i == 6 {
			p.add(sDim.Render(fmt.Sprintf("… %d more", len(r.children)-i)))
			break
		}
		left := sWhite.Render(c.name())
		if !strings.EqualFold(c.repo, r.repo) {
			left += sDim.Render(" from ") + sText.Render(a.short(c.repo))
		}
		p.row(left, fg(cSteel).Render(hours(c.week)))
	}
}

// whyItGrew names what made a workflow take more time than the week before:
// the job whose time went up most, per run or by running more often.
func whyItGrew(r *timeRow) string {
	if pct, ok := growth(r.week, r.prior, time.Hour); !ok || pct <= 0 {
		return ""
	}
	var top *timeJob
	for _, j := range r.jobs {
		if top == nil || j.week-j.prior > top.week-top.prior {
			top = j
		}
	}
	if top == nil {
		return ""
	}
	before, now := medianOf(top.before), medianOf(top.runs)
	runs := sDim.Render(fmt.Sprintf(" · runs %d → %d", len(r.before), len(r.runs)))
	if before > 0 && now-before >= noticeableDifference && float64(now) >= float64(before)*1.1 {
		return sWhite.Render(top.name) + sDim.Render(" went from ") + fg(cSteel).Render(dur(before)) + sDim.Render(" to ") +
			fg(cSteel).Render(dur(now)) + sDim.Render(" a run") + runs
	}
	return sWhite.Render(top.name) + sDim.Render(" ran more often") + runs
}

// topUsers names the repos that took the most of a workflow's time.
func topUsers(a *App, users map[string]time.Duration, n int) string {
	type ru struct {
		repo string
		d    time.Duration
	}
	var list []ru
	for repo, d := range users {
		list = append(list, ru{repo, d})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].d > list[j].d || (list[i].d == list[j].d && list[i].repo < list[j].repo)
	})
	var parts []string
	for i, x := range list {
		if i == n {
			break
		}
		parts = append(parts, icoIn("repo", cGold)+sText.Render(a.short(x.repo))+" "+sWhite.Render(hours(x.d)))
	}
	s := strings.Join(parts, "  ")
	if len(list) > n {
		s += sDim.Render(fmt.Sprintf("  +%d repos", len(list)-n))
	}
	return s
}

var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// daySpark draws a bar per day, scaled to peak.
func daySpark(days []time.Duration, peak time.Duration) string {
	var b strings.Builder
	for _, d := range days {
		if d <= 0 || peak <= 0 {
			b.WriteString(fg(cMeterOff).Render(string(sparkBlocks[0])))
			continue
		}
		i := min(int(int64(d)*int64(len(sparkBlocks)-1)/int64(peak)), len(sparkBlocks)-1)
		b.WriteString(fg(cSteel).Render(string(sparkBlocks[i])))
	}
	return b.String()
}
