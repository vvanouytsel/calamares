package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// Records are leaderboards of what your repos and the people on them did in
// a period: the last 24 hours, week, month or all time. Every record ranks
// people, repos, workflows, runs or moments; the list shows its leader and
// the panel the top of the board.

type period int

const (
	periodDay period = iota
	periodWeek
	periodMonth
	periodAll
)

var periodNames = []string{"24 hours", "Week", "Month", "All time"}

// span is how far back the period reaches; 0 for all time.
func (p period) span() time.Duration {
	switch p {
	case periodDay:
		return 24 * time.Hour
	case periodWeek:
		return timeWeek
	case periodMonth:
		return statsMonth
	}
	return 0
}

// words say the period in a sentence: "in the last 24 hours".
func (p period) words() string {
	switch p {
	case periodDay:
		return "in the last 24 hours"
	case periodWeek:
		return "in the last 7 days"
	case periodMonth:
		return "in the last 30 days"
	}
	return "ever"
}

// The groups of records, in the order of the list.
const (
	groupPeople    = "People"
	groupRepos     = "Repos"
	groupWorkflows = "Workflows"
	groupMoments   = "Moments"
	groupYou       = "You"
)

var recordGroups = []string{groupPeople, groupRepos, groupWorkflows, groupMoments, groupYou}

// boardSize is how many places a leaderboard keeps.
const boardSize = 10

// record is one leaderboard.
type record struct {
	id    string
	group string
	name  string // "Most commits"
	icon  string
	about string // what is counted, said in the panel
	board []standing
	// value and lead replace what the list says of the first place, for a
	// record whose figure is not its leader's, such as a total.
	value, lead string
	// missing says why the record is not counted in this period; "" when it is.
	missing string
	// people says the board ranks people, so the panel gives your place.
	people bool
	you    standing // your place on a board of people, when you are on it
	place  int      // and which place, from 1
	total  int      // everyone together, when that means something
	// entrants is how many are on the board before it is cut to boardSize.
	entrants int
}

// standing is a place on a leaderboard: who or what, and its figure.
type standing struct {
	name string
	n    int64  // what is ranked
	text string // how the figure shows: "412", "1h 48m"
	// What enter opens: a person's runs, a repo, a workflow or a run.
	login  string
	repo   string
	wfID   int64
	run    *gh.Run
	detail string // a few words more
	// The parts of a workflow or run's name: its repo, short, and the workflow.
	short, workflow string
}

// shown is the record's figure and holder as the list shows them.
func (r record) shown() (value, holder string) {
	value, holder = r.value, r.lead
	if len(r.board) > 0 {
		if value == "" {
			value = r.board[0].text
		}
		if holder == "" {
			holder = r.board[0].name
		}
	}
	return value, holder
}

// recordsIn is what Records reads.
type recordsIn struct {
	stats statsSnapshot
	runs  map[int64]*runTime
	me    string
	now   time.Time
	short func(repo string) string
}

// records are the leaderboards of period p, in the order of the list.
func records(in recordsIn, p period) []record {
	if in.short == nil {
		in.short = func(r string) string { return r }
	}
	runs := in.runsIn(p)
	out := []record{
		in.peopleCommits(p),
		in.peopleMerged(p),
		in.peopleRepos(p),
		in.peopleRuns(p, runs),
		in.repoCommits(p),
		in.repoMerged(p),
		in.repoRuns(p, runs, false),
		in.repoRuns(p, runs, true),
		in.repoRunnerTime(p, runs),
		in.workflowFailures(p, runs),
		in.workflowReruns(p, runs),
		in.longestRuns(p, runs),
		in.fastestRuns(p, runs),
		in.mostAttempts(p, runs),
		in.busiestHour(p, runs),
		in.nightRuns(p, runs),
		in.fridayDeploys(p, runs),
		in.contributions(p),
		in.longestStreak(p),
	}
	for i := range out {
		if out[i].people {
			out[i].you, out[i].place = yourPlace(out[i].board, in.me)
		}
		out[i].entrants = len(out[i].board)
		out[i].board = out[i].board[:min(len(out[i].board), boardSize)]
	}
	return out
}

// since is where period p starts.
func (in recordsIn) since(p period) time.Time {
	if p == periodAll {
		return time.Time{}
	}
	return in.now.Add(-p.span())
}

