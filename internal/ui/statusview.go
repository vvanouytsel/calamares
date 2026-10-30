package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// statusView is the GitHub status screen (ctrl+g, :status): the incidents
// GitHub reports about the parts we show, open ones first, the maintenance
// coming up and the last week's, with what an incident did to your runs.
type statusView struct {
	id    int
	t     *Table
	items map[string]statusItem
}

// statusItem is a line of the screen: an incident or a maintenance.
type statusItem struct {
	incident    *gh.Incident
	maintenance *gh.Maintenance
}

// statusDays is how far back the screen lists resolved incidents.
const statusDays = 7 * 24 * time.Hour

func newStatusView(a *App) *statusView {
	return &statusView{id: a.newID(), items: map[string]statusItem{}, t: newTable(
		Column{Title: "STATE", Min: 15, Help: "where GitHub says the incident stands, or when the maintenance is planned"},
		Column{Title: "INCIDENT", Max: 70, Flex: true},
		Column{Title: "AFFECTS", Max: 24, Flex: true, Drop: 1, Help: "the parts of GitHub it is about"},
		Column{Title: "DURATION", Right: true, Min: 9, Help: "how long it lasted, or has lasted so far; for maintenance, when it starts"},
	)}
}

func (v *statusView) ID() int       { return v.id }
func (v *statusView) Crumb() string { return "GitHub status" }

func (v *statusView) Init(a *App) tea.Cmd {
	a.st.ghStatus.next = time.Time{}
	return a.pollStatus()
}

func (v *statusView) Refresh(a *App, force bool) tea.Cmd {
	if !force {
		return nil
	}
	a.st.ghStatus.next = time.Time{}
	return a.pollStatus()
}

func (v *statusView) Updated(a *App) time.Time     { return a.st.ghStatus.state.at }
func (v *statusView) Loading(a *App) bool          { return a.st.ghStatus.state.busy }
func (v *statusView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *statusView) FilterValue() string          { return v.t.Filter() }
func (v *statusView) FilterInfo(a *App) filterInfo { return v.t.Info() }

func (v *statusView) sync(a *App) {
	now := time.Now()
	s := a.st.ghStatus.status
	v.items = map[string]statusItem{}
	var open, recent []Row
	for _, in := range relevantIncidents(s) {
		in := in
		if !in.Open() && now.Sub(in.CreatedAt) > statusDays {
			continue
		}
		key := "i\x00" + in.ID
		v.items[key] = statusItem{incident: &in}
		if in.Open() {
			open = append(open, v.incidentRow(a, key, in, now))
		} else {
			recent = append(recent, v.incidentRow(a, key, in, now))
		}
	}
	var coming []Row
	for _, m := range s.Maintenances {
		m := m
		if !relevant(m.Components, m.Name) || m.Status == "completed" {
			continue
		}
		key := "m\x00" + m.ID
		v.items[key] = statusItem{maintenance: &m}
		coming = append(coming, v.maintenanceRow(key, m))
	}
	var rows []Row
	d := a.st.ghStatus
	if !d.none && !d.state.at.IsZero() {
		for _, sec := range []struct {
			title, none string
			rows        []Row
		}{
			{"Open incidents", "None.", open},
			{"Upcoming maintenance", "None planned for the parts above.", coming},
			{"Last 7 days", "No incidents.", recent},
		} {
			rows = append(rows, statusNote(sec.title, Cell{Text: sec.title, Styled: sWhite.Bold(true).Render(sec.title)}))
			if len(sec.rows) == 0 {
				rows = append(rows, statusNote(sec.title+" none", Cell{}, Cell{Text: sec.none, Styled: sDim.Render(sec.none)}))
			}
			rows = append(rows, sec.rows...)
		}
	}
	switch {
	case d.none:
		v.t.Empty = a.client.Host + " has no public status page."
	case d.state.at.IsZero() && d.state.err != nil:
		v.t.Empty = "Could not read githubstatus.com: " + d.state.err.Error()
	case d.state.at.IsZero():
		v.t.Empty = "Reading githubstatus.com…"
	}
	v.t.SetRows(rows)
	v.offHeading(1)
}

// statusNote is a line of the screen the cursor passes over: a section's
// heading, or what a section says when it has nothing.
func statusNote(key string, cells ...Cell) Row {
	return Row{Key: "h\x00" + key, Kind: KNone, Cells: cells}
}

// offHeading moves the cursor off headings and notes, in the direction it
// was going, or back the other way at the end of the list.
func (v *statusView) offHeading(dir int) {
	onNote := func() bool {
		r := v.t.Selected()
		return r != nil && strings.HasPrefix(r.Key, "h\x00")
	}
	for _, d := range []int{dir, -dir} {
		for onNote() && v.t.Cursor()+d >= 0 && v.t.Cursor()+d < v.t.Len() {
			v.t.SetCursor(v.t.Cursor() + d)
		}
		if !onNote() {
			return
		}
	}
}

