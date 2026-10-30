package gh

import (
	"context"
	"net/http"
	"testing"
)

func TestARunThatStartedStaysInProgressWhenGitHubCallsItQueued(t *testing.T) {
	status := "in_progress"
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":1,"workflow_runs":[{"id":7,"run_attempt":1,"status":"` + status + `"}]}`))
	})
	ctx := context.Background()
	if _, _, err := c.ListRuns(ctx, "acme/webshop", RunFilter{}); err != nil {
		t.Fatal(err)
	}
	status = "queued"
	runs, _, err := c.ListRuns(ctx, "acme/webshop", RunFilter{PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Status != "in_progress" || !runs[0].Requeued {
		t.Errorf("a started run called queued should stay in progress and say why: %+v", runs[0])
	}
}

func TestARunThatNeverStartedStaysQueued(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	runs := []Run{{ID: 7, RunAttempt: 1, Status: "queued"}}
	c.KeepStarted(runs)
	if runs[0].Status != "queued" || runs[0].Requeued {
		t.Errorf("a run that never started should stay queued: %+v", runs[0])
	}
}

func TestANewAttemptOfAStartedRunStartsQueued(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	c.KeepStarted([]Run{{ID: 7, RunAttempt: 1, Status: "in_progress"}})
	rerun := []Run{{ID: 7, RunAttempt: 2, Status: "queued"}}
	c.KeepStarted(rerun)
	if rerun[0].Status != "queued" {
		t.Errorf("a rerun waiting for its first runner is queued: %+v", rerun[0])
	}
}

func TestAStartedJobMarksItsRunAsStarted(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[{"id":1,"run_id":7,"run_attempt":1,"status":"completed"},{"id":2,"run_id":7,"run_attempt":1,"status":"queued"}]}`))
	})
	if _, err := c.ListJobs(context.Background(), "acme/webshop", 7); err != nil {
		t.Fatal(err)
	}
	if !c.HasStarted(Run{ID: 7, RunAttempt: 1}) {
		t.Error("a run with a finished job has started")
	}
}

func TestStartedRunsAreKeptForTheNextSession(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	c.KeepStarted([]Run{{ID: 7, RunAttempt: 1, Status: "in_progress"}})
	next := NewWithToken("github.com", "x", "test")
	next.cache = c.cache
	if !next.HasStarted(Run{ID: 7, RunAttempt: 1}) {
		t.Error("the next session should still know the run started")
	}
}

func TestAFinishedRunIsForgotten(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	c.KeepStarted([]Run{{ID: 7, RunAttempt: 1, Status: "in_progress"}})
	c.KeepStarted([]Run{{ID: 7, RunAttempt: 1, Status: "completed", Conclusion: "success"}})
	if c.HasStarted(Run{ID: 7, RunAttempt: 1}) {
		t.Error("a finished run should be forgotten")
	}
}
