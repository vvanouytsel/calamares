package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// prReady says a pull request can be merged now: approved (or no review
// needed), every check passed, and not a draft.
func prReady(pr gh.PullRequest) bool {
	if pr.Draft || pr.Checks.Failed > 0 || pr.Checks.Pending > 0 || pr.ReviewDecision == "CHANGES_REQUESTED" || pr.ReviewDecision == "REVIEW_REQUIRED" {
		return false
	}
	return pr.ReviewDecision == "APPROVED"
}

type prStats struct{ review, failing, ready, drafts int }

func (v *pullsView) stats(a *App) prStats {
	var s prStats
	for _, pr := range v.list(a) {
		if a.prWaits(pr) {
			s.review++
		}
		if pr.Checks.Failed > 0 {
			s.failing++
		}
		if prReady(pr) {
			s.ready++
		}
		if pr.Draft {
			s.drafts++
		}
	}
	return s
}

// nextStepColor is the colour of the callout that holds a pull request's next step.
func nextStepColor(pr gh.PullRequest) lipgloss.Color {
	switch {
	case pr.Draft:
		return cDim
	case prReady(pr):
		return cGreen
	case pr.Checks.Failed > 0, pr.ReviewDecision == "CHANGES_REQUESTED":
		return cRed
	case pr.Why == gh.WhyReview && pr.ReviewDecision != "APPROVED", pr.ReviewDecision == "REVIEW_REQUIRED":
		return cWait
	case pr.Checks.Pending > 0:
		return cAmber
	}
	return cSteel
}

// nextStep says in a few words what the pull request wants from you.
func nextStep(pr gh.PullRequest) string {
	switch {
	case pr.Draft:
		return sDim.Render("Draft: not ready for review")
	case prReady(pr):
		return sGreen.Bold(true).Render("Ready to merge") + sDim.Render(" · press ") + keycap("M")
	case pr.Checks.Failed > 0:
		return sRed.Bold(true).Render(fmt.Sprintf("Checks failing: %d of %d", pr.Checks.Failed, pr.Checks.Total))
	case pr.Why == gh.WhyReview && pr.ReviewDecision != "APPROVED":
		return sWait.Bold(true).Render("Your review is requested")
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return sRed.Bold(true).Render("Changes were requested")
	case pr.Checks.Pending > 0:
		return sAmber.Bold(true).Render(fmt.Sprintf("Checks running: %d", pr.Checks.Pending))
	case pr.ReviewDecision == "REVIEW_REQUIRED":
		return sWait.Bold(true).Render("Waiting for a review")
	}
	return sText.Render("Nothing to do")
}

// panel describes the pull request under the cursor: what it is, what it
// wants from you, how its checks went and the runs on its branch.
func (v *pullsView) panel(a *App, w, h int) []string {
	pr, ok := v.selected(a)
	if !ok {
		return emptyPanel(w, h, "No pull request selected.")
	}
	now := time.Now()
	p := newPanel(w)
	p.add(prIcon("OPEN", pr.Draft) + a.repoLabel(pr.Repo) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", pr.Number)))
	for i, l := range strings.Split(ansi.Wordwrap(pr.Title, max(w-3, 10), ""), "\n") {
		if i == 4 {
			break
		}
		p.add(sWhite.Bold(true).Render(l))
	}
	p.add(fact("branch", pr.Branch) + sDim.Render(" → ") + sText.Render(pr.Base))
	p.add(fact("user", pr.Author) + "   " + fact("clock", age(pr.UpdatedAt, now)))
	p.label("Next step")
	step := []string{nextStep(pr)}
	if pr.Why == gh.WhyReview {
		if a.prAside(pr) {
			step = []string{sDim.Render("Set aside until the pull request changes"), keycap("a") + sDim.Render(" bring back")}
		} else {
			step = append(step, keycap("A")+sDim.Render(" approve  ")+keycap("a")+sDim.Render(" set aside"))
		}
	}
	p.callout(nextStepColor(pr), step...)

	p.label("Checks")
	c := pr.Checks
	if c.Total == 0 {
		p.add(sDim.Render("No checks on this commit."))
	} else {
		p.add(checkMeter(c.Passed, c.Failed, c.Pending, p.inner()))
		var parts []string
		if c.Passed > 0 {
			parts = append(parts, statusChip(KOK, a.frame, fmt.Sprintf("%d passed", c.Passed)))
		}
		if c.Failed > 0 {
			parts = append(parts, statusChip(KFail, a.frame, fmt.Sprintf("%d failed", c.Failed)))
		}
		if c.Pending > 0 {
			parts = append(parts, statusChip(KRun, a.frame, fmt.Sprintf("%d running", c.Pending)))
		}
		p.chips(parts)
	}

	var runs []gh.Run
	for _, r := range a.st.status[pr.Repo] {
		if r.HeadBranch == pr.Branch && len(runs) < 4 {
			runs = append(runs, r)
		}
	}
	if len(runs) > 0 {
		p.label("Checks on this branch")
		for _, r := range runs {
			k := StatusKind(r.Status, r.Conclusion)
			p.row(glyph(k, a.frame)+" "+sText.Render(fmt.Sprintf("%s #%d", r.Name, r.RunNumber)), fact("clock", age(r.CreatedAt, now)))
		}
	}
	p.label("Review")
	rv := reviewCell(pr)
	p.add(strings.TrimSpace(rv.Styled))
	return p.render(h)
}
