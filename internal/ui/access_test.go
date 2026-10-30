package ui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// refusingGitHub answers every request with status and headers, and counts them.
type refusingGitHub struct {
	mu      sync.Mutex
	status  int
	headers map[string]string
	body    string
	n       int
}

func (f *refusingGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	h := http.Header{}
	for k, v := range f.headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: f.status, Header: h, Body: io.NopCloser(strings.NewReader(f.body)), Request: r}, nil
}

func (f *refusingGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func useTransport(t *testing.T, rt http.RoundTripper) {
	orig := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// accessApp is an app whose repo o/r gives you role, signed in with token.
func accessApp(t *testing.T, role, token string) *App {
	t.Setenv("CALAMARES_CONFIG", t.TempDir()+"/config.yaml")
	a := New(gh.NewWithToken("github.com", token, "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	a.repos = []gh.Repo{{FullName: "o/r", HasActions: true, Permission: role, PushedAt: time.Now()}}
	a.st.loaded = true
	a.st.reposState.at = time.Now()
	return a
}

func finishedRun() gh.Run {
	return gh.Run{ID: 7, Name: "CI", Status: "completed", Conclusion: "failure", HTMLURL: "https://github.com/o/r/actions/runs/7"}
}

func barOf(a *App, bs []Binding) string {
	var ks []string
	for _, h := range a.hints(bs) {
		ks = append(ks, h.Key)
	}
	return " " + strings.Join(ks, " ") + " "
}

func TestReadAccessLocksRerunWithoutARequest(t *testing.T) {
	fake := &refusingGitHub{status: 200, body: "{}"}
	useTransport(t, fake)
	a := accessApp(t, "READ", "gho_x")
	bs := a.runKeys("o/r", finishedRun())
	if got := barOf(a, bs); strings.Contains(got, " R ") || !strings.Contains(got, " o ") {
		t.Errorf("rerun should be left out of the key bar for read access, got %q", got)
	}
	cmd, _ := a.press(bs, "R")
	msg := cmd()
	f, ok := msg.(flashMsg)
	if !ok || f.kind != nHint || !strings.Contains(f.text, "needs write access to o/r (you have read)") {
		t.Errorf("pressing R gave %+v", msg)
	}
	if fake.count() != 0 {
		t.Errorf("a locked key made %d requests", fake.count())
	}
	if h := lockedFor(a, bs[3]); !strings.Contains(h, "write access") {
		t.Errorf("help should say why rerun is locked, got %q", h)
	}
}

func TestWriteAccessKeepsRerun(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	bs := a.runKeys("o/r", finishedRun())
	if got := barOf(a, bs); !strings.Contains(got, " R ") {
		t.Errorf("rerun should be offered with write access, got %q", got)
	}
}

func TestRefusalOnTryingIsAHintAndLocksTheKey(t *testing.T) {
	fake := &refusingGitHub{status: 403, headers: map[string]string{"X-Accepted-GitHub-Permissions": "actions=write"},
		body: `{"message":"Resource not accessible by personal access token"}`}
	useTransport(t, fake)
	a := accessApp(t, "", "github_pat_x")
	bs := a.runKeys("o/r", finishedRun())
	// What R does after its confirmation: an action filed under CapWrite.
	a.actNeed = gh.CapWrite
	cmd := a.act("o/r", "Rerun", func(ctx context.Context) error {
		return a.client.Do(ctx, "POST", "repos/o/r/actions/runs/7/rerun", nil, nil)
	})
	a.actNeed = gh.CapRead
	_, _ = a.Update(cmd())
	if fake.count() != 1 {
		t.Fatalf("%d requests", fake.count())
	}
	if a.unreadErrors != 0 || a.flashKind != nHint || !strings.Contains(a.flash, "actions=write") {
		t.Errorf("a refusal should be an amber hint, got kind %v %q (%d errors)", a.flashKind, a.flash, a.unreadErrors)
	}
	if got := barOf(a, bs); strings.Contains(got, " R ") {
		t.Errorf("rerun should be locked after the refusal, got %q", got)
	}
	cmd, _ = a.press(bs, "R")
	if f, ok := cmd().(flashMsg); !ok || f.kind != nHint || fake.count() != 1 {
		t.Errorf("a learned lock should answer without a request")
	}
}

func TestPlacesAreReachedWithAngleBrackets(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	a.tab, a.stack = tabRepos, []View{a.roots[tabRepos]}
	press(a, ">")
	if a.tab != tabActions {
		t.Errorf("> went to place %d", a.tab)
	}
	press(a, "<", "<")
	if a.tab != tabAchievements {
		t.Errorf("< should wrap around to the last place, got %d", a.tab)
	}
}

func TestHeaderShowsTheDigitOfEveryPlace(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	head := ansi.Strip(a.renderTopBar())
	for _, want := range []string{"1", "Repos", "2", "Pull requests", "6", "Issues", "7", "Achievements"} {
		if !strings.Contains(head, want) {
			t.Errorf("header lacks %q:\n%s", want, head)
		}
	}
}

func TestAccessScreenListsRolesAndRefusals(t *testing.T) {
	a := accessApp(t, "READ", "gho_x")
	press(a, ":")
	a.input.SetValue(":access")
	press(a, "enter")
	out := ansi.Strip(a.View())
	for _, want := range []string{"Access", "OAuth token", "read", "merge, rerun"} {
		if !strings.Contains(out, want) {
			t.Errorf("access screen lacks %q:\n%s", want, out)
		}
	}
}
