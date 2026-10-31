package ui

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestStatusKind(t *testing.T) {
	cases := []struct {
		status, conclusion string
		want               Kind
	}{
		{"completed", "success", KOK},
		{"completed", "failure", KFail},
		{"completed", "timed_out", KFail},
		{"completed", "cancelled", KCancel},
		{"completed", "skipped", KSkip},
		{"completed", "action_required", KWait},
		{"in_progress", "", KRun},
		{"queued", "", KQueue},
		{"waiting", "", KWait},
		{"pending", "", KQueue},
	}
	for _, c := range cases {
		if got := StatusKind(c.status, c.conclusion); got != c.want {
			t.Errorf("StatusKind(%q, %q) = %v, want %v", c.status, c.conclusion, got, c.want)
		}
	}
}

func TestFuzzy(t *testing.T) {
	if _, _, ok := fuzzy("pay", "acme/payments-api"); !ok {
		t.Fatal("pay should match payments-api")
	}
	if _, _, ok := fuzzy("xyz", "acme/payments-api"); ok {
		t.Fatal("xyz should not match")
	}
	a, _, _ := fuzzy("pay", "acme/payments-api")
	b, _, _ := fuzzy("pay", "acme/p-a-y-thing-with-a-long-name")
	if a <= b {
		t.Errorf("contiguous word-start match should win: %d vs %d", a, b)
	}
	_, idx, _ := fuzzy("pmapi", "acme/payments-api")
	if len(idx) != 5 {
		t.Errorf("expected 5 matched positions, got %v", idx)
	}
}

func TestTableFilter(t *testing.T) {
	tb := newTable(Column{Title: "STATUS"}, Column{Title: "BRANCH"}, Column{Title: "ACTOR"})
	tb.SetRows([]Row{
		{Key: "1", Cells: []Cell{{Text: "✓ success"}, {Text: "main"}, {Text: "alice"}}},
		{Key: "2", Cells: []Cell{{Text: "✗ failure"}, {Text: "feat/x"}, {Text: "bob"}}},
		{Key: "3", Cells: []Cell{{Text: "✗ failure"}, {Text: "main"}, {Text: "alice"}}},
	})
	tb.SetFilter("branch:main", "alice")
	if tb.Len() != 2 {
		t.Errorf("branch:main → %d rows, want 2", tb.Len())
	}
	tb.SetFilter("fail actor:@me", "alice")
	if tb.Len() != 1 || tb.Selected().Key != "3" {
		t.Errorf("fail actor:@me → %d rows", tb.Len())
	}
	tb.SetFilter("", "")
	tb.SetCursor(2)
	tb.SetRows([]Row{
		{Key: "0", Cells: []Cell{{Text: "new"}}},
		{Key: "1", Cells: []Cell{{Text: "x"}}},
		{Key: "2", Cells: []Cell{{Text: "x"}}},
		{Key: "3", Cells: []Cell{{Text: "x"}}},
	})
	if tb.Selected().Key != "3" {
		t.Errorf("cursor should stay on key 3 after refresh, got %s", tb.Selected().Key)
	}
}

