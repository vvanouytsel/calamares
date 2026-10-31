package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// activityView is a place that looks across your repos, as views you switch
// with tab. The Actions place lists what runs: what fails on the default
// branches and the latest runs (running, waiting, the last 7 days). The
// Platform place lists what your repos share and what keeps them running:
// shared workflows and actions, warnings, schedules, runners and runner time.
// Both read the runs of your most active repos, or of all repos with
// workflows if you choose so.
type activityView struct {
	id            int
	place         int   // tabActions or tabPlatform
	views         []int // the views of the place, in the order of its strip
	read          map[int]bool
	filter        int
	peek          peeker
	mine          bool
	scope         string // which repos: scopeActive, scopeAll or scopeConsumers
	t             *Table
	failing       *failingView
	scheduled     *scheduledView
	warnings      *warningsView
	runners       *runnersView
	caches        *cachesView
	time          *timeView
	shared        *sharedView
	sharedActions *sharedView
	overview      *overviewView
	runs          map[string]repoRun
	recent        []repoRun // the runs of the tab's repos, newest first, as of the last sync

	// While the cursor is below the top row, runs that weren't shown yet are
	// held back so the rows you're looking at don't move.
	shown map[string]bool
	held  int
	// Runs that stop matching the filter stay a while, dimmed, instead of
	// vanishing from under the cursor.
	matched map[string]bool
	leaving map[string]time.Time
	// The rows of finished runs as last drawn, reused while nothing they show
	// changed: styling every row again on each key is slow for a long list.
	drawn map[string]drawnRow
}

// drawnRow is a row with what it was drawn from.
type drawnRow struct {
	from rowSource
	row  Row
}

// rowSource is what can change in the row of a finished run.
type rowSource struct {
	status, conclusion string
	updated            time.Time
	repo, age          string
	fading             bool
	theme              Theme
}

// fadeOut is how long a run that stopped matching the filter stays listed.
const fadeOut = 30 * time.Second

type actFilter struct {
	label string
	match func(*App, repoRun, time.Time) bool
	st    lipgloss.Style
}

func within(d time.Duration) func(*App, repoRun, time.Time) bool {
	return func(_ *App, x repoRun, now time.Time) bool { return now.Sub(x.run.CreatedAt) < d }
}

func kindIs(ks ...Kind) func(*App, repoRun, time.Time) bool {
	return func(_ *App, x repoRun, _ time.Time) bool {
		k := StatusKind(x.run.Status, x.run.Conclusion)
		for _, want := range ks {
			if k == want {
				return true
			}
		}
		return false
	}
}

// The views of the Actions and Platform places. The views from viewRunning
// to viewRecent list runs; the others list what they are about.
const (
	viewFailing = iota
	viewRunning
	viewWaiting
	viewRecent
	viewSharedWorkflows
	viewSharedActions
	viewWarnings
	viewScheduled
	viewRunners
	viewTime
	viewOverview
	viewCaches
)

// actionsViews and platformViews are the views of each place, in the order
// of its strip.
var (
	actionsViews  = []int{viewRunning, viewFailing, viewWaiting, viewRecent}
	platformViews = []int{viewOverview, viewSharedWorkflows, viewSharedActions, viewWarnings, viewScheduled, viewRunners, viewCaches, viewTime}
)

// viewNames are the names of the views, as the strip and the header show them.
var viewNames = map[int]string{viewFailing: "Failing", viewOverview: "Overview", viewSharedWorkflows: "Shared workflows",
	viewSharedActions: "Shared actions", viewWarnings: "Warnings", viewScheduled: "Scheduled", viewRunners: "Runners", viewCaches: "Caches", viewTime: "Time"}

// sharedFilter and sharedActionsFilter are the views of the shared workflows and actions.
const (
	sharedFilter        = viewSharedWorkflows
	sharedActionsFilter = viewSharedActions
)

// actFilterIcons are the icons of the views that list runs.
var actFilterIcons = []string{"gauge", "clock", "calendar"}

// actFilters are the views that list runs, from viewRunning on. Waiting
// holds the runs that wait for a runner and those that wait for you.
var actFilters = []actFilter{
	{"Running", kindIs(KRun), sAmber},
	{"Waiting", func(a *App, x repoRun, now time.Time) bool {
		return kindIs(KQueue)(a, x, now) || a.needsApproval(x, now)
	}, sWait},
	{"Last 7 days", within(7 * 24 * time.Hour), sText},
}

// listsRuns says view i is one of actFilters.
func listsRuns(i int) bool { return i >= viewRunning && i <= viewRecent }

// actSub is a view of the Actions tab that lists something else than runs.
type actSub interface {
	table() *Table
	sync(a *App)
	keys(a *App) []Binding
	chips(a *App) []string
	coverage(a *App) string
	listHead(a *App) string
	panel(a *App, w, h int) []string
	load(a *App, force bool) tea.Cmd
	busy(a *App) bool
	count(a *App) int
	SetFilter(a *App, q string)
	FilterValue() string
	FilterInfo(a *App) filterInfo
}

