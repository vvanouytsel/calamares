package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runnerHistoryFor is how long a job on a runner that is gone is remembered.
const runnerHistoryFor = 24 * time.Hour

const snapshotRunnerHistory = "runner-history"

// pastJob is a job that ran on a runner that has no row of its own any more,
// with only what the history shows of its run and itself.
type pastJob struct {
	Repo string
	Run  gh.Run
	Job  gh.Job
}

func (p pastJob) use() runnerUse { return runnerUse{p.Repo, p.Run, p.Job} }

// slim keeps what the history shows of a run and a job, so a day of jobs
// stays small in the cache on disk.
func slim(repo string, run gh.Run, job gh.Job) pastJob {
	return pastJob{Repo: repo,
		Run: gh.Run{ID: run.ID, Name: run.Name, DisplayTitle: run.DisplayTitle, RunNumber: run.RunNumber, RunAttempt: run.RunAttempt, Event: run.Event,
			Status: run.Status, Conclusion: run.Conclusion, HeadBranch: run.HeadBranch, HeadSHA: run.HeadSHA, WorkflowID: run.WorkflowID,
			HTMLURL: run.HTMLURL, Path: run.Path, Actor: run.Actor, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt},
		Job: gh.Job{ID: job.ID, RunID: job.RunID, Name: job.Name, Status: job.Status, Conclusion: job.Conclusion, HTMLURL: job.HTMLURL,
			CreatedAt: job.CreatedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt, RunnerName: job.RunnerName, Labels: job.Labels}}
}

// keepPast adds the finished jobs on runners without a row of their own to
// the history, drops what is older than a day, and saves it when it changed.
func (a *App) keepPast(uses []runnerUse, now time.Time) {
	h := a.st.runners.history
	changed := false
	for _, u := range uses {
		if _, ok := h[u.job.ID]; !ok {
			h[u.job.ID] = slim(u.repo, u.run, u.job)
			changed = true
		}
	}
	for id, p := range h {
		if now.Sub(p.use().at()) > runnerHistoryFor {
			delete(h, id)
			changed = true
		}
	}
	if changed {
		a.client.Remember(snapshotRunnerHistory, h)
	}
}

// pastJobs is the history, newest first.
func (a *App) pastJobs() []runnerUse {
	out := make([]runnerUse, 0, len(a.st.runners.history))
	for _, p := range a.st.runners.history {
		out = append(out, p.use())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at().After(out[j].at()) })
	return out
}

// clearHistory asks, then forgets the jobs of the runners that are gone.
func (a *App) clearHistory() tea.Cmd {
	n := len(a.st.runners.history)
	if n == 0 {
		return a.flashCmd(nHint, "The runner history is empty.")
	}
	return confirm("Clear the runner history?", []string{
		sText.Render(fmt.Sprintf("Calamares forgets which runner ran each of %d %s.", n, plural("job", n))),
		sDim.Render("Nothing changes on GitHub."),
	}, "Clear", false, func() tea.Cmd {
		a.st.runners.history = map[int64]pastJob{}
		a.client.Remember(snapshotRunnerHistory, a.st.runners.history)
		return a.flashCmd(nDone, "Cleared the runner history")
	})
}

const historyKey = "history"

// historyRow is the folded line of the jobs on runners that are gone.
func (v *runnersView) historyRow(a *App, past []runnerUse, open bool, now time.Time) Row {
	fold := "▸"
	if open {
		fold = "▾"
	}
	repos := map[string]bool{}
	for _, u := range past {
		repos[strings.ToLower(u.repo)] = true
	}
	what := fmt.Sprintf("%d %s", len(past), plural("job", len(past)))
	about := fmt.Sprintf("last %s ago · %s · the last 24h", age(past[0].at(), now), repoCount(len(repos)))
	return Row{Key: historyKey, Kind: KSkip, Cells: []Cell{
		{Text: fold + " ended runners", Styled: sDim.Render(fold + " ended runners")},
		{Text: what, Styled: sDim.Render(what)},
		{Text: about, Styled: sDim.Render(about)},
		{}, {},
	}}
}

func (v *runnersView) pastRow(a *App, i int, u runnerUse, last bool, now time.Time) Row {
	guide := "├─ "
	if last {
		guide = "└─ "
	}
	k := jobKind(u.job)
	where := a.short(u.repo) + " › " + u.run.Name + fmt.Sprintf(" #%d", u.run.RunNumber) + " › " + u.job.Name
	return Row{Key: fmt.Sprintf("past\x00%d", i), Kind: KSkip, Cells: []Cell{
		{Text: "  " + guide + u.job.RunnerName, Styled: "  " + sDim.Render(guide) + sText.Render(u.job.RunnerName)},
		statusCell(k, 0, ""),
		{Text: where, Styled: sText.Render(where)},
		steelCell(strings.Join(u.job.Labels, ", "), true),
		steelCell(age(u.at(), now), false),
	}}
}

func (v *runnersView) historyPanel(a *App, p *panelText, now time.Time) {
	p.add(icoIn("history", cSteel) + sWhite.Bold(true).Render("Ended runners"))
	p.add(sDim.Render(fmt.Sprintf("%d jobs of the last 24h on runners that are gone, as far as the runs we held show them.", len(v.past))))
	count := map[string]int{}
	var repos []string
	for _, u := range v.past {
		if count[u.repo] == 0 {
			repos = append(repos, u.repo)
		}
		count[u.repo]++
	}
	sort.SliceStable(repos, func(i, j int) bool { return count[repos[i]] > count[repos[j]] })
	p.label("Jobs per repository")
	for i, repo := range repos {
		if i == 8 {
			p.add(sDim.Render(fmt.Sprintf("… and %d more", len(repos)-i)))
			break
		}
		p.row(ico("repo")+sText.Render(a.short(repo)), sDim.Render(fmt.Sprint(count[repo])))
	}
	p.add("")
	p.add(keycap("space") + sText.Render(" the jobs  ") + keycap("/") + sText.Render(" find a runner  ") + keycap("X") + sText.Render(" clear"))
}

func (v *runnersView) pastPanel(a *App, p *panelText, u runnerUse, now time.Time) {
	k := jobKind(u.job)
	p.add(icoIn("server", cSteel) + sWhite.Bold(true).Render(u.job.RunnerName))
	p.add(sDim.Render("gone · ran one job we saw"))
	p.label("Job")
	p.add(ico("repo") + sText.Render(a.short(u.repo)+" › "+u.run.Name) + sGold.Render(fmt.Sprintf(" #%d", u.run.RunNumber)) + sText.Render(" › "+u.job.Name))
	p.add(statusChip(k, 0, "") + sDim.Render(" · ran ") + sText.Render(dur(u.job.Duration(now))) + sDim.Render(" · ended ") + sText.Render(age(u.at(), now)+" ago"))
	if q := u.job.Queued(now); q > 0 {
		p.add(sDim.Render("queued ") + queueText(q))
	}
	p.add(fact("branch", u.run.HeadBranch) + "  " + fact("user", u.run.Actor.Login))
	p.label("Labels")
	p.add(sText.Render(strings.Join(u.job.Labels, ", ")))
}
