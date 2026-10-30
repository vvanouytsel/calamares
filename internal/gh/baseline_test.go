package gh

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func jobsBody(seconds ...int) string {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var jobs []string
	for i, s := range seconds {
		jobs = append(jobs, fmt.Sprintf(`{"id":%d,"name":"build","conclusion":"success","started_at":%q,"completed_at":%q}`,
			i, start.Format(time.RFC3339), start.Add(time.Duration(s)*time.Second).Format(time.RFC3339)))
	}
	return `{"jobs":[` + strings.Join(jobs, ",") + `]}`
}

func TestBaselineIsTheMedianOfEarlierSuccessfulRunsWithoutTheCurrentOne(t *testing.T) {
	durations := map[string]int{"1": 10, "2": 100, "3": 30, "4": 20}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/workflows/7/runs"):
			if r.URL.Query().Get("status") != "success" || r.URL.Query().Get("event") != "push" {
				t.Errorf("runs asked for with %q, want successful push runs", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1},{"id":2},{"id":3},{"id":4}]}`))
		default:
			parts := strings.Split(r.URL.Path, "/")
			id := parts[len(parts)-2]
			if id == "1" {
				t.Error("the current run was read for its jobs")
			}
			_, _ = w.Write([]byte(jobsBody(durations[id])))
		}
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 1, time.Time{})
	got := b.Jobs
	if err != nil {
		t.Fatal(err)
	}
	if got["build"].Median != 30*time.Second || got["build"].Samples != 3 {
		t.Errorf("build typical = %+v, want median 30s of 3 runs", got["build"])
	}
}

func TestBaselineHasNoMedianFromTooFewSamplesButStillThePreviousRun(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1},{"id":2}]}`))
			return
		}
		_, _ = w.Write([]byte(jobsBody(10)))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	got := b.Jobs
	if err != nil {
		t.Fatal(err)
	}
	if got["build"].Samples != 0 || got["build"].Median != 0 || got["build"].Previous != 10*time.Second {
		t.Errorf("got %v, want no median from two runs, only the previous run's 10s", got)
	}
}

func TestBaselineReadsAtMostTheCapOfRuns(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			var runs []string
			for i := 1; i <= 30; i++ {
				runs = append(runs, fmt.Sprintf(`{"id":%d}`, i))
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[` + strings.Join(runs, ",") + `]}`))
			return
		}
		_, _ = w.Write([]byte(jobsBody(10)))
	})
	if _, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 1+baselineRuns {
		t.Errorf("GitHub got %d requests, want %d", n, 1+baselineRuns)
	}
}

func TestBaselineHoldsTheMedianOfEachStepAndKeepsRepeatedNamesApart(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	step := func(name string, seconds int) string {
		return fmt.Sprintf(`{"name":%q,"conclusion":"success","started_at":%q,"completed_at":%q}`,
			name, start.Format(time.RFC3339), start.Add(time.Duration(seconds)*time.Second).Format(time.RFC3339))
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1},{"id":2},{"id":3}]}`))
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		n := int(parts[len(parts)-2][0] - '0')
		_, _ = w.Write([]byte(fmt.Sprintf(`{"jobs":[{"name":"build","conclusion":"success","started_at":%q,"completed_at":%q,"steps":[%s,%s,%s]}]}`,
			start.Format(time.RFC3339), start.Add(time.Minute).Format(time.RFC3339),
			step("Checkout", 5*n), step("Test", 10), step("Test", 20))))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	got := b.Jobs
	if err != nil {
		t.Fatal(err)
	}
	steps := got["build"].Steps
	if steps["Checkout"].Median != 10*time.Second || steps["Checkout"].Samples != 3 {
		t.Errorf("Checkout typical = %+v, want 10s of 3 runs", steps["Checkout"])
	}
	if steps["Test"].Median != 10*time.Second || steps["Test #2"].Median != 20*time.Second {
		t.Errorf("repeated steps got %+v and %+v, want 10s and 20s apart", steps["Test"], steps["Test #2"])
	}
}

