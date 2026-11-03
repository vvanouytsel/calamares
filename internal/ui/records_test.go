package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

var recordsNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func recordByID(t *testing.T, recs []record, id string) record {
	t.Helper()
	for _, r := range recs {
		if r.id == id {
			return r
		}
	}
	t.Fatalf("no record %s", id)
	return record{}
}

func commitBy(login string, ago time.Duration) gh.Authored {
	return gh.Authored{Login: login, At: recordsNow.Add(-ago)}
}

func TestMostCommitsCountsOnlyThePeriodLeavesAppsOutAndGivesYourPlace(t *testing.T) {
	day := 24 * time.Hour
	in := recordsIn{now: recordsNow, me: "robin", stats: statsSnapshot{Repos: []gh.RepoStats{
		{Repo: "acme/webshop", Commits: []gh.Authored{commitBy("sam", time.Hour), commitBy("sam", 2*day), commitBy("robin", 3*day),
			commitBy("dependabot[bot]", time.Hour), commitBy("dependabot[bot]", 2*time.Hour), commitBy("dependabot[bot]", 3*time.Hour)}},
		{Repo: "acme/api", Commits: []gh.Authored{commitBy("sam", 4*day), commitBy("noor", 20*day)}},
	}}}
	week := recordByID(t, records(in, periodWeek), "people-commits")
	if len(week.board) != 2 || week.board[0].login != "sam" || week.board[0].n != 3 || week.board[1].login != "robin" {
		t.Fatalf("week board = %+v, want sam 3 then robin", week.board)
	}
	if week.place != 2 || week.you.n != 1 {
		t.Errorf("your place %d with %d, want 2nd with 1", week.place, week.you.n)
	}
	day1 := recordByID(t, records(in, periodDay), "people-commits")
	if len(day1.board) != 1 || day1.board[0].login != "sam" || day1.board[0].n != 1 {
		t.Errorf("24 hour board = %+v, want sam with 1", day1.board)
	}
	repos := recordByID(t, records(in, periodMonth), "people-repos")
	if repos.board[0].login != "sam" || repos.board[0].text != "2 repos" {
		t.Errorf("repos committed to = %+v, want sam on 2 repos", repos.board)
	}
}

func TestAllTimeCommitsAddUpTheContributorsOfEveryRepo(t *testing.T) {
	in := recordsIn{now: recordsNow, stats: statsSnapshot{People: map[string][]gh.Contributor{
		"acme/webshop": {{Login: "sam", Commits: 40}, {Login: "noor", Commits: 70}},
		"acme/api":     {{Login: "sam", Commits: 50}, {Login: "renovate[bot]", Commits: 900}},
	}}}
	r := recordByID(t, records(in, periodAll), "people-commits")
	if len(r.board) != 2 || r.board[0].login != "sam" || r.board[0].n != 90 || r.board[1].n != 70 {
		t.Errorf("board = %+v, want sam 90 then noor 70", r.board)
	}
	if r.total != 160 {
		t.Errorf("total = %d, want 160", r.total)
	}
}

func runAt(id int64, repo, conclusion string, ago time.Duration, took time.Duration) *runTime {
	return &runTime{ID: id, Repo: repo, Path: ".github/workflows/ci.yml", Name: "ci", Number: int(id), WorkflowID: 7,
		At: recordsNow.Add(-ago), Attempt: 1, Conclusion: conclusion, Actor: "sam", Took: took}
}

func TestRunRecordsCountTheWeekAndAreNotCountedForTheMonth(t *testing.T) {
	runs := map[int64]*runTime{
		1: runAt(1, "acme/webshop", "failure", time.Hour, 5*time.Minute),
		2: runAt(2, "acme/webshop", "success", 2*time.Hour, 3*time.Minute),
		3: runAt(3, "acme/api", "failure", 3*time.Hour, 50*time.Minute),
		4: runAt(4, "acme/api", "failure", 10*24*time.Hour, time.Minute),
	}
	in := recordsIn{now: recordsNow, runs: runs}
	week := records(in, periodWeek)
	failed := recordByID(t, week, "repo-failed")
	if len(failed.board) != 2 || failed.board[0].repo != "acme/api" || failed.board[0].n != 1 || failed.board[1].detail != "50% of its 2 runs" {
		t.Errorf("failed runs = %+v", failed.board)
	}
	longest := recordByID(t, week, "run-longest")
	if longest.board[0].run == nil || longest.board[0].run.ID != 3 || longest.board[0].text != "50m 00s" {
		t.Errorf("longest = %+v, want run 3 of 50 minutes", longest.board[0])
	}
	fastest := recordByID(t, week, "run-fastest")
	if len(fastest.board) != 1 || fastest.board[0].run.ID != 2 {
		t.Errorf("fastest passing = %+v, want only run 2", fastest.board)
	}
	if r := recordByID(t, records(in, periodMonth), "run-longest"); r.missing == "" || len(r.board) != 0 {
		t.Errorf("longest run in a month = %+v, want it not counted", r)
	}
}

