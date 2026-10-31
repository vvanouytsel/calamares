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

// sendNotification is replaced in tests.
var sendNotification = notifyWithLink

// watchEvery is how often watched runs are read, whether or not the
// terminal has focus.
const watchEvery = 10 * time.Second

type watch struct {
	repo     string
	run      gh.Run
	since    time.Time
	polling  bool
	failed   error
	notFound int // reads in a row that GitHub answered 404; see goneAfter
	notified bool
	// after is the attempt a rerun must get past: until GitHub reports a
	// later one, the run it reports is still the one that was rerun.
	after int
	// fromRepo says the watch of its repo found the run; you didn't pick it.
	fromRepo bool
}

// savedWatch is a watch as it is kept between sessions.
type savedWatch struct {
	Repo     string
	Run      gh.Run
	Since    time.Time
	Notified bool
	After    int
	FromRepo bool `json:",omitempty"`
}

const snapshotWatches = "watches"

// keepFinishedWatch is how long a finished run stays on the Watching tab
// across restarts.
const keepFinishedWatch = 24 * time.Hour

// goneAfter is how many reads in a row GitHub must answer 404 before a
// watched run or pull request counts as deleted; one could be a hiccup.
const goneAfter = 3

// rerunStartsWithin is how long a rerun gets to show up on GitHub before
// its watch gives up waiting for it.
const rerunStartsWithin = 2 * time.Minute

// saveWatches keeps the watches with the response cache, so they are still
// there the next time Calamares starts.
func (a *App) saveWatches() {
	out := make([]savedWatch, 0, len(a.watches))
	for _, w := range a.sortedWatches() {
		out = append(out, savedWatch{w.repo, w.run, w.since, w.notified, w.after, w.fromRepo})
	}
	a.client.Remember(snapshotWatches, out)
}

// loadWatches brings back the watches of the last session.
func (a *App) loadWatches() {
	var saved []savedWatch
	if _, ok := a.client.Recall(snapshotWatches, &saved); !ok {
		return
	}
	for _, s := range saved {
		if !s.Run.Active() && (time.Since(s.Since) > keepFinishedWatch || s.FromRepo && time.Since(s.Run.UpdatedAt) > keepFinishedFromRepo) {
			continue
		}
		if _, ok := a.watches[s.Run.ID]; !ok {
			a.watches[s.Run.ID] = &watch{repo: s.Repo, run: s.Run, since: s.Since, notified: s.Notified, after: s.After, fromRepo: s.FromRepo}
		}
	}
}

// watchRerun watches a run that was just asked to run again, so you hear
// how the new attempt ends without having to press w.
func (a *App) watchRerun(repo string, run gh.Run) tea.Cmd {
	after := max(run.RunAttempt, 1)
	run.Status, run.Conclusion = "queued", ""
	a.watches[run.ID] = &watch{repo: repo, run: run, since: time.Now(), after: after}
	a.saveWatches()
	started := run
	started.RunAttempt = after + 1
	a.addToast(&toast{kind: nStarted, repo: repo, run: started, right: fmt.Sprintf("attempt %d", after+1)})
	return a.pollWatches()
}

type watchPolledMsg struct {
	repo string
	id   int64
	run  gh.Run
	err  error
}

// watch toggles watching a run. Finished runs stay on the Watching tab
// until you stop watching them.
func (a *App) watch(repo string, run gh.Run) tea.Cmd {
	label := fmt.Sprintf("%s #%d", run.Name, run.RunNumber)
	if w, ok := a.watches[run.ID]; ok {
		if w.fromRepo {
			a.unwatched[run.ID] = true
		}
		delete(a.watches, run.ID)
		a.saveWatches()
		return a.flashCmd(nInfo, "Stopped watching "+label)
	}
	w := &watch{repo: repo, run: run, since: time.Now(), notified: !run.Active()}
	a.watches[run.ID] = w
	a.saveWatches()
	a.earnOnce("eagle-eye")
	if !run.Active() {
		return a.flashCmd(nInfo, "Watching "+label+" · it has already finished")
	}
	a.setFlash(nInfo, "Watching "+label+" · you'll get a notification when it finishes")
	return a.pollWatches()
}

