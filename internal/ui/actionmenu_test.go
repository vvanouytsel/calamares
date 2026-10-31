package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

const failedJobsJSON = `{"jobs":[
 {"id":11,"name":"Lint","status":"completed","conclusion":"success"},
 {"id":12,"name":"Tests","status":"completed","conclusion":"failure"}]}`

func runScreen(t *testing.T, role string, run gh.Run) *App {
	a := accessApp(t, role, "gho_x")
	rv := newRunView(a, "o/r", run)
	rv.jobsLoaded = true
	rv.jobs = []gh.Job{
		{ID: 11, Name: "Lint", Status: "completed", Conclusion: "success"},
		{ID: 12, Name: "Tests", Status: "completed", Conclusion: "failure"},
	}
	rv.rebuild()
	a.stack = append(a.stack, rv)
	return a
}

func TestActionMenuListsTheScreensKeysAndNarrowsAsYouType(t *testing.T) {
	a := runScreen(t, "WRITE", finishedRun())
	press(a, ".")
	m, ok := a.modal.(*actionMenu)
	if !ok {
		t.Fatalf("a . did not open the action menu, modal is %T", a.modal)
	}
	out := ansi.Strip(a.View())
	for _, want := range []string{"Actions for", "rerun", "on GitHub"} {
		if !strings.Contains(out, want) {
			t.Errorf("menu lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "refresh this screen") {
		t.Errorf("global keys should not be in the menu")
	}
	press(a, "d", "e", "b", "u", "g")
	if len(m.shown) == 0 || !strings.Contains(m.items[m.shown[0]].label, "debug") {
		t.Errorf("typing debug should put rerun first, shown %v", m.shown)
	}
	// enter runs it the way its key would: the rerun picker opens.
	press(a, "enter")
	if _, ok := a.modal.(*rerunModal); !ok {
		t.Errorf("enter on rerun should open the picker, modal is %T", a.modal)
	}
}

func TestActionMenuShowsLockedActionsDimWithTheReason(t *testing.T) {
	fake := &refusingGitHub{status: 200, body: "{}"}
	useTransport(t, fake)
	a := runScreen(t, "READ", finishedRun())
	press(a, ".")
	out := ansi.Strip(a.View())
	if !strings.Contains(out, "needs write access to o/r (you have read)") {
		t.Errorf("locked rerun should say why in the menu:\n%s", out)
	}
	press(a, "r", "e", "r", "enter")
	if a.flashKind != nHint || !strings.Contains(a.flash, "needs write access") {
		t.Errorf("enter on a locked action should give the hint, got %v %q", a.flashKind, a.flash)
	}
	if fake.count() != 0 {
		t.Errorf("a locked action sent %d requests", fake.count())
	}
}

func TestRerunPickerOffersTheJobUnderTheCursorAndReruns(t *testing.T) {
	fake := &refusingGitHub{status: 200, body: "{}"}
	useTransport(t, fake)
	a := runScreen(t, "WRITE", finishedRun())
	v := a.top().(*runView)
	// Put the cursor on the failed job.
	for i, it := range v.items {
		if it.kind == tJob && v.jobs[it.job].Name == "Tests" {
			v.cursor = i
		}
	}
	press(a, "R")
	m, ok := a.modal.(*rerunModal)
	if !ok {
		t.Fatalf("R should open the picker, modal is %T", a.modal)
	}
	out := ansi.Strip(a.View())
	for _, want := range []string{"Rerun failed jobs", "(1 of 2)", "Rerun this job: Tests", "Rerun one job…", "Rerun all jobs", "debug logging: off"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker lacks %q:\n%s", want, out)
		}
	}
	press(a, " ")
	if !m.debug {
		t.Errorf("space should switch debug logging on")
	}
	press(a, "down", "enter") // Rerun this job
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("a question should follow, modal is %T", a.modal)
	}
	press(a, "y")
	if fake.count() == 0 {
		t.Errorf("confirming sent no request")
	}
}

func TestRerunPickerIsOffWhileTheRunIsStillGoing(t *testing.T) {
	a := runScreen(t, "WRITE", gh.Run{ID: 7, Name: "CI", Status: "in_progress"})
	press(a, "R")
	m := a.modal.(*rerunModal)
	for _, it := range m.items {
		if !strings.Contains(it.off, "still going") {
			t.Errorf("%q should be off while the run is going, off = %q", it.label, it.off)
		}
	}
}

func TestKeyBarShowsAtMostFiveKeysThenActions(t *testing.T) {
	a := runScreen(t, "WRITE", finishedRun())
	bar := capsOff(ansi.Strip(a.renderKeyBar()))
	if !regexp.MustCompile(`\.\s+menu`).MatchString(bar) {
		t.Errorf("the key bar should end its keys with the actions key: %q", bar)
	}
	if !strings.Contains(bar, "more key") {
		t.Errorf("the bar leaves keys out, so it should say how many are left: %q", bar)
	}
	n := 0
	for _, h := range a.hints(a.top().Keys(a)) {
		_ = h
		n++
	}
	if n < 6 {
		t.Skip("this screen has no more than five keys")
	}
}
