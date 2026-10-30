package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// overviewView is the first view of the Platform place: what is wrong across
// the shared workflows and actions, the schedules, the runners and the
// warnings, in two sections, Failures and Maintenance, with the runner usage
// of the week under them. The panel and the keys of an issue are those of
// its row in the view that lists it.
type overviewView struct {
	place  *activityView
	t      *Table // the issues, in the order they are drawn
	issues []issue
	byKey  map[string]issue
}

// issueSection is the section an issue is listed in.
type issueSection int

const (
	secFailure     issueSection = iota // fails, or cannot start, now
	secMaintenance                     // works, but needs an update to keep working
)

// issue is one thing on the Overview: what it is about, what is wrong and
// where it is listed in full.
type issue struct {
	key     string
	section issueSection
	item    string    // what it is about: "build.yml v9"
	where   string    // where it lives, after the item: "payments"
	text    string    // what is wrong: "Fails more than v8"
	since   time.Time // failures: since when
	repos   int       // maintenance: how many repos it concerns
	view    int       // the view that lists it
	row     string    // the key of its row there
}

func newOverviewView(place *activityView) *overviewView {
	return &overviewView{place: place, byKey: map[string]issue{}, t: newTable(
		Column{Title: "ITEM", Help: "what the issue is about: a shared workflow or action at a version, a scheduled workflow, a waiting job or a warning"},
		Column{Title: "ISSUE", Help: "what is wrong with it"},
		Column{Title: "SINCE", Help: "failures: how long it has been failing or waiting"},
		Column{Title: "REPOS", Help: "maintenance: how many repositories it concerns"},
	)}
}

func (o *overviewView) table() *Table { return o.t }

// views are the other views of the place, whose findings it lists.
func (o *overviewView) views() []actSub {
	v := o.place
	return []actSub{v.shared, v.sharedActions, v.warnings, v.scheduled, v.runners, v.caches, v.time}
}

func (o *overviewView) load(a *App, force bool) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range o.views() {
		cmds = append(cmds, s.load(a, force))
	}
	return tea.Batch(cmds...)
}

func (o *overviewView) busy(a *App) bool {
	for _, s := range o.views() {
		if s.busy(a) {
			return true
		}
	}
	return false
}

func (o *overviewView) count(a *App) int { return len(o.issues) }

func (o *overviewView) SetFilter(a *App, q string) { o.t.SetFilter(q, a.me) }
func (o *overviewView) FilterValue() string        { return o.t.Filter() }
func (o *overviewView) FilterInfo(a *App) filterInfo {
	return o.t.Info()
}

