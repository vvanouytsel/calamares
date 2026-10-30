package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// A toast is a card above the key bar for what happens to a run you watch:
// it started, it passed, it failed. A failed one stays until you open or
// dismiss it, or for a while.
type toast struct {
	kind   noticeKind
	repo   string
	run    gh.Run
	at     time.Time
	life   time.Duration
	right  string // what the card says at the right: "now", or how long the run took
	detail string // the failed job and step
	// pr, when set, makes it a card about a watched pull request instead of a run;
	// act a card about what happened on a watched repo besides its runs.
	pr  *prCard
	act *repoNews
}

// prCard is what a card about a watched pull request shows: the pull
// request and what changed on it, the weightiest first.
type prCard struct {
	number         int
	title, url     string
	branch, author string
	state          string
	draft          bool
	news           []prNews
}

const (
	maxToasts   = 4
	toastLife   = 8 * time.Second
	toastPassed = 10 * time.Second
	toastFailed = 2 * time.Minute
	toastWidth  = 64
)

// addToast shows a card and keeps the same words for the Messages screen.
func (a *App) addToast(t *toast) {
	t.at = time.Now()
	t.life = toastLife
	switch t.kind {
	case nDone:
		t.life = toastPassed
	case nError:
		t.life = toastFailed
	}
	a.toasts = append(a.toasts, t)
	if len(a.toasts) > maxToasts {
		a.toasts = a.toasts[len(a.toasts)-maxToasts:]
	}
	a.note(t.kind, t.message())
}

// expireToasts removes the cards that were on screen long enough.
func (a *App) expireToasts() {
	a.toasts = a.toasts[:copyIf(a.toasts, func(t *toast) bool { return time.Since(t.at) <= t.life })]
}

// copyIf keeps the items that pass, in place, and returns how many.
func copyIf(ts []*toast, keep func(*toast) bool) int {
	n := 0
	for _, t := range ts {
		if keep(t) {
			ts[n] = t
			n++
		}
	}
	return n
}

// title is the run as the card names it: "Deploy #88".
func (t *toast) title() string {
	if t.pr != nil {
		return fmt.Sprintf("#%d %s", t.pr.number, t.pr.title)
	}
	if t.act != nil {
		return t.act.Text
	}
	name := t.run.Name
	if name == "" {
		name = t.run.DisplayTitle
	}
	return fmt.Sprintf("%s #%d", name, t.run.RunNumber)
}

// word is what the chip says.
func (t *toast) word() string {
	if t.pr != nil {
		return t.pr.news[0].Word
	}
	if t.act != nil {
		return t.act.Word
	}
	switch t.kind {
	case nStarted:
		return "started"
	case nDone:
		return "passed"
	}
	return "failed"
}

// message is the toast as one line, for the Messages screen.
func (t *toast) message() string {
	if t.act != nil {
		return t.act.Text + " · " + t.repo
	}
	s := fmt.Sprintf("%s %s · %s", t.title(), t.word(), t.repo)
	if t.pr != nil {
		var what []string
		for _, n := range t.pr.news {
			what = append(what, n.Text)
		}
		s += " · " + strings.Join(what, ", ")
	}
	if t.detail != "" {
		s += " · " + t.detail
	}
	return s
}

// kindOf is the status a card stands for.
func (t *toast) kindOf() Kind {
	if t.pr != nil {
		return t.pr.news[0].Kind
	}
	if t.act != nil {
		return t.act.Kind
	}
	switch t.kind {
	case nStarted:
		return KRun
	case nDone:
		return KOK
	}
	return KFail
}

// renderToast is the inside of a card w cells wide, for a card on its own.
func (a *App) renderToast(t *toast, w int) []string { return a.toastSlab(t, w, 0).body }

