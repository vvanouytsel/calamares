package ui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

type jobProgress struct{ done, total, failed int }

type runsView struct {
	id       int
	repo     string
	wf       *gh.Workflow
	branch   string
	mine     bool
	status   string // a run status or conclusion, see statusChoices; "" for any
	sha      string // only the runs of this commit, when set
	t        *Table
	runs     []gh.Run
	total    int
	progress map[int64]jobProgress
	loaded   bool
	busy     bool
	more     bool // loading an older page
	nextPage int  // next older page to fetch
	gen      int  // bumped when the filter changes; older responses are dropped
	last     time.Time
	progAt   time.Time
	poll     backoff
	at       time.Time // when the newest page was last read
	lastPage string    // fingerprint of the newest page, to tell whether a poll found anything
	// onBranch, when set, hears about a new branch filter (the repo screen
	// keeps it for every workflow).
	onBranch func(branch string)
	// onRuns, when set, hears that the list was read again.
	onRuns func() tea.Cmd
}

// statusChoices are the status filters offered, in the API's terms.
var statusChoices = []struct{ value, label string }{
	{"", "any status"},
	{"failure", "failed"},
	{"success", "passed"},
	{"in_progress", "running"},
	{"queued", "queued"},
	{"waiting", "waiting"},
	{"cancelled", "canceled"},
}

func statusLabel(value string) string {
	for _, c := range statusChoices {
		if c.value == value {
			return c.label
		}
	}
	return value
}

type runsMsg struct {
	id    int
	gen   int
	runs  []gh.Run
	total int
	page  int
	err   error
}

const runsPageSize = 30

// runCard draws a run as two lines: its state and title, with how it stands
// at the right; then where it comes from, with when and how long. repo is
// named on the second line when the list covers several.
func runCard(a *App, repo string, r gh.Run, p *jobProgress, w int, now time.Time) (string, string) {
	k := StatusKind(r.Status, r.Conclusion)
	name, state := sText, fg(k.Color())
	var how, when string
	switch k {
	case KRun:
		name, how = sWhite.Bold(true), "running "+dur(r.Duration(now))
	case KQueue:
		name, how = sWhite.Bold(true), "queued "+age(r.CreatedAt, now)
	case KWait:
		name, how = sWhite.Bold(true), "waiting"
		if r.Status == "waiting" {
			how = "needs approval"
		}
	case KFail:
		name, how = sWhite.Bold(true), "failed"
	case KOK:
		how = "passed"
	default:
		name, state, how = sGrey, sDim, k.Label()
	}
	if r.Active() {
		when = "started " + age(r.CreatedAt, now) + " ago"
		if p != nil && p.total > 0 {
			when, _ = progress(p.done, p.total, p.failed)
		}
	} else {
		when = sText.Render(age(r.CreatedAt, now) + " ago · " + dur(r.Duration(now)))
	}
	right1, right2 := state.Render(how), when
	if r.Active() && (p == nil || p.total == 0) {
		right2 = sText.Render(when)
	}

	from := ""
	if repo != "" {
		from = sText.Render(a.short(repo)) + sDim.Render(" · ")
	}
	from += sText.Render(r.Name) + sDim.Render(fmt.Sprintf(" #%d", r.RunNumber))
	if r.RunAttempt > 1 {
		from += sTan.Render(fmt.Sprintf(" ↻%d", r.RunAttempt))
	}
	from += sDim.Render(" · ") + fact("branch", r.HeadBranch) + sDim.Render(" · ") + sText.Render(r.Actor.Login)

	first := " " + glyph(k, a.frame) + " " + name.Render(r.DisplayTitle)
	second := "   " + from
	return fit(first, max(w-width(right1)-2, 0)) + " " + right1 + " ", fit(second, max(w-width(right2)-2, 0)) + " " + right2 + " "
}

