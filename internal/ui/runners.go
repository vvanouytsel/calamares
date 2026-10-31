package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runnersStale is how old the list of runners may get before showing the
// Runners view reads it again.
const runnersStale = 2 * time.Minute

// maxRepoRunnerReads caps how many repos the Runners view asks for their own
// runners: only admins may, one request each.
const maxRepoRunnerReads = 20

type placedRunner struct {
	gh.Runner
	where string // the organization, or the repo the runner belongs to
}

type runnerData struct {
	org      []gh.Runner
	orgState loadState
	orgOf    string     // the organization the runners are of
	orgLock  *gh.Denial // why GitHub won't list them
	repo     map[string][]gh.Runner
	state    map[string]*loadState
	jobs     map[int64][]gh.Job // the jobs of runs that wait or run, by run
	jobState map[int64]*loadState
	// history is, by job, the jobs of the last day on runners that are gone.
	history map[int64]pastJob
}

func newRunnerData() runnerData {
	return runnerData{repo: map[string][]gh.Runner{}, state: map[string]*loadState{}, jobs: map[int64][]gh.Job{}, jobState: map[int64]*loadState{},
		history: map[int64]pastJob{}}
}

type orgRunnersMsg struct {
	org     string
	runners []gh.Runner
	err     error
}

type repoRunnersMsg struct {
	repo    string
	runners []gh.Runner
	err     error
}

type activeJobsMsg struct {
	runID int64
	jobs  []gh.Job
	err   error
}

// runnersOrg is the organization whose runners the view lists: the owner of
// most of your repos, unless that is you.
func (a *App) runnersOrg() string {
	o := a.owner()
	if o == "" || strings.EqualFold(o, a.me) {
		return ""
	}
	return o
}

func (s *store) gotRunners(a *App, msg tea.Msg) (tea.Cmd, bool) {
	d := &s.runners
	switch m := msg.(type) {
	case orgRunnersMsg:
		d.orgState.end(m.err)
		d.orgOf = m.org
		d.orgLock = nil
		if m.err != nil {
			lock := runnersDenial(m.org, m.err)
			d.orgLock, d.org = &lock, nil
			return nil, true
		}
		d.org = m.runners
		return nil, true
	case repoRunnersMsg:
		stateIn(d.state, m.repo).end(m.err)
		if m.err == nil {
			d.repo[m.repo] = m.runners
		}
		return nil, true
	case activeJobsMsg:
		l, ok := d.jobState[m.runID]
		if ok {
			l.end(m.err)
		}
		if m.err == nil {
			d.jobs[m.runID] = m.jobs
		}
		return nil, true
	}
	return nil, false
}

// runnersDenial says why GitHub won't list an organization's runners.
func runnersDenial(org string, err error) gh.Denial {
	if d, ok := gh.AsDenial(err); ok && d.URL != "" {
		return d
	}
	if gh.IsAccessDenied(err) || gh.IsNotFound(err) {
		return gh.Denial{Reason: "only owners of " + org + " may list its runners", Fix: "ask an owner of " + org}
	}
	return gh.Denial{Reason: "Could not read the runners of " + org + ": " + err.Error()}
}

// labelSet is the labels a waiting job asks for, as one key.
func labelSet(labels []string) string {
	l := make([]string, len(labels))
	for i, x := range labels {
		l[i] = strings.ToLower(x)
	}
	sort.Strings(l)
	return strings.Join(l, ",")
}

// githubHosted says a job asks for a runner GitHub provides.
func githubHosted(labels []string) bool {
	if len(labels) == 0 {
		return false
	}
	for _, l := range labels {
		l = strings.ToLower(l)
		if l == "self-hosted" {
			return false
		}
	}
	l := strings.ToLower(labels[0])
	return strings.HasPrefix(l, "ubuntu-") || strings.HasPrefix(l, "windows-") || strings.HasPrefix(l, "macos-")
}

// hasLabels says have holds every label of want.
func hasLabels(have, want []string) bool {
	for _, w := range want {
		if !containsFold(have, w) {
			return false
		}
	}
	return true
}