func TestBaselineSkipsARunWhoseJobsCannotBeRead(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1},{"id":2},{"id":3},{"id":4}]}`))
		case strings.Contains(r.URL.Path, "/runs/2/"):
			http.Error(w, `{"message":"gone"}`, http.StatusNotFound)
		default:
			_, _ = w.Write([]byte(jobsBody(10)))
		}
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	got := b.Jobs
	if err != nil || got["build"].Samples != 3 {
		t.Errorf("got %+v, %v, want a baseline of the 3 readable runs", got["build"], err)
	}
}

func TestBaselineFailsWhenNoRunCanBeRead(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1}]}`))
			return
		}
		http.Error(w, `{"message":"gone"}`, http.StatusNotFound)
	})
	if _, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{}); err == nil {
		t.Error("got no error, want the failure of reading the jobs")
	}
}

func TestThePreviousRunIsTheNewestThatPassedBeforeTheRunLookedAt(t *testing.T) {
	durations := map[string]int{"5": 50, "4": 40, "3": 30, "2": 20}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workflows/7/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"id":5,"run_number":5,"created_at":"2026-01-05T00:00:00Z"},
				{"id":4,"run_number":4,"created_at":"2026-01-04T00:00:00Z"},
				{"id":3,"run_number":3,"created_at":"2026-01-03T00:00:00Z"},
				{"id":2,"run_number":2,"created_at":"2026-01-02T00:00:00Z"}]}`))
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		_, _ = w.Write([]byte(jobsBody(durations[parts[len(parts)-2]])))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if b.Previous == nil || b.Previous.RunNumber != 3 {
		t.Fatalf("previous = %+v, want run 3, the newest before the 3rd at noon", b.Previous)
	}
	if got := b.Jobs["build"]; got.Previous != 30*time.Second || got.Median != 40*time.Second {
		t.Errorf("build = %+v, want 30s in the previous run and a median of 40s", got)
	}
}

func TestAJobNamedAfterAVersionIsComparedWithItsOtherVersions(t *testing.T) {
	versions := map[string]string{"1": "2.45.0", "2": "2.44.0", "3": "2.43.1", "4": "2.42.0"}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workflows/7/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1},{"id":2},{"id":3},{"id":4}]}`))
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		id := parts[len(parts)-2]
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		_, _ = fmt.Fprintf(w, `{"jobs":[{"id":%s,"name":"Run storefront (%s) smoke tests","conclusion":"success","started_at":%q,"completed_at":%q}]}`,
			id, versions[id], start.Format(time.RFC3339), start.Add(time.Minute).Format(time.RFC3339))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := TypicalFor(b.Jobs, "Run storefront (2.46.0) smoke tests")
	if !ok || got.Median != time.Minute || got.Samples != 4 {
		t.Errorf("a new version should be compared with the four before it: %+v, %v", got, ok)
	}
	if got.Previous != time.Minute {
		t.Errorf("a new version should be compared with the version in the previous run: %+v", got)
	}
	if exact, _ := TypicalFor(b.Jobs, "Run storefront (2.45.0) smoke tests"); exact.Previous != time.Minute {
		t.Errorf("the job of the same name in the previous run still counts as the previous one: %+v", exact)
	}
}

