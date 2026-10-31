package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestSomeoneWhoKnewTheOldKeysIsToldOnceWhichMoved(t *testing.T) {
	a := apiApp(t)
	a.cfg.KeysVersion = 1
	drive(a, a.keysNews(), time.Second)
	if _, ok := a.modal.(*messageModal); !ok {
		t.Fatalf("an older key map should be told about, modal is %T", a.modal)
	}
	out := ansi.Strip(a.View())
	for _, want := range []string{"Keys changed", "approve", "cancel a run", "set aside"} {
		if !strings.Contains(out, want) {
			t.Errorf("the notice lacks %q:\n%s", want, out)
		}
	}
	if a.cfg.KeysVersion != config.CurrentKeys || a.keysNews() != nil {
		t.Errorf("the notice should be shown only once")
	}
	if b := apiApp(t); b.keysNews() != nil {
		t.Errorf("someone new has nothing to unlearn")
	}
}

func TestARunStillGoingIsFollowedFromTheStart(t *testing.T) {
	a := apiApp(t)
	if !newRunView(a, "o/r", ghRun(7, "in_progress")).follow {
		t.Errorf("a running run should be followed without pressing f")
	}
	if newRunView(a, "o/r", ghRun(7, "completed")).follow {
		t.Errorf("a finished run has nothing to follow")
	}
}

func TestTheRunKeyBarNamesWhatDAndTShow(t *testing.T) {
	a := apiApp(t)
	run := ghRun(7, "completed")
	run.Conclusion, run.HeadBranch = "failure", "main"
	v := newRunView(a, "o/r", run)
	if got := v.diffDesc(); got != "diff vs last pass" {
		t.Errorf("without a known passing run d says %q", got)
	}
	v.previous = &gh.Run{RunNumber: 212, HeadBranch: "main"}
	if got := v.diffDesc(); got != "diff vs #212 ✓" {
		t.Errorf("d should name the run that passed, got %q", got)
	}
	for mode, want := range map[string]string{config.CompareMedian: "times vs usual", config.ComparePrevious: "times vs previous", config.CompareOff: "plain times"} {
		a.cfg.CompareDurations = mode
		if got := timesDesc(a); got != want {
			t.Errorf("compare %q: t says %q, want %q", mode, got, want)
		}
	}
}

func TestTheMergePickerOffersAutoMerge(t *testing.T) {
	labels := func(items []pickItem) string {
		var out []string
		for _, it := range items {
			out = append(out, it.label)
		}
		return strings.Join(out, ", ")
	}
	d := gh.PullRequestDetail{MergeMethods: []string{"squash", "merge"}, AutoMergeAllowed: true, CanEnableAutoMerge: true}
	if got := labels(mergeItems(d)); got != "squash, merge, squash when checks pass, merge when checks pass" {
		t.Errorf("with auto-merge allowed: %s", got)
	}
	d.AutoMerge, d.CanDisableAutoMerge = &gh.AutoMerge{Method: "squash"}, true
	if got := labels(mergeItems(d)); got != "squash, merge, turn off auto-merge" {
		t.Errorf("with auto-merge on: %s", got)
	}
	d = gh.PullRequestDetail{MergeMethods: []string{"squash"}}
	if got := labels(mergeItems(d)); got != "squash" {
		t.Errorf("without auto-merge: %s", got)
	}
}

func TestHelpPutsTheKeysThatChangeGitHubApart(t *testing.T) {
	a := keysApp(t, false)
	rv := newRunView(a, "o/r", gh.Run{ID: 1, Name: "CI", RunNumber: 7, Status: "completed", Conclusion: "failure"})
	a.stack = append(a.stack, rv)
	a.Update(runes("?"))
	help := ansi.Strip(a.View())
	if !strings.Contains(help, "CHANGE GITHUB") || !strings.Contains(help, "rerun the failed jobs") {
		t.Errorf("the help should list rerun under CHANGE GITHUB:\n%s", help)
	}
}
