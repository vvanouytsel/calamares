package ui

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

var samplePlan = []gh.PlannedJob{
	{ID: "build", Name: "build"},
	{ID: "test", Name: "test", Needs: []string{"build"}},
	{ID: "lint", Name: "lint"},
	{ID: "deploy", Name: "deploy", Needs: []string{"test", "lint", "build"}},
}

func TestPipelineSeparatesJobsIntoColumnsByDependency(t *testing.T) {
	jobs := []gh.Job{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "test (linux)", Status: "in_progress"},
		{ID: 3, Name: "test (windows)", Status: "queued"},
	}
	p := buildPipeline(jobs, samplePlan, "", false, matrixOne)
	layer := func(key string) int { return p.byKey[key].layer }
	if layer("build") != 0 || layer("lint") != 0 || layer("test") != 1 || layer("deploy") != 2 {
		t.Errorf("layers build=%d lint=%d test=%d deploy=%d, want 0 0 1 2", layer("build"), layer("lint"), layer("test"), layer("deploy"))
	}
	if n := p.byKey["test"]; len(n.jobs) != 2 || n.kind != KRun {
		t.Errorf("both matrix legs belong to test and it is running, got %d jobs, kind %v", len(n.jobs), n.kind)
	}
	if p.byKey["deploy"].kind != KNone {
		t.Errorf("a job that has not started is pending, got %v", p.byKey["deploy"].kind)
	}
	if len(p.layers[1]) != 2 {
		t.Errorf("lint→deploy skips a column and needs a stand-in there, while build→deploy is implied by test; got %d nodes in column 1", len(p.layers[1]))
	}
}

func TestPipelineDrawsEveryJobAndTheLinesBetweenThem(t *testing.T) {
	jobs := []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: time.Now().Add(-time.Minute)}}
	p := buildPipeline(jobs, samplePlan, "", false, matrixOne)
	p.zoom = zoomBoxes
	a := apiApp(t)
	var x, y int
	lines := p.render(a, "build", 200, 40, &x, &y, viewKeep, time.Now())
	out := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"build", "test", "lint", "deploy", "╭", "┤"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pipeline lacks %q:\n%s", want, out)
		}
	}
}

func TestPipelineCursorMovesAlongDependencies(t *testing.T) {
	p := buildPipeline(nil, samplePlan, "", false, matrixOne)
	if got := p.step(p.byKey["build"], "right"); got.key != "test" && got.key != "deploy" {
		t.Errorf("right of build should be a job that needs it, got %s", got.key)
	}
	if got := p.step(p.byKey["deploy"], "left"); got.layer >= p.byKey["deploy"].layer {
		t.Errorf("left of deploy should be an earlier column, got %s", got.key)
	}
}

func TestPipelineKeyShowsTheGraphOfARun(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	v.loaded, v.jobsLoaded, v.planLoad, v.plan = true, true, 2, samplePlan
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success"}}
	v.rebuild()
	a.stack = append(a.stack, v)
	v.key(a, "p")
	out := ansi.Strip(a.View())
	if !v.graph || !strings.Contains(out, "Pipeline") || !strings.Contains(out, "deploy") {
		t.Errorf("p should draw the pipeline with the jobs that have not started:\n%s", out)
	}
	v.key(a, "right")
	if v.g.sel == "build" {
		t.Errorf("right should move the cursor on from build")
	}
}

func TestPipelineKeepsAChainOnOneRowAndAvoidsCrossings(t *testing.T) {
	plan := []gh.PlannedJob{
		{ID: "a", Name: "a"}, {ID: "b", Name: "b", Needs: []string{"a"}}, {ID: "c", Name: "c", Needs: []string{"b"}},
		{ID: "x", Name: "x"}, {ID: "y", Name: "y", Needs: []string{"x"}},
	}
	p := buildPipeline(nil, plan, "", false, matrixOne)
	if p.byKey["a"].row != p.byKey["b"].row || p.byKey["b"].row != p.byKey["c"].row {
		t.Errorf("a chain should run straight, rows a=%d b=%d c=%d", p.byKey["a"].row, p.byKey["b"].row, p.byKey["c"].row)
	}
	if got := p.totalCrossings(); got != 0 {
		t.Errorf("two independent chains cross %d times, want 0", got)
	}
}