// affects names the parts of GitHub we show that an incident is about.
func affects(components []string) string {
	var out []string
	for _, c := range components {
		if short, _, ok := statusPart(c); ok {
			out = append(out, short)
		}
	}
	switch len(out) {
	case 0:
		return ""
	case 1:
		return out[0]
	}
	return out[0] + fmt.Sprintf(" +%d", len(out)-1)
}

func (v *statusView) incidentRow(a *App, key string, in gh.Incident, now time.Time) Row {
	state := statusCell(KOK, 0, "resolved")
	lasted := steelCell(dur(in.Ended().Sub(in.CreatedAt)), false)
	k := KOK
	if in.Open() {
		k = KRun
		if a.incidentColor(in) == cRed {
			k = KFail
		}
		word := icon("warning") + " " + in.Status
		state = Cell{Text: word, Styled: fg(a.incidentColor(in)).Render(word)}
		lasted = steelCell(age(in.CreatedAt, now)+" so far", false)
	}
	return Row{Key: key, Kind: k, Cells: []Cell{
		state,
		titleCell(in.Name, false),
		steelCell(affects(in.Components), false),
		lasted,
	}}
}

func (v *statusView) maintenanceRow(key string, m gh.Maintenance) Row {
	word := icon("clock") + " " + strings.ReplaceAll(m.Status, "_", " ")
	return Row{Key: key, Kind: KQueue, Cells: []Cell{
		{Text: word, Styled: fg(cBlue).Render(word)},
		titleCell(m.Name, false),
		steelCell(affects(m.Components), false),
		steelCell(m.From.Local().Format("Mon 15:04"), false),
	}}
}

func (v *statusView) selected() (statusItem, bool) {
	r := v.t.Selected()
	if r == nil {
		return statusItem{}, false
	}
	it, ok := v.items[r.Key]
	return it, ok
}

func (v *statusView) Keys(a *App) []Binding {
	var bs []Binding
	if it, ok := v.selected(); ok && it.incident != nil {
		in := *it.incident
		failed, _ := a.runsDuring(in)
		if len(failed) > 0 {
			bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: fmt.Sprintf("%d failed %s", len(failed), plural("run", len(failed))),
				Help: "the runs that failed while it was going on",
				Do:   do(func() tea.Cmd { return a.push(newIncidentRunsView(a, in)) })},
				Binding{Keys: []string{"R"}, Desc: fmt.Sprintf("rerun %d", len(failed)), Help: "rerun the failed jobs of the runs that failed during it",
					Mutates: true, Need: gh.CapWrite, Do: do(func() tea.Cmd { return a.rerunFailedOf(failed, func() bool { return true }) })})
		}
		if in.URL != "" {
			bs = append(bs, Binding{Keys: []string{"o"}, Desc: "status page", Help: "open the incident on githubstatus.com",
				Do: do(func() tea.Cmd { return a.openURL(in.URL) })})
		}
	} else if ok && it.maintenance != nil && it.maintenance.URL != "" {
		url := it.maintenance.URL
		bs = append(bs, Binding{Keys: []string{"o"}, Desc: "status page", Help: "open it on githubstatus.com",
			Do: do(func() tea.Cmd { return a.openURL(url) })})
	}
	return grouped("GITHUB STATUS", append(bs, filterHint, v.t.moveKeys())...)
}

func (v *statusView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		before := v.t.Cursor()
		cmd, _ := a.press(v.Keys(a), k.String())
		dir := 1
		if v.t.Cursor() < before {
			dir = -1
		}
		v.offHeading(dir)
		return cmd
	}
	return nil
}

// partsLine says how each part of GitHub we show is doing.
func (a *App) partsLine() string {
	var parts []string
	for _, p := range statusParts {
		for _, c := range a.st.ghStatus.status.Components {
			if c.Name != p.name {
				continue
			}
			word, col := partState(c.Status)
			dot := fg(col).Render("●")
			if c.Status != "operational" {
				dot += " " + fg(col).Render(word)
			}
			parts = append(parts, sDim.Render(p.short)+" "+dot)
		}
	}
	return strings.Join(parts, "   ")
}

