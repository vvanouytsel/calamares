package ui

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// The two panes of the repo screen.
const (
	paneWorkflows = iota
	paneRuns
)

// statsFilter is the repo-wide run sample behind the sidebar's health
// numbers. It is the same request as the unfiltered "All runs" list.
var statsFilter = runsFilter(nil, "", false, "", "")

// workflowsEvery is how often the list of workflows is checked for changes.
const workflowsEvery = 5 * time.Minute

// repoView is one screen per repository: its workflows on the left and the
// runs of the selected workflow on the right, with a stats strip on top.
type repoView struct {
	id     int
	repo   string
	branch string // initial branch filter (e.g. from the current folder or a PR)
	wantWF int64  // select this workflow once workflows have loaded
	wfs    []gh.Workflow
	sample []gh.Run
	loaded bool
	busy   int
	last   time.Time
	sel    int // 0 = all runs, i+1 = wfs[i]
	focus  int
	panes  map[int64]*runsView
	selSeq int
	sideH  int
	offset int

	// The preview panel shows the jobs of the run under the cursor; they are
	// read once the cursor has rested on it.
	previewSeq   int
	previewAsked map[int64]bool

	// flaky holds, per run that passed on a rerun, the jobs that failed in
	// the attempt before.
	flaky      map[int64][]string
	flakyAsked map[int64]bool

	section int        // sectionActions, sectionPulls or sectionCommits
	pulls   *pullsView // the repo's pull requests, made when first shown
	commits *commitsView
	issues  *issuesView
	// branches, releases, environments and secrets are made when first shown.
	branches     *branchesView
	releases     *releasesView
	environments *environmentsView
	secrets      *secretsView
}

// repoSection is a section of the repo screen that is a list of its own.
type repoSection interface {
	View
	Filterer
	syncer
	Freshness
}

// extraSection is the list of the section on display when it is one of
// the newer ones, or nil.
func (v *repoView) extraSection(a *App) (repoSection, string, string) {
	switch v.section {
	case sectionBranches:
		if v.branches == nil {
			v.branches = newBranchesView(a, v.repo)
		}
		return v.branches, "Branches", "against the default branch"
	case sectionReleases:
		if v.releases == nil {
			v.releases = newReleasesView(a, v.repo)
		}
		return v.releases, "Releases", ""
	case sectionEnvironments:
		if v.environments == nil {
			v.environments = newEnvironmentsView(a, v.repo)
		}
		return v.environments, "Environments", ""
	case sectionSecrets:
		if v.secrets == nil {
			v.secrets = newSecretsView(a, v.repo)
		}
		return v.secrets, "Secrets", ""
	}
	return nil, "", ""
}

// The sections of the repo screen, switched with tab.
const (
	sectionActions = iota
	sectionPulls
	sectionIssues
	sectionCommits
	sectionBranches
	sectionReleases
	sectionEnvironments
	sectionSecrets
	sectionCount
)

type repoWfMsg struct {
	id  int
	wfs []gh.Workflow
	err error
}
type repoSampleMsg struct {
	id   int
	runs []gh.Run
}
type repoSelMsg struct{ id, seq int }

// repoPreviewMsg says the cursor rested on a run: read its jobs for the preview.
type repoPreviewMsg struct {
	id, seq int
	runID   int64
}

// repoJobsMsg says the jobs of a run were read; the preview finds them in the cache.
type repoJobsMsg struct{ id int }

func (m repoPreviewMsg) viewID() int { return m.id }
func (m repoJobsMsg) viewID() int    { return m.id }

// previewMinWidth is how wide the screen must be for the preview panel.
const previewMinWidth = 150

func (m repoWfMsg) viewID() int     { return m.id }
func (m repoSampleMsg) viewID() int { return m.id }
func (m repoSelMsg) viewID() int    { return m.id }

func newRepoView(a *App, repo, branch string) *repoView {
	return &repoView{id: a.newID(), repo: repo, branch: branch, panes: map[int64]*runsView{}, previewAsked: map[int64]bool{},
		flaky: map[int64][]string{}, flakyAsked: map[int64]bool{}}
}

func (v *repoView) ID() int          { return v.id }
func (v *repoView) RepoName() string { return v.repo }
func (v *repoView) Crumb() string    { return v.repo }

func (v *repoView) Children() []View {
	out := make([]View, 0, len(v.panes)+1)
	for _, p := range v.panes {
		out = append(out, p)
	}
	if v.pulls != nil {
		out = append(out, v.pulls)
	}
	if v.commits != nil {
		out = append(out, v.commits)
	}
	if v.issues != nil {
		out = append(out, v.issues)
	}
	if v.branches != nil {
		out = append(out, v.branches)
	}
	if v.releases != nil {
		out = append(out, v.releases)
	}
	if v.environments != nil {
		out = append(out, v.environments)
	}
	if v.secrets != nil {
		out = append(out, v.secrets)
	}
	return out
}