func TestPipelineZoomAndFullScreenKeys(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	v.loaded, v.jobsLoaded, v.planLoad, v.plan = true, true, 2, samplePlan
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "in_progress", Steps: []gh.Step{{Name: "compile", Status: "in_progress"}}}}
	v.rebuild()
	a.stack = append(a.stack, v)
	v.key(a, "p")
	_ = a.View()
	v.g.zoom, v.g.zoomNow = zoomBoxes, zoomBoxes
	v.key(a, "=")
	if v.g.zoom != zoomDetail || !strings.Contains(ansi.Strip(a.View()), "compile") {
		t.Errorf("= should show the step each job is on:\n%s", ansi.Strip(a.View()))
	}
	v.key(a, "-")
	v.key(a, "-")
	if v.g.zoom != zoomCompact {
		t.Errorf("- twice should be compact, got %d", v.g.zoom)
	}
	v.key(a, "Z")
	if out := ansi.Strip(a.View()); !v.g.full || strings.Contains(out, "Steps") {
		t.Errorf("Z should hide the details below the pipeline:\n%s", out)
	}
}

func graphApp(t *testing.T) (*App, *runView) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	v.loaded, v.jobsLoaded, v.planLoad, v.plan = true, true, 2, samplePlan
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success"}}
	v.rebuild()
	a.stack = append(a.stack, v)
	v.key(a, "p")
	_ = a.View()
	return a, v
}

func TestInThePipelineTheArrowsMoveAndLOpensTheLogs(t *testing.T) {
	a, v := graphApp(t)
	v.g.sel = "build"
	v.key(a, "right")
	if v.g.sel == "build" {
		t.Errorf("right should move the cursor to the right")
	}
	v.g.sel = "build"
	v.key(a, "l")
	if _, isLog := a.top().(*logView); !isLog {
		t.Errorf("l means logs everywhere, in the pipeline too; top is %T", a.top())
	}
}

func TestThePipelineZoomsToFitByDefault(t *testing.T) {
	_, v := graphApp(t)
	if v.g.zoom != zoomFit || v.g.zoomNow != zoomDetail {
		t.Errorf("a small pipeline on a wide screen should fit at the detailed zoom, got %d (%d)", v.g.zoomNow, v.g.zoom)
	}
}

const ciYAML = `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: make
  deploy:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - run: ./deploy.sh
`

func sourceApp(t *testing.T) (*App, *runView) {
	withGitHub(t, map[string]string{
		"/repos/o/r/contents/.github/workflows/ci.yml": `{"encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte(ciYAML)) + `"}`,
	})
	a := apiApp(t)
	run := ghRun(7, "completed")
	run.Path, run.HeadSHA = ".github/workflows/ci.yml", "0123456789abcdef0123456789abcdef01234567"
	v := newRunView(a, "o/r", run)
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success"}, {ID: 2, Name: "deploy", Status: "completed", Conclusion: "success"}}
	v.rebuild()
	a.stack = append(a.stack, v)
	return a, v
}

func TestCShowsTheYAMLThatDefinesTheSelectedJob(t *testing.T) {
	a, v := sourceApp(t)
	v.cursor = 0
	for i, it := range v.items {
		if it.kind == tJob && v.jobs[it.job].Name == "deploy" {
			v.cursor = i
		}
	}
	drive(a, v.key(a, "c"), 5*time.Second)
	pv, ok := a.top().(*pagerView)
	if !ok {
		t.Fatalf("c should open the job's YAML, top is %T", a.top())
	}
	out := ansi.Strip(a.View())
	if !strings.Contains(out, "./deploy.sh") || strings.Contains(out, "make") || !strings.Contains(out, "@ 0123456") {
		t.Errorf("only the deploy job should show, from the file at the run's commit:\n%s", out)
	}
	pv.Update(a, runes("v"))
	if out := ansi.Strip(a.View()); !strings.Contains(out, "make") || !strings.Contains(out, "▌") {
		t.Errorf("v should show the whole file with the job marked:\n%s", out)
	}
}

func TestCWithoutAJobShowsTheWholeWorkflowOfTheRun(t *testing.T) {
	a, v := sourceApp(t)
	v.jobs = nil
	v.rebuild()
	drive(a, v.key(a, "c"), 5*time.Second)
	if out := ansi.Strip(a.View()); !strings.Contains(out, "name: CI") || !strings.Contains(out, "./deploy.sh") {
		t.Errorf("c without a job under the cursor should show the whole workflow file:\n%s", out)
	}
}

