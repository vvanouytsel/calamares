package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// A pull request that is behind its base and has one failing check, no
// review yet, a label, and a thread that can be resolved.
const behindPR = `{"data":{"viewer":{"login":"me"},"repository":{"squashMergeAllowed":true,"rebaseMergeAllowed":true,"autoMergeAllowed":true,"viewerPermission":"WRITE","pullRequest":{
	"id":"PR_node","number":12,"title":"feat: deploy","url":"https://github.com/o/r/pull/12","state":"OPEN","headRefName":"feat","baseRefName":"main",
	"headRefOid":"abc123","body":"Body","reviewDecision":"REVIEW_REQUIRED",
	"viewerCanUpdate":true,"viewerCanClose":true,"viewerCanReopen":false,"viewerCanLabel":true,"viewerCanAssign":true,
	"viewerCanEnableAutoMerge":true,"viewerCanDisableAutoMerge":false,"viewerCanUpdateBranch":CANBRANCH,
	"author":{"login":"you"},"repository":{"nameWithOwner":"o/r"},"mergeStateStatus":"BEHIND","mergeable":"MERGEABLE","changedFiles":2,
	"labels":{"nodes":[{"name":"bug","color":"d73a4a"}]},
	"reviewThreads":{"nodes":[{"id":"T1","isResolved":false,"viewerCanResolve":true,"viewerCanUnresolve":false,"comments":{"nodes":[{"databaseId":10}]}}]},
	"latest":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
		{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","checkSuite":{"workflowRun":{"databaseId":99,"workflow":{"name":"CI"}}}},
		{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS"}]}}}}]}}}}}`

func mergeBoxApp(t *testing.T, canBranch string) (*App, *prView, *recorder) {
	withGitHub(t, map[string]string{
		"/graphql":                                strings.ReplaceAll(behindPR, "CANBRANCH", canBranch),
		"/repos/o/r/issues/12/comments":           prIssueComments,
		"/repos/o/r/pulls/12/reviews":             prReviews,
		"/repos/o/r/pulls/12/comments":            prLineComments,
		"/repos/o/r/pulls/12/files":               prFiles,
		"/repos/o/r/pulls/12/commits":             `[{"sha":"abcdef123456","commit":{"message":"feat: deploy\n\nlong","author":{"name":"you","date":"2026-10-12T08:00:00Z"}},"author":{"login":"you"}}]`,
		"/repos/o/r/compare/main...feat":          `{"behind_by":3}`,
		"/repos/o/r/labels":                       `[{"name":"bug","color":"d73a4a"},{"name":"docs","color":"0075ca"}]`,
		"/repos/o/r/pulls/12/update-branch":       `{}`,
		"/repos/o/r/issues/12/labels":             `[]`,
		"/repos/o/r/pulls/12/comments/10/replies": "{}",
	})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := accessApp(t, "WRITE", "gho_x")
	v := newPRView(a, "o/r", 12)
	drive(a, a.push(v), 5*time.Second)
	_ = a.View()
	return a, v, rec
}

func TestMergeBoxListsWhatStandsBetweenThePullRequestAndAMerge(t *testing.T) {
	a, _, _ := mergeBoxApp(t, "true")
	out := screen(a)
	for _, want := range []string{"To merge", "1 of 4", "no conflicts", "checks  1 of 2 failing", "review  none yet", "3 behind main", "bug", "Reviewers"} {
		if !strings.Contains(out, want) {
			t.Errorf("the merge box lacks %q:\n%s", want, out)
		}
	}
}

func TestBehindBranchIsUpdatedFromTheMergeBoxKey(t *testing.T) {
	a, _, rec := mergeBoxApp(t, "true")
	pressKeys(a, runes("B"))
	if _, ok := a.modal.(*pickerModal); !ok {
		t.Fatalf("B should ask how to update, modal is %T", a.modal)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEnter}, runes("y"))
	if body := rec.body("/repos/o/r/pulls/12/update-branch"); !strings.Contains(body, `"expected_head_sha":"abc123"`) {
		t.Errorf("update-branch request: %q", body)
	}
}

