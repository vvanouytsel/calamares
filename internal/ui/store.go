package ui

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// store holds what several screens share: your repositories, the latest runs
// of the ones the Actions tab covers, and your pull requests.
// Screens load what they show when you open them, when you press ctrl+r, and
// while they keep themselves up to date; the push check alone runs by itself.
type store struct {
	loaded     bool // the repo list is known, from the cache or from GitHub
	reposState loadState
	sweeping   []gh.Repo // repos read so far by the sweep in progress
	refreshing int       // repos being read again on their own, when only some are
	// pushesState is how the push check went; newRepos the repos it found
	// that the list doesn't hold yet.
	pushesState loadState
	newRepos    []string

	status  map[string][]gh.Run // latest runs per repo
	runsGen int                 // goes up whenever status changes
	shared  sharedCache

	// uses are what the workflow files of usesRepos repos use; see gh.Uses.
	uses      []gh.Use
	schedules []gh.Schedule // the workflows that run on a schedule, from the same files
	// scheduled says the files were read for schedules too; a snapshot of an
	// earlier version only knows what they use.
	scheduled bool
	usesRepos int
	usesState loadState
	usesGen   int // goes up whenever uses change

	// owners are the CODEOWNERS files of your repos, by repo name; see gh.CodeOwners.
	owners      map[string]gh.CodeOwners
	ownersState loadState

	runsState map[string]*loadState // how reading each repo's runs went; see runState
	// canApprove records, per waiting run, whether you may approve one of its deployments.
	canApprove map[int64]bool
	// startChecked is, per queued run whose jobs were read, the update it had then.
	startChecked map[int64]time.Time

	prs      []gh.PullRequest
	prsState loadState

	issues      gh.IssuesOfMine
	issuesState loadState

	// What the Failing view reads: the runs of each repo's default branch,
	// and why the failed ones failed.
	defaults      map[string][]gh.Run
	defaultsState map[string]*loadState
	causes        map[string]runCause // by causeID
	causeBusy     map[string]bool
	causeQueue    []repoRun
	wantCauses    bool // the Failing view was shown

	// What explains how a version of a shared workflow compares with the one
	// before it: the diffs between tags, and the jobs of some calls (nil
	// while they are read).
	diffs    map[string]*versionDiff
	callJobs map[int64][]gh.Job

	sched scheduleData // what the Scheduled view reads

	caches cacheData // what the Caches view reads

	// What the Warnings view reads: the warnings of the latest runs on the
	// default branches, by causeID.
	warnings     map[string][]runWarning
	warningBusy  map[string]bool
	warningQueue []repoRun
	wantWarnings bool // the Warnings view was shown

	runners runnerData // what the Runners view reads

	times timeData // what the Time view reads

	ghStatus statusData // what GitHub's status page says
}

// runState is how reading repo's latest runs went.
func (s *store) runState(repo string) *loadState {
	l, ok := s.runsState[repo]
	if !ok {
		l = &loadState{}
		s.runsState[repo] = l
	}
	return l
}

// repoStatusFilter is the run sample kept per repo for the Actions tab.
var repoStatusFilter = gh.RunFilter{PerPage: 10}

// sweepSince is how recently a repo must have been pushed to for the sweep
// to read it; it stops at the first older one.
const sweepSince = 365 * 24 * time.Hour

const (
	snapshotUses   = "uses"
	snapshotOwners = "codeowners"
	snapshotRepos  = "repos"
	snapshotPRs    = "pulls"
	snapshotIssues = "issues"
)

// stReposMsg is one page of the repo sweep; next is the cursor of the
// following page, or "" when the sweep is done.
type stReposMsg struct {
	repos []gh.Repo
	next  string
	err   error
}

// stPushesMsg is one page of the push check; next is the cursor of the
// following page, or "" when the check is done.
type stPushesMsg struct {
	pushes []gh.Push
	next   string
	err    error
}

// stSomeReposMsg is the answer to reading only some repositories again.
type stSomeReposMsg struct {
	repos []gh.Repo
	err   error
}
type stRunsMsg struct {
	repo string
	runs []gh.Run
	err  error
}
type stApproverMsg struct {
	runID int64
	can   bool
	err   error
}
type stPRsMsg struct {
	prs []gh.PullRequest
	err error
}
type stIssuesMsg struct {
	issues gh.IssuesOfMine
	err    error
}

