package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// scheduledStale is how old what the Scheduled view knows may get before
// showing it reads it again.
const scheduledStale = 30 * time.Minute

// schedule is a workflow that runs on a schedule, with what we know of it.
type schedule struct {
	gh.Schedule
	workflow *gh.Workflow // nil until the repo's workflows are read
	last     *gh.Run      // its latest scheduled run, nil when it never ran
	read     bool         // its runs were read
}

func scheduleKey(repo, file string) string { return strings.ToLower(repo) + "\x00" + file }

// disabled says GitHub, or someone, switched the workflow off.
func (s schedule) disabled() bool { return s.workflow != nil && s.workflow.State != "active" }

// byGitHub says GitHub switched it off: nothing happened in the repo for 60 days.
func (s schedule) byGitHub() bool {
	return s.workflow != nil && s.workflow.State == "disabled_inactivity"
}

func (s schedule) kind() Kind {
	switch {
	case s.disabled():
		return KWait
	case s.last == nil:
		return KNone
	}
	return StatusKind(s.last.Status, s.last.Conclusion)
}

func (s schedule) name() string {
	if s.workflow != nil && s.workflow.Name != "" {
		return s.workflow.Name
	}
	return pathBase(s.File)
}

type scheduleData struct {
	workflows      map[string][]gh.Workflow // per repo
	workflowsState map[string]*loadState
	runs           map[string]*gh.Run    // the latest scheduled run, by scheduleKey
	runsState      map[string]*loadState // per repo: its scheduled runs are read in one request
}

func newScheduleData() scheduleData {
	return scheduleData{workflows: map[string][]gh.Workflow{}, workflowsState: map[string]*loadState{},
		runs: map[string]*gh.Run{}, runsState: map[string]*loadState{}}
}

func stateIn(m map[string]*loadState, key string) *loadState {
	l, ok := m[key]
	if !ok {
		l = &loadState{}
		m[key] = l
	}
	return l
}

type scheduleWorkflowsMsg struct {
	repo      string
	workflows []gh.Workflow
	err       error
}

type scheduleRunsMsg struct {
	repo string
	runs []gh.Run
	err  error
}

// scheduledRunsFilter reads the latest scheduled runs of a repo, of all its
// workflows at once.
var scheduledRunsFilter = gh.RunFilter{Event: "schedule", PerPage: 50}

// schedules joins what the files say runs on a schedule with the workflows
// and runs read so far.
func (a *App) schedules() []schedule {
	d := &a.st.sched
	var out []schedule
	for _, s := range a.st.schedules {
		sc := schedule{Schedule: s}
		for i, w := range d.workflows[s.Repo] {
			if w.Path == s.File {
				sc.workflow = &d.workflows[s.Repo][i]
			}
		}
		sc.last = d.runs[scheduleKey(s.Repo, s.File)]
		sc.read = !stateIn(d.runsState, s.Repo).at.IsZero()
		out = append(out, sc)
	}
	return out
}

