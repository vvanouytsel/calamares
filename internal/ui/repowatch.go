package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// repoWatchEvery is how often the latest runs of each watched repo are read.
const repoWatchEvery = 30 * time.Second

// keepFinishedFromRepo is how long a finished run that a repo watch found
// stays on the Watching tab.
const keepFinishedFromRepo = 2 * time.Hour

// repoWatchState is how the watch of a repo is doing this session.
type repoWatchState struct {
	since   time.Time // runs that finished before it are not picked up
	polling bool
	failed  error
	lastNew time.Time // when the latest run it picked up started
	// primed says the runs still going when the watch started, or changed,
	// were read; until then a read lists them too.
	primed bool
	// act is what was last read of its commits, pull requests and releases
	// (actKnown: it was read), and news what changed in them while watched.
	act        gh.RepoActivity
	actKnown   bool
	actPolling bool
	actFailed  error
	news       []repoNews
}

type repoWatchPolledMsg struct {
	repo   string
	runs   []gh.Run
	primed bool
	err    error
}

// repoWatch is the state of the watch of repo, started now if it is new.
func (a *App) repoWatch(repo string) *repoWatchState {
	st, ok := a.repoWatches[strings.ToLower(repo)]
	if !ok {
		st = &repoWatchState{since: time.Now()}
		a.repoWatches[strings.ToLower(repo)] = st
	}
	return st
}

// pollRepoWatches reads the latest runs of every watched repo that isn't
// being read already: one request each, and the first time also the runs in
// progress and queued, which a busy repo has further down the list.
func (a *App) pollRepoWatches() tea.Cmd {
	a.dropOldFromRepo(time.Now())
	var cmds []tea.Cmd
	for _, w := range a.cfg.WatchedRepos {
		st := a.repoWatch(w.Repo)
		if st.polling {
			continue
		}
		st.polling = true
		repo, prime := w.Repo, !st.primed
		cmds = append(cmds, func() tea.Msg {
			runs, err := a.repoWatchRuns(repo, prime)
			return repoWatchPolledMsg{repo, runs, prime, err}
		})
	}
	return tea.Batch(cmds...)
}

// repoWatchRuns reads the latest runs of repo; with going also those in
// progress and queued.
func (a *App) repoWatchRuns(repo string, going bool) ([]gh.Run, error) {
	ctx := gh.Background(context.Background())
	filters := []gh.RunFilter{{PerPage: 20}}
	if going {
		filters = append(filters, gh.RunFilter{Status: "in_progress", PerPage: 100}, gh.RunFilter{Status: "queued", PerPage: 100})
	}
	var all []gh.Run
	seen := map[int64]bool{}
	for _, f := range filters {
		runs, _, err := a.client.ListRuns(ctx, repo, f)
		if err != nil {
			return all, err
		}
		for _, r := range runs {
			if !seen[r.ID] {
				seen[r.ID] = true
				all = append(all, r)
			}
		}
	}
	return all, nil
}

// handleRepoWatchPolled watches the new runs of a watched repo that match
// its workflows and branches.
func (a *App) handleRepoWatchPolled(m repoWatchPolledMsg) tea.Cmd {
	st := a.repoWatch(m.repo)
	st.polling, st.failed = false, m.err
	w, ok := a.cfg.RepoWatch(m.repo)
	if !ok || m.err != nil {
		return nil
	}
	if m.primed {
		st.primed = true
	}
	def := a.defaultBranch(m.repo)
	var cmds []tea.Cmd
	added := false
	for _, r := range m.runs {
		if _, known := a.watches[r.ID]; known || a.unwatched[r.ID] || !w.Matches(r.Path, r.HeadBranch, def) {
			continue
		}
		if !r.Active() && !r.CreatedAt.After(st.since) {
			continue
		}
		nw := &watch{repo: m.repo, run: r, since: time.Now(), fromRepo: true}
		a.watches[r.ID] = nw
		added = true
		if r.CreatedAt.After(st.lastNew) {
			st.lastNew = r.CreatedAt
		}
		if r.Active() {
			a.addToast(&toast{kind: nStarted, repo: m.repo, run: r, right: "now"})
			continue
		}
		cmds = append(cmds, a.watchFinished(nw))
	}
	if added {
		a.saveWatches()
		cmds = append(cmds, a.pollWatches())
	}
	return tea.Batch(cmds...)
}