// toastSlab is a card as a slab: the tile in the colour of how the run (or
// pull request, or repo) is doing, the title with its status word once and
// how long it took, the facts, what it is for and how it went, and its keys.
// more counts the cards behind it.
func (a *App) toastSlab(t *toast, w, more int) *slab {
	k := t.kindOf()
	c, fill := arriving(t.at, k.Color())
	tw := w - tileW - 5
	word := fg(k.Color()).Bold(true).Render(t.word())
	glyph := tileGlyph(k, a.frame)
	var head, meta, body []string
	switch {
	case t.act != nil:
		title, lines := a.actCard(t.act, tw)
		head = []string{lineLR(title, word, tw)}
		meta = []string{fact("repo", t.repo)}
		body = lines
		glyph = actGlyph(t.act, glyph)
	case t.pr != nil:
		pr := t.pr
		head = []string{lineLR(sGold.Bold(true).Render(fmt.Sprintf("#%d", pr.number))+" "+sWhite.Bold(true).Render(pr.title), word, tw)}
		facts := []string{fact("repo", t.repo), fact("branch", pr.branch)}
		if pr.author != "" {
			facts = append(facts, fact("user", pr.author))
		}
		meta = wrapCapped(joinFacts(facts...), tw, 2)
		for _, n := range pr.news[:min(len(pr.news), 4)] {
			body = append(body, wrapCapped(fg(n.Kind.Color()).Render(n.Kind.Glyph(a.frame))+" "+sText.Render(n.Text), tw, 2)...)
		}
		if g := strings.TrimSpace(icon("pr")); g != "" {
			glyph = g
		}
	default:
		title := sWhite.Bold(true).Render(t.run.Name) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", t.run.RunNumber))
		if t.run.RunAttempt > 1 {
			title += sDim.Render(fmt.Sprintf(" · attempt %d", t.run.RunAttempt))
		}
		head = []string{lineLR(title+"  "+word, fg(cSteel).Render(t.right), tw)}
		meta = wrapCapped(joinFacts(a.toastFacts(t)...), tw, 2)
		if t.run.DisplayTitle != "" {
			what := sText.Render(t.run.DisplayTitle)
			if len(t.run.PullRequests) > 0 {
				what = icoIn("pr", cGreen) + sGold.Render(fmt.Sprintf("#%d", t.run.PullRequests[0].Number)) + " " + what
			}
			body = wrapCapped(what, tw, 2)
		}
		body = append(body, a.toastOutcome(t, fill, tw)...)
	}
	text := append([]string{""}, head...)
	text = append(text, meta...)
	text = append(text, "")
	text = append(text, body...)
	keys := a.toastKeys(t, fill)
	if more > 0 {
		keys = lineLR(keys, sDim.Render(fmt.Sprintf("%d more ▴", more)), tw)
	}
	text = append(text, "", keys)
	return &slab{at: t.at, life: t.life, color: c, fill: fill, body: slabBody(c, fill, tile(c, glyph), tileW, text, w), peek: a.peekLine(t)}
}

// actGlyph is the icon on the tile of a card about a repo's activity.
func actGlyph(n *repoNews, or string) string {
	name := "commit"
	switch {
	case n.Pull != nil:
		name = "pr"
	case n.Release != nil:
		name = "tag"
	}
	if g := strings.TrimSpace(icon(name)); g != "" {
		return g
	}
	return or
}

// toastFacts are the repo, where the run ran, who started it and why, each
// with its icon.
func (a *App) toastFacts(t *toast) []string {
	facts := []string{fact("repo", t.repo), fact("branch", t.run.HeadBranch)}
	who := t.run.Actor.Login
	if who == a.me && who != "" {
		who = "you"
	}
	if who != "" {
		facts = append(facts, fact("user", who))
	}
	if t.run.Event != "" {
		facts = append(facts, fg(cSteel).Render(t.run.Event))
	}
	return facts
}

// joinFacts puts facts side by side with room between them.
func joinFacts(facts ...string) string { return strings.Join(facts, "  ") }