func newStore() *store {
	return &store{status: map[string][]gh.Run{}, runsState: map[string]*loadState{}, canApprove: map[int64]bool{}, startChecked: map[int64]time.Time{},
		defaults: map[string][]gh.Run{}, defaultsState: map[string]*loadState{}, causes: map[string]runCause{}, causeBusy: map[string]bool{},
		diffs: map[string]*versionDiff{}, callJobs: map[int64][]gh.Job{},
		sched: newScheduleData(), caches: newCacheData(), warnings: map[string][]runWarning{}, warningBusy: map[string]bool{},
		runners: newRunnerData(), times: timeData{runs: map[int64]*runTime{}}}
}

// seed fills the store from the response cache, so the first screen has data
// at once; each screen then checks with GitHub when it is shown.
func (s *store) seed(a *App) {
	var repos []gh.Repo
	if at, ok := a.client.Recall(snapshotRepos, &repos); ok {
		a.repos, s.reposState.at, s.loaded = repos, at, true
	}
	for _, r := range a.repos {
		if runs, _, ok := a.client.CachedRuns(r.FullName, repoStatusFilter); ok {
			s.status[r.FullName] = runs
			s.runsGen++
			s.runState(r.FullName).at, _ = a.client.CachedRunsAt(r.FullName, repoStatusFilter)
		}
	}
	s.seedTimes(a)
	var uses usesSnapshot
	if at, ok := a.client.Recall(snapshotUses, &uses); ok {
		s.uses, s.schedules, s.scheduled, s.usesRepos, s.usesState.at = uses.Uses, uses.Schedules, uses.Scheduled, uses.Repos, at
		s.usesGen++
	}
	if at, ok := a.client.Recall(snapshotOwners, &s.owners); ok {
		s.ownersState.at = at
	}
	for _, r := range a.repos {
		if r.DefaultBranch == "" {
			continue
		}
		if runs, _, ok := a.client.CachedRuns(r.FullName, defaultRunsFilter(r.DefaultBranch)); ok {
			s.defaults[r.FullName] = runs
			s.defaultState(r.FullName).at, _ = a.client.CachedRunsAt(r.FullName, defaultRunsFilter(r.DefaultBranch))
		}
	}
	a.client.Recall(snapshotCauses, &s.causes)
	if s.causes == nil {
		s.causes = map[string]runCause{}
	}
	a.client.Recall(snapshotRunnerHistory, &s.runners.history)
	if s.runners.history == nil {
		s.runners.history = map[int64]pastJob{}
	}
	a.client.Recall(snapshotWarnings, &s.warnings)
	if s.warnings == nil {
		s.warnings = map[string][]runWarning{}
	}
	if at, ok := a.client.Recall(snapshotPRs, &s.prs); ok {
		s.prsState.at = at
	}
	if at, ok := a.client.Recall(snapshotIssues, &s.issues); ok {
		s.issuesState.at = at
	}
}

// loadRepos sweeps your repositories with their CI health, a page of 25
// at a time; the list on screen grows as pages arrive.
func (s *store) loadRepos(a *App) tea.Cmd {
	if !s.reposState.begin() {
		return nil
	}
	s.sweeping = nil
	return s.sweepPage(a, "")
}

// loadPushes reads when each of your repos was last pushed to, a page of 100
// at a time. A repo pushed to since its runs were last read is then due on
// Actions at once; repos the list didn't hold yet are read in full after.
func (s *store) loadPushes(a *App) tea.Cmd {
	if !s.loaded || s.reposState.busy || a.client.GraphQLRate().Saving(time.Now()) || !s.pushesState.begin() {
		return nil
	}
	s.newRepos = nil
	return s.pushesPage(a, "")
}

func (s *store) pushesPage(a *App, after string) tea.Cmd {
	return func() tea.Msg {
		// The foreground lane: a page or three every 2 minutes shouldn't make
		// the background reads behind it wait.
		pushes, next, err := a.client.SweepPushes(context.Background(), after)
		return stPushesMsg{pushes, next, err}
	}
}

