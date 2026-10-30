package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func callingRun(id int64, conclusion string, created time.Time, paths ...string) gh.Run {
	r := gh.Run{ID: id, RunNumber: int(id), Name: "CI", Path: ".github/workflows/ci.yml", Status: "completed", Conclusion: conclusion, CreatedAt: created, UpdatedAt: created.Add(time.Minute)}
	for _, p := range paths {
		r.ReferencedWorkflows = append(r.ReferencedWorkflows, gh.ReferencedWorkflow{Path: p})
	}
	return r
}

const (
	buildV3 = "acme/workflows/.github/workflows/build.yml@v3"
	buildV2 = "acme/workflows/.github/workflows/build.yml@v2"
	lintSHA = "acme/security/.github/workflows/lint.yml@0123456789abcdef0123456789abcdef01234567"
)

// sharedApp holds runs of three repos: api and web call build.yml at v3,
// billing still at v2; api also calls lint.yml pinned to a commit. The
// workflow files are not read.
func sharedApp(t *testing.T) *App {
	now := time.Now()
	a := accessApp(t, "WRITE", "gho_x")
	a.repos = append(a.repos,
		gh.Repo{FullName: "acme/api", HasActions: true, PushedAt: now},
		gh.Repo{FullName: "acme/web", HasActions: true, PushedAt: now},
		gh.Repo{FullName: "acme/billing", HasActions: true, PushedAt: now})
	a.st.status = map[string][]gh.Run{
		"acme/api":     {callingRun(1, "success", now.Add(-10*time.Minute), buildV3, lintSHA)},
		"acme/web":     {callingRun(2, "success", now.Add(-time.Hour), buildV3)},
		"acme/billing": {callingRun(3, "failure", now.Add(-3*time.Hour), buildV2), callingRun(4, "success", now.Add(-5*time.Hour), buildV2)},
	}
	return a
}

func use(repo, file, target, version string) gh.Use {
	return gh.Use{Repo: repo, File: file, Target: target, Version: version}
}

// withFiles has the workflow files read: build.yml calls helper.yml inside,
// and the repos use actions of acme and of others.
func withFiles(a *App) {
	a.st.uses = []gh.Use{
		use("acme/api", ".github/workflows/ci.yml", "acme/workflows/.github/workflows/build.yml", "v3"),
		use("acme/web", ".github/workflows/ci.yml", "acme/workflows/.github/workflows/build.yml", "v3"),
		use("acme/billing", ".github/workflows/ci.yml", "acme/workflows/.github/workflows/build.yml", "v2"),
		use("acme/docs", ".github/workflows/ci.yml", "acme/workflows/.github/workflows/build.yml", "v3"),
		use("acme/workflows", ".github/workflows/build.yml", "acme/workflows/.github/workflows/helper.yml", "0123456789abcdef0123456789abcdef01234567"),
		use("acme/workflows", ".github/workflows/build.yml", "acme/lint-action", "v1"),
		use("acme/api", ".github/workflows/ci.yml", "acme/lint-action", "v1"),
		use("acme/web", ".github/workflows/ci.yml", "acme/lint-action", "v0.9"),
		use("acme/api", ".github/workflows/ci.yml", "other-org/checkout", "v4"),
	}
	a.st.usesRepos, a.st.usesState.at = 5, time.Now().Add(-2*time.Hour)
	a.st.usesGen++
}

func sharedTab(a *App, filter int) (*activityView, string) {
	v := newActivityView(a)
	if slices.Contains(platformViews, filter) {
		v = newPlatformView(a)
	}
	v.setFilter(filter)
	v.sync(a)
	return v, ansi.Strip(v.Render(a, 170, 40))
}

// groupedFailing shows the Failing view grouped by cause, as g does.
func groupedFailing(a *App) (*activityView, string) {
	v := newActivityView(a)
	v.failing.flat = false
	v.setFilter(viewFailing)
	v.sync(a)
	return v, ansi.Strip(v.Render(a, 170, 40))
}

