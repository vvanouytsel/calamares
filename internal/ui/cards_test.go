package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestTheSectionsOfAPanelAreLayersNotBoxes(t *testing.T) {
	p := newPanel(40)
	p.add("acme/webshop")
	p.label("RUNS")
	p.add("CI #128")
	p.callout(cWait, "waits for your approval")
	out := ansi.Strip(strings.Join(p.render(12), "\n"))
	wantAll(t, out, "acme/webshop", "RUNS ───", "CI #128", "▌ waits for your approval")
	if strings.ContainsAny(out, "╭╰│") {
		t.Errorf("a section is a title with a hairline, not a box:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if width(l) > 40 {
			t.Errorf("a section is wider than the panel: %q", l)
		}
	}
}

func TestACalloutRightUnderALabelIsThatSectionsBody(t *testing.T) {
	p := newPanel(40)
	p.label("NEXT")
	p.callout(cWait, "approve it")
	out := strings.Split(ansi.Strip(strings.Join(p.render(8), "\n")), "\n")
	if !strings.Contains(out[0], "NEXT ──") || !strings.Contains(out[1], "▌ approve it") {
		t.Errorf("the callout should sit under the NEXT title:\n%q", out)
	}
}

func TestFailingDrawsEachCauseAsASectionAndOneWithoutAnErrorLineDashed(t *testing.T) {
	a := failingApp(t)
	a.st.causes[causeID("acme/billing", a.st.status["acme/billing"][0])] = runCause{Job: "test", Step: "Unit tests", Text: noErrorLine}
	_, out := groupedFailing(a)
	wantAll(t, out, "toomanyrequests", "2 repos", "┄┄", "? no error line in the log", "Unit tests", "▌")
}

func TestAnOpenJobIsASectionAndAFoldedOneAPill(t *testing.T) {
	now := time.Now()
	a := accessApp(t, "WRITE", "gho_x")
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 3, Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-time.Hour), RunStartedAt: now.Add(-time.Hour)}
	v := newRunView(a, "o/r", run)
	v.loaded, v.jobsLoaded = true, true
	v.jobs = []gh.Job{
		{ID: 1, Name: "Lint", Status: "completed", Conclusion: "success", StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-59 * time.Minute)},
		{ID: 2, Name: "Tests", Status: "completed", Conclusion: "failure", StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-56 * time.Minute),
			Steps: []gh.Step{{Name: "Unit tests", Number: 1, Status: "completed", Conclusion: "failure"}}},
		{ID: 3, Name: "Build image", Status: "completed", Conclusion: "skipped"},
	}
	v.rebuild()
	out := ansi.Strip(v.Render(a, 140, 30))
	wantAll(t, out, "Tests", "▌", "Unit tests", pillLeft, "Lint", "Build image")
	if strings.Contains(out, "Jobs ──") {
		t.Errorf("the jobs stand as sections of their own, without a title over them:\n%s", out)
	}
}

func TestWithoutANerdFontTheActiveViewKeepsItsBrackets(t *testing.T) {
	defer func(was bool) { nerd = was }(nerd)
	nerd = false
	out := ansi.Strip(viewStrip([]stripItem{{Label: "Assigned", Count: "3"}, {Label: "Created"}}, 0, 80))
	if !strings.Contains(out, "[ Assigned 3 ]") {
		t.Errorf("plain fonts have no round ends, so the brackets stay: %q", out)
	}
}

// branchRunsApp holds successful CI runs of acme/api: three on main, two on
// feature branches, and the run compared, on the branch given.
func branchRunsApp(t *testing.T, branch string) (*App, gh.Run) {
	now := time.Now()
	a := sharedApp(t)
	for i := range a.repos {
		a.repos[i].DefaultBranch = "main"
	}
	ran := func(id int64, branch string, minutes int) gh.Run {
		return gh.Run{ID: id, Name: "CI", RunNumber: int(id), HeadBranch: branch, Status: "completed", Conclusion: "success",
			CreatedAt: now.Add(-time.Duration(id) * time.Hour), RunStartedAt: now.Add(-time.Duration(id) * time.Hour), UpdatedAt: now.Add(-time.Duration(id)*time.Hour + time.Duration(minutes)*time.Minute)}
	}
	run := ran(1, branch, 5)
	a.st.status["acme/api"] = []gh.Run{run, ran(2, "main", 30), ran(3, "feat/a", 4), ran(4, "main", 31), ran(5, "feat/b", 6), ran(6, "main", 32)}
	return a, run
}

func TestARunOnTheDefaultBranchIsComparedWithRunsOnItInThePanel(t *testing.T) {
	a, run := branchRunsApp(t, "main")
	out := ansi.Strip(strings.Join(a.runPanel(repoRun{"acme/api", run}, 60, 60), "\n"))
	wantAll(t, out, "median 31m 00s of 3", "previous #2")
}

func TestARunOnABranchIsComparedWithRunsOnOtherBranchesInThePanel(t *testing.T) {
	a, run := branchRunsApp(t, "feat/c")
	out := ansi.Strip(strings.Join(a.runPanel(repoRun{"acme/api", run}, 60, 60), "\n"))
	if strings.Contains(out, "of 3 runs") || strings.Contains(out, "#2 ") {
		t.Errorf("a branch run should not be compared with the runs on main:\n%s", out)
	}
	wantAll(t, out, "#3")
}

func TestTheRunHeaderSaysWhichBranchesItIsComparedWith(t *testing.T) {
	a, run := branchRunsApp(t, "main")
	if got := scopeText(a.branchScope("acme/api", run)); got != "on main" {
		t.Errorf("a run on main: %q", got)
	}
	run.HeadBranch = "feat/c"
	if got := scopeText(a.branchScope("acme/api", run)); got != "on branches other than main" {
		t.Errorf("a run on a branch: %q", got)
	}
}
