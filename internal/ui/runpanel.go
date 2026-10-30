package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runPanel describes a run in a panel beside a list of runs: where it comes
// from, how far it is and how long it usually takes, how its workflow has
// been doing and, when we hold them, its jobs.
func (a *App) runPanel(x repoRun, w, h int) []string {
	now := time.Now()
	run := x.run
	k := StatusKind(run.Status, run.Conclusion)
	p := newPanel(w)
	// A tile in the colour of how it is doing, with the run and what it is
	// for beside it.
	tw := max(w-3-tileW-2, 10)
	what := strings.Split(ansi.Wordwrap(run.DisplayTitle, tw, ""), "\n")
	art := tile(k.Color(), tileGlyph(k, a.frame))
	state := "after " + dur(run.Duration(now))
	if run.Active() {
		state = "for " + dur(run.Duration(now))
	}
	if run.RunAttempt > 1 {
		state += fmt.Sprintf(" · attempt %d", run.RunAttempt)
	}
	if run.Requeued {
		state += " · " + waitsForRunner
	}
	head := []string{
		sWhite.Bold(true).Render(run.Name) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", run.RunNumber)),
		sText.Render(what[0]),
		fg(k.Color()).Bold(true).Render(k.Label()) + " " + sText.Render(state),
	}
	if len(what) > 1 {
		head = slices.Insert(head, 2, sText.Render(what[1]))
	}
	for i, t := range head {
		pic := strings.Repeat(" ", tileW)
		if i < len(art) {
			pic = fit(art[i], tileW)
		}
		p.add(pic + "  " + t)
	}

	sha := run.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	p.add("")
	p.add(fact("repo", x.repo))
	p.add(fact("branch", run.HeadBranch) + sDim.Render(" @ "+sha))
	p.add(fact("user", run.Actor.Login) + sDim.Render(" · "+run.Event+" · ") + sText.Render(age(run.CreatedAt, now)+" ago"))
	a.runPRSection(p, x.repo, run)

	same := a.workflowRuns(x.repo, run.Name)
	usual := medianDuration(same)
	if done, total, ok := a.jobProgress(x.repo, run); run.Active() && ok {
		p.label("Progress")
		p.add(sAmber.Render(strings.Repeat("▰", done)) + sDim.Render(strings.Repeat("▱", total-done)) + sText.Render(fmt.Sprintf(" %d of %d jobs", done, total)))
	}
	durationSection(p, run, a.comparable(x.repo, run, same), now)
	if len(same) > 1 {
		var points []historyPoint
		for i := len(same) - 1; i >= 0; i-- {
			r := same[i]
			points = append(points, historyPoint{kind: StatusKind(r.Status, r.Conclusion), at: r.CreatedAt, took: r.Duration(time.Now()), label: fmt.Sprintf("#%d", r.RunNumber)})
		}
		historySection(a, p, "Workflow history", points, usual)
	}
	p.label("Jobs")
	a.jobLines(p, x.repo, run, now)
	if k == KFail {
		if _, cached := a.client.CachedJobs(x.repo, run.ID); cached {
			p.add("")
			p.add(keycap("enter") + sText.Render(" why it failed  ") + keycap("l") + sText.Render(" log"))
		}
	}
	return p.render(h)
}

// durationSection compares how long a run took, or has taken so far, with
// the median and the previous successful run of its workflow among the runs
// held, as bars on one scale.
func durationSection(p *panelText, run gh.Run, same []gh.Run, now time.Time) {
	var earlier []gh.Run
	var previous *gh.Run
	for i, r := range same {
		if r.ID == run.ID || r.Status != "completed" || r.Conclusion != "success" {
			continue
		}
		earlier = append(earlier, r)
		if r.CreatedAt.Before(run.CreatedAt) && (previous == nil || r.CreatedAt.After(previous.CreatedAt)) {
			previous = &same[i]
		}
	}
	usual := medianDuration(earlier)
	if usual == 0 && previous == nil {
		return
	}
	k := StatusKind(run.Status, run.Conclusion)
	took := run.Duration(now)
	scale := max(took, usual)
	if previous != nil {
		scale = max(scale, previous.Duration(now))
	}
	p.label("Duration")
	gw := p.inner()
	scale = scale * 115 / 100
	at := func(d time.Duration) int { return min(int(int64(d)*int64(gw)/int64(scale)), gw-1) }
	c := k.Color()
	if run.Active() {
		c = cAmber
	}
	p.add(gauge(at(took), at(usual), usual > 0, gw, c))
	label := "this run "
	if run.Active() {
		label = "so far "
	}
	left := sDim.Render(label) + sWhite.Bold(true).Render(dur(took))
	right := ""
	if usual > 0 {
		right = sDim.Render("median ") + sText.Render(dur(usual)) + sDim.Render(fmt.Sprintf(" of %d", len(earlier)))
	}
	p.add(lineLR(left, right, gw))
	switch {
	case run.Active() && usual > 0:
		eta := sAmber.Render("taking longer than usual")
		if left := usual - took; left > 0 {
			eta = sAmber.Render("about " + dur(left) + " left")
		}
		p.add(eta)
	case !run.Active():
		base := usual
		if base == 0 {
			base = previous.Duration(now)
		}
		if note, color := compare(took, base, k); note != "" {
			p.add(deltaPill(note, color))
		}
	}
	if previous != nil {
		p.add(sDim.Render("previous ") + sGold.Render(fmt.Sprintf("#%d", previous.RunNumber)) + " " + fg(cSteel).Render(dur(previous.Duration(now))))
	}
}

