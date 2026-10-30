package gh

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quickRetries(t *testing.T) {
	orig := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = orig })
}

// failingFirst answers code to the first n requests and then {"ok":true}.
func failingFirst(n int32, code int) http.HandlerFunc {
	var seen atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1) <= n {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("<html>Bad gateway</html>"))
			return
		}
		etagged(w, r)
	}
}

func TestABadGatewayIsTriedAgain(t *testing.T) {
	quickRetries(t)
	for _, code := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		c, hits := testClient(t, failingFirst(2, code))
		var out struct{ OK bool }
		if err := c.Get(context.Background(), "repos/acme/webshop", &out); err != nil || !out.OK {
			t.Fatalf("HTTP %d twice, then an answer: err = %v, out = %+v", code, err, out)
		}
		if n := hits.Load(); n != 3 {
			t.Errorf("HTTP %d: GitHub got %d requests, want 3", code, n)
		}
		if s := c.Stats(); s.Retried != 2 {
			t.Errorf("HTTP %d: retried = %d, want 2", code, s.Retried)
		}
	}
}

func TestAServerErrorThatLastsSaysSoPlainly(t *testing.T) {
	quickRetries(t)
	c, hits := testClient(t, failingFirst(100, http.StatusBadGateway))
	err := c.Get(context.Background(), "repos/acme/webshop", &struct{}{})
	if !IsServerError(err) {
		t.Fatalf("err = %v, want a server error", err)
	}
	if n := hits.Load(); n != 3 {
		t.Errorf("GitHub got %d requests, want 3", n)
	}
	if got := err.Error(); !strings.Contains(got, "HTTP 502 Bad Gateway") || !strings.Contains(got, "tried 3 times") {
		t.Errorf("message = %q, want the status in words and the tries", got)
	}
}

func TestOtherErrorsAreNotTriedAgain(t *testing.T) {
	quickRetries(t)
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusUnprocessableEntity} {
		c, hits := testClient(t, failingFirst(100, code))
		_ = c.Get(context.Background(), "x", nil)
		if n := hits.Load(); n != 1 {
			t.Errorf("HTTP %d: GitHub got %d requests, want 1", code, n)
		}
	}
}

func TestAnActionIsSentOnceEvenWhenGitHubFails(t *testing.T) {
	quickRetries(t)
	c, hits := testClient(t, failingFirst(100, http.StatusBadGateway))
	if err := c.Do(context.Background(), http.MethodPost, "repos/acme/webshop/actions/runs/1/rerun", nil, nil); err == nil {
		t.Fatal("want an error")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("a rerun was sent %d times, want once", n)
	}
	mut := 0
	c, hits = testClient(t, failingFirst(100, http.StatusBadGateway))
	_ = c.GraphQL(context.Background(), `mutation { addStar(input: {starrableId: "x"}) { clientMutationId } }`, nil, &mut)
	if n := hits.Load(); n != 1 {
		t.Errorf("a GraphQL mutation was sent %d times, want once", n)
	}
}

func TestAGraphQLQueryIsTriedAgain(t *testing.T) {
	quickRetries(t)
	var seen atomic.Int32
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if seen.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"sam"}}}`))
	})
	var out struct{ Viewer struct{ Login string } }
	if err := c.GraphQL(context.Background(), `query { viewer { login } }`, nil, &out); err != nil || out.Viewer.Login != "sam" {
		t.Fatalf("err = %v, out = %+v", err, out)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("GitHub got %d requests, want 2", n)
	}
}

func TestALongRetryAfterIsNotWaitedOut(t *testing.T) {
	quickRetries(t)
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	start := time.Now()
	_ = c.Get(context.Background(), "x", nil)
	if n := hits.Load(); n != 1 || time.Since(start) > 5*time.Second {
		t.Errorf("a 503 asking for two minutes: %d requests in %v, want 1 at once", n, time.Since(start))
	}
}

func TestARefreshGitHubFailsKeepsWhatWasReadBefore(t *testing.T) {
	quickRetries(t)
	var broken atomic.Bool
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		etagged(w, r)
	})
	c.fresh = 0
	var out struct{ OK bool }
	if err := c.Get(context.Background(), "x", &out); err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	out.OK = false
	if err := c.Get(Background(context.Background()), "x", &out); err != nil || !out.OK {
		t.Errorf("a background refresh GitHub failed: err = %v, out = %+v, want the earlier answer", err, out)
	}
	if s := c.Stats(); s.Stale != 1 {
		t.Errorf("stale = %d, want 1", s.Stale)
	}
	if err := c.Get(context.Background(), "x", &out); !IsServerError(err) {
		t.Errorf("a read you wait for says GitHub failed: err = %v", err)
	}
}

func TestABrokenConnectionIsTriedAgainButATimeoutIsNot(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{errors.New("read tcp: connection reset by peer"), true},
		{errors.New("wsarecv: An existing connection was forcibly closed by the remote host."), true},
		{errors.New("http2: client connection lost"), true},
		{timeoutError{}, false},
		{context.Canceled, false},
		{errors.New("x509: certificate signed by unknown authority"), false},
	} {
		if got := brokenConnection(tc.err); got != tc.want {
			t.Errorf("brokenConnection(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestSpreadStaysWithinAQuarter(t *testing.T) {
	for range 100 {
		if d := spread(4 * time.Second); d < 3*time.Second || d > 5*time.Second {
			t.Fatalf("spread(4s) = %v", d)
		}
	}
}