// releasePlan is shaped like a full release pipeline: a version job nearly every
// job needs directly, a long chain of stages, a matrix whose name holds an
// expression, and a called workflow that was skipped as a whole.
var releasePlan = []gh.PlannedJob{
	{ID: "version", Name: "ci / Version"},
	{ID: "lint", Name: "ci / Checks / Lint"},
	{ID: "build", Name: "ci / Build", Needs: []string{"version"}},
	{ID: "image", Name: "ci / Image", Needs: []string{"version", "build"}},
	{ID: "chart", Name: "ci / Chart", Needs: []string{"version", "image", "lint"}},
	{ID: "deploy", Name: "ci / Deploy ${{ matrix.name }}", Needs: []string{"version", "chart"}},
	{ID: "smoke", Name: "ci / Smoke", Needs: []string{"version", "deploy"}},
	{ID: "stage/prepare", Name: "ci / Staging tests / Prepare", Needs: []string{"version", "smoke"}},
	{ID: "stage/run", Name: "ci / Staging tests / Run", Needs: []string{"stage/prepare"}},
	{ID: "release", Name: "ci / Release", Needs: []string{"version", "stage/run"}},
	{ID: "notify", Name: "ci / Notify", Needs: []string{"release", "image"}},
}

func releaseJobs() []gh.Job {
	ok := func(id int64, name string) gh.Job {
		return gh.Job{ID: id, Name: name, Status: "completed", Conclusion: "success"}
	}
	return []gh.Job{
		ok(1, "ci / Version"), ok(2, "ci / Checks / Lint"), ok(3, "ci / Build"), ok(4, "ci / Image"), ok(5, "ci / Chart"),
		ok(6, "ci / Deploy api"), ok(7, "ci / Deploy web"), ok(8, "ci / Deploy worker"), ok(9, "ci / Smoke"),
		{ID: 10, Name: "ci / Staging tests", Status: "completed", Conclusion: "skipped"},
		{ID: 11, Name: "ci / Release", Status: "completed", Conclusion: "skipped"},
		{ID: 12, Name: "ci / Notify", Status: "completed", Conclusion: "success"},
	}
}

func TestAJobEveryoneNeedsGetsOneLineNotOnePerJob(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixOne)
	if got := p.byKey["version"].succs; len(got) != 1 {
		t.Errorf("version should only lead to build, the rest is implied; it leads to %v", got)
	}
	if got := p.totalCrossings(); got != 0 {
		t.Errorf("the pipeline crosses %d lines, want 0", got)
	}
	p.zoom = zoomCompact
	if g := p.geometry(); g.h > 5 {
		t.Errorf("the pipeline is %d lines tall at compact zoom; long lines should not each take a row", g.h)
	}
}

func TestASkippedCalledWorkflowMarksItsJobsSkippedInsteadOfStandingAlone(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixOne)
	for _, key := range []string{"stage/prepare", "stage/run"} {
		if n := p.byKey[key]; n.kind != KSkip || len(n.jobs) != 1 {
			t.Errorf("%s should hold the skipped call and be skipped, got kind %v with %d jobs", key, n.kind, len(n.jobs))
		}
	}
	for key := range p.byKey {
		if strings.HasPrefix(key, "job:") {
			t.Errorf("%s stands alone, outside the workflow's jobs", key)
		}
	}
}

func TestAMatrixWithAnExpressionInItsNameShowsItAsAnEllipsis(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixOne)
	if got := p.displayLabel(p.byKey["deploy"]); got != "Deploy … ×3" {
		t.Errorf("label = %q, want %q", got, "Deploy … ×3")
	}
}

func TestThePipelineKeepsTheJobTheListWasOnOnceTheWorkflowFileIsRead(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "completed"))
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "deploy", Status: "completed", Conclusion: "failure"},
	}
	v.rebuild()
	for i, it := range v.items {
		if it.kind == tJob && v.jobs[it.job].Name == "deploy" {
			v.cursor = i
		}
	}
	a.stack = append(a.stack, v)
	v.key(a, "p")
	_ = a.View()
	v.Update(a, planMsg{id: v.id, plan: samplePlan})
	_ = a.View()
	if v.g.sel != "deploy" {
		t.Errorf("the pipeline should stay on deploy, the job the list was on; it is on %q", v.g.sel)
	}
}