// runsIn are the runs of the Time view in period p; none for a period longer
// than a week, as only the last two weeks are read run by run.
func (in recordsIn) runsIn(p period) []*runTime {
	if p != periodDay && p != periodWeek {
		return nil
	}
	since := in.since(p)
	var out []*runTime
	for _, rt := range in.runs {
		if !rt.At.Before(since) {
			out = append(out, rt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// runsOnly says why a record of single runs is not counted for p.
func runsOnly(p period) string {
	if p == periodDay || p == periodWeek {
		return ""
	}
	return "Counted for the last 24 hours and week only: it needs every single run, and Calamares reads them one by one for the last two weeks."
}

// isBot says a login or name belongs to an app, such as dependabot[bot].
func isBot(who string) bool {
	who = strings.ToLower(who)
	return strings.HasSuffix(who, "[bot]") || who == "dependabot" || who == "renovate-bot" || who == "github-actions"
}

// tally counts by key and ranks the keys, most first and by name on a tie.
type tally map[string]int64

func (t tally) board(text func(n int64) string, fill func(key string, s *standing)) []standing {
	out := make([]standing, 0, len(t))
	for k, n := range t {
		if n <= 0 {
			continue
		}
		s := standing{name: k, n: n, text: text(n)}
		if fill != nil {
			fill(k, &s)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	return out
}

func count(n int64) string { return thousands(int(n)) }

func asPerson(key string, s *standing) { s.login = key }

func (in recordsIn) asRepo(key string, s *standing) {
	s.repo, s.name = key, in.short(key)
}

func yourPlace(board []standing, me string) (standing, int) {
	for i, s := range board {
		if me != "" && strings.EqualFold(s.login, me) {
			return s, i + 1
		}
	}
	return standing{}, 0
}

func (in recordsIn) peopleCommits(p period) record {
	r := record{id: "people-commits", group: groupPeople, name: "Most commits", icon: "commit", people: true,
		about: "Commits on the default branches " + p.words() + ", apps left out"}
	t := tally{}
	if p == periodAll {
		for _, people := range in.stats.People {
			for _, c := range people {
				if !isBot(c.Login) {
					t[c.Login] += int64(c.Commits)
				}
			}
		}
	} else {
		since := in.since(p)
		for _, repo := range in.stats.Repos {
			for _, c := range repo.Commits {
				if !c.At.Before(since) && !isBot(c.Who()) {
					t[c.Who()]++
				}
			}
		}
	}
	r.board = t.board(count, asPerson)
	for _, s := range r.board {
		r.total += int(s.n)
	}
	return r
}

func (in recordsIn) peopleMerged(p period) record {
	r := record{id: "people-merged", group: groupPeople, name: "Most pull requests merged", icon: "merge", people: true,
		about: "Pull requests merged " + p.words() + ", by who opened them"}
	if p == periodAll {
		r.missing = "Not counted for all time: GitHub counts the merged pull requests of a repo, not of a person."
		return r
	}
	since, t := in.since(p), tally{}
	for _, repo := range in.stats.Repos {
		for _, m := range repo.Merged {
			if !m.At.Before(since) && m.Login != "" && !isBot(m.Login) {
				t[m.Login]++
			}
		}
	}
	r.board = t.board(count, asPerson)
	return r
}

func (in recordsIn) peopleRepos(p period) record {
	r := record{id: "people-repos", group: groupPeople, name: "Most repos committed to", icon: "repo", people: true,
		about: "Repos someone committed to " + p.words()}
	seen := map[string]map[string]bool{}
	add := func(who, repo string) {
		if isBot(who) {
			return
		}
		if seen[who] == nil {
			seen[who] = map[string]bool{}
		}
		seen[who][repo] = true
	}
	if p == periodAll {
		for repo, people := range in.stats.People {
			for _, c := range people {
				add(c.Login, repo)
			}
		}
	} else {
		since := in.since(p)
		for _, repo := range in.stats.Repos {
			for _, c := range repo.Commits {
				if !c.At.Before(since) {
					add(c.Who(), repo.Repo)
				}
			}
		}
	}
	t := tally{}
	for who, repos := range seen {
		t[who] = int64(len(repos))
	}
	r.board = t.board(func(n int64) string { return fmt.Sprintf("%d %s", n, plural("repo", int(n))) }, asPerson)
	return r
}

func (in recordsIn) peopleRuns(p period, runs []*runTime) record {
	r := record{id: "people-runs", group: groupPeople, name: "Most runs started", icon: "actions", people: true,
		about: "Runs started " + p.words() + ", apps left out", missing: runsOnly(p)}
	t := tally{}
	for _, rt := range runs {
		if rt.Actor != "" && !isBot(rt.Actor) {
			t[rt.Actor]++
		}
	}
	r.board = t.board(count, asPerson)
	return r
}

func (in recordsIn) repoCommits(p period) record {
	r := record{id: "repo-commits", group: groupRepos, name: "Most commits", icon: "commit",
		about: "Commits on the default branch " + p.words()}
	t := tally{}
	since := in.since(p)
	for _, repo := range in.stats.Repos {
		if p == periodAll {
			t[repo.Repo] = int64(repo.AllCommits)
			continue
		}
		for _, c := range repo.Commits {
			if !c.At.Before(since) {
				t[repo.Repo]++
			}
		}
	}
	if p == periodAll {
		r.about = "Commits on the default branch, ever, of the repos pushed to in the last 30 days"
	}
	r.board = t.board(count, in.asRepo)
	return r
}

func (in recordsIn) repoMerged(p period) record {
	r := record{id: "repo-merged", group: groupRepos, name: "Most pull requests merged", icon: "merge",
		about: "Pull requests merged " + p.words()}
	t := tally{}
	since := in.since(p)
	for _, repo := range in.stats.Repos {
		if p == periodAll {
			t[repo.Repo] = int64(repo.AllMerged)
			continue
		}
		for _, m := range repo.Merged {
			if !m.At.Before(since) {
				t[repo.Repo]++
			}
		}
	}
	if p == periodAll {
		r.about = "Pull requests merged, ever, of the repos pushed to in the last 30 days"
	}
	r.board = t.board(count, in.asRepo)
	return r
}

// repoRuns ranks the repos by their runs, or by their failed runs.
func (in recordsIn) repoRuns(p period, runs []*runTime, failed bool) record {
	r := record{id: "repo-runs", group: groupRepos, name: "Most runs", icon: "actions", about: "Finished runs " + p.words()}
	if failed {
		r.id, r.name, r.icon, r.about = "repo-failed", "Most failed runs", "fail", "Failed runs "+p.words()+", with the share of the repo's runs"
	}
	all, bad := tally{}, tally{}
	switch p {
	case periodDay, periodWeek:
		for _, rt := range runs {
			all[rt.Repo]++
			if rt.Conclusion == "failure" {
				bad[rt.Repo]++
			}
		}
	default:
		for repo, n := range in.stats.Runs {
			all[repo], bad[repo] = int64(n.All), int64(n.AllFailed)
			if p == periodMonth {
				all[repo], bad[repo] = int64(n.Month), int64(n.MonthFailed)
			}
		}
	}
	if !failed {
		r.board = all.board(count, in.asRepo)
		return r
	}
	r.board = bad.board(count, func(key string, s *standing) {
		in.asRepo(key, s)
		if n := all[key]; n > 0 {
			s.detail = fmt.Sprintf("%d%% of its %s runs", s.n*100/n, thousands(int(n)))
		}
	})
	return r
}

func (in recordsIn) repoRunnerTime(p period, runs []*runTime) record {
	r := record{id: "repo-time", group: groupRepos, name: "Most runner time", icon: "clock",
		about: "Time the jobs of its runs took on runners " + p.words(), missing: runsOnly(p)}
	t := tally{}
	for _, rt := range runs {
		for _, j := range rt.Jobs {
			t[rt.Repo] += int64(j.Took)
		}
	}
	r.board = t.board(func(n int64) string { return hours(time.Duration(n)) }, in.asRepo)
	return r
}

// workflowKey and workflowStanding name a workflow of a repo.
func workflowKey(rt *runTime) string { return rt.Repo + "\x00" + rt.Path }

func (in recordsIn) workflowStanding(rts map[string]*runTime) func(key string, s *standing) {
	return func(key string, s *standing) {
		rt := rts[key]
		s.short, s.workflow = in.short(rt.Repo), workflowLabel(rt)
		s.name, s.repo, s.wfID = s.short+" › "+s.workflow, rt.Repo, rt.WorkflowID
	}
}

// workflowLabel is the name of a run's workflow, or its file without .yml.
func workflowLabel(rt *runTime) string {
	if rt.Name != "" {
		return rt.Name
	}
	return workflowFileName(rt.Path)
}

func (in recordsIn) workflowFailures(p period, runs []*runTime) record {
	r := record{id: "workflow-failed", group: groupWorkflows, name: "Most failures", icon: "fail",
		about: "Failed runs of a workflow " + p.words(), missing: runsOnly(p)}
	t, rts := tally{}, map[string]*runTime{}
	for _, rt := range runs {
		if rt.Conclusion == "failure" {
			t[workflowKey(rt)]++
			rts[workflowKey(rt)] = rt
		}
	}
	r.board = t.board(count, in.workflowStanding(rts))
	return r
}

func (in recordsIn) workflowReruns(p period, runs []*runTime) record {
	r := record{id: "workflow-reruns", group: groupWorkflows, name: "Most passes after a rerun", icon: "flaky",
		about: "Runs that failed and passed when run again, " + p.words(), missing: runsOnly(p)}
	t, rts := tally{}, map[string]*runTime{}
	for _, rt := range runs {
		if rt.Attempt > 1 && rt.Conclusion == "success" {
			t[workflowKey(rt)]++
			rts[workflowKey(rt)] = rt
		}
	}
	r.board = t.board(count, in.workflowStanding(rts))
	return r
}

// runStanding is a single run on a board.
func (in recordsIn) runStanding(rt *runTime, n int64, text string) standing {
	run := rt.run()
	s := standing{name: fmt.Sprintf("%s › %s #%d", in.short(rt.Repo), workflowLabel(rt), rt.Number), n: n, text: text, repo: rt.Repo, run: &run,
		short: in.short(rt.Repo), workflow: workflowLabel(rt)}
	if rt.Actor != "" {
		s.detail = "by " + rt.Actor
	}
	if rt.Branch != "" {
		s.detail = strings.TrimPrefix(s.detail+" on "+rt.Branch, " ")
	}
	return s
}

// rankRuns ranks the runs that keep says to by n, most first, or least first with fewest.
func (in recordsIn) rankRuns(runs []*runTime, keep func(*runTime) bool, n func(*runTime) int64, text func(*runTime) string, fewest bool) []standing {
	var out []standing
	for _, rt := range runs {
		if keep(rt) {
			out = append(out, in.runStanding(rt, n(rt), text(rt)))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if fewest {
			return out[i].n < out[j].n
		}
		return out[i].n > out[j].n
	})
	return out
}

func (in recordsIn) longestRuns(p period, runs []*runTime) record {
	r := record{id: "run-longest", group: groupWorkflows, name: "Longest run", icon: "clock",
		about: "Runs from their start to their end, " + p.words(), missing: runsOnly(p)}
	r.board = in.rankRuns(runs, func(rt *runTime) bool { return rt.Took > 0 },
		func(rt *runTime) int64 { return int64(rt.Took) }, func(rt *runTime) string { return dur(rt.Took) }, false)
	return r
}

func (in recordsIn) fastestRuns(p period, runs []*runTime) record {
	r := record{id: "run-fastest", group: groupWorkflows, name: "Fastest passing run", icon: "rocket",
		about: "Runs that passed, from their start to their end, " + p.words(), missing: runsOnly(p)}
	r.board = in.rankRuns(runs, func(rt *runTime) bool { return rt.Took > 0 && rt.Conclusion == "success" },
		func(rt *runTime) int64 { return int64(rt.Took) }, func(rt *runTime) string { return dur(rt.Took) }, true)
	return r
}

func (in recordsIn) mostAttempts(p period, runs []*runTime) record {
	r := record{id: "run-attempts", group: groupWorkflows, name: "Most attempts", icon: "history",
		about: "Runs that were run again, by how many times they ran, " + p.words(), missing: runsOnly(p)}
	r.board = in.rankRuns(runs, func(rt *runTime) bool { return rt.Attempt > 1 },
		func(rt *runTime) int64 { return int64(rt.Attempt) }, func(rt *runTime) string { return fmt.Sprintf("%d attempts", rt.Attempt) }, false)
	return r
}

func (in recordsIn) busiestHour(p period, runs []*runTime) record {
	r := record{id: "moment-hour", group: groupMoments, name: "Busiest hour", icon: "clock",
		about: "Runs started in each hour of the day " + p.words() + ", in your time zone", missing: runsOnly(p)}
	t := tally{}
	for _, rt := range runs {
		h := rt.At.In(in.now.Location()).Hour()
		t[fmt.Sprintf("%02d:00–%02d:00", h, (h+1)%24)]++
	}
	r.board = t.board(func(n int64) string { return count(n) + " " + plural("run", int(n)) }, nil)
	if len(r.board) > 0 {
		r.value, r.lead = r.board[0].name, r.board[0].text
	}
	return r
}

func (in recordsIn) nightRuns(p period, runs []*runTime) record {
	r := record{id: "moment-night", group: groupMoments, name: "Runs at night", icon: "moon",
		about: "Runs started between midnight and 5:00 in your time zone, " + p.words() + ", by repo", missing: runsOnly(p)}
	t := tally{}
	for _, rt := range runs {
		if rt.At.In(in.now.Location()).Hour() < 5 {
			t[rt.Repo]++
			r.total++
		}
	}
	r.board = t.board(count, in.asRepo)
	r.value = count(int64(r.total))
	return r
}

func (in recordsIn) fridayDeploys(p period, runs []*runTime) record {
	r := record{id: "moment-friday", group: groupMoments, name: "Friday afternoon deploys", icon: "rocket",
		about: "Runs of a workflow named deploy or release started on a Friday from 15:00, " + p.words() + ", by repo", missing: runsOnly(p)}
	t := tally{}
	for _, rt := range runs {
		at := rt.At.In(in.now.Location())
		name := strings.ToLower(workflowLabel(rt) + " " + rt.Path)
		if at.Weekday() == time.Friday && at.Hour() >= 15 && (strings.Contains(name, "deploy") || strings.Contains(name, "release")) {
			t[rt.Repo]++
			r.total++
		}
	}
	r.board = t.board(count, in.asRepo)
	r.value = count(int64(r.total))
	return r
}

// daysIn are your contribution days of period p, oldest first, up to today.
func (in recordsIn) daysIn(p period) []gh.ContributionDay {
	today := in.now.Format("2006-01-02")
	first := ""
	if p != periodAll {
		first = in.now.AddDate(0, 0, 1-int(p.span()/(24*time.Hour))).Format("2006-01-02")
	}
	var out []gh.ContributionDay
	for _, d := range in.stats.Days {
		if d.Date >= first && d.Date <= today {
			out = append(out, d)
		}
	}
	return out
}

// dayLabel writes a date of the calendar in layout: "Mon 2 Jan 2006".
func dayLabel(date, layout string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format(layout)
}

func (in recordsIn) contributions(p period) record {
	r := record{id: "you-contributions", group: groupYou, name: "Your contributions", icon: "user",
		about: "What GitHub counts for you " + p.words() + ": commits, pull requests, reviews and issues; your busiest days"}
	t := tally{}
	for _, d := range in.daysIn(p) {
		t[d.Date] = int64(d.Count)
		r.total += d.Count
	}
	r.board = t.board(count, func(key string, s *standing) { s.name = dayLabel(key, "Mon 2 Jan 2006") })
	r.value = count(int64(r.total))
	if len(r.board) > 0 {
		r.lead = "busiest " + r.board[0].name
	}
	return r
}

func (in recordsIn) longestStreak(p period) record {
	r := record{id: "you-streak", group: groupYou, name: "Your longest streak", icon: "calendar",
		about: "Days in a row with a contribution " + p.words()}
	if p == periodDay {
		r.missing = "A streak takes more than a day: pick a longer period."
		return r
	}
	var board []standing
	start, prev, n := "", time.Time{}, 0
	flush := func() {
		if n > 0 {
			name := dayLabel(start, "2 Jan 2006")
			if n > 1 {
				name = dayLabel(start, "2 Jan") + " – " + prev.Format("2 Jan 2006")
			}
			board = append(board, standing{name: name, n: int64(n), text: fmt.Sprintf("%d %s", n, plural("day", n))})
		}
	}
	for _, d := range in.daysIn(p) {
		at, err := time.Parse("2006-01-02", d.Date)
		if err != nil {
			continue
		}
		switch {
		case d.Count == 0:
			flush()
			n = 0
		case n > 0 && at.Sub(prev) == 24*time.Hour:
			n++
		default:
			flush()
			start, n = d.Date, 1
		}
		prev = at
	}
	flush()
	sort.SliceStable(board, func(i, j int) bool { return board[i].n > board[j].n })
	r.board = board
	return r
}