// newActivityView is the Actions place; it opens on what runs.
func newActivityView(a *App) *activityView {
	return newAcrossView(a, tabActions, actionsViews, viewRunning)
}

// newPlatformView is the Platform place; it opens on the Overview.
func newPlatformView(a *App) *activityView {
	return newAcrossView(a, tabPlatform, platformViews, viewOverview)
}

func newAcrossView(a *App, place int, views []int, filter int) *activityView {
	v := &activityView{id: a.newID(), place: place, views: views, read: map[int]bool{}, filter: filter, runs: map[string]repoRun{},
		shared: newSharedView(a, "", false), sharedActions: newSharedView(a, "", true)}
	v.failing, v.scheduled, v.warnings = newFailingView(v.repos), newScheduledView(), newWarningsView(v.repos)
	v.runners = newRunnersView(v.repos, func() []repoRun { return v.recent })
	v.time = newTimeView(v.repos)
	v.caches = newCachesView(v.repos)
	v.overview = newOverviewView(v)
	v.t = newTable(runColumns()...)
	v.t.Tickable = true
	v.t.Detail = func(r Row) []string {
		if x, ok := v.runs[r.Key]; ok {
			return runDetail(x)
		}
		return nil
	}
	return v
}

// runColumns are the columns of a list of runs across repos, as made by activityRow.
func runColumns() []Column {
	return []Column{
		{Title: "STATUS", Help: "how the run went, or why it has not finished: running, or waiting for a runner or for an approval"},
		{Title: "REPO", Max: 30, Flex: true, MidCut: true, Help: "the repository it ran in"},
		{Title: "WORKFLOW", Max: 26, Flex: true, Drop: 2, Help: "the workflow that ran"},
		{Title: "RUN", Right: true, Help: "the number of the run within its workflow"},
		{Title: "TITLE", Max: 50, Flex: true, Below: true, Help: "the commit message or pull request title it ran for"},
		{Title: "BRANCH", Max: 26, Flex: true, Drop: 1, Help: "the branch it ran on"},
		{Title: "ACTOR", Max: 18, Flex: true, Drop: 3, Help: "who started it"},
		{Title: "AGE", Right: true, Min: 3, Help: "how long ago it started"},
		{Title: "DURATION", Right: true, Min: 7, Help: "how long it took from start to end, or has taken so far, with a meter against its usual time while it runs"},
	}
}

// meterWidth is the cells of the meter beside the time of a running run.
const meterWidth = 5

// meterCell puts a thin meter before the time of a running run: lit for
// how far it is into its usual time, amber, or red all along when it runs
// far longer than usual.
func meterCell(took, usual time.Duration, over bool, clock Cell) Cell {
	lit := min(int(int64(took)*meterWidth/int64(usual))+1, meterWidth)
	c := cAmber
	if over {
		c, lit = cRed, meterWidth
	}
	on, off := "━", "─"
	if ascii {
		on, off = "=", "-"
	}
	var b strings.Builder
	for i := range meterWidth {
		if i < lit {
			b.WriteString(fg(lipgloss.Color(mix(string(cBg), string(c), 0.4+0.6*float64(i+1)/float64(lit)))).Render(on))
			continue
		}
		b.WriteString(fg(cFrame).Render(off))
	}
	return Cell{Text: strings.Repeat(on, meterWidth) + " " + clock.Text, Styled: b.String() + " " + clock.Styled}
}

// runDetail describes a run in full, for the strip under the table.
func runDetail(x repoRun) []string {
	r := x.run
	return []string{
		" " + sWhite.Bold(true).Render(x.repo) + sDim.Render(fmt.Sprintf(" · %s #%d · %s · %s", r.Name, r.RunNumber, r.HeadBranch, r.Actor.Login)),
		" " + sText.Render(r.DisplayTitle),
	}
}

// setFilter switches to view i and starts the list afresh.
func (v *activityView) setFilter(i int) {
	v.filter = i
	v.t.Top()
	if s := v.sub(); s != nil {
		s.table().Top()
	}
	v.restart()
}

// sub is the view on display when it lists something else than runs.
func (v *activityView) sub() actSub {
	switch v.filter {
	case viewFailing:
		return v.failing
	case viewScheduled:
		return v.scheduled
	case viewWarnings:
		return v.warnings
	case viewRunners:
		return v.runners
	case viewTime:
		return v.time
	case viewCaches:
		return v.caches
	case viewSharedWorkflows:
		return v.shared
	case viewSharedActions:
		return v.sharedActions
	case viewOverview:
		return v.overview
	}
	return nil
}

// subs are the views of the place that list something else than runs, by view.
func (v *activityView) subs() map[int]actSub {
	all := map[int]actSub{viewFailing: v.failing, viewScheduled: v.scheduled, viewWarnings: v.warnings, viewRunners: v.runners, viewTime: v.time, viewCaches: v.caches,
		viewSharedWorkflows: v.shared, viewSharedActions: v.sharedActions, viewOverview: v.overview}
	out := map[int]actSub{}
	for _, i := range v.views {
		if s, ok := all[i]; ok {
			out[i] = s
		}
	}
	return out
}

