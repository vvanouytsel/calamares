package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestAnErrorCanStillBeReadAfterTheKeyBarMovedOn(t *testing.T) {
	a := keysApp(t, false)
	a.setFlash(nError, "Could not read the runs of o/r: GitHub API: HTTP 502")
	a.setFlash(nInfo, "Live updates on")
	if top := ansi.Strip(a.View()); !strings.Contains(top, "! 1 error") {
		t.Errorf("the top bar should say an error is waiting to be read:\n%s", top)
	}
	a.Update(runes("!"))
	screen := ansi.Strip(a.View())
	if !strings.Contains(screen, "HTTP 502") || !strings.Contains(screen, "Live updates on") {
		t.Errorf("the Messages screen should list both messages:\n%s", screen)
	}
	if strings.Contains(screen, "! 1 error") || a.unreadErrors != 0 {
		t.Errorf("reading the messages should clear the count in the top bar")
	}
	a.Update(runes("!"))
	if _, still := a.top().(*noticesView); still {
		t.Errorf("! again should close the Messages screen")
	}
}

func TestWatchesAreThereAgainAfterARestart(t *testing.T) {
	a := keysApp(t, false)
	running := gh.Run{ID: 7, Name: "CI", RunNumber: 3, Status: "in_progress"}
	old := gh.Run{ID: 8, Name: "CI", RunNumber: 2, Status: "completed", Conclusion: "success"}
	a.watches[7] = &watch{repo: "o/r", run: running, since: time.Now()}
	a.watches[8] = &watch{repo: "o/r", run: old, since: time.Now().Add(-48 * time.Hour), notified: true}
	a.saveWatches()

	b := New(a.client, a.cfg, Options{}) // the next session, with the same saved cache
	b.loadWatches()
	if w, ok := b.watches[7]; !ok || w.repo != "o/r" || !w.run.Active() {
		t.Errorf("the running watch should be back: %+v", b.watches)
	}
	if _, ok := b.watches[8]; ok {
		t.Errorf("a run that finished two days ago should not come back")
	}
}

func TestARerunIsWatchedAndTheOldResultIsNotMistakenForTheNewOne(t *testing.T) {
	a := keysApp(t, false)
	var sent []string
	orig := sendNotification
	sendNotification = func(title, body, url string) error { sent = append(sent, title); return nil }
	t.Cleanup(func() { sendNotification = orig })

	failed := gh.Run{ID: 7, Name: "CI", RunNumber: 3, RunAttempt: 1, Status: "completed", Conclusion: "failure"}
	a.watchRerun("o/r", failed)
	if w := a.watches[7]; w == nil || !w.run.Active() {
		t.Fatalf("the rerun should be watched as still to run: %+v", w)
	}
	// GitHub hasn't started the new attempt yet and still reports the old one.
	drive(a, a.handleWatchPolled(watchPolledMsg{repo: "o/r", id: 7, run: failed}), time.Second)
	if len(sent) != 0 || !a.watches[7].run.Active() {
		t.Errorf("the result of the attempt that was rerun was taken for the new one: %v", sent)
	}
	passed := failed
	passed.RunAttempt, passed.Conclusion = 2, "success"
	drive(a, a.handleWatchPolled(watchPolledMsg{repo: "o/r", id: 7, run: passed}), time.Second)
	if len(sent) != 1 || !strings.Contains(sent[0], "passed") {
		t.Errorf("the new attempt finishing should notify once: %v", sent)
	}
}

func TestManyRequestsGitHubDidNotAnswerAreOneMessageThatCountsUp(t *testing.T) {
	a := keysApp(t, false)
	for _, repo := range []string{"acme/webshop", "acme/payments", "acme/handbook"} {
		a.setFlash(nError, "Could not read the runs of "+repo+": "+gh.ErrNoAnswer.Error())
	}
	var told []string
	for _, n := range a.notices {
		if strings.Contains(n.text, gh.ErrNoAnswer.Error()) {
			told = append(told, n.text)
		}
	}
	if len(told) != 1 || !strings.Contains(told[0], "3 requests timed out") {
		t.Errorf("three requests GitHub did not answer should be one message counting 3, got %q", told)
	}
	if a.unreadErrors != 1 {
		t.Errorf("they are one error to read, got %d", a.unreadErrors)
	}
	if !strings.Contains(a.flash, "3 requests timed out") {
		t.Errorf("the key bar should say how many: %q", a.flash)
	}
}

func TestManyRequestsGitHubFailedOnAreOneMessageThatCountsUp(t *testing.T) {
	a := keysApp(t, false)
	for _, repo := range []string{"acme/webshop", "acme/payments"} {
		err := &gh.APIError{Status: 502, Tries: 3}
		a.setFlash(nError, "Could not read the runs of "+repo+": "+err.Error())
	}
	a.setFlash(nError, "Could not read the runs of acme/handbook: "+gh.ErrNoAnswer.Error())
	if len(a.notices) != 2 {
		t.Fatalf("two that GitHub failed on and one it did not answer are two messages, got %d", len(a.notices))
	}
	if got := a.notices[0].text; !strings.Contains(got, "2 requests failed (HTTP 502 Bad Gateway, tried 3 times)") {
		t.Errorf("the message should count both: %q", got)
	}
}

func TestOtherErrorsStayMessagesOfTheirOwn(t *testing.T) {
	a := keysApp(t, false)
	a.setFlash(nError, "Could not read the runs of acme/webshop: GitHub API: Not Found (HTTP 404)")
	a.setFlash(nError, "Could not read the runs of acme/payments: GitHub API: Not Found (HTTP 404)")
	if len(a.notices) != 2 {
		t.Errorf("errors of another kind each keep their message, got %d", len(a.notices))
	}
}

func TestAWatchedRunThatIsGoneFromGitHubIsNoLongerAskedFor(t *testing.T) {
	a := keysApp(t, false)
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 12, Status: "in_progress"}
	a.watches[7] = &watch{repo: "acme/webshop", run: run, since: time.Now()}
	gone := watchPolledMsg{repo: "acme/webshop", id: 7, err: &gh.APIError{Status: 404, Message: "Not Found"}}
	a.handleWatchPolled(gone)
	a.handleWatchPolled(gone)
	if _, ok := a.watches[7]; !ok {
		t.Fatal("two 404s could still be a hiccup")
	}
	a.handleWatchPolled(gone)
	if _, ok := a.watches[7]; ok {
		t.Error("a deleted run should stop being watched")
	}
	if !strings.Contains(a.flash, "Stopped watching CI #12") {
		t.Errorf("the key bar should say so: %q", a.flash)
	}
}
