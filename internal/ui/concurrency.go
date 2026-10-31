package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// concurrencyGroup finds the group GitHub names when it cancels a run or a
// job for a newer one: "Canceling since a higher priority waiting request
// for 'deploy-main' exists".
var concurrencyGroup = regexp.MustCompile(`(?i)(?:higher priority waiting request for|concurrency group) '([^']+)'`)

// concurrencyNote is why a run was cancelled or is held by a concurrency
// group, and the run that took its place.
type concurrencyNote struct {
	group      string
	replacedBy *gh.Run
	heldBy     *gh.Run
}

type concurrencyMsg struct {
	id   int
	note concurrencyNote
}

func (m concurrencyMsg) viewID() int { return m.id }

// groupIn is the concurrency group named in a job's annotations, or "".
func groupIn(anns []gh.Annotation) string {
	for _, an := range anns {
		if m := concurrencyGroup.FindStringSubmatch(an.Message); m != nil {
			return m[1]
		}
	}
	return ""
}

// readConcurrency asks why a cancelled run was cancelled, and which run of
// its workflow took its place; for a pending run, which one it waits for.
func readConcurrency(a *App, repo string, run gh.Run, jobs []gh.Job) concurrencyNote {
	ctx := gh.Background(context.Background())
	var note concurrencyNote
	if run.Conclusion == "cancelled" {
		for i, j := range jobs {
			if i == 5 {
				break
			}
			if anns, err := a.client.Annotations(ctx, repo, j.ID); err == nil {
				if g := groupIn(anns); g != "" {
					note.group = g
					break
				}
			}
		}
		if note.group == "" {
			return note
		}
	}
	runs, _, err := a.client.ListRuns(ctx, repo, gh.RunFilter{WorkflowID: run.WorkflowID, PerPage: 20})
	if err != nil {
		return note
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	for i, r := range runs {
		switch {
		case run.Conclusion == "cancelled" && r.CreatedAt.After(run.CreatedAt) && note.replacedBy == nil:
			note.replacedBy = &runs[i]
		case run.Status == "pending" && r.Active() && r.Status != "pending" && r.CreatedAt.Before(run.CreatedAt):
			note.heldBy = &runs[i]
		}
	}
	return note
}

// loadConcurrency reads the note once, when the run was cancelled or is pending.
func (v *runView) loadConcurrency(a *App) tea.Cmd {
	if v.concurrencyAsked || !v.jobsLoaded || (v.run.Conclusion != "cancelled" && v.run.Status != "pending") {
		return nil
	}
	v.concurrencyAsked = true
	id, repo, run, jobs := v.id, v.repo, v.run, v.jobs
	return func() tea.Msg { return concurrencyMsg{id, readConcurrency(a, repo, run, jobs)} }
}

// concurrencyBox says, above a cancelled or held run's details, which
// concurrency group did it and which run took its place.
func (v *runView) concurrencyBox(w int) []string {
	n := v.concurrency
	var lines []string
	switch {
	case n.group != "" && n.replacedBy != nil:
		lines = append(lines, sText.Render("Canceled for a newer run of concurrency group ")+sWhite.Bold(true).Render(n.group)+sText.Render(": ")+
			sGold.Render(fmt.Sprintf("#%d", n.replacedBy.RunNumber))+sDim.Render(" · ")+keycap(".")+sText.Render(" open it"))
	case n.group != "":
		lines = append(lines, sText.Render("Canceled for a newer run of concurrency group ")+sWhite.Bold(true).Render(n.group))
	case n.heldBy != nil:
		lines = append(lines, sText.Render("Waits for ")+sGold.Render(fmt.Sprintf("#%d", n.heldBy.RunNumber))+sText.Render(" of this workflow to finish: they share a concurrency group")+
			sDim.Render(" · ")+keycap(".")+sText.Render(" open it"))
	default:
		return nil
	}
	var body []string
	for _, l := range lines {
		for _, part := range wrapLine(l, w-3) {
			body = append(body, " "+part)
		}
	}
	return strings.Split(boxLeft(sWait.Bold(true).Render("⊘ Concurrency"), strings.Join(body, "\n"), w, len(body)+2, cWait), "\n")
}

// concurrencyRun is the run that took this one's place or that it waits for.
func (v *runView) concurrencyRun() *gh.Run {
	if v.concurrency.replacedBy != nil {
		return v.concurrency.replacedBy
	}
	return v.concurrency.heldBy
}
