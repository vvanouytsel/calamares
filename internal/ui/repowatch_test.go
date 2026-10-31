package ui

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

const ciFile, nightlyFile = ".github/workflows/ci.yml", ".github/workflows/nightly.yml"

// repoWatchApp watches acme/webshop, whose default branch is main.
func repoWatchApp(t *testing.T, w config.RepoWatch) *App {
	a := apiApp(t)
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: time.Now()}}
	w.Repo = "acme/webshop"
	a.cfg.SetRepoWatch(w)
	return a
}

func webshopRun(id int64, file, branch, status, conclusion string, created time.Time) gh.Run {
	return gh.Run{ID: id, Name: workflowFileName(file), Path: file, HeadBranch: branch, Status: status, Conclusion: conclusion,
		RunAttempt: 1, CreatedAt: created, UpdatedAt: created}
}

func pollWebshop(a *App, runs ...gh.Run) {
	a.repoWatch("acme/webshop")
	drive(a, a.handleRepoWatchPolled(repoWatchPolledMsg{repo: "acme/webshop", runs: runs}), time.Second)
}

func TestAWatchedRepoWatchesItsNewRunsOfTheChosenWorkflowsAndBranches(t *testing.T) {
	a := repoWatchApp(t, config.RepoWatch{Workflows: []string{ciFile}})
	now := time.Now()
	pollWebshop(a,
		webshopRun(1, ciFile, "main", "in_progress", "", now),
		webshopRun(2, nightlyFile, "main", "in_progress", "", now),
		webshopRun(3, ciFile, "feat/login", "in_progress", "", now),
		webshopRun(4, ciFile, "main", "completed", "success", now.Add(-time.Hour)),
	)
	if w, ok := a.watches[1]; !ok || !w.fromRepo {
		t.Errorf("the running ci run on main should be watched, found by the repo watch: %+v", a.watches)
	}
	for _, id := range []int64{2, 3, 4} {
		if _, ok := a.watches[id]; ok {
			t.Errorf("run %d should not be watched: another workflow, another branch, or finished before the watch", id)
		}
	}
	if len(a.toasts) != 1 || a.toasts[0].kind != nStarted {
		t.Errorf("a new run should show a card that it started, got %d cards", len(a.toasts))
	}
}

func TestOnlyAFailedRunOfAWatchedRepoSendsANotification(t *testing.T) {
	sent := 0
	sendNotification = func(string, string, string) error { sent++; return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a := repoWatchApp(t, config.RepoWatch{})
	later := time.Now().Add(time.Minute)
	pollWebshop(a,
		webshopRun(1, ciFile, "main", "completed", "success", later),
		webshopRun(2, nightlyFile, "main", "completed", "failure", later),
	)
	if len(a.watches) != 2 {
		t.Fatalf("both runs finished after the watch started and should be listed, got %d", len(a.watches))
	}
	if sent != 1 || len(a.toasts) != 1 || a.toasts[0].kind != nError {
		t.Errorf("only the failure should be told: %d notifications, %d cards", sent, len(a.toasts))
	}
}

func TestARunOfAWatchedRepoYouStopWatchingStaysAway(t *testing.T) {
	a := repoWatchApp(t, config.RepoWatch{})
	run := webshopRun(1, ciFile, "main", "in_progress", "", time.Now())
	pollWebshop(a, run)
	a.watch("acme/webshop", run)
	pollWebshop(a, run)
	if _, ok := a.watches[1]; ok {
		t.Error("a run you stopped watching came back with the next read of its repo")
	}
}

func TestStoppingARepoWatchTakesItsRunsOffWatching(t *testing.T) {
	a := repoWatchApp(t, config.RepoWatch{})
	a.watches[9] = &watch{repo: "acme/webshop", run: webshopRun(9, ciFile, "main", "in_progress", "", time.Now())}
	pollWebshop(a, webshopRun(1, ciFile, "main", "in_progress", "", time.Now()))
	a.stopRepoWatch("acme/webshop")
	if _, ok := a.cfg.RepoWatch("acme/webshop"); ok {
		t.Error("the repo should no longer be watched")
	}
	if _, ok := a.watches[1]; ok {
		t.Error("the run the repo watch found should go with it")
	}
	if _, ok := a.watches[9]; !ok {
		t.Error("a run you watched yourself should stay")
	}
}

func TestAFinishedRunOfAWatchedRepoLeavesAfterTwoHours(t *testing.T) {
	a := repoWatchApp(t, config.RepoWatch{})
	old := webshopRun(1, ciFile, "main", "completed", "success", time.Now().Add(-3*time.Hour))
	mine := old
	mine.ID = 2
	a.watches[1] = &watch{repo: "acme/webshop", run: old, fromRepo: true, notified: true, since: time.Now()}
	a.watches[2] = &watch{repo: "acme/webshop", run: mine, notified: true, since: time.Now()}
	a.dropOldFromRepo(time.Now())
	if _, ok := a.watches[1]; ok {
		t.Error("a run the repo watch found should leave two hours after it finished")
	}
	if _, ok := a.watches[2]; !ok {
		t.Error("a run you watched yourself stays a day")
	}
}

func TestLowerWOnARepoPicksTheWorkflowsAndBranchesToWatch(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/acme/webshop/actions/workflows": `{"workflows":[{"id":1,"name":"CI","path":".github/workflows/ci.yml"},{"id":2,"name":"Nightly","path":".github/workflows/nightly.yml"}]}`})
	a := apiApp(t)
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: time.Now()}}
	a.tab, a.stack = tabRepos, []View{a.roots[tabRepos]}
	a.roots[tabRepos].(*reposView).sync(a)
	press(a, "w")
	if _, ok := a.modal.(*repoWatchModal); !ok {
		t.Fatalf("w should open the picker, got %T", a.modal)
	}
	out := ansi.Strip(a.View())
	for _, want := range []string{"Watch acme/webshop", "every workflow, also new ones", "Nightly", "default (main)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker lacks %q:\n%s", want, out)
		}
	}
	press(a, "down", "down", " ", "down", "right", "down", "release/*", "enter")
	w, ok := a.cfg.RepoWatch("acme/webshop")
	if !ok || strings.Join(w.Workflows, " ") != ciFile || strings.Join(w.Branches, " ") != "release/*" {
		t.Errorf("the watch should keep ci on release/*, got %+v", w)
	}
	saved, err := config.Load()
	if err != nil || len(saved.WatchedRepos) != 1 {
		t.Errorf("the watch should be saved in the config file: %+v %v", saved, err)
	}
}

