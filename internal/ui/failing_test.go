package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func mainRun(id, workflow int64, conclusion string, created time.Time, called ...string) gh.Run {
	r := callingRun(id, conclusion, created, called...)
	r.WorkflowID, r.HeadBranch, r.HeadSHA = workflow, "main", "sha"+string(rune('a'+id))
	r.Actor = gh.User{Login: "sam"}
	return r
}

// failingApp holds three repos whose default branch is main: api and web fail
// the same way in the docker workflow they both call, billing fails a test.
func failingApp(t *testing.T) *App {
	now := time.Now()
	a := accessApp(t, "WRITE", "gho_x")
	a.repos = []gh.Repo{
		{FullName: "acme/api", HasActions: true, PushedAt: now, DefaultBranch: "main"},
		{FullName: "acme/web", HasActions: true, PushedAt: now, DefaultBranch: "main"},
		{FullName: "acme/billing", HasActions: true, PushedAt: now, DefaultBranch: "main"},
	}
	docker := "acme/ci/.github/workflows/docker.yml@v9"
	a.st.status = map[string][]gh.Run{
		"acme/api":     {mainRun(1, 10, "failure", now.Add(-time.Hour), docker)},
		"acme/web":     {mainRun(2, 20, "failure", now.Add(-2*time.Hour), docker)},
		"acme/billing": {mainRun(3, 30, "failure", now.Add(-10*time.Minute)), mainRun(4, 31, "success", now.Add(-time.Hour))},
	}
	a.st.causes = map[string]runCause{
		causeID("acme/api", a.st.status["acme/api"][0]):         {Job: "build", Step: "Build image", Text: "toomanyrequests: rate limit for docker.io reached (pull 4f2a9c1d)"},
		causeID("acme/web", a.st.status["acme/web"][0]):         {Job: "build", Step: "Build image", Text: "toomanyrequests: rate limit for docker.io reached (pull 77ab31e0)"},
		causeID("acme/billing", a.st.status["acme/billing"][0]): {Job: "test", Step: "Unit tests", Text: "FAIL src/basket/delivery.test.ts"},
	}
	return a
}

func TestTheSameFailureInTwoReposHasTheSameKey(t *testing.T) {
	one := runCause{Text: "Error: ENOENT /home/runner/work/api/api/dist/index.js at 2026-10-12T10:00:01Z (id 3f9a2b7c11)"}
	two := runCause{Text: "Error: ENOENT /home/runner/work/web/web/dist/index.js at 2026-10-13T08:12:44Z (id 8812ab09ff)"}
	if one.key() != two.key() {
		t.Errorf("keys differ:\n%s\n%s", one.key(), two.key())
	}
	exitTests := runCause{Text: "Process completed with exit code 1.", Step: "Unit tests"}
	exitLint := runCause{Text: "Process completed with exit code 2.", Step: "Lint"}
	if exitTests.key() == exitLint.key() {
		t.Errorf("a bare exit code in two different steps is not the same cause: %q", exitTests.key())
	}
}

func TestLatestPerWorkflowSkipsOtherBranchesAndCancelledRuns(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	cancelled := mainRun(5, 30, "cancelled", now)
	branch := mainRun(6, 30, "success", now)
	branch.HeadBranch = "feature"
	a.st.status["acme/billing"] = append([]gh.Run{cancelled, branch}, a.st.status["acme/billing"]...)
	got := a.latestPerWorkflow("acme/billing")
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 4 {
		t.Errorf("latest per workflow = %v, want run 3 (failed) and run 4", ids(got))
	}
}

func ids(runs []gh.Run) []int64 {
	var out []int64
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}

func TestFailingGroupsRunsOfManyReposByCause(t *testing.T) {
	a := failingApp(t)
	v, out := groupedFailing(a)
	wantAll(t, out, "toomanyrequests", "Build image", "api  CI #1", "web  CI #2", "FAIL src/basket/delivery.test.ts", "3 failing", "2 same cause", "1 via your workflows")
	if n := strings.Count(out, "toomanyrequests"); n != 2 {
		t.Errorf("the rate limit should be one row (and the panel), seen %d times:\n%s", n, out)
	}
	if v.failing.groups[0].repos() != 2 {
		t.Errorf("the cause in most repos comes first")
	}
}