// dropOldFromRepo stops showing the runs a repo watch found once they have
// been finished a while.
func (a *App) dropOldFromRepo(now time.Time) {
	changed := false
	for id, w := range a.watches {
		if w.fromRepo && !w.run.Active() && now.Sub(w.run.UpdatedAt) > keepFinishedFromRepo {
			delete(a.watches, id)
			changed = true
		}
	}
	if changed {
		a.saveWatches()
	}
}

// watchRepoKey is W: watch the new runs of repo, or change how it is watched.
func (a *App) watchRepoKey(repo string) Binding {
	desc := "watch repo"
	if _, ok := a.cfg.RepoWatch(repo); ok {
		desc = "repo watch"
	}
	return Binding{Keys: []string{"w"}, Desc: desc, Help: "watch the repo: its new runs of the workflows and branches you pick, its new commits, pull requests and releases",
		Do: do(func() tea.Cmd { return a.openRepoWatch(repo) })}
}

// openRepoWatch opens the picker of what to watch in repo, reading its
// workflows first when they aren't known yet.
func (a *App) openRepoWatch(repo string) tea.Cmd {
	open := func(wfs []gh.Workflow) tea.Msg {
		w, watched := a.cfg.RepoWatch(repo)
		return modalMsg{newRepoWatchModal(repo, a.defaultBranch(repo), wfs, w, watched)}
	}
	if wfs, ok := a.client.CachedWorkflows(repo); ok {
		return func() tea.Msg { return open(wfs) }
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		wfs, err := a.client.ListWorkflows(ctx, repo)
		if err != nil {
			return flashMsg{nError, "Could not read the workflows of " + repo + ": " + err.Error()}
		}
		return open(wfs)
	}
}

// setRepoWatch saves how repo is watched and reads its runs at once.
func (a *App) setRepoWatch(w config.RepoWatch) tea.Cmd {
	a.cfg.SetRepoWatch(w)
	if err := a.cfg.Save(); err != nil {
		return a.flashCmd(nError, "Could not save config: "+err.Error())
	}
	st := a.repoWatch(w.Repo)
	st.primed = false
	if len(w.Activity) == 0 {
		st.act, st.actKnown = gh.RepoActivity{}, false
	}
	a.earnOnce("eagle-eye")
	a.earnBest("lighthouse-keeper", len(a.cfg.WatchedRepos))
	a.setFlash(nDone, "Watching "+w.Repo+" · "+describeRepoWatch(w, a.defaultBranch(w.Repo)))
	return tea.Batch(a.pollRepoWatches(), a.pollActivity())
}

// stopRepoWatch stops watching repo and takes the runs it found off Watching.
func (a *App) stopRepoWatch(repo string) tea.Cmd {
	a.cfg.StopRepoWatch(repo)
	if err := a.cfg.Save(); err != nil {
		return a.flashCmd(nError, "Could not save config: "+err.Error())
	}
	delete(a.repoWatches, strings.ToLower(repo))
	a.saveRepoActivity()
	for id, w := range a.watches {
		if w.fromRepo && strings.EqualFold(w.repo, repo) {
			delete(a.watches, id)
		}
	}
	a.saveWatches()
	return a.flashCmd(nInfo, "Stopped watching "+repo)
}

// describeRepoWatch says in a few words what of a repo is watched.
func describeRepoWatch(w config.RepoWatch, defaultBranch string) string {
	s := watchedWorkflows(w) + " on " + watchedBranches(w, defaultBranch)
	if len(w.Activity) > 0 {
		s += ", with " + describeActivity(w)
	}
	return s
}

func watchedWorkflows(w config.RepoWatch) string {
	if len(w.Workflows) == 0 {
		return "every workflow"
	}
	names := make([]string, len(w.Workflows))
	for i, p := range w.Workflows {
		names[i] = workflowFileName(p)
	}
	return strings.Join(names, ", ")
}