// sync gathers the issues from the other views, which are synced first.
func (o *overviewView) sync(a *App) {
	v := o.place
	now := time.Now()
	var out []issue
	add := func(is issue) { out = append(out, is) }

	held := heldShared(a)
	for _, it := range held.workflows {
		if c, ok := it.newCheck(); ok && c.verdict == verdictWorse {
			add(issue{key: "worse\x00" + it.key(), section: secFailure, item: it.name() + " " + shortVersion(c.ver.version),
				text: "Fails more than " + shortVersion(c.prev.version), since: c.first,
				view: viewSharedWorkflows, row: it.key() + "\x00" + c.ver.version})
		}
	}
	for _, s := range v.scheduled.list {
		if !s.disabled() && s.kind() == KFail && s.last != nil {
			add(issue{key: "sched\x00" + scheduleKey(s.Repo, s.File), section: secFailure, item: s.name(), where: a.short(s.Repo),
				text: "Last scheduled run failed", since: s.last.CreatedAt, view: viewScheduled, row: scheduleKey(s.Repo, s.File)})
		}
	}
	for i, g := range v.runners.waits {
		_, k := v.runners.waitVerdict(a, g.labels)
		waited := g.longest(now)
		if k != KFail && (k != KWait || waited < stuckAfter) {
			continue
		}
		j := g.jobs[0]
		item := j.job.Name
		if len(g.jobs) > 1 {
			item = fmt.Sprintf("%d jobs", len(g.jobs))
		}
		text := "No runner has its labels"
		if k == KWait {
			text = "Waiting for a runner"
		}
		add(issue{key: "wait\x00" + labelSet(g.labels), section: secFailure, item: item, where: a.short(j.repo),
			text: text, since: now.Add(-waited), view: viewRunners, row: fmt.Sprintf("wait\x00%d", i)})
	}

	outdated := func(it *sharedItem, view int) {
		var vers []string
		first := ""
		for _, ver := range it.versions {
			if ver.behind {
				vers = append(vers, shortVersion(ver.version))
				if first == "" {
					first = ver.version
				}
			}
		}
		if len(vers) == 0 {
			return
		}
		add(issue{key: "old\x00" + it.key(), section: secMaintenance, item: it.name() + " " + strings.Join(vers, ", "),
			text: "Older version", repos: it.reposBehind(), view: view, row: it.key() + "\x00" + first})
	}
	for _, it := range held.workflows {
		outdated(it, viewSharedWorkflows)
	}
	for _, it := range held.actions {
		if v.sharedActions.everyone || v.sharedActions.yours(a, it) {
			outdated(it, viewSharedActions)
		}
	}
	for _, g := range v.warnings.groups {
		item, where := g.example.run.Name, ""
		if len(g.repos) == 1 {
			where = a.short(g.repos[0])
		}
		if via := g.viaList(); len(via) > 0 {
			item, where = refName(via[0]), ""
			if len(via) > 1 {
				where = fmt.Sprintf("and %d more", len(via)-1)
			}
		}
		text := g.pattern
		if len(g.kinds) > 0 {
			text = g.kinds[0].text
		}
		add(issue{key: "warn\x00" + g.key, section: secMaintenance, item: item, where: where, text: firstSentence(text),
			repos: len(g.repos), view: viewWarnings, row: g.key})
	}
	for _, s := range v.scheduled.list {
		if !s.disabled() {
			continue
		}
		text := "Workflow disabled"
		if s.byGitHub() {
			text = "Schedule disabled by GitHub"
		}
		add(issue{key: "off\x00" + scheduleKey(s.Repo, s.File), section: secMaintenance, item: s.name(), where: a.short(s.Repo),
			text: text, repos: 1, view: viewScheduled, row: scheduleKey(s.Repo, s.File)})
	}

	for _, c := range v.caches.list {
		if c.kind() == KOK {
			continue
		}
		text := "Cache near its limit"
		if c.kind() == KFail {
			text = "Cache full"
		}
		add(issue{key: "cache\x00" + c.repo, section: secMaintenance, item: "Actions cache", where: a.short(c.repo),
			text: fmt.Sprintf("%s: %s of %s", text, bytesText(c.Size), cacheLimitText), repos: 1, view: viewCaches, row: c.repo})
	}

	// Failures that have lasted longest first; maintenance by how many repos it concerns.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].section != out[j].section {
			return out[i].section < out[j].section
		}
		if out[i].section == secFailure {
			return out[i].since.Before(out[j].since)
		}
		return out[i].repos > out[j].repos
	})
	o.issues, o.byKey = out, map[string]issue{}
	var rows []Row
	for _, is := range out {
		o.byKey[is.key] = is
		cells := []Cell{{Text: is.item + " " + is.where}, {Text: is.text}, {}, {}}
		rows = append(rows, Row{Key: is.key, Kind: KNone, Cells: cells})
	}
	o.t.SetRows(rows)
}

// stuckAfter is how long a job may wait for a runner we cannot tell about
// before the Overview lists it.
const stuckAfter = 10 * time.Minute

// firstSentence is the start of a text up to its first full stop.
func firstSentence(s string) string {
	if loc := sentenceEnd.FindStringIndex(s); loc != nil {
		return strings.TrimSpace(s[:loc[0]+1])
	}
	return strings.TrimSpace(s)
}

// selected is the issue under the cursor.
func (o *overviewView) selected() (issue, bool) {
	r := o.t.Selected()
	if r == nil {
		return issue{}, false
	}
	is, ok := o.byKey[r.Key]
	return is, ok
}

// owner is the view that lists is, with the cursor put on its row there.
func (o *overviewView) owner(is issue) (actSub, bool) {
	s, ok := o.place.subs()[is.view]
	if !ok {
		return nil, false
	}
	return s, s.table().SelectKey(is.row)
}

// viewKeysOff are keys of a view that change the view itself; on the
// Overview they would change a view you don't see.
var viewKeysOff = map[string]bool{"enter": true, "right": true, " ": true, "v": true, "g": true, "S": true, "b": true, "e": true, "X": true}

