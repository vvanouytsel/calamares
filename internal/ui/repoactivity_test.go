package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

var everything = config.RepoWatch{Repo: "acme/webshop", Activity: config.AllActivity}

func webshopActivity(at time.Time) gh.RepoActivity {
	return gh.RepoActivity{ReadAt: at, Branch: "main", Head: "c3",
		Commits:  []gh.ActivityCommit{{SHA: "c3", Title: "fix: totals", Author: "robin"}, {SHA: "c2"}, {SHA: "c1"}},
		Pulls:    []gh.ActivityPull{{Number: 57, Title: "feat: pay by invoice", State: "OPEN", Author: "robin", CreatedAt: at.Add(-time.Hour)}},
		Releases: []gh.ActivityRelease{{Tag: "v2.3.1", Author: "sam", At: at.Add(-24 * time.Hour)}},
	}
}

func newsTexts(news []repoNews) string {
	var out []string
	for _, n := range news {
		out = append(out, n.Text)
	}
	return strings.Join(out, " | ")
}

func TestARepoTellsItsNewCommitsPullRequestsAndReleases(t *testing.T) {
	then := time.Now().Add(-30 * time.Second)
	old := webshopActivity(then)
	cur := old
	cur.ReadAt, cur.Head = time.Now(), "c5"
	cur.Commits = append([]gh.ActivityCommit{{SHA: "c5", Title: "feat: gift cards", Author: "alex"}, {SHA: "c4", Title: "test: gift cards", Author: "alex"}}, old.Commits...)
	cur.Pulls = []gh.ActivityPull{
		{Number: 61, Title: "feat: gift cards", State: "OPEN", Author: "alex", CreatedAt: time.Now()},
		{Number: 57, Title: "feat: pay by invoice", State: "MERGED", MergedBy: "sam", MergedAt: time.Now()},
	}
	cur.Releases = append([]gh.ActivityRelease{{Tag: "v2.4.0", Author: "sam", At: time.Now()}}, old.Releases...)
	got := newsTexts(activityNews(old, cur, everything, "me", time.Now()))
	want := "2 new commits on main | #57 merged by sam | #61 opened by alex | v2.4.0 released by sam"
	if got != want {
		t.Errorf("news:\n got %s\nwant %s", got, want)
	}
}

func TestARepoTellsOnlyTheKindsYouPicked(t *testing.T) {
	old := webshopActivity(time.Now().Add(-time.Minute))
	cur := old
	cur.Head = "c4"
	cur.Commits = append([]gh.ActivityCommit{{SHA: "c4", Author: "alex"}}, old.Commits...)
	cur.Releases = append([]gh.ActivityRelease{{Tag: "v2.4.0", Author: "sam", At: time.Now()}}, old.Releases...)
	w := config.RepoWatch{Repo: "acme/webshop", Activity: []string{config.ActivityReleases}}
	if got := newsTexts(activityNews(old, cur, w, "me", time.Now())); got != "v2.4.0 released by sam" {
		t.Errorf("only releases were picked: %s", got)
	}
}

func TestWhatYouDidOnARepoIsNotNews(t *testing.T) {
	old := webshopActivity(time.Now().Add(-time.Minute))
	cur := old
	cur.Head = "c4"
	cur.Commits = append([]gh.ActivityCommit{{SHA: "c4", Author: "me"}}, old.Commits...)
	cur.Pulls = []gh.ActivityPull{{Number: 62, State: "OPEN", Author: "me", CreatedAt: time.Now()}, {Number: 57, State: "MERGED", MergedBy: "me", MergedAt: time.Now()}}
	cur.Releases = append([]gh.ActivityRelease{{Tag: "v2.4.0", Author: "me", At: time.Now()}}, old.Releases...)
	if news := activityNews(old, cur, everything, "me", time.Now()); len(news) != 0 {
		t.Errorf("your own push, pull request, merge and release are news: %s", newsTexts(news))
	}
}

func TestAnOldPullRequestOrReleaseComingIntoViewIsNotNews(t *testing.T) {
	old := webshopActivity(time.Now().Add(-time.Minute))
	cur := old
	cur.Pulls = append(cur.Pulls, gh.ActivityPull{Number: 12, State: "OPEN", Author: "alex", CreatedAt: time.Now().Add(-30 * 24 * time.Hour)},
		gh.ActivityPull{Number: 9, State: "MERGED", MergedBy: "alex", MergedAt: time.Now().Add(-30 * 24 * time.Hour)})
	cur.Releases = append(cur.Releases, gh.ActivityRelease{Tag: "v1.0.0", Author: "sam", At: time.Now().Add(-365 * 24 * time.Hour)})
	if news := activityNews(old, cur, everything, "me", time.Now()); len(news) != 0 {
		t.Errorf("old ones that came into view are news: %s", newsTexts(news))
	}
}