func watchedBranches(w config.RepoWatch, defaultBranch string) string {
	switch {
	case w.AllBranches:
		return "every branch"
	case len(w.Branches) > 0:
		return strings.Join(w.Branches, ", ")
	case defaultBranch != "":
		return defaultBranch
	}
	return "the default branch"
}

// workflowFileName is the name of a workflow file without its folder and extension.
func workflowFileName(p string) string {
	p = p[strings.LastIndex(p, "/")+1:]
	return strings.TrimSuffix(strings.TrimSuffix(p, ".yml"), ".yaml")
}

// The ways the branches of a repo watch are chosen, in the order of the picker.
const (
	branchesAll = iota
	branchesDefault
	branchesThese
)

// repoWatchModal is what w opens: the workflows to watch, every one (also
// those added later) or the ones ticked, the branches, and what else of the
// repo to tell about.
type repoWatchModal struct {
	repo, defaultBranch string
	wfs                 []gh.Workflow
	all                 bool
	ticked              map[string]bool
	branches            int
	patterns            textinput.Model
	activity            map[string]bool
	sel                 int
	watched             bool
	err                 string
}

func newRepoWatchModal(repo, defaultBranch string, wfs []gh.Workflow, w config.RepoWatch, watched bool) *repoWatchModal {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "release/*, hotfix/*"
	in.TextStyle = sText
	in.Cursor.Style = sCyan
	in.SetValue(strings.Join(w.Branches, ", "))
	m := &repoWatchModal{repo: repo, defaultBranch: defaultBranch, wfs: wfs, all: len(w.Workflows) == 0,
		ticked: map[string]bool{}, branches: branchesDefault, patterns: in, watched: watched, activity: map[string]bool{}}
	for _, p := range w.Workflows {
		m.ticked[p] = true
	}
	// A new watch tells about everything; one you already have keeps what you picked.
	for _, k := range config.AllActivity {
		m.activity[k] = !watched || w.Tells(k)
	}
	switch {
	case w.AllBranches:
		m.branches = branchesAll
	case len(w.Branches) > 0:
		m.branches = branchesThese
	}
	return m
}

// The rows of the picker: every workflow, each workflow, the branches,
// when you name them the field for their patterns, and each kind of activity.
func (m *repoWatchModal) branchRow() int   { return len(m.wfs) + 1 }
func (m *repoWatchModal) patternsRow() int { return len(m.wfs) + 2 }
func (m *repoWatchModal) activityRow(i int) int {
	if m.branches == branchesThese {
		return m.patternsRow() + 1 + i
	}
	return m.branchRow() + 1 + i
}
func (m *repoWatchModal) lastRow() int { return m.activityRow(len(config.AllActivity) - 1) }

func (m *repoWatchModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	k := msg.String()
	if m.sel == m.patternsRow() && m.branches == branchesThese {
		switch k {
		case "esc", "enter", "up", "down", "tab", "shift+tab":
		default:
			var cmd tea.Cmd
			m.patterns, cmd = m.patterns.Update(msg)
			m.err = ""
			return false, cmd
		}
	}
	switch k {
	case "esc", "q":
		return true, nil
	case "up", "k", "shift+tab":
		m.move(-1)
	case "down", "j", "tab":
		m.move(1)
	case "left", "h":
		if m.sel == m.branchRow() {
			m.branches = (m.branches + 2) % 3
		}
	case "right", "l":
		if m.sel == m.branchRow() {
			m.branches = (m.branches + 1) % 3
		}
	case " ":
		m.toggle()
	case "x":
		if m.watched {
			return true, a.stopRepoWatch(m.repo)
		}
	case "enter":
		w, err := m.result()
		if err != "" {
			m.err = err
			return false, nil
		}
		return true, a.setRepoWatch(w)
	}
	return false, nil
}

func (m *repoWatchModal) move(d int) {
	m.sel = min(max(m.sel+d, 0), m.lastRow())
	if m.sel == m.patternsRow() {
		m.patterns.Focus()
	} else {
		m.patterns.Blur()
	}
}

