package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestScheduledListsWhatGitHubSwitchedOffFirst(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	a.st.scheduled = true
	a.st.schedules = []gh.Schedule{
		{Repo: "acme/api", File: ".github/workflows/nightly.yml", Crons: []string{"0 3 * * *"}},
		{Repo: "acme/web", File: ".github/workflows/cleanup.yml", Crons: []string{"0 4 * * 1"}},
	}
	d := &a.st.sched
	d.workflows["acme/api"] = []gh.Workflow{{ID: 1, Name: "Nightly", Path: ".github/workflows/nightly.yml", State: "active"}}
	d.workflows["acme/web"] = []gh.Workflow{{ID: 2, Name: "Cleanup", Path: ".github/workflows/cleanup.yml", State: "disabled_inactivity"}}
	failed := mainRun(9, 1, "failure", now.Add(-5*time.Hour))
	d.runs[scheduleKey("acme/api", ".github/workflows/nightly.yml")] = &failed
	stateIn(d.runsState, "acme/api").end(nil)
	v, out := sharedTab(a, viewScheduled)
	wantAll(t, out, "Cleanup", "disabled by GitHub", "Nightly", "Daily at 03:00", "1 failing", "1 disabled", "GitHub disabled it")
	if first, _ := v.scheduled.selected(); first.name() != "Cleanup" {
		t.Errorf("the workflow GitHub switched off should come first, got %s", first.name())
	}
}

func TestScheduledReadsTheFilesWhenTheyWereNotReadForSchedules(t *testing.T) {
	a := failingApp(t)
	a.st.usesState.at = time.Now()
	if cmd := a.loadSchedules(false); cmd == nil || !a.st.usesState.busy {
		t.Error("a scan from before schedules were read should be read again")
	}
}

func TestWarningsAreGroupedByTheirFirstSentenceWithWhatCausesThem(t *testing.T) {
	a := failingApp(t)
	node := func(actions string) []runWarning {
		return []runWarning{{Job: "build", Message: "Node.js 20 actions are deprecated. The following actions are running on Node.js 20: " + actions + ". Update them."}}
	}
	for repo, runs := range a.st.status {
		for _, r := range runs {
			a.st.warnings[causeID(repo, r)] = []runWarning{}
		}
	}
	a.st.warnings[causeID("acme/api", a.st.status["acme/api"][0])] = node("acme/setup-tools@v2, actions/cache@v3")
	a.st.warnings[causeID("acme/web", a.st.status["acme/web"][0])] = node("acme/setup-tools@v2")
	_, out := sharedTab(a, viewWarnings)
	wantAll(t, out, "Node.js 20 actions are deprecated.", "acme/setup-tools@v2", "Source", "1 from your actions")
	if n := strings.Count(out, "Node.js 20 actions are deprecated."); n != 2 {
		t.Errorf("one row for the warning (and the panel), seen %d:\n%s", n, out)
	}
}

func TestWarningsThatDifferOnlyInTheirValuesHaveTheSamePattern(t *testing.T) {
	same := [][2]string{
		{"Node.js 20 actions are deprecated. Running: acme/a@v1", "Node.js 20 actions are deprecated. Running: acme/b@v2, other/c@main"},
		{"Back off of 10.123 seconds before retry.", "Back off of 9.4 seconds before retry."},
		{"Unexpected input(s) 'name', valid inputs are ['path', 'version', 'app_version']", "Unexpected input(s) 'new_release_channel', valid inputs are ['app_name', 'dashed_app_name']"},
	}
	for _, p := range same {
		if warningKey(p[0]) != warningKey(p[1]) {
			t.Errorf("%q and %q should be one warning: %q, %q", p[0], p[1], warningKey(p[0]), warningKey(p[1]))
		}
	}
	if got := warningPattern("Unexpected input(s) 'name', valid inputs are ['path']"); got != "Unexpected input(s) …, valid inputs are […]" {
		t.Errorf("pattern = %q", got)
	}
}