// waitGroup is the jobs waiting for a runner with the same labels.
type waitGroup struct {
	labels []string
	jobs   []waitingJob
}

type waitingJob struct {
	repo string
	run  gh.Run
	job  gh.Job
}

func (g waitGroup) longest(now time.Time) time.Duration {
	var d time.Duration
	for _, j := range g.jobs {
		d = max(d, j.job.Queued(now))
	}
	return d
}

// runnerUse is a job that ran, or runs, on a runner.
type runnerUse struct {
	repo string
	run  gh.Run
	job  gh.Job
}

func (u runnerUse) running() bool { return u.job.Status == "in_progress" }

// at is when the job finished, or started while it runs.
func (u runnerUse) at() time.Time {
	if u.job.CompletedAt.IsZero() {
		return u.job.StartedAt
	}
	return u.job.CompletedAt
}

// runnerInfo is a self-hosted runner as GitHub lists it, as the jobs we hold
// name it, or both.
type runnerInfo struct {
	name   string
	listed *placedRunner
	uses   []runnerUse // newest first
	asked  []string    // the labels the jobs on it asked for
}

func (r runnerInfo) labels() []string {
	if r.listed != nil {
		return r.listed.LabelNames()
	}
	return r.asked
}

// now is the job the runner runs, as far as we know.
func (r runnerInfo) now() *runnerUse {
	for i := range r.uses {
		if r.uses[i].running() {
			return &r.uses[i]
		}
	}
	return nil
}

// last is the newest job that finished on the runner.
func (r runnerInfo) last() *runnerUse {
	for i := range r.uses {
		if !r.uses[i].running() {
			return &r.uses[i]
		}
	}
	return nil
}

// state is how the runner is: from GitHub when it lists it, else guessed from
// the jobs, which can't tell idle from offline.
func (r runnerInfo) state() (Kind, string) {
	switch {
	case r.listed != nil && r.listed.Status != "online":
		return KSkip, "offline"
	case r.listed != nil && r.listed.Busy, r.now() != nil:
		return KRun, "busy"
	case r.listed != nil:
		return KOK, "idle"
	}
	return KNone, "idle?"
}

func (r runnerInfo) order() int {
	k, _ := r.state()
	switch k {
	case KRun:
		return 0
	case KOK:
		return 1
	case KNone:
		return 2
	}
	return 3
}

// hostedGroup is the GitHub-hosted runner name every job on one gets.
const hostedGroup = "GitHub Actions"

func hostedJob(j gh.Job) bool {
	return j.RunnerGroup == hostedGroup || (j.RunnerGroup == "" && githubHosted(j.Labels))
}

// runnersView is the Runners view of the Actions tab: the jobs waiting for a
// runner, by the labels they ask for, and the self-hosted runners with what
// they run: those GitHub lists to you and those the jobs we hold ran on.
type runnersView struct {
	t        *Table
	waits    []waitGroup
	runners  []runnerInfo
	past     []runnerUse // the history: jobs on runners that are gone, newest first
	open     bool        // the history is opened to its jobs
	hosted   []runnerUse // jobs running on GitHub's runners
	listedOK bool        // GitHub listed the organization's runners to you
	repos    func(a *App) []string
	recent   func() []repoRun
}

func newRunnersView(repos func(a *App) []string, recent func() []repoRun) *runnersView {
	return &runnersView{repos: repos, recent: recent, t: newTable(
		Column{Title: "RUNNER", Max: 32, Flex: true},
		Column{Title: "STATE", Min: 8, Help: "idle, busy or offline; for jobs that wait, how many wait and for how long"},
		Column{Title: "CURRENT JOB", Max: 60, Flex: true, Help: "the job it runs now, or the last one it ran"},
		Column{Title: "LABELS", Max: 40, Flex: true, Drop: 1, Help: "the labels a job asks for to run on it"},
		Column{Title: "OWNER", Max: 24, Flex: true, Drop: 2, Help: "whose runner it is: the organization's or a repo's"},
	)}
}

func (v *runnersView) table() *Table                { return v.t }
func (v *runnersView) count(a *App) int             { return len(v.runners) }
func (v *runnersView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *runnersView) FilterValue() string          { return v.t.Filter() }
func (v *runnersView) FilterInfo(a *App) filterInfo { return v.t.Info() }

