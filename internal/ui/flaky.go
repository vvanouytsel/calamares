package ui

import (
	"context"
	"fmt"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// maxFlakyReads caps how many reruns one look at a repo reads the earlier
// attempt of; each attempt is read once, as it never changes.
const maxFlakyReads = 10

type flakyMsg struct {
	id    int
	runID int64
	jobs  []string // the jobs that failed in the attempt before the one that passed
	err   error
}

func (m flakyMsg) viewID() int { return m.id }

// rerunToPass says the run passed, but only on a later attempt.
func rerunToPass(r gh.Run) bool {
	return r.RunAttempt > 1 && StatusKind(r.Status, r.Conclusion) == KOK
}

// loadFlaky reads, for the runs that passed on a rerun, which jobs failed in
// the attempt before.
func (v *repoView) loadFlaky(a *App) tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range v.sampleRuns() {
		if len(cmds) == maxFlakyReads {
			break
		}
		if !rerunToPass(r) || v.flakyAsked[r.ID] {
			continue
		}
		v.flakyAsked[r.ID] = true
		id, repo, run := v.id, v.repo, r
		cmds = append(cmds, func() tea.Msg {
			jobs, err := a.client.ListAttemptJobs(gh.Background(context.Background()), repo, run.ID, run.RunAttempt-1)
			var failed []string
			for _, j := range jobs {
				if jobKind(j) == KFail {
					failed = append(failed, run.Name+" › "+j.Name)
				}
			}
			return flakyMsg{id, run.ID, failed, err}
		})
	}
	return tea.Batch(cmds...)
}

// flakyJob is a job that failed and then passed on a rerun of the same commit.
type flakyJob struct {
	name  string
	times int
}

// flakes counts the runs of runs that only passed on a rerun, and the jobs
// that failed before they did, most often first.
func flakes(runs []gh.Run, failedBefore map[int64][]string) (reruns, finished int, jobs []flakyJob) {
	count := map[string]int{}
	for _, r := range runs {
		k := StatusKind(r.Status, r.Conclusion)
		if k != KOK && k != KFail {
			continue
		}
		finished++
		if !rerunToPass(r) {
			continue
		}
		reruns++
		for _, j := range failedBefore[r.ID] {
			count[j]++
		}
	}
	for name, n := range count {
		jobs = append(jobs, flakyJob{name, n})
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].times != jobs[j].times {
			return jobs[i].times > jobs[j].times
		}
		return jobs[i].name < jobs[j].name
	})
	return reruns, finished, jobs
}

// flakyLines are the lines of the Health panel about runs that needed a rerun.
func (v *repoView) flakyLines(runs []gh.Run, inner int) []string {
	reruns, finished, jobs := flakes(runs, v.flaky)
	if reruns == 0 {
		return nil
	}
	pct := reruns * 100 / finished
	lines := []string{"", fit(" "+icoIn("flaky", cWait)+sWait.Render(fmt.Sprintf("%d%% passed on a rerun", pct)), inner-1) + " "}
	for i, j := range jobs {
		if i == 3 {
			break
		}
		times := fmt.Sprintf("%d×", j.times)
		lines = append(lines, fit("   "+sText.Render(j.name), inner-width(times)-2)+" "+sWait.Render(times)+" ")
	}
	return lines
}
