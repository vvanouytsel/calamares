package ui

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestCronLinesComeDueAtTheirMinute(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		cron string
		when string
		want bool
	}{
		{"0 2 * * *", "2026-10-28 02:00", true},
		{"0 2 * * *", "2026-10-28 02:01", false},
		{"*/15 * * * *", "2026-10-28 13:45", true},
		{"*/15 * * * *", "2026-10-28 13:46", false},
		{"30 4 * * 1-5", "2026-10-30 04:30", true},  // a Friday
		{"30 4 * * 1-5", "2026-10-31 04:30", false}, // a Saturday
		{"0 0 * * SUN", "2026-11-01 00:00", true},
		{"0 0 * * 7", "2026-11-01 00:00", true},
		{"0 6 1 * MON", "2026-11-01 06:00", true}, // the 1st, a Sunday: either day will do
		{"0 6 1 * MON", "2026-11-02 06:00", true}, // a Monday
		{"0 6 1 * MON", "2026-11-03 06:00", false},
		{"0 12 * JAN,JUL *", "2026-07-04 12:00", true},
		{"0 12 * JAN,JUL *", "2026-08-04 12:00", false},
		{"0-30/10 8 * * *", "2026-10-28 08:20", true},
		{"0-30/10 8 * * *", "2026-10-28 08:40", false},
	}
	for _, c := range cases {
		spec, ok := parseCron(c.cron)
		if !ok {
			t.Errorf("%q should parse", c.cron)
			continue
		}
		if got := spec.matches(at(c.when)); got != c.want {
			t.Errorf("%q at %s: matches = %v, want %v", c.cron, c.when, got, c.want)
		}
	}
	for _, bad := range []string{"", "0 2 * *", "61 * * * *", "* * * * MOO", "*/0 * * * *", "5-1 * * * *"} {
		if _, ok := parseCron(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestAScheduleThatCameDueWakesItsRepo(t *testing.T) {
	now := time.Date(2026, 10, 28, 2, 10, 0, 0, time.UTC)
	if at, ok := cronLastDue("0 2 * * *", now, scheduleWake); !ok || !at.Equal(now.Add(-10*time.Minute)) {
		t.Errorf("the 02:00 schedule seen at 02:10: %v %v, want 02:00", at, ok)
	}
	if _, ok := cronLastDue("0 2 * * *", now.Add(time.Hour), scheduleWake); ok {
		t.Errorf("an hour after 02:00 the schedule should no longer wake the repo")
	}

	a := apiApp(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	a.repos = []gh.Repo{{FullName: "acme/nightly", HasActions: true, PushedAt: old}}
	a.st.schedules = []gh.Schedule{{Repo: "acme/nightly", File: ".github/workflows/nightly.yml", Crons: []string{"* * * * *"}}}
	if info := a.readInfoOf("acme/nightly", time.Now()); info.group != readLately || !strings.Contains(info.why, "schedule") {
		t.Errorf("a repo whose schedule just came due = %+v, want worked on lately for its schedule", info)
	}
}

func TestReposAreGroupedByHowMuchGoesOnInThem(t *testing.T) {
	a := apiApp(t)
	now := time.Now()
	a.me = "sam"
	a.cfg.Favourites = []string{"acme/pinned"}
	a.repos = []gh.Repo{
		{FullName: "acme/webshop", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)},
		{FullName: "acme/pinned", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)},
		{FullName: "acme/billing", HasActions: true, PushedAt: now.Add(-3 * time.Hour)},
		{FullName: "acme/docs", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)},
		{FullName: "acme/mine", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)},
	}
	a.st.status["acme/webshop"] = []gh.Run{{ID: 1, Status: "in_progress", CreatedAt: now}}
	a.st.status["acme/mine"] = []gh.Run{{ID: 2, Status: "completed", Conclusion: "success", CreatedAt: now.Add(-48 * time.Hour), Actor: gh.User{Login: "sam"}}}
	for repo, want := range map[string]readGroup{
		"acme/webshop": readBusy,
		"acme/pinned":  readLately,
		"acme/billing": readLately,
		"acme/mine":    readLately,
		"acme/docs":    readUntouched,
	} {
		if got := a.readInfoOf(repo, now).group; got != want {
			t.Errorf("%s is %s, want %s", repo, readGroupWords[got], readGroupWords[want])
		}
	}
	all := []string{"acme/webshop", "acme/pinned", "acme/billing", "acme/docs", "acme/mine"}
	if got := ansi.Strip(a.coverageWords(all, now)); got != "● 1 busy · 3 worked on lately · 1 untouched for a day or more" {
		t.Errorf("coverage line = %q", got)
	}
}

func TestEachRepoIsReadAtThePaceOfItsGroup(t *testing.T) {
	a := apiApp(t)
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "acme/docs", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)}}
	l := &loadState{tried: now.Add(-10 * time.Minute)}
	if a.readDue(l, "acme/docs", now, 0) {
		t.Errorf("an untouched repo read 10 minutes ago should wait for 20")
	}
	l.tried = now.Add(-21 * time.Minute)
	if !a.readDue(l, "acme/docs", now, 0) {
		t.Errorf("an untouched repo read 21 minutes ago should be due")
	}
	if !a.readDue(&loadState{}, "acme/docs", now, 0) {
		t.Errorf("a repo never read should be due")
	}
}

