package ui

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points the config at a scratch file, so no test can change the
// config of whoever runs the tests, and keeps the tests off the network.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "calamares-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("CALAMARES_CONFIG", filepath.Join(dir, "config.yaml"))
	os.Setenv("CALAMARES_ANIMATIONS", "off")
	if os.Getenv("CALAMARES_SNAPSHOT") == "" {
		http.DefaultTransport = offlineGitHub{}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// offlineGitHub answers every request the way GitHub answers the made-up
// tokens of the tests, without going out: a test that sets no transport of
// its own then waits on nothing and runs the same with or without a network.
// The snapshot tests, which render real screens, keep the real transport.
type offlineGitHub struct{}

func (offlineGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"message":"Bad credentials","status":"401"}`
	return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
