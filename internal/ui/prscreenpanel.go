package ui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"slices"
	"strings"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// panel is the side of the pull request screen: where it can be merged, its
// checks and reviews at a glance, the CI runs on its branch and the files
// that changed the most.
func (v *prView) panel(a *App, w, h int) []string {
	pr := v.pr
	now := time.Now()
	p := newPanel(w)
	v.mergeBox(a, p)

	p.label(fmt.Sprintf("Checks (%d)", len(pr.Checks)))
	if len(pr.Checks) == 0 {
		p.add(sDim.Render("No checks on this commit."))
	}
	for _, c := range pr.Checks[:min(len(pr.Checks), 8)] {
		k := KOK
		switch c.Kind {
		case "failed":
			k = KFail
		case "pending":
			k = KRun
		}
		right := ""
		if c.Workflow != "" {
			right = sDim.Render(c.Workflow)
		}
		p.row(chipGlyph(k, a.frame)+" "+sText.Render(c.Name), right)
	}
	if n := len(pr.Checks) - 8; n > 0 {
		p.add(sDim.Render(fmt.Sprintf("… and %d more", n)))
	}
	v.missingBox(p)

	v.reviewersBox(a, p)

	var runs []string
	for _, r := range a.st.status[v.repo] {
		if r.HeadBranch == pr.Branch && len(runs) < 3 {
			k := StatusKind(r.Status, r.Conclusion)
			runs = append(runs, fmt.Sprintf("%s|%s", chipGlyph(k, a.frame)+" "+sText.Render(r.Name)+" "+sGold.Render(fmt.Sprintf("#%d", r.RunNumber)), fact("clock", age(r.CreatedAt, now))))
		}
	}
	if len(runs) > 0 {
		p.label("Checks on this branch")
		for _, l := range runs {
			left, right, _ := strings.Cut(l, "|")
			p.row(left, right)
		}
	}

	if len(pr.ChangedFiles) > 0 {
		p.label("Changes  " + diffStat(pr.Additions, pr.Deletions))
		files := slices.Clone(pr.ChangedFiles)
		slices.SortFunc(files, func(x, y gh.FileChange) int { return (y.Additions + y.Deletions) - (x.Additions + x.Deletions) })
		for _, f := range files[:min(len(files), 4)] {
			p.row(ico("file")+sText.Render(midCut(f.Path, max(w-16, 10))), sGreen.Render(fmt.Sprintf("+%d", f.Additions))+" "+sRed.Render(fmt.Sprintf("−%d", f.Deletions)))
		}
	}
	return p.render(h)
}

// missingBox names the required checks that haven't reported: while checks
// still run they may yet; once none run, nothing will report them.
func (v *prView) missingBox(p *panelText) {
	missing := v.missingChecks()
	if len(missing) == 0 {
		return
	}
	running := false
	for _, c := range v.pr.Checks {
		running = running || c.Kind == "pending"
	}
	if running {
		for _, name := range missing {
			p.row(chipGlyph(KQueue, 0)+" "+sDim.Render(name), sDim.Render("required · not yet"))
		}
		return
	}
	p.add("")
	lines := []string{sRed.Bold(true).Render("Required, but nothing reports it")}
	for _, name := range missing {
		lines = append(lines, sWhite.Render(name))
	}
	lines = append(lines, sText.Render("No run of this pull request has a check by that name: a job renamed while "+v.pr.Base+" still requires the old one?"))
	p.callout(cRed, lines...)
}

// mergeStep is one condition between a pull request and its merge.
type mergeStep struct {
	state string // done, fail, wait, run
	text  string
	key   string // the key that fixes it, when you can use it
	lock  string // why you can't, when you can't
}

// mergeSteps lists what stands between the pull request and a merge, in
// the order GitHub checks it: conflicts, checks, reviews, the base, draft.
func (v *prView) mergeSteps(a *App) []mergeStep {
	pr := v.pr
	var steps []mergeStep
	switch {
	case pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY":
		steps = append(steps, mergeStep{state: "fail", text: "merge conflicts"})
	case pr.Mergeable == "UNKNOWN" && pr.MergeState == "UNKNOWN":
		steps = append(steps, mergeStep{state: "run", text: "checking for conflicts"})
	default:
		steps = append(steps, mergeStep{state: "done", text: "no conflicts"})
	}
	failed, pending := 0, 0
	for _, c := range pr.Checks {
		switch c.Kind {
		case "failed":
			failed++
		case "pending":
			pending++
		}
	}
	missing := v.missingChecks()
	switch {
	case len(missing) > 0 && pending == 0:
		steps = append(steps, mergeStep{state: "fail", text: fmt.Sprintf("checks  %d required never reported", len(missing))})
	case failed > 0:
		steps = append(steps, mergeStep{state: "fail", text: fmt.Sprintf("checks  %d of %d failing", failed, len(pr.Checks))})
	case pending > 0:
		steps = append(steps, mergeStep{state: "run", text: fmt.Sprintf("checks  %d of %d running", pending, len(pr.Checks))})
	case len(pr.Checks) == 0:
		steps = append(steps, mergeStep{state: "done", text: "no checks"})
	default:
		steps = append(steps, mergeStep{state: "done", text: fmt.Sprintf("checks  %d passed", len(pr.Checks))})
	}
	switch pr.ReviewDecision {
	case "APPROVED":
		steps = append(steps, mergeStep{state: "done", text: "review  approved"})
	case "CHANGES_REQUESTED":
		steps = append(steps, mergeStep{state: "fail", text: "review  changes requested"})
	case "REVIEW_REQUIRED":
		text := "review  none yet"
		if len(pr.Requested) > 0 || len(pr.Reviews) > 0 {
			text = "review  needed"
		}
		steps = append(steps, mergeStep{state: "wait", text: text, key: "@", lock: v.keyLock(a, "@")})
	default:
		steps = append(steps, mergeStep{state: "done", text: "review  none required"})
	}
	if pr.MergeState == "BEHIND" {
		text := "behind " + pr.Base
		if v.behind > 0 {
			text = fmt.Sprintf("%d behind %s", v.behind, pr.Base)
		}
		steps = append(steps, mergeStep{state: "wait", text: text, key: "B", lock: v.keyLock(a, "B")})
	} else {
		steps = append(steps, mergeStep{state: "done", text: "up to date with " + pr.Base})
	}
	if pr.Draft {
		steps = append(steps, mergeStep{state: "wait", text: "still a draft", key: "D", lock: v.keyLock(a, "D")})
	}
	return steps
}