// gotPushes takes a page of the push check into the repo list.
func (s *store) gotPushes(a *App, m stPushesMsg) tea.Cmd {
	if m.err != nil {
		s.pushesState.end(m.err)
		return nil // the next check tries again; a sweep or a refresh says what is wrong
	}
	at := make(map[string]int, len(a.repos))
	for i, r := range a.repos {
		at[strings.ToLower(r.FullName)] = i
	}
	moved := false
	for _, p := range m.pushes {
		i, ok := at[strings.ToLower(p.Repo)]
		switch {
		case !ok:
			s.newRepos = append(s.newRepos, p.Repo)
		case p.At.After(a.repos[i].PushedAt):
			if !moved {
				a.repos, moved = slices.Clone(a.repos), true
			}
			a.repos[i].PushedAt = p.At
		}
	}
	if moved {
		sort.SliceStable(a.repos, func(i, j int) bool { return a.repos[i].PushedAt.After(a.repos[j].PushedAt) })
		a.reads = readCache{}
	}
	if n := len(m.pushes); m.next != "" && n > 0 && time.Since(m.pushes[n-1].At) < sweepSince {
		return s.pushesPage(a, m.next)
	}
	s.pushesState.end(nil)
	if moved {
		a.client.Remember(snapshotRepos, a.repos)
	}
	if len(s.newRepos) > 0 {
		return s.loadSomeRepos(a, s.newRepos)
	}
	return nil
}

// loadSomeRepos reads only the named repositories again, leaving the rest of
// the list as it is.
func (s *store) loadSomeRepos(a *App, names []string) tea.Cmd {
	if !s.reposState.begin() {
		return nil
	}
	s.refreshing = len(names)
	return func() tea.Msg {
		repos, err := a.client.SweepRepos(context.Background(), names)
		return stSomeReposMsg{repos, err}
	}
}

func (s *store) sweepPage(a *App, after string) tea.Cmd {
	return func() tea.Msg {
		repos, next, err := a.client.SweepPage(context.Background(), after)
		return stReposMsg{repos, next, err}
	}
}

// loadRuns reads the latest runs of repos, one request each, revalidated
// with ETags. ctx decides the lane.
func (s *store) loadRuns(a *App, ctx context.Context, repos []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, repo := range repos {
		if !s.runState(repo).begin() {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			runs, _, err := a.client.ListRuns(ctx, repo, repoStatusFilter)
			return stRunsMsg{repo, runs, err}
		})
	}
	return tea.Batch(cmds...)
}

// loadPRs reads the open pull requests that concern you: one GraphQL request.
func (s *store) loadPRs(a *App) tea.Cmd {
	if !s.prsState.begin() {
		return nil
	}
	return func() tea.Msg {
		prs, err := a.client.MyPullRequests(context.Background())
		if err == nil {
			a.client.Remember(snapshotPRs, prs)
		}
		return stPRsMsg{prs, err}
	}
}

// usesStale is how old the workflow files read may get before a shared view
// reads them again. GitHub can't answer "unchanged" to GraphQL, so each read
// costs, if little: a point per 50 repos.
const usesStale = 24 * time.Hour

type usesSnapshot struct {
	Uses      []gh.Use
	Schedules []gh.Schedule
	Scheduled bool
	Repos     int
}

type stUsesMsg struct {
	scan  gh.Scan
	repos int
	err   error
}

// loadUses reads what the workflow files of all your repos with Actions use,
// when they were never read, are older than usesStale, or force is set.
func (s *store) loadUses(a *App, force bool) tea.Cmd {
	if !s.loaded || (!force && time.Since(s.usesState.at) < usesStale) || !s.usesState.begin() {
		return nil
	}
	var repos []string
	for _, r := range a.ciRepos() {
		repos = append(repos, r.FullName)
	}
	return func() tea.Msg {
		scan, err := a.client.ScanWorkflows(gh.Background(context.Background()), repos)
		if err == nil {
			a.client.Remember(snapshotUses, usesSnapshot{scan.Uses, scan.Schedules, true, len(repos)})
		}
		return stUsesMsg{scan, len(repos), err}
	}
}

type stOwnersMsg struct {
	owners map[string]gh.CodeOwners
	asked  []string
	all    bool
	err    error
}

// ownersStale is how old the CODEOWNERS files read may get before they are
// read again.
const ownersStale = 24 * time.Hour