// runsFilter is the exact request the runs view makes, so its answer is
// found in the cache next time.
func runsFilter(wf *gh.Workflow, branch string, mine bool, me, status string) gh.RunFilter {
	f := gh.RunFilter{PerPage: runsPageSize, Branch: branch, Status: status}
	if wf != nil {
		f.WorkflowID = wf.ID
	}
	if mine {
		f.Actor = me
	}
	return f
}

type runProgressMsg struct {
	id    int
	runID int64
	p     jobProgress
}

func (m runsMsg) viewID() int        { return m.id }
func (m runProgressMsg) viewID() int { return m.id }

// newCommitRunsView lists the runs that were started for one commit.
func newCommitRunsView(a *App, repo, sha string) *runsView {
	v := newRunsView(a, repo, nil, "")
	v.sha = sha
	v.t.Empty = "Loading runs…"
	return v
}

func newRunsView(a *App, repo string, wf *gh.Workflow, branch string) *runsView {
	v := &runsView{id: a.newID(), repo: repo, wf: wf, branch: branch, progress: map[int64]jobProgress{}, nextPage: 2,
		poll: newBackoff(a.liveEvery(), maxLiveEvery)}
	cols := []Column{
		{Title: "STATUS", Help: "how the run went, or why it has not finished: running, or waiting for a runner or for an approval"},
		{Title: "RUN", Right: true, Help: "the number of the run within its workflow"},
		{Title: "TITLE", Max: 60, Flex: true, Below: true, Help: "the commit message or pull request title it ran for"},
	}
	if wf == nil {
		cols = append(cols, Column{Title: "WORKFLOW", Max: 28, Flex: true, Drop: 2, Help: "the workflow that ran"})
	}
	cols = append(cols,
		Column{Title: "BRANCH", Max: 30, Flex: true, Drop: 1, Help: "the branch it ran on"},
		Column{Title: "ACTOR", Max: 20, Flex: true, Drop: 3, Help: "who started it"},
		Column{Title: "AGE", Right: true, Min: 3, Help: "how long ago it started"},
		Column{Title: "DURATION", Right: true, Help: "how long it took from start to end, or has taken so far"},
		Column{Title: "JOBS", HideEmpty: true, Help: "what its jobs wait for, such as your approval"},
	)
	v.t = newTable(cols...)
	v.t.Tickable = true
	v.t.Empty = "Loading runs…"
	return v
}

func (v *runsView) ID() int          { return v.id }
func (v *runsView) RepoName() string { return v.repo }
func (v *runsView) Crumb() string {
	if v.sha != "" {
		return v.sha[:min(7, len(v.sha))]
	}
	if v.wf != nil {
		return v.wf.Name
	}
	return "runs"
}

// Init shows the cached list at once, then refreshes it.
func (v *runsView) Init(a *App) tea.Cmd {
	v.seed(a)
	return v.load(a)
}

func (v *runsView) filter(a *App) gh.RunFilter {
	f := runsFilter(v.wf, v.branch, v.mine, a.me, v.status)
	f.HeadSHA = v.sha
	return f
}

// seed fills the list from the response cache, without any request.
func (v *runsView) seed(a *App) {
	if runs, total, ok := a.client.CachedRuns(v.repo, v.filter(a)); ok {
		v.runs, v.total, v.loaded = runs, total, true
		v.at, _ = a.client.CachedRunsAt(v.repo, v.filter(a))
		v.rebuild(a)
	}
}

// load refreshes the newest page; older pages already shown are kept.
func (v *runsView) load(a *App) tea.Cmd {
	v.busy = true
	v.last = time.Now()
	f := v.filter(a)
	id, repo, gen := v.id, v.repo, v.gen
	ctx := context.Background()
	if len(v.runs) > 0 { // a refresh of what's on screen, not something you're waiting for
		ctx = gh.Background(ctx)
	}
	return func() tea.Msg {
		runs, total, err := a.client.ListRuns(ctx, repo, f)
		return runsMsg{id, gen, runs, total, 1, err}
	}
}

