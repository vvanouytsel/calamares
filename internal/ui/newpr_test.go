package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestReadableBranchMakesATitle(t *testing.T) {
	for in, want := range map[string]string{"delivery-estimates": "Delivery estimates", "feat/show_eta": "Show eta", "fix": "Fix"} {
		if got := readableBranch(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestTheFormIsPrefilledFromTheTemplateOrTheCommits(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	f := a.prForm(prDraftInfo{repo: "o/r", head: "delivery-estimates", base: "main", branches: []string{"main", "dev"}, subjects: []string{"feat: eta", "test: eta"}})
	if f.field("Title").value() != "Delivery estimates" || !strings.Contains(f.field("Description").value(), "- feat: eta") {
		t.Errorf("two commits: title %q body %q", f.field("Title").value(), f.field("Description").value())
	}
	f = a.prForm(prDraftInfo{repo: "o/r", head: "x", base: "main", branches: []string{"main"}, subjects: []string{"feat: only one"}, template: "## What\n\n## Why"})
	if f.field("Title").value() != "feat: only one" || !strings.Contains(f.field("Description").value(), "## Why") {
		t.Errorf("one commit and a template: title %q body %q", f.field("Title").value(), f.field("Description").value())
	}
}

func TestUnpushedCommitsAreOfferedAndCreatingOpensThePullRequest(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/o/r/pulls": `{"number":58}`, "/graphql": reviewedPR,
		"/repos/o/r/issues/58/comments": "[]", "/repos/o/r/pulls/58/reviews": "[]", "/repos/o/r/pulls/58/comments": "[]", "/repos/o/r/actions/runs": oneRun})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := accessApp(t, "WRITE", "gho_x")
	f := a.prForm(prDraftInfo{repo: "o/r", head: "feat", base: "main", branches: []string{"main"}, local: true, unpushed: 2, subjects: []string{"feat: a"}})
	if len(f.fields) == 0 || !strings.Contains(f.fields[0].label, "2 commits not pushed") || !f.fields[0].on {
		t.Fatalf("the first line should offer to push: %+v", f.fields[0])
	}
	f.fields[0].on = false // no git in this test
	a.modal = f
	pressKeys(a, tea.KeyMsg{Type: tea.KeyCtrlS})
	body := rec.body("/repos/o/r/pulls")
	for _, want := range []string{`"title":"feat: a"`, `"head":"feat"`, `"base":"main"`, `"draft":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("create request lacks %s: %s", want, body)
		}
	}
	if _, ok := a.top().(*prView); !ok {
		t.Errorf("the new pull request should open, top is %T", a.top())
	}
}

func TestTheRepoOffersAPullRequestForABranchWithoutOne(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	a.opts.FromCwd, a.opts.StartRepo, a.opts.Branch = true, "o/r", "delivery-estimates"
	a.repos[0].DefaultBranch = "main"
	v := newRepoView(a, "o/r", "")
	if v.branchHint(a) != "" {
		t.Errorf("no hint before the pull requests are known")
	}
	v.pullsList(a).at = time.Now()
	if h := screenOf(capsOff(v.branchHint(a))); !strings.Contains(h, "delivery-estimates has no pull request") || !strings.Contains(h, "+ create") {
		t.Errorf("hint %q", h)
	}
	v.pulls.prs = []gh.PullRequest{{Branch: "delivery-estimates"}}
	if v.branchHint(a) != "" {
		t.Errorf("no hint when the branch has a pull request")
	}
}