// pollWatches reads every watched run that hasn't finished and isn't being
// read already: one request each.
func (a *App) pollWatches() tea.Cmd {
	var cmds []tea.Cmd
	for _, w := range a.watches {
		if w.polling || !w.run.Active() {
			continue
		}
		w.polling = true
		repo, id := w.repo, w.run.ID
		cmds = append(cmds, func() tea.Msg {
			run, err := a.client.GetRun(gh.Background(context.Background()), repo, id)
			return watchPolledMsg{repo: repo, id: id, run: run, err: err}
		})
	}
	return tea.Batch(cmds...)
}

func (a *App) handleWatchPolled(m watchPolledMsg) tea.Cmd {
	w, ok := a.watches[m.id]
	if !ok {
		return nil
	}
	w.polling = false
	w.failed = m.err
	if gh.IsNotFound(m.err) {
		w.notFound++
	} else {
		w.notFound = 0
	}
	if w.notFound >= goneAfter {
		// The run was deleted, or the repo went: asking again would only ever get a 404.
		delete(a.watches, m.id)
		a.saveWatches()
		a.setFlash(nInfo, fmt.Sprintf("Stopped watching %s #%d · it is gone from %s", w.run.Name, w.run.RunNumber, w.repo))
		return nil
	}
	if m.err != nil {
		return nil
	}
	if w.after > 0 && !m.run.Active() && m.run.RunAttempt <= w.after && time.Since(w.since) < rerunStartsWithin {
		return nil // GitHub still reports the attempt that was rerun
	}
	w.run = m.run
	if m.run.Active() || w.notified {
		return nil
	}
	return a.watchFinished(w)
}

// watchFinished tells how a watched run ended: a card, and a desktop
// notification while the terminal is out of focus. Of the runs a repo watch
// found, only a failure is told.
func (a *App) watchFinished(w *watch) tea.Cmd {
	w.notified = true
	a.saveWatches()
	run := w.run
	k := StatusKind(run.Status, run.Conclusion)
	if !w.fromRepo {
		a.earnFinished(w, time.Now())
	}
	if w.fromRepo && k != KFail {
		return nil
	}
	if k != KOK && k != KFail {
		a.setFlash(nInfo, fmt.Sprintf("%s #%d %s · %s", run.Name, run.RunNumber, k.Label(), w.repo))
		return nil
	}
	t := &toast{kind: nDone, repo: w.repo, run: run, right: dur(run.Duration(time.Now()))}
	if k == KFail {
		t.kind = nError
	}
	a.addToast(t)
	if k == KFail {
		return tea.Batch(a.finish(w.repo, run, k, t), a.st.readCauses(a, []repoRun{{w.repo, run}}))
	}
	return a.finish(w.repo, run, k, t)
}

// notificationText says which run finished and how, with what it was for
// (the pull request or commit title), where it ran and who started it.
func notificationText(repo string, run gh.Run, k Kind, me, failed string) (title, body string) {
	outcome, mark := "failed", "✕"
	if k == KOK {
		outcome, mark = "passed", "✓"
	}
	name := repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		name = repo[i+1:]
	}
	title = fmt.Sprintf("%s %s #%d %s · %s", mark, run.Name, run.RunNumber, outcome, name)
	where := "branch " + run.HeadBranch
	if len(run.PullRequests) > 0 {
		where = fmt.Sprintf("PR #%d · %s", run.PullRequests[0].Number, where)
	}
	who := run.Actor.Login
	if who == me && who != "" {
		who = "you"
	}
	if who != "" {
		who = " · by " + who
	}
	body = fmt.Sprintf("%s\n%s%s · took %s", run.DisplayTitle, where, who, dur(run.Duration(time.Now())))
	if failed != "" {
		body += "\n" + failed
	}
	return title, body
}

// sortedWatches lists running watches first, then finished ones, newest first.
func (a *App) sortedWatches() []*watch {
	out := make([]*watch, 0, len(a.watches))
	for _, w := range a.watches {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if ai, aj := out[i].run.Active(), out[j].run.Active(); ai != aj {
			return ai
		}
		return out[i].since.After(out[j].since)
	})
	return out
}

func (a *App) runningWatches() int {
	n := 0
	for _, w := range a.watches {
		if w.run.Active() {
			n++
		}
	}
	return n
}