func (v *statusView) chips(a *App) []string {
	var open, coming, week int
	now := time.Now()
	for _, it := range v.items {
		switch {
		case it.incident != nil && it.incident.Open():
			open++
		case it.maintenance != nil:
			coming++
		}
		if it.incident != nil && now.Sub(it.incident.CreatedAt) < statusDays {
			week++
		}
	}
	var chips []string
	if open > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s %d open", icon("warning"), open), cAmber, tint[KRun]))
	}
	if coming > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s %d %s", icon("clock"), coming, plural("maintenance", 1)), cBlue, lipgloss.Color(mix(string(cBg), string(cBlue), 0.18))))
	}
	if week > 0 && open == 0 {
		chips = append(chips, sText.Render(fmt.Sprintf("%d this week", week)))
	}
	return chips
}

func (v *statusView) Render(a *App, w, h int) string {
	scope := "githubstatus.com"
	if at := a.st.ghStatus.state.at; !at.IsZero() {
		scope += " · checked " + age(at, time.Now()) + " ago"
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("warning", "GitHub status", scope, -1, v.chips(a), w), " " + a.partsLine()},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

func (v *statusView) panel(a *App, w, h int) []string {
	it, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "Nothing selected.")
	}
	p := newPanel(w)
	now := time.Now()
	if m := it.maintenance; m != nil {
		p.add(fg(cBlue).Render(icon("clock")+" ") + sWhite.Bold(true).Render(m.Name))
		p.add(sDim.Render("from ") + fg(cSteel).Render(m.From.Local().Format("Mon 2 Jan 15:04")) + sDim.Render(" until ") + fg(cSteel).Render(m.Until.Local().Format("15:04")))
		if affects(m.Components) != "" {
			p.add(sDim.Render("affects ") + sText.Render(strings.Join(shortParts(m.Components), " · ")))
		}
		return p.render(h)
	}
	in := *it.incident
	c := a.incidentColor(in)
	glyph := icon("warning")
	if !in.Open() {
		c, glyph = cGreen, KOK.Glyph(0)
	}
	p.add(fg(c).Render(glyph+" ") + sWhite.Bold(true).Render(in.Name))
	when := sDim.Render("started ") + fg(cSteel).Render(clock(in.CreatedAt))
	if in.Open() {
		when += sDim.Render(" · ") + fg(cSteel).Render(age(in.CreatedAt, now)) + sDim.Render(" so far")
	} else {
		when += sDim.Render(" · lasted ") + fg(cSteel).Render(dur(in.Ended().Sub(in.CreatedAt)))
	}
	impact := in.Impact
	if impact == "" || impact == "none" {
		impact = "minor"
	}
	k := KRun
	if c == cRed {
		k = KFail
	}
	if !in.Open() {
		k = KOK
	}
	p.add(roundPill(impact, c, tint[k]) + " " + when)
	if parts := shortParts(in.Components); len(parts) > 0 {
		p.add(sDim.Render("affects ") + sText.Render(strings.Join(parts, " · ")))
	}

	p.label("Timeline")
	for i, u := range in.Updates {
		if i == 6 {
			p.add(sDim.Render(fmt.Sprintf("… %d earlier", len(in.Updates)-i)))
			break
		}
		uc := cAmber
		if u.Status == "resolved" {
			uc = cGreen
		}
		p.add(fg(cSteel).Render(clock(u.At)) + " " + fg(uc).Render(u.Status))
		for _, l := range strings.Split(ansi.Wordwrap(u.Body, max(p.inner()-6, 10), ""), "\n") {
			p.add("      " + sText.Render(l))
		}
	}

	if !affectsRuns(in) {
		return p.render(h)
	}
	failed, slow := a.runsDuring(in)
	p.label("Affected runs")
	if len(failed) == 0 && len(slow) == 0 {
		p.add(sDim.Render("none of the loaded runs failed or waited long during it"))
		return p.render(h)
	}
	if len(failed) > 0 {
		groups, _ := a.failedDuring(in)
		line := glyph0(KFail) + " " + sWhite.Render(fmt.Sprintf("%d %s", len(failed), plural("run", len(failed)))) + sDim.Render(" failed")
		if groups > 0 {
			line += sDim.Render(fmt.Sprintf(" in %d %s", groups, plural("cause", groups)))
		}
		p.add(line + sDim.Render(" since ") + fg(cSteel).Render(clock(in.CreatedAt)))
		p.add("  " + reposOf(a, failed))
	}
	if len(slow) > 0 {
		p.add(fg(cAmber).Render(icon("clock")) + " " + sWhite.Render(fmt.Sprintf("%d %s", len(slow), plural("run", len(slow)))) + sDim.Render(" queued over 10m"))
	}
	if len(failed) > 0 {
		p.add("")
		what := "the run"
		if len(failed) > 1 {
			what = fmt.Sprintf("the %d runs", len(failed))
		}
		keys := keycap("R") + sText.Render(" reruns "+what+"  ") + keycap("enter") + sText.Render(" lists them")
		if in.Open() {
			p.callout(cAmber, sWhite.Render("GitHub is investigating this incident. When it is resolved:"), keys)
		} else {
			p.callout(cGreen, sWhite.Render("It is over: the runs that failed during it can run again."), keys)
		}
	}
	return p.render(h)
}