func TestThePanelOfACauseNamesWhatItsRunsHaveInCommon(t *testing.T) {
	a := failingApp(t)
	_, out := groupedFailing(a)
	wantAll(t, out, "Shared cause", "docker.yml", "v9", "Likely in your workflow")
}

func TestRAsksToRerunEveryRunOfACause(t *testing.T) {
	a := failingApp(t)
	v, _ := groupedFailing(a)
	cmd := v.Update(a, runes("R"))
	drive(a, cmd, time.Second)
	if a.modal == nil || !strings.Contains(ansi.Strip(a.View()), "Rerun the failed jobs of 2 runs?") {
		t.Errorf("R on a cause should ask to rerun its 2 runs:\n%s", ansi.Strip(a.View()))
	}
}

func TestTheActionsTabOpensOnRunningEvenWhenSomethingFails(t *testing.T) {
	a := failingApp(t)
	v := newActivityView(a)
	v.sync(a)
	if v.filter != viewRunning {
		t.Errorf("view = %d, want Running", v.filter)
	}
	if got := v.stripItems(a, v.recent, time.Now())[1]; got.Label != "Failing" || got.Count == "0" || got.St == nil {
		t.Errorf("Failing should come second and stand out with its count, got %+v", got)
	}
}

func TestCausesAreNotReadUntilTheFailingViewIsShown(t *testing.T) {
	a := failingApp(t)
	a.st.causes = map[string]runCause{}
	if cmd := a.st.loadCauses(a, failingRuns(a, []string{"acme/api"})); cmd != nil {
		t.Error("causes were read before the Failing view was shown")
	}
	v := newActivityView(a)
	v.setFilter(viewFailing)
	v.failing.load(a, false)
	if !a.st.wantCauses || len(a.st.causeBusy) == 0 {
		t.Error("showing the Failing view should read the causes")
	}
}