func TestMonthAndAllTimeRunsComeFromTheRunCountsPerRepo(t *testing.T) {
	in := recordsIn{now: recordsNow, stats: statsSnapshot{Runs: map[string]runCounts{
		"acme/webshop": {Month: 300, MonthFailed: 12, All: 9000, AllFailed: 400},
		"acme/api":     {Month: 500, MonthFailed: 3, All: 2000, AllFailed: 90},
	}}}
	month := recordByID(t, records(in, periodMonth), "repo-runs")
	if month.board[0].repo != "acme/api" || month.board[0].n != 500 {
		t.Errorf("month = %+v, want acme/api with 500", month.board)
	}
	all := recordByID(t, records(in, periodAll), "repo-failed")
	if all.board[0].repo != "acme/webshop" || all.board[0].n != 400 {
		t.Errorf("all time failed = %+v, want acme/webshop with 400", all.board)
	}
}

func TestLongestStreakIsTheLongestRunOfDaysWithAContribution(t *testing.T) {
	var days []gh.ContributionDay
	counts := []int{1, 1, 0, 2, 3, 1, 4, 0, 1}
	for i, n := range counts {
		days = append(days, gh.ContributionDay{Date: recordsNow.AddDate(0, 0, i-len(counts)+1).Format("2006-01-02"), Count: n})
	}
	in := recordsIn{now: recordsNow, stats: statsSnapshot{Days: days}}
	r := recordByID(t, records(in, periodMonth), "you-streak")
	if len(r.board) != 3 || r.board[0].text != "4 days" || r.board[1].text != "2 days" {
		t.Errorf("streaks = %+v, want 4, 2 and 1 days", r.board)
	}
	if r := recordByID(t, records(in, periodDay), "you-streak"); r.missing == "" {
		t.Error("a streak in 24 hours is counted, want it not counted")
	}
	c := recordByID(t, records(in, periodWeek), "you-contributions")
	if c.value != "11" || !strings.HasPrefix(c.lead, "busiest ") {
		t.Errorf("week = %q %q, want 11 and the busiest day", c.value, c.lead)
	}
}

func recordsApp(t *testing.T) *App {
	a := achievementsApp(t)
	a.me = "robin"
	a.st.stats.snap = statsSnapshot{Repos: []gh.RepoStats{{Repo: "acme/webshop", Commits: []gh.Authored{
		{Login: "sam", At: time.Now().Add(-time.Hour)}, {Login: "sam", At: time.Now().Add(-2 * time.Hour)}, {Login: "robin", At: time.Now().Add(-3 * time.Hour)},
	}}}}
	a.st.stats.state.at = time.Now()
	a.st.times.state.at = time.Now()
	press(a, "7", "tab", "tab", "tab")
	return a
}

func TestRecordsListsTheHolderOfEachRecordAndTSwitchesThePeriod(t *testing.T) {
	a := recordsApp(t)
	out := ansi.Strip(a.View())
	for _, want := range []string{"Records", " 24 hours · ", "Most commits", "sam", "Leaderboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("Records misses %q:\n%s", want, out)
		}
	}
	press(a, "t")
	if out := ansi.Strip(a.View()); !strings.Contains(out, " Week · ") {
		t.Errorf("t did not show the week:\n%s", out)
	}
}

func TestEnterOnAPersonShowsTheRunsTheyStarted(t *testing.T) {
	a := recordsApp(t)
	a.View()
	press(a, "enter")
	v, ok := a.top().(*activityView)
	if a.tab != tabActions || !ok {
		t.Fatalf("enter went to place %d, want Actions", a.tab)
	}
	if v.filter != viewRecent || v.FilterValue() != "actor:sam" {
		t.Errorf("view %d filtered by %q, want the last 7 days by actor:sam", v.filter, v.FilterValue())
	}
}

