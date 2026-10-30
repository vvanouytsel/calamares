package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func openPR() gh.PullRequestPulse {
	return gh.PullRequestPulse{
		PullRequest: gh.PullRequest{Number: 57, Title: "Pay by invoice", Repo: "acme/webshop", Branch: "invoice", Base: "main", Author: "robin",
			Checks: gh.CheckSummary{State: "PENDING", Total: 4, Pending: 4}},
		State: "OPEN", Mergeable: "MERGEABLE", HeadSHA: "aaa", Commits: 3, PushedBy: "robin", Comments: 1, LastCommentBy: "robin",
	}
}

func words(news []prNews) string {
	var w []string
	for _, n := range news {
		w = append(w, n.Word)
	}
	return strings.Join(w, " ")
}

func TestAPullRequestTellsWhatChangedOnIt(t *testing.T) {
	now := time.Now()
	old := openPR()
	cur := old
	cur.Checks = gh.CheckSummary{State: "FAILURE", Total: 4, Passed: 3, Failed: 1}
	cur.Reviews = []gh.Review{{Author: "sam", State: "APPROVED", SubmittedAt: now}}
	cur.Comments, cur.LastCommentBy = 3, "sam"
	news := prChanges(old, cur, "me", now)
	if got := words(news); got != "failed approved commented" {
		t.Fatalf("news = %q", got)
	}
	if news[0].Text != "1 of 4 checks failed" || news[1].Text != "sam approved it" || news[2].Text != "2 new comments, the last by sam" {
		t.Errorf("texts = %+v", news)
	}
}

func TestWhatYouDidYourselfIsNotNews(t *testing.T) {
	now := time.Now()
	old := openPR()
	cur := old
	cur.HeadSHA, cur.Commits, cur.PushedBy = "bbb", 4, "me"
	cur.Reviews = []gh.Review{{Author: "me", State: "COMMENTED", SubmittedAt: now}}
	cur.Comments, cur.LastCommentBy = 2, "me"
	if news := prChanges(old, cur, "me", now); len(news) != 0 {
		t.Errorf("your own push, review and comment are news: %q", words(news))
	}
}

func TestAMergedPullRequestSaysOnlyThat(t *testing.T) {
	old := openPR()
	cur := old
	cur.State, cur.MergedBy = "MERGED", "sam"
	cur.Checks.State = "SUCCESS"
	news := prChanges(old, cur, "me", time.Now())
	if len(news) != 1 || news[0].Text != "merged by sam" {
		t.Errorf("news = %+v", news)
	}
}

func TestAPushOfSomeoneElseAndTheChecksAfterIt(t *testing.T) {
	old := openPR()
	old.Checks = gh.CheckSummary{State: "SUCCESS", Total: 4, Passed: 4}
	cur := old
	cur.HeadSHA, cur.Commits, cur.PushedBy = "bbb", 5, "sam"
	cur.Checks = gh.CheckSummary{State: "PENDING", Total: 4, Pending: 4}
	if got := words(prChanges(old, cur, "me", time.Now())); got != "pushed" {
		t.Errorf("a push: %q", got)
	}
	next := cur
	next.Checks = gh.CheckSummary{State: "SUCCESS", Total: 4, Passed: 4}
	if news := prChanges(cur, next, "me", time.Now()); words(news) != "passed" || news[0].Text != "all 4 checks passed" {
		t.Errorf("the checks after it: %+v", news)
	}
}

func TestConflictsDraftsAndReopening(t *testing.T) {
	old := openPR()
	cur := old
	cur.Mergeable, cur.Draft = "CONFLICTING", true
	if got := words(prChanges(old, cur, "me", time.Now())); got != "back to draft conflicting" {
		t.Errorf("got %q", got)
	}
	closed := old
	closed.State = "CLOSED"
	if got := words(prChanges(closed, old, "me", time.Now())); got != "reopened" {
		t.Errorf("reopened: %q", got)
	}
}