func wantAll(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func lineWith(out, s string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

func TestSharedWorkflowsFromRunsAreGroupedByVersion(t *testing.T) {
	a := sharedApp(t)
	held := heldShared(a)
	if len(held.workflows) != 2 || held.workflows[0].name() != "build.yml" || held.workflows[1].name() != "lint.yml" {
		t.Fatalf("workflows: got %d, want build.yml then lint.yml", len(held.workflows))
	}
	build := held.workflows[0]
	if build.repoCount() != 3 || len(build.calls) != 4 || len(build.versions) != 2 {
		t.Fatalf("build.yml: %d repos, %d calls, %d versions; want 3, 4, 2", build.repoCount(), len(build.calls), len(build.versions))
	}
	v3, v2 := build.versions[0], build.versions[1]
	if v3.version != "v3" || len(v3.users) != 2 || v3.behind || v2.version != "v2" || len(v2.users) != 1 || !v2.behind || len(v2.calls) != 2 {
		t.Errorf("versions: %+v %+v", *v3, *v2)
	}
}

func TestARepoOnAnOlderTagIsBehindButAFloatingTagOrACommitIsNot(t *testing.T) {
	versions := []string{"v3.2.1", "v3", "v3.1.0", "v2", "main", "0123456789abcdef0123456789abcdef01234567"}
	var runs []repoRun
	for i, v := range versions {
		runs = append(runs, repoRun{fmt.Sprint("acme/r", i), callingRun(int64(i), "success", time.Now(), "acme/workflows/.github/workflows/build.yml@"+v)})
	}
	workflows, _ := collectShared(runs, nil, false)
	behind := map[string]string{"v3.1.0": "v3.2.1", "v2": "v3"}
	var order []string
	for _, ver := range workflows[0].versions {
		order = append(order, ver.version)
		if want := behind[ver.version]; ver.ahead != want || ver.behind != (want != "") {
			t.Errorf("%s: behind %v of %q, want behind of %q", ver.version, ver.behind, ver.ahead, want)
		}
	}
	if got := strings.Join(order[:4], " "); got != "v3 v3.2.1 v3.1.0 v2" {
		t.Errorf("tags in order %q, want the floating v3 first", got)
	}
}

func TestOwnersSpelledInAnotherCaseAreTheSameAction(t *testing.T) {
	_, actions := collectShared(nil, []gh.Use{
		use("acme/api", ".github/workflows/ci.yml", "Acme/Lint-Action", "v1"),
		use("acme/web", ".github/workflows/ci.yml", "acme/lint-action", "v1"),
	}, true)
	if len(actions) != 1 || actions[0].repoCount() != 2 {
		t.Errorf("got %d actions, want one used by both repos", len(actions))
	}
}

func TestWorkflowFilesSayWhoUsesWhatAndNestHelpers(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	held := heldShared(a)
	if len(held.workflows) != 1 || held.workflows[0].name() != "build.yml" {
		t.Fatalf("only build.yml should be listed: lint.yml is no longer in a file, helper.yml is nested; got %d", len(held.workflows))
	}
	build := held.workflows[0]
	if build.repoCount() != 4 || len(build.versions[0].users) != 3 {
		t.Errorf("build.yml should count docs, which no run of ours called: %d repos", build.repoCount())
	}
	if len(build.nested) != 1 || build.nested[0] != "acme/workflows/.github/workflows/helper.yml" {
		t.Errorf("build.yml should call helper.yml: %v", build.nested)
	}
	var lint *sharedItem
	for _, it := range held.actions {
		if it.name() == "lint-action" {
			lint = it
		}
	}
	if lint == nil || lint.repoCount() != 2 || len(lint.inside) != 1 || !lint.version("v0.9").behind {
		t.Fatalf("lint-action: %+v", lint)
	}
}

func TestSharedWorkflowsShowAVersionLinePerVersionWithAPanel(t *testing.T) {
	a := sharedApp(t)
	_, out := sharedTab(a, sharedFilter)
	wantAll(t, out, "Shared workflows 2", "Shared actions …", "1 outdated repository", "seen in their last 10 runs",
		"WORKFLOW", "VERSION", "PASSED", "STATUS", "v3 latest", "0123456 commit",
		"Status", "Used by", "2 repositories", "Repositories on v3", "Run history", "Next steps")
	if l := lineWith(out, "1 of 2"); !strings.Contains(l, "v2") || !strings.Contains(l, "Outdated") {
		t.Errorf("v2 should be on a line of its own, outdated: %q", l)
	}
	if l := lineWith(out, "v3 latest"); !strings.Contains(l, "build.yml") {
		t.Errorf("the name should be on the line of the first version: %q", l)
	}
	for _, old := range []string{"├─", "└─", "▾", "▸", "pts", "behind"} {
		if strings.Contains(out, old) {
			t.Errorf("the list should not show %q any more:\n%s", old, out)
		}
	}
	if strings.Contains(out, "0123456789abcdef") {
		t.Errorf("a commit should show by its first seven characters:\n%s", out)
	}
}

func TestAVersionLineShowsTheReposOnIt(t *testing.T) {
	a := sharedApp(t)
	v, _ := sharedTab(a, sharedFilter)
	v.Update(a, runes("j"))
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "build.yml v2", "Outdated", "Newer", "v3, in 2 repositories", "Repositories on v2", "billing", "Run history", "1 of 2 runs", "last failed billing #3")
}

func TestSharedActionsListYourActionsAndEShowsEveryones(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	v, out := sharedTab(a, sharedActionsFilter)
	wantAll(t, out, "Shared actions 1", "Your actions used by 5 repositories · files read 2 hours ago", "lint-action", "v1 latest", "Up to date",
		"Used in shared workflows", "build.yml")
	if l := lineWith(out, "v0.9"); !strings.Contains(l, "Outdated") {
		t.Errorf("v0.9 should be outdated: %q", l)
	}
	if strings.Contains(out, "checkout") {
		t.Errorf("another owner's action should wait for e:\n%s", out)
	}
	v.Update(a, runes("e"))
	v.sync(a)
	if out := ansi.Strip(v.Render(a, 170, 40)); !strings.Contains(out, "checkout") {
		t.Errorf("e should show everyone's actions:\n%s", out)
	}
}

func TestTheNextViewKeyGoesThroughThePlatformViewsAndWrapsAround(t *testing.T) {
	a := sharedApp(t)
	v := newPlatformView(a)
	v.setFilter(viewRunners)
	for _, want := range []int{viewCaches, viewTime, viewOverview, sharedFilter, sharedActionsFilter, viewWarnings} {
		v.Update(a, runes("]"))
		if v.filter != want {
			t.Fatalf("] should reach view %d, got %d", want, v.filter)
		}
	}
}

func TestTheReposPanelNamesTheSharedWorkflowsAndWOpensThem(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	a.top().(Filterer).SetFilter(a, "billing")
	out := screen(a)
	wantAll(t, out, "Shared workflows", "build.yml v2 behind v3")
	pressKeys(a, runes("w"))
	v, ok := a.top().(*sharedView)
	if !ok {
		t.Fatalf("w should open the shared workflows of the repo, top is %T", a.top())
	}
	wantAll(t, screen(a), "used by billing")
	if l := lineWith(screen(a), "1 of 2"); !strings.Contains(l, "v2") || !strings.Contains(l, "Outdated") {
		t.Errorf("billing's version should be outdated: %q", l)
	}
	if v.t.Len() != 1 {
		t.Errorf("the list should hold only the version billing uses: %d rows\n%s", v.t.Len(), screen(a))
	}
}

func TestSharedListPicksUpRunsThatArriveLater(t *testing.T) {
	a := sharedApp(t)
	v, _ := sharedTab(a, sharedFilter)
	a.Update(stRunsMsg{repo: "acme/web", runs: []gh.Run{callingRun(9, "success", time.Now(), "acme/workflows/.github/workflows/deploy.yml@v5")}})
	v.sync(a)
	if out := ansi.Strip(v.Render(a, 170, 40)); !strings.Contains(out, "deploy.yml") {
		t.Errorf("a workflow called by runs read later should be listed:\n%s", out)
	}
}

func TestWorkflowFilesAreReadOnceADay(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	if cmd := a.st.loadUses(a, false); cmd != nil {
		t.Error("files read 2 hours ago should not be read again")
	}
	a.st.usesState.at = time.Now().Add(-25 * time.Hour)
	if cmd := a.st.loadUses(a, false); cmd == nil {
		t.Error("files read over a day ago should be read again")
	}
}

func TestAWorkflowWithOneVersionStaysOnOneLine(t *testing.T) {
	a := sharedApp(t)
	v, out := sharedTab(a, sharedFilter)
	if n := v.shared.t.Len(); n != 3 {
		t.Errorf("build.yml's two versions and lint.yml make 3 rows, got %d", n)
	}
	if l := lineWith(out, "0123456 commit"); !strings.Contains(l, "lint.yml") {
		t.Errorf("lint.yml should name its one version on its own line: %q", l)
	}
}

func TestTheFilterKeepsTheTreeAndKnowsFields(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	v, _ := sharedTab(a, sharedFilter)
	for _, c := range []struct {
		query string
		rows  int
		want  string
	}{
		{"repo:billing", 1, "billing"},
		{"is:behind", 1, "Outdated"},
		{"build v3", 1, "v3 latest"},
		{"source:security", 0, ""},
	} {
		v.SetFilter(a, c.query)
		v.sync(a)
		out := ansi.Strip(v.Render(a, 170, 30))
		if n := v.shared.t.Len(); n != c.rows || !strings.Contains(out, c.want) {
			t.Errorf("%q: %d rows, want %d with %q:\n%s", c.query, n, c.rows, c.want, out)
		}
	}
}

func TestOutdatedOnlyKeepsTheVersionsOthersHaveMovedPast(t *testing.T) {
	a := sharedApp(t)
	v, _ := sharedTab(a, sharedFilter)
	v.Update(a, runes("b"))
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 30))
	if n := v.shared.t.Len(); n != 1 || strings.Contains(out, "lint.yml") || !strings.Contains(out, "Outdated only") {
		t.Errorf("b should leave build.yml v2 and say so: %d rows\n%s", n, out)
	}
}