func TestLinesToTheSameJobMeetInsteadOfCrossing(t *testing.T) {
	plan := []gh.PlannedJob{
		{ID: "version", Name: "version"},
		{ID: "lint1", Name: "lint1"}, {ID: "lint2", Name: "lint2"}, {ID: "lint3", Name: "lint3"}, {ID: "lint4", Name: "lint4"},
		{ID: "image", Name: "image", Needs: []string{"version"}},
		{ID: "cleanup", Name: "cleanup", Needs: []string{"image"}},
		{ID: "publish", Name: "publish", Needs: []string{"image", "lint1", "lint2", "lint3", "lint4"}},
	}
	p := buildPipeline(nil, plan, "", false, matrixOne)
	for _, zoom := range []int{zoomCompact, zoomBoxes, zoomDetail} {
		p.zoom = zoom
		g := p.geometry()
		for l := range p.layers {
			if c := trunkCost(p.trunks(l, g), g); c != 0 {
				t.Errorf("at zoom %d the lines after column %d cross %d times, want 0", zoom, l, c)
			}
		}
		intoPublish := 0
		for _, tr := range p.trunks(1, g) {
			if tr.feeds(p.byKey["publish"]) {
				intoPublish++
			}
			if len(tr.sources) == 1 && tr.sources[0].key == "image" && len(tr.targets) != 2 {
				t.Errorf("at zoom %d the image feeds cleanup and publish and should be one line branching to both", zoom)
			}
		}
		if intoPublish != 2 {
			t.Errorf("at zoom %d publish should be reached by the image's line and the lint jobs' joined line, got %d lines", zoom, intoPublish)
		}
		for _, tr := range p.trunks(0, g) {
			if tr.targets[0].key == "~>publish@1" && len(tr.sources) != 4 {
				t.Errorf("at zoom %d the four lint jobs should join one line straight away, got %d", zoom, len(tr.sources))
			}
		}
	}
}

func TestLinesThatOnlyCrossAreDrawnWithAGapNotAsAJoin(t *testing.T) {
	across := []part{{line: 1, dirs: dirE | dirW}, {line: 2, dirs: dirE | dirW}, {line: 3, dirs: dirN | dirS}}
	if r, _ := lineGlyph(across); r != '─' {
		t.Errorf("two lines across passing a line up and down drew %q, want a gap in the vertical line", r)
	}
	join := []part{{line: 1, dirs: dirW}, {line: 1, dirs: dirN | dirS}}
	if r, _ := lineGlyph(join); r != '┤' {
		t.Errorf("a line ending on a vertical line drew %q, want it to join", r)
	}
}

func TestAJobFanningOutToJobsOnlyItFeedsBranchesFromOneLine(t *testing.T) {
	plan := []gh.PlannedJob{
		{ID: "v", Name: "v"},
		{ID: "a", Name: "a", Needs: []string{"v"}}, {ID: "b", Name: "b", Needs: []string{"v"}}, {ID: "c", Name: "c", Needs: []string{"v"}},
	}
	p := buildPipeline(nil, plan, "", false, matrixOne)
	p.zoom = zoomBoxes
	g := p.geometry()
	for _, tr := range p.trunks(0, g) {
		if tr.exits["v"] != 0 {
			t.Errorf("a line to %s leaves from line %d of v's box; lines to jobs only v feeds should all branch from its first line", tr.targets[0].key, tr.exits["v"])
		}
	}
}

func TestEachLegOfAMatrixIsAJobOfItsOwn(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixLegs)
	var legs []*pnode
	for _, n := range p.real() {
		if plannedKey(n.key) == "deploy" {
			legs = append(legs, n)
		}
	}
	if len(legs) != 3 {
		t.Fatalf("deploy ran 3 times and should show 3 jobs, got %d", len(legs))
	}
	for _, n := range legs {
		if len(n.preds) != 1 || n.preds[0] != "chart" {
			t.Errorf("leg %s should wait for chart, as the matrix does; waits for %v", n.key, n.preds)
		}
	}
	if smoke := p.byKey["smoke"]; len(smoke.preds) != 3 {
		t.Errorf("smoke waits for the matrix, so it should wait for every leg; waits for %v", smoke.preds)
	}
	if _, name := p.names(legs[0]); name != "api" {
		t.Errorf("a leg should be named by what sets it apart, got %q", name)
	}
	if collapsed := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixOne); len(collapsed.byKey["deploy"].jobs) != 3 {
		t.Errorf("collapsed, the matrix should be one job holding its 3 legs")
	}
}