// loadOwners reads the CODEOWNERS file of the repos it has none of yet, or of
// all of them when they are older than ownersStale or force is set.
func (s *store) loadOwners(a *App, force bool) tea.Cmd {
	if !s.loaded || !s.ownersState.begin() {
		return nil
	}
	all := force || time.Since(s.ownersState.at) >= ownersStale
	var repos []string
	for _, r := range a.repos {
		if _, known := s.owners[r.FullName]; !r.Archived && (all || !known) {
			repos = append(repos, r.FullName)
		}
	}
	if len(repos) == 0 {
		s.ownersState.end(nil)
		return nil
	}
	return func() tea.Msg {
		owners, err := a.client.CodeOwners(gh.Background(context.Background()), repos)
		return stOwnersMsg{owners, repos, all, err}
	}
}

// loadIssues reads the open issues that concern you: one GraphQL request.
func (s *store) loadIssues(a *App) tea.Cmd {
	if !s.issuesState.begin() {
		return nil
	}
	return func() tea.Msg {
		is, err := a.client.MyIssues(context.Background())
		if err == nil {
			a.client.Remember(snapshotIssues, is)
		}
		return stIssuesMsg{is, err}
	}
}

// busyRuns counts the repos whose runs are being read.
func (s *store) busyRuns() int {
	n := 0
	for _, l := range s.runsState {
		if l.busy {
			n++
		}
	}
	return n
}

// handle applies store messages; it reports whether msg was one.
func (s *store) handle(a *App, msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case stReposMsg:
		s.reposState.err = m.err
		if m.err != nil {
			s.reposState.busy = false
			return a.flashCmd(nError, "Could not read your repositories: "+m.err.Error()), true
		}
		s.sweeping = append(s.sweeping, m.repos...)
		a.repos, s.loaded = upsertRepos(a.repos, m.repos), true
		recent := len(m.repos) > 0 && time.Since(m.repos[len(m.repos)-1].PushedAt) < sweepSince
		if m.next != "" && recent {
			return s.sweepPage(a, m.next), true
		}
		// GitHub's order by push can lag a push by a while; the times it gives are what counts.
		a.repos = slices.Clone(s.sweeping)
		sort.SliceStable(a.repos, func(i, j int) bool { return a.repos[i].PushedAt.After(a.repos[j].PushedAt) })
		s.reposState.end(nil)
		a.client.Remember(snapshotRepos, a.repos)
		// Screens that list repos' runs can now know which repos to read.
		return a.visit(), true
	case stPushesMsg:
		return s.gotPushes(a, m), true
	case stSomeReposMsg:
		s.reposState.busy, s.refreshing = false, 0
		if m.err != nil {
			return a.flashCmd(nError, "Could not refresh those repositories: "+m.err.Error()), true
		}
		a.repos = upsertRepos(a.repos, m.repos)
		a.client.Remember(snapshotRepos, a.repos)
		return nil, true
	case stRunsMsg:
		s.runState(m.repo).end(m.err)
		if m.err != nil {
			return a.flashCmd(nError, "Could not read the runs of "+m.repo+": "+m.err.Error()), true
		}
		s.status[m.repo] = m.runs
		s.runsGen++
		return tea.Batch(s.checkApprovers(a, m.repo, m.runs), s.checkStarted(a, m.repo, m.runs), s.loadCauses(a, failingRuns(a, []string{m.repo}))), true
	case stStartCheckedMsg:
		if m.err != nil {
			delete(s.startChecked, m.id) // read again on the next refresh
			return nil, true
		}
		if runs := s.status[m.repo]; len(runs) > 0 {
			runs = slices.Clone(runs)
			a.client.KeepStarted(runs)
			s.status[m.repo] = runs
			s.runsGen++
		}
		return nil, true
	case defaultRunsMsg:
		s.defaultState(m.repo).end(m.err)
		if m.err != nil {
			return nil, true // the Failing view still has the latest runs of all branches
		}
		s.defaults[m.repo] = m.runs
		s.runsGen++
		var warn tea.Cmd
		if s.wantWarnings {
			var latest []repoRun
			for _, r := range a.latestPerWorkflow(m.repo) {
				latest = append(latest, repoRun{m.repo, r})
			}
			warn = s.loadWarnings(a, latest)
		}
		return tea.Batch(s.loadCauses(a, failingRuns(a, []string{m.repo})), warn), true
	case causeMsg:
		return s.gotCause(a, m), true
	case versionDiffMsg:
		s.gotDiff(m)
		return nil, true
	case callJobsMsg:
		if m.err != nil {
			delete(s.callJobs, m.runID)
			return nil, true
		}
		s.callJobs[m.runID] = m.jobs
		return nil, true
	case runTimesMsg:
		return s.gotRunTimes(a, m), true
	case statusMsg:
		return a.gotStatus(m), true
	case warningsMsg:
		return s.gotWarnings(a, m), true
	case scheduleWorkflowsMsg, scheduleRunsMsg:
		return s.gotSchedules(a, m)
	case cachesMsg:
		s.gotCaches(m)
		return nil, true
	case orgRunnersMsg, repoRunnersMsg, activeJobsMsg:
		return s.gotRunners(a, m)
	case stApproverMsg:
		if m.err != nil {
			delete(s.canApprove, m.runID)
			return nil, true
		}
		s.canApprove[m.runID] = m.can
		return nil, true
	case stPRsMsg:
		s.prsState.end(m.err)
		if m.err != nil {
			return a.flashCmd(nError, "Could not read your pull requests: "+m.err.Error()), true
		}
		s.prs = m.prs
		return nil, true
	case stUsesMsg:
		s.usesState.end(m.err)
		if m.err != nil {
			return a.flashCmd(nError, "Could not read the workflow files: "+m.err.Error()), true
		}
		s.uses, s.schedules, s.scheduled, s.usesRepos = m.scan.Uses, m.scan.Schedules, true, m.repos
		s.usesGen++
		// The repos that call yours, and the scheduled workflows, are known now.
		return a.visit(), true
	case stOwnersMsg:
		s.ownersState.end(m.err)
		if m.err != nil {
			return a.flashCmd(nError, "Could not read the CODEOWNERS files: "+m.err.Error()), true
		}
		if m.all || s.owners == nil {
			s.owners = map[string]gh.CodeOwners{}
		}
		for _, repo := range m.asked {
			s.owners[repo] = gh.CodeOwners{}
		}
		maps.Copy(s.owners, m.owners)
		a.client.Remember(snapshotOwners, s.owners)
		return nil, true
	case stIssuesMsg:
		s.issuesState.end(m.err)
		if m.err != nil {
			return a.flashCmd(nError, "Could not read your issues: "+m.err.Error()), true
		}
		s.issues = m.issues
		return nil, true
	}
	return nil, false
}