func TestGroupBySourcePutsEachUnderTheRepoItLivesIn(t *testing.T) {
	a := sharedApp(t)
	v, _ := sharedTab(a, sharedFilter)
	v.Update(a, runes("g"))
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 30))
	if n := v.shared.t.Len(); n != 5 {
		t.Errorf("two headings and three rows make 5, got %d", n)
	}
	wantAll(t, out, "1 workflow  ·  1 with an outdated version in use", "Grouped by source")
	if l := lineWith(out, "1 with an outdated version in use"); !strings.Contains(l, "workflows") {
		t.Errorf("the heading should name the source repo: %q", l)
	}
	v.Update(a, runes("k"))
	v.Update(a, runes("g"))
	if r := v.shared.t.Selected(); r == nil || !strings.HasPrefix(r.Key, groupKeyPrefix) {
		t.Fatalf("the first line should be a heading")
	}
	if out := ansi.Strip(v.Render(a, 170, 30)); !strings.Contains(out, "Source repository") {
		t.Errorf("a heading's panel should sum up its repo:\n%s", out)
	}
}

func TestSortingByNameAndByLastRun(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	v, _ := sharedTab(a, sharedActionsFilter)
	v.Update(a, runes("e"))
	v.sharedActions.order = "name"
	v.sync(a)
	if got := v.sharedActions.items[0].name(); got != "checkout" {
		t.Errorf("by name, checkout comes first, got %s", got)
	}
	v.sharedActions.order = "used"
	v.sync(a)
	if got := v.sharedActions.items[0].name(); got != "lint-action" {
		t.Errorf("by use, lint-action comes first, got %s", got)
	}
}