func TestAWarningOpensToTheRunsThatGaveIt(t *testing.T) {
	a := failingApp(t)
	for repo, runs := range a.st.status {
		for _, r := range runs {
			a.st.warnings[causeID(repo, r)] = []runWarning{}
		}
	}
	a.st.warnings[causeID("acme/api", a.st.status["acme/api"][0])] = []runWarning{{Message: "Unexpected input(s) 'name', valid inputs are ['path']"}}
	a.st.warnings[causeID("acme/web", a.st.status["acme/web"][0])] = []runWarning{{Message: "Unexpected input(s) 'token', valid inputs are ['github-token']"}}
	v, out := sharedTab(a, viewWarnings)
	wantAll(t, out, "Unexpected input(s) …, valid inputs are […]", "VARIANTS", "Grouped by warning")
	if strings.Contains(out, "├─") {
		t.Errorf("the warning should start closed:\n%s", out)
	}
	v.Update(a, tea.KeyMsg{Type: tea.KeyEnter})
	v.sync(a)
	out = ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "├─", "api  CI #1", "'name'", "└─", "web  CI #2", "'token'")
	v.Update(a, tea.KeyMsg{Type: tea.KeyDown})
	if bar := barOf(a, v.Keys(a)); !strings.Contains(bar, " r ") || !strings.Contains(bar, " enter ") {
		t.Errorf("on a run, enter opens it and r its repo: %s", bar)
	}
	v.Update(a, runes("g"))
	v.sync(a)
	wantAll(t, ansi.Strip(v.Render(a, 170, 40)), "Every run on its own line", "REPO", "WORKFLOW", "Unexpected input(s) 'name'")
}

func TestFailingListsEveryRunOnALineOfItsOwnUntilGGroupsThem(t *testing.T) {
	a := failingApp(t)
	v, out := sharedTab(a, viewFailing)
	wantAll(t, capsOff(out), "g  all runs", "REPO", "STEP", "api", "web", "billing", "toomanyrequests", "FAIL src/basket/delivery.test.ts")
	if strings.Contains(out, "├─") {
		t.Errorf("the flat list has no tree:\n%s", out)
	}
	v.Update(a, runes("g"))
	v.sync(a)
	if grouped := ansi.Strip(v.Render(a, 170, 40)); !strings.Contains(grouped, "group by cause") || !strings.Contains(grouped, "2 repos") {
		t.Errorf("g should group the runs by cause:\n%s", grouped)
	}
}

func TestARunOpensItsRepoAtItsWorkflow(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	v := newRunView(a, "o/r", gh.Run{ID: 7, Name: "CI", WorkflowID: 5, Status: "completed", Conclusion: "success"})
	v.loaded, v.jobsLoaded = true, true
	drive(a, a.push(v), time.Second)
	pressKeys(a, runes("r"))
	repo, ok := a.top().(*repoView)
	if !ok || repo.repo != "o/r" || repo.wantWF != 5 {
		t.Errorf("r should open o/r at workflow 5, top is %T", a.top())
	}
}

func TestRunnersSayWhenNoRunnerHasTheLabelsAJobWaitsFor(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	a.me = "sam"
	waiting := mainRun(7, 10, "", now.Add(-20*time.Minute))
	waiting.Status, waiting.Conclusion = "queued", ""
	a.st.status["acme/api"] = []gh.Run{waiting}
	a.st.runners.orgOf = "acme"
	a.st.runners.org = []gh.Runner{runner("gpu-1", "online", false, "self-hosted", "gpu")}
	a.st.runners.jobs[7] = []gh.Job{{ID: 70, Name: "train", Status: "queued", CreatedAt: now.Add(-20 * time.Minute), Labels: []string{"self-hosted", "gpu-large"}}}
	_, out := sharedTab(a, viewRunners)
	wantAll(t, out, "self-hosted, gpu-large", "1 waiting", "20m", "no runner has these labels", "1 without a runner", "gpu-1")
}

