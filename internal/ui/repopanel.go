package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// history draws one tick per commit on a single line, oldest first: green for
// a pass, red for a failure, amber for one still running. A tick is 7/8 of a
// cell wide so a thin seam parts it from the next. A gap after every five makes
// them countable, and the newest is brighter.
func history(kinds []Kind, frame int) (styled, plain string) {
	bars := [...]string{"▉", "▉", "▉"}
	if ascii {
		bars = [...]string{"#", "_", "+"}
	}
	var b, p strings.Builder
	for i, k := range kinds {
		if i > 0 && i%5 == 0 {
			b.WriteString(" ")
			p.WriteString(" ")
		}
		g, c := bars[0], cGreen
		switch k {
		case KFail:
			g, c = bars[1], cRed
		case KRun:
			animated = true
			g, c = bars[2], cAmber
		}
		st := fg(c)
		if i == len(kinds)-1 {
			st = fg(lipgloss.Color(mix(string(c), string(cWhite), 0.35))).Bold(true)
		}
		b.WriteString(st.Render(g))
		p.WriteString(g)
	}
	return b.String(), p.String()
}

// historyPoint is one result in a run history: how it went, when, and what to
// call it when it is the last failure.
type historyPoint struct {
	kind  Kind
	at    time.Time     // zero when unknown
	took  time.Duration // how long it ran; zero when unknown
	label string
}

// sparkline draws points as bars two lines high, newest last: as high as
// the run took (all alike when that is unknown), in the colour of how it
// went, the one that runs breathing. Plain terminals get a glyph each.
func sparkline(a *App, points []historyPoint) []string {
	if ascii {
		var glyphs []string
		for _, pt := range points {
			glyphs = append(glyphs, fg(pt.kind.Color()).Render(pt.kind.Glyph(a.frame)))
		}
		return []string{strings.Join(glyphs, " ")}
	}
	// The scale is the longest run, but at most twice the usual one, so a
	// run that hangs does not flatten the others.
	var most time.Duration
	var took []time.Duration
	for _, pt := range points {
		most = max(most, pt.took)
		if pt.took > 0 {
			took = append(took, pt.took)
		}
	}
	if m := median(took); m > 0 {
		most = min(most, 2*m)
	}
	eighths := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	var top, bottom strings.Builder
	for i, pt := range points {
		h := 12
		if most > 0 {
			h = min(max(int(int64(pt.took)*16/int64(most)), 2), 16)
		}
		c := pt.kind.Color()
		if pt.kind == KRun {
			c = breathe(c)
		}
		st := fg(c)
		lo, hi := eighths[min(h, 8)], eighths[max(h-8, 0)]
		if i > 0 {
			top.WriteString(" ")
			bottom.WriteString(" ")
		}
		top.WriteString(st.Render(hi + hi))
		bottom.WriteString(st.Render(lo + lo))
	}
	return []string{top.String(), bottom.String()}
}

// historySection draws a run history in a panel, newest last: a glyph per
// result with room between them, a line saying which end is old and which is
// new, the share that passed, how long it usually takes, and the last failure.
func historySection(a *App, p *panelText, title string, points []historyPoint, usual time.Duration) {
	if len(points) == 0 {
		return
	}
	shown := points[max(len(points)-(p.inner()+1)/3, 0):]
	p.label(title)
	line := sparkline(a, shown)
	for _, l := range line {
		p.add(l)
	}
	older, newer := "older", "newer"
	if first, last := shown[0].at, shown[len(shown)-1].at; !first.IsZero() && !last.IsZero() {
		now := time.Now()
		older, newer = age(first, now)+" ago", age(last, now)+" ago"
		if now.Sub(last) < time.Minute {
			newer = "now"
		}
	}
	if gap := width(line[0]) - width(older) - width(newer); gap >= 1 {
		p.add(sDim.Render(older + strings.Repeat(" ", gap) + newer))
	} else {
		p.add(sDim.Render(older + " → " + newer))
	}
	p.add("")
	passed, done := 0, 0
	var lastFail *historyPoint
	for i := range points {
		switch points[i].kind {
		case KOK:
			passed++
			done++
		case KFail:
			done++
			lastFail = &points[i]
		}
	}
	var summary []string
	if done > 0 {
		pct := passed * 100 / done
		summary = append(summary, rateStyle(pct).Bold(true).Render(fmt.Sprintf("%d%%", pct))+sDim.Render(" passed  ")+
			sWhite.Render(fmt.Sprint(passed))+sDim.Render(" of ")+sWhite.Render(fmt.Sprint(done)))
	}
	if usual > 0 {
		summary = append(summary, sDim.Render("usually ")+fg(cSteel).Render(dur(usual)))
	}
	if len(summary) > 0 {
		p.add(strings.Join(summary, "   "))
	}
	if lastFail != nil {
		line := fg(cRed).Render(KFail.Glyph(a.frame)) + sDim.Render(" last failed ") + sText.Render(lastFail.label)
		if !lastFail.at.IsZero() {
			line += " " + fg(cSteel).Render(age(lastFail.at, time.Now())+" ago")
		}
		p.add(line)
	}
}

