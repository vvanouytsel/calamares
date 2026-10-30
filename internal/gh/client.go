// Package gh is a small GitHub REST client focused on the Actions API.
//
// It reuses the user's existing gh CLI login, caches GET responses with
// ETags (304 responses don't count against the rate limit) and tracks the
// remaining rate limit for display.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks to the GitHub REST API for a single host.
type Client struct {
	Host        string
	TokenSource string
	// StatusURL is GitHub's status page, "" for none: a GitHub Enterprise
	// Server has none, and a client made with NewWithToken leaves it out.
	StatusURL string

	apiBase string
	token   string
	http    *http.Client
	fg      chan struct{} // requests the user is waiting for
	bg      chan struct{} // refreshes and prefetches

	// started is the attempt of each run known to have started; see KeepStarted.
	started   map[int64]int
	startedMu sync.Mutex

	mu       sync.Mutex
	cache    map[string]cacheEntry
	inflight map[string]*flight
	rate     Rate
	gqlRate  Rate
	stats    Stats
	// pauseUntil is set when GitHub asks us to slow down (403/429).
	pauseUntil time.Time
	pauseFor   time.Duration
	// bgNext is the earliest time the next background request may start.
	bgNext time.Time
	bgGap  time.Duration
	fresh  time.Duration
	// mutatedAt is when an action last changed something; answers from
	// before it aren't reused as fresh.
	mutatedAt time.Time
	// scopes are the OAuth scopes the last response listed; denied holds what
	// GitHub refused this session; ssoURLs the SSO pages it pointed at.
	scopes      []string
	scopesKnown bool
	denied      map[string]map[Capability]Denial
	ssoURLs     []string
}

// flight is a GET in progress that later callers for the same URL wait on.
// It is cancelled once every caller waiting for it has given up.
type flight struct {
	done    chan struct{}
	body    []byte
	err     error
	waiters int
	cancel  context.CancelFunc
}

// Stats counts what the client did this session.
type Stats struct {
	Requests    int // sent to GitHub
	NotModified int // of those, answered 304: no rate limit cost
	Shared      int // answered from a request in flight or one just made
	Limited     int // answered 403/429 for exceeding a limit
	Retried     int // tried again after a 502, 503, 504 or a broken connection
	Stale       int // refreshes GitHub failed, answered from the cache instead
}

type cacheEntry struct {
	etag         string
	lastModified string // for endpoints that revalidate by date
	body         []byte
	at           time.Time
}

func (e cacheEntry) revalidatable() bool { return e.etag != "" || e.lastModified != "" }

// APIError is a non-2xx response from GitHub.
type APIError struct {
	Status  int
	Message string
	// Tries is how many times the request was sent before this answer.
	Tries int
}

func (e *APIError) Error() string {
	if e.Status >= 500 {
		// The body of these is often an HTML page or a bare "Server Error".
		s := fmt.Sprintf("%s (HTTP %d %s", ErrServerTrouble, e.Status, http.StatusText(e.Status))
		if e.Tries > 1 {
			s += fmt.Sprintf(", tried %d times", e.Tries)
		}
		return s + ")"
	}
	if e.Message == "" {
		return fmt.Sprintf("GitHub API: HTTP %d", e.Status)
	}
	return fmt.Sprintf("GitHub API: %s (HTTP %d)", e.Message, e.Status)
}

// Is makes an answer of HTTP 500 or more an ErrServerTrouble.
func (e *APIError) Is(target error) bool { return target == ErrServerTrouble && e.Status >= 500 }

// IsNotFound reports whether err is a 404 from GitHub.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// ErrSavingRateLimit is returned instead of reading from the REST API while
// Rate.Saving holds.
var ErrSavingRateLimit = errors.New("paused to stay under your GitHub rate limit")

// ErrRateLimited is returned while GitHub has asked us to slow down.
var ErrRateLimited = errors.New("GitHub asked Calamares to slow down; paused")

// ErrNoAnswer is returned when GitHub did not answer a request in time, as
// when the connection to it died while the laptop slept or the network
// changed.
var ErrNoAnswer = errors.New("GitHub did not answer in time")

// noAnswer says err is a request that got no answer: it timed out, or the
// connection it waited on was found dead.
func noAnswer(err error) bool {
	var ne net.Error
	return (errors.As(err, &ne) && ne.Timeout()) || strings.Contains(err.Error(), "client connection lost")
}

// pingIdleConnections has the default transport ping a connection to GitHub
// that has gone quiet, and drop it when no answer comes. Without it a dead
// connection holds every request sent on it until each one times out.
func pingIdleConnections() {
	pingOnce.Do(func() {
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			pingWhenQuiet(t)
		}
	})
}