// resetFilter starts over after the branch or "mine" filter changed.
func (v *runsView) resetFilter(a *App) tea.Cmd {
	v.gen++
	v.t.ResetWidths()
	v.runs, v.total, v.more, v.nextPage = nil, 0, false, 2
	v.t.SetRows(nil)
	v.t.Empty = "Loading runs…"
	return v.load(a)
}

// loadMore fetches the next page when the cursor reaches the bottom.
func (v *runsView) loadMore(a *App) tea.Cmd {
	if v.more || len(v.runs) >= v.total || len(v.runs) == 0 {
		return nil
	}
	v.more = true
	f := v.filter(a)
	f.Page = v.nextPage
	id, repo, gen := v.id, v.repo, v.gen
	return func() tea.Msg {
		runs, total, err := a.client.ListRuns(context.Background(), repo, f)
		return runsMsg{id, gen, runs, total, f.Page, err}
	}
}

// merge folds a fetched page into the list by run ID: a refreshed first page
// replaces the runs it contains and keeps every older run already loaded; an
// older page appends the runs not seen yet.
func (v *runsView) merge(page int, runs []gh.Run) {
	if page > 1 {
		seen := map[int64]bool{}
		for _, r := range v.runs {
			seen[r.ID] = true
		}
		for _, r := range runs {
			if !seen[r.ID] {
				v.runs = append(v.runs, r)
			}
		}
		return
	}
	fresh := map[int64]bool{}
	for _, r := range runs {
		fresh[r.ID] = true
	}
	merged := append([]gh.Run{}, runs...)
	for _, r := range v.runs {
		if !fresh[r.ID] {
			merged = append(merged, r)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].CreatedAt.After(merged[j].CreatedAt) })
	v.runs = merged
}

// progressEvery is how often job counts of running runs on screen refresh.
const progressEvery = 30 * time.Second

// loadProgress fetches the job counts of the running runs on screen.
func (v *runsView) loadProgress(a *App) tea.Cmd {
	if time.Since(v.progAt) < progressEvery {
		return nil
	}
	visible := map[string]bool{}
	for _, k := range v.t.VisibleKeys() {
		visible[k] = true
	}
	var cmds []tea.Cmd
	id, repo := v.id, v.repo
	for _, r := range v.runs {
		if !r.Active() || !visible[fmt.Sprint(r.ID)] {
			continue
		}
		runID := r.ID
		cmds = append(cmds, func() tea.Msg {
			jobs, err := a.client.ListJobs(gh.Background(context.Background()), repo, runID)
			if err != nil {
				return nil
			}
			var p jobProgress
			for _, j := range jobs {
				p.total++
				if j.Status == "completed" {
					p.done++
					if StatusKind(j.Status, j.Conclusion) == KFail {
						p.failed++
					}
				}
			}
			return runProgressMsg{id, runID, p}
		})
	}
	if len(cmds) > 0 {
		v.progAt = time.Now()
	}
	return tea.Batch(cmds...)
}

func (v *runsView) active() bool {
	for _, r := range v.runs {
		if r.Active() {
			return true
		}
	}
	return false
}

// Refresh reloads the newest page: often while runs are going, backing off
// while nothing changes, and once a minute otherwise.
func (v *runsView) Refresh(a *App, force bool) tea.Cmd {
	if v.busy {
		return nil
	}
	every := time.Minute
	if v.active() {
		every = v.poll.every()
	}
	if force || time.Since(v.last) > every {
		return v.load(a)
	}
	return nil
}

