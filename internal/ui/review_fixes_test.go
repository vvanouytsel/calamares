package ui

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestAnActionReReadsOnlyItsRepo(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun, "/repos/o/s/actions/runs": oneRun})
	a := apiApp(t)
	a.repos = append(a.repos, gh.Repo{FullName: "o/s", HasActions: true, PushedAt: time.Now()})
	a.st.runState("o/r").at, a.st.runState("o/s").at = time.Now(), time.Now()
	v := a.roots[tabActions]
	drive(a, a.setTab(tabActions), 5*time.Second)
	drive(a, a.refreshAfter(refreshNowMsg{v.ID(), "o/r"}), 5*time.Second)
	if fake.count("/repos/o/r/actions/runs") != 1 || fake.count("/repos/o/s/actions/runs") != 0 {
		t.Errorf("after an action in o/r: o/r read %d times, o/s %d; want 1 and 0",
			fake.count("/repos/o/r/actions/runs"), fake.count("/repos/o/s/actions/runs"))
	}
	a.setTab(tabIssues)
	if cmd := a.refreshAfter(refreshNowMsg{v.ID(), "o/r"}); cmd != nil {
		t.Errorf("a screen you left shouldn't be refreshed for an action")
	}
}

func TestLiveUpdatesDoNotRetryAFailingLoadEverySecond(t *testing.T) {
	fake := withGitHub(t, nil) // the issues answer 404
	a := apiApp(t)
	a.st.issuesState.at = time.Now().Add(-time.Hour)
	a.live = true
	tickFor(a, 5)
	if n := fake.count("/graphql"); n != 1 {
		t.Errorf("failing issues were asked for %d times in 5 seconds, want 1", n)
	}
}

func TestActionsTabReadsTheRepoListWhenItHasNone(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/graphql": `{"data":{"viewer":{"repositories":{"pageInfo":{"hasNextPage":false},
			"nodes":[{"nameWithOwner":"o/r","name":"r","owner":{"login":"o"},"pushedAt":"` + time.Now().Format(time.RFC3339) + `",
			"workflows":{"entries":[{"name":"ci.yml"}]}}]}}}}`,
		"/repos/o/r/actions/runs": oneRun,
	})
	a := apiApp(t)
	a.repos, a.st.loaded = nil, false
	drive(a, a.setTab(tabActions), 5*time.Second)
	if fake.count("/repos/o/r/actions/runs") != 1 {
		t.Errorf("the Actions tab should read the repo list, then o/r's runs")
	}
}

func TestShowingActionsReadsOnlyTheReposWithOldData(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun, "/repos/o/s/actions/runs": oneRun})
	a := apiApp(t)
	a.repos = append(a.repos, gh.Repo{FullName: "o/s", HasActions: true, PushedAt: time.Now()})
	a.st.runState("o/r").at = time.Now().Add(-time.Hour)
	a.st.runState("o/s").at = time.Now()
	drive(a, a.setTab(tabActions), 5*time.Second)
	if fake.count("/repos/o/r/actions/runs") != 1 || fake.count("/repos/o/s/actions/runs") != 0 {
		t.Errorf("only the old repo should be read: o/r %d, o/s %d", fake.count("/repos/o/r/actions/runs"), fake.count("/repos/o/s/actions/runs"))
	}
}

func TestChecksWithTheSameNameStayApart(t *testing.T) {
	a := apiApp(t)
	v := newPRView(a, "o/r", 1)
	v.pr.Checks = []gh.Check{{Name: "build", Kind: "failed", RunID: 1}, {Name: "build", Kind: "failed", RunID: 2}}
	v.loaded = true
	v.rebuild()
	v.checks.Render(80, 10)
	v.checks.Move(1)
	if c, ok := v.selectedCheck(); !ok || c.RunID != 2 {
		t.Errorf("the second build check should be run 2, got %+v", c)
	}
}

// pagedGitHub answers GraphQL with one page after another, then with an
// HTTP 500: a failure the client does not try again, so no retry is waited out.
type pagedGitHub struct {
	mu    sync.Mutex
	pages []string
	calls int
}

func (p *pagedGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body := `{"message":"boom"}`
	status := http.StatusInternalServerError
	if p.calls < len(p.pages) {
		body, status = p.pages[p.calls], http.StatusOK
	}
	p.calls++
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func sweepPageJSON(next bool, cursor string, repos ...string) string {
	var nodes []string
	for _, r := range repos {
		name, pushed, _ := strings.Cut(r, "@")
		nodes = append(nodes, `{"nameWithOwner":"`+name+`","pushedAt":"`+pushed+`","workflows":{"entries":[{"name":"ci.yml"}]}}`)
	}
	hasNext := "false"
	if next {
		hasNext = "true"
	}
	return `{"data":{"viewer":{"repositories":{"pageInfo":{"hasNextPage":` + hasNext + `,"endCursor":"` + cursor + `"},"nodes":[` + strings.Join(nodes, ",") + `]}}}}`
}

func withPages(t *testing.T, pages ...string) *pagedGitHub {
	p := &pagedGitHub{pages: pages}
	orig := http.DefaultTransport
	http.DefaultTransport = p
	t.Cleanup(func() { http.DefaultTransport = orig })
	return p
}

func TestSweepReadsPagesUntilReposGetOld(t *testing.T) {
	recent := time.Now().Format(time.RFC3339)
	old := time.Now().Add(-2 * 365 * 24 * time.Hour).Format(time.RFC3339)
	p := withPages(t,
		sweepPageJSON(true, "c1", "o/a@"+recent),
		sweepPageJSON(true, "c2", "o/b@"+old),
		sweepPageJSON(false, "", "o/c@"+old),
	)
	a := apiApp(t)
	a.repos, a.st.reposState.at = nil, time.Time{}
	drive(a, a.st.loadRepos(a), 5*time.Second)
	if p.calls != 2 || len(a.repos) != 2 || a.st.reposState.busy {
		t.Errorf("the sweep should stop after the page with an old repo: %d requests, %d repos, busy %v", p.calls, len(a.repos), a.st.reposState.busy)
	}
}

func TestASweepThatFailsHalfwayKeepsWhatItHad(t *testing.T) {
	recent := time.Now().Format(time.RFC3339)
	withPages(t, sweepPageJSON(true, "c1", "o/a@"+recent))
	a := apiApp(t)
	a.repos = []gh.Repo{{FullName: "o/known", HasActions: true, PushedAt: time.Now().Add(-time.Hour)}}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	drive(a, cmd, 5*time.Second)
	drive(a, a.st.loadRepos(a), 5*time.Second)
	if a.st.reposState.busy || a.st.reposState.err == nil || len(a.repos) != 2 {
		t.Errorf("busy %v, err %v, %d repos; want done, the error, and both repos", a.st.reposState.busy, a.st.reposState.err, len(a.repos))
	}
}
