package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

type treeKind int

const (
	tGroup treeKind = iota
	tJob
	tStep
)

type treeItem struct {
	kind  treeKind
	group string
	job   int // index into jobs
	step  int // index into jobs[job].Steps
	depth int
}

// key identifies a tree row by IDs (not indexes), so the cursor survives the
// job list being replaced. Out-of-range indexes yield "".
func (t treeItem) key(jobs []gh.Job) string {
	if t.kind == tGroup {
		return "g:" + t.group
	}
	if t.job < 0 || t.job >= len(jobs) {
		return ""
	}
	if t.kind == tJob {
		return fmt.Sprintf("j:%d", jobs[t.job].ID)
	}
	if t.step < 0 || t.step >= len(jobs[t.job].Steps) {
		return ""
	}
	return fmt.Sprintf("s:%d:%d", jobs[t.job].ID, jobs[t.job].Steps[t.step].Number)
}

type runView struct {
	id          int
	repo        string
	run         gh.Run
	jobs        []gh.Job
	loaded      bool
	busy        int
	err         error
	last        time.Time
	expanded    map[string]bool
	items       []treeItem
	cursor      int
	offset      int
	placed      bool
	jobsLoaded  bool
	annotations map[int64][]gh.Annotation
	pending     map[int64]bool
	treeW       int
	poll        backoff
	jobsFP      string
	follow      bool // the cursor stays on whatever is running
	// attempt is the earlier attempt on display; 0 is the latest.
	attempt    int
	attemptRun *gh.Run

	graph    bool // the pipeline is drawn instead of the job list
	plan     []gh.PlannedJob
	planLoad int        // 0 not asked for, 1 loading, 2 done
	g        graphState // where the pipeline is looked at, and how

	typical  map[string]gh.TypicalJob // how long each job usually takes
	previous *gh.Run                  // the previous successful run
	// runTypical is how long the whole run usually takes, and took in previous.
	runTypical gh.TypicalJob
	// byPrevious says durations are compared with previous rather than the
	// median, as the config said when the run was last drawn.
	byPrevious  bool
	typicalLoad int // 0 not asked for, 1 loading, 2 done

	sourceWanted bool // open the selected job's YAML once the workflow file is read

	concurrency      concurrencyNote // why it was cancelled or waits, when a concurrency group did it
	concurrencyAsked bool

	arts     []gh.Artifact // what the run uploaded, read once it has completed
	artsFor  string        // the run and attempt arts belongs to
	artsBusy bool
}

// runDataMsg carries either a fresh run or its jobs; both are fetched in parallel.
type runDataMsg struct {
	id      int
	attempt int
	run     *gh.Run
	jobs    []gh.Job
	hasJobs bool
	err     error
}

type planMsg struct {
	id   int
	plan []gh.PlannedJob
}

func (m planMsg) viewID() int { return m.id }

type annotationsMsg struct {
	id    int
	jobID int64
	anns  []gh.Annotation
}

func (m runDataMsg) viewID() int     { return m.id }
func (m annotationsMsg) viewID() int { return m.id }

func newRunView(a *App, repo string, run gh.Run) *runView {
	return &runView{
		id: a.newID(), repo: repo, run: run,
		expanded:    map[string]bool{},
		annotations: map[int64][]gh.Annotation{},
		pending:     map[int64]bool{},
		poll:        newBackoff(a.liveEvery(), maxLiveEvery),
		g:           graphState{zoom: zoomFit},
		// A run still going is followed from the start: f stops it.
		follow: run.Active(),
	}
}

func (v *runView) ID() int          { return v.id }
func (v *runView) RepoName() string { return v.repo }
func (v *runView) Crumb() string    { return clip(v.run.Name, 30) + fmt.Sprintf(" #%d", v.run.RunNumber) }

// Init renders immediately: the run itself is already known from the list,
// and its jobs come from the cache if they were seen before.
func (v *runView) Init(a *App) tea.Cmd {
	v.loaded = true
	var cmd tea.Cmd
	if jobs, ok := a.client.CachedJobs(v.repo, v.run.ID); ok {
		v.jobs, v.jobsLoaded = jobs, true
		v.rebuild()
		cmd = v.ensure(a)
	}
	return tea.Batch(cmd, v.load(a, context.Background(), true), v.loadArtifacts(a))
}