func (v *runsView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case runsMsg:
		if msg.gen != v.gen {
			return nil // answer to a filter that is no longer active
		}
		if msg.page > 1 {
			v.more = false
			if msg.err == nil {
				v.nextPage = msg.page + 1
			}
		} else {
			v.busy = false
		}
		v.loaded = true
		if msg.err != nil {
			v.t.Empty = "Could not load runs: " + msg.err.Error()
			if len(v.runs) == 0 {
				v.t.SetRows(nil)
			}
			return nil
		}
		if msg.page == 1 {
			v.at = time.Now()
			fp := runsFingerprint(msg.runs)
			v.poll.seen(fp != v.lastPage)
			v.lastPage = fp
		}
		v.merge(msg.page, msg.runs)
		v.total = msg.total
		v.t.Empty = "No runs yet."
		if v.sha != "" {
			v.t.Empty = "No workflow runs for this commit."
		}
		if v.branch != "" || v.mine || v.status != "" {
			v.t.Empty = "No runs match these filters (b branch · u mine · s status)."
		}
		v.rebuild(a)
		if v.onRuns != nil {
			return tea.Batch(v.loadProgress(a), v.onRuns())
		}
		return v.loadProgress(a)
	case runProgressMsg:
		v.progress[msg.runID] = msg.p
		v.rebuild(a)
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

// MenuTitle names the selected run for the action menu.
func (v *runsView) MenuTitle(a *App) string {
	if r := v.selected(); r != nil {
		return fmt.Sprintf("%s #%d", r.Name, r.RunNumber)
	}
	return ""
}

func (v *runsView) selected() *gh.Run {
	r := v.t.Selected()
	if r == nil {
		return nil
	}
	for i := range v.runs {
		if fmt.Sprint(v.runs[i].ID) == r.Key {
			return &v.runs[i]
		}
	}
	return nil
}

func (v *runsView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *runsView) Keys(a *App) []Binding {
	var bs []Binding
	if ticked := v.ticked(); len(ticked) > 0 {
		bs = append(bs, a.bulkRunKeys(v.t, ticked)...)
	}
	if run := v.selected(); run != nil {
		r := *run
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, r)) })})
		acts := a.runKeys(v.repo, r)
		bs = append(bs, without(acts, "o", "y")...)
		bs = append(bs, Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, v.repo, r, nil, 0)) })})
		bs = append(bs, only(acts, "o", "y")...)
	}
	bs = append(bs,
		Binding{Keys: []string{"b"}, Desc: "branch", Help: "pick a branch", Do: do(func() tea.Cmd { return v.pickBranch(a) })},
		Binding{Keys: []string{"s"}, Desc: "status", Help: "pick a status", Do: do(func() tea.Cmd { return v.pickStatus(a) })},
		Binding{Keys: []string{"u"}, Desc: "mine", Help: "only my runs / everyone's", Do: do(func() tea.Cmd {
			if a.me == "" {
				return a.flashCmd(nHint, "Still loading your GitHub user, try again in a moment.")
			}
			v.mine = !v.mine
			return v.resetFilter(a)
		})},
	)
	bs = append(bs, runTickKeys(v.t)...)
	bs = append(bs,
		filterHint,
		a.compactKey(),
		moveBinding("", func(k string) tea.Cmd {
			v.t.handleNav(k)
			if v.t.Cursor() >= v.t.Len()-3 && v.t.Filter() == "" {
				return v.loadMore(a)
			}
			return nil
		}),
	)
	return grouped("RUNS", bs...)
}

// filterHint puts "/" in a list's key bar; the key itself works everywhere.
var filterHint = Binding{Label: "/", Desc: "filter"}

// twoLineKey switches lists between one and two lines per row.
func (a *App) twoLineKey() Binding {
	return Binding{Keys: []string{"z"}, Desc: "two-line rows", Help: "show each title on a line of its own",
		Do: do(func() tea.Cmd { a.twoLine = !a.twoLine; return nil })}
}

// compactKey switches lists of runs between cards of two lines and a table
// with one line per run.
func (a *App) compactKey() Binding {
	desc := "table"
	if a.compact {
		desc = "cards"
	}
	return Binding{Keys: []string{"z"}, Desc: desc, Help: "runs as a table of one line each, or as cards of two lines",
		Do: do(func() tea.Cmd { a.compact = !a.compact; return nil })}
}