// has says view i is one of the place's.
func (v *activityView) has(i int) bool { return slices.Contains(v.views, i) }

// at is where the view on display sits in the place's strip.
func (v *activityView) at() int { return max(slices.Index(v.views, v.filter), 0) }

func (v *activityView) restart() {
	v.shown, v.matched, v.leaving = nil, nil, nil
	v.t.ResetWidths()
}

func (v *activityView) ID() int             { return v.id }
func (v *activityView) Crumb() string       { return tabNames[v.place] }
func (v *activityView) Init(a *App) tea.Cmd { return nil }

// KeepsUp: Actions reads its repos at their own pace whatever the live
// setting; see coverage.go.
func (v *activityView) KeepsUp() bool { return v.place == tabActions }

// The repos the tab can cover; "" is all of them.
const (
	scopeAll       = "all"       // every repo with workflows
	scopePinned    = "pinned"    // the pinned ones
	scopeConsumers = "consumers" // those that call your reusable workflows and actions
)

// repos are the repositories whose runs the tab shows.
func (v *activityView) repos(a *App) []string {
	switch v.scope {
	case scopePinned:
		return a.pinnedRepos()
	case scopeConsumers:
		return a.consumers()
	}
	var out []string
	for _, r := range a.ciRepos() {
		out = append(out, r.FullName)
	}
	return out
}

// consumers are the repos whose workflow files use a reusable workflow or an
// action of the owner of most of your repos, other than that one itself,
// most recently pushed first.
func (a *App) consumers() []string {
	owner := strings.ToLower(a.owner())
	calls := map[string]bool{}
	for _, u := range a.st.uses {
		target := strings.ToLower(u.TargetRepo())
		if owner != "" && strings.HasPrefix(target, owner+"/") && !strings.EqualFold(u.Repo, target) {
			calls[strings.ToLower(u.Repo)] = true
		}
	}
	var out []string
	for _, r := range a.ciRepos() {
		if calls[strings.ToLower(r.FullName)] {
			out = append(out, r.FullName)
		}
	}
	return out
}

// Updated is when the least recently read repo was read.
func (v *activityView) Updated(a *App) time.Time {
	var oldest time.Time
	for i, repo := range v.repos(a) {
		at := a.st.runState(repo).at
		if i == 0 || at.Before(oldest) {
			oldest = at
		}
	}
	return oldest
}

func (v *activityView) Loading(a *App) bool {
	return a.st.busyRuns() > 0 || (a.st.reposState.busy && len(a.repos) == 0) || (v.sub() != nil && v.sub().busy(a))
}

// repoListStale is how old the repo list may be before refreshing the tab
// reads it again in full; the push check keeps the push times up to date
// in between.
const repoListStale = 24 * time.Hour

// Refresh reads the runs of every repo in the tab when asked, and otherwise
// those due at the pace of how much goes on in them (see coverage.go).
// Which repos those are comes from the repo list, read first if it isn't
// known yet.
func (v *activityView) Refresh(a *App, force bool) tea.Cmd {
	now := time.Now()
	runs := v.load(a, force && now.Sub(a.st.reposState.at) > repoListStale, func(repo string) bool {
		return force || a.readDue(a.st.runState(repo), repo, now, 0)
	})
	if force {
		v.shown = nil // what you asked to see again is not held back
	}
	return tea.Batch(runs, v.loadSub(a, force))
}

// loadSub reads what the view on display lists, when it isn't runs. Failing
// is read too, so its count in the strip says whether something fails.
func (v *activityView) loadSub(a *App, force bool) tea.Cmd {
	if s := v.sub(); s != nil {
		v.read[v.filter] = true
		if v.filter == viewOverview {
			for _, i := range v.views {
				v.read[i] = true // the Overview reads them all
			}
		}
		return s.load(a, force)
	}
	if v.has(viewFailing) {
		v.read[viewFailing] = true
		return v.failing.load(a, force)
	}
	return nil
}

// RefreshStale reads only the repos whose runs are old, for when the tab is shown.
func (v *activityView) RefreshStale(a *App) tea.Cmd {
	now := time.Now()
	runs := v.load(a, false, func(repo string) bool {
		return now.Sub(a.st.runState(repo).at) > staleAfter && now.Sub(a.st.runState(repo).tried) > staleAfter
	})
	return tea.Batch(runs, v.loadSub(a, false))
}