// pingWhenQuiet sets t to ping a connection that has been quiet for 15
// seconds and to drop it when 10 more pass without an answer.
func pingWhenQuiet(t *http.Transport) {
	if t.HTTP2 == nil {
		t.HTTP2 = &http.HTTP2Config{SendPingTimeout: 15 * time.Second, PingTimeout: 10 * time.Second}
	}
}

var pingOnce sync.Once

// ErrNoToken is returned when no credentials could be found.
var ErrNoToken = errors.New("no GitHub token found: run `gh auth login`, or set GH_TOKEN")

// New creates a client for host (e.g. "github.com"), resolving a token from
// the environment or the gh CLI.
func New(host string) (*Client, error) {
	if host == "" {
		host = "github.com"
	}
	token, src := resolveToken(host)
	if token == "" {
		return nil, ErrNoToken
	}
	c := NewWithToken(host, token, src)
	if host == "github.com" {
		c.StatusURL = githubStatus
	}
	return c, nil
}

// NewWithToken creates a client with an explicit token.
func NewWithToken(host, token, source string) *Client {
	pingIdleConnections()
	base := "https://api.github.com"
	if host != "github.com" {
		base = "https://" + host + "/api/v3"
	}
	return &Client{
		Host:        host,
		TokenSource: source,
		apiBase:     base,
		token:       token,
		http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			// The token goes to GitHub only, never to where a download is redirected.
			if req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		}},
		fg:       make(chan struct{}, 8),
		bg:       make(chan struct{}, 4),
		cache:    map[string]cacheEntry{},
		inflight: map[string]*flight{},
		bgGap:    200 * time.Millisecond,
		fresh:    2 * time.Second,
	}
}

func resolveToken(host string) (token, source string) {
	vars := []string{"GH_TOKEN", "GITHUB_TOKEN"}
	if host != "github.com" {
		vars = []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	}
	for _, v := range vars {
		if t := strings.TrimSpace(os.Getenv(v)); t != "" {
			return t, v
		}
	}
	out, err := exec.Command("gh", "auth", "token", "--hostname", host).Output()
	if err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, "gh auth"
		}
	}
	return "", ""
}

// Rate returns the most recently observed rate limit.
func (c *Client) Rate() Rate {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.rate
	r.PausedUntil = c.pauseUntil
	return r
}

// Stats returns the request counts of this session.
func (c *Client) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// WebURL returns the browser URL for a path such as "owner/repo/actions".
func (c *Client) WebURL(path string) string {
	return "https://" + c.Host + "/" + strings.TrimPrefix(path, "/")
}

func (c *Client) url(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return c.apiBase + "/" + strings.TrimPrefix(path, "/")
}

// Get fetches path and decodes the JSON body into v, using the ETag cache.
// The cache keeps v re-encoded, so it only holds the fields v declares.
func (c *Client) Get(ctx context.Context, path string, v any) error {
	body, err := c.getRaw(ctx, path, "application/vnd.github+json", true)
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return err
	}
	c.compact(path, body, v)
	return nil
}