// runKeys are the keys that act on a run, shared by every screen that shows
// one. Those that don't apply to the run as it is now are Off.
func (a *App) runKeys(repo string, run gh.Run) []Binding {
	active := run.Active()
	return grouped("A RUN",
		Binding{Keys: []string{"a"}, Desc: "approve", Help: "approve / reject a deployment", Off: run.Status != "waiting", Mutates: true, Need: gh.CapWrite, Repo: repo,
			Do: do(func() tea.Cmd { return a.approveRun(repo, run) })},
		Binding{Keys: []string{"c"}, Desc: "cancel", Off: !active, Mutates: true, Need: gh.CapWrite, Repo: repo, Do: do(func() tea.Cmd { return a.cancelRun(repo, run) })},
		Binding{Keys: []string{"w"}, Desc: "watch", Help: "watch: notify me when it finishes", Off: !active,
			Do: do(func() tea.Cmd { return a.watch(repo, run) })},
		Binding{Keys: []string{"R"}, Desc: "rerun", Help: "rerun: failed jobs, one job or all, with debug logging if you like", Off: active, Mutates: true, Need: gh.CapWrite, Repo: repo,
			Do: do(func() tea.Cmd { return a.rerunPicker(repo, run, nil, nil) })},
		// Menu only: no key of its own.
		Binding{Help: "Download artifacts…", Off: active, Need: gh.CapRead,
			Do: do(func() tea.Cmd { return a.downloadArtifacts(repo, run, nil) })},
		Binding{Help: "Rerun one job…", Off: active, Mutates: true, Need: gh.CapWrite, Repo: repo,
			Do: do(func() tea.Cmd { return a.pickJobToRerun(repo, run, nil, false) })},
		Binding{Keys: []string{"#"}, Desc: "pull request", Help: "open the pull request the run belongs to", Off: len(a.runPRNumbers(repo, run)) == 0,
			Do: do(func() tea.Cmd { return a.openRunPR(repo, run) })},
		Binding{Keys: []string{"o"}, Desc: "browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(run.HTMLURL) })},
		Binding{Keys: []string{"y"}, Help: "copy the URL", Do: do(func() tea.Cmd { return a.copyText(run.HTMLURL, "run URL") })},
	)
}

// rerun asks to run a finished run again: all of it, or only its failed jobs.
func (a *App) rerun(repo string, run gh.Run, failedOnly, debug bool) tea.Cmd {
	label := fmt.Sprintf("%s #%d", run.Name, run.RunNumber)
	if run.Active() {
		return a.flashCmd(nHint, "Run is still going; cancel it first (c) or wait for it to finish.")
	}
	if failedOnly && !debug && StatusKind(run.Status, run.Conclusion) != KFail {
		return a.flashCmd(nHint, "No failed jobs to rerun; use R to rerun everything.")
	}
	what := "Rerun of " + label
	title := "Rerun all jobs?"
	effect := "Every job of this run will run again."
	if failedOnly {
		what = "Rerun of failed jobs of " + label
		title = "Rerun failed jobs?"
		effect = "Failed jobs, and the jobs that depend on them, will run again."
	}
	if debug {
		what += " with debug logging"
		title = strings.TrimSuffix(title, "?") + " with debug logging?"
	}
	lines := []string{
		sWhite.Render(label) + sDim.Render(" · "+run.DisplayTitle),
		sDim.Render(run.HeadBranch + " · " + StatusKind(run.Status, run.Conclusion).Label() + " " + age(run.UpdatedAt, time.Now()) + " ago"),
		"",
		effect,
	}
	if debug {
		lines = append(lines, "Runner and step debug logging (ACTIONS_STEP_DEBUG) will be on.")
	}
	lines = append(lines, sDim.Render("You'll get a notification when it finishes (it shows on the Watching tab)."))
	return confirm(title, lines, "Rerun", false, func() tea.Cmd {
		return a.actThen(repo, what+" requested · watching it", func(ctx context.Context) error {
			if failedOnly {
				return a.client.RerunFailed(ctx, repo, run.ID, debug)
			}
			return a.client.Rerun(ctx, repo, run.ID, debug)
		}, func() tea.Cmd {
			a.earnRerun(run, debug)
			return a.watchRerun(repo, run)
		})
	})
}

// cancelRun asks to stop a run that is still going.
func (a *App) cancelRun(repo string, run gh.Run) tea.Cmd {
	label := fmt.Sprintf("%s #%d", run.Name, run.RunNumber)
	if !run.Active() {
		return a.flashCmd(nHint, "Run already finished.")
	}
	return confirm("Cancel run?", []string{
		sWhite.Render(label) + sDim.Render(" · "+run.DisplayTitle),
		sDim.Render(run.HeadBranch + " · started " + age(run.RunStartedAt, time.Now()) + " ago by " + run.Actor.Login),
		"",
		"Running jobs will be stopped and queued jobs won't start.",
	}, "Cancel run", true, func() tea.Cmd {
		return a.actThen(repo, "Cancellation of "+label+" requested", func(ctx context.Context) error {
			return a.client.Cancel(ctx, repo, run.ID)
		}, func() tea.Cmd { a.earn("cancel-culture", 1); return nil })
	})
}

func (v *runsView) rebuild(a *App) {
	now := time.Now()
	shown := a.visibleRuns(v.repo, v.runs)
	rows := make([]Row, 0, len(shown))
	for _, r := range shown {
		k := StatusKind(r.Status, r.Conclusion)
		quiet := k.RowColor() == cDim
		num := fmt.Sprintf("#%d", r.RunNumber)
		numCell := numberCell(r.RunNumber, quiet)
		if r.RunAttempt > 1 {
			t := fmt.Sprintf("%s ↻%d", num, r.RunAttempt)
			numCell = Cell{Text: t, Styled: quietOr(sGold, quiet).Render(num) + sTan.Render(fmt.Sprintf(" ↻%d", r.RunAttempt))}
		}
		jobs := Cell{}
		if p, ok := v.progress[r.ID]; ok && r.Active() && p.total > 0 {
			st, pl := progress(p.done, p.total, p.failed)
			jobs = Cell{Text: pl, Styled: st}
		} else if k == KQueue {
			jobs = Cell{Text: "waiting for a runner", Styled: sDim.Render("waiting for a runner")}
		} else if k == KWait {
			jobs = Cell{Text: "needs approval (a)", Styled: sWait.Render("needs approval (a)")}
		}
		status := statusCell(k, a.frame, "")
		if quiet {
			status.Styled = sDim.Render(status.Text)
		}
		cells := []Cell{
			status,
			numCell,
			titleCell(r.DisplayTitle, quiet),
		}
		if v.wf == nil {
			cells = append(cells, iconText("workflow", r.Name, quiet))
		}
		took := steelCell(dur(r.Duration(now)), quiet)
		if r.Active() {
			took = Cell{Text: took.Text, Styled: sAmber.Render(took.Text)}
		}
		cells = append(cells,
			iconText("branch", r.HeadBranch, quiet),
			iconText("user", r.Actor.Login, quiet),
			steelCell(age(r.CreatedAt, now), quiet),
			took,
			jobs,
		)
		rows = append(rows, Row{Key: fmt.Sprint(r.ID), Kind: k, Cells: cells})
	}
	if len(rows) == 0 && len(v.runs) > 0 {
		v.t.Empty = "Your saved filter hides all these runs (F shows them)."
	}
	byKey := map[string]gh.Run{}
	for _, r := range shown {
		byKey[fmt.Sprint(r.ID)] = r
	}
	v.t.Card = func(r Row, w int) (string, string) {
		run := byKey[r.Key]
		var p *jobProgress
		if jp, ok := v.progress[run.ID]; ok {
			p = &jp
		}
		return runCard(a, "", run, p, w, time.Now())
	}
	v.t.SetRows(rows)
}

func (v *runsView) SetFilter(a *App, q string) { v.t.SetFilter(q, a.me) }

// ticked are the ticked runs still listed.
func (v *runsView) ticked() []repoRun {
	var out []repoRun
	for _, r := range v.runs {
		if v.t.Marked(fmt.Sprint(r.ID)) {
			out = append(out, repoRun{v.repo, r})
		}
	}
	return out
}

// ClearMarks unticks the ticked runs.
func (v *runsView) ClearMarks() bool { return v.t.ClearMarks() }

// layout applies the settings that decide which columns show and how.
func (v *runsView) layout(a *App) {
	v.t.Col("ACTOR").Hidden = v.mine
	v.t.TwoLine = a.twoLine
	v.t.Cards = !a.compact
}

// chips shows the branch, "mine" and status filters as buttons, each with
// the key that changes it.
func (v *runsView) chips() string {
	branch := "all branches"
	if v.branch != "" {
		branch = v.branch
	}
	who := "everyone"
	if v.mine {
		who = "only mine"
	}
	parts := []struct {
		key, text string
		on        bool
	}{
		{"b", "branch: " + branch + " ▾", v.branch != ""},
		{"u", "who: " + who, v.mine},
		{"s", "status: " + statusLabel(v.status) + " ▾", v.status != ""},
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(chip(p.key, p.text, p.on))
	}
	return b.String()
}

// pickBranch opens a list of the repo's branches, the ones with recent runs
// first, to filter the runs by.
func (v *runsView) pickBranch(a *App) tea.Cmd {
	repo := v.repo
	key := "branches:" + repo
	m := newPicker(key, "Branch", v.branch, branchItems(a, repo, v.runs), func(b string) tea.Cmd { return v.setBranch(a, b) })
	m.free = true
	if names, ok := a.client.CachedBranches(repo); ok {
		m.add(otherBranches(names))
	}
	a.modal = m // open now, so the branches loaded below always find it
	return func() tea.Msg {
		names, err := a.client.ListBranches(context.Background(), repo)
		if err != nil {
			return nil
		}
		return pickerItemsMsg{key, otherBranches(names)}
	}
}

func (v *runsView) setBranch(a *App, branch string) tea.Cmd {
	if branch == v.branch {
		return nil
	}
	v.branch = branch
	if v.onBranch != nil {
		v.onBranch(branch)
	}
	return v.resetFilter(a)
}

func (v *runsView) pickStatus(a *App) tea.Cmd {
	items := make([]pickItem, len(statusChoices))
	for i, c := range statusChoices {
		items[i] = pickItem{value: c.value, label: c.label}
	}
	return openModal(newPicker("status", "Status", v.status, items, func(s string) tea.Cmd {
		if s == v.status {
			return nil
		}
		v.status = s
		return v.resetFilter(a)
	}))
}

// branchItems lists "all branches", then the default branch and every branch
// with a known run, newest run first, each with how its latest run went.
func branchItems(a *App, repo string, runs []gh.Run) []pickItem {
	known := append(append([]gh.Run{}, runs...), a.st.status[repo]...)
	sort.SliceStable(known, func(i, j int) bool { return known[i].CreatedAt.After(known[j].CreatedAt) })
	def := ""
	if r, ok := a.repoByName(repo); ok {
		def = r.DefaultBranch
	}
	now := time.Now()
	items := []pickItem{{value: "", label: "all branches"}}
	seen := map[string]bool{}
	for _, r := range known {
		if r.HeadBranch == "" || seen[r.HeadBranch] {
			continue
		}
		seen[r.HeadBranch] = true
		k := StatusKind(r.Status, r.Conclusion)
		note := glyph(k, 0) + sDim.Render(" "+r.Name+" · "+age(r.CreatedAt, now))
		if r.HeadBranch == def {
			note += sDim.Render(" · default")
		}
		items = append(items, pickItem{value: r.HeadBranch, label: r.HeadBranch, note: note})
	}
	if def != "" && !seen[def] {
		items = append(items[:1], append([]pickItem{{value: def, label: def, note: sDim.Render("default")}}, items[1:]...)...)
	}
	return items
}

// otherBranches turns branch names into picker items for branches without
// a known run; the picker skips the ones it already lists.
func otherBranches(names []string) []pickItem {
	sorted := slices.Sorted(slices.Values(names))
	items := make([]pickItem, len(sorted))
	for i, n := range sorted {
		items[i] = pickItem{value: n, label: n, note: sDim.Render("no recent runs")}
	}
	return items
}

// statsLine summarises the loaded runs in one line.
func (v *runsView) statsLine(a *App) string {
	if !v.loaded {
		return sDim.Render("Loading…")
	}
	s := computeStats(v.runs, time.Now())
	var parts []string
	switch {
	case s.streak > 1 && s.streakKind == KFail && s.streakSince != nil:
		parts = append(parts, sRed.Bold(true).Render(fmt.Sprintf("✗ failing %d in a row since #%d", s.streak, s.streakSince.RunNumber)))
	case s.streak > 0 && s.streakKind == KFail:
		parts = append(parts, sRed.Bold(true).Render("✗ last run failed"))
	case s.streak > 0:
		parts = append(parts, sGreen.Render(fmt.Sprintf("✓ %d passing in a row", s.streak)))
	}
	if pct := s.successRate(); pct >= 0 {
		parts = append(parts, rateStyle(pct).Render(fmt.Sprintf("%d%%", pct))+sDim.Render(" success"))
	}
	if s.median > 0 {
		parts = append(parts, sDim.Render("median ")+sWhite.Render(dur(s.median))+sDim.Render(" · p90 ")+sWhite.Render(dur(s.p90)))
	}
	if len(s.failBranches) > 0 {
		parts = append(parts, sDim.Render("fails on ")+topN(s.failBranches, 2))
	}
	if s.active > 0 {
		parts = append(parts, sAmber.Render(fmt.Sprintf("%d running", s.active)))
	}
	if len(parts) == 0 {
		return sDim.Render("No finished runs yet.")
	}
	return strings.Join(parts, sDim.Render("  ·  ")) + sDim.Render(fmt.Sprintf("   (last %d runs)", len(v.runs)))
}

// renderPane draws the runs inside the repo screen: stats strip, filter
// chips and the table, in a box titled ttl.
func (v *runsView) renderPane(a *App, w, h int, ttl string, focused bool) string {
	border := cFrameDim
	if focused {
		border = cCyan
	}
	head := " " + v.chips() + "   " + a.filterLine(v.t.Filter(), v.FilterInfo(a)) + "\n\n"
	if n := len(v.t.Marks()); n > 0 {
		head = tickedRunsLine(n, w-2) + "\n\n"
	}
	v.layout(a)
	v.t.blur = !focused // the cursor row only stands out in the pane you're in
	body := head + v.t.Render(w-2, h-4)
	return box(ttl, body, w, h, border)
}
func (v *runsView) FilterValue() string { return v.t.Filter() }

func (v *runsView) FilterInfo(a *App) filterInfo {
	info := v.t.Info()
	info.runs = true
	info.available = v.total
	info.hidden = len(v.runs) - len(a.visibleRuns(v.repo, v.runs))
	return info
}

// ClearFilters drops the branch, "mine" and status filters too.
func (v *runsView) ClearFilters(a *App) tea.Cmd {
	if v.branch == "" && !v.mine && v.status == "" {
		return nil
	}
	v.mine, v.status = false, ""
	if v.branch != "" {
		v.branch = ""
		if v.onBranch != nil {
			v.onBranch("")
		}
	}
	return v.resetFilter(a)
}

func (v *runsView) sync(a *App) { v.rebuild(a) }

func (v *runsView) Render(a *App, w, h int) string {
	scope := v.repo
	if v.wf != nil {
		scope += " › " + v.wf.Name
	}
	if v.sha != "" {
		scope += " › " + v.Crumb()
	}
	extra := ""
	if v.branch != "" {
		extra += "<branch:" + v.branch + "> "
	}
	if v.mine {
		extra += "<mine> "
	}
	if f := v.t.Filter(); f != "" {
		extra += "</" + f + ">"
	}
	count := v.total
	if v.t.Filter() != "" || a.filtering() {
		count = v.t.Len()
	}
	v.layout(a)
	return box(title("Runs", scope, count, extra), v.t.Render(w-2, h-2), w, h, cFrame)
}