// filteredRepos are the repos that have a run matching the text filter, or
// nil when no filter narrows the list.
func (v *activityView) filteredRepos() map[string]bool {
	if v.t.Filter() == "" {
		return nil
	}
	set := map[string]bool{}
	for _, key := range v.t.VisibleKeys() {
		if x, ok := v.runs[key]; ok {
			set[x.repo] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// RefreshScope says which part of the list a refresh reads, when not all of it.
func (v *activityView) RefreshScope(a *App) string {
	if set := v.filteredRepos(); set != nil {
		return fmt.Sprintf("%d repos matching the filter", len(set))
	}
	return ""
}

func (v *activityView) load(a *App, readRepoList bool, due func(repo string) bool) tea.Cmd {
	only := v.filteredRepos()
	var cmds []tea.Cmd
	if !a.st.loaded || (readRepoList && only == nil) {
		cmds = append(cmds, a.st.loadRepos(a))
	}
	// The workflow files say which repos run on a schedule (and, for the
	// consumers, who calls what); they are read once a day.
	cmds = append(cmds, a.st.loadUses(a, false))
	// Busy repos and the ones worked on lately are read first; the untouched
	// ones, which can be hundreds, spaced out behind them.
	now := time.Now()
	var first, later []string
	for _, repo := range v.repos(a) {
		if only != nil && !only[repo] {
			continue
		}
		if !due(repo) {
			continue
		}
		if a.readInfoOf(repo, now).group == readUntouched {
			later = append(later, repo)
		} else {
			first = append(first, repo)
		}
	}
	ctx := context.Background()
	return tea.Batch(append(cmds, a.st.loadRuns(a, ctx, first), a.st.loadRuns(a, gh.Background(ctx), later))...)
}

// RefreshRepo re-reads one repo's runs, after an action in it, and what the
// view on display holds of it.
func (v *activityView) RefreshRepo(a *App, repo string) tea.Cmd {
	runs := a.st.loadRuns(a, context.Background(), []string{repo})
	if r, ok := v.sub().(interface{ refreshRepo(*App, string) tea.Cmd }); ok {
		return tea.Batch(runs, r.refreshRepo(a, repo))
	}
	return runs
}

// activeEvery is how often the runners of a busy view are read again.
const activeEvery = 15 * time.Second

// repoActive reports whether repo has a run that hasn't finished.
func repoActive(a *App, repo string) bool {
	for _, r := range a.st.status[repo] {
		if r.Active() {
			return true
		}
	}
	return false
}

func (v *activityView) pickScope(a *App) tea.Cmd {
	current := v.scope
	if current == "" {
		current = scopeAll
	}
	consumers := "the repos whose workflow files use one of yours"
	if a.st.usesState.at.IsZero() {
		consumers += " · reads the workflow files first"
	} else {
		consumers = repositories(len(a.consumers())) + " whose workflow files use one of yours"
	}
	items := []pickItem{
		{value: scopeAll, label: "all repos with workflows", note: sDim.Render(repositories(len(a.ciRepos())) + " · each read at the pace of how much goes on in it")},
		{value: scopePinned, label: "pinned repos", note: sDim.Render(repositories(len(a.pinnedRepos())))},
		{value: scopeConsumers, label: "repos that call your workflows and actions", note: sDim.Render(consumers)},
	}
	return openModal(newPicker("actions-scope", "Show runs of", current, items, func(s string) tea.Cmd {
		if s != current {
			v.scope = s
			v.restart()
			return v.Refresh(a, false)
		}
		return nil
	}))
}
func (v *activityView) SetFilter(a *App, q string) {
	if s := v.sub(); s != nil {
		s.SetFilter(a, q)
		return
	}
	v.t.SetFilter(q, a.me)
}

func (v *activityView) FilterValue() string {
	if s := v.sub(); s != nil {
		return s.FilterValue()
	}
	return v.t.Filter()
}

func (v *activityView) FilterInfo(a *App) filterInfo {
	if s := v.sub(); s != nil {
		return s.FilterInfo(a)
	}
	info := v.t.Info()
	info.runs = true
	if a.filtering() {
		_, info.hidden = a.hiddenBy(a.cfg.RunFilter)
	}
	return info
}

func (v *activityView) Keys(a *App) []Binding {
	if s := v.sub(); s != nil {
		bs := append(s.keys(a), v.viewKey(a), v.scopeKey(a), filterHint, s.table().moveKeys())
		return grouped("ACTIONS", bs...)
	}
	var bs []Binding
	if ticked := v.ticked(); len(ticked) > 0 {
		bs = append(bs, a.bulkRunKeys(v.t, ticked)...)
	}
	if v.held > 0 {
		bs = append(bs, Binding{Keys: []string{"n"}, Desc: fmt.Sprintf("show %d new", v.held), Help: "show the runs held back",
			Do: do(func() tea.Cmd { v.shown = nil; v.t.Top(); return nil })})
	}
	if x, ok := v.selected(); ok {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })})
		acts := a.runKeys(x.repo, x.run)
		bs = append(bs, without(acts, "o", "y")...)
		bs = append(bs, Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })},
			a.runRepoKey(x.repo, x.run))
		bs = append(bs, only(acts, "o", "y")...)
	}
	bs = append(bs, runTickKeys(v.t)...)
	mine := "only mine"
	if v.mine {
		mine = "everyone"
	}
	bs = append(bs,
		v.viewKey(a),
		v.scopeKey(a),
		Binding{Keys: []string{"u"}, Desc: mine, Help: "only my runs / everyone's", Do: do(func() tea.Cmd {
			if a.me == "" {
				return a.flashCmd(nHint, "Still loading your GitHub user, try again in a moment.")
			}
			v.mine = !v.mine
			v.restart()
			return nil
		})},
		filterHint,
		a.compactKey(),
		v.t.moveKeys(),
	)
	return grouped("ACTIONS", bs...)
}