func TestAKeyGitHubSaysYouCannotUseIsLockedWithItsReason(t *testing.T) {
	a, v, _ := mergeBoxApp(t, "false")
	if got := barOf(a, v.Keys(a)); strings.Contains(got, " B ") {
		t.Errorf("B should not be offered, got %q", got)
	}
	cmd, _ := a.press(v.Keys(a), "B")
	f := cmd().(flashMsg)
	if f.kind != nHint || !strings.Contains(f.text, "can't update this branch") {
		t.Errorf("B gave %+v", f)
	}
}

func TestLabelsArePickedWithTheirColoursAndSentAsOneList(t *testing.T) {
	a, _, _ := mergeBoxApp(t, "true")
	pressKeys(a, runes("l"))
	m, ok := a.modal.(*multiPicker)
	if !ok {
		t.Fatalf("l should open the label picker, modal is %T", a.modal)
	}
	out := screen(a)
	if !strings.Contains(out, "[x]") || !strings.Contains(out, "docs") {
		t.Errorf("picker:\n%s", out)
	}
	for i, it := range m.items {
		if it.value == "docs" {
			m.sel = indexOf(m.shown, i)
		}
	}
	pressKeys(a, runes(" "))
	if !strings.Contains(screen(a), "+ docs") {
		t.Errorf("the picker should say what will change:\n%s", screen(a))
	}
}

func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return 0
}

func TestLineCommentsAreHeldAndGoOutWithOneReview(t *testing.T) {
	a, v, rec := mergeBoxApp(t, "true")
	rec.next.(*countingGitHub).bodies["/repos/o/r/pulls/12/reviews"] = "{}"
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab}, runes("j"), runes("l"), runes("j"), runes("j"), runes("j"), runes("c"))
	pressKeys(a, runes("needs a test"), tea.KeyMsg{Type: tea.KeyCtrlS})
	if n := len(a.pendingFor("o/r", 12)); n != 1 {
		t.Fatalf("%d pending comments, want 1", n)
	}
	if !strings.Contains(screen(a), "1 pending") {
		t.Errorf("the strip should count the pending comment:\n%s", screen(a))
	}
	if rec.body("/repos/o/r/pulls/12/comments") != "" {
		t.Errorf("a pending comment was posted at once")
	}
	pressKeys(a, runes("a"))
	if !strings.Contains(screen(a), "with 1 pending comments") {
		t.Errorf("the confirmation should mention the pending comments:\n%s", screen(a))
	}
	pressKeys(a, runes("y"))
	body := rec.body("/repos/o/r/pulls/12/reviews")
	for _, want := range []string{`"event":"APPROVE"`, `"body":"needs a test"`, `"path":"src/deploy.sh"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the review lacks %s: %s", want, body)
		}
	}
	if len(a.pendingFor("o/r", 12)) != 0 {
		t.Errorf("sent comments should be forgotten")
	}
	_ = v
}

func TestACommitsTabListsTheCommits(t *testing.T) {
	a, _, _ := mergeBoxApp(t, "true")
	for range 4 {
		pressKeys(a, tea.KeyMsg{Type: tea.KeyTab})
	}
	out := screen(a)
	if !strings.Contains(out, "abcdef1") || !strings.Contains(out, "feat: deploy") || !strings.Contains(out, "Commits 1") {
		t.Errorf("commits tab:\n%s", out)
	}
}

func TestAThreadIsResolvedWithT(t *testing.T) {
	a, _, _ := mergeBoxApp(t, "true")
	pressKeys(a, runes("j"), runes("j"), runes("j"))
	cmd, _ := a.press(a.top().Keys(a), "t")
	_ = cmd
	out := screen(a)
	if !strings.Contains(out, "unresolved") {
		t.Errorf("the thread should be tagged unresolved:\n%s", out)
	}
}