func runner(name, status string, busy bool, labels ...string) gh.Runner {
	r := gh.Runner{ID: int64(len(name)), Name: name, Status: status, Busy: busy}
	for _, l := range labels {
		r.Labels = append(r.Labels, struct {
			Name string `json:"name"`
		}{l})
	}
	return r
}

func TestOrgRunnersYouMayNotSeeAreExplained(t *testing.T) {
	a := failingApp(t)
	a.me = "sam"
	lock := runnersDenial("acme", &gh.AccessError{Repo: "acme"})
	a.st.runners.orgLock = &lock
	_, out := sharedTab(a, viewRunners)
	wantAll(t, out, "only owners of acme may list its runners", "ask an owner of acme")
}

func TestAJobOnAGitHubHostedRunnerIsNotStuck(t *testing.T) {
	if !githubHosted([]string{"ubuntu-24.04"}) || githubHosted([]string{"self-hosted", "linux"}) || githubHosted([]string{"gpu-large"}) {
		t.Error("ubuntu-24.04 is GitHub's; self-hosted and custom labels are not")
	}
}

func TestTheStripKeepsTheViewOnDisplayInSightOnANarrowScreen(t *testing.T) {
	var items []stripItem
	for _, l := range []string{"Failing", "Running", "Queued", "Needs approval", "Last 7 days", "Scheduled", "Warnings", "Runners", "Shared workflows", "Shared actions"} {
		items = append(items, stripItem{Label: l, Icon: "gauge", Count: "3"})
	}
	out := ansi.Strip(viewStrip(items, 9, 80))
	if !strings.Contains(out, "Shared actions") || width(out) > 80 {
		t.Errorf("the last view should show on 80 columns:\n%q", out)
	}
	if !strings.Contains(out, "‹") {
		t.Errorf("views cut off at the left should be marked: %q", out)
	}
}

func TestARunSaysHowLongItQueuedAndWhatItCalledButNotItsOnlyAttempt(t *testing.T) {
	now := time.Now()
	a := accessApp(t, "WRITE", "gho_x")
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 3, Status: "completed", Conclusion: "success", RunAttempt: 1,
		CreatedAt: now.Add(-time.Hour), RunStartedAt: now.Add(-time.Hour),
		DisplayTitle:        "feat: show delivery estimates in the basket and on the order confirmation page, taking weekends and public holidays into account",
		ReferencedWorkflows: []gh.ReferencedWorkflow{{Path: "acme/ci/.github/workflows/docker.yml@v9"}}}
	v := newRunView(a, "o/r", run)
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", CreatedAt: now.Add(-time.Hour), StartedAt: now.Add(-46 * time.Minute), CompletedAt: now.Add(-40 * time.Minute)}}
	v.rebuild()
	out := ansi.Strip(v.Render(a, 140, 30))
	wantAll(t, out, "queued 14m", "calls docker.yml@v9", "holidays into", "account", "build · queued 14m")
	if strings.Contains(out, "attempt 1") {
		t.Errorf("a first attempt needs no mention:\n%s", out)
	}
}

func TestTheConsumersScopeCoversTheReposThatCallYourWorkflows(t *testing.T) {
	a := failingApp(t)
	a.repos = append(a.repos, gh.Repo{FullName: "acme/ci", HasActions: true, PushedAt: time.Now(), DefaultBranch: "main"})
	a.st.uses = []gh.Use{
		use("acme/api", ".github/workflows/ci.yml", "acme/ci/.github/workflows/docker.yml", "v9"),
		use("acme/ci", ".github/workflows/docker.yml", "acme/ci/.github/actions/login", "v9"),
		use("acme/billing", ".github/workflows/ci.yml", "other/checkout", "v4"),
	}
	v := newActivityView(a)
	v.scope = scopeConsumers
	if got := strings.Join(v.repos(a), " "); got != "acme/api" {
		t.Errorf("consumers = %q, want only acme/api: ci only calls itself, billing calls someone else", got)
	}
}

