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

const twoRuns = `{"total_count":2,"workflow_runs":[
 {"id":7,"name":"CI","display_title":"feat: one","run_number":7,"status":"completed","conclusion":"failure","head_branch":"main","actor":{"login":"sam"}},
 {"id":6,"name":"CI","display_title":"feat: two","run_number":6,"status":"completed","conclusion":"success","head_branch":"main","actor":{"login":"sam"}}]}`

func repoApp(t *testing.T, width int) (*App, *countingGitHub) {
	fake := withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs":              twoRuns,
		"/repos/o/r/actions/workflows":         `{"workflows":[{"id":1,"name":"CI","path":".github/workflows/ci.yml","state":"active"}]}`,
		"/repos/o/r/actions/runs/7/jobs":       `{"total_count":1,"jobs":[{"id":70,"name":"Tests","status":"completed","conclusion":"failure","steps":[{"name":"Unit tests","number":1,"status":"completed","conclusion":"failure"}]}]}`,
		"/repos/o/r/actions/runs/6/jobs":       `{"total_count":1,"jobs":[{"id":60,"name":"Tests","status":"completed","conclusion":"success"}]}`,
		"/repos/o/r/actions/jobs/70/logs":      "",
		"/repos/o/r/check-runs/70/annotations": "[]",
	})
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	drive(a, a.openRepo("o/r", "", 0), 5*time.Second)
	a.Update(tea.KeyMsg{Type: tea.KeyRight})
	return a, fake
}

func TestRunsAreATableAndZMakesThemCardsWithARuleBetween(t *testing.T) {
	a, _ := repoApp(t, 120)
	table := ansi.Strip(a.View())
	if !strings.Contains(table, "STATUS") || !strings.Contains(table, "feat: one") {
		t.Errorf("runs should show as a table with a header, one line each:\n%s", table)
	}
	a.Update(runes("z"))
	cards := ansi.Strip(a.View())
	if strings.Contains(cards, "STATUS") || !strings.Contains(cards, "feat: one") || !strings.Contains(cards, "CI #7") {
		t.Errorf("z should switch to cards, title above where it comes from:\n%s", cards)
	}
	if !strings.Contains(cards, strings.Repeat("─", 20)) {
		t.Errorf("the cards should be divided by a rule:\n%s", cards)
	}
}

func TestThePreviewShowsTheJobsOfTheRunUnderTheCursorAndReadsThemOnce(t *testing.T) {
	a, fake := repoApp(t, 170)
	drive(a, a.top().(*repoView).preview(a), 5*time.Second)
	screen := ansi.Strip(a.View())
	if !strings.Contains(screen, "Unit tests") || !strings.Contains(screen, "why it failed") {
		t.Errorf("the preview should list the failed job and step of the selected run:\n%s", screen)
	}
	for _, k := range []tea.KeyType{tea.KeyDown, tea.KeyUp, tea.KeyDown, tea.KeyUp} {
		_, cmd := a.Update(tea.KeyMsg{Type: k})
		drive(a, cmd, 5*time.Second)
	}
	if n, m := fake.count("/repos/o/r/actions/runs/7/jobs"), fake.count("/repos/o/r/actions/runs/6/jobs"); n != 1 || m != 1 {
		t.Errorf("each run's jobs should be read once for the preview: %d and %d requests", n, m)
	}
}

func TestANarrowScreenHasNoPreviewAndReadsNoJobs(t *testing.T) {
	a, fake := repoApp(t, 120)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyDown})
	drive(a, cmd, 2*time.Second)
	if n := fake.count("/repos/o/r/actions/runs/6/jobs") + fake.count("/repos/o/r/actions/runs/7/jobs"); n != 0 {
		t.Errorf("no preview panel, yet %d requests for jobs", n)
	}
}

func TestIconsComeFromANerdFontOnlyWhenAskedFor(t *testing.T) {
	defer func(was bool) { nerd = was }(nerd)
	nerd = false
	if got := KOK.Glyph(0); got != "✓" || icon("repo") != "" {
		t.Errorf("plain icons: passed is %q and the repo icon %q", got, icon("repo"))
	}
	nerd = true
	if got := KOK.Glyph(0); got == "✓" || icon("repo") == "" {
		t.Errorf("Nerd Font icons: passed is %q and the repo icon %q", got, icon("repo"))
	}
}

// openRepoView is the repo screen of o/r, opened at width w.
func openRepoView(t *testing.T, w int) (*App, *repoView) {
	a, _ := repoApp(t, w)
	for _, v := range a.stack {
		if rv, ok := v.(*repoView); ok {
			return a, rv
		}
	}
	t.Fatalf("no repo screen on the stack")
	return nil, nil
}

func TestEnvironmentsAndSecretsOfARepoFinishLoading(t *testing.T) {
	a, rv := openRepoView(t, 160)
	for rv.section != sectionEnvironments {
		press(a, "tab")
	}
	if rv.environments == nil || rv.environments.busy || strings.Contains(screen(a), "Loading environments") {
		t.Errorf("the environments should be read and shown:\n%s", screen(a))
	}
	press(a, "tab")
	if rv.secrets == nil || rv.secrets.busy || strings.Contains(screen(a), "Loading secrets") {
		t.Errorf("the secrets should be read and shown:\n%s", screen(a))
	}
}