func TestQueryMatching(t *testing.T) {
	fields := map[string][]string{
		"branch":   {"dependabot/npm/lodash-4.17"},
		"workflow": {"Build and release"},
		"actor":    {"alice"},
	}
	values := func(field string) []string {
		if field == "" {
			var all []string
			for _, v := range fields {
				all = append(all, v...)
			}
			return all
		}
		var out []string
		for name, v := range fields {
			if strings.HasPrefix(name, field) {
				out = append(out, v...)
			}
		}
		return out
	}
	cases := []struct {
		q    string
		want bool
	}{
		{"", true},
		{"lodash", true},
		{"branch:main", false},
		{"branch:main,dependabot", true},
		{"-branch:dependabot/*", false},
		{"-branch:renovate/*", true},
		{"branch:dependabot/*", true},
		{"branch:npm/*", false},
		{`workflow:"build and release"`, true},
		{`workflow:"build and test"`, false},
		{"actor:@me", true},
		{"br:dependabot wo:build", true},
		{"nosuchfield:x", false},
		{"-nosuchfield:x", true},
	}
	for _, c := range cases {
		if got := parseQuery(c.q, "Alice").match(values); got != c.want {
			t.Errorf("%q matched = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestTableFilterTakesEitherValueOrExcludes(t *testing.T) {
	tb := newTable(Column{Title: "BRANCH"}, Column{Title: "ACTOR"})
	tb.SetRows([]Row{
		{Key: "1", Cells: []Cell{{Text: "main"}, {Text: "alice"}}},
		{Key: "2", Cells: []Cell{{Text: "develop"}, {Text: "bob"}}},
		{Key: "3", Cells: []Cell{{Text: "feat/x"}, {Text: "dependabot[bot]"}}},
	})
	tb.SetFilter("branch:main,develop", "")
	if tb.Len() != 2 {
		t.Errorf("branch:main,develop → %d rows, want 2", tb.Len())
	}
	tb.SetFilter("-actor:*[bot]", "")
	if tb.Len() != 2 {
		t.Errorf("-actor:*[bot] → %d rows, want 2", tb.Len())
	}
}

func savedFilterApp(t *testing.T, filter string) *App {
	t.Setenv("CALAMARES_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	cfg := config.Default()
	cfg.RunFilter = filter
	a := New(gh.NewWithToken("github.com", "x", "test"), cfg, Options{Version: "dev"})
	a.me = "me"
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "org/r", DefaultBranch: "main", HasActions: true, PushedAt: now}}
	a.st.loaded = true
	a.st.status["org/r"] = []gh.Run{
		{ID: 2, Name: "CI", HeadBranch: "dependabot/npm/x", Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-time.Minute), Actor: gh.User{Login: "me"}},
		{ID: 1, Name: "CI", HeadBranch: "main", Status: "completed", Conclusion: "success", CreatedAt: now.Add(-time.Hour), Actor: gh.User{Login: "me"}},
	}
	return a
}

func TestSavedFilterHidesRunsUntilSwitchedOff(t *testing.T) {
	a := savedFilterApp(t, "-branch:dependabot/*")
	if got := recentRuns(a, []string{"org/r"}); len(got) != 1 || got[0].run.ID != 1 {
		t.Fatalf("saved filter should hide the dependabot run, got %d runs", len(got))
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	if _, ok := a.modal.(*savedFilterModal); !ok {
		t.Fatalf("F should open the saved filter, modal is %T", a.modal)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := recentRuns(a, []string{"org/r"}); len(got) != 2 || a.cfg.RunFilter == "" {
		t.Errorf("switching the saved filter off should show every run and keep the filter: %d runs, filter %q", len(got), a.cfg.RunFilter)
	}
}

func TestFilterCommandSavesTheFilter(t *testing.T) {
	a := savedFilterApp(t, "")
	a.execute(":filter -branch:dependabot/* -actor:*[bot]")
	if a.cfg.RunFilter != "-branch:dependabot/* -actor:*[bot]" || !a.filtering() {
		t.Fatalf("filter not saved: %q", a.cfg.RunFilter)
	}
	saved, err := config.Load()
	if err != nil || saved.RunFilter != a.cfg.RunFilter {
		t.Errorf("filter not written to the config file: %q, %v", saved.RunFilter, err)
	}
	a.execute(":filter")
	if a.filtering() {
		t.Errorf(":filter without a value should clear it")
	}
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune(":")},
		{Type: tea.KeyRunes, Runes: []rune(`filter workflow:"Build and release"`)},
		{Type: tea.KeyEnter},
	} {
		a.Update(msg)
	}
	if a.cfg.RunFilter != `workflow:"Build and release"` {
		t.Errorf("typing the command in the search box saved %q", a.cfg.RunFilter)
	}
}

func TestTableRenderFits(t *testing.T) {
	tb := newTable(Column{Title: "NAME", Flex: true}, Column{Title: "AGE", Right: true})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: strings.Repeat("long-name-", 20)}, {Text: "3d"}}}})
	for _, l := range strings.Split(tb.Render(40, 5), "\n") {
		if w := width(l); w > 40 {
			t.Errorf("line is %d cells wide, want ≤ 40: %q", w, l)
		}
	}
}

func TestTableUsesSpareWidthBeyondMax(t *testing.T) {
	tb := newTable(Column{Title: "REPO", Max: 10, Flex: true}, Column{Title: "AGE", Right: true})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "my-actions-edge-feature"}, {Text: "3d"}}}})
	if out := tb.Render(80, 5); !strings.Contains(out, "my-actions-edge-feature") {
		t.Errorf("a wide table should show the full name, got:\n%s", out)
	}
	if out := tb.Render(20, 5); strings.Contains(out, "my-actions-edge-feature") {
		t.Errorf("a narrow table should still cut the name, got:\n%s", out)
	}
}