// watchView is the Watching tab, in three views: every run you watch, with
// its current state, the pull requests you watch, and the repos whose new
// runs are watched by themselves.
type watchView struct {
	id    int
	view  int // watchRuns, watchPRs or watchRepos
	t     *Table
	prs   *Table
	repos *Table
	peek  peeker
}

// The views of the Watching tab, in the order of its strip.
const (
	watchRuns = iota
	watchPRs
	watchRepos
)

var watchViews = []string{"Runs", "Pull requests", "Repos"}

func newWatchView(a *App) *watchView {
	v := &watchView{id: a.newID()}
	v.t = newTable(
		Column{Title: "STATUS", Help: "how the run is doing"},
		Column{Title: "REPO", Max: 30, Flex: true, MidCut: true, Help: "the repository it runs in"},
		Column{Title: "WORKFLOW", Max: 26, Flex: true, Drop: 2, Help: "the workflow"},
		Column{Title: "RUN", Right: true},
		Column{Title: "TITLE", Max: 50, Flex: true, Below: true},
		Column{Title: "BRANCH", Max: 26, Flex: true, Drop: 1},
		Column{Title: "ACTOR", Max: 18, Flex: true, Drop: 3},
		Column{Title: "AGE", Right: true, Min: 3, Help: "how long ago it started"},
		Column{Title: "DURATION", Right: true, Min: 7, Help: "how long it has taken so far, or took"},
		Column{Title: "PROGRESS", Drop: 4, Help: "how many of its jobs have finished"},
		Column{Title: "ETA", Drop: 5, Help: "when it should be done, going by how long its successful runs usually take"},
		Column{Title: "VIA", Drop: 6, Help: "you, with w on the run, or the watch of its repo"},
	)
	v.t.Empty = "Nothing watched yet. Press w on a run to be notified when it succeeds or fails, or W on a repo to watch its new runs."
	v.repos = newTable(
		Column{Title: "STATUS", Help: "whether its runs could be read, and how many of the runs it found are running"},
		Column{Title: "REPO", Max: 30, Flex: true, MidCut: true, Help: "the repository"},
		Column{Title: "WORKFLOWS", Max: 40, Flex: true, Help: "the workflows whose new runs are watched"},
		Column{Title: "BRANCHES", Max: 30, Flex: true, Drop: 1, Help: "the branches whose new runs are watched"},
		Column{Title: "ACTIVITY", Max: 36, Flex: true, Drop: 3, Help: "what else of the repo is told about: new commits on the default branch, pull requests opened and merged, releases"},
		Column{Title: "LAST CHANGE", Max: 40, Flex: true, Drop: 2, Help: "the latest new run or other change it found, and how long ago"},
	)
	v.repos.Empty = "No repo watched yet. Press W on a repo to watch its new runs."
	v.prs = newTable(
		Column{Title: "STATE", Help: "open, draft, merged or closed"},
		Column{Title: "PR", Right: true},
		Column{Title: "TITLE", Max: 50, Flex: true},
		Column{Title: "REPO", Max: 30, Flex: true, MidCut: true, Drop: 2, Help: "the repository"},
		Column{Title: "CHECKS", Drop: 1, Help: "how the checks on its latest commit are doing"},
		Column{Title: "REVIEW", Drop: 3, Help: "what the reviews decided"},
		Column{Title: "LAST CHANGE", Max: 44, Flex: true, Drop: 4, Help: "the latest change seen while you watched it, and how long ago"},
	)
	v.prs.Empty = "No pull request watched yet. Press w on one to hear when it is reviewed, pushed to, merged or its checks finish."
	v.t.Detail = func(r Row) []string {
		for _, w := range a.watches {
			if fmt.Sprint(w.run.ID) != r.Key {
				continue
			}
			lines := runDetail(repoRun{w.repo, w.run})
			if w.failed != nil {
				lines = append(lines, " "+sAmber.Render("could not be refreshed: "+w.failed.Error()))
			}
			return lines
		}
		return nil
	}
	return v
}

func (v *watchView) ID() int             { return v.id }
func (v *watchView) Crumb() string       { return "Watching" }
func (v *watchView) ViewName() string    { return watchViews[v.view] }
func (v *watchView) Init(a *App) tea.Cmd { return nil }

