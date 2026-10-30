package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

const failedJobs = `{"jobs":[{"id":1,"name":"build","status":"completed","conclusion":"success","steps":[]},
{"id":2,"name":"Run tests","status":"completed","conclusion":"failure","steps":[{"name":"Set up","number":1,"status":"completed","conclusion":"success"},{"name":"go test","number":2,"status":"completed","conclusion":"failure"}]}]}`

func TestAFailedWatchShowsACardWithTheFailedStepAndNotifiesWhenAway(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs/7":      `{"id":7,"name":"CI","run_number":412,"status":"completed","conclusion":"failure","head_branch":"main","actor":{"login":"me"},"html_url":"https://github.com/o/r/actions/runs/7"}`,
		"/repos/o/r/actions/runs/7/jobs": failedJobs,
	})
	var title, body string
	sendNotification = func(ti, bo, url string) error { title, body = ti, bo; return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a := apiApp(t)
	a.me = "me"
	a.watches[7] = &watch{repo: "o/r", run: ghRun(7, "in_progress")}
	drive(a, a.pollWatches(), 5*time.Second)

	if len(a.toasts) != 1 || a.toasts[0].kind != nError {
		t.Fatalf("a failed run should leave one failed card: %+v", a.toasts)
	}
	screen := ansi.Strip(a.View())
	for _, want := range []string{"CI #412  failed", "o/r", "you", "Run tests › go test", "ctrl+o", "ctrl+l", "ctrl+x"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the card should say %q:\n%s", want, screen)
		}
	}
	if !strings.Contains(title, "✕ CI #412 failed") || !strings.Contains(body, "go test") {
		t.Errorf("desktop notification = %q / %q", title, body)
	}
	if n := a.notices[len(a.notices)-1]; n.kind != nError || !strings.Contains(n.text, "go test") {
		t.Errorf("the Messages screen should keep the card with its failed step: %+v", n)
	}
}

func TestNoDesktopNotificationWhileTheTerminalHasTheFocus(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/o/r/actions/runs/7": `{"id":7,"name":"CI","status":"completed","conclusion":"success"}`})
	sent := 0
	sendNotification = func(string, string, string) error { sent++; return nil }
	t.Cleanup(func() { sendNotification = notifyWithLink })
	a := apiApp(t)
	a.focus = focusYes
	a.watches[7] = &watch{repo: "o/r", run: ghRun(7, "in_progress")}
	drive(a, a.pollWatches(), 5*time.Second)
	if sent != 0 || len(a.toasts) != 1 || a.toasts[0].kind != nDone {
		t.Errorf("focused: %d notifications, cards %+v", sent, a.toasts)
	}
}

func TestACardGoesAwayAfterItsTimeAndCtrlOOpensItsRun(t *testing.T) {
	a := keysApp(t, false)
	run := gh.Run{ID: 7, Name: "Deploy", RunNumber: 88, Status: "in_progress", HTMLURL: "https://x/7"}
	a.addToast(&toast{kind: nStarted, repo: "o/r", run: run, right: "now"})
	if !strings.Contains(ansi.Strip(a.View()), "Deploy #88") {
		t.Errorf("the started card should show")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if _, ok := a.top().(*runView); !ok || len(a.toasts) != 0 {
		t.Errorf("ctrl+o should open the run and take its card away: %T, %d cards", a.top(), len(a.toasts))
	}
	a.addToast(&toast{kind: nDone, repo: "o/r", run: run})
	a.toasts[0].at = time.Now().Add(-time.Minute)
	a.expireToasts()
	if len(a.toasts) != 0 {
		t.Errorf("an old card should be gone")
	}
}

func TestADispatchedRunIsFoundWatchedAndAnnounced(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/workflows/5/runs": `{"total_count":1,"workflow_runs":[{"id":9,"name":"Deploy","run_number":88,"status":"queued","event":"workflow_dispatch","head_branch":"main","created_at":"` + now + `","actor":{"login":"me"}}]}`,
		"/repos/o/r/actions/runs/9":           `{"id":9,"name":"Deploy","run_number":88,"status":"in_progress"}`,
	})
	a := apiApp(t)
	a.me = "me"
	cmd := a.findDispatched(dispatchedPollMsg{repo: "o/r", wf: 5, since: time.Now().Add(-5 * time.Second)})
	drive(a, cmd, 5*time.Second)
	if _, ok := a.watches[9]; !ok {
		t.Fatalf("the run that the dispatch started should be watched: %v", a.watches)
	}
	if len(a.toasts) != 1 || a.toasts[0].kind != nStarted || !strings.Contains(a.toasts[0].title(), "Deploy #88") {
		t.Errorf("a started card should show: %+v", a.toasts)
	}
}