func TestTableColumnsKeepTheirWidthWhenWideRowsLeave(t *testing.T) {
	tb := newTable(Column{Title: "NAME"}, Column{Title: "AGE"})
	tb.SetRows([]Row{
		{Key: "a", Cells: []Cell{{Text: "a-very-long-repository-name"}, {Text: "1m"}}},
		{Key: "b", Cells: []Cell{{Text: "short"}, {Text: "2m"}}},
	})
	header := func() string { return ansi.Strip(strings.Split(tb.Render(100, 5), "\n")[0]) }
	before := header()
	tb.SetRows([]Row{{Key: "b", Cells: []Cell{{Text: "short"}, {Text: "2m"}}}})
	if after := header(); after != before {
		t.Errorf("header moved when the wide row left:\n%q\n%q", before, after)
	}
	tb.ResetWidths()
	if after := header(); after == before {
		t.Errorf("columns should shrink to their content after ResetWidths")
	}
}

func TestTableMinWidthKeepsTickingValuesFromShiftingColumns(t *testing.T) {
	tb := newTable(Column{Title: "TIME", Right: true, Min: 5}, Column{Title: "NAME"})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "59s"}, {Text: "x"}}}})
	before := strings.Index(ansi.Strip(tb.Render(40, 3)), "NAME")
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "10:00"}, {Text: "x"}}}})
	tb.ResetWidths()
	if after := strings.Index(ansi.Strip(tb.Render(40, 3)), "NAME"); after != before {
		t.Errorf("NAME moved from %d to %d when the time grew", before, after)
	}
}

func TestMidCutKeepsTheEndOfLongNames(t *testing.T) {
	got := midCut("my-actions-edge-feature", 18)
	if got != "my-ac…edge-feature" {
		t.Errorf("midCut = %q", got)
	}
	if width(got) != 18 {
		t.Errorf("midCut is %d wide, want 18", width(got))
	}
}

func TestTableHiddenColumnIsNotDrawn(t *testing.T) {
	tb := newTable(Column{Title: "NAME"}, Column{Title: "ACTOR"})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "x"}, {Text: "alice"}}}})
	tb.Col("ACTOR").Hidden = true
	if out := tb.Render(40, 3); strings.Contains(out, "ACTOR") || strings.Contains(out, "alice") {
		t.Errorf("hidden column was drawn:\n%s", out)
	}
}

func TestTableTwoLineRowsShowTheBelowColumnUnderneath(t *testing.T) {
	tb := newTable(Column{Title: "STATUS"}, Column{Title: "TITLE", Below: true}, Column{Title: "AGE"})
	tb.SetRows([]Row{
		{Key: "a", Cells: []Cell{{Text: "ok"}, {Text: "first title"}, {Text: "1m"}}},
		{Key: "b", Cells: []Cell{{Text: "ok"}, {Text: "second title"}, {Text: "2m"}}},
	})
	tb.TwoLine = true
	lines := strings.Split(ansi.Strip(tb.Render(40, 10)), "\n")
	if strings.Contains(lines[0], "TITLE") {
		t.Errorf("the title column should leave the header: %q", lines[0])
	}
	if !strings.Contains(lines[2], "first title") || !strings.Contains(lines[4], "second title") {
		t.Errorf("titles should sit under their rows:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTableDetailDescribesTheSelectedRow(t *testing.T) {
	tb := newTable(Column{Title: "NAME", Max: 5})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "alpha"}}}, {Key: "b", Cells: []Cell{{Text: "bravo"}}}})
	tb.Detail = func(r Row) []string { return []string{"full name of " + r.Key} }
	tb.SetCursor(1)
	out := tb.Render(40, 10)
	if lines := strings.Split(out, "\n"); len(lines) != 10 || lines[9] != "full name of b" {
		t.Errorf("detail should fill the last line, got:\n%s", out)
	}
	if out := tb.Render(40, 4); strings.Contains(out, "full name") {
		t.Errorf("detail should give way when there is no room, got:\n%s", out)
	}
}

func TestShortLeavesOutTheOwnerOfMostRepos(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.me = "me"
	if got := a.short("me/tool"); got != "tool" {
		t.Errorf("before repos load, your own name is left out: got %q", got)
	}
	a.repos = []gh.Repo{{FullName: "Acme/api"}, {FullName: "Acme/web"}, {FullName: "me/tool"}}
	if got := a.short("Acme/api"); got != "api" {
		t.Errorf("short(Acme/api) = %q, want api", got)
	}
	if got := a.short("me/tool"); got != "me/tool" {
		t.Errorf("short(me/tool) = %q, other owners stay", got)
	}
}

// activityApp is the Actions place on Last 7 days, which lists every run.
func activityApp(runs ...gh.Run) (*App, *activityView) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.st.loaded = true
	a.st.status["o/r"] = runs
	v := newActivityView(a)
	v.filter = viewRecent
	return a, v
}

func activityRun(id int64, status string, created time.Time) gh.Run {
	r := gh.Run{ID: id, Status: status, CreatedAt: created, Actor: gh.User{Login: "alice"}}
	if status == "completed" {
		r.Conclusion = "success"
	}
	return r
}

