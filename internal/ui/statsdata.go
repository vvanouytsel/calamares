package ui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// What Records reads besides the runs of the Time view: the commits and
// merged pull requests of the last month on every repo pushed to in it, the
// commits per person ever, the runs per repo of the last month and ever, and
// your contribution calendar. It is read once a day and kept for the next start.
const (
	snapshotStats = "stats/1"
	statsMonth    = 30 * 24 * time.Hour
	// statsCountingWait is how long GitHub gets to work out the commits per
	// person of the repos it had not counted yet, before they are asked again.
	statsCountingWait = 3 * time.Second
)

type statsSnapshot struct {
	Repos  []gh.RepoStats              `json:"r,omitempty"`
	People map[string][]gh.Contributor `json:"p,omitempty"` // per repo, ever
	// Counting are the repos whose commits per person GitHub had not worked out.
	Counting []string             `json:"w,omitempty"`
	Runs     map[string]runCounts `json:"n,omitempty"` // per repo with workflows
	Days     []gh.ContributionDay `json:"d,omitempty"`
}

func (sn statsSnapshot) empty() bool {
	return len(sn.Repos) == 0 && len(sn.People) == 0 && len(sn.Runs) == 0 && len(sn.Days) == 0
}

// runCounts are how many runs a repo had in the last month and ever, and
// how many of them failed.
type runCounts struct {
	Month       int `json:"m"`
	MonthFailed int `json:"mf"`
	All         int `json:"a"`
	AllFailed   int `json:"af"`
}

type statsData struct {
	snap  statsSnapshot
	state loadState
	// While a read goes on: what it has read so far, which replaces snap
	// once every stream is done; how many streams are still going, how many
	// steps they have and have done, and the first error.
	next          statsSnapshot
	streams       int
	steps, done   int
	err           error
	failed, fresh int
}

// statsStep reads one piece, such as a repo's run counts, and says how to
// put it in a snapshot.
type statsStep func() (apply func(*statsSnapshot), err error)

// statsStepMsg is a step that was read, and the rest of its stream.
type statsStepMsg struct {
	apply func(*statsSnapshot)
	err   error
	rest  []statsStep
}

// statsStreams is how many streams the repos whose commits per person are
// read are split over; the background lane takes four requests at a time.
const statsStreams = 2

// statsTries is how many times the commits per person of a repo are asked
// for while GitHub still works them out.
const statsTries = 3

func (s *store) seedStats(a *App) {
	if at, ok := a.client.Recall(snapshotStats, &s.stats.snap); ok {
		s.stats.state.at = at
	}
}

// loadStats reads what Records shows once a day, or when forced. It reads in
// a few streams side by side, cheapest first, and every step shows as soon
// as it is read.
func (s *store) loadStats(a *App, force bool) tea.Cmd {
	l := &s.stats.state
	if len(a.repos) == 0 || (!force && time.Since(l.at) < timeStale) || (!force && !l.waited(time.Hour)) || !l.begin() {
		return nil
	}
	var active, all, ci []string
	since := time.Now().Add(-statsMonth)
	for _, r := range a.repos {
		if r.Archived || r.Fork {
			continue
		}
		all = append(all, r.FullName)
		if r.PushedAt.After(since) {
			active = append(active, r.FullName)
		}
	}
	for _, r := range a.ciRepos() {
		ci = append(ci, r.FullName)
	}
	c := a.client
	ctx := gh.Background(context.Background())

	first := []statsStep{func() (func(*statsSnapshot), error) {
		days, err := c.Contributions(ctx)
		return func(sn *statsSnapshot) { sn.Days = days }, statsErr("your contributions", err)
	}}
	for start := 0; start < len(active); start += statsBatch {
		batch := active[start:min(start+statsBatch, len(active))]
		first = append(first, func() (func(*statsSnapshot), error) {
			got, err := c.RepoStats(ctx, batch, since)
			return func(sn *statsSnapshot) { sn.Repos = upsertRepoStats(sn.Repos, got) }, statsErr("the commits and pull requests of "+batch[0]+" and the next", err)
		})
	}
	var counts []statsStep
	for _, repo := range ci {
		counts = append(counts, func() (func(*statsSnapshot), error) {
			var n runCounts
			var err error
			if n.Month, n.MonthFailed, err = c.RunCount(ctx, repo, since); err != nil {
				return nil, statsErr("the runs of "+repo, err)
			}
			if n.All, n.AllFailed, err = c.RunCount(ctx, repo, time.Time{}); err != nil {
				return nil, statsErr("the runs of "+repo, err)
			}
			return func(sn *statsSnapshot) { sn.Runs[repo] = n }, nil
		})
	}
	people := make([][]statsStep, statsStreams)
	for i, repo := range all {
		people[i%statsStreams] = append(people[i%statsStreams], func() (func(*statsSnapshot), error) {
			got, ready, err := contributorsOf(ctx, c, repo)
			return func(sn *statsSnapshot) {
				if ready {
					sn.People[repo] = got
				} else {
					sn.Counting = append(sn.Counting, repo)
				}
			}, statsErr("the commits per person of "+repo, err)
		})
	}

	s.stats.next = statsSnapshot{People: map[string][]gh.Contributor{}, Runs: map[string]runCounts{}}
	s.stats.streams, s.stats.done, s.stats.steps, s.stats.err, s.stats.failed, s.stats.fresh = 0, 0, 0, nil, 0, 0
	var cmds []tea.Cmd
	for _, stream := range append([][]statsStep{first, counts}, people...) {
		if len(stream) > 0 {
			s.stats.streams++
			s.stats.steps += len(stream)
			cmds = append(cmds, readStatsStream(stream))
		}
	}
	if len(cmds) == 0 {
		l.end(nil)
	}
	return tea.Batch(cmds...)
}

