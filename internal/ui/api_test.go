package ui

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// countingGitHub serves fixed bodies by URL path, answers 304 to a matching
// If-None-Match, and counts every request per path.
type countingGitHub struct {
	mu     sync.Mutex
	bodies map[string]string
	hits   map[string]int
}

func (f *countingGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	body, ok := f.bodies[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	}
	h := http.Header{}
	etag := `"` + r.URL.Path + `"`
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		return &http.Response{StatusCode: http.StatusNotModified, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func (f *countingGitHub) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *countingGitHub) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.hits {
		n += c
	}
	return n
}

func withGitHub(t *testing.T, bodies map[string]string) *countingGitHub {
	f := &countingGitHub{bodies: bodies, hits: map[string]int{}}
	orig := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = orig })
	return f
}

const oneRun = `{"total_count":1,"workflow_runs":[{"id":7,"name":"CI","status":"completed","conclusion":"success","head_branch":"main"}]}`

// tickFor lets n seconds of clock ticks reach the app, running what they start.
func tickFor(a *App, n int) {
	for range n {
		_, cmd := a.Update(tickMsg(time.Now()))
		drive(a, cmd, 200*time.Millisecond) // long enough for the fake GitHub, short of the next tick
	}
}

func TestNothingIsRequestedWhileLiveUpdatesAreOff(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun})
	a := apiApp(t)
	a.tab, a.stack = tabActions, []View{a.roots[tabActions]}
	a.st.runState("o/r").at = time.Now().Add(-time.Hour)
	tickFor(a, 5)
	if n := fake.total(); n != 0 {
		t.Errorf("%d requests while idle with live updates off, want 0", n)
	}
}

func TestOpeningAScreenWithFreshDataRequestsNothing(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun})
	a := apiApp(t)
	a.st.runState("o/r").at = time.Now()
	drive(a, a.setTab(tabActions), 5*time.Second)
	if n := fake.total(); n != 0 {
		t.Errorf("%d requests opening a fresh screen, want 0", n)
	}
}

func TestOpeningAScreenWithOldDataRefreshesItOnce(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun})
	a := apiApp(t)
	a.st.runState("o/r").at = time.Now().Add(-time.Hour)
	drive(a, a.setTab(tabActions), 5*time.Second)
	drive(a, a.setTab(tabActions), 5*time.Second)
	if n := fake.count("/repos/o/r/actions/runs"); n != 1 {
		t.Errorf("runs were requested %d times, want 1", n)
	}
}

func TestRefreshReadsOnlyTheScreenOnDisplay(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun, "/graphql": `{"data":{}}`})
	a := apiApp(t)
	a.st.runState("o/r").at = time.Now()
	a.st.prsState.at = time.Now()
	a.tab, a.stack = tabPulls, []View{a.roots[tabPulls]}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	drive(a, cmd, 5*time.Second)
	if n, all := fake.count("/graphql"), fake.total(); n != 1 || all != 1 {
		t.Errorf("ctrl+r on pull requests sent %d of their requests and %d in all, want 1 and 1", n, all)
	}
}

func TestLiveUpdatesContinueWhileTheTerminalLosesFocus(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun})
	a := apiApp(t)
	a.tab, a.stack = tabActions, []View{a.roots[tabActions]}
	a.live = true
	a.Update(tea.BlurMsg{})
	a.st.runState("o/r").at = time.Now().Add(-time.Hour)
	tickFor(a, 3)
	if n := fake.count("/repos/o/r/actions/runs"); n < 1 {
		t.Errorf("the runs were not refreshed while the terminal was out of focus")
	}
}

func TestOpeningARepoRequestsItsRunsOnce(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs":      oneRun,
		"/repos/o/r/actions/workflows": `{"workflows":[]}`,
	})
	a := apiApp(t)
	drive(a, a.openRepo("o/r", "", 0), 5*time.Second)
	if n := fake.count("/repos/o/r/actions/runs"); n != 1 {
		t.Errorf("runs were requested %d times while opening the repo, want 1", n)
	}
}