func (c *Client) compact(path string, body []byte, v any) {
	small, err := json.Marshal(v)
	if err != nil || len(small) >= len(body) {
		return
	}
	u := c.url(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.cache[u]; ok && bytes.Equal(e.body, body) {
		e.body = small
		c.cache[u] = e
	}
}

// cachedAt reports when the cached response for path was last confirmed by GitHub.
func (c *Client) cachedAt(path string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[c.url(path)]
	return e.at, ok
}

// Cached decodes the last response seen for path without touching the
// network. Views use it to show data instantly and refresh in the background.
func (c *Client) Cached(path string, v any) bool {
	c.mu.Lock()
	e, ok := c.cache[c.url(path)]
	c.mu.Unlock()
	return ok && json.Unmarshal(e.body, v) == nil
}

// getRaw GETs path; a request for a URL that is already in flight shares its
// answer. With useCache, the response is also cached by ETag, revalidated
// with If-None-Match, and reused as is when it was confirmed a moment ago.
func (c *Client) getRaw(ctx context.Context, path, accept string, useCache bool) ([]byte, error) {
	u := c.url(path)
	// Flights are per lane, so something you wait for never queues behind
	// a background refresh of the same URL.
	key := u
	if isBackground(ctx) {
		key = "bg " + u
	}
	c.mu.Lock()
	cached, hasCache := c.cache[u]
	hasCache = hasCache && useCache
	if hasCache && time.Since(cached.at) < c.fresh && cached.at.After(c.mutatedAt) {
		c.stats.Shared++
		c.mu.Unlock()
		return cached.body, nil
	}
	f, joined := c.inflight[key]
	if joined {
		c.stats.Shared++
	} else {
		f = &flight{done: make(chan struct{})}
		c.inflight[key] = f
	}
	f.waiters++
	var fctx context.Context
	if !joined {
		fctx, f.cancel = context.WithCancel(context.WithoutCancel(ctx))
	}
	c.mu.Unlock()

	if !joined {
		var entry *cacheEntry
		if useCache {
			if !hasCache {
				cached = cacheEntry{}
			}
			entry = &cached
		}
		cancel := f.cancel
		go func() {
			defer cancel()
			f.body, f.err = c.fetch(fctx, u, accept, entry)
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			close(f.done)
		}()
	}
	select {
	case <-f.done:
		return f.body, f.err
	case <-ctx.Done():
		c.mu.Lock()
		if f.waiters--; f.waiters == 0 {
			f.cancel()
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// fetch sends one GET. With a cache entry (possibly empty), it revalidates
// that entry and stores the response in the cache.
func (c *Client) fetch(ctx context.Context, u, accept string, cached *cacheEntry) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req, accept)
	if cached != nil && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	if cached != nil && cached.lastModified != "" {
		req.Header.Set("If-Modified-Since", cached.lastModified)
	}
	resp, tries, err := c.sendRetrying(req, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && cached != nil && cached.revalidatable() {
		c.mu.Lock()
		c.stats.NotModified++
		c.pauseFor = 0
		cached.at = time.Now()
		c.cache[u] = *cached
		c.mu.Unlock()
		return cached.body, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		err := c.fail(resp, body, tries)
		// A refresh GitHub failed keeps what was read before; the next one tries again.
		if IsServerError(err) && isBackground(ctx) && cached != nil && cached.body != nil {
			c.mu.Lock()
			c.stats.Stale++
			c.mu.Unlock()
			return cached.body, nil
		}
		return nil, err
	}
	c.mu.Lock()
	c.pauseFor = 0
	e := cacheEntry{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified"), body: body, at: time.Now()}
	if cached != nil && e.revalidatable() {
		c.cache[u] = e
	}
	c.mu.Unlock()
	return body, nil
}

// Do sends a request that changes something on GitHub, with an optional
// JSON body, and decodes the response into v when v is non-nil.
func (c *Client) Do(ctx context.Context, method, path string, in, v any) error {
	return c.sendJSON(ctx, method, path, in, v, true, false)
}

// sendJSON sends a request with a JSON body; mutation marks requests that
// change something, after which no earlier answer is reused as fresh, and
// retry those that change nothing, so they are tried again when GitHub
// fails on its side.
func (c *Client) sendJSON(ctx context.Context, method, path string, in, v any, mutation, retry bool) error {
	var rdr io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rdr)
	if err != nil {
		return err
	}
	c.decorate(req, "application/vnd.github+json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, tries, err := c.sendRetrying(req, retry)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return c.fail(resp, body, tries)
	}
	if mutation {
		c.mu.Lock()
		c.mutatedAt = time.Now()
		c.mu.Unlock()
	}
	if v != nil && len(body) > 0 {
		return json.Unmarshal(body, v)
	}
	return nil
}

func (c *Client) decorate(req *http.Request, accept string) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "calamares")
}

type bgKey struct{}

// Background marks requests made with ctx as low priority: they use their own
// connection slots so they never delay what the user is waiting for.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, bgKey{}, true)
}

func isBackground(ctx context.Context) bool {
	bg, _ := ctx.Value(bgKey{}).(bool)
	return bg
}

// send sends req once, in its lane and within the limits; sendRetrying is
// what the requests go through.
func (c *Client) send(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	sem := c.fg
	if isBackground(ctx) {
		sem = c.bg
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}
	}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-sem }()
	// Checked after waiting for a slot, so requests queued before a pause began honour it.
	if req.Method == http.MethodGet {
		if err := c.paused(time.Now()); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.stats.Requests++
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	c.trackRate(resp)
	c.trackScopes(resp)
	return resp, nil
}