func TestActivityHoldsNewRunsWhileYouAreBelowTheTop(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-2*time.Hour)), activityRun(2, "completed", now.Add(-time.Hour)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.t.SetCursor(1)
	a.st.status["o/r"] = append(a.st.status["o/r"], activityRun(3, "completed", now.Add(-time.Minute)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 2 || v.held != 1 {
		t.Fatalf("new run should wait: %d rows, %d held", v.t.Len(), v.held)
	}
	if v.t.Selected().Key != "1" || v.t.Cursor() != 1 {
		t.Errorf("cursor should stay on run 1 in the same place")
	}
	v.Update(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 3 || v.held != 0 {
		t.Errorf("n should show the new run: %d rows, %d held", v.t.Len(), v.held)
	}
}

func TestActivityShowsNewRunsAtOnceWhenYouAreAtTheTop(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Hour)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	a.st.status["o/r"] = append(a.st.status["o/r"], activityRun(2, "completed", now.Add(-time.Minute)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 2 {
		t.Errorf("got %d rows, want 2", v.t.Len())
	}
}

func TestActivityKeepsAFinishedRunDimmedForAWhile(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "in_progress", now.Add(-time.Minute)))
	v.setFilter(viewRunning)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	a.st.status["o/r"] = []gh.Run{activityRun(1, "completed", now.Add(-time.Minute))}
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 1 || v.t.Selected().Color == nil {
		t.Fatalf("finished run should stay, dimmed: %d rows", v.t.Len())
	}
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now.Add(fadeOut))
	if v.t.Len() != 0 {
		t.Errorf("finished run should be gone after %s", fadeOut)
	}
}

func TestActivityDoesNotKeepRunsFromThePreviousFilter(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Minute)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.setFilter(viewRunning)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Len() != 0 {
		t.Errorf("switching filter should not leave old rows fading: %d rows", v.t.Len())
	}
}

func TestActivityShowsTheNewConclusionOfARunThatRanAgain(t *testing.T) {
	now := time.Now()
	run := activityRun(1, "completed", now.Add(-time.Hour))
	run.UpdatedAt = now.Add(-50 * time.Minute)
	a, v := activityApp(run)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	run.Conclusion, run.UpdatedAt = "failure", now.Add(-time.Minute)
	a.st.status["o/r"] = []gh.Run{run}
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if got := v.t.Selected().Cells[0].Text; !strings.Contains(got, "failed") {
		t.Errorf("status cell = %q, want the run to show as failed", got)
	}
}

func TestActivityAgeOfAFinishedRunKeepsCounting(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Hour)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now.Add(2*time.Hour))
	if got := v.t.Selected().Cells[7].Text; got != "3h" {
		t.Errorf("age = %q, want 3h", got)
	}
}

func TestActivityRowsTakeOnANewTheme(t *testing.T) {
	inColour(t)
	was := currentTheme
	t.Cleanup(func() { applyTheme(was) })
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Hour)))
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	before := v.t.Selected().Cells[0].Styled
	for _, th := range themes {
		if th.Green != was.Green {
			applyTheme(th)
			break
		}
	}
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	if v.t.Selected().Cells[0].Styled == before {
		t.Error("the status cell kept the colours of the previous theme")
	}
}

func TestActivityHidesActorWhenShowingOnlyYourRuns(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Minute)))
	a.me = "alice"
	v.Update(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	if out := v.Render(a, 160, 30); strings.Contains(out, "ACTOR") {
		t.Errorf("ACTOR column should be hidden with only your runs")
	}
}

func TestBoxDimensions(t *testing.T) {
	out := box(title("Runs", "acme/api", 12, ""), "hello\nworld", 50, 6, cFrame)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("box has %d lines, want 6", len(lines))
	}
	for _, l := range lines {
		if width(l) != 50 {
			t.Errorf("box line is %d wide, want 50", width(l))
		}
	}
}

func TestFormatters(t *testing.T) {
	now := time.Now()
	if got := age(now.Add(-90*time.Second), now); got != "1m" {
		t.Errorf("age = %s", got)
	}
	if got := dur(142 * time.Second); got != "2m 22s" {
		t.Errorf("dur = %s", got)
	}
	if got := dur(4 * time.Second); got != "4s" {
		t.Errorf("dur = %s", got)
	}
	if got := thousands(4812); got != "4,812" {
		t.Errorf("thousands = %s", got)
	}
	if got := groupName("test (ubuntu-latest, 1.23)"); got != "test" {
		t.Errorf("groupName = %s", got)
	}
}