// pullsList returns the repo's pull request list, making it the first time.
func (v *repoView) pullsList(a *App) *pullsView {
	if v.pulls == nil {
		v.pulls = newPullsView(a, v.repo)
	}
	return v.pulls
}

// issuesList returns the repo's issue list, making it the first time.
func (v *repoView) issuesList(a *App) *issuesView {
	if v.issues == nil {
		v.issues = newIssuesView(a, v.repo)
	}
	return v.issues
}

// commitsList returns the repo's commits, making the list the first time.
func (v *repoView) commitsList(a *App) *commitsView {
	if v.commits == nil {
		v.commits = newCommitsView(a, v.repo, v.branch)
	}
	return v.commits
}

// setSection shows section i, loading it if its data is old.
func (v *repoView) setSection(a *App, i int) tea.Cmd {
	v.section = i
	if s, _, _ := v.extraSection(a); s != nil {
		if time.Since(s.Updated(a)) > staleAfter {
			return s.Refresh(a, true)
		}
		return nil
	}
	if i == sectionPulls {
		p := v.pullsList(a)
		if time.Since(p.at) > staleAfter {
			return p.Refresh(a, true)
		}
	}
	if i == sectionCommits {
		c := v.commitsList(a)
		if time.Since(c.at) > staleAfter {
			return c.Refresh(a, true)
		}
	}
	if i == sectionIssues {
		c := v.issuesList(a)
		if time.Since(c.at) > staleAfter {
			return c.Refresh(a, true)
		}
	}
	return nil
}

func (v *repoView) Updated(a *App) time.Time {
	if s, _, _ := v.extraSection(a); s != nil {
		return s.Updated(a)
	}
	switch v.section {
	case sectionPulls:
		return v.pullsList(a).at
	case sectionCommits:
		return v.commitsList(a).at
	case sectionIssues:
		return v.issuesList(a).at
	}
	p, _ := v.pane(a)
	return p.at
}

func (v *repoView) Loading(a *App) bool {
	if s, _, _ := v.extraSection(a); s != nil {
		return s.Loading(a)
	}
	switch v.section {
	case sectionPulls:
		return v.pullsList(a).busy
	case sectionCommits:
		return v.commitsList(a).busy
	case sectionIssues:
		return v.issuesList(a).busy
	}
	p, _ := v.pane(a)
	return p.busy
}

func (v *repoView) selectedWF() *gh.Workflow {
	if v.sel > 0 && v.sel <= len(v.wfs) {
		return &v.wfs[v.sel-1]
	}
	return nil
}

func (v *repoView) paneKey() int64 {
	if wf := v.selectedWF(); wf != nil {
		return wf.ID
	}
	return 0
}

// MenuTitle names what the action menu is about: the selected run, else the repo.
func (v *repoView) MenuTitle(a *App) string {
	if v.section == sectionActions && v.focus == paneRuns {
		p, _ := v.pane(a)
		return p.MenuTitle(a)
	}
	return a.short(v.repo)
}

// pane returns the runs pane of the selected workflow, creating it (seeded
// from the cache) the first time; created reports whether it is new.
func (v *repoView) pane(a *App) (p *runsView, created bool) {
	k := v.paneKey()
	if p, ok := v.panes[k]; ok {
		return p, false
	}
	p = newRunsView(a, v.repo, v.selectedWF(), v.branch)
	p.onBranch = func(branch string) { v.setBranch(p, branch) }
	p.onRuns = func() tea.Cmd { return tea.Batch(v.preview(a), v.loadFlaky(a)) }
	p.seed(a)
	v.panes[k] = p
	return p, true
}

// setBranch makes branch the filter for every workflow: panes other than
// from, which still show the old branch, are dropped and rebuilt on demand.
func (v *repoView) setBranch(from *runsView, branch string) {
	v.branch = branch
	for k, p := range v.panes {
		if p != from {
			delete(v.panes, k)
		}
	}
}

func (v *repoView) Init(a *App) tea.Cmd {
	if wfs, ok := a.client.CachedWorkflows(v.repo); ok {
		v.setWorkflows(a, wfs)
	}
	if runs, _, ok := a.client.CachedRuns(v.repo, statsFilter); ok {
		v.sample = runs
	}
	p, _ := v.pane(a)
	cmds := []tea.Cmd{p.load(a), v.load(a, context.Background())}
	// In a checkout on a branch of its own: find out whether it has a pull request.
	if a.inCheckout(v.repo) && a.opts.Branch != "" {
		cmds = append(cmds, v.pullsList(a).Refresh(a, false))
	}
	return tea.Batch(cmds...)
}