func (v *activityView) viewKey(a *App) Binding {
	return viewKeys("next view", "next view", "", func(d int) tea.Cmd {
		n := len(v.views)
		v.setFilter(v.views[(v.at()+d+n)%n])
		return v.loadSub(a, false)
	})
}

func (v *activityView) scopeKey(a *App) Binding {
	return Binding{Keys: []string{"p"}, Desc: "which repos", Help: "which repos: active, all, or those that call yours",
		Do: do(func() tea.Cmd { return v.pickScope(a) })}
}

func (v *activityView) Update(a *App, msg tea.Msg) tea.Cmd {
	if cmd, ok := v.peek.handle(a, v.id, msg); ok {
		return cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		if x, ok := v.selected(); ok && v.sub() == nil {
			return tea.Batch(cmd, v.peek.ask(a, v.id, x.repo, &x.run))
		}
		if m, ok := v.sub().(interface{ afterMove(*App) tea.Cmd }); ok {
			return tea.Batch(cmd, m.afterMove(a))
		}
		return cmd
	}
	return nil
}

// ticked are the ticked runs still listed.
func (v *activityView) ticked() []repoRun {
	var out []repoRun
	for _, key := range v.t.Marks() {
		if x, ok := v.runs[key]; ok {
			out = append(out, x)
		}
	}
	return out
}

// ClearMarks unticks the ticked runs (esc does it before it goes back).
func (v *activityView) ClearMarks() bool { return v.sub() == nil && v.t.ClearMarks() }

func (v *activityView) selected() (repoRun, bool) {
	r := v.t.Selected()
	if r == nil {
		return repoRun{}, false
	}
	x, ok := v.runs[r.Key]
	return x, ok
}

// mineOK says x is shown with "only mine" on: you started it, or ran it again.
func (v *activityView) mineOK(a *App, x repoRun) bool {
	return !v.mine || strings.EqualFold(x.run.Actor.Login, a.me) || strings.EqualFold(x.run.TriggeringActor.Login, a.me)
}

func (v *activityView) rebuild(a *App, all []repoRun, now time.Time) {
	f := actFilters[v.filter-viewRunning]
	// Only a long list holds new runs back: in the short ones a new run is
	// what you are watching for.
	hold := v.shown != nil && v.t.Cursor() > 0 && v.filter == viewRecent
	var rows []Row
	runs := map[string]repoRun{}
	shown := map[string]bool{}
	matched := map[string]bool{}
	leaving := map[string]time.Time{}
	drawn := map[string]drawnRow{}
	v.held = 0
	for _, x := range all {
		if !v.mineOK(a, x) {
			continue
		}
		key := fmt.Sprint(x.run.ID)
		fading := false
		if f.match(a, x, now) {
			matched[key] = true
		} else {
			since, ok := v.leaving[key]
			if !ok && v.matched[key] {
				since, ok = now, true
			}
			if !ok || now.Sub(since) >= fadeOut {
				continue
			}
			leaving[key] = since
			fading = true
		}
		if hold && !v.shown[key] {
			v.held++
			continue
		}
		shown[key] = true
		runs[key] = x
		rows = append(rows, v.row(a, key, x, now, fading, drawn))
	}
	v.runs, v.shown, v.matched, v.leaving, v.drawn = runs, shown, matched, leaving, drawn
	v.t.Card = func(r Row, w int) (string, string) {
		x := runs[r.Key]
		return runCard(a, x.repo, x.run, nil, w, now)
	}
	v.t.Empty = "No runs match “" + f.label + "”."
	if !a.st.loaded {
		v.t.Empty = "Loading…"
	}
	v.t.SetRows(rows)
}

// row is the row of x, the one drawn last time when x has finished and
// nothing it shows changed; it is noted in drawn for the next rebuild.
func (v *activityView) row(a *App, key string, x repoRun, now time.Time, fading bool, drawn map[string]drawnRow) Row {
	if x.run.Active() {
		return activityRow(a, key, x, now, fading)
	}
	from := rowSource{x.run.Status, x.run.Conclusion, x.run.UpdatedAt, a.short(x.repo), age(x.run.CreatedAt, now), fading, currentTheme}
	d, ok := v.drawn[key]
	if !ok || d.from != from {
		d = drawnRow{from, activityRow(a, key, x, now, fading)}
	}
	drawn[key] = d
	return d.row
}