func TestEveryScreenRendersWithoutData(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, k := range []string{"1", "2", "3", "4", "/", "esc", "?"} {
		var msg tea.KeyMsg
		switch k {
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		a.Update(msg)
		out := a.View()
		for i, l := range strings.Split(out, "\n") {
			if w := width(l); w > 120 {
				t.Errorf("after %q line %d is %d wide", k, i, w)
			}
		}
	}
	_ = a.openRepo("me/x", "main", 0)
	if out := a.View(); !strings.Contains(out, "Workflows") {
		t.Errorf("repo screen should show the workflows sidebar")
	}
}

// A rerun replaces the job list (often with fewer jobs and no steps yet);
// the cursor must survive without indexing into the old list.
func TestRunViewSurvivesShrinkingJobs(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	run := gh.Run{ID: 1, Name: "CI", Status: "completed", Conclusion: "failure"}
	v := newRunView(a, "o/r", run)
	steps := []gh.Step{{Name: "a", Number: 1, Status: "completed", Conclusion: "success"}, {Name: "b", Number: 2, Status: "completed", Conclusion: "failure"}}
	v.Update(a, runDataMsg{id: v.id, hasJobs: true, jobs: []gh.Job{
		{ID: 10, Name: "lint", Status: "completed", Conclusion: "success", Steps: steps[:1]},
		{ID: 11, Name: "test", Status: "completed", Conclusion: "failure", Steps: steps},
	}})
	if it, ok := v.selected(); !ok || it.kind != tStep {
		t.Fatalf("cursor should start on the failed step, got %+v", it)
	}
	v.Update(a, runDataMsg{id: v.id, hasJobs: true, jobs: []gh.Job{{ID: 12, Name: "test", Status: "queued"}}})
	_ = v.Render(a, 120, 30)
	v.Update(a, runDataMsg{id: v.id, hasJobs: true, jobs: []gh.Job{{ID: 12, Name: "test", Status: "queued"}, {ID: 13, Name: "lint", Status: "queued"}}})
	_ = v.Render(a, 120, 30)
}

func mkRuns(ids ...int64) []gh.Run {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var rs []gh.Run
	for _, id := range ids {
		rs = append(rs, gh.Run{ID: id, Status: "completed", Conclusion: "success", CreatedAt: base.Add(time.Duration(id) * time.Minute)})
	}
	return rs
}

func TestRunsMergeKeepsOlderRuns(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	v := newRunsView(a, "o/r", nil, "")
	v.Update(a, runsMsg{id: v.id, runs: mkRuns(60, 59, 58), page: 1, total: 6})
	v.Update(a, runsMsg{id: v.id, runs: mkRuns(57, 56, 55), page: 2, total: 6})
	// two new runs arrive; the refreshed first page pushes 58 out of it
	v.Update(a, runsMsg{id: v.id, runs: mkRuns(62, 61, 60), page: 1, total: 8})
	if len(v.runs) != 8 {
		t.Fatalf("got %d runs after refresh, want 8 (nothing dropped)", len(v.runs))
	}
	for i := 1; i < len(v.runs); i++ {
		if !v.runs[i-1].CreatedAt.After(v.runs[i].CreatedAt) {
			t.Fatalf("runs not newest-first at %d", i)
		}
	}
	if v.nextPage != 3 {
		t.Errorf("nextPage = %d, want 3", v.nextPage)
	}
}

func TestRunsIgnoresStaleFilterResponse(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	v := newRunsView(a, "o/r", nil, "")
	v.Update(a, runsMsg{id: v.id, runs: mkRuns(3, 2, 1), page: 1, total: 3})
	v.branch = "feature"
	_ = v.resetFilter(a)
	v.Update(a, runsMsg{id: v.id, gen: 0, runs: mkRuns(9, 8, 7), page: 1, total: 3}) // late answer for the old filter
	if len(v.runs) != 0 {
		t.Fatalf("stale response was applied: %d runs", len(v.runs))
	}
	v.Update(a, runsMsg{id: v.id, gen: v.gen, runs: mkRuns(5), page: 1, total: 1})
	if len(v.runs) != 1 || v.runs[0].ID != 5 {
		t.Fatalf("filtered list = %+v", v.runs)
	}
}

type fakeGitHub map[string]string

func (f fakeGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := f[r.URL.Path]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	}
	h := http.Header{}
	h.Set("ETag", `"`+r.URL.Path+`"`)
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestRecentRunsKeepsRunsStartedTogetherInPlace(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	pushed := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	var repos []string
	for i := int64(1); i <= 10; i++ {
		repo := fmt.Sprintf("org/r%d", i)
		repos = append(repos, repo)
		a.st.status[repo] = []gh.Run{{ID: i, CreatedAt: pushed}}
	}
	order := func() string {
		var ids []string
		for _, x := range recentRuns(a, repos) {
			ids = append(ids, fmt.Sprint(x.run.ID))
		}
		return strings.Join(ids, " ")
	}
	want := "10 9 8 7 6 5 4 3 2 1"
	for i := 0; i < 20; i++ {
		if got := order(); got != want {
			t.Fatalf("redraw %d ordered runs %q, want %q", i, got, want)
		}
	}
}

func branchPickerRepo(t *testing.T) (*App, *repoView, *runsView) {
	orig := http.DefaultTransport
	http.DefaultTransport = fakeGitHub{
		"/repos/o/r/branches": `[{"name":"main"},{"name":"develop"},{"name":"feat/x"}]`,
	}
	t.Cleanup(func() { http.DefaultTransport = orig })
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	a.repos = []gh.Repo{{FullName: "o/r", DefaultBranch: "main"}}
	v := newRepoView(a, "o/r", "")
	p, _ := v.pane(a)
	now := time.Now()
	p.runs = []gh.Run{
		{ID: 2, Name: "CI", HeadBranch: "feat/x", Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-time.Minute)},
		{ID: 1, Name: "CI", HeadBranch: "main", Status: "completed", Conclusion: "success", CreatedAt: now.Add(-time.Hour)},
	}
	p.loaded = true
	return a, v, p
}