func TestASweepOfOnePageIsOneRequest(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/graphql": `{"data":{"viewer":{"repositories":{
		"pageInfo":{"hasNextPage":false},
		"nodes":[{"nameWithOwner":"o/r","name":"r","owner":{"login":"o"},
			"defaultBranchRef":{"name":"main","target":{"history":{"nodes":[{"statusCheckRollup":{"state":"FAILURE"}}]}}},
			"workflows":{"entries":[{"name":"ci.yml"}]}}]}}}}`})
	a := apiApp(t)
	a.repos, a.st.reposState.at = nil, time.Time{}
	drive(a, a.setTab(tabRepos), 5*time.Second)
	if n := fake.total(); n != 1 {
		t.Errorf("the sweep sent %d requests, want 1", n)
	}
	if len(a.repos) != 1 || !a.repos[0].HasActions || a.repos[0].Health[0] != "FAILURE" {
		t.Errorf("repos = %+v", a.repos)
	}
}

func TestAWatchedRunPollsOnlyThatRunAndStaysListedWhenFinished(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs/7": `{"id":7,"name":"CI","status":"completed","conclusion":"success","html_url":"https://github.com/o/r/actions/runs/7"}`})
	var urls []string
	sendNotification = func(title, msg, url string) error { urls = append(urls, url); return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a := apiApp(t)
	a.watches[7] = &watch{repo: "o/r", run: ghRun(7, "in_progress")}
	drive(a, a.pollWatches(), 5*time.Second)
	drive(a, a.pollWatches(), 5*time.Second)
	if w, ok := a.watches[7]; !ok || w.run.Active() || fake.total() != 1 {
		t.Errorf("the finished watch should stay listed after one request: %+v, %d requests", a.watches[7], fake.total())
	}
	if len(urls) != 1 || urls[0] != "https://github.com/o/r/actions/runs/7" {
		t.Errorf("want one notification linking to the run, got %v", urls)
	}
}

func TestACancelledWatchedRunSendsNoNotification(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/o/r/actions/runs/7": `{"id":7,"name":"CI","status":"completed","conclusion":"cancelled"}`})
	sent := 0
	sendNotification = func(string, string, string) error { sent++; return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a := apiApp(t)
	a.watches[7] = &watch{repo: "o/r", run: ghRun(7, "in_progress")}
	drive(a, a.pollWatches(), 5*time.Second)
	if sent != 0 {
		t.Errorf("a cancelled run sent %d notifications, want 0", sent)
	}
}

func TestRunViewFetchesTheRunOnlyWhenItsJobsAreDone(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	v.jobsLoaded = true
	v.jobs = []gh.Job{{ID: 1, Status: "in_progress"}}
	cmd := v.Refresh(a, false)
	if cmd == nil || v.busy != 1 {
		t.Fatalf("a running run should poll its jobs only, busy = %d", v.busy)
	}
	v.busy = 0
	v.last = time.Time{}
	v.jobs = []gh.Job{{ID: 1, Status: "completed"}}
	_ = v.Refresh(a, false)
	if v.busy != 2 {
		t.Errorf("once every job is done the run should be fetched too, busy = %d", v.busy)
	}
}

func TestRunViewFetchesAQueuedRunAgainOnceAJobHasStarted(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "queued"))
	v.jobsLoaded = true
	v.jobs = []gh.Job{{ID: 1, Status: "queued"}}
	_ = v.Refresh(a, false)
	if v.busy != 1 {
		t.Fatalf("while nothing runs yet only the jobs should be polled, busy = %d", v.busy)
	}
	v.busy = 0
	v.last = time.Time{}
	v.jobs = []gh.Job{{ID: 1, Status: "in_progress"}, {ID: 2, Status: "queued"}}
	_ = v.Refresh(a, false)
	if v.busy != 2 {
		t.Errorf("a run still shown as queued while a job runs should be fetched again, busy = %d", v.busy)
	}
}

func TestAScreenFollowingARunningRunUpdatesEvenWithLiveUpdatesOff(t *testing.T) {
	fake := withGitHub(t, map[string]string{"/repos/o/r/actions/runs/7/jobs": `{"jobs":[{"id":1,"status":"in_progress"}]}`})
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	a.stack = append(a.stack, v)
	v.last = time.Time{}
	tickFor(a, 1)
	if n := fake.count("/repos/o/r/actions/runs/7/jobs"); n != 1 {
		t.Errorf("a running run was polled %d times with live updates off, want 1", n)
	}
	if got := ansi.Strip(a.liveState()); !strings.Contains(got, "live") {
		t.Errorf("the header should say it is live, got %q", got)
	}
}

func TestFollowingARunKeepsTheCursorOnTheRunningStep(t *testing.T) {
	a := apiApp(t)
	v := newRunView(a, "o/r", ghRun(7, "in_progress"))
	steps := func(done int) []gh.Step {
		out := []gh.Step{{Name: "checkout", Number: 1}, {Name: "build", Number: 2}, {Name: "test", Number: 3}}
		for i := range out {
			out[i].Status, out[i].Conclusion = "queued", ""
			if i < done {
				out[i].Status, out[i].Conclusion = "completed", "success"
			}
			if i == done {
				out[i].Status = "in_progress"
			}
		}
		return out
	}
	v.jobsLoaded = true
	v.jobs = []gh.Job{{ID: 1, Name: "build", Status: "in_progress", Steps: steps(1)}}
	v.rebuild()
	v.key(a, "f")
	if !v.follow || v.items[v.cursor].kind != tStep || v.items[v.cursor].step != 1 {
		t.Fatalf("follow should select the running step, follow=%v cursor=%d", v.follow, v.cursor)
	}
	v.jobs[0].Steps = steps(2)
	v.followRunning(a)
	if v.items[v.cursor].step != 2 {
		t.Errorf("after a step finished the cursor should follow to the next, on step %d", v.items[v.cursor].step)
	}
	v.key(a, "up")
	if v.follow {
		t.Errorf("moving the cursor should stop following")
	}
}

func TestRunViewStepsThroughTheAttemptsOfARun(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs/7/attempts/2/jobs": `{"jobs":[{"id":22,"name":"build","status":"completed","conclusion":"failure"}]}`,
		"/repos/o/r/actions/runs/7/attempts/2":      `{"id":7,"name":"CI","status":"completed","conclusion":"failure","run_attempt":2}`,
	})
	a := apiApp(t)
	run := ghRun(7, "completed")
	run.RunAttempt = 3
	v := newRunView(a, "o/r", run)
	a.stack = append(a.stack, v)
	drive(a, v.key(a, "["), 5*time.Second)
	if v.attempt != 2 || len(v.jobs) != 1 || v.jobs[0].ID != 22 {
		t.Fatalf("[ should show attempt 2 with its jobs, attempt=%d jobs=%v", v.attempt, v.jobs)
	}
	if fake.count("/repos/o/r/actions/runs/7/attempts/2/jobs") != 1 {
		t.Errorf("the jobs of attempt 2 were not requested")
	}
	if got := v.shownRun(); got.Conclusion != "failure" {
		t.Errorf("the header should describe attempt 2, got conclusion %q", got.Conclusion)
	}
	v.key(a, "]")
	if v.attempt != 0 {
		t.Errorf("] on the second of three attempts should return to the latest, attempt = %d", v.attempt)
	}
}

func apiApp(t *testing.T) *App {
	t.Setenv("CALAMARES_CONFIG", t.TempDir()+"/config.yaml")
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	a.repos = []gh.Repo{{FullName: "o/r", HasActions: true, PushedAt: time.Now().Add(-time.Hour)}}
	a.st.loaded = true
	a.st.reposState.at = time.Now()
	a.tab = tabIssues
	a.stack = []View{a.roots[tabIssues]}
	a.st.issuesState.at = time.Now()
	return a
}

func ghRun(id int64, status string) gh.Run {
	r := gh.Run{ID: id, Name: "CI", Status: status}
	if status == "completed" {
		r.Conclusion = "success"
	}
	return r
}

func TestRefreshWithAFilterReadsOnlyTheMatchingRepos(t *testing.T) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/a/actions/runs": oneRun,
		"/repos/o/b/actions/runs": oneRun,
	})
	a := apiApp(t)
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "o/a", HasActions: true, PushedAt: now}, {FullName: "o/b", HasActions: true, PushedAt: now}}
	for repo, title := range map[string]string{"o/a": "alpha release", "o/b": "beta release"} {
		a.st.status[repo] = []gh.Run{{ID: int64(len(title)), Name: "CI", DisplayTitle: title, Status: "completed", Conclusion: "success", CreatedAt: now}}
		a.st.runState(repo).at = now
	}
	a.roots[tabActions].(*activityView).filter = viewRecent
	drive(a, a.setTab(tabActions), 5*time.Second)
	a.top().(Filterer).SetFilter(a, "alpha")
	_ = a.View()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	drive(a, cmd, 5*time.Second)
	if fake.count("/repos/o/a/actions/runs") != 1 || fake.count("/repos/o/b/actions/runs") != 0 {
		t.Errorf("a filtered refresh read o/a %d times and o/b %d times, want 1 and 0",
			fake.count("/repos/o/a/actions/runs"), fake.count("/repos/o/b/actions/runs"))
	}
}
