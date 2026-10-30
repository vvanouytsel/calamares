package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

const gb = 1 << 30

// cachesApp holds webshop with a cache near its limit, much of it for pull
// requests, and docs with a small one.
func cachesApp(t *testing.T) (*App, *activityView) {
	a := keysApp(t, false)
	now := time.Now()
	a.repos = []gh.Repo{
		{FullName: "acme/webshop", HasActions: true, PushedAt: now},
		{FullName: "acme/docs", HasActions: true, PushedAt: now.Add(-time.Hour)},
	}
	a.st.loaded = true
	a.st.caches.byRepo["acme/webshop"] = gh.Caches{Size: 85 * gb / 10, Count: 120, Largest: []gh.Cache{
		{ID: 1, Ref: "refs/heads/main", Key: "node-modules-linux-4f9c", Size: 2 * gb, LastAccessed: now.Add(-time.Hour)},
		{ID: 2, Ref: "refs/pull/61/merge", Key: "node-modules-linux-77aa", Size: 15 * gb / 10, LastAccessed: now.Add(-2 * time.Hour)},
		{ID: 3, Ref: "refs/pull/57/merge", Key: "node-modules-linux-88bb", Size: gb, LastAccessed: now.Add(-3 * time.Hour)},
	}}
	a.st.caches.byRepo["acme/docs"] = gh.Caches{Size: 200 << 20, Count: 3, Largest: []gh.Cache{{ID: 9, Ref: "refs/heads/main", Key: "hugo", Size: 200 << 20}}}
	for _, r := range []string{"acme/webshop", "acme/docs"} {
		stateIn(a.st.caches.state, r).at = now
	}
	v := newPlatformView(a)
	v.setFilter(viewCaches)
	v.sync(a)
	return a, v
}

func TestCachesListsHowFullEachRepoIsAndWhatOnlyPullRequestsUse(t *testing.T) {
	a, v := cachesApp(t)
	out := ansi.Strip(v.Render(a, 190, 40))
	wantAll(t, out, "Caches", "1 near the limit", "STATUS", "USED", "PULL REQUESTS", "LARGEST",
		"Near limit", "8.5 GB of 10 GB", "2.5 GB", "node-modules-linux-4f9c", "Healthy", "200.0 MB",
		"Space by branch", "#61", "Largest caches", "Next step", "clear pull request caches")
	if !strings.Contains(out, "Caches 2") {
		t.Errorf("the strip should count the repos with a cache:\n%s", out)
	}
}

func TestClearingPullRequestCachesDeletesOnlyThose(t *testing.T) {
	a, v := cachesApp(t)
	fake := withGitHub(t, map[string]string{})
	c, _ := v.caches.selected()
	if c.repo != "acme/webshop" {
		t.Fatalf("the fullest repo should come first, got %q", c.repo)
	}
	cmd, _ := a.press(v.caches.keys(a), "X")
	drive(a, cmd, time.Second)
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("X should ask first, modal %T", a.modal)
	}
	press(a, "y")
	for _, id := range []string{"2", "3"} {
		if fake.hits["/repos/acme/webshop/actions/caches/"+id] != 1 {
			t.Errorf("cache %s of a pull request should be deleted: %v", id, fake.hits)
		}
	}
	if fake.hits["/repos/acme/webshop/actions/caches/1"] != 0 {
		t.Error("the cache of main must stay")
	}
}

func TestAFullCacheIsMaintenanceOnTheOverview(t *testing.T) {
	a, v := cachesApp(t)
	v.setFilter(viewOverview)
	v.sync(a)
	out := ansi.Strip(v.Render(a, 190, 40))
	wantAll(t, out, "Actions cache", "Cache near its limit: 8.5 GB of 10 GB")
}

func TestACacheRefNamesItsBranchOrPullRequest(t *testing.T) {
	for ref, want := range map[string]string{"refs/heads/main": "main", "refs/pull/61/merge": "#61", "refs/tags/v2.4.0": "v2.4.0"} {
		if got := refLabel(ref); got != want {
			t.Errorf("refLabel(%q) = %q, want %q", ref, got, want)
		}
	}
	if n := (gh.Cache{Ref: "refs/pull/61/merge"}).PullNumber(); n != 61 {
		t.Errorf("PullNumber = %d", n)
	}
}