// load fetches the jobs and, with withRun, the run itself. The jobs show the
// progress, so the run is only needed for its final state.
func (v *runView) load(a *App, ctx context.Context, withRun bool) tea.Cmd {
	v.last = time.Now()
	id, repo, runID, attempt := v.id, v.repo, v.run.ID, v.attempt
	cmds := []tea.Cmd{func() tea.Msg {
		jobs, err := a.client.ListAttemptJobs(ctx, repo, runID, attempt)
		return runDataMsg{id: id, attempt: attempt, jobs: jobs, hasJobs: err == nil, err: err}
	}}
	if withRun || attempt > 0 && v.attemptRun == nil {
		cmds = append(cmds, func() tea.Msg {
			var run gh.Run
			var err error
			if attempt > 0 {
				run, err = a.client.GetRunAttempt(ctx, repo, runID, attempt)
			} else {
				run, err = a.client.GetRun(ctx, repo, runID)
			}
			if err != nil {
				return runDataMsg{id: id, attempt: attempt, err: err}
			}
			return runDataMsg{id: id, attempt: attempt, run: &run}
		})
	}
	v.busy = len(cmds)
	return tea.Batch(cmds...)
}

// Refresh polls a running run's jobs, backing off while nothing changes, and
// fetches the run once its jobs are all done. A finished run is checked
// every few minutes, or at once after you rerun it.
func (v *runView) Refresh(a *App, force bool) tea.Cmd {
	if v.busy > 0 || (v.attempt > 0 && !force) {
		return nil
	}
	every := 5 * time.Minute
	done := v.jobsLoaded && allJobsDone(v.jobs)
	if v.run.Active() || !done {
		every = v.poll.every()
	}
	if !force && time.Since(v.last) < every {
		return nil
	}
	// The run itself is fetched when its state may no longer match its jobs:
	// all jobs done while it still shows running, jobs running again after
	// it finished (a rerun), it still shows queued while a job has started,
	// or it waits for an approval.
	withRun := force || v.run.Active() == done || v.run.Status == "waiting" ||
		(v.run.Status == "queued" && anyJobStarted(v.jobs))
	return v.load(a, gh.Background(context.Background()), withRun)
}

// Following reports whether the run is still going, so the screen follows it.
func (v *runView) Following() bool {
	return v.attempt == 0 && (v.run.Active() || (v.jobsLoaded && !allJobsDone(v.jobs)))
}

// anyJobStarted reports whether a job of the run is running or done.
func anyJobStarted(jobs []gh.Job) bool {
	for _, j := range jobs {
		if j.Status == "in_progress" || j.Status == "completed" {
			return true
		}
	}
	return false
}

func allJobsDone(jobs []gh.Job) bool {
	for _, j := range jobs {
		if j.Status != "completed" {
			return false
		}
	}
	return true
}

