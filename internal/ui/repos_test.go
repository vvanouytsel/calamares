package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func sortApp(t *testing.T) (*App, *reposView) {
	a := keysApp(t, false)
	now := time.Now()
	a.repos = []gh.Repo{
		{FullName: "acme/handbook", HasActions: true, PushedAt: now.Add(-3 * time.Hour), Health: []string{"SUCCESS"}},
		{FullName: "acme/webshop", HasActions: true, PushedAt: now.Add(-40 * 24 * time.Hour), Health: []string{"FAILURE"}},
		{FullName: "acme/payments", HasActions: true, PushedAt: now.Add(-5 * time.Minute), Health: []string{"SUCCESS"}},
	}
	a.st.loaded = true
	a.cfg.ToggleFavourite("acme/webshop")
	v := newReposView(a)
	return a, v
}

func TestReposSortsPinnedFirstThenByPushByDefault(t *testing.T) {
	a, v := sortApp(t)
	v.rebuild(a)
	if got := strings.Join(v.t.FilteredKeys(), " "); got != "acme/webshop acme/payments acme/handbook" {
		t.Errorf("order = %s", got)
	}
}

func TestReposSortsByEachOrder(t *testing.T) {
	for order, want := range map[string]string{
		"pushed":  "acme/payments acme/handbook acme/webshop",
		"name":    "acme/handbook acme/payments acme/webshop",
		"failing": "acme/webshop acme/payments acme/handbook",
	} {
		a, v := sortApp(t)
		v.order = order
		v.rebuild(a)
		if got := strings.Join(v.t.FilteredKeys(), " "); got != want {
			t.Errorf("%s: order = %s, want %s", order, got, want)
		}
	}
}

func TestTheClocksOfPushedLineUp(t *testing.T) {
	a, v := sortApp(t)
	v.rebuild(a)
	out := ansi.Strip(v.t.Render(160, 10))
	clock := strings.TrimSpace(icon("clock"))
	col := -1
	for _, line := range strings.Split(out, "\n") {
		i := strings.LastIndex(line, clock)
		if i < 0 {
			continue
		}
		at := ansi.StringWidth(line[:i])
		if col >= 0 && at != col {
			t.Errorf("a clock at column %d, another at %d:\n%s", at, col, out)
		}
		col = at
	}
	if col < 0 {
		t.Fatalf("no clocks in:\n%s", out)
	}
}

func TestASweepListsReposByTheirLastPush(t *testing.T) {
	a := keysApp(t, false)
	now := time.Now()
	page := []gh.Repo{
		{FullName: "acme/handbook", PushedAt: now.Add(-time.Hour)},
		{FullName: "acme/webshop", PushedAt: now.Add(-time.Minute)},
	}
	a.st.reposState.busy = true
	a.st.handle(a, stReposMsg{repos: page})
	if a.repos[0].FullName != "acme/webshop" {
		t.Errorf("the latest push should come first: %v, %v", a.repos[0].FullName, a.repos[1].FullName)
	}
}