func TestTickedRunsAreRerunOrCancelledTogether(t *testing.T) {
	now := time.Now()
	failed := activityRun(1, "completed", now.Add(-time.Hour))
	failed.Conclusion = "failure"
	running := activityRun(2, "in_progress", now.Add(-time.Minute))
	a, v := activityApp(failed, running, activityRun(3, "completed", now.Add(-2*time.Hour)))
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.Update(a, tea.KeyMsg{Type: tea.KeyCtrlA})
	bar := barOf(a, v.Keys(a))
	if !strings.Contains(bar, " R ") || !strings.Contains(bar, " X ") {
		t.Fatalf("with runs ticked, R and X act on them: %s", bar)
	}
	var rerun, cancel string
	for _, b := range v.Keys(a) {
		switch {
		case len(b.Keys) > 0 && b.Keys[0] == "R" && rerun == "":
			rerun = b.Desc
		case len(b.Keys) > 0 && b.Keys[0] == "X" && cancel == "":
			cancel = b.Desc
		}
	}
	if rerun != "rerun failed (1)" || cancel != "cancel (1)" {
		t.Errorf("R = %q, X = %q: one failed and one running run are ticked", rerun, cancel)
	}
	if !strings.Contains(ansi.Strip(v.Render(a, 140, 30)), "3 ticked") {
		t.Errorf("the line above the list should say how many are ticked")
	}
}

func TestTheActionsTabSaysHowFarBackItsRunsAreComplete(t *testing.T) {
	now := time.Now()
	var runs []gh.Run
	for i := range 10 {
		runs = append(runs, activityRun(int64(100-i), "completed", now.Add(-time.Duration(i+1)*20*time.Minute)))
	}
	a, v := activityApp(runs...)
	a.repos = []gh.Repo{{FullName: "o/r", HasActions: true, PushedAt: now}}
	a.Update(tea.WindowSizeMsg{Width: 170, Height: 40})
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, out, "complete for the last 3h")
}

func TestAPullRequestNamesARequiredCheckNothingReports(t *testing.T) {
	withGitHub(t, map[string]string{
		"/graphql":                       reviewedPR,
		"/repos/o/r/rules/branches/main": `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"CI / test"},{"context":"build / unit"}]}}]`,
		"/repos/o/r/issues/12/comments":  "[]",
		"/repos/o/r/pulls/12/reviews":    "[]",
		"/repos/o/r/pulls/12/comments":   "[]",
		"/repos/o/r/pulls/12/files":      "[]",
	})
	a := apiApp(t)
	a.Update(tea.WindowSizeMsg{Width: 170, Height: 45})
	v := newPRView(a, "o/r", 12)
	drive(a, a.push(v), 5*time.Second)
	out := screen(a)
	wantAll(t, out, "1 required never reported", "Required, but nothing reports it", "build / unit")
	if strings.Contains(out, "CI / test\n") {
		t.Errorf("CI / test reported, as the check test of workflow CI")
	}
}

func TestAMovedMajorTagIsAChangeSinceTheRunPassed(t *testing.T) {
	green := gh.Run{ReferencedWorkflows: []gh.ReferencedWorkflow{
		{Path: "acme/ci/.github/workflows/docker.yml@v9", SHA: "1111111aaaa"},
		{Path: "acme/ci/.github/workflows/lint.yml@v3", SHA: "2222222bbbb"}}}
	failed := gh.Run{ReferencedWorkflows: []gh.ReferencedWorkflow{
		{Path: "acme/ci/.github/workflows/docker.yml@v9", SHA: "3333333cccc"},
		{Path: "acme/ci/.github/workflows/lint.yml@v3", SHA: "2222222bbbb"}}}
	got := calledChanges(green, failed)
	if len(got) != 2 || !got[0].changed() || got[1].changed() {
		t.Errorf("docker.yml moved at v9, lint.yml did not: %+v", got)
	}
}