// adminRepos are the repos of the tab you are an admin of, whose own runners
// you may list.
func (v *runnersView) adminRepos(a *App) []string {
	var out []string
	for _, repo := range v.repos(a) {
		if r, ok := a.repoByName(repo); ok && strings.EqualFold(r.Permission, "ADMIN") && len(out) < maxRepoRunnerReads {
			out = append(out, repo)
		}
	}
	return out
}

func (v *runnersView) load(a *App, force bool) tea.Cmd {
	d := &a.st.runners
	ctx := gh.Background(context.Background())
	var cmds []tea.Cmd
	if org := a.runnersOrg(); org != "" && (force || time.Since(d.orgState.tried) > runnersStale) && d.orgState.begin() {
		cmds = append(cmds, func() tea.Msg {
			rs, err := a.client.OrgRunners(ctx, org)
			return orgRunnersMsg{org, rs, err}
		})
	}
	for _, repo := range v.adminRepos(a) {
		l := stateIn(d.state, repo)
		if (force || time.Since(l.tried) > runnersStale) && l.begin() {
			cmds = append(cmds, func() tea.Msg {
				rs, err := a.client.RepoRunners(ctx, repo)
				return repoRunnersMsg{repo, rs, err}
			})
		}
	}
	for _, x := range v.recent() {
		if !x.run.Active() {
			continue
		}
		l, ok := d.jobState[x.run.ID]
		if !ok {
			l = &loadState{}
			d.jobState[x.run.ID] = l
		}
		if (force || time.Since(l.tried) > activeEvery) && l.begin() {
			cmds = append(cmds, func() tea.Msg {
				jobs, err := a.client.ListJobs(ctx, x.repo, x.run.ID)
				return activeJobsMsg{x.run.ID, jobs, err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (v *runnersView) busy(a *App) bool {
	d := &a.st.runners
	if d.orgState.busy {
		return true
	}
	for _, l := range d.state {
		if l.busy {
			return true
		}
	}
	return false
}

// jobsOf are the jobs we hold of run: read for this view while it runs,
// otherwise from the cache. Nothing is read for it.
func (a *App) jobsOf(x repoRun) []gh.Job {
	if jobs, ok := a.st.runners.jobs[x.run.ID]; ok {
		return jobs
	}
	jobs, _ := a.client.CachedJobs(x.repo, x.run.ID)
	return jobs
}

func (v *runnersView) sync(a *App) {
	d := &a.st.runners
	byName := map[string]*runnerInfo{}
	var order []string
	info := func(name string) *runnerInfo {
		key := strings.ToLower(name)
		r, ok := byName[key]
		if !ok {
			r = &runnerInfo{name: name}
			byName[key] = r
			order = append(order, key)
		}
		return r
	}
	v.listedOK = strings.EqualFold(d.orgOf, a.runnersOrg()) && d.orgLock == nil && a.runnersOrg() != ""
	if strings.EqualFold(d.orgOf, a.runnersOrg()) {
		for _, r := range d.org {
			info(r.Name).listed = &placedRunner{r, d.orgOf}
		}
	}
	for _, repo := range v.adminRepos(a) {
		for _, r := range d.repo[repo] {
			info(r.Name).listed = &placedRunner{r, repo}
		}
	}

	byLabels := map[string]*waitGroup{}
	var waitOrder []string
	v.hosted = nil
	for _, x := range v.recent() {
		for _, j := range a.jobsOf(x) {
			if x.run.Active() && (j.Status == "queued" || j.Status == "waiting") {
				key := labelSet(j.Labels)
				g, ok := byLabels[key]
				if !ok {
					g = &waitGroup{labels: j.Labels}
					byLabels[key] = g
					waitOrder = append(waitOrder, key)
				}
				g.jobs = append(g.jobs, waitingJob{x.repo, x.run, j})
				continue
			}
			if j.RunnerName == "" || j.StartedAt.IsZero() {
				continue
			}
			use := runnerUse{x.repo, x.run, j}
			if hostedJob(j) {
				if use.running() {
					v.hosted = append(v.hosted, use)
				}
				continue
			}
			r := info(j.RunnerName)
			r.uses = append(r.uses, use)
			for _, l := range j.Labels {
				if !containsFold(r.asked, l) {
					r.asked = append(r.asked, l)
				}
			}
		}
	}
	v.runners = nil
	var gone []runnerUse
	for _, key := range order {
		r := byName[key]
		sort.SliceStable(r.uses, func(i, j int) bool { return r.uses[i].at().After(r.uses[j].at()) })
		if r.listed == nil && r.now() == nil {
			gone = append(gone, r.uses...)
			continue
		}
		v.runners = append(v.runners, *r)
	}
	now := time.Now()
	a.keepPast(gone, now)
	v.past = a.pastJobs()
	sort.SliceStable(v.runners, func(i, j int) bool {
		if oi, oj := v.runners[i].order(), v.runners[j].order(); oi != oj {
			return oi < oj
		}
		return strings.ToLower(v.runners[i].name) < strings.ToLower(v.runners[j].name)
	})

	v.waits = nil
	for _, key := range waitOrder {
		v.waits = append(v.waits, *byLabels[key])
	}
	sort.SliceStable(v.waits, func(i, j int) bool { return v.waits[i].longest(now) > v.waits[j].longest(now) })

	var rows []Row
	for i, g := range v.waits {
		rows = append(rows, v.waitRow(a, i, g, now))
	}
	for _, r := range v.runners {
		rows = append(rows, v.runnerRow(a, r, now))
	}
	if len(v.hosted) > 0 {
		rows = append(rows, v.hostedRow(a))
	}
	if len(v.past) > 0 {
		// A filter looks through the history too.
		open := v.open || v.t.Filter() != ""
		rows = append(rows, v.historyRow(a, v.past, open, now))
		for i, u := range v.past {
			if !open {
				break
			}
			rows = append(rows, v.pastRow(a, i, u, i == len(v.past)-1, now))
		}
	}
	v.t.Empty = "No job waits for a runner, and no self-hosted runner shows in the jobs loaded."
	v.t.SetRows(rows)
}

// idleFor counts the runners that carry the labels, and those of them known
// to be idle.
func (v *runnersView) idleFor(labels []string) (idle, any int) {
	for _, r := range v.runners {
		if !hasLabels(r.labels(), labels) {
			continue
		}
		any++
		if k, _ := r.state(); k == KOK {
			idle++
		}
	}
	return idle, any
}

// waitVerdict says, for jobs waiting on labels, whether a runner can take them.
func (v *runnersView) waitVerdict(a *App, labels []string) (string, Kind) {
	idle, any := v.idleFor(labels)
	switch {
	case githubHosted(labels):
		return "GitHub-hosted", KQueue
	case any == 0 && !v.listedOK:
		return "no runner seen with these labels", KWait
	case any == 0:
		return "no runner has these labels", KFail
	case idle == 0 && !v.listedOK:
		return fmt.Sprintf("%d seen, none known to be idle", any), KWait
	case idle == 0:
		return fmt.Sprintf("all %d busy or offline", any), KWait
	}
	return fmt.Sprintf("%d idle of %d", idle, any), KQueue
}

func (v *runnersView) waitRow(a *App, i int, g waitGroup, now time.Time) Row {
	verdict, k := v.waitVerdict(a, g.labels)
	labels := strings.Join(g.labels, ", ")
	state := fmt.Sprintf("%d waiting · %s", len(g.jobs), dur(g.longest(now)))
	return Row{Key: fmt.Sprintf("wait\x00%d", i), Kind: k, Cells: []Cell{
		{Text: KQueue.Glyph(a.frame) + " " + labels, Styled: fg(KQueue.Color()).Render(KQueue.Glyph(a.frame)) + " " + sWhite.Bold(true).Render(labels)},
		{Text: state, Styled: sAmber.Render(state)},
		{Text: verdict, Styled: fg(k.Color()).Render(verdict)},
		{}, {},
	}}
}

// useText is "webshop › CI #128 › build" with how long it ran, or how long ago.
func (a *App) useText(u runnerUse, now time.Time) Cell {
	where := a.short(u.repo) + " › " + u.run.Name + fmt.Sprintf(" #%d", u.run.RunNumber) + " › " + u.job.Name
	if u.running() {
		d := dur(u.job.Duration(now))
		return Cell{Text: where + "  " + d, Styled: ico("repo") + sText.Render(where) + "  " + sAmber.Render(d)}
	}
	when := "last: " + where + " · " + age(u.at(), now) + " ago"
	return Cell{Text: when, Styled: sDim.Render(when)}
}

func (v *runnersView) runnerRow(a *App, r runnerInfo, now time.Time) Row {
	k, state := r.state()
	quiet := k == KSkip
	use := Cell{}
	switch {
	case r.now() != nil:
		use = a.useText(*r.now(), now)
	case r.last() != nil:
		use = a.useText(*r.last(), now)
	}
	of := Cell{}
	if r.listed != nil {
		of = iconText("repo", a.short(r.listed.where), quiet)
	}
	status := statusCell(k, a.frame, state)
	if k == KNone {
		status = Cell{Text: "○ " + state, Styled: sDim.Render("○ " + state)}
	}
	return Row{Key: "runner\x00" + strings.ToLower(r.name), Kind: k, Cells: []Cell{
		iconText("server", r.name, quiet),
		status,
		use,
		steelCell(strings.Join(r.labels(), ", "), quiet),
		of,
	}}
}

// hostedRow sums up the jobs running on GitHub's runners, by image.
func (v *runnersView) hostedRow(a *App) Row {
	count := map[string]int{}
	var images []string
	for _, u := range v.hosted {
		img := "github-hosted"
		if len(u.job.Labels) > 0 {
			img = u.job.Labels[0]
		}
		if count[img] == 0 {
			images = append(images, img)
		}
		count[img]++
	}
	var parts []string
	for _, img := range images {
		parts = append(parts, fmt.Sprintf("%s ×%d", img, count[img]))
	}
	state := fmt.Sprintf("%d %s", len(v.hosted), plural("job", len(v.hosted)))
	return Row{Key: "hosted", Kind: KRun, Cells: []Cell{
		{Text: "GitHub-hosted", Styled: icoIn("server", cSteel) + sDim.Render("GitHub-hosted")},
		{Text: state, Styled: fg(KRun.Color()).Render(KRun.Glyph(a.frame)) + " " + sAmber.Render(state)},
		steelCell(strings.Join(parts, " · "), false),
		{}, {},
	}}
}

// selectedPast is the job of the history under the cursor, and whether the
// cursor is on the history's own line.
func (v *runnersView) selectedPast() (*runnerUse, bool) {
	r := v.t.Selected()
	if r == nil {
		return nil, false
	}
	if r.Key == historyKey {
		return nil, true
	}
	if i, ok := strings.CutPrefix(r.Key, "past\x00"); ok {
		var n int
		fmt.Sscan(i, &n)
		if n < len(v.past) {
			return &v.past[n], false
		}
	}
	return nil, false
}

func (v *runnersView) selected() (*waitGroup, *runnerInfo, bool) {
	r := v.t.Selected()
	if r == nil {
		return nil, nil, false
	}
	if r.Key == "hosted" {
		return nil, nil, true
	}
	if i, ok := strings.CutPrefix(r.Key, "wait\x00"); ok {
		var n int
		fmt.Sscan(i, &n)
		if n < len(v.waits) {
			return &v.waits[n], nil, false
		}
		return nil, nil, false
	}
	for i := range v.runners {
		if "runner\x00"+strings.ToLower(v.runners[i].name) == r.Key {
			return nil, &v.runners[i], false
		}
	}
	return nil, nil, false
}

func (v *runnersView) chips(a *App) []string {
	waiting, stuck, offline, busy := 0, 0, 0, 0
	for _, g := range v.waits {
		waiting += len(g.jobs)
		if _, k := v.waitVerdict(a, g.labels); k == KFail {
			stuck++
		}
	}
	for _, r := range v.runners {
		switch k, _ := r.state(); k {
		case KSkip:
			offline++
		case KRun:
			busy++
		}
	}
	var chips []string
	if busy > 0 {
		chips = append(chips, statusChip(KRun, a.frame, fmt.Sprintf("%d busy", busy)))
	}
	if waiting > 0 {
		chips = append(chips, statusChip(KQueue, a.frame, fmt.Sprintf("%d %s waiting", waiting, plural("job", waiting))))
	}
	if stuck > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d without a runner", stuck)))
	}
	if offline > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d offline", offline)))
	}
	return chips
}

func (v *runnersView) coverage(a *App) string {
	parts := []string{"seen in the jobs of " + repoCount(len(v.repos(a)))}
	if v.listedOK {
		parts = append(parts, "runners of "+a.runnersOrg())
	}
	if n := len(v.adminRepos(a)); n > 0 {
		parts = append(parts, fmt.Sprintf("of %s you're an admin of", repoCount(n)))
	}
	s := strings.Join(parts, " · ")
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

// listHead says, when it does, why the organization's runners are missing.
func (v *runnersView) listHead(a *App) string {
	head := a.filterLine(v.t.Filter(), v.FilterInfo(a))
	if d := a.st.runners.orgLock; d != nil {
		head += "   " + lockMark() + sWait.Render(d.Reason)
		if d.Fix != "" {
			head += sDim.Render(" · " + d.Fix)
		}
	}
	return head
}

// shownUse is the job the keys act on: what the runner runs, else its last job.
func (r runnerInfo) shownUse() *runnerUse {
	if u := r.now(); u != nil {
		return u
	}
	return r.last()
}

func (v *runnersView) keys(a *App) []Binding {
	g, r, hosted := v.selected()
	var u *runnerUse
	switch {
	case g != nil:
		j := g.jobs[0]
		u = &runnerUse{j.repo, j.run, j.job}
	case r != nil:
		u = r.shownUse()
	case hosted && len(v.hosted) > 0:
		u = &v.hosted[0]
	}
	past, onHistory := v.selectedPast()
	if past != nil {
		u = past
	}
	var bs []Binding
	if u != nil {
		use := *u
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run", Help: "the run of the job it runs, or ran last",
				Do: do(func() tea.Cmd { return a.push(newRunView(a, use.repo, use.run)) })},
			Binding{Keys: []string{"l"}, Desc: "logs", Help: "the log of that job",
				Do: do(func() tea.Cmd { return a.push(newLogView(a, use.repo, use.run, &use.job, 0)) })})
	}
	if r != nil && r.listed != nil {
		where := r.listed.where
		url := "organizations/" + where + "/settings/actions/runners"
		if strings.Contains(where, "/") {
			url = where + "/settings/actions/runners"
		}
		bs = append(bs, Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open the runners in the browser",
			Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(url)) })})
	}
	if len(v.past) > 0 {
		desc := "history"
		if onHistory || past != nil {
			desc = "open jobs"
			if v.open {
				desc = "close jobs"
			}
		}
		bs = append(bs,
			Binding{Keys: []string{" "}, Label: "space", Desc: desc, Help: "open / close the jobs of the runners that are gone",
				Do: do(func() tea.Cmd { v.toggleHistory(); return nil })},
			Binding{Keys: []string{"X"}, Desc: "clear history", Help: "forget which runner that is gone ran which job",
				Do: do(func() tea.Cmd { return a.clearHistory() })})
	}
	return bs
}