// loadSchedules reads, for each repo with a schedule, its workflows and its
// latest scheduled runs: two requests a repo, in the background lane.
func (a *App) loadSchedules(force bool) tea.Cmd {
	if !a.st.scheduled {
		return a.st.loadUses(a, true)
	}
	d := &a.st.sched
	ctx := gh.Background(context.Background())
	due := func(l *loadState) bool {
		return (force || (time.Since(l.at) > scheduledStale && time.Since(l.tried) > scheduledStale)) && l.begin()
	}
	var cmds []tea.Cmd
	seen := map[string]bool{}
	for _, sc := range a.st.schedules {
		repo := sc.Repo
		if seen[repo] {
			continue
		}
		seen[repo] = true
		if due(stateIn(d.workflowsState, repo)) {
			cmds = append(cmds, func() tea.Msg {
				wfs, err := a.client.ListWorkflows(ctx, repo)
				return scheduleWorkflowsMsg{repo, wfs, err}
			})
		}
		if due(stateIn(d.runsState, repo)) {
			cmds = append(cmds, func() tea.Msg {
				runs, _, err := a.client.ListRuns(ctx, repo, scheduledRunsFilter)
				return scheduleRunsMsg{repo, runs, err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (s *store) gotSchedules(a *App, msg tea.Msg) (tea.Cmd, bool) {
	d := &s.sched
	switch m := msg.(type) {
	case scheduleWorkflowsMsg:
		stateIn(d.workflowsState, m.repo).end(m.err)
		if m.err == nil {
			d.workflows[m.repo] = m.workflows
		}
		return nil, true
	case scheduleRunsMsg:
		stateIn(d.runsState, m.repo).end(m.err)
		if m.err != nil {
			return nil, true
		}
		for _, sc := range s.schedules {
			if strings.EqualFold(sc.Repo, m.repo) {
				delete(d.runs, scheduleKey(sc.Repo, sc.File))
			}
		}
		for i, r := range m.runs {
			key := scheduleKey(m.repo, r.Path)
			if old, ok := d.runs[key]; !ok || r.CreatedAt.After(old.CreatedAt) {
				d.runs[key] = &m.runs[i]
			}
		}
		return nil, true
	}
	return nil, false
}

// scheduledView is the Scheduled view of the Actions tab: every workflow
// with a schedule trigger, how its latest scheduled run went, and the ones
// that no longer run.
type scheduledView struct {
	t    *Table
	list []schedule
}

func newScheduledView() *scheduledView {
	return &scheduledView{t: newTable(
		Column{Title: "STATUS", Help: "how its latest scheduled run went, or disabled by GitHub when the repository saw no activity for 60 days"},
		Column{Title: "REPO", Max: 30, Flex: true, MidCut: true},
		Column{Title: "WORKFLOW", Max: 30, Flex: true, Help: "the workflow file"},
		Column{Title: "SCHEDULE", Max: 36, Flex: true, Drop: 1, Help: "when it runs, in UTC; the panel shows the cron line"},
		Column{Title: "LAST RUN", Right: true, Min: 8, Help: "how long ago it last ran on its schedule"},
	)}
}

func (v *scheduledView) table() *Table    { return v.t }
func (v *scheduledView) count(a *App) int { return len(v.list) }
func (v *scheduledView) listHead(a *App) string {
	return a.filterLine(v.t.Filter(), v.FilterInfo(a))
}
func (v *scheduledView) SetFilter(a *App, q string)      { v.t.SetFilter(q, a.me) }
func (v *scheduledView) FilterValue() string             { return v.t.Filter() }
func (v *scheduledView) FilterInfo(a *App) filterInfo    { return v.t.Info() }
func (v *scheduledView) load(a *App, force bool) tea.Cmd { return a.loadSchedules(force) }

func (v *scheduledView) busy(a *App) bool {
	if a.st.usesState.busy {
		return true
	}
	for _, l := range a.st.sched.workflowsState {
		if l.busy {
			return true
		}
	}
	for _, l := range a.st.sched.runsState {
		if l.busy {
			return true
		}
	}
	return false
}

// scheduleOrder puts what needs you first: switched off, then failed, then the rest.
func scheduleOrder(s schedule) int {
	switch {
	case s.disabled():
		return 0
	case s.kind() == KFail:
		return 1
	case s.last == nil:
		return 2
	}
	return 3
}

func (v *scheduledView) sync(a *App) {
	v.list = a.schedules()
	sort.SliceStable(v.list, func(i, j int) bool {
		if oi, oj := scheduleOrder(v.list[i]), scheduleOrder(v.list[j]); oi != oj {
			return oi < oj
		}
		return strings.ToLower(v.list[i].Repo+v.list[i].File) < strings.ToLower(v.list[j].Repo+v.list[j].File)
	})
	now := time.Now()
	var rows []Row
	for _, s := range v.list {
		rows = append(rows, v.row(a, s, now))
	}
	v.t.Empty = "None of your repos has a workflow that runs on a schedule."
	switch {
	case !a.st.scheduled && a.st.usesState.busy:
		v.t.Empty = "Reading the workflow files of your repos…"
	case !a.st.scheduled:
		v.t.Empty = "The workflow files are not read yet; ctrl+r reads them."
	}
	v.t.SetRows(rows)
}

func (v *scheduledView) row(a *App, s schedule, now time.Time) Row {
	k := s.kind()
	status := statusCell(k, a.frame, "")
	switch {
	case s.byGitHub():
		status = statusCell(KWait, 0, "disabled by GitHub")
	case s.disabled():
		status = statusCell(KWait, 0, "disabled")
	case s.last == nil && s.read:
		status = statusCell(KNone, 0, "never ran")
	case s.last == nil:
		status = statusCell(KNone, 0, "…")
	}
	last := Cell{}
	if s.last != nil {
		last = steelCell(age(s.last.CreatedAt, now)+" ago", false)
	}
	return Row{Key: scheduleKey(s.Repo, s.File), Kind: k, Cells: []Cell{
		status,
		iconText("repo", a.short(s.Repo), false),
		iconText("workflow", s.name(), false),
		iconText("calendar", cronsWords(s.Crons), false),
		last,
	}}
}

func (v *scheduledView) selected() (schedule, bool) {
	r := v.t.Selected()
	if r == nil {
		return schedule{}, false
	}
	for _, s := range v.list {
		if scheduleKey(s.Repo, s.File) == r.Key {
			return s, true
		}
	}
	return schedule{}, false
}

func (v *scheduledView) chips(a *App) []string {
	off, failing := 0, 0
	for _, s := range v.list {
		switch {
		case s.disabled():
			off++
		case s.kind() == KFail:
			failing++
		}
	}
	var chips []string
	if failing > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failing", failing)))
	}
	if off > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d disabled", off)))
	}
	return chips
}

func (v *scheduledView) coverage(a *App) string {
	s := fmt.Sprintf("workflows with a schedule trigger in %s, read from their files", repoCount(a.st.usesRepos))
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

func (v *scheduledView) keys(a *App) []Binding {
	s, ok := v.selected()
	if !ok {
		return nil
	}
	var bs []Binding
	if s.last != nil {
		run := *s.last
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, s.Repo, run)) })})
	}
	if s.workflow != nil {
		wf := *s.workflow
		bs = append(bs, Binding{Keys: []string{"h"}, Desc: "runs", Help: "the runs of the workflow",
			Do: do(func() tea.Cmd { return a.openRepo(s.Repo, "", wf.ID) })})
		verb := "disable"
		if wf.State != "active" {
			verb = "enable"
		}
		bs = append(bs, Binding{Keys: []string{"E"}, Desc: verb, Help: "enable / disable the workflow", Mutates: true, Need: gh.CapWrite, Repo: s.Repo,
			Do: do(func() tea.Cmd { return a.toggleWorkflow(s.Repo, wf) })})
	}
	bs = append(bs,
		Binding{Keys: []string{"c"}, Desc: "workflow file", Help: "the YAML of the workflow", Do: do(func() tea.Cmd { return a.push(newWorkflowSourceView(a, s.Repo, s.File, "")) })},
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open the workflow in the browser",
			Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(s.Repo + "/actions/workflows/" + pathBase(s.File))) })})
	return bs
}