func TestANewVersionIsComparedWithThePreviousRunEvenWithTooFewRunsForAMedian(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workflows/7/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1}]}`))
			return
		}
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		_, _ = fmt.Fprintf(w, `{"jobs":[{"id":1,"name":"deploy (2.45.0)","conclusion":"success","started_at":%q,"completed_at":%q,
			"steps":[{"name":"helm","conclusion":"success","started_at":%q,"completed_at":%q}]}]}`,
			start.Format(time.RFC3339), start.Add(time.Minute).Format(time.RFC3339), start.Format(time.RFC3339), start.Add(20*time.Second).Format(time.RFC3339))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := TypicalFor(b.Jobs, "deploy (2.46.0)")
	if !ok || got.Previous != time.Minute || got.Steps["helm"].Previous != 20*time.Second {
		t.Errorf("deploy and its steps should be compared with the version in the previous run: %+v, %v", got, ok)
	}
}

func TestLegsThatDifferOnlyInTheirNumbersAreNotTakenForOneAnother(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workflows/7/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1}]}`))
			return
		}
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		job := func(id int, name string, took time.Duration) string {
			return fmt.Sprintf(`{"id":%d,"name":%q,"conclusion":"success","started_at":%q,"completed_at":%q}`,
				id, name, start.Format(time.RFC3339), start.Add(took).Format(time.RFC3339))
		}
		_, _ = w.Write([]byte(`{"jobs":[` + job(1, "shard 1", time.Minute) + "," + job(2, "shard 2", 5*time.Minute) + `]}`))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := TypicalFor(b.Jobs, "shard 3"); got.Previous != 0 {
		t.Errorf("shard 3 is new and could be either shard of the previous run, yet compares with %s", got.Previous)
	}
	if got, _ := TypicalFor(b.Jobs, "shard 2"); got.Previous != 5*time.Minute {
		t.Errorf("shard 2 should still compare with itself: %+v", got)
	}
}

func TestBaselineHoldsHowLongTheWholeRunUsuallyTakesAndTookLastTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := func(id, minutes int) string {
		return fmt.Sprintf(`{"id":%d,"created_at":%q,"run_started_at":%q,"updated_at":%q}`, id,
			start.Format(time.RFC3339), start.Format(time.RFC3339), start.Add(time.Duration(minutes)*time.Minute).Format(time.RFC3339))
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[` + strings.Join([]string{run(1, 99), run(2, 5), run(3, 3), run(4, 4)}, ",") + `]}`))
			return
		}
		_, _ = w.Write([]byte(jobsBody(10)))
	})
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{}, 1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Run.Median != 4*time.Minute || b.Run.Samples != 3 || b.Run.Previous != 5*time.Minute {
		t.Errorf("the whole run = %+v, want a median of 4m from 3 runs, without the current one, and 5m for the previous run", b.Run)
	}
}

// branchRuns answers the runs of workflow 7: a, b and c on feature branches,
// m1 to m3 on main, and records the query it was asked with; every job takes
// as many seconds as its run's id.
func branchRuns(t *testing.T, query *string) *Client {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workflows/7/runs") {
			*query = r.URL.RawQuery
			if r.URL.Query().Get("branch") == "main" {
				_, _ = w.Write([]byte(`{"workflow_runs":[{"id":11,"head_branch":"main"},{"id":12,"head_branch":"main"},{"id":13,"head_branch":"main"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":11,"head_branch":"main"},{"id":21,"head_branch":"feat/a"},{"id":22,"head_branch":"feat/b"},{"id":12,"head_branch":"main"},{"id":23,"head_branch":"feat/a"}]}`))
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		var id int
		fmt.Sscan(parts[len(parts)-2], &id)
		_, _ = w.Write([]byte(jobsBody(id)))
	})
	return c
}

func TestARunOnTheDefaultBranchIsComparedWithRunsOnItOnly(t *testing.T) {
	var query string
	c := branchRuns(t, &query)
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{Default: "main", OnDefault: true}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "branch=main") {
		t.Errorf("the runs should be asked for on main: %q", query)
	}
	if got := b.Jobs["build"]; got.Median != 12*time.Second || got.Samples != 3 {
		t.Errorf("build typical = %+v, want the median of the 3 runs on main", got)
	}
}

func TestARunOnABranchIsComparedWithRunsOnOtherBranchesButNotTheDefault(t *testing.T) {
	var query string
	c := branchRuns(t, &query)
	b, err := c.JobBaseline(context.Background(), "o/r", 7, "push", BranchScope{Default: "main"}, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, "branch=") {
		t.Errorf("the runs of every branch should be read, then main's left out: %q", query)
	}
	if got := b.Jobs["build"]; got.Median != 22*time.Second || got.Samples != 3 {
		t.Errorf("build typical = %+v, want the median of the 3 runs on feat/a and feat/b", got)
	}
	if b.Previous == nil || b.Previous.HeadBranch == "main" {
		t.Errorf("the previous run should come from a branch, got %+v", b.Previous)
	}
}