func (o *overviewView) keys(a *App) []Binding {
	is, ok := o.selected()
	if !ok {
		return nil
	}
	name := viewNames[is.view]
	bs := []Binding{{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open in " + name, Help: "open it in the view that lists it in full",
		Do: do(func() tea.Cmd {
			o.place.setFilter(is.view)
			if s, ok := o.place.subs()[is.view]; ok {
				s.sync(a)
				s.table().SelectKey(is.row)
			}
			return o.place.loadSub(a, false)
		})}}
	s, ok := o.owner(is)
	if !ok {
		return bs
	}
	for _, b := range s.keys(a) {
		if !slicesAny(b.Keys, func(k string) bool { return viewKeysOff[k] }) {
			bs = append(bs, b)
		}
	}
	return bs
}

func slicesAny(keys []string, f func(string) bool) bool {
	for _, k := range keys {
		if f(k) {
			return true
		}
	}
	return false
}

// afterMove reads what the panel of the issue under the cursor explains.
func (o *overviewView) afterMove(a *App) tea.Cmd {
	is, ok := o.selected()
	if !ok {
		return nil
	}
	if s, ok := o.owner(is); ok {
		if m, ok := s.(interface{ afterMove(*App) tea.Cmd }); ok {
			return m.afterMove(a)
		}
	}
	return nil
}

func (o *overviewView) panel(a *App, w, h int) []string {
	is, ok := o.selected()
	if !ok {
		return emptyPanel(w, h, "No issue selected.")
	}
	if s, ok := o.owner(is); ok {
		return s.panel(a, w, h)
	}
	p := newLayerPanel(w)
	p.add(sWhite.Bold(true).Render(is.item))
	p.add(sText.Render(is.text))
	return p.render(h)
}

// failures and maintenance count the issues of each section.
func (o *overviewView) sections() (failures, maintenance int) {
	for _, is := range o.issues {
		if is.section == secFailure {
			failures++
		} else {
			maintenance++
		}
	}
	return
}

func (o *overviewView) chips(a *App) []string {
	f, m := o.sections()
	var chips []string
	if f > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d %s", f, plural("failure", f))))
	}
	if m > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d maintenance %s", m, plural("item", m))))
	}
	if f+m == 0 && !o.busy(a) {
		chips = append(chips, statusChip(KOK, 0, "No issues"))
	}
	return chips
}

func (o *overviewView) coverage(a *App) string { return "" }
func (o *overviewView) listHead(a *App) string { return "" }

// healthy names the areas that were read and have nothing to report.
func (o *overviewView) healthy(a *App) (fine, reading []string) {
	v := o.place
	has := map[int]bool{}
	for _, is := range o.issues {
		has[is.view] = true
	}
	areas := []struct {
		view int
		name string
	}{{viewSharedWorkflows, "Shared workflows"}, {viewSharedActions, "Shared actions"}, {viewWarnings, "Warnings"},
		{viewScheduled, "Schedules"}, {viewRunners, "Runners"}}
	for _, ar := range areas {
		s := v.subs()[ar.view]
		switch {
		case s.busy(a) && !has[ar.view]:
			reading = append(reading, strings.ToLower(ar.name))
		case !has[ar.view]:
			fine = append(fine, ar.name)
		}
	}
	return fine, reading
}