func TestAPushSinceTheLastReadMakesARepoDueAtOnce(t *testing.T) {
	a := apiApp(t)
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "acme/docs", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour)}}
	l := &loadState{tried: now.Add(-time.Minute)}
	if a.readDue(l, "acme/docs", now, 0) {
		t.Fatalf("nothing happened since the last read a minute ago")
	}
	_, _ = a.st.handle(a, stPushesMsg{pushes: []gh.Push{{Repo: "acme/docs", At: now.Add(-30 * time.Second)}}})
	if !a.readDue(l, "acme/docs", now, 0) {
		t.Errorf("a push after the last read should make the repo due")
	}
	if l.tried = now.Add(-5 * time.Second); a.readDue(l, "acme/docs", now, 0) {
		t.Errorf("a repo just read should not be read again within 15 seconds")
	}
}

func TestThePushCheckRunsEveryTwoMinutesOnAnyScreen(t *testing.T) {
	pushes := `{"data":{"viewer":{"repositories":{"pageInfo":{"hasNextPage":false},"nodes":[{"nameWithOwner":"o/r","pushedAt":"%s"}]}}}}`
	fresh := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	fake := withGitHub(t, map[string]string{"/graphql": fmt.Sprintf(pushes, fresh)})
	a := apiApp(t)
	a.st.pushesState.tried = time.Time{}
	tickFor(a, 3)
	if n := fake.count("/graphql"); n != 1 {
		t.Fatalf("the push check asked %d times in 3 seconds, want 1", n)
	}
	r, _ := a.repoByName("o/r")
	if time.Since(r.PushedAt) > 2*time.Minute {
		t.Errorf("the push check should move o/r's push time up, it is %s", r.PushedAt)
	}
}

func TestThePushCheckReadsReposTheListDidNotHold(t *testing.T) {
	a := apiApp(t)
	a.st.pushesState.begin()
	cmd, _ := a.st.handle(a, stPushesMsg{pushes: []gh.Push{{Repo: "o/r", At: time.Now()}, {Repo: "acme/new", At: time.Now()}}})
	if cmd == nil || len(a.st.newRepos) != 1 || a.st.newRepos[0] != "acme/new" || a.st.refreshing != 1 {
		t.Errorf("a repo the list didn't hold should be read in full: new %v, reading %d", a.st.newRepos, a.st.refreshing)
	}
}

func TestActionsCoversEveryRepoWithWorkflows(t *testing.T) {
	a := apiApp(t)
	a.repos = nil
	for i := range 60 {
		a.repos = append(a.repos, gh.Repo{FullName: fmt.Sprintf("acme/r%02d", i), HasActions: true, PushedAt: time.Now().Add(-time.Duration(i) * 24 * time.Hour)})
	}
	a.repos = append(a.repos, gh.Repo{FullName: "acme/old", HasActions: true, Archived: true}, gh.Repo{FullName: "acme/plain"})
	v := a.roots[tabActions].(*activityView)
	if got := len(v.repos(a)); got != 60 {
		t.Errorf("Actions covers %d repos, want all 60 with workflows", got)
	}
	a.cfg.Favourites = []string{"acme/r07"}
	v.scope = scopePinned
	if got := v.repos(a); len(got) != 1 || got[0] != "acme/r07" {
		t.Errorf("the pinned scope covers %v, want acme/r07", got)
	}
}

func TestActionsSaysHowOftenItReadsItsRepos(t *testing.T) {
	a := apiApp(t)
	a.tab, a.stack = tabActions, []View{a.roots[tabActions]}
	a.repos[0].PushedAt = time.Now().Add(-40 * 24 * time.Hour)
	out := ansi.Strip(a.View())
	for _, want := range []string{"repos: all 1 repos with workflows", "1 untouched for a day or more"} {
		if !strings.Contains(out, want) {
			t.Errorf("Actions lacks %q:\n%s", want, out)
		}
	}
}

func TestTheRepoPanelSaysHowOftenTheRepoIsRead(t *testing.T) {
	a := apiApp(t)
	a.tab, a.stack = tabRepos, []View{a.roots[tabRepos]}
	a.repos[0].PushedAt = time.Now().Add(-3 * time.Hour)
	out := ansi.Strip(a.View())
	if !strings.Contains(out, "every 2 min · worked on lately: pushed 3h ago") {
		t.Errorf("the repo panel should say how often the repo is read:\n%s", out)
	}
}

// rateGitHub answers every request with the rate limit headers of a budget
// with left requests of 5000 left.
type rateGitHub struct{ left int }

func (f rateGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("X-RateLimit-Resource", "core")
	h.Set("X-RateLimit-Limit", "5000")
	h.Set("X-RateLimit-Remaining", fmt.Sprint(f.left))
	h.Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
}

func TestQuieterReposWaitLongerWhenRequestsRunLow(t *testing.T) {
	for _, c := range []struct {
		left              int
		lately, untouched time.Duration
	}{
		{4000, 2 * time.Minute, 20 * time.Minute},
		{2000, 4 * time.Minute, 40 * time.Minute},
		{1000, 6 * time.Minute, time.Hour},
	} {
		orig := http.DefaultTransport
		http.DefaultTransport = rateGitHub{c.left}
		a := apiApp(t)
		_, _ = a.client.CurrentUser(t.Context())
		http.DefaultTransport = orig
		if got := a.readEvery(readLately); got != c.lately {
			t.Errorf("%d left: worked on lately every %s, want %s", c.left, got, c.lately)
		}
		if got := a.readEvery(readUntouched); got != c.untouched {
			t.Errorf("%d left: untouched every %s, want %s", c.left, got, c.untouched)
		}
		if got := a.readEvery(readBusy); got != busyEvery {
			t.Errorf("%d left: busy every %s, want it never to wait longer", c.left, got)
		}
	}
	if everyWords(time.Hour) != "1 h" || everyWords(15*time.Second) != "15 s" || everyWords(2*time.Minute) != "2 min" {
		t.Errorf("everyWords: %q %q %q", everyWords(time.Hour), everyWords(15*time.Second), everyWords(2*time.Minute))
	}
}
