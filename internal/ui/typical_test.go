package ui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"strings"
	"testing"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestVersusTypical(t *testing.T) {
	typical := 2 * time.Minute
	cases := []struct {
		name    string
		elapsed time.Duration
		running bool
		note    string
		color   any
	}{
		{"within a few seconds of typical is on par", 2*time.Minute + 3*time.Second, false, "≈", cDim},
		{"a bit slower is amber", 3 * time.Minute, false, "+1m 00s ▲", cAmber},
		{"twice as slow is red", 4 * time.Minute, false, "+2m 00s ▲", cRed},
		{"much faster is green", time.Minute, false, "−1m 00s ▼", cGreen},
		{"running under typical shows how far along it is", time.Minute, true, "50%", cDim},
		{"a long job slightly over typical stays quiet", 2*time.Minute + 11*time.Second, false, "+11s ▲", cDim},
		{"running over typical shows the overrun", 5 * time.Minute, true, "+3m 00s ▲", cRed},
	}
	for _, c := range cases {
		note, color := versusTypical(c.elapsed, typical, c.running)
		if note != c.note || color != c.color {
			t.Errorf("%s: got %q %v, want %q %v", c.name, note, color, c.note, c.color)
		}
	}
	if note, _ := versusTypical(time.Minute, 0, false); note != "" {
		t.Errorf("without a baseline got %q, want nothing", note)
	}
}

func TestPipelineKnowsHowLongEachJobUsuallyTakes(t *testing.T) {
	jobs := []gh.Job{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "test (linux)", Status: "completed", Conclusion: "success"},
		{ID: 3, Name: "test (windows)", Status: "completed", Conclusion: "success"},
	}
	p := buildPipeline(jobs, samplePlan, "", true, matrixOne)
	p.applyTypical(jobs, map[string]gh.TypicalJob{
		"build":          {Median: time.Minute, Samples: 8},
		"test (linux)":   {Median: 2 * time.Minute, Samples: 5},
		"test (windows)": {Median: 4 * time.Minute, Samples: 3},
	}, false)
	if n := p.byKey["build"]; n.typical != time.Minute || n.samples != 8 {
		t.Errorf("build typical %v from %d runs, want 1m from 8", n.typical, n.samples)
	}
	if n := p.byKey["test"]; n.typical != 4*time.Minute || n.samples != 3 {
		t.Errorf("a matrix usually takes as long as its slowest leg, got %v from %d runs", n.typical, n.samples)
	}
	if n := p.byKey["lint"]; n.typical != 0 {
		t.Errorf("a job without a baseline has typical %v, want 0", n.typical)
	}
}

func TestPipelineBoxShowsHowMuchSlowerAJobWasThanTypical(t *testing.T) {
	now := time.Now()
	jobs := []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-5 * time.Minute), CompletedAt: now}}
	p := buildPipeline(jobs, samplePlan, "", true, matrixOne)
	p.applyTypical(jobs, map[string]gh.TypicalJob{"build": {Median: 2 * time.Minute, Samples: 8}}, false)
	p.zoom = zoomBoxes
	a := apiApp(t)
	var x, y int
	lines := p.render(a, "build", 200, 40, &x, &y, viewKeep, now)
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "+3m 00s ▲") {
		t.Errorf("the box of build shows no comparison with its typical 2m on its bottom border:\n%s", ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestTypicalDurationsAreOffByDefaultAndReadNothing(t *testing.T) {
	a := apiApp(t)
	run := ghRun(7, "completed")
	run.WorkflowID = 3
	v := newRunView(a, "o/r", run)
	if a.cfg.Comparing() {
		t.Error("durations are compared by default, want off")
	}
	if cmd := v.loadTypical(a); cmd != nil {
		t.Error("a baseline is read while the setting is off")
	}
}

func TestTheTKeyCyclesFromOffToTheMedianToThePreviousRunAndOff(t *testing.T) {
	a := apiApp(t)
	run := ghRun(7, "completed")
	run.WorkflowID = 3
	v := newRunView(a, "o/r", run)
	v.key(a, "t")
	if a.cfg.CompareDurations != config.CompareMedian || v.typicalLoad != typicalLoading {
		t.Errorf("after t: mode %q, load state %d, want median and loading", a.cfg.CompareDurations, v.typicalLoad)
	}
	if saved, err := config.Load(); err != nil || saved.CompareDurations != config.CompareMedian {
		t.Errorf("the choice was not saved: %v %v", saved, err)
	}
	v.key(a, "t")
	if a.cfg.CompareDurations != config.ComparePrevious {
		t.Errorf("after t twice: mode %q, want previous", a.cfg.CompareDurations)
	}
	v.key(a, "t")
	v.Render(a, 100, 30)
	if a.cfg.Comparing() || v.typical != nil || v.typicalLoad != 0 {
		t.Errorf("after t three times: mode %q, typical %v, load state %d, want off and cleared", a.cfg.CompareDurations, v.typical, v.typicalLoad)
	}
}

func TestHeaderSaysWhereTheTypicalDurationsStand(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "completed"))
	for state, want := range map[int]string{
		typicalLoading: "reading earlier runs",
		typicalFailed:  "could not read earlier runs",
		typicalDone:    "too few earlier runs",
	} {
		v.typicalLoad = state
		if got := ansi.Strip(v.typicalChip(a)); !strings.Contains(got, want) {
			t.Errorf("state %d shows %q, want it to say %q", state, got, want)
		}
	}
	v.typical = map[string]gh.TypicalJob{"build": {Median: time.Minute, Samples: 8}}
	v.typicalLoad = typicalDone
	if got := ansi.Strip(v.typicalChip(a)); !strings.Contains(got, "median of 8 runs") {
		t.Errorf("a loaded baseline shows %q, want the number of runs", got)
	}
}