// sampleFromPane reports whether the open pane is the unfiltered "All runs"
// list, whose runs are then the sample, so it needn't be fetched twice.
func (v *repoView) sampleFromPane(p *runsView) bool {
	return p.wf == nil && p.branch == "" && !p.mine && p.status == ""
}

func (v *repoView) setWorkflows(a *App, wfs []gh.Workflow) {
	cur := v.paneKey()
	// Active workflows first, then by name as GitHub returns them.
	var active, off []gh.Workflow
	for _, w := range wfs {
		if w.State == "active" {
			active = append(active, w)
		} else {
			off = append(off, w)
		}
	}
	v.wfs = append(active, off...)
	v.loaded = true
	a.workflows[v.repo] = wfs
	want := cur
	if v.wantWF != 0 {
		want, v.wantWF = v.wantWF, 0
	}
	v.sel = 0
	for i, w := range v.wfs {
		if w.ID == want {
			v.sel = i + 1
		}
	}
}

// load refreshes the workflows and, unless the open pane already holds
// them, the repo-wide runs behind the health numbers. ctx decides the lane:
// foreground when you're waiting for it, background for refreshes.
func (v *repoView) load(a *App, ctx context.Context) tea.Cmd {
	v.last = time.Now()
	id, repo := v.id, v.repo
	cmds := []tea.Cmd{func() tea.Msg {
		wfs, err := a.client.ListWorkflows(ctx, repo)
		return repoWfMsg{id, wfs, err}
	}}
	if p, _ := v.pane(a); !v.sampleFromPane(p) {
		cmds = append(cmds, func() tea.Msg {
			runs, _, err := a.client.ListRuns(ctx, repo, statsFilter)
			if err != nil {
				return repoSampleMsg{id: id}
			}
			return repoSampleMsg{id, runs}
		})
	}
	v.busy = len(cmds)
	return tea.Batch(cmds...)
}

func (v *repoView) Refresh(a *App, force bool) tea.Cmd {
	if s, _, _ := v.extraSection(a); s != nil {
		return s.Refresh(a, force)
	}
	switch v.section {
	case sectionPulls:
		return v.pullsList(a).Refresh(a, force)
	case sectionCommits:
		return v.commitsList(a).Refresh(a, force)
	case sectionIssues:
		return v.issuesList(a).Refresh(a, force)
	}
	p, _ := v.pane(a)
	if v.sampleFromPane(p) && p.loaded {
		v.sample = p.runs
	}
	cmds := []tea.Cmd{p.Refresh(a, force)}
	if v.busy == 0 && (force || time.Since(v.last) > workflowsEvery) {
		cmds = append(cmds, v.load(a, gh.Background(context.Background())))
	}
	return tea.Batch(cmds...)
}

func (v *repoView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case repoWfMsg:
		v.busy = max(v.busy-1, 0)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not load workflows: "+msg.err.Error())
		}
		before := v.paneKey()
		v.setWorkflows(a, msg.wfs)
		if v.paneKey() != before {
			p, created := v.pane(a)
			if created {
				return p.load(a)
			}
		}
		return nil
	case repoSampleMsg:
		v.busy = max(v.busy-1, 0)
		if msg.runs != nil {
			v.sample = msg.runs
		}
		return v.loadFlaky(a)
	case flakyMsg:
		if msg.err == nil {
			v.flaky[msg.runID] = msg.jobs
		} else {
			delete(v.flakyAsked, msg.runID)
		}
		return nil
	case repoPreviewMsg:
		if msg.seq != v.previewSeq {
			return nil
		}
		v.previewAsked[msg.runID] = true
		id, repo, runID := v.id, v.repo, msg.runID
		return func() tea.Msg {
			_, _ = a.client.ListJobs(context.Background(), repo, runID)
			return repoJobsMsg{id}
		}
	case repoJobsMsg:
		return nil
	case repoSelMsg:
		// The cursor rested on a workflow: load its runs.
		if msg.seq != v.selSeq {
			return nil
		}
		p, _ := v.pane(a)
		if time.Since(p.last) > 15*time.Second {
			return p.load(a)
		}
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

// selectWorkflow selects the workflow with this ID and shows its runs.
func (v *repoView) selectWorkflow(a *App, id int64) tea.Cmd {
	for i, w := range v.wfs {
		if w.ID == id {
			v.focus = paneRuns
			return v.selectWF(a, i+1)
		}
	}
	return nil
}