func (v *watchView) Refresh(a *App, force bool) tea.Cmd {
	return tea.Batch(a.pollWatches(), a.pollRepoWatches(), a.pollPRWatches(), a.pollActivity())
}

// list is the table of the view on display.
func (v *watchView) list() *Table {
	switch v.view {
	case watchRepos:
		return v.repos
	case watchPRs:
		return v.prs
	}
	return v.t
}

func (v *watchView) SetFilter(a *App, q string)   { v.list().SetFilter(q, a.me) }
func (v *watchView) FilterValue() string          { return v.list().Filter() }
func (v *watchView) FilterInfo(a *App) filterInfo { return v.list().Info() }

func (v *watchView) selected(a *App) (repoRun, bool) {
	if v.view != watchRuns {
		return repoRun{}, false
	}
	r := v.t.Selected()
	if r == nil {
		return repoRun{}, false
	}
	for _, w := range a.watches {
		if fmt.Sprint(w.run.ID) == r.Key {
			return repoRun{w.repo, w.run}, true
		}
	}
	return repoRun{}, false
}

func (v *watchView) viewKey() Binding {
	return viewKeys("next view", "runs / pull requests / repos you watch", "", func(d int) tea.Cmd {
		v.view = (v.view + d + len(watchViews)) % len(watchViews)
		return nil
	})
}

func (v *watchView) Keys(a *App) []Binding {
	switch v.view {
	case watchRepos:
		return v.repoKeys(a)
	case watchPRs:
		return v.prWatchKeys(a)
	}
	var bs []Binding
	if x, ok := v.selected(a); ok {
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run", Do: do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })},
			Binding{Keys: []string{"w"}, Desc: "stop watching", Do: do(func() tea.Cmd { return a.watch(x.repo, x.run) })},
		)
		acts := a.runKeys(x.repo, x.run)
		bs = append(bs, without(acts, "o", "y")...)
		bs = append(bs, Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })},
			a.runRepoKey(x.repo, x.run))
		bs = append(bs, only(acts, "o", "y")...)
	}
	return grouped("WATCHING", append(bs, v.viewKey(), filterHint, a.compactKey(), v.t.moveKeys())...)
}

// repoKeys are the keys of the Repos view: open, change or stop the watch.
func (v *watchView) repoKeys(a *App) []Binding {
	var bs []Binding
	if r := v.repos.Selected(); r != nil {
		repo := r.Key
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open repo", Do: do(func() tea.Cmd { return a.push(newRepoView(a, repo, "")) })},
			Binding{Keys: []string{"w"}, Desc: "change watch", Help: "change what is watched, or stop watching", Do: do(func() tea.Cmd { return a.openRepoWatch(repo) })},
			Binding{Desc: "stop watching", Help: "stop watching the repo", Do: do(func() tea.Cmd { return a.stopRepoWatch(repo) })},
		)
	}
	return grouped("WATCHING", append(bs, v.viewKey(), filterHint, v.repos.moveKeys())...)
}

func (v *watchView) Update(a *App, msg tea.Msg) tea.Cmd {
	if cmd, ok := v.peek.handle(a, v.id, msg); ok {
		return cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		if x, ok := v.selected(a); ok {
			return tea.Batch(cmd, v.peek.ask(a, v.id, x.repo, &x.run))
		}
		return cmd
	}
	return nil
}

// progressCells say how far a watched run is (jobs done of all) and how
// long it has left, from how long its workflow usually takes.
func (a *App) progressCells(x repoRun, now time.Time) (Cell, Cell) {
	progress, eta := Cell{Text: "–", Styled: sDim.Render("–")}, Cell{Text: "–", Styled: sDim.Render("–")}
	if done, total, ok := a.jobProgress(x.repo, x.run); ok {
		bar := strings.Repeat("▰", done) + strings.Repeat("▱", total-done)
		text := fmt.Sprintf("%s %d/%d", bar, done, total)
		progress = Cell{Text: text, Styled: sAmber.Render(strings.Repeat("▰", done)) + sDim.Render(strings.Repeat("▱", total-done)) + sText.Render(fmt.Sprintf(" %d/%d", done, total))}
		if !x.run.Active() {
			progress.Styled = sGreen.Render(strings.Repeat("▰", done)) + sText.Render(fmt.Sprintf(" %d/%d", done, total))
			if StatusKind(x.run.Status, x.run.Conclusion) == KFail {
				progress.Styled = sRed.Render(strings.Repeat("▰", done)) + sText.Render(fmt.Sprintf(" %d/%d", done, total))
			}
		}
	}
	if !x.run.Active() {
		t := "done " + age(x.run.UpdatedAt, now) + " ago"
		return progress, Cell{Text: t, Styled: sText.Render(t)}
	}
	if usual := medianDuration(a.comparable(x.repo, x.run, a.workflowRuns(x.repo, x.run.Name))); usual > 0 {
		t := "taking longer"
		if left := usual - x.run.Duration(now); left > 0 {
			t = "~" + dur(left) + " left"
		}
		eta = Cell{Text: t, Styled: sAmber.Render(t)}
	}
	return progress, eta
}