// throttle spaces background requests out, so a burst of refreshes never
// trips GitHub's secondary rate limits. A request takes the next free slot
// when it is due; one that gives up holds no slot.
func (c *Client) throttle(ctx context.Context) error {
	for {
		c.mu.Lock()
		now := time.Now()
		wait := c.bgNext.Sub(now)
		if wait <= 0 {
			c.bgNext = now.Add(c.bgGap)
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

func (c *Client) paused(now time.Time) error {
	r := c.Rate()
	switch {
	case now.Before(r.PausedUntil):
		return fmt.Errorf("%w until %s", ErrRateLimited, r.PausedUntil.Local().Format("15:04:05"))
	case r.Saving(now):
		return fmt.Errorf("%w until %s", ErrSavingRateLimit, r.Reset.Local().Format("15:04"))
	}
	return nil
}

// fail turns an error response into an error, pausing reads when GitHub
// says a rate limit was hit: until Retry-After or the reset time if given,
// otherwise for a minute, doubling while it keeps happening.
func (c *Client) fail(resp *http.Response, body []byte, tries int) error {
	err := apiError(resp.StatusCode, body)
	err.Tries = tries
	if !rateLimited(resp, body) {
		if resp.StatusCode == http.StatusForbidden {
			return c.accessFromResponse(resp, err)
		}
		return err
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Limited++
	until := now.Add(max(time.Minute, min(2*c.pauseFor, 15*time.Minute)))
	if secs, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && secs > 0 {
		until = now.Add(time.Duration(secs) * time.Second)
	} else if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if reset, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			until = time.Unix(reset, 0)
		}
	}
	c.pauseFor = until.Sub(now)
	c.pauseUntil = until
	return fmt.Errorf("%w: %v", ErrRateLimited, err)
}

// rateLimited reports whether resp says the core REST limit (the one every
// screen reads from) was hit. Search and GraphQL have budgets of their own.
func rateLimited(resp *http.Response, body []byte) bool {
	if r := resp.Header.Get("X-RateLimit-Resource"); r != "" && r != "core" {
		return false
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.StatusCode != http.StatusForbidden {
		return false
	}
	return resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0" ||
		strings.Contains(strings.ToLower(string(body)), "rate limit")
}

// trackRate records the REST ("core") and GraphQL budgets; search has a
// small budget of its own that isn't shown.
func (c *Client) trackRate(resp *http.Response) {
	resource := resp.Header.Get("X-RateLimit-Resource")
	if resource != "" && resource != "core" && resource != "graphql" {
		return
	}
	lim, err1 := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit"))
	rem, err2 := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining"))
	if err1 != nil || err2 != nil {
		return
	}
	reset, _ := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	r := Rate{Limit: lim, Remaining: rem, Reset: time.Unix(reset, 0)}
	c.mu.Lock()
	if resource == "graphql" {
		c.gqlRate = laterRate(c.gqlRate, r)
	} else {
		c.rate = laterRate(c.rate, r)
	}
	c.mu.Unlock()
}

// laterRate is the budget to keep when a response reports r while old is
// known. Requests run side by side, so a response sent earlier can arrive
// later with a higher count: within one window the budget only goes down,
// and it goes up only when GitHub starts a new window (a later reset) or
// the limit itself changes.
func laterRate(old, r Rate) Rate {
	switch {
	case old.Limit == 0, r.Limit != old.Limit:
		return r
	case r.Reset.Sub(old.Reset) > 2*time.Second:
		return r // a new window
	case r.Reset.Before(old.Reset.Add(-2 * time.Second)):
		return old // a response from the window before
	case r.Remaining < old.Remaining:
		return r
	}
	return old
}

// GraphQLRate returns the most recently observed GraphQL budget.
func (c *Client) GraphQLRate() Rate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gqlRate
}

func apiError(status int, body []byte) *APIError {
	var payload struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload)
	return &APIError{Status: status, Message: payload.Message}
}

// SyncRate reads the REST and GraphQL budgets from GitHub, so they are known
// before the first real request and reflect what other tools on the same
// account have used. GitHub does not count this request against any limit.
func (c *Client) SyncRate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url("rate_limit"), nil)
	if err != nil {
		return err
	}
	c.decorate(req, "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, body)
	}
	var payload struct {
		Resources struct {
			Core    rateWindow `json:"core"`
			GraphQL rateWindow `json:"graphql"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if payload.Resources.Core.Limit > 0 {
		c.rate = laterRate(c.rate, payload.Resources.Core.rate())
	}
	if payload.Resources.GraphQL.Limit > 0 {
		c.gqlRate = laterRate(c.gqlRate, payload.Resources.GraphQL.rate())
	}
	return nil
}

type rateWindow struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

func (w rateWindow) rate() Rate {
	return Rate{Limit: w.Limit, Remaining: w.Remaining, Reset: time.Unix(w.Reset, 0)}
}