func activityRow(a *App, key string, x repoRun, now time.Time, fading bool) Row {
	r := x.run
	k := StatusKind(r.Status, r.Conclusion)
	quiet := fading || k.RowColor() == cDim
	took := steelCell(dur(r.Duration(now)), quiet)
	if r.Active() {
		took = Cell{Text: took.Text, Styled: sAmber.Render(took.Text)}
	}
	usual, over := a.overrunning(x, now)
	if over {
		took = Cell{Text: took.Text, Styled: sRed.Bold(true).Render(took.Text)}
	}
	if r.Active() && usual > 0 {
		took = meterCell(r.Duration(now), usual, over, took)
	}
	row := Row{Key: key, Kind: k, Cells: []Cell{
		statusCell(k, a.frame, ""),
		iconText("repo", a.short(x.repo), quiet),
		iconText("workflow", r.Name, quiet),
		numberCell(r.RunNumber, quiet),
		requeuedTitle(r, quiet),
		iconText("branch", r.HeadBranch, quiet),
		iconText("user", r.Actor.Login, quiet),
		steelCell(age(r.CreatedAt, now), quiet),
		took,
	}}
	if quiet {
		row.Cells[0] = Cell{Text: row.Cells[0].Text, Styled: sDim.Render(row.Cells[0].Text)}
	}
	if fading {
		dim := KSkip
		row.Color = &dim
	}
	return row
}

// requeuedTitle is the title of a run, saying so when its next job waits for a runner.
func requeuedTitle(r gh.Run, quiet bool) Cell {
	c := titleCell(r.DisplayTitle, quiet)
	if r.Requeued {
		note := " · " + waitsForRunner
		c.Text += note
		c.Styled += sDim.Render(note)
	}
	return c
}

// waitsForRunner is what a run that GitHub calls queued again is waiting for.
const waitsForRunner = "next job waits for a runner"

// sync gathers the runs of the tab's repos and lists the ones the view shows.
func (v *activityView) sync(a *App) {
	v.recent = recentRuns(a, v.repos(a))
	for i, s := range v.subs() {
		if i != viewOverview {
			s.sync(a)
		}
	}
	if v.has(viewOverview) {
		v.overview.sync(a) // after the views whose findings it lists
	}
	if v.sub() != nil {
		return
	}
	v.rebuild(a, v.recent, time.Now())
}

func (v *activityView) Render(a *App, w, h int) string {
	now := time.Now()
	all := v.recent
	v.t.Col("ACTOR").Hidden = v.mine
	v.t.TwoLine = a.twoLine
	v.t.Cards = !a.compact
	v.t.HideDetail = w >= panelMinWidth

	items := v.stripItems(a, all, now)

	// the chips: what is going on across these repos
	ok7, fail7, running, waiting, failedToday, long := 0, 0, 0, 0, 0, 0
	var finished []gh.Run
	for _, x := range all {
		if !v.mineOK(a, x) {
			continue
		}
		k := StatusKind(x.run.Status, x.run.Conclusion)
		switch {
		case k == KRun:
			running++
			if _, over := a.overrunning(x, now); over {
				long++
			}
		case a.needsApproval(x, now):
			waiting++
		}
		if k == KFail && now.Sub(x.run.CreatedAt) < 24*time.Hour {
			failedToday++
		}
		if now.Sub(x.run.CreatedAt) < 7*24*time.Hour {
			switch k {
			case KOK:
				ok7++
				finished = append(finished, x.run)
			case KFail:
				fail7++
			}
		}
	}
	var chips []string
	if running > 0 {
		chips = append(chips, statusChip(KRun, a.frame, fmt.Sprintf("%d running", running)))
	}
	if long > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d running long", long)))
	}
	if waiting > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d awaiting approval", waiting)))
	}
	if failedToday > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failed today", failedToday)))
	}
	if ok7+fail7 > 0 {
		pct := ok7 * 100 / (ok7 + fail7)
		chips = append(chips, rateStyle(pct).Bold(true).Render(fmt.Sprintf("%d%% passing", pct))+" "+meter(pct, 8))
	}
	if med := medianDuration(finished); med > 0 {
		chips = append(chips, fact("clock", "median "+dur(med)))
	}

	repos := v.repos(a)
	scope := fmt.Sprintf("%d across %d repos", v.t.Len(), len(repos))
	if v.mine {
		scope = "mine · " + scope
	}
	if span, ok := sampleSpan(a, repos, now); ok && span < 7*24*time.Hour {
		scope += " · complete for the last " + age(now.Add(-span), now)
	}
	if v.held > 0 {
		scope += fmt.Sprintf(" · ↑ %d new, n shows", v.held)
	}
	which := fmt.Sprintf("all %d repos with workflows", len(repos))
	switch v.scope {
	case scopePinned:
		which = fmt.Sprintf("%d pinned repos", len(repos))
	case scopeConsumers:
		which = fmt.Sprintf("%d repos that call yours", len(repos))
	}
	scopeChip := " " + chip("p", "repos: "+which+" ▾", v.scope != "" && v.scope != scopeAll) + "   "
	// Under it, on Actions, how many of those repos are busy, worked on
	// lately or untouched, which says how often each is read.
	coverage := func(lw int) (string, int) {
		if v.place != tabActions {
			return "", 0
		}
		return "\n " + clip(a.coverageWords(repos, now), lw-1), 1
	}
	if s := v.sub(); s != nil && v.place == tabPlatform {
		return v.renderPlatform(a, s, items, w, h)
	}
	if s := v.sub(); s != nil {
		return listWithPanel(a, w, h,
			[]string{screenTitle(tabIcons[v.place], v.viewLabel(), s.coverage(a), -1, s.chips(a), w), viewStrip(items, v.at(), w)},
			func(lw, lh int) string {
				cov, n := coverage(lw)
				if c, ok := s.(interface{ renderList(*App, int, int) string }); ok {
					return scopeChip + s.listHead(a) + cov + "\n" + c.renderList(a, lw, lh-1-n)
				}
				return scopeChip + s.listHead(a) + cov + "\n" + s.table().Render(lw, lh-1-n)
			},
			func(pw, ph int) []string { return s.panel(a, pw, ph) })
	}
	scopeLine := scopeChip + a.filterLine(v.t.Filter(), v.FilterInfo(a))
	if n := len(v.t.Marks()); n > 0 {
		scopeLine = tickedRunsLine(n, w)
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle(tabIcons[v.place], v.viewLabel(), scope, -1, chips, w), viewStrip(items, v.at(), w)},
		func(lw, lh int) string {
			cov, n := coverage(lw)
			return scopeLine + cov + "\n" + v.withAfterglow(a, all, now, lw, lh-1-n)
		},
		func(pw, ph int) []string {
			if x, ok := v.selected(); ok {
				return a.runPanel(x, pw, ph)
			}
			return emptyPanel(pw, ph, "No run selected.")
		})
}