func pickerLabels(m *pickerModal) []string {
	var out []string
	for _, i := range m.shown {
		out = append(out, m.items[i].label)
	}
	return out
}

func TestBranchPickerListsBranchesWithRunsFirst(t *testing.T) {
	a, v, _ := branchPickerRepo(t)
	drive(a, v.key(a, "b"), 5*time.Second)
	m, ok := a.modal.(*pickerModal)
	if !ok {
		t.Fatalf("b should open the branch picker, modal is %T", a.modal)
	}
	want := "all branches feat/x main develop"
	if got := strings.Join(pickerLabels(m), " "); got != want {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestPickingABranchFiltersEveryWorkflow(t *testing.T) {
	a, v, p := branchPickerRepo(t)
	v.panes[99] = newRunsView(a, "o/r", &gh.Workflow{ID: 99}, "")
	drive(a, v.key(a, "b"), 5*time.Second)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("dev")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.modal != nil {
		t.Fatalf("enter should close the picker")
	}
	if p.branch != "develop" || v.branch != "develop" {
		t.Errorf("branch = %q in the pane, %q in the repo; want develop", p.branch, v.branch)
	}
	if _, ok := v.panes[99]; ok {
		t.Errorf("other workflows should be rebuilt with the new branch")
	}
}

func TestBranchPickerAcceptsABranchItDoesNotList(t *testing.T) {
	a, v, p := branchPickerRepo(t)
	drive(a, v.key(a, "b"), 5*time.Second)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz-hotfix")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.branch != "zzz-hotfix" {
		t.Errorf("branch = %q, want the typed name", p.branch)
	}
}

func TestEscClosesThePickerWithoutChanging(t *testing.T) {
	a, v, p := branchPickerRepo(t)
	drive(a, v.key(a, "b"), 5*time.Second)
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.modal != nil || p.branch != "" {
		t.Errorf("esc should close and keep all branches: modal %T, branch %q", a.modal, p.branch)
	}
}

func TestStatusPickerSetsTheStatusFilter(t *testing.T) {
	a, v, p := branchPickerRepo(t)
	drive(a, byHelp(t, a, p.Keys(a), "pick a status"), 5*time.Second)
	_ = v
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("failed")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.status != "failure" {
		t.Errorf("status = %q, want failure", p.status)
	}
}

func TestTableDropsLowPriorityColumnsWhenTooNarrow(t *testing.T) {
	tb := newTable(Column{Title: "NAME"}, Column{Title: "ACTOR", Drop: 1}, Column{Title: "AGE"})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "repository-name"}, {Text: "someone-with-a-long-login"}, {Text: "3d"}}}})
	out := ansi.Strip(tb.Render(30, 3))
	if strings.Contains(out, "ACTOR") || !strings.Contains(out, "AGE") {
		t.Errorf("ACTOR should make way for AGE:\n%s", out)
	}
	if out := ansi.Strip(tb.Render(80, 3)); !strings.Contains(out, "ACTOR") {
		t.Errorf("with room, ACTOR should show:\n%s", out)
	}
}

func TestTableShrinksLongColumnsEvenly(t *testing.T) {
	tb := newTable(Column{Title: "TITLE", Flex: true}, Column{Title: "BRANCH", Flex: true})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: strings.Repeat("t", 60)}, {Text: strings.Repeat("b", 40)}}}})
	tb.Render(64, 3)
	ws := tb.widths(64, tb.lineCols())
	if d := ws[0] - ws[1]; d < -1 || d > 1 {
		t.Errorf("widths = %v, want them about equal", ws)
	}
}