// checkApprovers asks, once per waiting run, whether you can approve it.
func (s *store) checkApprovers(a *App, repo string, runs []gh.Run) tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range runs {
		if _, checked := s.canApprove[r.ID]; checked || r.Status != "waiting" {
			continue
		}
		s.canApprove[r.ID] = false
		id := r.ID
		cmds = append(cmds, func() tea.Msg {
			envs, err := a.client.PendingDeployments(gh.Background(context.Background()), repo, id)
			can := false
			for _, e := range envs {
				can = can || e.CurrentUserCanApprove
			}
			return stApproverMsg{id, can, err}
		})
	}
	return tea.Batch(cmds...)
}

// stStartCheckedMsg says the jobs of a queued run of repo were read.
type stStartCheckedMsg struct {
	repo string
	id   int64
	err  error
}

// checkStarted reads the jobs of the queued runs not known to have started,
// once per change of the run: one that started before Calamares saw it run
// is queued again while a later job waits for a runner.
func (s *store) checkStarted(a *App, repo string, runs []gh.Run) tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range runs {
		if at, checked := s.startChecked[r.ID]; r.Status != "queued" || checked && at.Equal(r.UpdatedAt) {
			continue
		}
		s.startChecked[r.ID] = r.UpdatedAt
		id := r.ID
		cmds = append(cmds, func() tea.Msg {
			_, err := a.client.ListJobs(gh.Background(context.Background()), repo, id)
			return stStartCheckedMsg{repo, id, err}
		})
	}
	return tea.Batch(cmds...)
}