// withAfterglow is the table in h lines, and on Running, when the list is
// short, what just finished and the day's runs under it.
func (v *activityView) withAfterglow(a *App, all []repoRun, now time.Time, w, h int) string {
	table := v.t.Render(w, h)
	if v.filter != viewRunning {
		return table
	}
	lines := strings.Split(table, "\n")
	used := len(lines)
	for used > 0 && ansiStrip(lines[used-1]) == "" {
		used--
	}
	if h-used < afterglowMin {
		return table
	}
	out := append(lines[:used], v.afterglow(a, all, now, w, h-used)...)
	return strings.Join(out, "\n")
}

// stripItems are the views of the place with how many things each holds,
// or … while a view has not been read yet.
func (v *activityView) stripItems(a *App, all []repoRun, now time.Time) []stripItem {
	byView := map[int]stripItem{}
	n := v.failing.count(a)
	failing := stripItem{Label: "Failing", Icon: "fail", Count: v.countText(a, viewFailing, n)}
	if n > 0 {
		failing.St = &sRed
	}
	byView[viewFailing] = failing
	for i, f := range actFilters {
		n := 0
		for _, x := range all {
			if f.match(a, x, now) && v.mineOK(a, x) {
				n++
			}
		}
		it := stripItem{Label: f.label, Icon: actFilterIcons[i], Count: v.countText(a, viewRunning+i, n)}
		if n > 0 {
			switch viewRunning + i {
			case viewRunning:
				it.St = &sAmber
			case viewWaiting:
				it.St = &sWait
			}
		}
		byView[viewRunning+i] = it
	}
	byView[viewScheduled] = stripItem{Label: "Scheduled", Icon: "calendar", Count: v.countText(a, viewScheduled, v.scheduled.count(a))}
	warn := stripItem{Label: "Warnings", Icon: "warning", Count: v.countText(a, viewWarnings, v.warnings.count(a))}
	if v.warnings.count(a) > 0 {
		warn.St = &sWait
	}
	byView[viewWarnings] = warn
	runners := stripItem{Label: "Runners", Icon: "server", Count: v.countText(a, viewRunners, v.runners.count(a))}
	if len(v.runners.waits) > 0 {
		runners.St = &sAmber
	}
	byView[viewRunners] = runners
	caches := stripItem{Label: "Caches", Icon: "archive", Count: v.countText(a, viewCaches, v.caches.count(a))}
	if f, _ := v.caches.counts(); f > 0 {
		caches.St = &sRed
	} else if _, n := v.caches.counts(); n > 0 {
		caches.St = &sWait
	}
	byView[viewCaches] = caches
	timeCount := v.countText(a, viewTime, int(v.time.total))
	if timeCount != notReadYet && v.time.total > 0 {
		timeCount = v.time.stripCount()
	}
	byView[viewTime] = stripItem{Label: "Time", Icon: "clock", Count: timeCount}
	byView[viewSharedWorkflows] = stripItem{Label: "Shared workflows", Icon: "workflow", Count: v.countText(a, viewSharedWorkflows, v.shared.count(a))}
	byView[viewSharedActions] = stripItem{Label: "Shared actions", Icon: "code", Count: v.countText(a, viewSharedActions, v.sharedActions.count(a))}
	overview := stripItem{Label: "Overview", Icon: "gauge", Count: v.countText(a, viewOverview, v.overview.count(a))}
	if f, _ := v.overview.sections(); f > 0 {
		overview.St = &sRed
	} else if v.overview.count(a) > 0 {
		overview.St = &sWait
	}
	byView[viewOverview] = overview
	var items []stripItem
	for _, i := range v.views {
		items = append(items, byView[i])
	}
	return items
}