func (v *repoView) selectWF(a *App, i int) tea.Cmd {
	if i < 0 || i > len(v.wfs) || i == v.sel {
		return nil
	}
	v.sel = i
	v.pane(a)
	v.selSeq++
	seq, id := v.selSeq, v.id
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return repoSelMsg{id, seq} })
}

func (v *repoView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	if v.section != sectionActions || a.top() != View(v) {
		return cmd
	}
	return tea.Batch(cmd, v.preview(a))
}

// preview reads the jobs of the run under the cursor, once the cursor has
// rested there, when the screen is wide enough to show the preview panel.
func (v *repoView) preview(a *App) tea.Cmd {
	if a.w < previewMinWidth || v.section != sectionActions {
		return nil
	}
	p, _ := v.pane(a)
	run := p.selected()
	if run == nil || (v.previewAsked[run.ID] && !run.Active()) {
		return nil
	}
	if _, cached := a.client.CachedJobs(v.repo, run.ID); cached && !run.Active() {
		return nil
	}
	v.previewSeq++
	id, seq, runID := v.id, v.previewSeq, run.ID
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return repoPreviewMsg{id, seq, runID} })
}

// ClearMarks unticks the ticked runs of the pane on display.
func (v *repoView) ClearMarks() bool {
	if p, ok := v.panes[v.paneKey()]; ok && v.section == sectionActions {
		return p.ClearMarks()
	}
	return false
}

func (v *repoView) Keys(a *App) []Binding {
	bs := v.sectionKeys(a)
	alerts, watchRepo := a.alertsKey(v.repo), a.watchRepoKey(v.repo)
	if v.section != sectionActions {
		alerts.Desc, watchRepo.Desc = "", "" // still keys, but the bar keeps its room for the section's own
	}
	bs = append(bs, grouped("A REPO", alerts, watchRepo)...)
	if a.inCheckout(v.repo) && a.opts.Branch != "" {
		bs = append(bs, grouped("A REPO", a.newPRKey(v.repo))...)
	}
	return bs
}

func (v *repoView) sectionKeys(a *App) []Binding {
	tab := viewKeys("next view", "next section", "A REPO", func(d int) tea.Cmd {
		return v.setSection(a, v.stepSection(a, d))
	})
	if s, _, _ := v.extraSection(a); s != nil {
		return append(s.Keys(a), tab)
	}
	switch v.section {
	case sectionPulls:
		return append(v.pullsList(a).Keys(a), tab)
	case sectionCommits:
		return append(v.commitsList(a).Keys(a), tab)
	case sectionIssues:
		return append(v.issuesList(a).Keys(a), tab)
	}

	p, _ := v.pane(a)
	wf := v.selectedWF()
	hasFile := wf != nil && !wf.Dynamic()
	// The workflow keys show in the key bar only while the workflows have
	// the focus; the list itself names them too.
	onWorkflows := v.focus == paneWorkflows
	bar := func(desc string) string {
		if onWorkflows && hasFile {
			return desc
		}
		return ""
	}
	enableDesc := "disable"
	if wf != nil && wf.State != "active" {
		enableDesc = "enable"
	}
	workflow := grouped("A WORKFLOW",
		Binding{Keys: []string{"d"}, Desc: bar("run workflow"), Help: "run the workflow", Off: !hasFile, Mutates: true, Need: gh.CapWrite, Repo: v.repo,
			Do: do(func() tea.Cmd {
				if wf == nil {
					return a.flashCmd(nHint, "Pick a workflow on the left first, then press d.")
				}
				return a.startDispatch(v.repo, *wf)
			})},
		Binding{Keys: []string{"v"}, Desc: bar("yaml"), Help: "the workflow's YAML", Off: !hasFile, Do: do(func() tea.Cmd {
			if !hasFile {
				return a.flashCmd(nHint, "Pick a workflow with a YAML file on the left first.")
			}
			return a.push(newPagerView(a, v.repo, *wf))
		})},
		Binding{Keys: []string{"E"}, Desc: bar(enableDesc), Help: "enable / disable the workflow", Off: wf == nil, Mutates: true, Need: gh.CapWrite, Repo: v.repo, Do: do(func() tea.Cmd { return v.toggleWorkflow(a) })},
	)
	pin := a.pinKey(v.repo)
	pin.Group = "A REPO"

	if !onWorkflows {
		bs := append(p.Keys(a), Binding{Keys: []string{"left", "h"}, Label: "←", Desc: "workflows", Group: "A REPO",
			Do: do(func() tea.Cmd { v.focus = paneWorkflows; return nil })})
		pin.Desc = ""
		return append(append(bs, workflow...), pin, tab)
	}
	bs := []Binding{
		moveBinding("workflow", func(k string) tea.Cmd {
			switch k {
			case "up", "k":
				return v.selectWF(a, v.sel-1)
			case "down", "j":
				return v.selectWF(a, v.sel+1)
			case "home", "g":
				return v.selectWF(a, 0)
			case "end", "G":
				return v.selectWF(a, len(v.wfs))
			}
			return nil
		}),
		{Keys: []string{"right", "enter", "l"}, Label: "→", Desc: "its runs", Do: do(func() tea.Cmd { v.focus = paneRuns; return nil })},
	}
	bs = append(bs, only(p.Keys(a), "b", "s", "z")...)
	bs = append(bs, workflow...)
	pin.Desc = "pin repo"
	bs = append(bs, pin, Binding{Label: "/", Desc: "filter runs"},
		Binding{Keys: []string{"o"}, Help: "open the workflow in the browser", Do: do(func() tea.Cmd {
			if hasFile {
				return a.openURL(a.client.WebURL(v.repo + "/actions/workflows/" + path.Base(wf.Path)))
			}
			return a.openURL(a.client.WebURL(v.repo + "/actions"))
		})}, tab)
	return grouped("A REPO", bs...)
}