func (m *repoWatchModal) toggle() {
	switch {
	case m.sel == 0:
		m.all = !m.all
		clear(m.ticked)
	case m.sel <= len(m.wfs):
		p := m.wfs[m.sel-1].Path
		if m.all {
			m.all = false
			for _, wf := range m.wfs {
				m.ticked[wf.Path] = true
			}
		}
		m.ticked[p] = !m.ticked[p]
	case m.sel == m.branchRow():
		m.branches = (m.branches + 1) % 3
	case m.sel >= m.activityRow(0):
		k := config.AllActivity[m.sel-m.activityRow(0)]
		m.activity[k] = !m.activity[k]
	}
	m.err = ""
}

// result is the watch the picker describes, or why it can't be saved.
func (m *repoWatchModal) result() (config.RepoWatch, string) {
	w := config.RepoWatch{Repo: m.repo}
	if !m.all {
		for _, wf := range m.wfs {
			if m.ticked[wf.Path] {
				w.Workflows = append(w.Workflows, wf.Path)
			}
		}
		if len(w.Workflows) == 0 {
			return w, "Tick a workflow, or every workflow."
		}
	}
	for _, k := range config.AllActivity {
		if m.activity[k] {
			w.Activity = append(w.Activity, k)
		}
	}
	switch m.branches {
	case branchesAll:
		w.AllBranches = true
	case branchesThese:
		w.Branches = strings.FieldsFunc(m.patterns.Value(), func(r rune) bool { return r == ',' || r == ' ' })
		if len(w.Branches) == 0 {
			return w, "Name a branch, such as release/*."
		}
	}
	return w, ""
}

func (m *repoWatchModal) Render(a *App, w, h int) string {
	var lines []string
	row := func(i int, text string) string {
		if i == m.sel {
			return sSel.Bold(true).Render("▸ ") + text
		}
		return "  " + text
	}
	box := func(on, dim bool) string {
		mark := "[ ]"
		if on {
			mark = "[x]"
		}
		if dim {
			return sDim.Render(mark)
		}
		return sText.Render(mark)
	}
	lines = append(lines, sDim.Render("WORKFLOWS"))
	lines = append(lines, row(0, box(m.all, false)+" "+sWhite.Render("every workflow, also new ones")))
	nameW := 0
	for _, wf := range m.wfs {
		nameW = max(nameW, width(wf.Name))
	}
	for i, wf := range m.wfs {
		lines = append(lines, row(i+1, "  "+box(m.all || m.ticked[wf.Path], m.all)+" "+sText.Render(fit(wf.Name, nameW))+"  "+sDim.Render(wf.Path)))
	}
	lines = append(lines, "", sDim.Render("BRANCHES"))
	def := "default"
	if m.defaultBranch != "" {
		def += " (" + m.defaultBranch + ")"
	}
	var opts []string
	for i, label := range []string{"every branch", def, "these"} {
		mark := "( ) "
		st := sText
		if i == m.branches {
			mark, st = "(•) ", sCyan
		}
		opts = append(opts, st.Render(mark+label))
	}
	lines = append(lines, row(m.branchRow(), strings.Join(opts, "   ")))
	if m.branches == branchesThese {
		lines = append(lines, row(m.patternsRow(), sDim.Render("patterns: ")+m.patterns.View()))
	}
	lines = append(lines, "", sDim.Render("ACTIVITY"))
	branch := m.defaultBranch
	if branch == "" {
		branch = "the default branch"
	}
	labels := map[string]string{config.ActivityCommits: "new commits on " + branch,
		config.ActivityPulls: "pull requests opened and merged", config.ActivityReleases: "releases published"}
	for i, k := range config.AllActivity {
		lines = append(lines, row(m.activityRow(i), box(m.activity[k], false)+" "+sText.Render(labels[k])))
	}
	lines = append(lines, "")
	if m.err != "" {
		lines = append(lines, sAmber.Render(m.err), "")
	}
	hints := []string{keycap("space") + sDim.Render(" select"), keycap("←→") + sDim.Render(" branches"), keycap("enter") + sDim.Render(" watch")}
	if m.watched {
		hints = append(hints, keycap("w")+sDim.Render(" change or stop watching"))
	}
	hints = append(hints, keycap("esc")+sDim.Render(" cancel"))
	lines = append(lines, strings.Join(hints, "  "))
	return modalBox(title("Watch "+m.repo, "", -1, ""), lines, cCyan, 56, w-4)
}

