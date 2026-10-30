package gh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewWithToken("github.com", "x", "test")
	c.apiBase = srv.URL
	return c, &hits
}

func etagged(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("If-None-Match") == `"v1"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", `"v1"`)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func TestConcurrentGetsOfOneURLShareARequest(t *testing.T) {
	release := make(chan struct{})
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		etagged(w, r)
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Get(context.Background(), "x", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := hits.Load(); n != 1 {
		t.Errorf("GitHub got %d requests, want 1", n)
	}
}

func TestRecentAnswerIsReusedThenRevalidated(t *testing.T) {
	c, hits := testClient(t, etagged)
	ctx := context.Background()
	_ = c.Get(ctx, "x", nil)
	_ = c.Get(ctx, "x", nil)
	if n := hits.Load(); n != 1 {
		t.Fatalf("a repeat within the freshness window sent %d requests, want 1", n)
	}
	c.fresh = 0
	_ = c.Get(ctx, "x", nil)
	if s := c.Stats(); s.Requests != 2 || s.NotModified != 1 {
		t.Errorf("stats = %+v, want 2 requests of which 1 not modified", s)
	}
}

func TestRetryAfterPausesReadsButNotActions(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx := context.Background()
	if err := c.Get(ctx, "x", nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if err := c.Get(ctx, "y", nil); !errors.Is(err, ErrRateLimited) || hits.Load() != 1 {
		t.Fatalf("a read while paused should fail without a request: err %v, %d requests", err, hits.Load())
	}
	if until := c.Rate().PausedUntil; time.Until(until) < 25*time.Second {
		t.Errorf("paused until %s, want ~30s from now", until)
	}
	if err := c.Do(ctx, http.MethodPost, "z", nil, nil); err != nil {
		t.Errorf("actions should still go through: %v", err)
	}
}

func TestSecondaryRateLimitPausesForAMinute(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
	})
	_ = c.Get(context.Background(), "x", nil)
	if left := time.Until(c.Rate().PausedUntil); left < 55*time.Second || left > time.Minute {
		t.Errorf("paused for %s, want a minute", left)
	}
	if c.Stats().Limited != 1 {
		t.Errorf("Limited = %d, want 1", c.Stats().Limited)
	}
}

func TestForbiddenWithoutRateLimitDoesNotPause(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	})
	_ = c.Get(context.Background(), "x", nil)
	if !c.Rate().PausedUntil.IsZero() {
		t.Errorf("a permission error should not pause reads")
	}
}

func TestBackgroundRequestsAreSpacedOut(t *testing.T) {
	c, _ := testClient(t, etagged)
	c.bgGap = 40 * time.Millisecond
	ctx := Background(context.Background())
	start := time.Now()
	for _, p := range []string{"a", "b", "c"} {
		_ = c.Get(ctx, p, nil)
	}
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Errorf("3 background requests took %s, want at least 2 gaps", d)
	}
}

func TestForegroundRequestDoesNotQueueBehindBackgroundOnes(t *testing.T) {
	c, _ := testClient(t, etagged)
	c.bgGap = time.Second
	bg := Background(t.Context())
	for _, p := range []string{"a", "b", "c"} {
		go func() { _ = c.Get(bg, p, nil) }()
	}
	short, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	go func() { _ = c.Get(short, "x", nil) }()
	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	if err := c.Get(context.Background(), "x", nil); err != nil {
		t.Fatalf("foreground request failed: %v", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("foreground request took %s behind background ones", d)
	}
}

func TestCallerGivingUpDoesNotFailOthersWaiting(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		etagged(w, r)
	})
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	go func() { _ = c.Get(short, "x", nil) }()
	time.Sleep(5 * time.Millisecond)
	if err := c.Get(context.Background(), "x", nil); err != nil {
		t.Errorf("second caller got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("GitHub got %d requests, want 1", n)
	}
}

func TestSearchLimitDoesNotPauseOtherReads(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Resource", "search")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	_ = c.Get(context.Background(), "search/repositories", nil)
	if !c.Rate().PausedUntil.IsZero() {
		t.Errorf("a search limit should not pause the REST reads every screen needs")
	}
}

func TestAnActionMakesRecentAnswersStale(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		etagged(w, r)
	})
	ctx := context.Background()
	_ = c.Get(ctx, "x", nil)
	_ = c.Do(ctx, http.MethodPost, "rerun", nil, nil)
	_ = c.Get(ctx, "x", nil)
	if n := hits.Load(); n != 3 {
		t.Errorf("GitHub got %d requests, want the read after the action to go out", n)
	}
}

func TestGraphQLUsesThePartOfAnAnswerThatCame(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"repositories":{"pageInfo":{"hasNextPage":false},
			"nodes":[{"nameWithOwner":"o/ok","name":"ok","owner":{"login":"o"}},null]}}},
			"errors":[{"message":"Resource protected by organization SAML enforcement."}]}`))
	})
	repos, _, err := c.SweepPage(context.Background(), "")
	if err != nil || len(repos) != 1 || repos[0].FullName != "o/ok" {
		t.Errorf("got %+v, %v; want the one repo GitHub sent", repos, err)
	}
}