// renderList draws the sections: their title, what they hold and their
// issues under column heads, then the runner usage and what is healthy.
func (o *overviewView) renderList(a *App, w, h int) string {
	now := time.Now()
	itemW := 0
	for _, is := range o.issues {
		itemW = max(itemW, width(o.itemText(is)))
	}
	itemW = min(max(itemW, 12), max(w/3, 16))
	lastW := 8
	issueW := max(w-itemW-lastW-6, 10)
	visible := map[string]bool{}
	for _, k := range o.t.FilteredKeys() {
		visible[k] = true
	}
	sel := ""
	if r := o.t.Selected(); r != nil {
		sel = r.Key
	}

	var lines []string
	cursorLine := 0
	section := func(title, about, last string, sec issueSection) {
		var mine []issue
		for _, is := range o.issues {
			if is.section == sec && visible[is.key] {
				mine = append(mine, is)
			}
		}
		if len(mine) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, " "+sWhite.Bold(true).Render(title), " "+sDim.Render(about),
			" "+sDim.Render(padTo("ITEM", itemW)+"  "+padTo("ISSUE", issueW)+"  "+fmt.Sprintf("%*s", lastW, last)))
		for _, is := range mine {
			st := sRed
			right := ageWords(is.since, now)
			if sec == secMaintenance {
				st, right = sWait, fmt.Sprint(is.repos)
			}
			item := clip(sWhite.Bold(true).Render(is.item)+o.whereText(is), itemW)
			line := padTo(item, itemW) + "  " + padTo(clip(st.Render(is.text), issueW), issueW) + "  " + sText.Render(fmt.Sprintf("%*s", lastW, right))
			if is.key == sel {
				cursorLine = len(lines)
				lines = append(lines, withSelectionBg(accentBar()+line, cSelBg, false))
				continue
			}
			lines = append(lines, " "+line)
		}
	}
	section("Failures", "Workflows and jobs that fail or cannot start", "SINCE", secFailure)
	section("Maintenance", "Still works, but needs an update to keep working", "REPOS", secMaintenance)

	if tv := o.place.time; tv.total > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		usage := sText.Render(hoursWords(tv.total))
		if pct, ok := growth(tv.total, tv.prior, time.Hour); ok {
			if pct > 0 {
				usage += "  " + sRed.Render(fmt.Sprintf("+%d%%", pct)) + sDim.Render(" on the week before")
			} else {
				usage += "  " + sGreen.Render(fmt.Sprintf("%d%%", pct)) + sDim.Render(" on the week before")
			}
		}
		lines = append(lines, " "+sWhite.Bold(true).Render("Runner usage"),
			" "+sDim.Render(padTo("Last 7 days", fieldWidth))+usage)
		if top := tv.largest(); top != nil {
			lines = append(lines, " "+sDim.Render(padTo("Largest", fieldWidth))+sText.Render(top.name()+", "+hoursWords(top.week)))
		}
	}

	fine, reading := o.healthy(a)
	if len(fine) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, " "+sWhite.Bold(true).Render("Healthy")+"  "+sDim.Render(clip(strings.Join(fine, ", "), max(w-10, 8))))
	}
	if len(reading) > 0 {
		lines = append(lines, " "+sDim.Render(clip("Still reading "+strings.Join(reading, ", ")+"…", w-1)))
	}
	if len(lines) == 0 {
		lines = append(lines, " "+sDim.Render("Reading your repositories…"))
	}
	// Keep the cursor in sight on a short screen.
	if len(lines) > h {
		top := min(max(cursorLine-h/2, 0), len(lines)-h)
		lines = lines[top : top+h]
	}
	return strings.Join(lines, "\n")
}

// itemText is the item of an issue and where it lives, as plain text.
func (o *overviewView) itemText(is issue) string { return ansi.Strip(is.item + o.whereText(is)) }

func (o *overviewView) whereText(is issue) string {
	if is.where == "" {
		return ""
	}
	return sDim.Render(" · " + is.where)
}

// padTo pads s with spaces to w cells.
func padTo(s string, w int) string { return s + strings.Repeat(" ", max(w-width(s), 0)) }

func (o *overviewView) explain(a *App) []explanation {
	return []explanation{
		{"Failures", "shared workflow versions that fail more often than the version before them, scheduled workflows whose last scheduled run failed, and jobs that no runner can take"},
		{"Maintenance", "versions of your shared workflows and actions that newer ones have replaced in other repositories, warnings such as deprecations, and schedules that no longer run"},
		{"Runner usage", "the runner time of the last 7 days against the week before, and the workflow that took the most"},
		{"Healthy", "the views that were read and have nothing to report"},
		{"enter", "open the issue in the view that lists it in full; the other keys act on it as they do there"},
	}
}

// largest is the row of the time view that took the most this week.
func (v *timeView) largest() *timeRow {
	var top *timeRow
	for _, r := range v.rows {
		if top == nil || r.week > top.week {
			top = r
		}
	}
	return top
}

// hoursWords is a runner time in words: "153 hours", "45 minutes".
func hoursWords(d time.Duration) string {
	if d < time.Hour {
		n := int(d.Round(time.Minute).Minutes())
		return fmt.Sprintf("%d %s", n, plural("minute", n))
	}
	n := int(d.Hours() + 0.5)
	return fmt.Sprintf("%d %s", n, plural("hour", n))
}