// toggleHistory opens or closes the history and leaves the cursor on its line.
func (v *runnersView) toggleHistory() {
	v.open = !v.open
	for i := range v.t.Len() {
		v.t.SetCursor(i)
		if r := v.t.Selected(); r != nil && r.Key == historyKey {
			return
		}
	}
}

func (v *runnersView) panel(a *App, w, h int) []string {
	g, r, hosted := v.selected()
	now := time.Now()
	p := newLayerPanel(w)
	past, onHistory := v.selectedPast()
	switch {
	case past != nil:
		v.pastPanel(a, p, *past, now)
	case onHistory:
		v.historyPanel(a, p, now)
	case g != nil:
		verdict, k := v.waitVerdict(a, g.labels)
		title := g.jobs[0].job.Name
		if len(g.jobs) > 1 {
			title = fmt.Sprintf("%d jobs", len(g.jobs))
		}
		p.add(sWhite.Bold(true).Render(title))
		p.add(sDim.Render("Waiting for a runner"))
		p.add("")
		p.field("Labels", sText.Render(strings.Join(g.labels, ", ")))
		p.field("Waiting", sAmber.Render(dur(g.longest(now))))
		p.field("Runners", fg(k.Color()).Render(verdict))
		if k == KFail {
			p.add("")
			p.callout(cRed, "No runner carries all of these labels: a typo in runs-on, or a pool that was removed.")
		}
		p.label("Waiting jobs")
		for _, j := range g.jobs {
			p.row(ico("repo")+sText.Render(a.short(j.repo))+sDim.Render(" · "+j.job.Name), sAmber.Render(dur(j.job.Queued(now))))
		}
	case r != nil:
		v.runnerPanel(a, p, *r, now)
	case hosted:
		p.add(icoIn("server", cSteel) + sWhite.Bold(true).Render("GitHub-hosted"))
		p.add(sDim.Render("A new runner for every job; its name says nothing."))
		p.label("Current job")
		for _, u := range v.hosted {
			p.row(ico("repo")+sText.Render(a.short(u.repo)+" › "+u.run.Name+" › "+u.job.Name), sAmber.Render(dur(u.job.Duration(now))))
		}
	default:
		return emptyPanel(w, h, "Nothing selected.")
	}
	return p.render(h)
}