// toastOutcome is how the run is going: its jobs and how long it usually
// takes while it runs, how its time compares when it passed, and what broke
// when it failed, set into the card on a well.
func (a *App) toastOutcome(t *toast, fill lipgloss.Color, w int) []string {
	same := a.comparable(t.repo, t.run, a.workflowRuns(t.repo, t.run.Name))
	usual := medianDuration(same)
	switch t.kind {
	case nStarted:
		var parts []string
		if done, total, ok := a.jobProgress(t.repo, t.run); ok {
			parts = append(parts, sAmber.Render(strings.Repeat("▰", done))+sDim.Render(strings.Repeat("▱", total-done))+" "+sText.Render(fmt.Sprintf("%d of %d jobs", done, total)))
		}
		if usual > 0 {
			parts = append(parts, fg(cSteel).Render("usually "+dur(usual)))
		}
		if len(parts) == 0 {
			return nil
		}
		return []string{strings.Join(parts, sDim.Render(" · "))}
	case nDone:
		if usual > 0 && len(same) > 1 {
			return []string{versusUsual(t.run.Duration(time.Now()), usual, len(same))}
		}
		return nil
	}
	var out []string
	cause := a.st.causes[causeID(t.repo, t.run)]
	where := sWhite.Render(t.detail)
	if cause.Job != "" {
		where = sWhite.Bold(true).Render(cause.Job)
		if cause.Step != "" {
			where += sDim.Render(" › ") + sWhite.Render(cause.Step)
		}
	}
	if cause.Job != "" || t.detail != "" {
		for _, l := range wrapCapped(where, w-2, 2) {
			out = append(out, inset(l, cRed, fill, w))
		}
	}
	if cause.Text != "" && cause.Text != noErrorLine {
		for _, l := range wrapCapped(cause.Text, w-2, 3) {
			out = append(out, inset(fg(cPink).Render(l), cRed, fill, w))
		}
	}
	return out
}

// versusUsual says how a run's time stands against the median of n runs.
func versusUsual(took, usual time.Duration, n int) string {
	d := took - usual
	switch {
	case d < -5*time.Second:
		return sGreen.Render("▼ "+dur(-d)+" faster") + sDim.Render(fmt.Sprintf(" than the median of %d runs", n))
	case d > 5*time.Second:
		return sAmber.Render("▲ "+dur(d)+" slower") + sDim.Render(fmt.Sprintf(" than the median of %d runs", n))
	}
	return sDim.Render(fmt.Sprintf("as long as the median of %d runs", n))
}

// toastKeys are the keys of the newest card, as round caps on its surface.
func (a *App) toastKeys(t *toast, under lipgloss.Color) string {
	keys := []string{roundKey("ctrl+o", under) + " " + sHint.Render("open")}
	if t.kind == nError && t.pr == nil && t.act == nil {
		keys = append(keys, roundKey("ctrl+l", under)+" "+sHint.Render("log"))
	}
	keys = append(keys, roundKey("ctrl+x", under)+" "+sHint.Render("dismiss"))
	return strings.Join(keys, "  ")
}

// wrapCapped wraps s to lines of w cells, at most n of them, the last cut
// with an ellipsis when there is more.
func wrapCapped(s string, w, n int) []string {
	lines := strings.Split(ansi.Wrap(s, max(w, 4), ""), "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines[n-1] = clip(lines[n-1]+" …", w)
	}
	return lines
}

// peekLine is a card as one line, for when it sits behind a newer one: how
// it is doing, what it is about and its repo.
func (a *App) peekLine(t *toast) string {
	k := t.kindOf()
	what := sText.Render(t.title()) + " " + fg(k.Color()).Render(t.word())
	if t.act != nil {
		what = sText.Render(t.act.Text)
	}
	return fg(k.Color()).Render(k.Glyph(a.frame)) + " " + what + sDim.Render("  "+t.repo)
}