func TestANewRunShowsAtOnceInTheRunningViewWhereverTheCursorIs(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "in_progress", now.Add(-2*time.Hour)), activityRun(2, "in_progress", now.Add(-time.Hour)))
	v.setFilter(viewRunning)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.t.SetCursor(1)
	a.st.status["o/r"] = append(a.st.status["o/r"], activityRun(3, "in_progress", now.Add(-time.Minute)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 3 || v.held != 0 {
		t.Errorf("the new running run should show at once: %d rows, %d held", v.t.Len(), v.held)
	}
}

func TestRefreshingShowsTheRunsHeldBack(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-2*time.Hour)), activityRun(2, "completed", now.Add(-time.Hour)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.t.SetCursor(1)
	a.st.status["o/r"] = append(a.st.status["o/r"], activityRun(3, "completed", now.Add(-time.Minute)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.Refresh(a, true)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 3 || v.held != 0 {
		t.Errorf("ctrl+r should show the held run: %d rows, %d held", v.t.Len(), v.held)
	}
}

func TestOnlyMineShowsARunYouRanAgain(t *testing.T) {
	now := time.Now()
	run := activityRun(1, "in_progress", now)
	run.Actor, run.TriggeringActor = gh.User{Login: "alice"}, gh.User{Login: "sam"}
	a, v := activityApp(run)
	a.me = "sam"
	v.mine = true
	v.setFilter(viewRunning)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 1 {
		t.Errorf("a run of alice that sam ran again is one of sam's: %d rows", v.t.Len())
	}
}

func TestAContainerHookFailureIsNotACauseOfItsOwn(t *testing.T) {
	hook := "Executing the custom container implementation failed. Please contact your self hosted runner administrator."
	if got := annotationCause([]gh.Annotation{{AnnotationLevel: "failure", Message: hook}}); got != "" {
		t.Errorf("the hook message says nothing about why, got %q", got)
	}
	merge := runCause{Step: "Enable auto merge", Text: hook}
	build := runCause{Step: "Build image", Text: hook}
	if merge.key() == build.key() {
		t.Errorf("without a better line, two different steps are two causes: %q", merge.key())
	}
	g := &failGroup{known: true, cause: merge, runs: []repoRun{{"acme/api", gh.Run{}}}}
	if got := g.causeText(); !strings.HasPrefix(got, "Enable auto merge failed") {
		t.Errorf("the row should name the step that failed, got %q", got)
	}
}

func TestAGroupNamesAStepOnlyWhenItsRunsShareIt(t *testing.T) {
	a := failingApp(t)
	text := "2 tests failed, see the report above."
	a.st.causes[causeID("acme/api", a.st.status["acme/api"][0])] = runCause{Job: "e2e", Step: "Run browser tests", Text: text}
	a.st.causes[causeID("acme/web", a.st.status["acme/web"][0])] = runCause{Job: "e2e", Step: "Run smoke tests", Text: text}
	_, out := groupedFailing(a)
	wantAll(t, out, text, "2 steps", "Run browser tests", "Run smoke tests", "2 different steps")
}

func TestTheLineBeforeTheContainerHooksErrorsIsTheCause(t *testing.T) {
	step := gh.Step{Name: "Run tests", Number: 1, Status: "completed", Conclusion: "failure"}
	raw := "2026-10-12T10:00:00.0000000Z ##[group]Run make e2e\n" +
		"2026-10-12T10:00:01.0000000Z + echo '2 tests failed, see the report above.'\n" +
		"2026-10-12T10:00:01.0000000Z + exit 1\n" +
		"2026-10-12T10:00:01.0000000Z 2 tests failed, see the report above.\n" +
		"2026-10-12T10:00:02.0000000Z ##[error]Error: failed to run script step: command terminated with non-zero exit code: error executing command [sh -e /__w/_temp/9f76df90.sh], exit code 1\n" +
		"2026-10-12T10:00:02.0000000Z ##[error]Process completed with exit code 1.\n" +
		"2026-10-12T10:00:02.0000000Z ##[error]Executing the custom container implementation failed. Please contact your self hosted runner administrator.\n"
	lines := gh.ParseLog(raw)
	job := gh.Job{Steps: []gh.Step{step}}
	step.StartedAt = lines[0].Time
	job.Steps[0].StartedAt = lines[0].Time
	got := logCause(newJobLog(gh.SplitByStep(lines, job.Steps), len(lines)), job)
	if got != "2 tests failed, see the report above." {
		t.Errorf("cause = %q", got)
	}
}

// stepLogCause is the cause logCause reads from raw, the log of a job whose
// only step failed.
func stepLogCause(raw string) string {
	lines := gh.ParseLog(raw)
	job := gh.Job{Steps: []gh.Step{{Name: "Install dependencies", Number: 1, Status: "completed", Conclusion: "failure", StartedAt: lines[0].Time}}}
	return logCause(newJobLog(gh.SplitByStep(lines, job.Steps), len(lines)), job)
}

func TestOutputThatDoesNotReadLikeAFailureIsNoCause(t *testing.T) {
	for name, raw := range map[string]string{
		"download progress": "2026-10-12T10:00:00.0000000Z ##[group]Run apt-get install -y jq\n" +
			"2026-10-12T10:00:00.0000000Z apt-get install -y jq\n" +
			"2026-10-12T10:00:00.0000000Z ##[endgroup]\n" +
			"2026-10-12T10:00:01.0000000Z Fetched 3321 kB in 0s (51.8 MB/s)\n" +
			"2026-10-12T10:00:02.0000000Z ##[error]Process completed with exit code 100.\n",
		"runner marker": "2026-10-12T10:00:00.0000000Z ##[group]Run ./scan.sh\n" +
			"2026-10-12T10:00:00.0000000Z ##[endgroup]\n" +
			"2026-10-12T10:00:01.0000000Z ##[end-action id=__self.__run_2;outcome=success;conclusion=success;duration_ms=3310]\n" +
			"2026-10-12T10:00:02.0000000Z ##[error]Process completed with exit code 1.\n",
		"its own environment": "2026-10-12T10:00:00.0000000Z ##[group]Run gh pr merge --auto\n" +
			"2026-10-12T10:00:00.0000000Z gh pr merge --auto\n" +
			"2026-10-12T10:00:00.0000000Z shell: /usr/bin/bash -e {0}\n" +
			"2026-10-12T10:00:00.0000000Z env:\n" +
			"2026-10-12T10:00:00.0000000Z   GH_TOKEN: ***\n" +
			"2026-10-12T10:00:00.0000000Z ##[endgroup]\n" +
			"2026-10-12T10:00:02.0000000Z ##[error]Process completed with exit code 1.\n",
	} {
		if got := stepLogCause(raw); got != "" {
			t.Errorf("%s: cause = %q, want none", name, got)
		}
	}
}

func TestALineThatSaysWhatFailedIsTheCauseThoughTheRunnerMarkedNone(t *testing.T) {
	raw := "2026-10-12T10:00:00.0000000Z ##[group]Run make deploy\n" +
		"2026-10-12T10:00:00.0000000Z ##[endgroup]\n" +
		"2026-10-12T10:00:01.0000000Z Deploying to staging\n" +
		"2026-10-12T10:00:01.0000000Z Permission denied for role deployer\n" +
		"2026-10-12T10:00:01.0000000Z Cleaning up\n" +
		"2026-10-12T10:00:02.0000000Z ##[error]Process completed with exit code 1.\n"
	if got := stepLogCause(raw); got != "Permission denied for role deployer" {
		t.Errorf("cause = %q", got)
	}
}

func TestFailuresWithoutAnErrorLineAreToldApartByStepAndSaySo(t *testing.T) {
	a := failingApp(t)
	a.st.causes[causeID("acme/api", a.st.status["acme/api"][0])] = runCause{Job: "build", Step: "Install dependencies", Text: noErrorLine}
	a.st.causes[causeID("acme/web", a.st.status["acme/web"][0])] = runCause{Job: "build", Step: "Scan image", Text: noErrorLine}
	v, out := groupedFailing(a)
	wantAll(t, out, "? no error line in the log", "Install dependencies", "Scan image")
	if n := strings.Count(out, "no error line in the log"); n < 2 {
		t.Errorf("two steps without an error line are two rows, found %d:\n%s", n, out)
	}
	t2 := v.failing.table()
	for i := 0; i < t2.Len(); i++ {
		if t2.SetCursor(i); strings.Contains(t2.Selected().Cells[0].Text, "no error line") {
			break
		}
	}
	panel := ansi.Strip(strings.Join(v.failing.panel(a, 50, 30), "\n"))
	wantAll(t, panel, "l opens its log")
}

func runningMain(id, workflow int64, name string, created time.Time) gh.Run {
	r := mainRun(id, workflow, "", created)
	r.Name, r.Status = name, "in_progress"
	return r
}

func TestWorkflowsOnDefaultShowsRunningOnesInPlaceSortedByName(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	build, lint := mainRun(10, 40, "success", now.Add(-time.Hour)), mainRun(11, 41, "success", now.Add(-time.Hour))
	build.Name, lint.Name = "build", "lint"
	other := runningMain(13, 42, "e2e", now)
	other.HeadBranch = "feature"
	a.st.status["acme/billing"] = []gh.Run{lint, runningMain(12, 40, "build", now), other, build}

	got := a.workflowsOnDefault("acme/billing")
	if len(got) != 2 || got[0].ID != 12 || got[1].ID != 11 {
		t.Errorf("workflows = %v, want the running build (12) then lint (11)", ids(got))
	}
}

func TestMainNowCountsRunningWorkflows(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	repo, _ := a.repoByName("acme/api")
	repo.Health = []string{"PENDING", "SUCCESS"}
	a.st.status["acme/api"] = []gh.Run{runningMain(20, 50, "build", now), runningMain(21, 51, "e2e", now)}
	if _, label := a.mainNow(repo); label != "2 running" {
		t.Errorf("label = %q, want 2 running", label)
	}
}