func TestEveryKindOfMessageHasItsOwnChip(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range []noticeKind{nInfo, nDone, nHint, nError, nStarted} {
		c := ansi.Strip(k.chip())
		if seen[c] || !strings.Contains(c, k.word()) {
			t.Errorf("kind %d: chip %q", k, c)
		}
		seen[c] = true
	}
}

// cardRun is a run of acme/webshop for a pull request with a long title.
func cardRun(id int64, status, conclusion string) gh.Run {
	now := time.Now()
	r := gh.Run{ID: id, Name: "Build and release", RunNumber: 1283, Status: status, Conclusion: conclusion, HeadBranch: "feat/gift-card-limits",
		Event: "pull_request", DisplayTitle: "feat: limit gift cards to five per order and show the balance left on each card",
		Actor: gh.User{Login: "sam"}, RunAttempt: 1, CreatedAt: now.Add(-4 * time.Minute), RunStartedAt: now.Add(-4 * time.Minute), UpdatedAt: now}
	r.PullRequests = append(r.PullRequests, struct {
		Number int `json:"number"`
	}{1145})
	return r
}

func TestACardIsASlabThatWrapsInsteadOfCutting(t *testing.T) {
	a := keysApp(t, false)
	for _, kind := range []noticeKind{nStarted, nDone, nError} {
		card := a.renderToast(&toast{kind: kind, repo: "acme/webshop", run: cardRun(7, "completed", "success"), at: time.Now(), life: time.Minute, right: "now"}, 64)
		text := ansi.Strip(strings.Join(card, "\n"))
		for _, want := range []string{"acme/webshop", "#1145", "each card", "feat/gift-card-limits", "sam", "pull_request"} {
			if !strings.Contains(text, want) {
				t.Errorf("the %s card lacks %q:\n%s", kind.word(), want, text)
			}
		}
		for i, l := range card {
			if width(l) != 64 {
				t.Errorf("line %d of the %s card is %d cells, want 64:\n%s", i, kind.word(), width(l), text)
			}
		}
	}
}

func TestAPassedCardComparesItsTimeWithTheUsual(t *testing.T) {
	a := keysApp(t, false)
	now := time.Now()
	run := cardRun(7, "completed", "success")
	var runs []gh.Run
	for i := range 4 {
		start := now.Add(-time.Duration(i+1) * time.Hour)
		runs = append(runs, gh.Run{ID: int64(10 + i), Name: run.Name, HeadBranch: run.HeadBranch, Status: "completed", Conclusion: "success",
			CreatedAt: start, RunStartedAt: start, UpdatedAt: start.Add(10 * time.Minute)})
	}
	a.st.status["acme/webshop"] = append([]gh.Run{run}, runs...)
	card := ansi.Strip(strings.Join(a.renderToast(&toast{kind: nDone, repo: "acme/webshop", run: run, at: now, life: time.Minute}, 64), "\n"))
	if !strings.Contains(card, "faster") || !strings.Contains(card, "median") {
		t.Errorf("a passed card should say how its time compares with the usual:\n%s", card)
	}
}

func TestAFailedCardNamesTheErrorLine(t *testing.T) {
	a := keysApp(t, false)
	run := cardRun(7, "completed", "failure")
	a.st.causes[causeID("acme/webshop", run)] = runCause{Job: "deploy-staging", Step: "Apply manifests", Text: `error: deployment "web" exceeded its progress deadline`}
	card := ansi.Strip(strings.Join(a.renderToast(&toast{kind: nError, repo: "acme/webshop", run: run, at: time.Now(), life: time.Minute}, 64), "\n"))
	for _, want := range []string{"deploy-staging › Apply manifests", "exceeded its progress", "ctrl+l"} {
		if !strings.Contains(card, want) {
			t.Errorf("the failed card lacks %q:\n%s", want, card)
		}
	}
}

// filledScreen is a screen of w×h cells, each one cell.
func filledScreen(cell string, w, h int) string {
	rows := make([]string, h)
	for i := range rows {
		rows[i] = strings.Repeat(cell, w)
	}
	return strings.Join(rows, "\n")
}