// notReadYet stands in the strip for the count of a view not read yet.
const notReadYet = "…"

// countText is how many things view i holds, as its strip shows it: … until
// what it lists has been read, so nothing reads as none before it is known.
func (v *activityView) countText(a *App, i, n int) string {
	if listsRuns(i) || i == viewFailing {
		if !a.st.loaded {
			return notReadYet
		}
		return fmt.Sprint(n)
	}
	s := v.subs()[i]
	if n == 0 && (!v.read[i] || (s != nil && s.busy(a))) {
		return notReadYet
	}
	return fmt.Sprint(n)
}

// sampleSpan is how far back the runs we hold of repos are complete: each
// repo is read for its latest runs only, so a busy one reaches less far.
// ok is false when no repo has more runs than we hold.
func sampleSpan(a *App, repos []string, now time.Time) (time.Duration, bool) {
	var span time.Duration
	full := false
	for _, repo := range repos {
		runs := a.st.status[repo]
		if len(runs) < repoStatusFilter.PerPage {
			continue
		}
		d := now.Sub(runs[len(runs)-1].CreatedAt)
		if !full || d < span {
			span, full = d, true
		}
	}
	return span, full
}

// A view of the Platform place can say more about itself than a view of
// Actions: what it lists in a line under its title, the way the list is
// shown (sorted, grouped) instead of the keys that change it, and a note
// under the list.
type (
	subtitler interface {
		subtitle(a *App, repos string) string
	}
	stater    interface{ state(a *App) []string }
	footnoter interface{ footnote(a *App) string }
)

// renderPlatform draws a view of the Platform place: its title with the
// chips, a line saying what it lists, the strip of views, how the list is
// shown, the list, and the panel at a width that does not change from view
// to view.
func (v *activityView) renderPlatform(a *App, s actSub, items []stripItem, w, h int) string {
	name := v.viewLabel()
	if v.filter == viewOverview {
		name = tabNames[v.place]
	}
	sub := ""
	if st, ok := s.(subtitler); ok {
		sub = st.subtitle(a, v.reposWords(a))
	}
	head := []string{screenTitle(tabIcons[v.place], name, "", -1, s.chips(a), w), " " + sDim.Render(clip(sub, max(w-2, 1))), viewStrip(items, v.at(), w)}
	return listWithSteadyPanel(w, h, head,
		func(lw, lh int) string {
			line := ""
			if st, ok := s.(stater); ok {
				line = stateLine(st.state(a))
			}
			if q := s.FilterValue(); q != "" {
				line = joinState(line, sCyan.Render("Filter: ")+sWhite.Render(q)+sDim.Render(fmt.Sprintf(" · showing %d of %d", s.FilterInfo(a).shown, s.FilterInfo(a).total)))
			}
			lh--
			note := ""
			if f, ok := s.(footnoter); ok {
				if note = f.footnote(a); note != "" {
					lh -= 2
				}
			}
			var list string
			if c, ok := s.(interface{ renderList(*App, int, int) string }); ok {
				list = c.renderList(a, lw, lh)
			} else {
				list = s.table().Render(lw, lh)
			}
			out := " " + clip(line, max(lw-2, 1)) + "\n" + list
			if note != "" {
				out += "\n\n " + clip(note, max(lw-2, 1))
			}
			return out
		},
		func(pw, ph int) []string { return s.panel(a, pw, ph) })
}

// reposWords names the repos the place covers, in words.
func (v *activityView) reposWords(a *App) string {
	n := len(v.repos(a))
	switch v.scope {
	case scopePinned:
		return fmt.Sprintf("the %d pinned %s", n, plural("repository", n))
	case scopeConsumers:
		return fmt.Sprintf("the %d %s that call yours", n, plural("repository", n))
	}
	return fmt.Sprintf("all %d %s with workflows", n, plural("repository", n))
}

// stateLine says how a list is shown: "Sorted by most used · Grouped by source".
func stateLine(parts []string) string {
	line := ""
	for _, p := range parts {
		line = joinState(line, sDim.Render(p))
	}
	return line
}

func joinState(line, part string) string {
	if line == "" {
		return part
	}
	return line + sDim.Render("  ·  ") + part
}