func TestLegsAreNamedByWhatSetsThemApart(t *testing.T) {
	got := differences([]string{
		"Deploying search-api(1.57.3) to staging",
		"Deploying webshop(16.2.0) to staging",
	})
	if got[0] != "search-api(1.57.3)" || got[1] != "webshop(16.2.0)" {
		t.Errorf("got %q", got)
	}
	got = differences([]string{"Build image (Dockerfile, true)", "Build image (e2e.Dockerfile, e2e, true)"})
	if got[0] != "Dockerfile" || got[1] != "e2e.Dockerfile, e2e" {
		t.Errorf("got %q", got)
	}
	got = differences([]string{"Test (windows-latest)", "Test (ubuntu-latest)", "Test (macos-latest)"})
	if got[0] != "windows-latest" || got[2] != "macos-latest" {
		t.Errorf("legs that share the end of their value lose the bracket, got %q", got)
	}
}

func TestTheDefaultZoomNeverDropsTheBoxes(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixLegs)
	if z := p.bestZoom(40, 10); z != zoomBoxes {
		t.Errorf("a pipeline too big for the screen should still be drawn with boxes, got zoom %d", z)
	}
}

// legsPlan is shaped like the start of a real run: two jobs both feeding a
// two-leg matrix, and four checks only the publish job, far on, waits for.
var legsPlan = []gh.PlannedJob{
	{ID: "parse", Name: "Parse"}, {ID: "version", Name: "Version"},
	{ID: "check1", Name: "Check 1"}, {ID: "check2", Name: "Check 2"}, {ID: "check3", Name: "Check 3"}, {ID: "check4", Name: "Check 4"},
	{ID: "build", Name: "Build", Needs: []string{"version"}},
	{ID: "image", Name: "Image", Needs: []string{"build", "parse"}},
	{ID: "cleanup", Name: "Cleanup", Needs: []string{"image"}},
	{ID: "publish", Name: "Publish", Needs: []string{"image", "check1", "check2", "check3", "check4"}},
}

func legsJobs() []gh.Job {
	return []gh.Job{
		{ID: 1, Name: "Image (Dockerfile)", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "Image (test.Dockerfile)", Status: "completed", Conclusion: "success"},
	}
}

func TestJobsFeedingTheSameLegsShareOneLine(t *testing.T) {
	p := buildPipeline(legsJobs(), legsPlan, "", false, matrixAuto)
	for _, zoom := range []int{zoomCompact, zoomBoxes, zoomDetail} {
		p.zoom = zoom
		g := p.geometry()
		into := 0
		for l := range p.layers {
			for _, tr := range p.trunks(l, g) {
				for _, t := range tr.targets {
					if plannedKey(t.key) == "image" || t.group == "image" {
						into++
						break
					}
				}
			}
		}
		if into != 1 {
			t.Errorf("at zoom %d, parse and build both feed both legs and should share one line into them, got %d", zoom, into)
		}
	}
}

func TestLongLinesIntoTheSameJobJoinEarly(t *testing.T) {
	p := buildPipeline(legsJobs(), legsPlan, "", false, matrixAuto)
	standIns := 0
	for _, col := range p.layers {
		for _, n := range col {
			if strings.Contains(n.key, "check") || strings.HasPrefix(n.key, "~>publish") {
				if n.virtual {
					standIns++
				}
			}
		}
	}
	if pub := p.byKey["publish"]; standIns != 2 {
		t.Errorf("the four checks' lines to publish should join into one line across the 2 columns between, got %d stand-ins (publish waits for %v)", standIns, pub.preds)
	}
}

func TestABigMatrixStaysOneJobUnlessAsked(t *testing.T) {
	var jobs []gh.Job
	for i := range maxLegs + 1 {
		jobs = append(jobs, gh.Job{ID: int64(i + 1), Name: fmt.Sprintf("ci / Deploy app%d", i), Status: "completed", Conclusion: "success"})
	}
	if p := buildPipeline(jobs, releasePlan, "ci / ", true, matrixAuto); p.byKey["deploy"] == nil {
		t.Errorf("a matrix of %d legs should show as one job by default", maxLegs+1)
	}
	if p := buildPipeline(jobs, releasePlan, "ci / ", true, matrixLegs); p.byKey["deploy"] != nil {
		t.Errorf("asked for every leg, a big matrix should show each one")
	}
}