func TestSinceItPassedListsTheCommitsAndWhatTheRunCalled(t *testing.T) {
	now := time.Now()
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/workflows/5/runs": `{"workflow_runs":[{"id":6,"run_number":6,"name":"CI","head_sha":"aaaaaaa1","status":"completed","conclusion":"success","created_at":"2026-10-01T10:00:00Z",
			"referenced_workflows":[{"path":"acme/ci/.github/workflows/docker.yml@v9","sha":"1111111"}]}]}`,
		"/repos/o/r/compare/aaaaaaa1...bbbbbbb2": `{"ahead_by":1,"commits":[{"sha":"bbbbbbb2","commit":{"message":"feat: cache the basket","author":{"name":"sam"}}}]}`,
	})
	a := accessApp(t, "WRITE", "gho_x")
	run := gh.Run{ID: 7, RunNumber: 7, Name: "CI", WorkflowID: 5, HeadBranch: "main", HeadSHA: "bbbbbbb2", Status: "completed", Conclusion: "failure", CreatedAt: now,
		ReferencedWorkflows: []gh.ReferencedWorkflow{{Path: "acme/ci/.github/workflows/docker.yml@v9", SHA: "9999999"}}}
	v := newSinceGreenView(a, "o/r", run)
	drive(a, a.push(v), 5*time.Second)
	out := screen(a)
	wantAll(t, out, "#6 passed", "#7 failed", "1 commit since", "feat: cache the basket", "docker.yml", "v9 moved: 1111111 → 9999999", "inputs")
}

func TestACancelledRunNamesItsConcurrencyGroupAndTheRunThatReplacedIt(t *testing.T) {
	if g := groupIn([]gh.Annotation{{Message: "Canceling since a higher priority waiting request for 'deploy-refs/heads/main' exists"}}); g != "deploy-refs/heads/main" {
		t.Fatalf("group = %q", g)
	}
	a := accessApp(t, "WRITE", "gho_x")
	v := newRunView(a, "o/r", gh.Run{ID: 7, Name: "Deploy", Status: "completed", Conclusion: "cancelled"})
	v.loaded, v.jobsLoaded = true, true
	v.concurrency = concurrencyNote{group: "deploy-refs/heads/main", replacedBy: &gh.Run{ID: 8, RunNumber: 129}}
	out := ansi.Strip(v.Render(a, 140, 30))
	wantAll(t, out, "Concurrency", "deploy-refs/heads/main", "#129")
	found := false
	for _, b := range v.Keys(a) {
		found = found || strings.HasPrefix(b.Help, "open #129")
	}
	if !found {
		t.Errorf("the menu should open the run that took its place")
	}
}

func TestAJobThatOnlyPassedOnARerunIsCountedAsFlaky(t *testing.T) {
	passedOnRerun := gh.Run{ID: 1, Name: "CI", Status: "completed", Conclusion: "success", RunAttempt: 2}
	again := gh.Run{ID: 2, Name: "CI", Status: "completed", Conclusion: "success", RunAttempt: 3}
	clean := gh.Run{ID: 3, Name: "CI", Status: "completed", Conclusion: "success", RunAttempt: 1}
	failed := gh.Run{ID: 4, Name: "CI", Status: "completed", Conclusion: "failure", RunAttempt: 2}
	reruns, finished, jobs := flakes([]gh.Run{passedOnRerun, again, clean, failed}, map[int64][]string{
		1: {"CI › test (windows)"}, 2: {"CI › test (windows)", "CI › lint"},
	})
	if reruns != 2 || finished != 4 {
		t.Errorf("%d of %d passed on a rerun, want 2 of 4", reruns, finished)
	}
	if len(jobs) != 2 || jobs[0].name != "CI › test (windows)" || jobs[0].times != 2 {
		t.Errorf("flaky jobs = %+v", jobs)
	}
	v := &repoView{flaky: map[int64][]string{1: {"CI › test (windows)"}}}
	out := ansi.Strip(strings.Join(v.flakyLines([]gh.Run{passedOnRerun, clean}, 40), "\n"))
	wantAll(t, out, "50% passed on a rerun", "CI › test (windows)", "1×")
}

func TestEnvironmentsShowTheirProtectionAndWhatWasLastDeployed(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/o/r/environments": `{"total_count":2,"environments":[
			{"name":"production","protection_rules":[{"type":"required_reviewers","reviewers":[{"type":"Team","reviewer":{"slug":"release-team"}}]},{"type":"wait_timer","wait_timer":10}],"deployment_branch_policy":{"protected_branches":true}},
			{"name":"staging","protection_rules":[]}]}`,
		"/repos/o/r/deployments":            `[{"id":5,"sha":"abc1234567","ref":"main","environment":"production","creator":{"login":"sam"},"created_at":"2026-10-12T10:00:00Z"}]`,
		"/repos/o/r/deployments/5/statuses": `[{"state":"failure","log_url":"https://github.com/o/r/actions/runs/88"}]`,
		"/repos/o/r/actions/runs/88":        `{"id":88,"name":"Deploy","run_number":41,"status":"completed","conclusion":"failure"}`,
	})
	a := accessApp(t, "WRITE", "gho_x")
	a.Update(tea.WindowSizeMsg{Width: 170, Height: 40})
	v := newEnvironmentsView(a, "o/r")
	v.Update(a, v.Refresh(a, true)())
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, out, "production", "approval by @release-team", "waits 10m", "protected branches only", "failure", "staging", "none",
		"deployments failed", "1 without protection", "Deploy #41")
}

func TestSecretsSayWhereEachIsDefinedAndWhenItIsDefinedTwice(t *testing.T) {
	v := &secretsView{rows: settingRows(gh.Settings{
		Secrets:    []gh.Setting{{Name: "NPM_TOKEN"}},
		OrgSecrets: []gh.Setting{{Name: "NPM_TOKEN"}, {Name: "SONAR_TOKEN"}},
		Variables:  []gh.Setting{{Name: "REGION"}},
		EnvSecrets: map[string][]gh.Setting{"production": {{Name: "DEPLOY_KEY"}}},
	}, []string{"production"})}
	a := accessApp(t, "ADMIN", "gho_x")
	v.t = newSecretsView(a, "o/r").t
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, out, "NPM_TOKEN", "SONAR_TOKEN", "DEPLOY_KEY", "production", "REGION", "variable", "1 defined twice", "names only")
}

func TestSecretsYouMayNotListAreExplained(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	v := newSecretsView(a, "o/r")
	if cmd := v.Refresh(a, true); cmd != nil {
		t.Error("with write access nothing should be asked: only admins may list secrets")
	}
	v.sync(a)
	wantAll(t, ansi.Strip(v.Render(a, 170, 30)), "needs admin access to o/r")
}

func TestARunnerSaysWhatItRunsAndWhereItRanEvenWhenItCannotBeListed(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	a.me = "sam"
	lock := runnersDenial("acme", &gh.AccessError{Repo: "acme"})
	a.st.runners.orgLock = &lock
	running := mainRun(7, 10, "", now.Add(-5*time.Minute))
	running.Status, running.Conclusion, running.Name, running.RunNumber = "in_progress", "", "CI", 128
	a.st.status["acme/api"] = []gh.Run{running}
	a.st.runners.jobs[7] = []gh.Job{
		{ID: 70, Name: "build", Status: "in_progress", RunnerName: "build-01", RunnerGroup: "Default", Labels: []string{"self-hosted", "linux"}, StartedAt: now.Add(-4 * time.Minute)},
		{ID: 71, Name: "lint", Status: "in_progress", RunnerName: "GitHub Actions 12", RunnerGroup: "GitHub Actions", Labels: []string{"ubuntu-24.04"}, StartedAt: now.Add(-time.Minute)},
		{ID: 72, Name: "gpu", Status: "queued", Labels: []string{"self-hosted", "gpu-large"}, CreatedAt: now.Add(-3 * time.Minute)},
	}
	v, out := sharedTab(a, viewRunners)
	wantAll(t, out, "build-01", "busy", "api › CI #128 › build", "GitHub-hosted", "ubuntu-24.04 ×1", "no runner seen with these labels", "1 busy")
	if strings.Contains(out, "GitHub Actions 12") {
		t.Errorf("a GitHub-hosted runner's name says nothing and should not be listed:\n%s", out)
	}
	v.runners.t.SetCursor(1)
	wantAll(t, ansi.Strip(v.Render(a, 170, 40)), "seen in your runs", "Current job", "Jobs per repository")
	if !strings.Contains(barOf(a, v.Keys(a)), " l ") {
		t.Errorf("l should open the log of the job the runner runs")
	}
}

func TestTheWarningsOfManyRunsAreReadInOneGraphQLRequest(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/graphql": `{"data":{"r0":{"checkSuite":{"checkRuns":{"nodes":[]}}},"r1":{"checkSuite":{"checkRuns":{"nodes":[]}}}}}`})
	a := failingApp(t)
	var runs []repoRun
	for i, repo := range []string{"acme/api", "acme/web", "acme/billing"} {
		r := mainRun(int64(40+i), 1, "success", time.Now())
		if i < 2 {
			r.NodeID = fmt.Sprintf("WFR_%d", i)
		}
		runs = append(runs, repoRun{repo, r})
	}
	drive(a, a.st.loadWarnings(a, runs), 5*time.Second)
	if n := fake.total(); n != 1 {
		t.Errorf("%d requests, want one for both runs with a node id", n)
	}
	if _, ok := a.st.warnings[causeID("acme/api", runs[0].run)]; !ok {
		t.Error("the warnings of the first run should be known")
	}
	if _, ok := a.st.warnings[causeID("acme/billing", runs[2].run)]; ok {
		t.Error("a run without a node id waits until its list is read again")
	}
}

func TestScheduledRunsAreReadOncePerRepo(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/acme/api/actions/workflows": `{"workflows":[{"id":1,"name":"Nightly","path":".github/workflows/nightly.yml","state":"active"},{"id":2,"name":"Weekly","path":".github/workflows/weekly.yml","state":"active"}]}`,
		"/repos/acme/api/actions/runs":      `{"workflow_runs":[{"id":9,"name":"Nightly","path":".github/workflows/nightly.yml","status":"completed","conclusion":"failure"}]}`,
	})
	a := failingApp(t)
	a.st.scheduled = true
	a.st.schedules = []gh.Schedule{
		{Repo: "acme/api", File: ".github/workflows/nightly.yml", Crons: []string{"0 3 * * *"}},
		{Repo: "acme/api", File: ".github/workflows/weekly.yml", Crons: []string{"0 4 * * 1"}},
	}
	drive(a, a.loadSchedules(true), 5*time.Second)
	if n := fake.count("/repos/acme/api/actions/runs"); n != 1 {
		t.Errorf("runs were read %d times for two scheduled workflows of one repo, want 1", n)
	}
	got := map[string]schedule{}
	for _, s := range a.schedules() {
		got[s.name()] = s
	}
	if got["Nightly"].kind() != KFail || got["Weekly"].last != nil || !got["Weekly"].read {
		t.Errorf("Nightly failed, Weekly never ran: %+v", got)
	}
}

func TestRunnersThatAreGoneFoldIntoADayOfHistoryThatCanBeCleared(t *testing.T) {
	now := time.Now()
	a := failingApp(t)
	a.me = "sam"
	run := mainRun(7, 10, "success", now.Add(-time.Hour))
	run.RunNumber = 77
	a.st.status["acme/api"] = []gh.Run{run}
	a.st.runners.jobs[7] = []gh.Job{{ID: 70, Name: "test", Status: "completed", Conclusion: "success", RunnerName: "arc-linux-7d9f-a81mz",
		Labels: []string{"self-hosted", "linux"}, StartedAt: now.Add(-50 * time.Minute), CompletedAt: now.Add(-40 * time.Minute)}}
	old := slim("acme/web", mainRun(8, 20, "success", now.Add(-30*time.Hour)), gh.Job{ID: 80, Name: "build", Status: "completed", Conclusion: "success",
		RunnerName: "arc-linux-old", StartedAt: now.Add(-26 * time.Hour), CompletedAt: now.Add(-25 * time.Hour)})
	a.st.runners.history[80] = old
	v, out := sharedTab(a, viewRunners)
	wantAll(t, out, "ended runners", "1 job", "the last 24h")
	if strings.Contains(out, "arc-linux-7d9f-a81mz") {
		t.Errorf("a pod that is gone should not have a row of its own:\n%s", out)
	}
	if _, kept := a.st.runners.history[80]; kept {
		t.Error("a job older than a day should be forgotten")
	}
	v.runners.toggleHistory()
	v.sync(a)
	wantAll(t, ansi.Strip(v.Render(a, 170, 40)), "└─ arc-linux-7d9f-a81mz", "api › CI #77 › test")
	drive(a, v.Update(a, runes("X")), time.Second)
	if a.modal == nil {
		t.Fatal("X should ask before it clears the history")
	}
	pressKeys(a, runes("y"))
	if len(a.st.runners.history) != 0 {
		t.Errorf("the history should be empty, has %d", len(a.st.runners.history))
	}
}

func TestInPreviousModeAJobShowsHowLongItTookInThePreviousRun(t *testing.T) {
	now := time.Now()
	a := accessApp(t, "WRITE", "gho_x")
	a.cfg.CompareDurations = config.ComparePrevious
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 128, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-time.Hour), RunStartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour + 4*time.Minute)}
	v := newRunView(a, "o/r", run)
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-time.Hour + 4*time.Minute)}}
	v.typical = map[string]gh.TypicalJob{"build": {Median: 5 * time.Minute, Samples: 8, Previous: 4*time.Minute + 40*time.Second}}
	v.previous = &gh.Run{ID: 6, RunNumber: 127, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-2 * time.Hour), RunStartedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2*time.Hour + 4*time.Minute + 40*time.Second)}
	v.typicalLoad = typicalDone
	v.rebuild()
	a.logs[1] = newJobLog(nil, 0)
	out := ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, squeezed(out), "#127", "4m 40s", "−40s ▼", "previous run", "took 4m 00s ▼ −40s vs #127 4m 40s")
	if strings.Contains(out, "MEDIAN") || strings.Contains(out, "5m 00s") {
		t.Errorf("only the previous run is compared with in this mode:\n%s", out)
	}
	a.cfg.CompareDurations = config.CompareMedian
	out = ansi.Strip(v.Render(a, 170, 30))
	wantAll(t, squeezed(out), "MEDIAN", "5m 00s", "−1m 00s ▼", "vs median 5m 00s of 8 runs")
}

func TestARunWithoutAStartTimeHasNoQueueTime(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	v := newRunView(a, "o/r", gh.Run{ID: 7, Name: "CI", Status: "completed", Conclusion: "success"})
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: time.Now().Add(-time.Minute)}}
	if q := v.queued(time.Now()); q != 0 {
		t.Errorf("queued %s without knowing when the run was created", q)
	}
}

func TestAFullStopAfterAnActionIsNotPartOfIt(t *testing.T) {
	got := warningVia(runWarning{Message: "Running on Node.js 20: acme/setup-tools@v1, actions/cache@v3. Update them."})
	if len(got) != 2 || got[1] != "actions/cache@v3" {
		t.Errorf("via = %q", got)
	}
}
