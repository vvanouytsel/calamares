package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func prLinkedRun(numbers ...int) gh.Run {
	r := gh.Run{ID: 7, Name: "CI", RunNumber: 812, Status: "completed", Conclusion: "failure", HeadBranch: "feat", Event: "pull_request", CreatedAt: time.Now()}
	for _, n := range numbers {
		r.PullRequests = append(r.PullRequests, struct {
			Number int `json:"number"`
		}{n})
	}
	return r
}

// actionsTabWith shows the Actions tab on Last 7 days holding run, wide
// enough for its panel.
func actionsTabWith(t *testing.T, run gh.Run) *App {
	a := apiApp(t)
	a.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	a.st.status["o/r"] = []gh.Run{run}
	v := newActivityView(a)
	v.filter = viewRecent
	a.stack = []View{v}
	_ = a.View()
	return a
}

func TestHashOpensThePullRequestARunBelongsTo(t *testing.T) {
	withGitHub(t, map[string]string{"/graphql": reviewedPR})
	a := actionsTabWith(t, prLinkedRun(12))
	pressKeys(a, runes("#"))
	if v, ok := a.top().(*prView); !ok || v.number != 12 {
		t.Fatalf("# should open pull request #12, the screen is %T", a.top())
	}
}

func TestHashFindsTheOpenPullRequestOfTheBranchARunRanOn(t *testing.T) {
	withGitHub(t, map[string]string{"/graphql": reviewedPR})
	run := prLinkedRun()
	run.Event = "push"
	a := actionsTabWith(t, run)
	a.st.prs = []gh.PullRequest{{Number: 12, Repo: "o/r", Branch: "feat", Base: "main", Title: "feat: deploy"}}
	pressKeys(a, runes("#"))
	if v, ok := a.top().(*prView); !ok || v.number != 12 {
		t.Fatalf("# should open #12, opened from the branch the run ran on; the screen is %T", a.top())
	}
}

func TestHashDoesNotTakeAForkPullRequestFromMainForARunOnMain(t *testing.T) {
	run := prLinkedRun()
	run.HeadBranch, run.Event = "main", "push"
	a := actionsTabWith(t, run)
	a.st.prs = []gh.PullRequest{{Number: 12, Repo: "o/r", Branch: "main", Base: "main"}}
	if got := a.runPRNumbers("o/r", run); len(got) != 0 {
		t.Errorf("a run on main belongs to no pull request, got %v", got)
	}
}

func TestHashSaysSoWhenARunHasNoPullRequest(t *testing.T) {
	run := prLinkedRun()
	run.Event = "push"
	a := actionsTabWith(t, run)
	if got := hintKeys(a, a.runKeys("o/r", run)); strings.Contains(got, " # ") {
		t.Errorf("the key bar offers # for a run without a pull request: %q", got)
	}
	pressKeys(a, runes("#"))
	if _, ok := a.top().(*activityView); !ok || !strings.Contains(a.flash, "pull request") {
		t.Errorf("# should stay and say there is no pull request: screen %T, flash %q", a.top(), a.flash)
	}
}

func TestHashAsksWhichPullRequestWhenARunHasSeveral(t *testing.T) {
	a := actionsTabWith(t, prLinkedRun(12, 13))
	pressKeys(a, runes("#"))
	if _, ok := a.modal.(*pickerModal); !ok {
		t.Errorf("# on a run of two pull requests should ask which, got dialog %T", a.modal)
	}
}

func TestRunPanelNamesThePullRequestAndHowItStands(t *testing.T) {
	a := actionsTabWith(t, prLinkedRun(12))
	a.st.prs = []gh.PullRequest{{Number: 12, Repo: "o/r", Branch: "feat", Base: "main", Title: "Fix login on Safari", ReviewDecision: "REVIEW_REQUIRED"}}
	out := screen(a)
	for _, want := range []string{"Pull request", "#12", "Fix login on Safari", "open it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run panel lacks %q:\n%s", want, out)
		}
	}
}

func TestRunScreenHeaderOffersThePullRequest(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", prLinkedRun(12))
	v.loaded = true
	a.stack = append(a.stack, v)
	if out := capsOff(screen(a)); !strings.Contains(out, "#12  # ") {
		t.Errorf("the run header should name #12 with its key:\n%s", out)
	}
}

const prBranchRuns = `{"total_count":3,"workflow_runs":[
 {"id":31,"name":"CI","run_number":812,"status":"completed","conclusion":"failure","head_branch":"feat","head_sha":"abc123","event":"pull_request","created_at":"2026-10-15T08:00:00Z","updated_at":"2026-10-15T08:04:00Z"},
 {"id":30,"name":"Build","run_number":344,"status":"completed","conclusion":"success","head_branch":"feat","head_sha":"abc123","event":"pull_request","created_at":"2026-10-15T08:00:00Z","updated_at":"2026-10-15T08:01:00Z"},
 {"id":20,"name":"CI","run_number":809,"status":"completed","conclusion":"success","head_branch":"feat","head_sha":"def456","event":"pull_request","created_at":"2026-10-15T05:00:00Z","updated_at":"2026-10-15T05:03:00Z"},
 {"id":10,"name":"CI","run_number":700,"status":"completed","conclusion":"success","head_branch":"feat","head_sha":"999999","event":"pull_request","created_at":"2026-10-01T05:00:00Z","updated_at":"2026-10-01T05:03:00Z","pull_requests":[{"number":3}]}]}`

const prTwoCommits = `[
 {"sha":"def456","commit":{"message":"Fix login","author":{"name":"you","date":"2026-10-15T04:59:00Z"}}},
 {"sha":"abc123","commit":{"message":"Retry on Safari\n\nbody","author":{"name":"you","date":"2026-10-15T07:59:00Z"}}}]`

func prWithRunsApp(t *testing.T) (*App, *prView) {
	withGitHub(t, map[string]string{
		"/graphql":                      reviewedPR,
		"/repos/o/r/actions/runs":       prBranchRuns,
		"/repos/o/r/pulls/12/commits":   prTwoCommits,
		"/repos/o/r/issues/12/comments": prIssueComments,
		"/repos/o/r/pulls/12/reviews":   prReviews,
		"/repos/o/r/pulls/12/comments":  prLineComments,
		"/repos/o/r/pulls/12/files":     prFiles,
	})
	a := apiApp(t)
	v := newPRView(a, "o/r", 12)
	drive(a, a.push(v), 5*time.Second)
	for v.tab != prTabRuns {
		pressKeys(a, tea.KeyMsg{Type: tea.KeyTab})
	}
	return a, v
}

func TestPullRequestRunsTabListsItsRunsNewestCommitFirst(t *testing.T) {
	a, _ := prWithRunsApp(t)
	out := screen(a)
	for _, want := range []string{"Runs 3", "#812", "#344", "#809", "Retry on Safari", "Fix login"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Runs tab lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Retry on Safari") > strings.Index(out, "Fix login") {
		t.Errorf("the newest commit should come first:\n%s", out)
	}
	if strings.Contains(out, "#700") {
		t.Errorf("a run of another pull request on the same branch name should not show:\n%s", out)
	}
}

func TestPullRequestRunsTabOpensTheRunUnderTheCursor(t *testing.T) {
	a, _ := prWithRunsApp(t)
	pressKeys(a, runes("j"), tea.KeyMsg{Type: tea.KeyEnter})
	if v, ok := a.top().(*runView); !ok || v.run.ID != 30 {
		t.Fatalf("enter should open run 30, the screen is %T", a.top())
	}
}