func (v *repoView) toggleWorkflow(a *App) tea.Cmd {
	wf := v.selectedWF()
	if wf == nil {
		return a.flashCmd(nHint, "Pick a workflow on the left first.")
	}
	return a.toggleWorkflow(v.repo, *wf)
}

// Filtering applies to the runs pane, or to the pull requests.
func (v *repoView) SetFilter(a *App, q string) {
	if s, _, _ := v.extraSection(a); s != nil {
		s.SetFilter(a, q)
		return
	}
	switch v.section {
	case sectionPulls:
		v.pullsList(a).SetFilter(a, q)
		return
	case sectionCommits:
		v.commitsList(a).SetFilter(a, q)
		return
	case sectionIssues:
		v.issuesList(a).SetFilter(a, q)
		return
	}
	p, _ := v.pane(a)
	p.SetFilter(a, q)
	v.focus = paneRuns
}
func (v *repoView) FilterValue() string {
	if v.section == sectionBranches && v.branches != nil {
		return v.branches.FilterValue()
	}
	if v.section == sectionReleases && v.releases != nil {
		return v.releases.FilterValue()
	}
	if v.section == sectionPulls && v.pulls != nil {
		return v.pulls.FilterValue()
	}
	if v.section == sectionCommits && v.commits != nil {
		return v.commits.FilterValue()
	}
	if v.section == sectionIssues && v.issues != nil {
		return v.issues.FilterValue()
	}
	if p, ok := v.panes[v.paneKey()]; ok {
		return p.FilterValue()
	}
	return ""
}
func (v *repoView) FilterInfo(a *App) filterInfo {
	if s, _, _ := v.extraSection(a); s != nil {
		return s.FilterInfo(a)
	}
	switch v.section {
	case sectionPulls:
		return v.pullsList(a).FilterInfo(a)
	case sectionCommits:
		return v.commitsList(a).FilterInfo(a)
	case sectionIssues:
		return v.issuesList(a).FilterInfo(a)
	}
	p, _ := v.pane(a)
	return p.FilterInfo(a)
}

func (v *repoView) ClearFilters(a *App) tea.Cmd {
	if v.section != sectionActions {
		return nil
	}
	p, _ := v.pane(a)
	return p.ClearFilters(a)
}

// health returns the latest kind and success rate of a workflow from the sample.
func (v *repoView) health(wfID int64) (Kind, int, bool) {
	var runs []gh.Run
	if p, ok := v.panes[wfID]; ok && wfID != 0 && p.loaded && p.branch == "" && !p.mine && p.status == "" {
		runs = p.runs
	} else {
		for _, r := range v.sampleRuns() {
			if wfID == 0 || r.WorkflowID == wfID {
				runs = append(runs, r)
			}
		}
	}
	if len(runs) == 0 {
		return KNone, -1, false
	}
	return StatusKind(runs[0].Status, runs[0].Conclusion), computeStats(runs, time.Now()).successRate(), true
}

// sync brings the list of the section on display up to date.
func (v *repoView) sync(a *App) {
	if s, _, _ := v.extraSection(a); s != nil {
		s.sync(a)
		return
	}
	switch v.section {
	case sectionPulls:
		v.pullsList(a).sync(a)
	case sectionCommits:
		v.commitsList(a).sync(a)
	case sectionIssues:
		v.issuesList(a).sync(a)
	default:
		p, _ := v.pane(a)
		p.sync(a)
	}
}