func TestNoRowIsLeftEmptyAcrossTheWholePipeline(t *testing.T) {
	p := buildPipeline(legsJobs(), legsPlan, "", false, matrixAuto)
	used := map[int]bool{}
	top := 0
	for _, col := range p.layers {
		for _, n := range col {
			used[n.row] = true
			top = max(top, n.row)
		}
	}
	for r := 0; r <= top; r++ {
		if !used[r] {
			t.Errorf("row %d has no job or line in any column", r)
		}
	}
}

func TestUpAndDownMoveThroughEveryLegOfAMatrix(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixLegs)
	var legs []*pnode
	for _, n := range p.layers[p.byKey["chart"].layer+1] {
		if !n.virtual {
			legs = append(legs, n)
		}
	}
	if len(legs) != 3 {
		t.Fatalf("want the 3 deploy legs in one column, got %d", len(legs))
	}
	bottom := legs[len(legs)-1]
	if got := p.step(p.step(bottom, "up"), "up"); got != legs[0] {
		t.Errorf("up twice from the last leg should reach the first, got %s", got.key)
	}
	if got := p.step(legs[0], "down"); got != legs[1] {
		t.Errorf("down from the first leg should reach the second, got %s", got.key)
	}
}

func TestAMatrixIsFramedAndLinesMeetTheFrameNotEachLeg(t *testing.T) {
	p := buildPipeline(releaseJobs(), releasePlan, "ci / ", true, matrixLegs)
	p.zoom = zoomBoxes
	a := apiApp(t)
	g := p.geometry()
	var x, y int
	lines := p.render(a, "", g.w, g.h, &x, &y, viewKeep, time.Now())
	out := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(out, "⊞ Deploy …") || !strings.Contains(out, "3 legs") {
		t.Errorf("the legs should be framed under the matrix's name and leg count:\n%s", out)
	}
	for l := range p.layers {
		for _, tr := range p.trunks(l, g) {
			for _, n := range append(append([]*pnode(nil), tr.sources...), tr.targets...) {
				if n.group != "" && !n.port {
					t.Errorf("a line leads to leg %s; lines should meet the matrix's frame", n.key)
				}
			}
		}
	}
}

// fanPlan is a deploy fanning out to three jobs: resolve feeds web, and
// feature and api join one line on to lock, past web.
var fanPlan = []gh.PlannedJob{
	{ID: "deploy", Name: "deploy"},
	{ID: "resolve", Name: "resolve", Needs: []string{"deploy"}},
	{ID: "feature", Name: "feature", Needs: []string{"deploy"}},
	{ID: "api", Name: "api", Needs: []string{"deploy"}},
	{ID: "web", Name: "web", Needs: []string{"resolve"}},
	{ID: "lock", Name: "lock", Needs: []string{"web", "feature", "api"}},
}

func fanJobs(web string) []gh.Job {
	done := func(id int64, name string) gh.Job {
		return gh.Job{ID: id, Name: name, Status: "completed", Conclusion: "success"}
	}
	return []gh.Job{done(1, "deploy"), done(2, "resolve"), done(3, "feature"), done(4, "api"), {ID: 5, Name: "web", Status: web}}
}

func lineHot(c *canvas, x, y int) bool {
	_, hot := lineGlyph(c.parts[y][x])
	return len(c.parts[y][x]) > 0 && hot
}

func TestASelectedJobLightsOnlyTheLinesOfItsOwnChain(t *testing.T) {
	p := buildPipeline(fanJobs("completed"), fanPlan, "", false, matrixOne)
	p.zoom = zoomBoxes
	g := p.geometry()
	c := p.draw(g, "api", 0, time.Now())
	gutter := func(l int) (int, int) { return g.colX[l] + g.colW[l], g.colX[l+1] - 1 }
	resolve, feature, api := g.lineY(p.byKey["resolve"]), g.lineY(p.byKey["feature"]), g.lineY(p.byKey["api"])
	x0, x1 := gutter(0)
	for y := 0; y < feature; y++ {
		for x := x0; x <= x1; x++ {
			if lineHot(c, x, y) {
				t.Errorf("the line from deploy is lit at (%d,%d), on its way up to resolve (row %d), which is not on api's chain", x, y, resolve)
			}
		}
	}
	x0, x1 = gutter(1)
	for y := feature; y < api; y++ {
		for x := x0; x <= x1; x++ {
			if lineHot(c, x, y) {
				t.Errorf("the line from feature is lit at (%d,%d); only api's line on to lock is on api's chain", x, y)
			}
		}
	}
	lit := 0
	for x := x0; x <= x1; x++ {
		if lineHot(c, x, api) {
			lit++
		}
	}
	if lit == 0 {
		t.Errorf("api's own line on to lock should be lit")
	}
}