// statsErr says which part of the records err kept from being read.
func statsErr(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// statsBatch is how many repos one step of the commits and pull requests reads.
const statsBatch = 10

func readStatsStream(stream []statsStep) tea.Cmd {
	return func() tea.Msg {
		apply, err := stream[0]()
		return statsStepMsg{apply: apply, err: err, rest: stream[1:]}
	}
}

// contributorsOf reads the commits per person of repo, asking again a few
// seconds later while GitHub still works them out.
func contributorsOf(ctx context.Context, c *gh.Client, repo string) ([]gh.Contributor, bool, error) {
	for try := 1; ; try++ {
		got, ready, err := c.Contributors(ctx, repo)
		if err != nil || ready || try == statsTries {
			return got, ready, err
		}
		time.Sleep(statsCountingWait)
	}
}

// upsertRepoStats puts got in list, in place of what list held of the same repos.
func upsertRepoStats(list, got []gh.RepoStats) []gh.RepoStats {
	for _, g := range got {
		i := slices.IndexFunc(list, func(r gh.RepoStats) bool { return r.Repo == g.Repo })
		if i < 0 {
			list = append(list, g)
			continue
		}
		list[i] = g
	}
	return list
}

// gotStatsStep shows a step at once and goes on with the rest of its
// stream. When the last stream ends, what this read found replaces what was
// known, unless a step failed: then it is only added, so nothing is lost.
func (s *store) gotStatsStep(a *App, m statsStepMsg) tea.Cmd {
	st := &s.stats
	st.done++
	if m.err != nil {
		st.failed++
		if st.err == nil {
			st.err = m.err
		}
	} else if m.apply != nil {
		st.fresh++
		if st.snap.People == nil {
			st.snap.People = map[string][]gh.Contributor{}
		}
		if st.snap.Runs == nil {
			st.snap.Runs = map[string]runCounts{}
		}
		m.apply(&st.snap)
		m.apply(&st.next)
	}
	if len(m.rest) > 0 {
		return readStatsStream(m.rest)
	}
	if st.streams--; st.streams > 0 {
		return nil
	}
	if st.fresh == 0 {
		st.state.end(st.err)
		return a.flashCmd(nError, "Could not read the records: "+st.err.Error())
	}
	st.state.end(nil)
	st.snap.Counting = st.next.Counting
	if st.failed == 0 {
		st.snap = st.next
	}
	st.next = statsSnapshot{}
	a.client.Remember(snapshotStats, st.snap)
	if st.failed > 0 {
		return a.flashCmd(nError, fmt.Sprintf("%d of %d reads for the records failed; the first: %v", st.failed, st.steps, st.err))
	}
	return nil
}