// repoWatchRows are the rows of the Repos view of Watching: each watched
// repo, what it watches and how it is doing.
func (a *App) repoWatchRows(now time.Time) []Row {
	var rows []Row
	for _, w := range a.cfg.WatchedRepos {
		st := a.repoWatch(w.Repo)
		running := 0
		for _, x := range a.watches {
			if x.fromRepo && strings.EqualFold(x.repo, w.Repo) && x.run.Active() {
				running++
			}
		}
		last := steelCell("none yet", true)
		if !st.lastNew.IsZero() {
			t := "new run · " + age(st.lastNew, now)
			last = Cell{Text: t, Styled: sText.Render("new run") + fg(cSteel).Render(" · "+age(st.lastNew, now))}
		}
		if n := len(st.news); n > 0 && st.news[n-1].At.After(st.lastNew) {
			x := st.news[n-1]
			t := x.Text + " · " + age(x.At, now)
			last = Cell{Text: t, Styled: fg(x.Kind.Color()).Render(x.Text) + fg(cSteel).Render(" · "+age(x.At, now))}
		}
		activity := iconText("commit", describeActivity(w), len(w.Activity) == 0)
		state := statusCell(KOK, a.frame, "watching")
		switch {
		case st.failed != nil || st.actFailed != nil:
			state = statusCell(KFail, a.frame, "can't read")
		case running > 0:
			state = statusCell(KRun, a.frame, fmt.Sprintf("%d running", running))
		}
		rows = append(rows, Row{Key: w.Repo, Cells: []Cell{
			state,
			iconText("repo", a.short(w.Repo), false),
			iconText("workflow", watchedWorkflows(w), false),
			iconText("branch", watchedBranches(w, a.defaultBranch(w.Repo)), false),
			activity,
			last,
		}})
	}
	return rows
}

// repoWatchPanel tells about the watched repo under the cursor.
func (a *App) repoWatchPanel(repo string, w, h int) []string {
	rw, ok := a.cfg.RepoWatch(repo)
	if !ok {
		return emptyPanel(w, h, "No repo watched yet. Press W on a repo.")
	}
	st := a.repoWatch(repo)
	p := newPanel(w)
	p.add(icoIn("repo", cGold) + sWhite.Bold(true).Render(repo))
	p.add("")
	p.label("Watch settings")
	p.field("Workflows", ico("workflow")+sText.Render(watchedWorkflows(rw)))
	p.field("Branches", ico("branch")+sText.Render(watchedBranches(rw, a.defaultBranch(repo))))
	p.field("Activity", ico("commit")+quietOr(sText, len(rw.Activity) == 0).Render(describeActivity(rw)))
	if st.failed != nil {
		p.callout(cRed, sText.Render("Could not read its runs: "+st.failed.Error()))
	}
	if st.actFailed != nil {
		p.callout(cRed, sText.Render("Could not read its commits, pull requests and releases: "+st.actFailed.Error()))
	}
	if len(rw.Activity) > 0 {
		now := time.Now()
		p.label("Changes")
		p.add(sDim.Render("seen since you started watching"))
		if len(st.news) == 0 {
			p.add(sDim.Render("None yet."))
		}
		for i := len(st.news) - 1; i >= max(len(st.news)-8, 0); i-- {
			x := st.news[i]
			p.row(fg(x.Kind.Color()).Render(x.Kind.Glyph(a.frame))+" "+sText.Render(x.Text), fg(cSteel).Render(age(x.At, now)))
		}
	}
	var runs []*watch
	for _, x := range a.sortedWatches() {
		if x.fromRepo && strings.EqualFold(x.repo, repo) {
			runs = append(runs, x)
		}
	}
	p.label("Watched runs")
	if len(runs) == 0 {
		p.add(sDim.Render("None since you started watching."))
	}
	for _, x := range runs[:min(len(runs), 8)] {
		k := StatusKind(x.run.Status, x.run.Conclusion)
		p.add(fg(k.Color()).Render(k.Glyph(a.frame)) + " " + sText.Render(x.run.Name) + " " + sGold.Render(fmt.Sprintf("#%d", x.run.RunNumber)) + " " + sDim.Render(x.run.HeadBranch))
	}
	return p.render(h)
}