func TestReadingRecordsFillsThemAndEnds(t *testing.T) {
	withGitHub(t, map[string]string{
		"/graphql":                               `{"data":{"viewer":{"contributionsCollection":{"contributionYears":[]}}}}`,
		"/repos/acme/webshop/stats/contributors": `[{"total":12,"author":{"login":"sam"}}]`,
		"/repos/acme/api/stats/contributors":     `[{"total":3,"author":{"login":"noor"}}]`,
		"/repos/acme/webshop/actions/runs":       `{"total_count":40,"workflow_runs":[]}`,
	})
	a := achievementsApp(t)
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, PushedAt: time.Now()}, {FullName: "acme/api", PushedAt: time.Now()}}
	drive(a, a.st.loadStats(a, true), 5*time.Second)
	st := a.st.stats
	if st.state.busy || st.state.err != nil {
		t.Fatalf("still busy %v, error %v", st.state.busy, st.state.err)
	}
	if len(st.snap.People["acme/webshop"]) != 1 || len(st.snap.People["acme/api"]) != 1 {
		t.Errorf("people = %+v, want both repos", st.snap.People)
	}
	if st.snap.Runs["acme/webshop"].All != 40 {
		t.Errorf("runs = %+v, want 40 for acme/webshop", st.snap.Runs)
	}
}

func TestARecordsStepShowsBeforeTheReadEnds(t *testing.T) {
	a := achievementsApp(t)
	st := &a.st.stats
	st.state.begin()
	st.next = statsSnapshot{People: map[string][]gh.Contributor{}, Runs: map[string]runCounts{}}
	st.streams, st.steps = 2, 2
	a.st.gotStatsStep(a, statsStepMsg{apply: func(sn *statsSnapshot) { sn.Runs["acme/webshop"] = runCounts{All: 9} }})
	if !st.state.busy {
		t.Error("the read ended with a stream still going")
	}
	if st.snap.Runs["acme/webshop"].All != 9 {
		t.Errorf("runs = %+v, want the step shown at once", st.snap.Runs)
	}
}

func TestARecordsReadWithAFailedStepKeepsWhatWasKnown(t *testing.T) {
	a := achievementsApp(t)
	st := &a.st.stats
	st.snap.People = map[string][]gh.Contributor{"acme/api": {{Login: "noor", Commits: 3}}}
	st.state.begin()
	st.next = statsSnapshot{People: map[string][]gh.Contributor{}, Runs: map[string]runCounts{}}
	st.streams, st.steps = 2, 2
	a.st.gotStatsStep(a, statsStepMsg{apply: func(sn *statsSnapshot) { sn.People["acme/webshop"] = []gh.Contributor{{Login: "sam", Commits: 1}} }})
	a.st.gotStatsStep(a, statsStepMsg{err: errors.New("HTTP 502")})
	if st.state.busy {
		t.Fatal("the read did not end")
	}
	if len(st.snap.People) != 2 {
		t.Errorf("people = %+v, want what was known and what was read", st.snap.People)
	}
}

func contributionDays(counts ...int) []gh.ContributionDay {
	var days []gh.ContributionDay
	for i, n := range counts {
		days = append(days, gh.ContributionDay{Date: recordsNow.AddDate(0, 0, i-len(counts)+1).Format("2006-01-02"), Count: n})
	}
	return days
}

func TestAWeekOfContributionsIsABarPerDayWithItsWeekday(t *testing.T) {
	in := recordsIn{now: recordsNow, stats: statsSnapshot{Days: contributionDays(9, 0, 3, 17, 7, 5, 0, 11)}}
	bars := contributionBars(in, periodWeek)
	if len(bars) != 7 || bars[0].label != "We" || bars[6].label != "Tu" {
		t.Fatalf("bars = %+v, want the last 7 days from Wednesday to Tuesday", bars)
	}
	lines := contributionChart(bars, 40)
	if len(lines) != chartHeight+1 {
		t.Fatalf("got %d lines, want %d bars and the labels", len(lines), chartHeight)
	}
	top := []rune(ansi.Strip(lines[0]))
	if top[2*4] != '█' || top[0] == '█' {
		t.Errorf("top line %q, want only the busiest day (the third bar) full", string(top))
	}
	if got := chartSummary(in, periodWeek, bars); got != "43 contributions · busiest Fri 30 Oct, 17" {
		t.Errorf("summary = %q", got)
	}
}

func TestAllTimeContributionsAreABarPerMonthAndNoneFor24Hours(t *testing.T) {
	in := recordsIn{now: recordsNow, stats: statsSnapshot{Days: []gh.ContributionDay{{Date: "2025-01-15", Count: 4}, {Date: "2025-01-20", Count: 6}, {Date: "2026-11-02", Count: 1}}}}
	bars := contributionBars(in, periodAll)
	if len(bars) != 24 || bars[0].label != "Dec 2024" || bars[1].count != 10 || bars[1].label != "Jan 2025" {
		t.Errorf("bars = %+v, want 24 months from Dec 2024 with January's 10", bars)
	}
	if bars := contributionBars(in, periodDay); bars != nil {
		t.Errorf("24 hours has bars %+v, want none", bars)
	}
}