// Render draws the repo's header line, then the section.
func (v *repoView) Render(a *App, w, h int) string {
	head := v.renderHead(a, w)
	if s, _, _ := v.extraSection(a); s != nil {
		return head + "\n" + s.(interface{ Render(*App, int, int) string }).Render(a, w, h-2)
	}
	switch v.section {
	case sectionPulls:
		p := v.pullsList(a)
		return head + "\n" + box(title("Pull requests", "open", p.t.Len(), ""), p.Render(a, w-2, h-4), w, h-2, cCyan)
	case sectionIssues:
		c := v.issuesList(a)
		return head + "\n" + box(title("Issues", "open", c.t.Len(), ""), c.Render(a, w-2, h-4), w, h-2, cCyan)
	case sectionCommits:
		c := v.commitsList(a)
		return head + "\n" + box(title("Commits", c.branchName(a), c.t.Len(), ""), c.Render(a, w-2, h-4), w, h-2, cCyan)
	}
	return head + "\n" + v.renderActions(a, w, h-2)
}

// renderHead is the repo's header: a line of facts (its name, the branch on
// display and how its last run went) and the strip of its sections.
func (v *repoView) renderHead(a *App, w int) string {
	owner, name, _ := strings.Cut(v.repo, "/")
	facts := " " + ico("repo") + sText.Render(owner+" / ") + sWhite.Bold(true).Render(name)
	branch := v.branch
	if branch == "" {
		if r, ok := a.repoByName(v.repo); ok {
			branch = r.DefaultBranch
		}
	}
	if branch != "" {
		facts += "   " + fact("branch", branch)
	}
	if k, _, known := v.health(0); known {
		facts += "  " + fg(k.Color()).Render(k.Glyph(a.frame)+" last run "+k.Label())
	}
	facts += a.roleChip(v.repo)
	if hint := v.branchHint(a); hint != "" && width(facts)+width(hint)+4 < w {
		facts += strings.Repeat(" ", w-width(facts)-width(hint)-1) + hint
	}

	all := []stripItem{{Label: "Actions", Icon: "actions"}, {Label: "Pull requests", Icon: "pr"}, {Label: "Issues", Icon: "issue"},
		{Label: "Commits", Icon: "commit"}, {Label: "Branches", Icon: "branch"}, {Label: "Releases", Icon: "tag"},
		{Label: "Environments", Icon: "rocket"}, {Label: "Secrets", Icon: "key"}}
	counted := func(i int, known bool, n int) {
		if known {
			all[i].Count, all[i].Quiet = fmt.Sprint(n), n == 0
		}
	}
	if v.pulls != nil {
		counted(sectionPulls, !v.pulls.at.IsZero(), len(v.pulls.prs))
	}
	if v.issues != nil {
		counted(sectionIssues, !v.issues.at.IsZero(), len(v.issues.items))
	}
	if v.releases != nil {
		counted(sectionReleases, !v.releases.at.IsZero(), len(v.releases.rels))
	}
	if v.environments != nil {
		counted(sectionEnvironments, !v.environments.at.IsZero(), len(v.environments.list))
	}
	var items []stripItem
	active := 0
	for _, i := range v.sections(a) {
		if i == v.section {
			active = len(items)
		}
		items = append(items, all[i])
	}
	return fit(facts, w) + "\n" + viewStrip(items, active, w)
}

// issuesOff says the repo has issues turned off, as the repo list or a read
// of its issues found.
func (v *repoView) issuesOff(a *App) bool {
	if r, ok := a.repoByName(v.repo); ok && r.IssuesOff {
		return true
	}
	return v.issues != nil && !v.issues.at.IsZero() && !v.issues.enabled
}