func TestAWatchedPullRequestShowsACardOnlyWhenSomethingChanged(t *testing.T) {
	a := keysApp(t, false)
	a.me = "me"
	var sent []string
	sendNotification = func(title, body, url string) error { sent = append(sent, title); return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a.watchPR("acme/webshop", 57) // its read is left out: the fake GitHub has no pull request
	if a.prWatches[prWatchKey("acme/webshop", 57)] == nil {
		t.Fatal("w should watch the pull request")
	}
	a.toasts = nil
	first := openPR()
	drive(a, a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: first}), time.Second)
	if len(a.toasts) != 0 {
		t.Fatalf("the first read only notes how it is, got %d cards", len(a.toasts))
	}
	approved := first
	approved.Reviews = []gh.Review{{Author: "sam", State: "APPROVED", SubmittedAt: time.Now()}}
	approved.ReviewDecision = "APPROVED"
	drive(a, a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: approved}), time.Second)
	if len(a.toasts) != 1 || a.toasts[0].word() != "approved" {
		t.Fatalf("an approval should be a card: %+v", a.toasts)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "#57 approved") {
		t.Errorf("out of focus, it is a desktop notification too: %v", sent)
	}
	card := ansi.Strip(strings.Join(a.renderToast(a.toasts[0], 64), "\n"))
	for _, want := range []string{"#57", "Pay by invoice", "sam approved it", "invoice"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card lacks %q:\n%s", want, card)
		}
	}
	drive(a, a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: approved}), time.Second)
	if len(a.toasts) != 1 {
		t.Errorf("nothing changed, yet another card: %d", len(a.toasts))
	}
	if w := a.prWatches[prWatchKey("acme/webshop", 57)]; len(w.News) != 1 {
		t.Errorf("the watch should remember the approval: %+v", w.News)
	}
}

func TestPullRequestWatchesSurviveARestart(t *testing.T) {
	a := keysApp(t, false)
	a.watchPR("acme/webshop", 57) // its read is left out: the fake GitHub has no pull request
	a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: openPR()})
	b := New(a.client, a.cfg, Options{Version: "dev"})
	b.loadPRWatches()
	w := b.prWatches[prWatchKey("acme/webshop", 57)]
	if w == nil || !w.Known || w.Pulse.Title != "Pay by invoice" {
		t.Errorf("the watch should come back as it was: %+v", w)
	}
}

func TestWatchingListsWatchedPullRequestsInAViewOfTheirOwn(t *testing.T) {
	a := keysApp(t, false)
	a.prWatches[prWatchKey("acme/webshop", 57)] = &prWatch{Repo: "acme/webshop", Number: 57, Since: time.Now(), Known: true, Pulse: openPR(),
		News: []prNews{{At: time.Now(), Kind: KOK, Word: "approved", Text: "sam approved it"}}}
	v := newWatchView(a)
	v.view = watchPRs
	v.sync(a)
	out := ansi.Strip(v.Render(a, 160, 30))
	for _, want := range []string{"Pull requests 1", "#57", "Pay by invoice", "sam approved it", "Status", "Changes", "LAST CHANGE"} {
		if !strings.Contains(out, want) {
			t.Errorf("the view lacks %q:\n%s", want, out)
		}
	}
	if got := hintKeys(a, v.Keys(a)); !strings.Contains(got, " enter ") || !strings.Contains(got, " w ") {
		t.Errorf("keys = %s", got)
	}
}

func TestAnOlderReviewReadAgainIsNotNews(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	old := openPR()
	old.Reviews = []gh.Review{{Author: "sam", State: "APPROVED", SubmittedAt: at}}
	cur := old
	cur.Reviews = []gh.Review{{Author: "sam", State: "APPROVED", SubmittedAt: at.Add(-time.Second)}}
	if news := prChanges(old, cur, "me", time.Now()); len(news) != 0 {
		t.Errorf("the same review is news: %q", words(news))
	}
}

func TestTheListOfPullRequestsFollowsTheirWatch(t *testing.T) {
	a := keysApp(t, false)
	a.st.prs = []gh.PullRequest{{Number: 57, Repo: "acme/webshop", Title: "Pay by invoice", Why: gh.WhyReview, Checks: gh.CheckSummary{State: "PENDING", Total: 4, Pending: 4}}}
	a.watchPR("acme/webshop", 57)
	p := openPR()
	p.Checks = gh.CheckSummary{State: "SUCCESS", Total: 4, Passed: 4}
	a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: p})
	if got := a.st.prs[0]; got.Checks.Passed != 4 || got.Why != gh.WhyReview {
		t.Errorf("the list should show the checks the watch read and keep why it is listed: %+v", got)
	}
	p.State = "MERGED"
	a.handlePRPulse(prPulseMsg{repo: "acme/webshop", number: 57, pulse: p})
	if len(a.st.prs) != 0 {
		t.Errorf("a merged pull request leaves the list of open ones: %+v", a.st.prs)
	}
}