// toggleWorkflow asks, then enables a disabled workflow or disables an active one.
func (a *App) toggleWorkflow(repo string, w gh.Workflow) tea.Cmd {
	enable := w.State != "active"
	verb := map[bool]string{true: "Enable", false: "Disable"}[enable]
	return confirm(verb+" workflow?", []string{
		sWhite.Render(w.Name) + sDim.Render("  "+w.Path),
		"",
		map[bool]string{true: "New runs will be triggered again.", false: "No new runs will start until you enable it again."}[enable],
	}, verb, !enable, func() tea.Cmd {
		return a.act(repo, verb+"d "+w.Name, func(ctx context.Context) error {
			return a.client.SetWorkflowEnabled(ctx, repo, w.ID, enable)
		})
	})
}

func (v *scheduledView) panel(a *App, w, h int) []string {
	s, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "Nothing selected.")
	}
	now := time.Now()
	p := newLayerPanel(w)
	p.add(icoIn("workflow", s.kind().Color()) + sWhite.Bold(true).Render(s.name()))
	p.add(sDim.Render("Scheduled workflow · " + s.Repo + " · " + s.File))
	p.add("")
	status, k := "Runs on its schedule", KOK
	switch {
	case s.byGitHub():
		status, k = "Disabled by GitHub", KWait
	case s.disabled():
		status, k = "Disabled", KWait
	case s.last == nil && s.read:
		status, k = "Never ran on its schedule", KNone
	case s.last == nil:
		status, k = "Reading…", KNone
	case StatusKind(s.last.Status, s.last.Conclusion) == KFail:
		status, k = "Last scheduled run failed", KFail
	}
	p.field("Status", fg(k.Color()).Render(status))
	for i, c := range s.Crons {
		label := ""
		if i == 0 {
			label = "Schedule"
		}
		p.field(label, sText.Render(cronWords(c))+sDim.Render("  "+c))
	}
	if s.last != nil {
		lk := StatusKind(s.last.Status, s.last.Conclusion)
		p.field("Last run", glyph(lk, a.frame)+" "+sText.Render(fmt.Sprintf("#%d, %s", s.last.RunNumber, agoWords(s.last.CreatedAt, now)))+sDim.Render(" on ")+fact("branch", s.last.HeadBranch))
	}
	p.label("Next step")
	switch {
	case s.byGitHub():
		p.callout(cWait, "GitHub disabled it: nothing happened in the repository for 60 days, so its schedule stopped.", keycap("E")+sText.Render(" enable it again"))
	case s.disabled():
		p.callout(cWait, "Someone disabled it, so its schedule doesn't run.", keycap("E")+sText.Render(" enable it"))
	case s.kind() == KFail:
		p.callout(cRed, "Its last scheduled run failed.", keycap("enter")+sText.Render(" open the run"))
	case s.last == nil && s.read:
		p.callout(cFrame, "No scheduled run so far: check the cron, and that the file is on the default branch.", keycap("c")+sText.Render(" the workflow file"))
	default:
		p.add(sDim.Render("Runs on its schedule."))
	}
	return p.render(h)
}