// upsertRepos updates the repos in list that page has newer data for and
// adds the ones it didn't have, keeping the most recently pushed first.
func upsertRepos(list, page []gh.Repo) []gh.Repo {
	out := slices.Clone(list)
	at := map[string]int{}
	for i, r := range out {
		at[r.FullName] = i
	}
	for _, r := range page {
		if i, ok := at[r.FullName]; ok {
			out[i] = r
		} else {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].PushedAt.After(out[j].PushedAt) })
	return out
}

// ciRepos returns the repos with workflows (plus pinned ones), most recently
// pushed first.
func (a *App) ciRepos() []gh.Repo {
	var out []gh.Repo
	for _, r := range a.repos {
		if (r.HasActions && !r.Archived) || a.cfg.IsFavourite(r.FullName) {
			out = append(out, r)
		}
	}
	return out
}

// pinnedRepos are the pinned repos, in the order they were pinned.
func (a *App) pinnedRepos() []string { return slices.Clone(a.cfg.Favourites) }

// needsApproval reports whether a run waits for you: a deployment you may
// approve, or, in one of your repos, a run from an outside contributor
// that needs approval to start (within the last week).
func (a *App) needsApproval(x repoRun, now time.Time) bool {
	if x.run.Status == "waiting" {
		return a.st.canApprove[x.run.ID]
	}
	return StatusKind(x.run.Status, x.run.Conclusion) == KWait && now.Sub(x.run.CreatedAt) < 7*24*time.Hour && a.yours(x.repo, now)
}

// yours reports whether repo is pinned or had a run of yours in the last week.
func (a *App) yours(repo string, now time.Time) bool {
	return a.cfg.IsFavourite(repo) || a.st.activeIn(repo, a.me, now.Add(-7*24*time.Hour))
}

// activeIn reports whether user started any of repo's sampled runs since since.
func (s *store) activeIn(repo, user string, since time.Time) bool {
	if user == "" {
		return false
	}
	for _, r := range s.status[repo] {
		if r.CreatedAt.After(since) && (strings.EqualFold(r.Actor.Login, user) || strings.EqualFold(r.TriggeringActor.Login, user)) {
			return true
		}
	}
	return false
}

// latest returns the newest known run of repo.
func (s *store) latest(repo string) (gh.Run, bool) {
	if runs := s.status[repo]; len(runs) > 0 {
		return runs[0], true
	}
	return gh.Run{}, false
}

type repoRun struct {
	repo string
	run  gh.Run
}

// recentRuns gathers the kept runs of repos, newest first.
func recentRuns(a *App, repos []string) []repoRun {
	var all []repoRun
	for _, repo := range repos {
		for _, run := range a.visibleRuns(repo, a.st.status[repo]) {
			all = append(all, repoRun{repo, run})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].run.CreatedAt.Equal(all[j].run.CreatedAt) {
			return all[i].run.CreatedAt.After(all[j].run.CreatedAt)
		}
		return all[i].run.ID > all[j].run.ID
	})
	return all
}

// short leaves the owner out of a repository name when it is the owner of
// most of your repositories; other owners stay in.
func (a *App) short(full string) string {
	owner := a.owner()
	if owner != "" && strings.HasPrefix(strings.ToLower(full), strings.ToLower(owner)+"/") {
		return full[len(owner)+1:]
	}
	return full
}

// repoLabel is a repo's short name with its owner, when it shows, dimmed:
// "acme/" grey, "webshop" light.
func (a *App) repoLabel(full string) string {
	s := a.short(full)
	if o, n, ok := strings.Cut(s, "/"); ok {
		return sDim.Render(o+"/") + sText.Render(n)
	}
	return sText.Render(s)
}

// owner is the account most of your repositories belong to (alphabetically
// first on a tie), or you while your repositories are loading.
func (a *App) owner() string {
	if len(a.repos) == 0 {
		return a.me
	}
	if len(a.ownerOf) == len(a.repos) && &a.ownerOf[0] == &a.repos[0] {
		return a.ownerName
	}
	count := map[string]int{}
	best := ""
	for _, r := range a.repos {
		o, _, _ := strings.Cut(r.FullName, "/")
		count[o]++
		if n := count[o]; n > count[best] || (n == count[best] && o < best) {
			best = o
		}
	}
	a.ownerOf, a.ownerName = a.repos, best
	return best
}