// flowFirstLit is when each cell of a line into a running job is first lit
// during the first few seconds, and the most cells lit at once.
func flowFirstLit(c *canvas) (first map[[2]int]int64, most int) {
	first = map[[2]int]int64{}
	for ms := int64(0); ms < 5000; ms += 5 {
		lights := flowLights(c, ms)
		most = max(most, len(lights))
		for at, k := range lights {
			if _, seen := first[at]; !seen && k > 0 {
				first[at] = ms
			}
		}
	}
	return first, most
}

func TestTheLightIntoARunningJobRunsAlongTheLineAsOneComet(t *testing.T) {
	for _, zoom := range []int{zoomCompact, zoomBoxes} {
		p := buildPipeline(fanJobs("in_progress"), fanPlan, "", false, matrixOne)
		p.zoom = zoom
		g := p.geometry()
		c := p.draw(g, "", 0, time.Now())
		first, most := flowFirstLit(c)
		if most > flowTail {
			t.Errorf("at zoom %d %d cells are lit at once; one comet with a tail of %d should run along the line", zoom, most, flowTail)
		}
		resolve, web := g.lineY(p.byKey["resolve"]), g.lineY(p.byKey["web"])
		_, _, x1, _ := g.rect(p.byKey["resolve"])
		start := x1 + 1
		if zoom == zoomCompact {
			start = x1 // the line runs on from the name, inside the job's column
		}
		if _, ok := first[[2]int{start, resolve}]; !ok {
			t.Errorf("at zoom %d the light should start where resolve's line does, at x=%d", zoom, start)
		}
		var bend []int64
		for y := resolve + 1; y < web; y++ {
			for x := range c.w {
				if at, ok := first[[2]int{x, y}]; ok {
					bend = append(bend, at)
				}
			}
		}
		if len(bend) == 0 {
			t.Fatalf("at zoom %d the light should run down the bend from resolve to web", zoom)
		}
		for i := 1; i < len(bend); i++ {
			if bend[i] <= bend[i-1] {
				t.Errorf("at zoom %d the light should run down the bend one cell after another, got first lit at %v", zoom, bend)
				break
			}
		}
		for at, ms := range first {
			for other, later := range first {
				if other[1] == at[1] && other[0] > at[0] && later < ms {
					t.Errorf("at zoom %d on row %d the light reaches x=%d before x=%d; it should run towards web", zoom, at[1], other[0], at[0])
				}
			}
		}
	}
}

func TestTheLightRunsAlongALongLineIntoARunningJobFromItsStart(t *testing.T) {
	plan := []gh.PlannedJob{
		{ID: "lint", Name: "lint"}, {ID: "build", Name: "build"},
		{ID: "test", Name: "test", Needs: []string{"build"}},
		{ID: "deploy", Name: "deploy", Needs: []string{"test", "lint"}},
	}
	jobs := []gh.Job{
		{ID: 1, Name: "lint", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "build", Status: "completed", Conclusion: "success"},
		{ID: 3, Name: "test", Status: "completed", Conclusion: "success"},
		{ID: 4, Name: "deploy", Status: "in_progress"},
	}
	p := buildPipeline(jobs, plan, "", false, matrixOne)
	p.zoom = zoomBoxes
	g := p.geometry()
	c := p.draw(g, "", 0, time.Now())
	first, _ := flowFirstLit(c)
	standIn := p.byKey["~lint@1"]
	if standIn == nil {
		t.Fatalf("lint's line to deploy should pass column 1 as a stand-in")
	}
	y := g.lineY(standIn)
	for x := g.colX[1]; x < g.colX[1]+g.colW[1]; x++ {
		if _, ok := first[[2]int{x, y}]; !ok {
			t.Errorf("lint's line past test's column, at (%d,%d), should carry the light into deploy", x, y)
		}
	}
	_, _, lx1, _ := g.rect(p.byKey["lint"])
	if _, ok := first[[2]int{lx1 + 1, g.lineY(p.byKey["lint"])}]; !ok {
		t.Errorf("the light should start right where lint's line leaves its box")
	}
}
