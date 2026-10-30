package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// timeApp holds two weeks of runs: api and web call build.yml of
// acme/workflows from their ci.yml, whose test job got slower this week;
// web also runs a nightly of its own six times as often as before.
func timeApp(t *testing.T) *App {
	now := time.Now()
	a := sharedApp(t)
	a.st.uses = []gh.Use{
		{Repo: "acme/api", File: ".github/workflows/ci.yml", Target: "acme/workflows/.github/workflows/build.yml", Version: "v3", Job: "build"},
		{Repo: "acme/web", File: ".github/workflows/ci.yml", Target: "acme/workflows/.github/workflows/build.yml", Version: "v3", Job: "Build it"},
	}
	runs := map[int64]*runTime{}
	id := int64(1)
	add := func(repo, path string, at time.Time, called []string, jobs ...gh.JobTime) {
		runs[id] = &runTime{Repo: repo, ID: id, Path: path, At: at, Attempt: 1, Called: called, Jobs: jobs, Read: true, WorkflowID: 7}
		id++
	}
	build := []string{"acme/workflows/.github/workflows/build.yml"}
	for i := range 10 {
		before, now := now.Add(-time.Duration(8*24+i)*time.Hour), now.Add(-time.Duration(1*24+i)*time.Hour)
		add("acme/api", ".github/workflows/ci.yml", before, build,
			gh.JobTime{Name: "build / test (ubuntu)", Took: 3 * time.Minute}, gh.JobTime{Name: "build / test (windows)", Took: 3 * time.Minute},
			gh.JobTime{Name: "build / lint", Took: time.Minute}, gh.JobTime{Name: "notify", Took: 10 * time.Second})
		add("acme/api", ".github/workflows/ci.yml", now, build,
			gh.JobTime{Name: "build / test (ubuntu)", Took: 9 * time.Minute}, gh.JobTime{Name: "build / test (windows)", Took: 9 * time.Minute},
			gh.JobTime{Name: "build / lint", Took: time.Minute}, gh.JobTime{Name: "notify", Took: 10 * time.Second})
		add("acme/web", ".github/workflows/ci.yml", now, build,
			gh.JobTime{Name: "Build it / test (ubuntu)", Took: 8 * time.Minute}, gh.JobTime{Name: "Build it / lint", Took: time.Minute})
	}
	for i := range 2 {
		add("acme/web", ".github/workflows/nightly.yml", now.Add(-time.Duration(9*24+i)*time.Hour), nil, gh.JobTime{Name: "e2e", Took: 20 * time.Minute})
	}
	for i := range 12 {
		add("acme/web", ".github/workflows/nightly.yml", now.Add(-time.Duration(2*24+i)*time.Hour), nil, gh.JobTime{Name: "e2e", Took: 20 * time.Minute})
	}
	a.st.times = timeData{runs: runs, repos: 2, state: loadState{at: now.Add(-3 * time.Hour)}}
	return a
}

func TestJobsCountForTheWorkflowTheyRanIn(t *testing.T) {
	rows := timeRows(timeApp(t).st.times.runs, timeApp(t).st.uses, false, time.Now())
	byKey := map[string]*timeRow{}
	for _, r := range rows {
		byKey[r.key] = r
	}
	build := byKey["acme/workflows/.github/workflows/build.yml"]
	if build == nil {
		t.Fatalf("no row for build.yml: %v", rows)
	}
	if want := 10*(18+1)*time.Minute + 10*9*time.Minute; build.week != want {
		t.Errorf("build.yml took %v this week, want %v", build.week, want)
	}
	if len(build.users) != 2 {
		t.Errorf("build.yml worked for %d repos, want 2", len(build.users))
	}
	test := build.jobs["test"]
	if test == nil || len(test.legs) != 2 {
		t.Fatalf("the test legs are not one job: %+v", build.jobs)
	}
	if own := byKey[sharedKey("acme/api", ".github/workflows/ci.yml")]; own == nil || own.week != 10*10*time.Second {
		t.Errorf("ci.yml of api keeps only its own notify job: %+v", own)
	}
	if rows[0].key != build.key {
		t.Errorf("the biggest is %s, want build.yml", rows[0].key)
	}
}

func TestTheTimeViewShowsHoursGrowthAndWhy(t *testing.T) {
	a := timeApp(t)
	v, out := sharedTab(a, viewTime)
	wantAll(t, out, "Time", "this week", "on last week", "build.yml", "nightly.yml", "▲ 6× runs", "▲ 300%")
	panel := ansi.Strip(strings.Join(v.time.panel(a, 70, 40), "\n"))
	wantAll(t, panel, "Daily usage", "Time per job", "Cause of increase", "test went from", "Top repositories", "One minute off test saves about")
}

func TestSpaceShowsTheJobsAndGGroupsByRepo(t *testing.T) {
	a := timeApp(t)
	v, _ := sharedTab(a, viewTime)
	v.Update(a, runes(" "))
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "test (2 legs)", "lint")
	v.Update(a, runes("g"))
	v.sync(a)
	out = ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "REPO", "api", "web")
}

func TestHoursAreShortAndGrowthNeedsToMatter(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Minute: "45m", 90 * time.Minute: "1.5h", 3 * time.Hour: "3h", 212 * time.Hour: "212h"} {
		if got := hours(d); got != want {
			t.Errorf("hours(%v) = %q, want %q", d, got, want)
		}
	}
	if _, ok := growth(110*time.Minute, 100*time.Minute, time.Hour); ok {
		t.Error("ten minutes more is not worth a pill")
	}
	if pct, ok := growth(30*time.Hour, 20*time.Hour, time.Hour); !ok || pct != 50 {
		t.Errorf("growth = %d %v, want 50 true", pct, ok)
	}
}

func TestACallerJobWithAMatrixStillFindsItsWorkflow(t *testing.T) {
	rt := &runTime{Repo: "acme/api", Path: ".github/workflows/ci.yml"}
	callers := map[string]string{"acme/api\x00.github/workflows/ci.yml\x00build": "acme/workflows/.github/workflows/build.yml"}
	if owner, name := jobOwner(rt, "build (node 20) / test", callers); owner != "acme/workflows/.github/workflows/build.yml" || name != "test" {
		t.Errorf("owner %q name %q", owner, name)
	}
	if owner, name := jobOwner(rt, "lint", callers); owner != sharedKey("acme/api", ".github/workflows/ci.yml") || name != "lint" {
		t.Errorf("own job: owner %q name %q", owner, name)
	}
}