// repoStats counts what the repos in the list are doing.
type repoStats struct {
	broken, waiting int
}

func (v *reposView) stats(a *App) repoStats {
	var s repoStats
	for _, r := range v.listed {
		if len(healthKinds(r.Health)) > 0 {
			if k, _ := a.mainNow(r); k == KFail {
				s.broken++
			}
		}
		for _, run := range a.st.status[r.FullName] {
			if run.Status == "waiting" {
				s.waiting++
				break
			}
		}
	}
	return s
}

// panelMinWidth is how wide the screen must be for the detail panel.
const panelMinWidth = 110

// Render draws the title with what the repos are doing, the filter line, and
// the list; on a wide terminal a panel at the right describes the repo
// under the cursor.
func (v *reposView) Render(a *App, w, h int) string {
	scope := fmt.Sprintf("%d with Actions", v.t.Len())
	if v.showAll {
		scope = fmt.Sprintf("%d, all", v.t.Len())
	}
	if owner := a.owner(); owner != "" {
		scope = owner + " · " + scope
	}
	if a.st.reposState.busy && a.st.refreshing > 0 {
		scope += fmt.Sprintf(" · refreshing %d repos…", a.st.refreshing)
	} else if a.st.reposState.busy {
		scope += fmt.Sprintf(" · reading your repos… %d so far", len(a.st.sweeping))
	}
	st := v.stats(a)
	var chips []string
	if st.broken > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failing on %s", st.broken, commonBranch(v.listed))))
	}
	if st.waiting > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d awaiting approval", st.waiting)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("repo", "Repos", scope, -1, chips, w),
			" " + chip("s", "sort: "+v.orderLabel()+" ▾", v.order != repoOrders[0].key) + "   " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

