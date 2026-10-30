package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// overviewApp holds a new build.yml v4 that fails more than v3, an older
// build.yml v2 in billing, a nightly schedule that failed and one GitHub
// disabled.
func overviewApp(t *testing.T) *App {
	a := releaseApp(t, "failure", "success", "failure", "success", "failure", "success")
	a.st.scheduled = true
	a.st.schedules = []gh.Schedule{
		{Repo: "acme/api", File: ".github/workflows/nightly.yml", Crons: []string{"0 3 * * *"}},
		{Repo: "acme/web", File: ".github/workflows/cleanup.yml", Crons: []string{"0 4 * * 1"}},
	}
	d := &a.st.sched
	d.workflows["acme/api"] = []gh.Workflow{{ID: 1, Name: "Nightly", Path: ".github/workflows/nightly.yml", State: "active"}}
	d.workflows["acme/web"] = []gh.Workflow{{ID: 2, Name: "Cleanup", Path: ".github/workflows/cleanup.yml", State: "disabled_inactivity"}}
	failed := mainRun(9, 1, "failure", time.Now().Add(-5*time.Hour))
	d.runs[scheduleKey("acme/api", ".github/workflows/nightly.yml")] = &failed
	return a
}

func TestPlatformOpensOnAnOverviewOfFailuresAndMaintenance(t *testing.T) {
	a := overviewApp(t)
	v := newPlatformView(a)
	if v.filter != viewOverview {
		t.Fatalf("Platform should open on the Overview, it opens on view %d", v.filter)
	}
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "Platform", "Shared workflows, actions, schedules and runners of", "Overview",
		"Failures", "Workflows and jobs that fail or cannot start", "ITEM", "ISSUE", "SINCE",
		"build.yml v4", "Fails more than v3", "Nightly", "Last scheduled run failed", "5 hours",
		"Maintenance", "Still works, but needs an update to keep working", "REPOS",
		"build.yml v3, v2", "Older version", "Cleanup", "Schedule disabled by GitHub",
		"2 failures", "2 maintenance items")
	if strings.Index(out, "Failures") > strings.Index(out, "Maintenance") {
		t.Error("failures should come before maintenance")
	}
	if s := lineWith(out, "Status"); !strings.Contains(s, "Fails more than v3") {
		t.Errorf("the panel should be the one of build.yml v4 in Shared workflows: %q", s)
	}
}

func TestEnterOnAnIssueOpensItInItsViewAndItsKeysWorkOnTheOverview(t *testing.T) {
	a := overviewApp(t)
	v := newPlatformView(a)
	v.sync(a)
	if !strings.Contains(barOf(a, v.Keys(a)), " = ") {
		t.Errorf("the keys of the version in Shared workflows should work here: %s", barOf(a, v.Keys(a)))
	}
	v.Update(a, tea.KeyMsg{Type: tea.KeyDown})
	if is, _ := v.overview.selected(); is.item != "Nightly" {
		t.Fatalf("the second failure should be Nightly, got %q", is.item)
	}
	v.Update(a, tea.KeyMsg{Type: tea.KeyEnter})
	if v.filter != viewScheduled {
		t.Fatalf("enter should open Scheduled, it is on view %d", v.filter)
	}
	if s, ok := v.scheduled.selected(); !ok || s.name() != "Nightly" {
		t.Errorf("Scheduled should have Nightly under the cursor, has %q", s.name())
	}
}

func TestTheOverviewSaysWhatIsHealthy(t *testing.T) {
	a := sharedApp(t)
	v := newPlatformView(a)
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "Maintenance", "build.yml v2", "Healthy", "Shared actions", "Schedules", "Runners")
	if strings.Contains(out, "Failures") {
		t.Errorf("without failures the section is left out:\n%s", out)
	}
}

func TestACronLineIsSaidInWords(t *testing.T) {
	for cron, want := range map[string]string{
		"0 3 * * *":    "Daily at 03:00",
		"30 2 * * 1":   "Mondays at 02:30",
		"0 6 * * 1-5":  "Weekdays at 06:00",
		"*/30 * * * *": "Every 30 minutes",
		"15 * * * *":   "Hourly at :15",
		"0 0 1 * *":    "0 0 1 * *",
	} {
		if got := cronWords(cron); got != want {
			t.Errorf("%q: got %q, want %q", cron, got, want)
		}
	}
}

func TestPluralsOfWordsEndingInY(t *testing.T) {
	if got := plural("repository", 2); got != "repositories" {
		t.Errorf("got %q", got)
	}
	if got := plural("day", 2); got != "days" {
		t.Errorf("got %q", got)
	}
}