// deck is the cards on screen, oldest first: the cards of runs, pull
// requests and repos, with the card of what you unlocked in its place in
// time. Only the card in front says how many wait behind it.
func (a *App) deck(w int) []*slab {
	cw := min(cardWidth, w-4)
	var aw *award
	if len(a.awards) > 0 && !a.awards[0].at.IsZero() {
		aw = a.awards[0]
	}
	type item struct {
		t *toast
		w *award
	}
	var items []item
	for _, t := range a.toasts {
		if aw != nil && aw.at.Before(t.at) {
			items = append(items, item{w: aw})
			aw = nil
		}
		items = append(items, item{t: t})
	}
	if aw != nil {
		items = append(items, item{w: aw})
	}
	out := make([]*slab, len(items))
	for i, it := range items {
		more := 0
		if i == len(items)-1 {
			more = len(items) - 1
		}
		if it.w != nil {
			out[i] = a.awardSlab(it.w, cw, more)
		} else {
			out[i] = a.toastSlab(it.t, cw, more)
		}
	}
	return out
}

// laid lays the cards over the bottom right of the main area; a new one
// glides in from the edge.
func (a *App) laid(main string, w, h int) string { return layDeck(main, a.deck(w), w, h) }

// openToastRun opens the run, or the pull request, in the newest card.
func (a *App) openToastRun() tea.Cmd {
	for i := len(a.toasts) - 1; i >= 0; i-- {
		t := a.toasts[i]
		switch {
		case t.pr != nil:
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			return a.push(newPRView(a, t.repo, t.pr.number))
		case t.act != nil:
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			return a.openActivity(t.repo, t.act)
		case t.run.ID != 0:
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			return a.push(newRunView(a, t.repo, t.run))
		}
	}
	return a.flashCmd(nHint, "No run to open: nothing was started or finished lately.")
}

// openToastLog opens the log of the failed run in the newest card that has one.
func (a *App) openToastLog() tea.Cmd {
	for i := len(a.toasts) - 1; i >= 0; i-- {
		if t := a.toasts[i]; t.kind == nError && t.run.ID != 0 {
			a.toasts = append(a.toasts[:i], a.toasts[i+1:]...)
			a.earn("root-cause", 1)
			return a.push(newLogView(a, t.repo, t.run, nil, 0))
		}
	}
	return a.flashCmd(nHint, "No failed run to open the log of.")
}

// dismissToast takes the newest card off the screen.
func (a *App) dismissToast() tea.Cmd {
	awardFirst := len(a.awards) > 0 && (len(a.toasts) == 0 || a.awards[0].at.After(a.toasts[len(a.toasts)-1].at))
	switch {
	case awardFirst:
		a.dismissAward()
	case len(a.toasts) > 0:
		a.toasts = a.toasts[:len(a.toasts)-1]
	default:
		return a.flashCmd(nHint, "No card to dismiss.")
	}
	a.earn("ghost", 1)
	return nil
}

// failure names the failed job and its failed step, from the jobs of the run.
func failure(jobs []gh.Job) string {
	for _, j := range jobs {
		if StatusKind(j.Status, j.Conclusion) != KFail {
			continue
		}
		for _, s := range j.Steps {
			if StatusKind(s.Status, s.Conclusion) == KFail {
				return fmt.Sprintf("%s · step “%s”", j.Name, s.Name)
			}
		}
		return j.Name
	}
	return ""
}

// finishedMsg carries what was found out about a run that finished: the
// failed step, for the card.
type finishedMsg struct {
	id     int64
	detail string
}

// finish reads the jobs of a failed run once, for the failed step, and sends
// the desktop notification with it. A run that passed needs no jobs.
func (a *App) finish(repo string, run gh.Run, k Kind, t *toast) tea.Cmd {
	me, away := a.me, !a.focused()
	known := t.detail
	return func() tea.Msg {
		detail := known
		if k == KFail && detail == "" {
			if jobs, ok := a.client.CachedJobs(repo, run.ID); ok {
				detail = failure(jobs)
			}
			if detail == "" {
				ctx, cancel := context.WithTimeout(gh.Background(context.Background()), 15*time.Second)
				defer cancel()
				if jobs, err := a.client.ListJobs(ctx, repo, run.ID); err == nil {
					detail = failure(jobs)
				}
			}
		}
		if away {
			title, body := notificationText(repo, run, k, me, detail)
			_ = sendNotification(title, body, run.HTMLURL)
		}
		return finishedMsg{run.ID, detail}
	}
}