func (v *runView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case runDataMsg:
		if msg.attempt != v.attempt {
			return nil
		}
		v.busy = max(v.busy-1, 0)
		if msg.err != nil {
			v.err = msg.err
			return a.flashCmd(nError, "Could not refresh run: "+msg.err.Error())
		}
		v.err = nil
		if msg.run != nil {
			if v.attempt > 0 {
				v.attemptRun = msg.run
			} else {
				v.run = *msg.run
			}
			return v.loadArtifacts(a)
		}
		prev := v.selectedKey() // computed against the old jobs, before they're replaced
		fp := jobsFingerprint(msg.jobs)
		v.poll.seen(fp != v.jobsFP)
		v.jobsFP = fp
		v.jobs, v.jobsLoaded = msg.jobs, true
		for _, j := range v.jobs {
			// Retry logs that failed to load (other than expired ones) on each refresh.
			if l := a.logs[j.ID]; l != nil && l.err != nil && !gh.IsNotFound(l.err) {
				delete(a.logs, j.ID)
			}
		}
		v.rebuildAt(prev)
		if v.follow {
			return tea.Batch(v.followRunning(a), v.ensure(a), v.loadTypical(a), v.loadConcurrency(a))
		}
		return tea.Batch(v.ensure(a), v.loadTypical(a), v.loadConcurrency(a))
	case concurrencyMsg:
		v.concurrency = msg.note
		return nil
	case artifactsMsg:
		v.artsBusy = false
		if msg.err == nil {
			v.arts, v.artsFor = msg.arts, msg.key
		}
		return nil
	case typicalMsg:
		v.typicalLoaded(a, msg)
		return nil
	case annotationsMsg:
		v.annotations[msg.jobID] = msg.anns
		return nil
	case planMsg:
		v.plan, v.planLoad = msg.plan, 2
		if v.graph {
			// The jobs are named after the workflow file now: find the one
			// the list is on again.
			v.selectNodeAtCursor()
			v.g.center = true
		}
		if v.sourceWanted && a.top() == View(v) {
			v.sourceWanted = false
			return v.showJobSource(a)
		}
		v.sourceWanted = false
		return nil
	case jobLogMsg:
		delete(v.pending, msg.jobID)
		var job *gh.Job
		for i := range v.jobs {
			if v.jobs[i].ID == msg.jobID {
				job = &v.jobs[i]
			}
		}
		a.storeLog(msg.jobID, msg.log, job)
		notReady := gh.IsNotFound(msg.log.err) && job != nil && time.Since(job.CompletedAt) < 5*time.Minute
		if msg.log.err != nil && !notReady {
			a.logs[msg.jobID] = msg.log // remember the failure until the next refresh
		}
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

// groupName returns the matrix base name: "test (ubuntu, 1.23)" → "test".
func groupName(job string) string {
	if i := strings.Index(job, " ("); i > 0 && strings.HasSuffix(job, ")") {
		return job[:i]
	}
	return job
}

func (v *runView) groups() (order []string, members map[string][]int) {
	members = map[string][]int{}
	for i, j := range v.jobs {
		g := groupName(j.Name)
		if _, ok := members[g]; !ok {
			order = append(order, g)
		}
		members[g] = append(members[g], i)
	}
	return
}

func jobKind(j gh.Job) Kind { return StatusKind(j.Status, j.Conclusion) }

func stepKind(s gh.Step) Kind { return StatusKind(s.Status, s.Conclusion) }

func aggregate(kinds []Kind) Kind {
	has := map[Kind]bool{}
	for _, k := range kinds {
		has[k] = true
	}
	for _, k := range []Kind{KFail, KRun, KWait, KQueue, KCancel} {
		if has[k] {
			return k
		}
	}
	if has[KOK] {
		return KOK
	}
	return KSkip
}

func (v *runView) rebuild() {
	var curKey string
	if v.cursor < len(v.items) {
		curKey = v.items[v.cursor].key(v.jobs)
	}
	v.rebuildAt(curKey)
}

// selectedKey is the ID-based key of the row under the cursor.
func (v *runView) selectedKey() string {
	if v.cursor < len(v.items) {
		return v.items[v.cursor].key(v.jobs)
	}
	return ""
}

// rebuildAt rebuilds the tree and puts the cursor back on curKey.
func (v *runView) rebuildAt(curKey string) {
	order, members := v.groups()
	if !v.placed {
		for _, g := range order {
			ids := members[g]
			for _, i := range ids {
				k := jobKind(v.jobs[i])
				if k == KFail || k == KRun || len(v.jobs) == 1 {
					v.expanded[fmt.Sprintf("j:%d", v.jobs[i].ID)] = true
					v.expanded["g:"+g] = true
				}
			}
		}
	}
	v.items = v.items[:0]
	for _, g := range order {
		ids := members[g]
		depth := 0
		if len(ids) > 1 {
			v.items = append(v.items, treeItem{kind: tGroup, group: g})
			if !v.expanded["g:"+g] {
				continue
			}
			depth = 1
		}
		for _, i := range ids {
			v.items = append(v.items, treeItem{kind: tJob, group: g, job: i, depth: depth})
			if v.expanded[fmt.Sprintf("j:%d", v.jobs[i].ID)] {
				for s := range v.jobs[i].Steps {
					v.items = append(v.items, treeItem{kind: tStep, group: g, job: i, step: s, depth: depth + 1})
				}
			}
		}
	}
	if !v.placed && len(v.items) > 0 {
		v.placed = true
		v.cursor = 0
		if i := v.findStep(0, KFail); i >= 0 {
			v.cursor = i
		} else if i := v.findStep(0, KRun); i >= 0 {
			v.cursor = i
		}
		return
	}
	for i, it := range v.items {
		if it.key(v.jobs) == curKey {
			v.cursor = i
			return
		}
	}
	v.cursor = min(v.cursor, max(len(v.items)-1, 0))
}

func (v *runView) findStep(from int, k Kind) int {
	for n := 0; n < len(v.items); n++ {
		i := (from + n) % len(v.items)
		it := v.items[i]
		if it.kind == tStep && stepKind(v.jobs[it.job].Steps[it.step]) == k {
			return i
		}
	}
	return -1
}

func (v *runView) selected() (treeItem, bool) {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return treeItem{}, false
	}
	return v.items[v.cursor], true
}

