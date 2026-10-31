package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

const prDetail = `{"data":{"viewer":{"login":"me"},"repository":{"squashMergeAllowed":true,"pullRequest":{
	"number":12,"title":"feat: deploy","url":"https://github.com/o/r/pull/12","state":"OPEN","headRefName":"feat","baseRefName":"main",
	"author":{"login":"you"},"repository":{"nameWithOwner":"o/r"},"mergeStateStatus":"CLEAN","mergeable":"MERGEABLE",
	"latest":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
		{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","checkSuite":{"workflowRun":{"databaseId":99,"workflow":{"name":"CI"}}}}]}}}}]}}}}}`

// prApp is on the screen of pull request o/r#12.
func prApp(t *testing.T) (*App, *countingGitHub) {
	fake := withGitHub(t, map[string]string{"/graphql": prDetail, "/repos/o/r/pulls/12/reviews": "{}"})
	a := apiApp(t)
	drive(a, a.push(newPRView(a, "o/r", 12)), 5*time.Second)
	_ = a.View()
	return a, fake
}

func TestPullRequestScreenOpensTheRunBehindACheck(t *testing.T) {
	a, _ := prApp(t)
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	_ = a.View()
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v, ok := a.top().(*runView); !ok || v.run.ID != 99 {
		t.Fatalf("enter on the check should open run 99, top is %T", a.top())
	}
}

func TestApprovingAPullRequestAsksFirst(t *testing.T) {
	a, fake := prApp(t)
	_, cmd := a.Update(runes("A"))
	drive(a, cmd, 5*time.Second)
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("A should ask before approving, modal is %T", a.modal)
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	drive(a, cmd, 5*time.Second)
	if fake.count("/repos/o/r/pulls/12/reviews") != 0 {
		t.Errorf("cancelling should not submit a review")
	}
	_, cmd = a.Update(runes("A"))
	drive(a, cmd, 5*time.Second)
	_, cmd = a.Update(runes("y"))
	drive(a, cmd, 5*time.Second)
	if fake.count("/repos/o/r/pulls/12/reviews") != 1 {
		t.Errorf("y should submit the approval")
	}
}

func TestMergeOffersOnlyWhatTheRepoAllows(t *testing.T) {
	a, _ := prApp(t)
	_, cmd := a.Update(runes("M"))
	drive(a, cmd, 5*time.Second)
	p, ok := a.modal.(*pickerModal)
	if !ok {
		t.Fatalf("M should ask how to merge, modal is %T", a.modal)
	}
	if got := pickerLabels(p); len(got) != 1 || got[0] != "squash" {
		t.Errorf("merge methods = %v, want only squash", got)
	}
}

func TestRepoScreenShowsItsPullRequestsOnTab(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs":      oneRun,
		"/repos/o/r/actions/workflows": `{"workflows":[]}`,
		"/graphql":                     `{"data":{"repository":{"pullRequests":{"nodes":[{"number":3,"title":"fix: typo","url":"u","repository":{"nameWithOwner":"o/r"}}]}}}}`,
	})
	a := apiApp(t)
	drive(a, a.openRepo("o/r", "", 0), 5*time.Second)
	for range sectionCount + 1 {
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyTab})
		drive(a, cmd, 5*time.Second)
	}
	if out := ansi.Strip(a.View()); !strings.Contains(out, "fix: typo") {
		t.Errorf("the pull requests section should list #3:\n%s", out)
	}
	// One read each for the pull requests, the issues and the branches passed on the way;
	// coming back to the pull requests reads nothing.
	if n := fake.count("/graphql"); n != 3 {
		t.Errorf("switching sections read GitHub %d times, want 3", n)
	}
}

func TestRepoScreenListsCommitsAndOpensTheRunsOfOne(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs":          oneRun,
		"/repos/o/r/actions/workflows":     `{"workflows":[]}`,
		"/repos/o/r/commits/abcdef1234567": `{"files":[{"filename":"src/main.go","status":"modified","additions":2,"deletions":1,"patch":"@@ -1 +1,2 @@\n-old\n+new\n+newer"}]}`,
		"/repos/o/r/commits":               `[{"sha":"abcdef1234567","html_url":"u","commit":{"message":"fix: typo\n\nbody","author":{"name":"Ann","date":"2026-01-01T00:00:00Z"}},"author":{"login":"ann"}}]`,
	})
	a := apiApp(t)
	drive(a, a.openRepo("o/r", "", 0), 5*time.Second)
	for range 3 {
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyTab})
		drive(a, cmd, 5*time.Second)
	}
	out := ansi.Strip(a.View())
	if !strings.Contains(out, "fix: typo") || !strings.Contains(out, "abcdef1") {
		t.Fatalf("the commits section should list the commit's subject and short SHA:\n%s", out)
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(a, cmd, 5*time.Second)
	cv, ok := a.top().(*commitView)
	if !ok || cv.runs.sha != "abcdef1234567" {
		t.Fatalf("enter on a commit should open the runs of that commit, top is %T", a.top())
	}
	if n := fake.count("/repos/o/r/actions/runs"); n < 1 {
		t.Errorf("the runs of the commit were not requested")
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyTab})
	drive(a, cmd, 5*time.Second)
	if out := ansi.Strip(a.View()); !strings.Contains(out, "main.go") || !strings.Contains(out, "newer") {
		t.Errorf("the changes tab should list the changed file and its diff:\n%s", out)
	}
}