func TestARewrittenBranchSaysSo(t *testing.T) {
	old := webshopActivity(time.Now().Add(-time.Minute))
	cur := old
	cur.Head = "x2"
	cur.Commits = []gh.ActivityCommit{{SHA: "x2", Author: "alex"}, {SHA: "x1", Author: "alex"}}
	if got := newsTexts(activityNews(old, cur, everything, "me", time.Now())); got != "the history of main was rewritten" {
		t.Errorf("got %q", got)
	}
}

func TestARepoWatchShowsACardForWhatIsNewAndRemembersIt(t *testing.T) {
	a := repoWatchApp(t, everything)
	a.me = "me"
	var sent []string
	sendNotification = func(title, body, url string) error { sent = append(sent, title); return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	old := webshopActivity(time.Now().Add(-time.Minute))
	drive(a, a.handleRepoActivity(repoActivityMsg{repo: "acme/webshop", act: old}), time.Second)
	if len(a.toasts) != 0 {
		t.Fatalf("the first read only notes how the repo is, got %d cards", len(a.toasts))
	}
	cur := old
	cur.ReadAt, cur.Head = time.Now(), "c4"
	cur.Commits = append([]gh.ActivityCommit{{SHA: "c4", Title: "feat: gift cards", Author: "alex"}}, old.Commits...)
	cur.Releases = append([]gh.ActivityRelease{{Tag: "v2.4.0", Name: "Gift cards", Author: "sam", At: time.Now()}}, old.Releases...)
	drive(a, a.handleRepoActivity(repoActivityMsg{repo: "acme/webshop", act: cur}), time.Second)
	if len(a.toasts) != 2 {
		t.Fatalf("a push and a release are two cards, got %d", len(a.toasts))
	}
	card := ansi.Strip(strings.Join(a.renderToast(a.toasts[0], 64), "\n"))
	for _, want := range []string{"pushed", "1 new commit on main", "c4", "feat: gift cards", "alex"} {
		if !strings.Contains(card, want) {
			t.Errorf("the commit card lacks %q:\n%s", want, card)
		}
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "v2.4.0 released by sam") {
		t.Errorf("out of focus, only the release is a desktop notification: %v", sent)
	}
	b := New(a.client, a.cfg, Options{Version: "dev"})
	b.loadRepoActivity()
	if st := b.repoWatch("acme/webshop"); !st.actKnown || st.act.Head != "c4" || len(st.news) != 2 {
		t.Errorf("what the watch knows should survive a restart: known %v head %q news %d", st.actKnown, st.act.Head, len(st.news))
	}
}

func TestThePickerTicksEveryActivityForANewWatchAndSavesTheChoice(t *testing.T) {
	m := newRepoWatchModal("acme/webshop", "main", []gh.Workflow{{Name: "CI", Path: ciFile}}, config.RepoWatch{}, false)
	out := ansi.Strip(m.Render(keysApp(t, false), 100, 40))
	for _, want := range []string{"ACTIVITY", "[x] new commits on main", "[x] pull requests opened and merged", "[x] releases published"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker lacks %q:\n%s", want, out)
		}
	}
	m.sel = m.activityRow(1)
	m.toggle()
	w, _ := m.result()
	if strings.Join(w.Activity, ",") != "commits,releases" {
		t.Errorf("activity = %v", w.Activity)
	}
	old := newRepoWatchModal("acme/webshop", "main", nil, config.RepoWatch{Repo: "acme/webshop"}, true)
	if w, _ := old.result(); len(w.Activity) != 0 {
		t.Errorf("a watch you already have keeps runs only until you change it: %v", w.Activity)
	}
}

func TestWatchingReposListsWhatEachRepoTellsAndItsChanges(t *testing.T) {
	a := repoWatchApp(t, everything)
	st := a.repoWatch("acme/webshop")
	st.actKnown = true
	st.news = []repoNews{{prNews: prNews{At: time.Now(), Kind: KOK, Word: "released", Text: "v2.4.0 released by sam"}}}
	a.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
	v := a.roots[tabWatching].(*watchView)
	v.view = watchRepos
	v.sync(a)
	out := ansi.Strip(v.Render(a, 220, 30))
	for _, want := range []string{"ACTIVITY", "LAST CHANGE", "commits, pull requests and releases", "v2.4.0 released by sam", "Changes", "Activity"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Repos view lacks %q:\n%s", want, out)
		}
	}
}