// selectedJob returns the job under the cursor (for a group: its first failed leg).
func (v *runView) selectedJob() *gh.Job {
	it, ok := v.selected()
	if !ok {
		return nil
	}
	if it.kind != tGroup {
		return &v.jobs[it.job]
	}
	_, members := v.groups()
	ids := members[it.group]
	for _, i := range ids {
		if jobKind(v.jobs[i]) == KFail {
			return &v.jobs[i]
		}
	}
	return &v.jobs[ids[0]]
}

// ensure starts loading whatever the right pane needs for the current selection.
func (v *runView) ensure(a *App) tea.Cmd {
	var cmds []tea.Cmd
	for _, j := range v.jobs {
		if jobKind(j) == KFail {
			if _, ok := v.annotations[j.ID]; !ok {
				v.annotations[j.ID] = nil
				id, repo, jobID := v.id, v.repo, j.ID
				cmds = append(cmds, func() tea.Msg {
					anns, _ := a.client.Annotations(context.Background(), repo, jobID)
					return annotationsMsg{id, jobID, anns}
				})
			}
		}
	}
	if job := v.selectedJob(); job != nil && job.Status == "completed" {
		if _, ok := a.logs[job.ID]; !ok && !v.pending[job.ID] {
			v.pending[job.ID] = true
			cmds = append(cmds, a.fetchJobLog(v.id, v.repo, *job))
		}
	}
	return tea.Batch(cmds...)
}

// openLogs shows the log of the job, or step, under the cursor.
func (v *runView) openLogs(a *App) tea.Cmd {
	job := v.selectedJob()
	if job == nil {
		return nil
	}
	step := 0
	if it, _ := v.selected(); it.kind == tStep {
		step = v.jobs[it.job].Steps[it.step].Number
	}
	return a.push(v.logView(a, job, step))
}

// viewedAttempt is the attempt on display.
func (v *runView) viewedAttempt() int {
	if v.attempt > 0 {
		return v.attempt
	}
	return max(v.run.RunAttempt, 1)
}

// stepAttempt shows the previous (d < 0) or next attempt of the run.
func (v *runView) stepAttempt(a *App, d int) tea.Cmd {
	latest := max(v.run.RunAttempt, 1)
	next := v.viewedAttempt() + d
	if next < 1 || next > latest {
		if latest == 1 {
			return a.flashCmd(nHint, "This run has only one attempt.")
		}
		return nil
	}
	v.follow = false
	v.attempt, v.attemptRun = next, nil
	if next == latest {
		v.attempt = 0
	}
	v.jobs, v.jobsLoaded, v.items, v.jobsFP = nil, false, nil, ""
	v.expanded, v.placed, v.cursor, v.offset = map[string]bool{}, false, 0, 0
	return v.load(a, context.Background(), true)
}

func (v *runView) logView(a *App, job *gh.Job, step int) *logView {
	lv := newLogView(a, v.repo, v.run, job, step)
	lv.attempt = v.attempt
	return lv
}

// attemptLabel names the attempt on display, highlighted when it isn't the
// latest, or is "" while there is only one.
func (v *runView) attemptLabel() string {
	latest := max(v.run.RunAttempt, 1)
	if latest == 1 {
		return ""
	}
	text := fmt.Sprintf("attempt %d of %d", v.viewedAttempt(), latest)
	if v.attempt > 0 {
		return sAmber.Bold(true).Render(text + " (older)")
	}
	return sText.Render(text)
}

// shownRun is the run as it was in the attempt on display.
func (v *runView) shownRun() gh.Run {
	if v.attempt > 0 && v.attemptRun != nil {
		return *v.attemptRun
	}
	return v.run
}

