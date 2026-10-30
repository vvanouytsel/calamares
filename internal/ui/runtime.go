package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// What the Time view reads: the finished runs of the last two weeks of each
// repo, and how long their jobs took. A finished run's jobs never change, so
// they are read once and kept; the runs are read again once a day.
const (
	timeWeek       = 7 * 24 * time.Hour
	timeSpan       = 2 * timeWeek // this week and the week before
	timeStale      = 24 * time.Hour
	timePages      = 5    // pages of 100 runs read per repo
	maxJobTimeRuns = 3000 // runs whose jobs one read may ask for, 50 to a request
)

// runTime is a finished run with how long each of its jobs took.
type runTime struct {
	Repo       string       `json:"r"`
	ID         int64        `json:"i"`
	NodeID     string       `json:"o,omitempty"`
	WorkflowID int64        `json:"w,omitempty"`
	Path       string       `json:"p"`
	At         time.Time    `json:"a"`
	Attempt    int          `json:"t,omitempty"`
	Called     []string     `json:"c,omitempty"` // the reusable workflows it called, by sharedKey
	Jobs       []gh.JobTime `json:"j,omitempty"`
	Read       bool         `json:"d,omitempty"` // its jobs were read
}

// timeData is what the store holds for the Time view.
type timeData struct {
	runs    map[int64]*runTime
	state   loadState
	repos   int      // how many repos were read
	partial []string // repos that had more runs than were read
}

type timeSnapshot struct {
	Runs    []*runTime
	Repos   int
	Partial []string
}

const snapshotTimes = "runtimes/1"

type runTimesMsg struct {
	runs    []*runTime
	repos   int
	partial []string
	err     error
}

func (s *store) seedTimes(a *App) {
	var snap timeSnapshot
	at, ok := a.client.Recall(snapshotTimes, &snap)
	if !ok {
		return
	}
	s.times.runs = map[int64]*runTime{}
	for _, rt := range snap.Runs {
		s.times.runs[rt.ID] = rt
	}
	s.times.repos, s.times.partial, s.times.state.at = snap.Repos, snap.Partial, at
}

// loadTimes reads the runs of the last two weeks of repos and the jobs of
// the ones not known yet, once a day or when forced.
func (s *store) loadTimes(a *App, repos []string, force bool) tea.Cmd {
	l := &s.times.state
	if len(repos) == 0 || (!force && time.Since(l.at) < timeStale) || (!force && !l.waited(time.Hour)) || !l.begin() {
		return nil
	}
	known := make(map[int64]runTime, len(s.times.runs))
	for id, rt := range s.times.runs {
		known[id] = *rt
	}
	repos = append([]string(nil), repos...)
	return func() tea.Msg { return readRunTimes(a, repos, known) }
}

// readRunTimes lists the finished runs of the last two weeks of each repo,
// then reads the jobs of the runs whose jobs are not known.
func readRunTimes(a *App, repos []string, known map[int64]runTime) runTimesMsg {
	ctx := gh.Background(context.Background())
	since := time.Now().Add(-timeSpan)
	m := runTimesMsg{repos: len(repos)}
	byNode := map[string]*runTime{}
	var ids []string
	failed := 0
	for _, repo := range repos {
		runs, more, err := a.client.RunsSince(ctx, repo, since, timePages)
		if err != nil {
			failed++
			m.err = err
			for _, rt := range known {
				if strings.EqualFold(rt.Repo, repo) {
					rt := rt
					m.runs = append(m.runs, &rt)
				}
			}
			continue
		}
		if more {
			m.partial = append(m.partial, repo)
		}
		for _, r := range runs {
			if old, ok := known[r.ID]; ok && old.Read && old.Attempt == r.RunAttempt {
				m.runs = append(m.runs, &old)
				continue
			}
			rt := &runTime{Repo: repo, ID: r.ID, NodeID: r.NodeID, WorkflowID: r.WorkflowID, Path: r.Path, At: r.CreatedAt, Attempt: r.RunAttempt}
			seen := map[string]bool{}
			for _, ref := range r.ReferencedWorkflows {
				if rp, file, _ := ref.Split(); rp != "" && !seen[sharedKey(rp, file)] {
					seen[sharedKey(rp, file)] = true
					rt.Called = append(rt.Called, sharedKey(rp, file))
				}
			}
			m.runs = append(m.runs, rt)
			if r.NodeID != "" && len(ids) < maxJobTimeRuns {
				ids = append(ids, r.NodeID)
				byNode[r.NodeID] = rt
			}
		}
	}
	if failed < len(repos) {
		m.err = nil // some repos are read; the others keep what was known
	}
	times, err := a.client.RunJobTimes(ctx, ids)
	for id, jobs := range times {
		byNode[id].Jobs, byNode[id].Read = jobs, true
	}
	if err != nil && len(times) == 0 && m.err == nil {
		m.err = err
	}
	return m
}