// ageWords says how long ago t was in words: "20 min", "5 hours", "2 days".
func ageWords(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		n := int(d.Hours())
		return fmt.Sprintf("%d %s", n, plural("hour", n))
	}
	n := int(d.Hours() / 24)
	return fmt.Sprintf("%d %s", n, plural("day", n))
}

func (o *overviewView) subtitle(a *App, repos string) string {
	return "Shared workflows, actions, schedules and runners of " + repos
}

// repositories counts repositories in words: "9 repositories".
func repositories(n int) string { return fmt.Sprintf("%d %s", n, plural("repository", n)) }

func (v *sharedView) subtitle(a *App, repos string) string {
	s := "Reusable workflows called by " + repositories(v.held.repos)
	if v.actions {
		s = "Your actions used by " + repositories(v.held.repos)
		if v.everyone {
			s = "Actions used by " + repositories(v.held.repos)
		}
	}
	switch {
	case !v.held.fromFiles && a.st.usesState.busy:
		s += " · reading the workflow files…"
	case !v.held.fromFiles:
		s += fmt.Sprintf(" · seen in their last %d runs", repoStatusFilter.PerPage)
	case a.st.usesState.busy:
		s += " · reading the workflow files again…"
	default:
		s += " · files read " + agoWords(a.st.usesState.at, time.Now())
	}
	return s
}

func (v *sharedView) state(a *App) []string {
	parts := []string{"Sorted by " + v.orderLabel()}
	if v.grouped {
		parts = append(parts, "Grouped by source")
	}
	if v.behind {
		parts = append(parts, "Outdated only")
	}
	return parts
}

func (v *warningsView) subtitle(a *App, repos string) string {
	s := "Warnings of the latest run of each workflow on the default branch of " + repos
	if v.busy(a) {
		s += fmt.Sprintf(" · reading %d…", len(a.st.warningBusy)+len(a.st.warningQueue))
	}
	return s
}

func (v *warningsView) state(a *App) []string {
	if v.flat {
		return []string{"Every run on its own line"}
	}
	return []string{"Grouped by warning"}
}

func (v *scheduledView) subtitle(a *App, repos string) string {
	s := "Workflows that run on a schedule in " + repositories(a.st.usesRepos)
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

func (v *runnersView) subtitle(a *App, repos string) string {
	s := "Self-hosted runners and the jobs that wait for one, in " + repos
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

// footnote says why the organization's runners are missing, when they are.
func (v *runnersView) footnote(a *App) string {
	d := a.st.runners.orgLock
	if d == nil {
		return ""
	}
	note := lockMark() + sWait.Render(d.Reason) + sDim.Render(": these are the runners seen in your jobs")
	if d.Fix != "" {
		note += sDim.Render(" · " + d.Fix)
	}
	return note
}

func (v *timeView) subtitle(a *App, repos string) string {
	d := &a.st.times
	s := "Runner time of the last 7 days against the week before"
	if d.repos > 0 {
		s += ", in " + repositories(d.repos)
	}
	switch {
	case d.state.busy:
		s += " · reading…"
	case !d.state.at.IsZero():
		s += " · read " + agoWords(d.state.at, time.Now())
	}
	return s
}

func (v *timeView) state(a *App) []string {
	parts := []string{"By workflow", "Sorted by " + v.orderLabel()}
	if v.byRepo {
		parts[0] = "By repository"
	}
	if n := len(a.st.times.partial); n > 0 {
		parts = append(parts, fmt.Sprintf("The %d busiest repositories cover less than 7 days", n))
	}
	return parts
}

// agoWords says how long ago t was in words: "5 hours ago", or "just now".
func agoWords(t, now time.Time) string {
	if w := ageWords(t, now); w != "now" {
		return w + " ago"
	}
	return "just now"
}

// refName names an action or a reusable workflow the way the shared views
// do: "setup-tools v1" for acme/setup-tools@v1.
func refName(ref string) string {
	target, version, ok := strings.Cut(ref, "@")
	if !ok {
		return pathBase(ref)
	}
	return pathBase(target) + " " + shortVersion(version)
}