func TestEmptyColumnIsHidden(t *testing.T) {
	tb := newTable(Column{Title: "NAME"}, Column{Title: "JOBS", HideEmpty: true})
	tb.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "x"}, {}}}})
	if out := tb.Render(40, 3); strings.Contains(out, "JOBS") {
		t.Errorf("JOBS has no values and should hide:\n%s", out)
	}
}

func TestJobTreeLeavesOutTheSharedPrefix(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	v := newRunView(a, "o/r", gh.Run{ID: 1})
	v.jobs = []gh.Job{{Name: "build / Test"}, {Name: "build / Deploy / eu"}}
	if got := v.jobPrefix(); got != "build / " {
		t.Errorf("prefix = %q, want %q", got, "build / ")
	}
	v.jobs = append(v.jobs, gh.Job{Name: "lint"})
	if got := v.jobPrefix(); got != "" {
		t.Errorf("no shared prefix, got %q", got)
	}
}

func TestSlashFiltersTheListWithoutOpeningGoTo(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now.Add(-time.Minute)), activityRun(2, "completed", now.Add(-time.Hour)))
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	a.roots[tabActions] = v
	a.stack = []View{v}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("brnch:x")})
	out := ansi.Strip(a.View())
	if !strings.Contains(out, "Filter ›") || !strings.Contains(out, "No column called “brnch”") {
		t.Errorf("the filter box should show and explain the unknown column:\n%s", out)
	}
	if strings.Contains(out, "GO TO") {
		t.Errorf("/ on a list should not open the go-to palette")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if v.t.Filter() != "" {
		t.Errorf("x should clear the filter, still %q", v.t.Filter())
	}
}

func TestActivityTabSwitchesViewAndArrowsDoNot(t *testing.T) {
	a, v := activityApp()
	start := v.filter
	v.Update(a, tea.KeyMsg{Type: tea.KeyRight})
	if v.filter != start {
		t.Errorf("→ should not change the view")
	}
	v.Update(a, tea.KeyMsg{Type: tea.KeyTab})
	if v.filter != actionsViews[(slices.Index(actionsViews, start)+1)%len(actionsViews)] {
		t.Errorf("tab should move to the next view")
	}
}

func TestRunHintsOnlyOfferWhatApplies(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	keys := func(run gh.Run) string {
		var ks []string
		for _, h := range a.hints(a.runKeys("o/r", run)) {
			ks = append(ks, h.Key)
		}
		return strings.Join(ks, " ")
	}
	running := gh.Run{Status: "in_progress"}
	if got := keys(running); !strings.Contains(got, "X") || strings.Contains(got, "R") {
		t.Errorf("a running run offers %q", got)
	}
	failed := gh.Run{Status: "completed", Conclusion: "failure"}
	if got := keys(failed); !strings.Contains(got, "R") || strings.Contains(got, "X") {
		t.Errorf("a failed run offers %q", got)
	}
	waiting := gh.Run{Status: "waiting"}
	if got := keys(waiting); !strings.HasPrefix(got, "A") {
		t.Errorf("a waiting run should offer approve first, got %q", got)
	}
}

func TestTheWorkflowListIsWideEnoughForTheFullNames(t *testing.T) {
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	v := newRepoView(a, "o/r", "")
	v.setWorkflows(a, []gh.Workflow{
		{ID: 1, Name: "Build and release webshop-api", State: "active", Path: ".github/workflows/build.yml"},
		{ID: 2, Name: "Publish a preview build of the docs site", State: "active", Path: ".github/workflows/pre.yml"},
	})
	out := ansi.Strip(v.Render(a, 200, 30))
	for _, name := range []string{"Build and release webshop-api", "Publish a preview build of the docs site"} {
		if !strings.Contains(out, name) {
			t.Errorf("the workflow list cuts %q:\n%s", name, out)
		}
	}
}

func TestActionsTabFiltersOnFailedAndPassedRuns(t *testing.T) {
	now := time.Now()
	failed := activityRun(2, "completed", now.Add(-time.Hour))
	failed.Conclusion = "failure"
	a, v := activityApp(activityRun(1, "completed", now.Add(-2*time.Hour)), failed)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	for query, want := range map[string]string{"status:failed": "2", "status:passed": "1", "failed": "2"} {
		v.t.SetFilter(query, a.me)
		if v.t.Len() != 1 || v.t.Selected().Key != want {
			t.Errorf("%q shows %d rows with %q selected, want only run %s", query, v.t.Len(), v.t.Selected().Key, want)
		}
	}
	if help := ansi.Strip(queryHelp("", v.t.Info().fields, a.me)); !strings.Contains(help, "status:failed") {
		t.Errorf("the filter help gives no example for failed runs: %q", help)
	}
}