func (s *store) gotRunTimes(a *App, m runTimesMsg) tea.Cmd {
	s.times.state.end(m.err)
	if m.err != nil && len(m.runs) == 0 {
		return a.flashCmd(nError, "Could not read the runner time: "+m.err.Error())
	}
	s.times.runs = map[int64]*runTime{}
	for _, rt := range m.runs {
		s.times.runs[rt.ID] = rt
	}
	s.times.repos, s.times.partial = m.repos, m.partial
	snap := timeSnapshot{Repos: m.repos, Partial: m.partial}
	for _, rt := range s.times.runs {
		snap.Runs = append(snap.Runs, rt)
	}
	a.client.Remember(snapshotTimes, snap)
	return nil
}

// callerJobs maps the jobs that call a reusable workflow, by repo, file and
// the job's name, to the workflow they call, from the workflow files.
func callerJobs(uses []gh.Use) map[string]string {
	out := map[string]string{}
	for _, u := range uses {
		if u.Job != "" && u.Workflow() {
			out[strings.ToLower(u.Repo)+"\x00"+u.File+"\x00"+u.Job] = sharedKey(u.TargetRepo(), u.TargetFile())
		}
	}
	return out
}

var matrixSuffix = regexp.MustCompile(`\s*\([^()]*\)$`)

// jobOwner says which workflow a job of rt ran in, by sharedKey: the
// reusable workflow its calling job calls, or the run's own workflow; and
// the job's name within that workflow.
func jobOwner(rt *runTime, job string, callers map[string]string) (owner, name string) {
	own := sharedKey(rt.Repo, rt.Path)
	caller, rest, nested := strings.Cut(job, " / ")
	if !nested {
		return own, job
	}
	base := strings.ToLower(rt.Repo) + "\x00" + rt.Path + "\x00"
	if key, ok := callers[base+caller]; ok {
		return key, rest
	}
	if key, ok := callers[base+matrixSuffix.ReplaceAllString(caller, "")]; ok {
		return key, rest
	}
	if len(rt.Called) == 1 {
		return rt.Called[0], rest
	}
	return own, job
}

// legOf is a job's name without its matrix values, so the legs of a matrix
// count as one job.
func legOf(name string) string { return matrixSuffix.ReplaceAllString(name, "") }

// timeJob is a job of a workflow and the time it took.
type timeJob struct {
	name         string
	legs         map[string]bool
	week, prior  time.Duration
	runs, before map[int64]time.Duration // its time per run, this week and the week before
}

// timeRow is a workflow, or a repo, and the runner time it took: this week
// and the week before.
type timeRow struct {
	key         string // sharedKey of the workflow, or the repo
	repo, path  string // where the workflow lives; path "" for a repo
	workflowID  int64  // of one of its runs in its own repo, to open it
	week, prior time.Duration
	runs        map[int64]time.Duration // its time per run this week
	before      map[int64]time.Duration // and the week before
	rerun       time.Duration           // this week, in runs that ran again
	days        [14]time.Duration       // per day, oldest first
	jobs        map[string]*timeJob
	users       map[string]time.Duration // the repos whose runs it worked in, this week
	children    []*timeRow               // by repo: the workflows of the repo
}

func newTimeRow(key, repo, path string) *timeRow {
	return &timeRow{key: key, repo: repo, path: path, runs: map[int64]time.Duration{}, before: map[int64]time.Duration{},
		jobs: map[string]*timeJob{}, users: map[string]time.Duration{}}
}

func (r *timeRow) add(rt *runTime, job string, took time.Duration, now time.Time) {
	thisWeek := now.Sub(rt.At) < timeWeek
	if day := int(now.Sub(rt.At) / (24 * time.Hour)); day >= 0 && day < 14 {
		r.days[13-day] += took
	}
	j, ok := r.jobs[legOf(job)]
	if !ok {
		j = &timeJob{name: legOf(job), legs: map[string]bool{}, runs: map[int64]time.Duration{}, before: map[int64]time.Duration{}}
		r.jobs[legOf(job)] = j
	}
	j.legs[job] = true
	if thisWeek {
		r.week += took
		r.runs[rt.ID] += took
		r.users[rt.Repo] += took
		j.week += took
		j.runs[rt.ID] += took
		if rt.Attempt > 1 {
			r.rerun += took
		}
		return
	}
	r.prior += took
	r.before[rt.ID] += took
	j.prior += took
	j.before[rt.ID] += took
}