func TestJobListShowsTypicalColumnsOnlyWhileTheyAreOn(t *testing.T) {
	a := apiApp(t)
	now := time.Now()
	v := newRunView(a, "o/r", ghRun(7, "completed"))
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-5 * time.Minute), CompletedAt: now}}
	v.typical = map[string]gh.TypicalJob{"build": {Median: 2 * time.Minute, Samples: 8}}
	v.loaded, v.jobsLoaded = true, true
	v.rebuild()
	if out := ansi.Strip(v.Render(a, 140, 20)); strings.Contains(out, "MEDIAN") || strings.Contains(out, "+3m 00s") {
		t.Errorf("columns show while the setting is off:\n%s", out)
	}
	a.cfg.CompareDurations = config.CompareMedian
	v.typical = map[string]gh.TypicalJob{"build": {Median: 2 * time.Minute, Samples: 8}}
	v.typicalLoad = typicalDone
	out := ansi.Strip(v.Render(a, 140, 20))
	for _, want := range []string{"DURATION", "MEDIAN", "2m 00s", "+3m 00s ▲", "median of 8 runs"} {
		if !strings.Contains(out, want) {
			t.Errorf("job list lacks %q:\n%s", want, out)
		}
	}
}

func TestASlowerJobIsColoredOnlyWhenSlowerInProportionAndByTheClock(t *testing.T) {
	cases := []struct {
		name            string
		typical, actual time.Duration
		color           any
	}{
		{"a short job 10s over stays quiet", 30 * time.Second, 40 * time.Second, cDim},
		{"a short job 3x slower by 12s stays quiet", 6 * time.Second, 18 * time.Second, cDim},
		{"15s over and 1.5x is amber", 30 * time.Second, 45 * time.Second, cAmber},
		{"30s over and 2x is red", 30 * time.Second, 60 * time.Second, cRed},
		{"2x but only 20s over is amber", 20 * time.Second, 40 * time.Second, cAmber},
	}
	for _, c := range cases {
		if _, color := versusTypical(c.actual, c.typical, false); color != c.color {
			t.Errorf("%s: got %v, want %v", c.name, color, c.color)
		}
	}
}

func TestExpandedJobShowsTypicalDurationsOfItsSteps(t *testing.T) {
	a := apiApp(t)
	a.cfg.CompareDurations = config.CompareMedian
	now := time.Now()
	v := newRunView(a, "o/r", ghRun(7, "completed"))
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-5 * time.Minute), CompletedAt: now,
		Steps: []gh.Step{{Name: "Test", Number: 1, Status: "completed", Conclusion: "success", StartedAt: now.Add(-4 * time.Minute), CompletedAt: now}}}}
	v.typical = map[string]gh.TypicalJob{"build": {Median: 2 * time.Minute, Samples: 8,
		Steps: map[string]gh.TypicalJob{"Test": {Median: time.Minute, Samples: 8}}}}
	v.loaded, v.jobsLoaded, v.typicalLoad = true, true, typicalDone
	v.expanded["j:1"] = true
	v.rebuild()
	out := ansi.Strip(v.Render(a, 150, 20))
	if !strings.Contains(out, "4m 00s     1m 00s  +3m 00s ▲") {
		t.Errorf("the Test step row shows no typical duration:\n%s", out)
	}
	summary := ansi.Strip(strings.Join(v.jobSummary(a, v.jobs[0], 100, now), "\n"))
	if !strings.Contains(summary, "STEP") || !strings.Contains(summary, "+3m 00s ▲") {
		t.Errorf("the steps in the job details lack a header or a comparison:\n%s", summary)
	}
}

func TestAFailedBaselineIsNotRequestedAgainOnEveryRefresh(t *testing.T) {
	a := apiApp(t)
	a.cfg.CompareDurations = config.CompareMedian
	run := ghRun(7, "in_progress")
	run.WorkflowID = 3
	v := newRunView(a, "o/r", run)
	v.typicalLoad = typicalFailed
	if cmd := v.loadTypical(a); cmd != nil {
		t.Error("a failed baseline is requested again")
	}
}

