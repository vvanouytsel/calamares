package gh

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiskCacheRoundTrip(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	keep := c.url(workflowsPath("o/r"))
	skip := c.url("repos/o/r/actions/jobs/1/logs")
	c.cache[keep] = cacheEntry{etag: `"abc"`, body: []byte(`{"workflows":[{"id":1,"name":"CI"}]}`), at: time.Now()}
	c.cache[skip] = cacheEntry{etag: `"def"`, body: []byte("log"), at: time.Now()}

	p := filepath.Join(t.TempDir(), "sub", "cache")
	if err := c.SaveCache(p); err != nil {
		t.Fatal(err)
	}
	d := NewWithToken("github.com", "x", "test")
	if err := d.LoadCache(p); err != nil {
		t.Fatal(err)
	}
	wfs, ok := d.CachedWorkflows("o/r")
	if !ok || len(wfs) != 1 || wfs[0].Name != "CI" {
		t.Fatalf("workflows not restored: %v %v", ok, wfs)
	}
	if _, ok := d.cache[skip]; ok {
		t.Error("logs should not be persisted")
	}
	// A cache for another host must not leak in.
	e := NewWithToken("ghe.example.com", "x", "test")
	_ = e.LoadCache(p)
	if len(e.cache) != 0 {
		t.Error("entries from another host were loaded")
	}
}

func TestCacheKeepsOnlyDeclaredFieldsAndStillServes304(t *testing.T) {
	heavy := strings.Repeat("x", 10000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprintf(w, `{"total_count":1,"workflow_runs":[{"id":7,"name":"CI","repository":{"description":%q}}]}`, heavy)
	}))
	defer srv.Close()
	c := NewWithToken("github.com", "x", "test")
	c.apiBase = srv.URL

	for i := 0; i < 2; i++ {
		runs, total, err := c.ListRuns(context.Background(), "o/r", RunFilter{PerPage: 1})
		if err != nil || total != 1 || len(runs) != 1 || runs[0].Name != "CI" {
			t.Fatalf("request %d: runs=%+v total=%d err=%v", i+1, runs, total, err)
		}
	}
	if body := c.cache[c.url(runsPath("o/r", RunFilter{PerPage: 1}))].body; strings.Contains(string(body), heavy) {
		t.Errorf("cache kept fields Run does not declare (%d bytes)", len(body))
	}
	p := filepath.Join(t.TempDir(), "cache")
	if err := c.SaveCache(p); err != nil {
		t.Fatal(err)
	}
	d := NewWithToken("github.com", "x", "test")
	d.apiBase = srv.URL
	if err := d.LoadCache(p); err != nil {
		t.Fatal(err)
	}
	if runs, _, ok := d.CachedRuns("o/r", RunFilter{PerPage: 1}); !ok || len(runs) != 1 || runs[0].ID != 7 {
		t.Fatalf("cached runs not restored: %v %+v", ok, runs)
	}
	if at, ok := d.CachedRunsAt("o/r", RunFilter{PerPage: 1}); !ok || time.Since(at) > time.Minute {
		t.Errorf("CachedRunsAt = %v %v, want the time of the last response", at, ok)
	}
}

type graphQLReply string

func (g graphQLReply) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(g))), Request: r}, nil
}

func TestMyPullRequestsListsReviewRequestsThenYoursThenTheRestOnce(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = graphQLReply(`{"data":{
		"review":{"nodes":[{"number":2,"url":"u2","repository":{"nameWithOwner":"o/b"}}]},
		"authored":{"nodes":[{"number":1,"url":"u1","repository":{"nameWithOwner":"o/a"}}]},
		"involved":{"nodes":[{"number":1,"url":"u1","repository":{"nameWithOwner":"o/a"}},{"number":3,"url":"u3","repository":{"nameWithOwner":"o/c"}}]}}}`)
	t.Cleanup(func() { http.DefaultTransport = orig })

	prs, more, err := NewWithToken("github.com", "x", "test").MyPullRequests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if more {
		t.Errorf("searches without a next page found no more")
	}
	var got []string
	for _, pr := range prs {
		got = append(got, fmt.Sprintf("#%d %s", pr.Number, pr.Why))
	}
	want := "#2 review requested, #1 yours, #3 involved"
	if strings.Join(got, ", ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, ", "), want)
	}
}

func TestReadsPauseWhenTheRateLimitRunsLow(t *testing.T) {
	remaining := "1001"
	var reads, writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reads++
		} else {
			writes++
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", remaining)
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		fmt.Fprint(w, `{"login":"me"}`)
	}))
	defer srv.Close()
	c := NewWithToken("github.com", "x", "test")
	c.apiBase = srv.URL

	if _, err := c.CurrentUser(context.Background()); err != nil {
		t.Fatalf("read with a fifth of the limit left: %v", err)
	}
	remaining = "999"
	_, _ = c.CurrentUser(context.Background())
	if _, err := c.CurrentUser(context.Background()); !errors.Is(err, ErrSavingRateLimit) {
		t.Fatalf("read below a fifth of the limit: err = %v, want ErrSavingRateLimit", err)
	}
	if err := c.Cancel(context.Background(), "o/r", 1); err != nil {
		t.Fatalf("actions must still work while reads are paused: %v", err)
	}
	if reads != 2 || writes != 1 {
		t.Errorf("GitHub saw %d reads and %d writes, want 2 and 1", reads, writes)
	}
}

func TestRememberedResultsSurviveARestart(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	c.Remember("repos", []Repo{{FullName: "o/r", HasActions: true, Health: []string{"FAILURE"}}})
	path := filepath.Join(t.TempDir(), "cache")
	if err := c.SaveCache(path); err != nil {
		t.Fatal(err)
	}
	next := NewWithToken("github.com", "x", "test")
	if err := next.LoadCache(path); err != nil {
		t.Fatal(err)
	}
	var repos []Repo
	if _, ok := next.Recall("repos", &repos); !ok || len(repos) != 1 || !repos[0].HasActions || repos[0].Health[0] != "FAILURE" {
		t.Errorf("recalled %+v, ok=%v", repos, ok)
	}
}

func TestDiskCacheFromAnOlderFormatIsDropped(t *testing.T) {
	c := NewWithToken("github.com", "x", "test")
	u := c.url(runsPath("o/r", RunFilter{PerPage: 10}))
	c.cache[u] = cacheEntry{etag: `"abc"`, body: []byte(`{"workflow_runs":[{"id":1}]}`), at: time.Now()}
	p := filepath.Join(t.TempDir(), "cache")
	if err := c.SaveCache(p); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	old := []diskEntry{{URL: u, ETag: `"abc"`, Body: []byte(`{"workflow_runs":[{"id":1}]}`), At: time.Now(), Format: cacheFormat - 1}}
	if err := gob.NewEncoder(zw).Encode(old); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()

	d := NewWithToken("github.com", "x", "test")
	if err := d.LoadCache(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.cache[u]; ok {
		t.Error("an entry saved before the cached types gained fields should be read again, not reused")
	}
}