func (v *runnersView) runnerPanel(a *App, p *panelText, r runnerInfo, now time.Time) {
	k, state := r.state()
	p.add(icoIn("server", k.Color()) + sWhite.Bold(true).Render(r.name))
	line := statusChip(k, a.frame, state)
	switch {
	case r.listed == nil:
		line += sDim.Render(" · seen in your runs")
	case r.listed.OS != "":
		line += sDim.Render(" · " + r.listed.OS)
	}
	p.add(line)
	if r.listed != nil {
		p.add(fact("repo", a.short(r.listed.where)))
	}
	if u := r.now(); u != nil {
		p.label("Current job")
		p.add(ico("repo") + sText.Render(a.short(u.repo)+" › "+u.run.Name) + sGold.Render(fmt.Sprintf(" #%d", u.run.RunNumber)) + sText.Render(" › "+u.job.Name))
		p.add("  " + fact("branch", u.run.HeadBranch) + "  " + fact("user", u.run.Actor.Login) + "  " + sAmber.Render(dur(u.job.Duration(now))))
	}
	var done []runnerUse
	byRepo := map[string]int{}
	var repos []string
	for _, u := range r.uses {
		if !u.running() {
			done = append(done, u)
		}
		if byRepo[u.repo] == 0 {
			repos = append(repos, u.repo)
		}
		byRepo[u.repo]++
	}
	if len(done) > 0 {
		p.label("Recent jobs")
		for i, u := range done {
			if i == 6 {
				break
			}
			uk := jobKind(u.job)
			p.row(fg(uk.Color()).Render(uk.Glyph(0))+" "+sText.Render(a.short(u.repo)+" › "+u.run.Name+fmt.Sprintf(" #%d", u.run.RunNumber)+" › "+u.job.Name),
				steelCell(age(u.at(), now), false).Styled)
		}
	}
	if len(repos) > 0 {
		sort.SliceStable(repos, func(i, j int) bool { return byRepo[repos[i]] > byRepo[repos[j]] })
		var parts []string
		for _, repo := range repos {
			parts = append(parts, sText.Render(a.short(repo))+" "+sDim.Render(fmt.Sprint(byRepo[repo])))
		}
		p.label("Jobs per repository")
		p.add(strings.Join(parts, sDim.Render(" · ")))
	}
	p.label("Labels")
	p.add(sText.Render(strings.Join(r.labels(), ", ")))
	if r.listed == nil {
		p.add(sDim.Render("The labels its jobs asked for."))
	}
}
