package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// personApp has a page for sam: a pull request opened and one merged, a
// review of yours, an issue, two commits on one day and a run that failed.
func personApp(t *testing.T) (*App, *personView) {
	a := apiApp(t)
	a.me = "you"
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, PushedAt: now}}
	a.st.status["acme/webshop"] = []gh.Run{{ID: 128, Name: "CI", RunNumber: 128, Status: "completed", Conclusion: "failure",
		DisplayTitle: "delivery estimates", CreatedAt: now.Add(-40 * time.Minute), Actor: gh.User{Login: "sam"}}}
	v := newPersonView(a, "sam")
	v.at = now
	v.p = gh.Person{Login: "sam", Name: "Sam Jansen",
		Opened: []gh.PersonPR{
			{Repo: "acme/webshop", Number: 57, Title: "feat: delivery estimates", State: "OPEN", CreatedAt: now.Add(-12 * time.Minute)},
			{Repo: "acme/handbook", Number: 54, Title: "docs: onboarding page", State: "MERGED", CreatedAt: now.Add(-20 * 24 * time.Hour), MergedAt: now.Add(-5 * time.Hour)},
		},
		Reviewed: []gh.PersonPR{{Repo: "acme/payments", Number: 62, Title: "fix: refunds", Author: "you", Review: "APPROVED", Reviewed: now.Add(-2 * time.Hour)}},
		Issues:   []gh.PersonIssue{{Repo: "acme/payments", Number: 33, Title: "Refund webhook times out", State: "OPEN", CreatedAt: now.Add(-26 * time.Hour)}},
		Commits: []gh.PersonCommit{
			{Repo: "acme/webshop", SHA: "aaaaaaa1", Message: "feat: estimate per carrier", At: now.Add(-41 * time.Minute)},
			{Repo: "acme/webshop", SHA: "bbbbbbb2", Message: "test: weekends", At: now.Add(-42 * time.Minute)},
		},
		ForYou: []gh.PersonPR{{Repo: "acme/webshop", Number: 57, Title: "feat: delivery estimates", State: "OPEN"}},
	}
	a.stack = append(a.stack, v)
	return a, v
}

func TestAPersonPageListsWhatTheyDidNewestFirst(t *testing.T) {
	a, v := personApp(t)
	out := ansi.Strip(a.View())
	var whats []string
	for _, e := range v.rows {
		whats = append(whats, e.whatCell().Text)
	}
	want := "opened #57, CI #128 failed, pushed 2 commits, approved #62, merged #54, opened #33"
	if got := strings.Join(whats, ", "); got != want {
		t.Errorf("activity:\n got %s\nwant %s", got, want)
	}
	wantAll(t, out, "Sam Jansen", "@sam · last 14 days, in repos you can see", "1 awaiting your review", "1 failed run", "1 merged",
		"Activity 6", "Pull requests 2", "Runs 1", "Commits 2", "Reviews 1", "Issues 1", "AGE", "WHAT", "REPO", "TITLE")
	if strings.Contains(out, "opened #48") {
		t.Errorf("a pull request opened before the 14 days should only show for what happened since")
	}
}

func TestAPersonPanelNamesTheReviewRequestsBetweenYou(t *testing.T) {
	a, _ := personApp(t)
	out := ansi.Strip(a.View())
	wantAll(t, out, "Review requests", "#57 feat: delivery estimates", "for you", "Repositories", "1 pull request · 1 run · 2 commits",
		"Activity per day", "14 days ago", "today")
}

func TestTheViewsOfAPersonKeepTheirKindAndListEachCommit(t *testing.T) {
	a, v := personApp(t)
	press(a, "tab", "tab", "tab")
	if v.view != personCommits {
		t.Fatalf("three tabs should reach Commits, view %d", v.view)
	}
	_ = a.View()
	if len(v.rows) != 2 || v.rows[0].ref != "aaaaaaa" {
		t.Errorf("Commits should list each commit: %+v", v.rows)
	}
	if out := ansi.Strip(a.View()); !strings.Contains(out, "on default branches") {
		t.Errorf("Commits should say GitHub searches only default branches:\n%s", out)
	}
}

func TestInvolvingYouKeepsTheirReviewRequestsAndTheirReviewsOfYours(t *testing.T) {
	a, v := personApp(t)
	press(a, "u")
	_ = a.View()
	var keys []string
	for _, e := range v.rows {
		keys = append(keys, e.whatCell().Text)
	}
	if got := strings.Join(keys, ", "); got != "opened #57, approved #62" {
		t.Errorf("involving you: %s", got)
	}
}

func TestTildeOpensThePageOfWhoeverStartedTheRun(t *testing.T) {
	a := apiApp(t)
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 3, Status: "completed", Conclusion: "success", Actor: gh.User{Login: "sam"}}
	a.stack = append(a.stack, newRunView(a, "acme/webshop", run))
	if !strings.Contains(barOf(a, a.top().Keys(a)), " ~ ") {
		t.Errorf("the run's key bar should offer ~ sam: %s", barOf(a, a.top().Keys(a)))
	}
	press(a, "~")
	if v, ok := a.top().(*personView); !ok || v.login != "sam" {
		t.Fatalf("~ should open sam's page, top is %T", a.top())
	}
	if a.people[0] != "sam" {
		t.Errorf("sam should be remembered for the go-to box: %v", a.people)
	}
}

func TestTheGoToBoxFindsPeopleByAt(t *testing.T) {
	a := apiApp(t)
	a.people = []string{"sam"}
	a.st.prs = []gh.PullRequest{{Repo: "acme/webshop", Number: 1, Title: "x", Author: "robin"}}
	a.pal.update(a, "@sa")
	if len(a.pal.items) == 0 || a.pal.items[0].kind != sugPerson || a.pal.items[0].cmd != "sam" {
		t.Fatalf("@sa should find sam first: %+v", a.pal.items)
	}
	a.pal.update(a, "@alex")
	found := false
	for _, it := range a.pal.items {
		found = found || it.kind == sugPerson && it.cmd == "alex"
	}
	if !found {
		t.Errorf("a login nobody knows yet should still be offered as typed")
	}
	a.mode = modeSearch
	a.pal.sel = 0
	for i, it := range a.pal.items {
		if it.cmd == "alex" {
			a.pal.sel = i
		}
	}
	drive(a, a.execute("@alex"), 0)
	if v, ok := a.top().(*personView); !ok || v.login != "alex" {
		t.Errorf("enter on @alex should open alex's page, top is %T", a.top())
	}
}