// followRunning opens the jobs that are running and puts the cursor on the
// step that is running now. When nothing runs any more, it stops following.
func (v *runView) followRunning(a *App) tea.Cmd {
	for _, j := range v.jobs {
		if jobKind(j) == KRun {
			v.expanded[fmt.Sprintf("j:%d", j.ID)] = true
			v.expanded["g:"+groupName(j.Name)] = true
		}
	}
	v.rebuild()
	target := -1
	for i, it := range v.items {
		switch {
		case it.kind == tStep && stepKind(v.jobs[it.job].Steps[it.step]) == KRun:
			target = i
		case it.kind == tJob && target < 0 && jobKind(v.jobs[it.job]) == KRun:
			target = i
		}
	}
	if target < 0 {
		if v.run.Active() {
			return nil
		}
		v.follow = false
		return a.flashCmd(nInfo, "The run finished: stopped following.")
	}
	v.cursor = target
	v.selectNodeAtCursor()
	return nil
}

func (v *runView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *runView) Keys(a *App) []Binding {
	follow := Binding{Keys: []string{"f"}, Desc: "follow", Help: "keep the cursor on the step that is running (on by default)", Off: !v.Following(),
		Do: do(func() tea.Cmd { return v.toggleFollow(a) })}
	if v.follow {
		follow.Desc = "stop following"
	}
	graph := Binding{Keys: []string{"p"}, Desc: "pipeline", Help: "the jobs as a graph of what waits for what",
		Do: do(func() tea.Cmd { return v.toggleGraph(a) })}
	var bs []Binding
	if v.graph {
		graph.Desc = "job list"
		bs = append(bs, v.graphKeys(a)...)
	} else {
		bs = append(bs, follow)
	}
	bs = append(bs,
		graph,
		Binding{Keys: []string{"c"}, Desc: "workflow file", Help: "the YAML of the workflow, at the job under the cursor (v there shows the whole file)",
			Do: do(func() tea.Cmd {
				if v.selectedJob() == nil {
					return v.showWorkflowSource(a)
				}
				return v.showJobSource(a)
			})},
		Binding{Keys: []string{"enter", "right", "left"}, Label: "enter", Desc: "open step log", Help: "open a step's log · fold a job",
			Do: func(k string) tea.Cmd { return v.openOrFold(a, k) }},
		Binding{Keys: []string{" "}, Label: "space", Desc: "fold", Help: "fold / unfold jobs and steps", Do: func(k string) tea.Cmd { return v.openOrFold(a, k) }},
		Binding{Keys: []string{"e"}, Desc: "next error", Help: "jump to the next failed step", Do: do(func() tea.Cmd { return v.nextError(a) })},
		Binding{Keys: []string{"t"}, Desc: timesDesc(a), Help: "compare each duration with the usual one (the median of earlier runs), with the previous run, or not at all",
			Do: do(func() tea.Cmd { return v.toggleTypical(a) })},
		Binding{Keys: []string{"d"}, Desc: v.diffDesc(), Help: "what differs from the last run of this workflow that passed on this branch: its commits, moved tags and runner image",
			Off: StatusKind(v.run.Status, v.run.Conclusion) != KFail,
			Do:  do(func() tea.Cmd { return a.push(newSinceGreenView(a, v.repo, v.run)) })},
		Binding{Help: "previous attempt of the rerun", Off: v.run.RunAttempt <= 1 || v.shownRun().RunAttempt <= 1,
			Do: do(func() tea.Cmd { return v.stepAttempt(a, -1) })},
		Binding{Help: "next attempt of the rerun", Off: v.run.RunAttempt <= 1 || v.shownRun().RunAttempt >= v.run.RunAttempt,
			Do: do(func() tea.Cmd { return v.stepAttempt(a, 1) })},
	)
	if other := v.concurrencyRun(); other != nil {
		r := *other
		bs = append(bs, Binding{Help: fmt.Sprintf("open #%d: the run of the same concurrency group that took its place, or that it waits for", r.RunNumber),
			Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, r)) })})
	}
	if v.graph {
		bs = append(bs, follow)
	}
	bs = append(bs,
		Binding{Keys: []string{"l"}, Desc: "logs", Help: "the log of the run, job or step under the cursor", Do: do(func() tea.Cmd { return v.openLogs(a) })},
		a.runRepoKey(v.repo, v.run),
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open the run, or the job under the cursor, on GitHub", Do: do(func() tea.Cmd {
			if job := v.selectedJob(); job != nil {
				if it, _ := v.selected(); it.kind != tGroup {
					return a.openURL(job.HTMLURL)
				}
			}
			return a.openURL(v.run.HTMLURL)
		})},
	)
	for _, b := range a.runKeys(v.repo, v.run) {
		if len(b.Keys) == 0 {
			if strings.HasPrefix(b.Help, "Download artifacts") {
				continue // the screen has its own, with the artifacts it read
			}
			b.Do = do(func() tea.Cmd { return a.pickJobToRerun(v.repo, v.shownRun(), v.jobs, false) })
			bs = append(bs, b)
			continue
		}
		switch b.Keys[0] {
		case "R":
			b.Do = do(func() tea.Cmd { return a.rerunPicker(v.repo, v.shownRun(), v.jobs, v.selectedJob()) })
		}
		bs = append(bs, b)
	}
	bs = append(bs, a.downloadArtifactsKey(v.repo, v.run, v.arts, nil))
	bs = append(bs, moveBinding("", func(k string) tea.Cmd { return v.move(a, k) }))
	if !v.graph {
		bs = prioritise(bs, "f", "A", "enter", "l", "R", "d", "X", "r", "e", "p")
	}
	return grouped("A RUN", bs...)
}