func glyph0(k Kind) string { return fg(k.Color()).Render(k.Glyph(0)) }

// shortParts are the short names of the parts we show of components.
func shortParts(components []string) []string {
	var out []string
	for _, c := range components {
		if short, _, ok := statusPart(c); ok {
			out = append(out, short)
		}
	}
	return out
}

// reposOf names the repos of runs: the first two and how many more.
func reposOf(a *App, runs []repoRun) string {
	var repos []string
	for _, x := range runs {
		if !containsFold(repos, x.repo) {
			repos = append(repos, x.repo)
		}
	}
	var parts []string
	for i, r := range repos {
		if i == 2 {
			break
		}
		parts = append(parts, icoIn("repo", cGold)+sText.Render(a.short(r)))
	}
	s := strings.Join(parts, sDim.Render(", "))
	if len(repos) > 2 {
		s += sDim.Render(fmt.Sprintf(" and %d more", len(repos)-2))
	}
	return s
}

// incidentRunsView lists the runs that failed while an incident was going on.
type incidentRunsView struct {
	id   int
	in   gh.Incident
	t    *Table
	runs map[string]repoRun
	peek peeker
}

func newIncidentRunsView(a *App, in gh.Incident) *incidentRunsView {
	v := &incidentRunsView{id: a.newID(), in: in, t: newTable(runColumns()...)}
	v.t.Tickable = true
	return v
}

func (v *incidentRunsView) ID() int                            { return v.id }
func (v *incidentRunsView) Crumb() string                      { return "during the incident" }
func (v *incidentRunsView) Init(a *App) tea.Cmd                { return nil }
func (v *incidentRunsView) Refresh(a *App, force bool) tea.Cmd { return nil }
func (v *incidentRunsView) SetFilter(a *App, q string)         { v.t.SetFilter(q, a.me) }
func (v *incidentRunsView) FilterValue() string                { return v.t.Filter() }
func (v *incidentRunsView) FilterInfo(a *App) filterInfo       { return v.t.Info() }

func (v *incidentRunsView) sync(a *App) {
	now := time.Now()
	failed, _ := a.runsDuring(v.in)
	v.runs = map[string]repoRun{}
	var rows []Row
	for _, x := range failed {
		key := fmt.Sprint(x.run.ID)
		v.runs[key] = x
		rows = append(rows, activityRow(a, key, x, now, false))
	}
	v.t.Empty = "None of the loaded runs failed during it."
	v.t.SetRows(rows)
}

func (v *incidentRunsView) selected() (repoRun, bool) {
	r := v.t.Selected()
	if r == nil {
		return repoRun{}, false
	}
	x, ok := v.runs[r.Key]
	return x, ok
}

func (v *incidentRunsView) Keys(a *App) []Binding {
	var bs []Binding
	var all []repoRun
	for _, x := range v.runs {
		all = append(all, x)
	}
	if len(all) > 0 {
		bs = append(bs, Binding{Keys: []string{"R"}, Desc: fmt.Sprintf("rerun all %d", len(all)), Help: "rerun the failed jobs of every run listed",
			Mutates: true, Need: gh.CapWrite, Do: do(func() tea.Cmd { return a.rerunFailedOf(all, v.t.ClearMarks) })})
	}
	if x, ok := v.selected(); ok {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })},
			Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })},
			a.runRepoKey(x.repo, x.run))
	}
	return grouped("RUNS", append(bs, filterHint, v.t.moveKeys())...)
}

func (v *incidentRunsView) Update(a *App, msg tea.Msg) tea.Cmd {
	if cmd, ok := v.peek.handle(a, v.id, msg); ok {
		return cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		if x, ok := v.selected(); ok {
			return tea.Batch(cmd, v.peek.ask(a, v.id, x.repo, &x.run))
		}
		return cmd
	}
	return nil
}

func (v *incidentRunsView) Render(a *App, w, h int) string {
	return listWithPanel(a, w, h,
		[]string{screenTitle("warning", "Failed during the incident", v.in.Name, v.t.Len(), nil, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string {
			if x, ok := v.selected(); ok {
				return a.runPanel(x, pw, ph)
			}
			return emptyPanel(pw, ph, "No run selected.")
		})
}

// clock is a time of day, with the weekday when it is not today.
func clock(t time.Time) string {
	t = t.Local()
	if y, m, d := t.Date(); y == time.Now().Year() && m == time.Now().Month() && d == time.Now().Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}