// gauge is a thin line w cells long, lit in c up to lit with its head
// brightest, with a white tick where the usual time falls.
func gauge(lit, tick int, ticked bool, w int, c lipgloss.Color) string {
	on, off, mark := "━", "─", "┃"
	if ascii {
		on, off, mark = "=", "-", "|"
	}
	var b strings.Builder
	for i := range w {
		switch {
		case ticked && i == tick:
			b.WriteString(sWhite.Bold(true).Render(mark))
		case i <= lit:
			b.WriteString(fg(lipgloss.Color(mix(string(cPanel), string(c), 0.35+0.65*float64(i+1)/float64(lit+1)))).Render(on))
		default:
			b.WriteString(fg(cFrame).Render(off))
		}
	}
	return b.String()
}

// durationBar is d as a bar on a scale where scale fills w cells, drawn to
// an eighth of a cell.
func durationBar(d, scale time.Duration, w int) string {
	if scale <= 0 {
		return ""
	}
	eighths := int(int64(d) * int64(w*8) / int64(scale))
	if ascii {
		return strings.Repeat("#", eighths/8)
	}
	bar := strings.Repeat("█", eighths/8)
	if part := eighths % 8; part > 0 {
		bar += string([]rune(" ▏▎▍▌▋▊▉")[part])
	}
	return bar
}

// runPRSection names the pull requests a run belongs to and, for those we
// hold, how they stand.
func (a *App) runPRSection(p *panelText, repo string, run gh.Run) {
	numbers := a.runPRNumbers(repo, run)
	if len(numbers) == 0 {
		return
	}
	p.label("Pull request")
	for _, n := range numbers {
		pr, held := a.heldPR(repo, n)
		if !held {
			p.add(a.prMark(repo, n))
			continue
		}
		p.add(a.prMark(repo, n) + " " + sWhite.Render(pr.Title))
		p.add(reviewCell(pr).Styled + sDim.Render(" · ") + checksCell(pr.Checks).Styled)
	}
	p.add(keycap("#") + sText.Render(" open it"))
}

// prMark is a pull request's icon in the colour of its state, when we hold
// it, and its number.
func (a *App) prMark(repo string, n int) string {
	mark := icoIn("pr", cGrey)
	if pr, ok := a.heldPR(repo, n); ok {
		mark = prIcon("OPEN", pr.Draft)
	}
	return mark + sGold.Bold(true).Render(fmt.Sprintf("#%d", n))
}

// runPRNumbers are the pull requests a run belongs to: those GitHub links it
// to or, when it links none, the open ones we hold that come from the branch
// it ran on. A pull request from a fork's branch of the same name as its base
// is left out, or every run on the base would belong to it.
func (a *App) runPRNumbers(repo string, run gh.Run) []int {
	var out []int
	for _, pr := range run.PullRequests {
		out = append(out, pr.Number)
	}
	if len(out) > 0 {
		return out
	}
	for _, pr := range a.heldPRs(repo) {
		if pr.Branch == run.HeadBranch && pr.Branch != pr.Base {
			out = append(out, pr.Number)
		}
	}
	return out
}

// heldPRs are the open pull requests of repo we already hold: from your own
// list and from the repo's list when it was read.
func (a *App) heldPRs(repo string) []gh.PullRequest {
	var listed []gh.PullRequest
	a.client.Recall(repoPullsSnapshot(repo), &listed)
	var out []gh.PullRequest
	seen := map[int]bool{}
	for _, pr := range append(listed, a.st.prs...) {
		if strings.EqualFold(pr.Repo, repo) && !seen[pr.Number] {
			seen[pr.Number] = true
			out = append(out, pr)
		}
	}
	return out
}

func (a *App) heldPR(repo string, n int) (gh.PullRequest, bool) {
	for _, pr := range a.heldPRs(repo) {
		if pr.Number == n {
			return pr, true
		}
	}
	return gh.PullRequest{}, false
}

// openRunPR opens the pull request a run belongs to, asking which when it
// belongs to several.
func (a *App) openRunPR(repo string, run gh.Run) tea.Cmd {
	numbers := a.runPRNumbers(repo, run)
	switch len(numbers) {
	case 0:
		return a.flashCmd(nHint, "This run belongs to no pull request we know of.")
	case 1:
		return a.push(newPRView(a, repo, numbers[0]))
	}
	var items []pickItem
	for _, n := range numbers {
		it := pickItem{value: fmt.Sprint(n), label: fmt.Sprintf("#%d", n)}
		if pr, ok := a.heldPR(repo, n); ok {
			it.label += " " + pr.Title
		}
		items = append(items, it)
	}
	return openModal(newPicker("run-pr", "Which pull request?", "", items, func(v string) tea.Cmd {
		n, _ := strconv.Atoi(v)
		return a.push(newPRView(a, repo, n))
	}))
}