func TestHeaderSaysHowOldTheDataIs(t *testing.T) {
	a := apiApp(t)
	a.st.issuesState.at = time.Now().Add(-3 * time.Minute)
	if out := ansi.Strip(a.renderTopBar()); !strings.Contains(out, "updated 3m ago") || !strings.Contains(out, "live off") {
		t.Errorf("header = %q", out)
	}
	a.live = true
	if out := ansi.Strip(a.renderTopBar()); !strings.Contains(out, "● live") {
		t.Errorf("header = %q", out)
	}
}

func TestPullRequestListOpensThePullRequest(t *testing.T) {
	withGitHub(t, map[string]string{"/graphql": prDetail})
	a := apiApp(t)
	a.st.prs = []gh.PullRequest{{Number: 12, Title: "feat: deploy", Repo: "o/r", Why: gh.WhyReview}}
	a.st.prsState.at = time.Now()
	drive(a, a.setTab(tabPulls), 5*time.Second)
	_ = a.View()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(a, cmd, 5*time.Second)
	if v, ok := a.top().(*prView); !ok || v.number != 12 {
		t.Fatalf("enter should open PR #12, top is %T", a.top())
	}
}

func TestPullRequestScreenFitsTheTerminal(t *testing.T) {
	a := apiApp(t)
	v := newPRView(a, "o/r", 159)
	v.pr = gh.PullRequestDetail{PullRequest: gh.PullRequest{Number: 159, Title: strings.Repeat("long title ", 10),
		Branch: strings.Repeat("feat/branch-", 6), Base: "main", Author: "someone-with-a-long-login"}, State: "OPEN", Files: 2}
	v.loaded = true
	for i, l := range strings.Split(v.Render(a, 80, 24), "\n") {
		if w := width(l); w > 80 {
			t.Errorf("line %d is %d wide, want ≤ 80: %q", i, w, ansi.Strip(l))
		}
	}
}

func TestTopBarAlwaysSaysHowOldTheDataIs(t *testing.T) {
	a := apiApp(t)
	a.st.issuesState.at = time.Now().Add(-3 * time.Minute)
	for _, w := range []int{70, 100, 160} {
		a.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		bar := a.renderTopBar()
		if out := ansi.Strip(bar); !strings.Contains(out, "updated 3m ago") || !strings.Contains(out, "live off") {
			t.Errorf("at %d columns the top bar is %q", w, out)
		}
		for _, line := range strings.Split(bar, "\n") {
			if width(line) > w {
				t.Errorf("at %d columns a top bar line is %d wide", w, width(line))
			}
		}
	}
}

func TestSlashOpensAnEmptyFilterEvenWhenOneIsActive(t *testing.T) {
	a := apiApp(t)
	a.top().(Filterer).SetFilter(a, "deploy")
	a.Update(runes("/"))
	if got := a.input.Value(); got != "" {
		t.Errorf("the filter box starts with %q, want it empty", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := a.top().(Filterer).FilterValue(); got != "deploy" {
		t.Errorf("esc should bring back the previous filter, got %q", got)
	}
}

func TestMOnACommitShowsItsWholeMessage(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs":      oneRun,
		"/repos/o/r/actions/workflows": `{"workflows":[]}`,
		"/repos/o/r/commits":           `[{"sha":"abcdef1234567","html_url":"u","commit":{"message":"fix: typo\n\nthe long explanation","author":{"name":"Ann","date":"2026-01-01T00:00:00Z"}}}]`,
	})
	a := apiApp(t)
	drive(a, a.openRepo("o/r", "", 0), 5*time.Second)
	for range 3 {
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyTab})
		drive(a, cmd, 5*time.Second)
	}
	_ = a.View()
	_, cmd := a.Update(runes("m"))
	drive(a, cmd, 5*time.Second)
	if out := ansi.Strip(a.View()); !strings.Contains(out, "the long explanation") {
		t.Errorf("m should show the whole commit message:\n%s", out)
	}
}

func TestTickedPullRequestsAreApprovedTogether(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/acme/web/pulls/1/reviews": "{}", "/repos/acme/web/pulls/2/reviews": "{}", "/repos/acme/web/pulls/3/reviews": "{}"})
	a := apiApp(t)
	now := time.Now()
	for i := 1; i <= 3; i++ {
		a.st.prs = append(a.st.prs, gh.PullRequest{Repo: "acme/web", Number: i, Title: fmt.Sprint("change ", i), UpdatedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	v := newPullsView(a, "")
	a.stack = append(a.stack, v)
	_ = a.View()
	pressKeys(a, runes("J"))
	ticked := v.marked(a)
	if len(ticked) != 2 || !strings.Contains(screen(a), "2 ticked") {
		t.Fatalf("J should tick two pull requests, got %d:\n%s", len(ticked), screen(a))
	}
	pressKeys(a, runes("A"))
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("A should ask before approving them all, modal is %T", a.modal)
	}
	pressKeys(a, runes("y"))
	for i := 1; i <= 3; i++ {
		want := 0
		for _, pr := range ticked {
			if pr.Number == i {
				want = 1
			}
		}
		if got := fake.count(fmt.Sprintf("/repos/acme/web/pulls/%d/reviews", i)); got != want {
			t.Errorf("pull request %d was reviewed %d times, want %d", i, got, want)
		}
	}
	if len(v.t.Marks()) != 0 {
		t.Errorf("the ticks should go once they were acted on")
	}
}