// timeRows adds up the runner time of runs by the workflow each job ran in,
// or by repo; the biggest first.
func timeRows(runs map[int64]*runTime, uses []gh.Use, byRepo bool, now time.Time) []*timeRow {
	callers := callerJobs(uses)
	rows := map[string]*timeRow{}
	get := func(key, repo, path string) *timeRow {
		r, ok := rows[key]
		if !ok {
			r = newTimeRow(key, repo, path)
			rows[key] = r
		}
		return r
	}
	sub := map[string]map[string]*timeRow{}
	for _, rt := range runs {
		if now.Sub(rt.At) >= timeSpan {
			continue
		}
		for _, j := range rt.Jobs {
			owner, name := jobOwner(rt, j.Name, callers)
			repo, path := splitShared(owner, rt)
			if !byRepo {
				r := get(owner, repo, path)
				r.add(rt, name, j.Took, now)
				if strings.EqualFold(repo, rt.Repo) && r.workflowID == 0 {
					r.workflowID = rt.WorkflowID
				}
				continue
			}
			r := get(strings.ToLower(rt.Repo), rt.Repo, "")
			r.add(rt, j.Name, j.Took, now)
			if sub[r.key] == nil {
				sub[r.key] = map[string]*timeRow{}
			}
			c, ok := sub[r.key][owner]
			if !ok {
				c = newTimeRow(owner, repo, path)
				sub[r.key][owner] = c
			}
			c.add(rt, name, j.Took, now)
			if strings.EqualFold(repo, rt.Repo) && c.workflowID == 0 {
				c.workflowID = rt.WorkflowID
			}
		}
	}
	var out []*timeRow
	for _, r := range rows {
		for _, c := range sub[r.key] {
			r.children = append(r.children, c)
		}
		sortTimeRows(r.children, "hours")
		out = append(out, r)
	}
	return sortTimeRows(out, "hours")
}

// splitShared is the repo and path of a workflow's sharedKey, spelled as the
// run spells them when it is the run's own.
func splitShared(key string, rt *runTime) (repo, path string) {
	if key == sharedKey(rt.Repo, rt.Path) {
		return rt.Repo, rt.Path
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 3 {
		return key, ""
	}
	return parts[0] + "/" + parts[1], parts[2]
}

// timeOrders are the ways the Time view sorts, the first the default.
var timeOrders = []struct{ key, label string }{
	{"hours", "most hours"}, {"growth", "most growth"}, {"runs", "most runs"}, {"name", "name"},
}

func sortTimeRows(list []*timeRow, order string) []*timeRow {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch order {
		case "growth":
			if x, y := a.week-a.prior, b.week-b.prior; x != y {
				return x > y
			}
		case "runs":
			if len(a.runs) != len(b.runs) {
				return len(a.runs) > len(b.runs)
			}
		case "name":
			return a.name() < b.name()
		}
		if a.week != b.week {
			return a.week > b.week
		}
		return a.name() < b.name()
	})
	return list
}

func (r *timeRow) name() string {
	if r.path == "" {
		return r.repo
	}
	return pathBase(r.path)
}

// median is the median time it took per run this week.
func (r *timeRow) median() time.Duration { return medianOf(r.runs) }

func medianOf(m map[int64]time.Duration) time.Duration {
	ds := make([]time.Duration, 0, len(m))
	for _, d := range m {
		ds = append(ds, d)
	}
	return median(ds)
}

// sortedJobs are its jobs, the most time first.
func (r *timeRow) sortedJobs() []*timeJob {
	var out []*timeJob
	for _, j := range r.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].week != out[k].week {
			return out[i].week > out[k].week
		}
		return out[i].name < out[k].name
	})
	return out
}

// growth says how much more time something took than the week before, when
// that is worth showing: 10% and at least min more, or less.
func growth(week, prior, least time.Duration) (pct int, ok bool) {
	if prior <= 0 || week <= 0 {
		return 0, false
	}
	diff := week - prior
	pct = int(diff * 100 / prior)
	if (pct >= 10 || pct <= -10) && (diff >= least || -diff >= least) {
		return pct, true
	}
	return pct, false
}

// moreRuns says how many times as many runs as the week before, when it is
// at least three times and ten runs.
func moreRuns(week, prior int) (int, bool) {
	if prior == 0 || week < 10 || week < 3*prior {
		return 0, false
	}
	return week / prior, true
}

// hours writes a runner time short: minutes below an hour, one decimal below
// ten hours, whole hours above.
func hours(d time.Duration) string {
	switch {
	case d <= 0:
		return "0h"
	case d < time.Hour:
		return strings.TrimSuffix(dur(d.Round(time.Minute)), " 00s")
	case d < 10*time.Hour:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", d.Hours()), ".0") + "h"
	}
	return fmt.Sprintf("%dh", int(d.Hours()+0.5))
}