func TestTheHistoryColumnHasAGapEveryFiveRuns(t *testing.T) {
	_, plain := history([]Kind{KOK, KOK, KFail, KOK, KOK, KOK, KOK}, 0)
	if plain != "▉▉▉▉▉ ▉▉" {
		t.Errorf("got %q, want a gap after five", plain)
	}
}

func TestAdoptionBarCellsLeaveASeamBetweenThem(t *testing.T) {
	if got := barCells(3); got != "▉▉▉" {
		t.Errorf("got %q, want three 7/8 cells", got)
	}
}

func TestEnterOnASharedWorkflowListsTheWorkflowsThatUseIt(t *testing.T) {
	a := sharedApp(t)
	withFiles(a)
	a.stack = append(a.stack, newSharedView(a, "", false))
	_ = a.View()
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := a.top().(*sharedUsersView); !ok {
		t.Fatalf("enter should list the workflows that use build.yml, top is %T", a.top())
	}
	out := screen(a)
	wantAll(t, out, "build.yml", "used by", "api", "web", "billing", "docs", "ci.yml", "LAST RUN", "not run lately")
	if l := lineWith(out, "billing"); !strings.Contains(l, "v2") || !strings.Contains(l, "outdated") {
		t.Errorf("billing's line should show it is outdated on v2: %q", l)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEsc}, runes("r"))
	if _, ok := a.top().(*sharedRunsView); !ok {
		t.Errorf("r should list the runs that called it, top is %T", a.top())
	}
}