// refreshRepo reads the workflows of repo again, after one was enabled or disabled.
func (v *scheduledView) refreshRepo(a *App, repo string) tea.Cmd {
	stateIn(a.st.sched.workflowsState, repo).at = time.Time{}
	stateIn(a.st.sched.workflowsState, repo).tried = time.Time{}
	return a.loadSchedules(false)
}

// cronsWords says when a workflow runs, for each of its cron lines.
func cronsWords(crons []string) string {
	var out []string
	for _, c := range crons {
		out = append(out, cronWords(c))
	}
	return strings.Join(out, " · ")
}

// cronWords says a cron line in words when it is a common one: "Daily at
// 03:00", "Mondays at 04:00", "Every 30 minutes", "Hourly at :15". Any other
// line is returned as it is.
func cronWords(cron string) string {
	f := strings.Fields(cron)
	if len(f) != 5 {
		return cron
	}
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]
	num := func(s string, hi int) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= 0 && n <= hi
	}
	if dom != "*" || month != "*" {
		return cron
	}
	if every, ok := strings.CutPrefix(minute, "*/"); ok && hour == "*" && dow == "*" {
		if n, ok := num(every, 59); ok && n > 0 {
			return fmt.Sprintf("Every %d minutes", n)
		}
		return cron
	}
	m, okM := num(minute, 59)
	if !okM {
		return cron
	}
	if hour == "*" && dow == "*" {
		return fmt.Sprintf("Hourly at :%02d", m)
	}
	h, okH := num(hour, 23)
	if !okH {
		return cron
	}
	at := fmt.Sprintf("%02d:%02d", h, m)
	days := map[string]string{"0": "Sundays", "1": "Mondays", "2": "Tuesdays", "3": "Wednesdays", "4": "Thursdays", "5": "Fridays", "6": "Saturdays", "7": "Sundays",
		"1-5": "Weekdays", "MON-FRI": "Weekdays", "SUN": "Sundays", "MON": "Mondays", "TUE": "Tuesdays", "WED": "Wednesdays", "THU": "Thursdays", "FRI": "Fridays", "SAT": "Saturdays"}
	switch {
	case dow == "*":
		return "Daily at " + at
	case days[strings.ToUpper(dow)] != "":
		return days[strings.ToUpper(dow)] + " at " + at
	}
	return cron
}