// countingView counts how often its list is rebuilt.
type countingView struct {
	id    int
	syncs int
	t     *Table
}

func (v *countingView) ID() int                        { return v.id }
func (v *countingView) Init(*App) tea.Cmd              { return nil }
func (v *countingView) Crumb() string                  { return "counting" }
func (v *countingView) Keys(*App) []Binding            { return []Binding{v.t.moveKeys()} }
func (v *countingView) Refresh(*App, bool) tea.Cmd     { return nil }
func (v *countingView) Render(a *App, w, h int) string { return v.t.Render(w, h) }
func (v *countingView) sync(*App)                      { v.syncs++ }
func (v *countingView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

func TestMovingTheCursorDoesNotRebuildTheList(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	v := &countingView{id: a.newID(), t: newTable(Column{Title: "NAME"})}
	v.t.SetRows([]Row{{Key: "a", Cells: []Cell{{Text: "a"}}}, {Key: "b", Cells: []Cell{{Text: "b"}}}, {Key: "c", Cells: []Cell{{Text: "c"}}}})
	a.stack = append(a.stack, v)
	_ = a.View()
	before := v.syncs
	for range 5 {
		a.Update(tea.KeyMsg{Type: tea.KeyDown})
		_ = a.View()
	}
	if v.syncs != before {
		t.Errorf("five arrow keys rebuilt the list %d times, want none", v.syncs-before)
	}
	if v.t.Cursor() != 2 {
		t.Errorf("the cursor should still move, it is on %d", v.t.Cursor())
	}
	a.Update(runes("x"))
	_ = a.View()
	if v.syncs == before {
		t.Errorf("any other key should bring the list up to date")
	}
}

func TestAQueuedRunWhoseJobsStartedIsListedAsRunning(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs/7/jobs": `{"jobs":[{"id":1,"run_id":7,"run_attempt":1,"status":"completed"},{"id":2,"run_id":7,"run_attempt":1,"status":"queued"}]}`,
	})
	a := apiApp(t)
	queued := gh.Run{ID: 7, RunAttempt: 1, Name: "deploy", DisplayTitle: "Ship it", Status: "queued", CreatedAt: time.Now()}
	_, cmd := a.Update(stRunsMsg{repo: "o/r", runs: []gh.Run{queued}})
	drive(a, cmd, 5*time.Second)
	v := newActivityView(a)
	v.sync(a)
	if v.t.Len() != 1 {
		t.Fatalf("the run should be in Running once its jobs say it started, got %d rows", v.t.Len())
	}
	if out := ansi.Strip(v.t.Render(200, 10)); !strings.Contains(out, "next job waits for a runner") {
		t.Errorf("the row should say what the run waits for:\n%s", out)
	}
}

func TestAQueuedRunThatNeverStartedStaysInWaiting(t *testing.T) {
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs/7/jobs": `{"jobs":[{"id":1,"run_id":7,"run_attempt":1,"status":"queued"}]}`,
	})
	a := apiApp(t)
	queued := gh.Run{ID: 7, RunAttempt: 1, Name: "deploy", Status: "queued", CreatedAt: time.Now()}
	_, cmd := a.Update(stRunsMsg{repo: "o/r", runs: []gh.Run{queued}})
	drive(a, cmd, 5*time.Second)
	v := newActivityView(a)
	v.setFilter(viewWaiting)
	v.sync(a)
	if v.t.Len() != 1 {
		t.Errorf("a run waiting for its first runner belongs in Waiting, got %d rows", v.t.Len())
	}
}

func TestROpensTheRepoOfARunFromWatchingAndFromItsLog(t *testing.T) {
	run := gh.Run{ID: 7, WorkflowID: 3, Name: "deploy", Status: "in_progress", CreatedAt: time.Now()}
	for name, open := range map[string]func(a *App){
		"watching": func(a *App) {
			a.watches[run.ID] = &watch{repo: "o/r", run: run, since: time.Now()}
			a.tab, a.stack = tabWatching, []View{a.roots[tabWatching]}
			a.roots[tabWatching].(*watchView).sync(a)
		},
		"log": func(a *App) { a.stack = append(a.stack, newLogView(a, "o/r", run, nil, 0)) },
	} {
		a := apiApp(t)
		open(a)
		pressKeys(a, runes("r"))
		if v, ok := a.top().(*repoView); !ok || v.repo != "o/r" || v.wantWF != run.WorkflowID {
			t.Errorf("r on the %s screen should open o/r at the run's workflow, the screen is %T", name, a.top())
		}
	}
}