func TestOlderCardsTuckBehindTheNewestAsADeck(t *testing.T) {
	a := keysApp(t, false)
	a.addToast(&toast{kind: nDone, repo: "acme/billing", run: gh.Run{ID: 1, Name: "CI", RunNumber: 77}, right: "4m 12s"})
	a.addToast(&toast{kind: nStarted, repo: "acme/webshop", run: cardRun(2, "in_progress", ""), right: "now"})
	out := strings.Split(ansi.Strip(a.laid(filledScreen(".", 140, 30), 140, 30)), "\n")
	peek, front := -1, -1
	for i, l := range out {
		if strings.Contains(l, "CI #77 passed") && strings.Contains(l, "acme/billing") {
			peek = i
		}
		if strings.Contains(l, "Build and release #1283") && strings.Contains(l, "started") {
			front = i
		}
	}
	if peek < 0 || front < 0 || peek >= front {
		t.Fatalf("the older card should peek out above the newest:\n%s", strings.Join(out, "\n"))
	}
	if !strings.Contains(strings.Join(out, "\n"), "1 more") {
		t.Errorf("the card in front should count the one behind it:\n%s", strings.Join(out, "\n"))
	}
	if !strings.HasPrefix(out[0], "....") || len(out) != 30 {
		t.Errorf("the screen around the cards should stay as it was")
	}
}

func TestACardCastsAShadowThatKeepsTheTextUnderIt(t *testing.T) {
	inColour(t)
	a := keysApp(t, false)
	a.addToast(&toast{kind: nDone, repo: "acme/billing", run: gh.Run{ID: 1, Name: "CI", RunNumber: 77}, right: "4m 12s"})
	lines := strings.Split(a.laid(filledScreen(sText.Render("x"), 100, 30), 100, 30), "\n")
	// The card is 64 wide at the right with room for its shadow: it ends at
	// column 97, and its shadow falls on column 98.
	shaded, lit := 0, 0
	for _, l := range lines[20:30] {
		switch c := parseCells(l, 100)[98]; {
		case c.ch == "x" && nearColor(c.fg, mix(string(cText), "#000000", shadowDepth())):
			shaded++
		case c.ch == "x" && nearColor(c.fg, string(cText)):
			lit++
		}
	}
	if shaded == 0 || parseCells(lines[29], 100)[99].ch != "x" {
		t.Errorf("the column right of the card should be in its shadow, text and all (%d shaded, %d lit)", shaded, lit)
	}
}

func TestCtrlXDismissesAndCtrlLOpensTheLogOfAFailure(t *testing.T) {
	a := keysApp(t, false)
	run := gh.Run{ID: 7, Name: "Deploy", RunNumber: 88, Status: "completed", Conclusion: "failure"}
	a.addToast(&toast{kind: nStarted, repo: "o/r", run: gh.Run{ID: 8, Name: "CI"}})
	a.addToast(&toast{kind: nError, repo: "o/r", run: run})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(a.toasts) != 1 || a.toasts[0].kind != nStarted {
		t.Fatalf("ctrl+x should take the newest card away: %+v", a.toasts)
	}
	a.addToast(&toast{kind: nError, repo: "o/r", run: run})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if _, ok := a.top().(*logView); !ok {
		t.Errorf("ctrl+l should open the log of the failed run, the screen is %T", a.top())
	}
}

func TestAFailedCardStaysLongerThanAPassedOne(t *testing.T) {
	a := keysApp(t, false)
	a.addToast(&toast{kind: nDone, repo: "o/r", run: gh.Run{ID: 1}})
	a.addToast(&toast{kind: nError, repo: "o/r", run: gh.Run{ID: 2}})
	for _, c := range a.toasts {
		c.at = time.Now().Add(-time.Minute)
	}
	a.expireToasts()
	if len(a.toasts) != 1 || a.toasts[0].kind != nError {
		t.Errorf("after a minute only the failure should still show: %+v", a.toasts)
	}
}

func TestTheBottomEdgeRunsOutWithTheCardsTime(t *testing.T) {
	old := animLevel
	animLevel = animFull
	t.Cleanup(func() { animLevel = old })
	fresh := drainCells(time.Now(), 10*time.Second, 20)
	half := drainCells(time.Now().Add(-5*time.Second), 10*time.Second, 20)
	gone := drainCells(time.Now().Add(-time.Minute), 10*time.Second, 20)
	if fresh != 20 || half != 10 || gone != 0 {
		t.Errorf("the edge should be lit for the time left: fresh %d, half way %d, gone %d", fresh, half, gone)
	}
}