func TestSweepReposReadsOnlyTheNamedReposInOneRequest(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"r0":{"nameWithOwner":"o/a","name":"a","owner":{"login":"o"},"defaultBranchRef":{"name":"main","target":{"history":{"nodes":[{"statusCheckRollup":{"state":"FAILURE"}}]}}}},"r1":null}}`))
	})
	repos, err := c.SweepRepos(context.Background(), []string{"o/a", "o/gone"})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || len(repos) != 1 || repos[0].FullName != "o/a" || repos[0].Health[0] != "FAILURE" {
		t.Errorf("got %d requests and repos %+v, want 1 request and o/a with its health", hits.Load(), repos)
	}
}

func TestTheSweepSaysWhichReposHaveIssuesOff(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"r0":{"nameWithOwner":"acme/webshop","hasIssuesEnabled":true},"r1":{"nameWithOwner":"acme/infra","hasIssuesEnabled":false}}}`))
	})
	repos, err := c.SweepRepos(context.Background(), []string{"acme/webshop", "acme/infra"})
	if err != nil || len(repos) != 2 {
		t.Fatalf("got %+v, %v", repos, err)
	}
	if repos[0].IssuesOff || !repos[1].IssuesOff {
		t.Errorf("only acme/infra has issues off: %+v", repos)
	}
}

func TestTheBudgetOnlyGoesDownWithinAWindow(t *testing.T) {
	reset := time.Unix(1_800_000_000, 0)
	r := laterRate(Rate{}, Rate{Limit: 5000, Remaining: 4900, Reset: reset})
	// A response sent earlier arrives late with a higher count: ignored.
	if r = laterRate(r, Rate{Limit: 5000, Remaining: 4950, Reset: reset}); r.Remaining != 4900 {
		t.Fatalf("a late response raised the budget to %d", r.Remaining)
	}
	if r = laterRate(r, Rate{Limit: 5000, Remaining: 4890, Reset: reset.Add(time.Second)}); r.Remaining != 4890 {
		t.Fatalf("a lower count should be kept, got %d", r.Remaining)
	}
	// A new window starts: the budget is back up.
	if r = laterRate(r, Rate{Limit: 5000, Remaining: 4999, Reset: reset.Add(time.Hour)}); r.Remaining != 4999 {
		t.Fatalf("a new window should raise the budget, got %d", r.Remaining)
	}
	// A straggler from the old window changes nothing.
	if r = laterRate(r, Rate{Limit: 5000, Remaining: 4100, Reset: reset}); r.Remaining != 4999 {
		t.Fatalf("a response from the old window lowered the budget to %d", r.Remaining)
	}
}