func TestInPreviousModeThePipelineBoxComparesWithThePreviousRun(t *testing.T) {
	now := time.Now()
	jobs := []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-5 * time.Minute), CompletedAt: now}}
	p := buildPipeline(jobs, samplePlan, "", true, matrixOne)
	p.applyTypical(jobs, map[string]gh.TypicalJob{"build": {Median: 2 * time.Minute, Samples: 8, Previous: 4 * time.Minute}}, true)
	p.zoom = zoomBoxes
	a := apiApp(t)
	var x, y int
	out := ansi.Strip(strings.Join(p.render(a, "build", 200, 40, &x, &y, viewKeep, now), "\n"))
	if !strings.Contains(out, "+1m 00s ▲") || strings.Contains(out, "+3m 00s") {
		t.Errorf("the box should compare with the previous run's 4m, not the median:\n%s", out)
	}
}

// squeezed is s with every run of spaces as one, so text around a pill can be
// matched whether its ends are drawn as round caps or spaces.
func squeezed(s string) string {
	s = strings.NewReplacer(pillLeft, " ", pillRight, " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func comparedRun(now time.Time, status string, took time.Duration) gh.Run {
	r := gh.Run{ID: 7, Name: "CI", RunNumber: 128, Status: status, CreatedAt: now.Add(-took), RunStartedAt: now.Add(-took), UpdatedAt: now}
	if status == "completed" {
		r.Conclusion = "success"
	}
	return r
}

func TestARunShowsHowLongItTookAgainstTheMedianOrThePreviousRun(t *testing.T) {
	now := time.Now()
	a := apiApp(t)
	v := newRunView(a, "o/r", comparedRun(now, "completed", 4*time.Minute))
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-4 * time.Minute), CompletedAt: now}}
	v.rebuild()
	if out := ansi.Strip(v.Render(a, 170, 30)); !strings.Contains(out, "took 4m 00s") || strings.Contains(out, " vs ") {
		t.Errorf("with comparing off the run should only say how long it took:\n%s", out)
	}
	load := func() {
		v.typical = map[string]gh.TypicalJob{"build": {Median: 3 * time.Minute, Samples: 8, Previous: 4*time.Minute + 40*time.Second}}
		v.runTypical = gh.TypicalJob{Median: 3 * time.Minute, Samples: 8, Previous: 4*time.Minute + 40*time.Second}
		v.previous = &gh.Run{ID: 6, RunNumber: 127, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-2 * time.Hour)}
		v.typicalLoad = typicalDone
	}
	a.cfg.CompareDurations = config.CompareMedian
	load()
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, squeezed(out), "took 4m 00s ▲ +1m 00s vs median 3m 00s", "median of 8 runs", "the whole run 4m 00s 3m 00s +1m 00s ▲")
	a.cfg.CompareDurations = config.ComparePrevious
	load()
	out = ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, squeezed(out), "took 4m 00s ▼ −40s vs #127 4m 40s", "previous run")
}

func TestARunningRunShowsHowFarItIsAgainstTheMedian(t *testing.T) {
	now := time.Now()
	a := apiApp(t)
	a.cfg.CompareDurations = config.CompareMedian
	v := newRunView(a, "o/r", comparedRun(now, "in_progress", 2*time.Minute))
	v.loaded, v.jobsLoaded = true, true
	v.runTypical = gh.TypicalJob{Median: 4 * time.Minute, Samples: 8}
	v.typical = map[string]gh.TypicalJob{}
	v.typicalLoad = typicalDone
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, squeezed(out), "running 2m 00s ▰▰▰▰▱▱▱▱ 50% of median 4m 00s")
}

func TestTheRunPanelComparesARunWithTheMedianAndThePreviousRun(t *testing.T) {
	now := time.Now()
	earlier := func(id int64, n int, ago, took time.Duration) gh.Run {
		return gh.Run{ID: id, Name: "CI", RunNumber: n, Status: "completed", Conclusion: "success",
			CreatedAt: now.Add(-ago), RunStartedAt: now.Add(-ago), UpdatedAt: now.Add(-ago + took)}
	}
	runs := []gh.Run{
		comparedRun(now, "completed", 4*time.Minute),
		earlier(6, 127, 2*time.Hour, 3*time.Minute+30*time.Second),
		earlier(5, 126, 3*time.Hour, 3*time.Minute),
		earlier(4, 125, 4*time.Hour, 2*time.Minute+30*time.Second),
	}
	a := actionsTabWith(t, runs[0])
	a.st.status["o/r"] = runs
	wantAll(t, squeezed(screen(a)), "DURATION", "this run 4m 00s median 3m 00s of 3", "▲ +1m 00s", "previous #127 3m 30s")

	runs[0] = comparedRun(now, "in_progress", 2*time.Minute)
	a = actionsTabWith(t, runs[0])
	a.st.status["o/r"] = runs
	wantAll(t, squeezed(screen(a)), "DURATION", "so far 2m 00s median 3m 00s of 3", "about 1m 00s left")
}