// focused says whether the terminal has the focus. Where it can't tell, it
// says no, so a finished run is never missed.
func (a *App) focused() bool { return a.focus == focusYes }

type focusState int

const (
	focusUnknown focusState = iota
	focusYes
	focusNo
)

// gotFinished puts what was found out on the card that is still showing.
func (a *App) gotFinished(m finishedMsg) {
	for _, t := range a.toasts {
		if t.run.ID == m.id && t.kind == nError && m.detail != "" && t.detail == "" {
			t.detail = m.detail
			a.note(nError, t.message())
		}
	}
}

// dispatchedPollMsg asks for the run that a dispatch started.
type dispatchedPollMsg struct {
	repo  string
	wf    int64
	since time.Time
	tries int
	// branch, when set, looks for the first run of any kind on the branch
	// (a pull request starts them) instead of a dispatched one.
	branch string
}

type dispatchedFoundMsg struct {
	dispatchedPollMsg
	run gh.Run
	ok  bool
}

// dispatchLooksFor is how often, and how many times, Calamares looks for the
// run a dispatch started: GitHub answers the dispatch before the run exists.
const (
	dispatchLooksEvery = 3 * time.Second
	dispatchLooks      = 6
)

// awaitDispatched looks for the run a workflow dispatch is about to create,
// so it is watched without a key.
func (a *App) awaitDispatched(repo string, wf int64) tea.Cmd {
	return a.lookForDispatched(dispatchedPollMsg{repo: repo, wf: wf, since: time.Now()})
}

// awaitBranchRun looks for the first run started on a branch, to watch it.
func (a *App) awaitBranchRun(repo, branch string) tea.Cmd {
	return a.lookForDispatched(dispatchedPollMsg{repo: repo, branch: branch, since: time.Now()})
}

func (a *App) lookForDispatched(p dispatchedPollMsg) tea.Cmd {
	return tea.Tick(dispatchLooksEvery, func(time.Time) tea.Msg { return p })
}

// findDispatched reads the newest manual runs of the workflow, once.
func (a *App) findDispatched(p dispatchedPollMsg) tea.Cmd {
	me := a.me
	return func() tea.Msg {
		filter := gh.RunFilter{WorkflowID: p.wf, Event: "workflow_dispatch", PerPage: 5}
		if p.branch != "" {
			filter = gh.RunFilter{Branch: p.branch, PerPage: 5}
		}
		runs, _, err := a.client.ListRuns(gh.Background(context.Background()), p.repo, filter)
		out := dispatchedFoundMsg{dispatchedPollMsg: p}
		if err != nil {
			return out
		}
		for _, r := range runs {
			if r.CreatedAt.Before(p.since.Add(-30*time.Second)) || (p.branch == "" && me != "" && r.Actor.Login != me && r.TriggeringActor.Login != me) {
				continue
			}
			out.run, out.ok = r, true
			break
		}
		return out
	}
}

// foundDispatched watches the run a dispatch started and says it started.
func (a *App) foundDispatched(m dispatchedFoundMsg) tea.Cmd {
	if !m.ok {
		if m.tries+1 >= dispatchLooks {
			return a.flashCmd(nInfo, "Could not find the run you started to watch it: press w on it when it shows up")
		}
		p := m.dispatchedPollMsg
		p.tries++
		return a.lookForDispatched(p)
	}
	if _, ok := a.watches[m.run.ID]; ok {
		return nil
	}
	a.watches[m.run.ID] = &watch{repo: m.repo, run: m.run, since: time.Now()}
	a.saveWatches()
	a.addToast(&toast{kind: nStarted, repo: m.repo, run: m.run, right: "now"})
	return a.pollWatches()
}
