package gh

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// stalled is a transport whose requests time out before any answer.
type stalled struct{}

func (stalled) RoundTrip(*http.Request) (*http.Response, error) { return nil, timeoutError{} }

type timeoutError struct{}

func (timeoutError) Error() string   { return "Client.Timeout exceeded while awaiting headers" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestARequestGitHubDoesNotAnswerSaysSoPlainly(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = stalled{}
	t.Cleanup(func() { http.DefaultTransport = orig })
	c := NewWithToken("github.com", "x", "test")
	err := c.Get(context.Background(), "repos/acme/webshop/actions/runs", &struct{}{})
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("err = %v, want %v", err, ErrNoAnswer)
	}
}

func TestAQuietConnectionIsPingedAndDroppedWhenItDoesNotAnswer(t *testing.T) {
	tr := &http.Transport{}
	pingWhenQuiet(tr)
	if tr.HTTP2 == nil || tr.HTTP2.SendPingTimeout <= 0 || tr.HTTP2.PingTimeout <= 0 {
		t.Fatalf("the transport should ping quiet connections: %+v", tr.HTTP2)
	}
}