// move moves the cursor through the job list, which stops following.
func (v *runView) move(a *App, k string) tea.Cmd {
	v.follow = false
	last := max(len(v.items)-1, 0)
	switch k {
	case "up", "k":
		v.cursor = max(v.cursor-1, 0)
	case "down", "j":
		v.cursor = min(v.cursor+1, last)
	case "pgup", "ctrl+b":
		v.cursor = max(v.cursor-10, 0)
	case "pgdown", "ctrl+f":
		v.cursor = min(v.cursor+10, last)
	case "home":
		v.cursor = 0
	case "end":
		v.cursor = last
	}
	return v.ensure(a)
}

func (v *runView) toggleFollow(a *App) tea.Cmd {
	if !v.Following() {
		return a.flashCmd(nHint, "Nothing is running: there is nothing to follow.")
	}
	v.follow = !v.follow
	if !v.follow {
		return a.flashCmd(nInfo, "Stopped following")
	}
	return tea.Batch(v.followRunning(a), v.ensure(a))
}

// openOrFold opens the log of the step under the cursor, or folds and
// unfolds the job or matrix there: → opens, ← closes, enter and space switch.
func (v *runView) openOrFold(a *App, k string) tea.Cmd {
	it, ok := v.selected()
	if !ok {
		return nil
	}
	if it.kind == tStep {
		if k == "left" || k == " " {
			// fold the parent job and land on it
			v.expanded[fmt.Sprintf("j:%d", v.jobs[it.job].ID)] = false
			v.rebuild()
			for i, x := range v.items {
				if x.kind == tJob && x.job == it.job {
					v.cursor = i
				}
			}
			return nil
		}
		return a.push(v.logView(a, &v.jobs[it.job], v.jobs[it.job].Steps[it.step].Number))
	}
	key := it.key(v.jobs)
	switch k {
	case "right":
		v.expanded[key] = true
	case "left":
		v.expanded[key] = false
	default:
		v.expanded[key] = !v.expanded[key]
	}
	v.rebuild()
	return v.ensure(a)
}

// nextError puts the cursor on the next failed step, opening failed jobs
// that are folded.
func (v *runView) nextError(a *App) tea.Cmd {
	v.follow = false
	if i := v.findStep(v.cursor+1, KFail); i >= 0 {
		v.cursor = i
		return v.ensure(a)
	}
	for _, j := range v.jobs {
		if jobKind(j) == KFail {
			v.expanded[fmt.Sprintf("j:%d", j.ID)] = true
			v.expanded["g:"+groupName(j.Name)] = true
		}
	}
	v.rebuild()
	if i := v.findStep(v.cursor+1, KFail); i >= 0 {
		v.cursor = i
		return v.ensure(a)
	}
	return a.flashCmd(nHint, "No failed steps in this run.")
}

// timesDesc says how durations are compared now; t moves to the next way.
func timesDesc(a *App) string {
	switch a.cfg.CompareDurations {
	case config.CompareMedian:
		return "times vs usual"
	case config.ComparePrevious:
		return "times vs previous"
	}
	return "plain times"
}

// diffDesc names the run the d key compares with: the previous run that
// passed, when it ran on the same branch, else the last one that passed.
func (v *runView) diffDesc() string {
	if p := v.previous; p != nil && p.HeadBranch == v.run.HeadBranch {
		return fmt.Sprintf("diff vs #%d ✓", p.RunNumber)
	}
	return "diff vs last pass"
}