func TestThePickerWantsAWorkflowAndABranch(t *testing.T) {
	m := newRepoWatchModal("acme/webshop", "main", []gh.Workflow{{Name: "CI", Path: ciFile}}, config.RepoWatch{Workflows: []string{ciFile}}, true)
	m.ticked[ciFile] = false
	if _, err := m.result(); err == "" {
		t.Error("with no workflow ticked the watch can't be saved")
	}
	m.all, m.branches = true, branchesThese
	if _, err := m.result(); err == "" {
		t.Error("with no branch named the watch can't be saved")
	}
}

func TestWatchingListsTheWatchedReposInItsOwnView(t *testing.T) {
	a := repoWatchApp(t, config.RepoWatch{AllBranches: true})
	pollWebshop(a, webshopRun(1, ciFile, "feat/login", "in_progress", "", time.Now()))
	a.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	press(a, "4")
	v := a.roots[tabWatching].(*watchView)
	v.sync(a)
	if out := ansi.Strip(a.View()); !strings.Contains(out, "VIA") || !strings.Contains(out, "Repos 1") {
		t.Errorf("Watching should say how a run came to be watched and count the watched repos:\n%s", out)
	}
	press(a, "tab", "tab") // past Pull requests
	out := ansi.Strip(a.View())
	for _, want := range []string{"› Repos", "acme/webshop", "every workflow", "every branch", "1 running"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Repos view lacks %q:\n%s", want, out)
		}
	}
}

func TestWatchingARepoWatchesTheRunsAlreadyGoing(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/acme/webshop/actions/workflows": `{"workflows":[{"id":1,"name":"CI","path":".github/workflows/ci.yml"}]}`,
		"/repos/acme/webshop/actions/runs":      `{"total_count":1,"workflow_runs":[{"id":5,"name":"CI","path":".github/workflows/ci.yml","head_branch":"main","status":"in_progress","run_attempt":1}]}`,
	})
	a := apiApp(t)
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: time.Now()}}
	a.tab, a.stack = tabRepos, []View{a.roots[tabRepos]}
	a.roots[tabRepos].(*reposView).sync(a)
	press(a, "w", "enter")
	if w, ok := a.watches[5]; !ok || !w.fromRepo {
		t.Errorf("the run already going should be watched as soon as the repo is: %+v", a.watches)
	}
}

func TestUntickingEveryWorkflowUnticksThemAll(t *testing.T) {
	wfs := []gh.Workflow{{Name: "CI", Path: ciFile}, {Name: "Nightly", Path: nightlyFile}}
	m := newRepoWatchModal("acme/webshop", "main", wfs, config.RepoWatch{}, false)
	m.toggle()
	if m.all || m.ticked[ciFile] || m.ticked[nightlyFile] {
		t.Errorf("unticking every workflow should leave none ticked: all %v, ticked %v", m.all, m.ticked)
	}
	if _, err := m.result(); err == "" {
		t.Error("with none ticked the watch can't be saved yet")
	}
}

// runsByStatus answers the runs of acme/webshop by the status asked for:
// the latest runs when none is.
type runsByStatus map[string]string

func (r runsByStatus) RoundTrip(req *http.Request) (*http.Response, error) {
	body := r[req.URL.Query().Get("status")]
	if body == "" {
		body = `{"total_count":0,"workflow_runs":[]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func withRunsByStatus(t *testing.T, r runsByStatus) {
	orig := http.DefaultTransport
	http.DefaultTransport = r
	t.Cleanup(func() { http.DefaultTransport = orig })
}

const ciGoingOnRelease = `{"total_count":1,"workflow_runs":[{"id":5,"name":"ci","path":".github/workflows/ci.yml","head_branch":"release/2.1","status":"in_progress","run_attempt":1}]}`

func TestWatchingARepoWatchesARunningRunPastItsLatestRuns(t *testing.T) {
	withRunsByStatus(t, runsByStatus{"in_progress": ciGoingOnRelease})
	a := repoWatchApp(t, config.RepoWatch{AllBranches: true})
	drive(a, a.pollRepoWatches(), 5*time.Second)
	if _, ok := a.watches[5]; !ok {
		t.Error("a run in progress should be watched as soon as the repo is, even past its latest runs")
	}
}

func TestChangingARepoWatchWatchesTheRunningRunsThatNowMatch(t *testing.T) {
	withRunsByStatus(t, runsByStatus{"in_progress": ciGoingOnRelease})
	a := repoWatchApp(t, config.RepoWatch{})
	drive(a, a.pollRepoWatches(), 5*time.Second)
	if _, ok := a.watches[5]; ok {
		t.Fatal("a run on release/2.1 is not on the default branch")
	}
	drive(a, a.setRepoWatch(config.RepoWatch{Repo: "acme/webshop", Branches: []string{"release/*"}}), 5*time.Second)
	if _, ok := a.watches[5]; !ok {
		t.Error("once release/* is watched, its run in progress should be watched")
	}
}