// keyLock is why the key on the pull request screen is locked, or "".
func (v *prView) keyLock(a *App, key string) string {
	for _, b := range v.actionKeys(a) {
		if slices.Contains(b.Keys, key) {
			if d, locked := a.lockOf(b); locked {
				return d.Reason
			}
		}
	}
	return ""
}

// mergeBox is the top of the panel: what still stands between the pull
// request and a merge, each item with its state and the key that fixes it.
func (v *prView) mergeBox(a *App, p *panelText) {
	pr := v.pr
	switch pr.State {
	case "MERGED":
		p.callout(cViolet, icoIn("merge", cViolet)+lipgloss.NewStyle().Foreground(cViolet).Bold(true).Render("Merged"))
		return
	case "CLOSED":
		p.callout(cRed, sRed.Bold(true).Render("Closed")+sDim.Render(" · not merged"))
		return
	}
	steps := v.mergeSteps(a)
	done := 0
	for _, s := range steps {
		if s.state == "done" {
			done++
		}
	}
	merge := keycap("M") + sDim.Render(" merge")
	if done == len(steps) {
		p.callout(cGreen, sGreen.Bold(true).Render(icon("merge")+"Ready to merge")+"   "+merge)
	} else {
		p.row(sDim.Render("To merge")+"  "+stepMeter(steps), sDim.Render(fmt.Sprintf("%d of %d", done, len(steps))))
	}
	for _, s := range steps {
		k := KOK
		switch s.state {
		case "fail":
			k = KFail
		case "wait":
			k = KWait
		case "run":
			k = KRun
		}
		right := ""
		switch {
		case s.lock != "":
			right = sDim.Render(strings.TrimSpace(icon("lock")))
		case s.key != "":
			right = sDim.Render(s.key)
		}
		p.row(chipGlyph(k, a.frame)+" "+sText.Render(s.text), right)
	}
	if pr.AutoMerge != nil {
		text := "auto-merge on · " + pr.AutoMerge.Method
		if pr.AutoMerge.By != "" {
			text += " · by " + pr.AutoMerge.By
		}
		p.row(sCyan.Render("⟳")+" "+sText.Render(text), sDim.Render("A"))
	} else if v.keyLock(a, "A") == "" && done < len(steps) {
		p.row(sDim.Render("A  auto-merge when ready"), "")
	}
	if len(pr.Labels) > 0 || len(pr.Assignees) > 0 {
		p.label("Labels")
		var chips []string
		for _, l := range pr.Labels {
			chips = append(chips, labelChip(l.Name, l.Color))
		}
		p.chips(chips)
		if len(pr.Assignees) > 0 {
			var people []string
			for _, who := range pr.Assignees {
				people = append(people, fact("user", who))
			}
			p.chips(append([]string{sDim.Render("assigned")}, people...))
		}
	}
	if len(pr.Closes) > 0 {
		p.label("Closing issues")
		for _, c := range pr.Closes {
			p.add(icoIn("issue", cGreen) + sGold.Render(fmt.Sprintf("#%d", c.Number)) + " " + sText.Render(c.Title))
		}
	}
}

// chipGlyph is a status glyph on a tint of its colour, a small square chip.
func chipGlyph(k Kind, frame int) string {
	bg, ok := tint[k]
	if !ok {
		return " " + glyph(k, frame)
	}
	if k == KRun {
		bg = breathe(bg)
	}
	return lipgloss.NewStyle().Foreground(k.Color()).Background(bg).Render(k.Glyph(frame) + " ")
}

// stepMeter is one block per step to a merge, in the colour of where it stands.
func stepMeter(steps []mergeStep) string {
	var b strings.Builder
	for _, s := range steps {
		c := cGreen
		switch s.state {
		case "fail":
			c = cRed
		case "wait":
			c = cWait
		case "run":
			c = cAmber
		}
		g := "■"
		if ascii {
			g = "#"
		}
		b.WriteString(fg(c).Render(g))
	}
	return b.String()
}

// reviewersBox lists who was asked and who reviewed, each with their state.
func (v *prView) reviewersBox(a *App, p *panelText) {
	pr := v.pr
	p.label("Reviewers")
	if len(pr.Reviews) == 0 && len(pr.Requested) == 0 {
		p.add(sDim.Render("none yet"))
	}
	for _, r := range pr.Reviews {
		state := strings.ToLower(strings.ReplaceAll(r.State, "_", " "))
		right := sText.Render(state)
		switch r.State {
		case "APPROVED":
			right = statusChip(KOK, 0, state)
		case "CHANGES_REQUESTED":
			right = statusChip(KFail, 0, "changes")
		}
		p.row(person(r.Author), right)
	}
	for _, who := range pr.Requested {
		p.row(person(who), statusChip(KWait, 0, "asked"))
	}
	if strings.EqualFold(pr.ViewerPermission, "READ") && !pr.ViewerOwner {
		p.add(sDim.Render("your approval won't count"))
	}
}