// panel describes the repo under the cursor: what it is, how it is doing,
// what runs now and what waits for you, and its open pull requests.
func (v *reposView) panel(a *App, w, h int) []string {
	name := v.selectedRepo()
	r, ok := a.repoByName(name)
	if name == "" || !ok {
		return emptyPanel(w, h, "No repository selected.")
	}
	p := newPanel(w)
	p.add(ico("repo") + sWhite.Bold(true).Render(r.FullName))
	meta := []string{fact("branch", r.DefaultBranch)}
	if r.Private {
		meta = append(meta, icoIn("lock", cSteel)+sText.Render("private"))
	}
	if r.Fork {
		meta = append(meta, sText.Render("fork"))
	}
	if r.Archived {
		meta = append(meta, sDim.Render("archived"))
	}
	meta = append(meta, fact("clock", "pushed "+age(r.PushedAt, time.Now())+" ago"))
	p.add(strings.Join(meta, sDim.Render("  ·  ")))
	if r.HasActions && !r.Archived {
		p.field("Read", a.readWords(r.FullName, time.Now()))
	}
	if chip := a.alertChip(r.FullName); chip != "" {
		p.add(chip + sDim.Render("  ") + keycap("S"))
	}

	if kinds := healthKinds(r.Health); len(kinds) > 0 {
		k, label := a.mainNow(r)
		p.label("Default branch")
		p.tone(k.Color())
		p.add(statusChip(k, a.frame, label))
		a.workflowsNow(p, r.FullName)
		var points []historyPoint
		for i, k := range kinds {
			n := len(kinds) - 1 - i
			points = append(points, historyPoint{kind: k, label: fmt.Sprintf("%d %s ago", n, plural("commit", n))})
		}
		points[len(points)-1].label = "the latest commit"
		historySection(a, p, fmt.Sprintf("Last %d %s", len(kinds), plural("commit", len(kinds))), points, 0)
	}

	branch := a.defaultBranch(r.FullName)
	var live, done []gh.Run
	for _, run := range a.st.status[r.FullName] {
		if run.Active() {
			if run.HeadBranch != branch {
				live = append(live, run)
			}
		} else if len(done) < 3 {
			done = append(done, run)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		if !live[i].CreatedAt.Equal(live[j].CreatedAt) {
			return live[i].CreatedAt.After(live[j].CreatedAt)
		}
		return live[i].ID > live[j].ID
	})
	now := time.Now()
	runLine := func(run gh.Run, right string) {
		k := StatusKind(run.Status, run.Conclusion)
		p.row(glyph(k, a.frame)+" "+sText.Render(run.DisplayTitle), right)
	}
	if len(live) > 0 {
		p.label(fmt.Sprintf("Other branches (%d running)", len(live)))
		for _, run := range live[:min(len(live), 4)] {
			k := StatusKind(run.Status, run.Conclusion)
			right := dur(run.Duration(now))
			if run.Status == "waiting" {
				right = "approve"
			}
			p.row(glyph(k, a.frame)+" "+sText.Render(run.DisplayTitle)+" "+sDim.Render(run.HeadBranch), fg(k.Color()).Render(right))
		}
		if more := len(live) - 4; more > 0 {
			p.add(sDim.Render(fmt.Sprintf("+%d more", more)))
		}
	}
	if len(done) > 0 {
		p.label("Recent runs")
		for _, run := range done {
			runLine(run, sDim.Render(age(run.CreatedAt, now)))
		}
	}
	a.ownersSection(p, r)
	a.sharedSection(p, r.FullName, now)
	var prs []gh.PullRequest
	for _, pr := range a.st.prs {
		if strings.EqualFold(pr.Repo, r.FullName) {
			prs = append(prs, pr)
		}
	}
	if len(prs) > 0 {
		p.label(fmt.Sprintf("Pull requests (%d)", len(prs)))
		for _, pr := range prs[:min(len(prs), 4)] {
			p.row(fact("pr", fmt.Sprintf("#%d ", pr.Number)+pr.Title), "")
		}
	}
	return p.render(h)
}

// workflowsNow lists each workflow on repo's default branch with what it is
// doing now: its running or waiting run, otherwise how its latest run went.
// The order is by workflow name, so a row stays where it is as runs change.
func (a *App) workflowsNow(p *panelText, repo string) {
	now := time.Now()
	for _, run := range a.workflowsOnDefault(repo) {
		k := StatusKind(run.Status, run.Conclusion)
		var right string
		switch {
		case run.Status == "waiting":
			right = fg(k.Color()).Render("approve")
		case run.Active():
			right = fg(k.Color()).Render(dur(run.Duration(now)))
		case k == KFail:
			right = sRed.Render("failed ") + fg(cSteel).Render(age(run.CreatedAt, now))
		default:
			right = sDim.Render("passed " + age(run.CreatedAt, now))
		}
		p.row(glyph(k, a.frame)+" "+sWhite.Render(run.Name), right)
	}
}

// sharedSection lists the shared workflows repo uses, at which version, and
// how its last call of each went.
func (a *App) sharedSection(p *panelText, repo string, now time.Time) {
	shared := sharedUsedBy(a, repo)
	if len(shared) == 0 {
		return
	}
	p.label("Shared workflows")
	for _, s := range shared[:min(len(shared), 5)] {
		ver, _ := s.versionOf(repo)
		left := sWhite.Render(s.name()) + " " + versionText(ver.version)
		if ver.behind {
			left += " " + sWait.Render("behind "+shortVersion(ver.ahead))
		}
		right := sDim.Render("not run lately")
		if c, ok := lastCall(s.calls, repo); ok {
			right = glyph(StatusKind(c.run.Status, c.run.Conclusion), a.frame) + " " + fg(cSteel).Render(age(c.run.CreatedAt, now))
		}
		p.row(left, right)
	}
	p.add(keycap("w") + sDim.Render(" shows them in Shared"))
}