// sync lists the watched runs as they are now.
func (v *watchView) sync(a *App) {
	now := time.Now()
	var rows []Row
	for _, x := range a.sortedWatches() {
		row := activityRow(a, fmt.Sprint(x.run.ID), repoRun{x.repo, x.run}, now, false)
		progress, eta := a.progressCells(repoRun{x.repo, x.run}, now)
		row.Cells = append(row.Cells, progress, eta, viaCell(x))
		rows = append(rows, row)
	}
	v.repos.SetRows(a.repoWatchRows(now))
	v.prs.SetRows(a.prWatchRows(now))
	v.t.Card = func(r Row, w int) (string, string) {
		for _, x := range a.watches {
			if fmt.Sprint(x.run.ID) == r.Key {
				return runCard(a, x.repo, x.run, nil, w, now)
			}
		}
		return "", ""
	}
	v.t.SetRows(rows)
}

// viaCell says how a run came to be watched.
func viaCell(x *watch) Cell {
	if x.fromRepo {
		return iconText("repo", "repo", true)
	}
	return steelCell("you", false)
}

func (v *watchView) Render(a *App, w, h int) string {
	v.t.TwoLine = a.twoLine
	v.t.Cards = !a.compact
	v.t.HideDetail = w >= panelMinWidth
	running, passed, failed := 0, 0, 0
	for _, x := range a.watches {
		switch StatusKind(x.run.Status, x.run.Conclusion) {
		case KOK:
			passed++
		case KFail:
			failed++
		default:
			if x.run.Active() {
				running++
			}
		}
	}
	var chips []string
	if running > 0 {
		chips = append(chips, statusChip(KRun, a.frame, fmt.Sprintf("%d running", running)))
	}
	if passed > 0 {
		chips = append(chips, statusChip(KOK, 0, fmt.Sprintf("%d passed", passed)))
	}
	if failed > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failed", failed)))
	}
	chips = append(chips, fact("bell", "notifications on"))
	items := []stripItem{
		{Label: watchViews[watchRuns], Icon: "actions", Count: fmt.Sprint(v.t.Len())},
		{Label: watchViews[watchPRs], Icon: "pr", Count: fmt.Sprint(v.prs.Len())},
		{Label: watchViews[watchRepos], Icon: "repo", Count: fmt.Sprint(v.repos.Len())},
	}
	if running > 0 {
		items[watchRuns].St = &sAmber
	}
	l := v.list()
	head := []string{screenTitle("watching", "Watching", "the runs, pull requests and repos you watch, with a card when one changes", l.Len(), chips, w), viewStrip(items, v.view, w), " " + a.filterLine(l.Filter(), v.FilterInfo(a))}
	return listWithPanel(a, w, h, head,
		func(lw, lh int) string { return l.Render(lw, lh) },
		func(pw, ph int) []string {
			switch v.view {
			case watchRepos:
				if r := v.repos.Selected(); r != nil {
					return a.repoWatchPanel(r.Key, pw, ph)
				}
				return emptyPanel(pw, ph, "No repo watched yet. Press W on a repo.")
			case watchPRs:
				if r := v.prs.Selected(); r != nil {
					return a.prWatchPanel(r.Key, pw, ph)
				}
				return emptyPanel(pw, ph, "No pull request watched yet. Press w on one.")
			}
			if x, ok := v.selected(a); ok {
				return a.runPanel(x, pw, ph)
			}
			return emptyPanel(pw, ph, "Nothing watched yet. Press w on a run.")
		})
}
