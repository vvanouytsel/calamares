package gh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRepoStatsKeepsCommitAuthorsAndOnlyThePullRequestsMergedSince(t *testing.T) {
	since := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"r0": map[string]any{"nameWithOwner": "acme/webshop",
				"defaultBranchRef": map[string]any{"target": map[string]any{
					"recent": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{
						map[string]any{"committedDate": "2026-11-02T10:00:00Z", "author": map[string]any{"name": "Sam", "user": map[string]any{"login": "sam"}}},
						map[string]any{"committedDate": "2026-11-01T10:00:00Z", "author": map[string]any{"name": "Robin", "user": nil}},
					}},
					"all": map[string]any{"totalCount": 812},
				}},
				"pullRequests": map[string]any{"totalCount": 140, "nodes": []any{
					map[string]any{"mergedAt": "2026-11-02T09:00:00Z", "author": map[string]any{"login": "noor"}},
					map[string]any{"mergedAt": "2026-09-01T09:00:00Z", "author": map[string]any{"login": "kim"}},
				}},
			},
		}})
	})
	got, err := c.RepoStats(context.Background(), []string{"acme/webshop"}, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d repos, want 1", len(got))
	}
	a := got[0]
	if a.AllCommits != 812 || a.AllMerged != 140 {
		t.Errorf("all commits %d, all merged %d; want 812 and 140", a.AllCommits, a.AllMerged)
	}
	if len(a.Commits) != 2 || a.Commits[0].Who() != "sam" || a.Commits[1].Who() != "Robin" {
		t.Errorf("commits = %+v, want sam and then Robin by name", a.Commits)
	}
	if len(a.Merged) != 1 || a.Merged[0].Login != "noor" {
		t.Errorf("merged = %+v, want only noor's", a.Merged)
	}
}

func TestRepoStatsReadsMorePagesOfABusyRepo(t *testing.T) {
	commit := map[string]any{"committedDate": "2026-11-02T10:00:00Z", "author": map[string]any{"name": "Sam", "user": map[string]any{"login": "sam"}}}
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "CommitsAfter") {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"defaultBranchRef": map[string]any{"target": map[string]any{
				"history": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{commit, commit}}}}}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"r0": map[string]any{"nameWithOwner": "acme/webshop", "defaultBranchRef": map[string]any{"target": map[string]any{
				"recent": map[string]any{"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "c1"}, "nodes": []any{commit}},
				"all":    map[string]any{"totalCount": 3}}}},
		}})
	})
	got, err := c.RepoStats(context.Background(), []string{"acme/webshop"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Commits) != 3 || got[0].CommitsMore {
		t.Errorf("read %d commits (more: %v), want all 3", len(got[0].Commits), got[0].CommitsMore)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2", hits.Load())
	}
}

func TestContributorsIsNotReadyWhileGitHubWorksThemOut(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{}`))
	})
	got, ready, err := c.Contributors(context.Background(), "acme/webshop")
	if err != nil || ready || got != nil {
		t.Errorf("got %v, ready %v, err %v; want not ready", got, ready, err)
	}
}

func TestContributorsCountsCommitsPerLogin(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"total":12,"author":{"login":"sam"},"weeks":[]},{"total":3,"author":null},{"total":40,"author":{"login":"noor"}}]`))
	})
	got, ready, err := c.Contributors(context.Background(), "acme/webshop")
	if err != nil || !ready {
		t.Fatalf("ready %v, err %v", ready, err)
	}
	if len(got) != 2 || got[0] != (Contributor{"sam", 12}) || got[1] != (Contributor{"noor", 40}) {
		t.Errorf("got %+v", got)
	}
}

func TestRunCountAsksForAllRunsAndTheFailedOnes(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		total := 120
		if r.URL.Query().Get("status") == "failure" {
			total = 9
		}
		if r.URL.Query().Get("created") != ">=2026-10-04" {
			t.Errorf("created = %q", r.URL.Query().Get("created"))
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": total, "workflow_runs": []any{}})
	})
	runs, failed, err := c.RunCount(context.Background(), "acme/webshop", time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC))
	if err != nil || runs != 120 || failed != 9 {
		t.Errorf("runs %d, failed %d, err %v; want 120 and 9", runs, failed, err)
	}
}

func TestContributionsReadsEveryYearOldestDayFirst(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "contributionYears") {
			w.Write([]byte(`{"data":{"viewer":{"contributionsCollection":{"contributionYears":[2026,2025]}}}}`))
			return
		}
		w.Write([]byte(`{"data":{"viewer":{
			"y2026":{"contributionCalendar":{"weeks":[{"contributionDays":[{"date":"2026-01-01","contributionCount":4}]}]}},
			"y2025":{"contributionCalendar":{"weeks":[{"contributionDays":[{"date":"2025-12-31","contributionCount":2}]}]}}}}}`))
	})
	got, err := c.Contributions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (ContributionDay{"2025-12-31", 2}) || got[1] != (ContributionDay{"2026-01-01", 4}) {
		t.Errorf("got %+v", got)
	}
}
