package gh

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrServerTrouble is what an answer of HTTP 500 or more is: GitHub failed
// on its side, and a moment later the same request usually works.
var ErrServerTrouble = errors.New("GitHub had trouble answering")

// retryDelays are the waits before the second and third try of a request
// GitHub answered with 502, 503 or 504, or whose connection broke on the
// way. Each is spread by up to a quarter either way, so requests that failed
// together don't all come back at the same moment.
var retryDelays = []time.Duration{time.Second, 3 * time.Second}

// maxRetryAfter is the longest Retry-After on a 503 that is waited out
// before trying again; GitHub asking for longer is reported instead.
const maxRetryAfter = 10 * time.Second

// transientStatus reports whether an answer with code is worth asking again:
// a proxy that got no answer in time (502, 504) or a service that is busy (503).
func transientStatus(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// brokenConnection reports whether err is a connection that broke before
// GitHub answered: reset, closed, or found dead by a ping. A request that
// simply took too long is not one; it would only take too long again.
func brokenConnection(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, sign := range []string{"connection reset", "forcibly closed", "broken pipe", "client connection lost", "goaway", "connection was aborted"} {
		if strings.Contains(s, sign) {
			return true
		}
	}
	return false
}

type noRetryKey struct{}

// withoutRetry has requests made with ctx tried once: a query that fails
// because it asks too much is better split than asked again.
func withoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRetryKey{}, true)
}

func retryAllowed(ctx context.Context) bool {
	no, _ := ctx.Value(noRetryKey{}).(bool)
	return !no
}

// sendRetrying sends req. When retry holds (the request changes nothing,
// so sending it twice is harmless), a 502, 503 or 504 or a broken
// connection is tried again after a pause, twice at most. It returns the
// last response or error and how many tries it took.
func (c *Client) sendRetrying(req *http.Request, retry bool) (*http.Response, int, error) {
	ctx := req.Context()
	retry = retry && retryAllowed(ctx)
	for try := 1; ; try++ {
		r := req
		if try > 1 {
			r = req.Clone(ctx)
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, try, err
				}
				r.Body = body
			}
		}
		resp, err := c.send(r)
		if !retry || try > len(retryDelays) {
			return resp, try, c.noAnswerError(ctx, err)
		}
		wait := retryDelays[try-1]
		switch {
		case err != nil:
			if !brokenConnection(err) {
				return resp, try, c.noAnswerError(ctx, err)
			}
		case !transientStatus(resp.StatusCode):
			return resp, try, nil
		default:
			if after := retryAfter(resp); after > maxRetryAfter {
				return resp, try, nil
			} else if after > 0 {
				wait = after
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		}
		c.mu.Lock()
		c.stats.Retried++
		c.mu.Unlock()
		if err := sleep(ctx, spread(wait)); err != nil {
			return nil, try, err
		}
	}
}

// noAnswerError says plainly that a request got no answer.
func (c *Client) noAnswerError(ctx context.Context, err error) error {
	if err != nil && noAnswer(err) && ctx.Err() == nil {
		return ErrNoAnswer
	}
	return err
}

func retryAfter(resp *http.Response) time.Duration {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// spread moves d by up to a quarter either way.
func spread(d time.Duration) time.Duration {
	q := int64(d / 4)
	if q <= 0 {
		return d
	}
	return d - time.Duration(q) + time.Duration(rand.Int64N(2*q+1))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsServerError reports whether err is GitHub failing on its side (HTTP 500 or more).
func IsServerError(err error) bool { return errors.Is(err, ErrServerTrouble) }