// sections are the sections the repo shows, in order: Issues only while the
// repo has them on, or while you are on it.
func (v *repoView) sections(a *App) []int {
	var out []int
	for i := range sectionCount {
		if i == sectionIssues && i != v.section && v.issuesOff(a) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// stepSection is the section d steps from the one on display, wrapping around.
func (v *repoView) stepSection(a *App, d int) int {
	shown := v.sections(a)
	at := slices.Index(shown, v.section)
	return shown[(at+d+len(shown))%len(shown)]
}

// sidebarWidth fits the longest workflow name with its bar and success
// rate, up to a third of the screen; a longer name is cut with an ellipsis.
func (v *repoView) sidebarWidth(w int) int {
	want := runeLen("All runs")
	for _, wf := range v.wfs {
		want = max(want, runeLen(wf.Name))
	}
	return min(max(30, want+18), max(30, w/3))
}

// sampleRuns are the repo's latest runs, behind the health numbers.
func (v *repoView) sampleRuns() []gh.Run {
	if p, ok := v.panes[0]; ok && v.sampleFromPane(p) && p.loaded {
		return p.runs
	}
	return v.sample
}

// renderActions draws the panels of the Actions section: the workflows and
// the repo's health at the left, the runs in the middle and, when there is
// room, a preview of the run under the cursor at the right.
func (v *repoView) renderActions(a *App, w, h int) string {
	sideW := v.sidebarWidth(w)
	prevW := 0
	if w >= previewMinWidth {
		prevW = min(max(w*3/10, 46), 60)
	}
	wfH := min(len(v.wfs)+3, h)
	if h-wfH < 8 {
		wfH = h // no room for the health panel
	}
	side := v.renderWorkflows(a, sideW, wfH)
	if h > wfH {
		side += "\n" + v.renderHealth(a, sideW, h-wfH)
	}

	p, _ := v.pane(a)
	ttl := title("Runs", "all workflows", p.total, "")
	if wf := v.selectedWF(); wf != nil {
		file := path.Base(wf.Path)
		if wf.Dynamic() {
			file = "managed by GitHub"
		}
		ttl = title("Runs", wf.Name+" · "+file, p.total, "")
	}
	main := p.renderPane(a, w-sideW-prevW, h, ttl, v.focus == paneRuns)
	if prevW == 0 {
		return hjoin(side, main)
	}
	return hjoin(side, main, v.renderPreview(a, p, prevW, h))
}

// renderWorkflows is the list of workflows: how the latest run of each
// went, and how often it passes.
func (v *repoView) renderWorkflows(a *App, w, h int) string {
	v.sideH = h - 2
	if v.sel < v.offset {
		v.offset = v.sel
	}
	if v.sel >= v.offset+v.sideH {
		v.offset = v.sel - v.sideH + 1
	}
	var lines []string
	inner := w - 2
	for i := v.offset; i <= len(v.wfs) && len(lines) < v.sideH; i++ {
		name := "All runs"
		var id int64
		off := false
		if i > 0 {
			wf := v.wfs[i-1]
			name, id, off = wf.Name, wf.ID, wf.State != "active"
		}
		k, pct, known := v.health(id)
		dot := sDim.Render("○")
		if known {
			dot = fg(k.Color()).Render("●")
		}
		if i == 0 {
			dot = sGrey.Render("≡")
		}
		right, ns := "", sText
		switch {
		case off:
			right, ns = sDim.Render("off"), sDim
		case pct >= 0:
			right = thinBar(pct, 5) + " " + rateStyle(pct).Render(fmt.Sprintf("%3d%%", pct))
		}
		if i == v.sel {
			ns = sWhite.Bold(true)
		}
		line := fit(" "+dot+" "+ns.Render(name), inner-width(right)-2) + " " + right + " "
		if i == v.sel {
			line = selectedLine(line, v.focus != paneWorkflows)
		}
		lines = append(lines, line)
	}
	if !v.loaded {
		lines = append(lines, " "+sDim.Render("Loading workflows…"))
	} else if len(v.wfs) == 0 {
		lines = append(lines, "", " "+sDim.Render("No workflows in this repo."))
	}
	border := cFrameDim
	if v.focus == paneWorkflows {
		border = cCyan
	}
	return box(title("Workflows", "", -1, ""), strings.Join(lines, "\n"), w, h, border)
}

// renderHealth sums up the repo's latest runs: how many passed, how each
// went, how long they take and what needs you.
func (v *repoView) renderHealth(a *App, w, h int) string {
	now := time.Now()
	runs := v.sampleRuns()
	inner := w - 2
	kv := func(label, val string) string {
		return fit(" "+sGrey.Render(label), inner-width(val)-1) + val + " "
	}
	if len(runs) == 0 {
		return box(title("Health", "", -1, ""), " "+sDim.Render("No runs yet."), w, h, cFrameDim)
	}
	s := computeStats(runs, now)
	var lines []string
	if pct := s.successRate(); pct >= 0 {
		lines = append(lines, " "+rateStyle(pct).Bold(true).Render(fmt.Sprintf("%d of %d", s.ok, s.ok+s.fail))+sGrey.Render(" finished passed"))
	}
	if s.cancel > 0 {
		lines = append(lines, " "+sDim.Render(fmt.Sprintf("%d cancelled, not counted", s.cancel)))
	}
	// One block per run, oldest first, as many as fit.
	n := min(len(runs), inner-2)
	kinds := make([]Kind, 0, n)
	waiting := 0
	for i := n - 1; i >= 0; i-- {
		kinds = append(kinds, StatusKind(runs[i].Status, runs[i].Conclusion))
	}
	for _, r := range runs {
		if a.needsApproval(repoRun{v.repo, r}, now) {
			waiting++
		}
	}
	history, _ := spark(kinds, a.frame)
	lines = append(lines, " "+history, "")
	if s.median > 0 {
		lines = append(lines, kv("median", sWhite.Render(dur(s.median))), kv("slowest 10%", sWhite.Render(dur(s.p90))),
			" "+sDim.Render("start to end, waits included"))
	}
	if s.active > 0 {
		lines = append(lines, kv("running now", sAmber.Render(fmt.Sprint(s.active))))
	}
	if waiting > 0 {
		lines = append(lines, kv("waiting on you", sWait.Render(fmt.Sprint(waiting))))
	}
	if len(s.failBranches) > 0 {
		if where := topN(s.failBranches, 1); width(where)+12 <= inner {
			lines = append(lines, kv("fails on", where))
		} else {
			lines = append(lines, " "+sGrey.Render("fails on"), "   "+where)
		}
	}
	lines = append(lines, v.flakyLines(runs, inner)...)
	if al := a.alertLine(v.repo); al != "" {
		lines = append(lines, "", " "+al)
	}
	return box(title("Health", fmt.Sprintf("last %d runs", len(runs)), -1, ""), strings.Join(lines, "\n"), w, h, cFrameDim)
}

// renderPreview shows the run under the cursor without opening it: how it
// went, where it comes from and its jobs.
func (v *repoView) renderPreview(a *App, p *runsView, w, h int) string {
	run := p.selected()
	if run == nil {
		return box(title("Preview", "", -1, ""), " "+sDim.Render("No run selected."), w, h, cFrameDim)
	}
	now := time.Now()
	inner := w - 2
	k := StatusKind(run.Status, run.Conclusion)
	state := "after " + dur(run.Duration(now))
	if run.Active() {
		state = "for " + dur(run.Duration(now))
	}
	if run.RunAttempt > 1 {
		state += fmt.Sprintf(" · attempt %d", run.RunAttempt)
	}
	lines := []string{" " + statusPill(k, a.frame, "") + " " + sGrey.Render(state)}
	for i, l := range strings.Split(ansi.Wordwrap(run.DisplayTitle, max(inner-2, 10), ""), "\n") {
		if i == 3 {
			break
		}
		lines = append(lines, " "+sWhite.Bold(true).Render(l))
	}
	sha := run.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	kv := func(label, val string) string { return " " + sGrey.Render(fit(label, 9)) + val }
	lines = append(lines, "",
		kv("branch", fact("branch", run.HeadBranch)+sDim.Render(" @ "+sha)),
		kv("started", sText.Render(run.Event+" by "+run.Actor.Login+", "+age(run.CreatedAt, now)+" ago")),
		" "+sDim.Render(strings.Repeat("─", max(inner-2, 0))))

	jobs, cached := a.client.CachedJobs(v.repo, run.ID)
	if !cached {
		for _, l := range jobSkeleton(inner - 2) {
			lines = append(lines, " "+l)
		}
	}
	for _, j := range jobs {
		jk := jobKind(j)
		name := fg(jk.RowColor()).Render(j.Name)
		if jk == KFail {
			name = sRed.Render(j.Name)
		}
		d := sGrey.Render(jobDur(j, now))
		lines = append(lines, fit(" "+glyph(jk, a.frame)+" "+name, inner-width(d)-1)+d+" ")
		if jk != KFail && jk != KRun {
			continue
		}
		for _, st := range j.Steps {
			if sk := stepKind(st); sk == KFail || sk == KRun {
				sd := sDim.Render(stepDur(st, now))
				lines = append(lines, fit("     "+glyph(sk, a.frame)+" "+fg(sk.Color()).Render(st.Name), inner-width(sd)-1)+sd+" ")
			}
		}
	}
	if k == KFail && cached {
		lines = append(lines, "", " "+keycap("enter")+sGrey.Render(" why it failed  ")+keycap("l")+sGrey.Render(" log"))
	}
	return box(title(fmt.Sprintf("%s #%d", run.Name, run.RunNumber), "", -1, ""), strings.Join(lines, "\n"), w, h, cFrameDim)
}

// thinBar is a slim bar of w cells filled to pct, in the colour of the rate.
func thinBar(pct, w int) string {
	on, off := "━", "━"
	if ascii {
		on, off = "=", "-"
	}
	n := min(max((pct*w+50)/100, 0), w)
	return rateStyle(pct).Render(strings.Repeat(on, n)) + sDim.Render(strings.Repeat(off, w-n))
}
