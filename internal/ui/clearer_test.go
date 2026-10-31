package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestTheCommitsOfARepoAndTheirPanelFitTheScreen(t *testing.T) {
	a, rv := openRepoView(t, 160)
	c := rv.commitsList(a)
	c.commits = []gh.Commit{{SHA: "7febc0f1a2b3", Message: "chore: update the versions on staging", Author: "sam", Date: time.Now()}}
	c.at = time.Now()
	c.sync(a)
	rv.section = sectionCommits
	out := ansi.Strip(rv.Render(a, 160, 30))
	for _, l := range strings.Split(out, "\n") {
		if width(l) > 160 {
			t.Errorf("a line is wider than the screen: %q", l)
		}
		if strings.HasSuffix(strings.TrimRight(l, " "), "…│") {
			t.Errorf("the panel is cut off by the frame: %q", l)
		}
	}
	wantAll(t, out, "Commits", "chore: update the versions on staging")
}

func TestHealthCountsCancelledRunsAloudAndSaysWhatADurationCovers(t *testing.T) {
	a, rv := openRepoView(t, 160)
	now := time.Now()
	run := func(id int64, conclusion string) gh.Run {
		return gh.Run{ID: id, Name: "CI", Status: "completed", Conclusion: conclusion, CreatedAt: now, RunStartedAt: now.Add(-time.Minute), UpdatedAt: now}
	}
	runs := []gh.Run{run(1, "success"), run(2, "cancelled"), run(3, "cancelled"), run(4, "success"), run(5, "failure")}
	rv.sample = runs
	if p, ok := rv.panes[0]; ok {
		p.runs = runs
	}
	out := ansi.Strip(rv.renderHealth(a, 40, 20))
	wantAll(t, out, "2 of 3 finished passed", "2 cancelled, not counted", "waits included")
}

func TestEmptySectionsOfARepoAreQuiet(t *testing.T) {
	a, rv := openRepoView(t, 160)
	rv.pullsList(a).at = time.Now()
	is := rv.issuesList(a)
	is.at = time.Now()
	strip := rv.renderHead(a, 160)
	wantAll(t, ansi.Strip(strip), "Pull requests 0", "Issues 0")
	if !strings.Contains(strip, sDim.Render("Issues")) {
		t.Errorf("a section with nothing in it should be dim:\n%q", strip)
	}
}

func TestARepoWithIssuesOffHasNoIssuesSection(t *testing.T) {
	a, rv := openRepoView(t, 160)
	a.repos = []gh.Repo{{FullName: "o/r", HasActions: true, DefaultBranch: "main", IssuesOff: true}}
	if strip := ansi.Strip(rv.renderHead(a, 160)); strings.Contains(strip, "Issues") {
		t.Errorf("the strip should leave Issues out:\n%s", strip)
	}
	press(a, "tab", "tab")
	if rv.section != sectionCommits {
		t.Errorf("tab from Pull requests should go to Commits, got section %d", rv.section)
	}
	press(a, "[")
	if rv.section != sectionPulls {
		t.Errorf("[ from Commits should go back to Pull requests, got section %d", rv.section)
	}
}

func TestIssuesFoundOffStayUntilYouLeaveThem(t *testing.T) {
	a, rv := openRepoView(t, 160)
	press(a, "tab", "tab")
	is := rv.issuesList(a)
	is.at, is.enabled = time.Now(), false
	if strip := ansi.Strip(rv.renderHead(a, 160)); !strings.Contains(strip, "Issues") {
		t.Errorf("the section you are on stays in the strip:\n%s", strip)
	}
	press(a, "tab")
	if strip := ansi.Strip(rv.renderHead(a, 160)); strings.Contains(strip, "Issues") {
		t.Errorf("once you leave it, Issues should be gone:\n%s", strip)
	}
}

// withBudget has the client hear from GitHub that remaining of 5000 GraphQL
// points are left this hour, and plenty of REST requests.
func withBudget(t *testing.T, a *App, remaining string) {
	reset := fmt.Sprint(time.Now().Add(time.Hour).Unix())
	for resource, left := range map[string]string{"core": "4900", "graphql": remaining} {
		useTransport(t, &refusingGitHub{status: 200, body: "{}", headers: map[string]string{
			"X-RateLimit-Resource": resource, "X-RateLimit-Limit": "5000", "X-RateLimit-Remaining": left, "X-RateLimit-Reset": reset}})
		_ = a.client.Get(context.Background(), "/user", &struct{}{})
	}
}

func TestTheHeaderLeavesTheBudgetOutWhilePlentyIsLeft(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	withBudget(t, a, "4800")
	if out := ansi.Strip(a.renderTopBar()); strings.Contains(out, "left") || strings.Contains(out, "API") {
		t.Errorf("the budget should not show while plenty is left:\n%s", out)
	}
}

func TestTheHeaderNamesTheBudgetOnceItRunsLow(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	withBudget(t, a, "412")
	wantAll(t, ansi.Strip(a.renderTopBar()), "GraphQL 412 left")
}

const dependabotBody = "Bumps the actions group.\n\n<details>\n<summary>Release notes</summary>\n\n<p>v1.80.0 fixes the linter.</p>\n<details><summary>Inner</summary>nested</details>\n</details>\n\nThe end."

func TestDetailsAreFoldedToTheirSummaryAndUnfoldOnRequest(t *testing.T) {
	folded := ansi.Strip(strings.Join(renderMarkdownFolded(dependabotBody, 80, true), "\n"))
	wantAll(t, folded, "Bumps the actions group.", "▸ Release notes …", "The end.")
	if strings.Contains(folded, "fixes the linter") || strings.Contains(folded, "nested") {
		t.Errorf("a folded <details> should hide what it holds:\n%s", folded)
	}
	open := ansi.Strip(strings.Join(renderMarkdown(dependabotBody, 80), "\n"))
	wantAll(t, open, "Release notes", "v1.80.0 fixes the linter.", "The end.")
}

func TestEUnfoldsTheDetailsOfThePullRequestDescription(t *testing.T) {
	a, v, _ := reviewedPRApp(t)
	v.pr.Body = dependabotBody
	v.conv.ver++
	if out := screen(a); strings.Contains(out, "fixes the linter") || !strings.Contains(out, "Release notes") {
		t.Fatalf("the description should open folded:\n%s", out)
	}
	for v.conv.list.items[v.conv.list.cursor].id != "body" {
		v.conv.list.cursor++
	}
	pressKeys(a, runes("v"))
	if out := screen(a); !strings.Contains(out, "fixes the linter") {
		t.Errorf("v should unfold the details of the description:\n%s", out)
	}
}

func TestActionsListsOnlyWhatRunsAndPlatformWhatYourReposShare(t *testing.T) {
	a := sharedApp(t)
	_, actions := sharedTab(a, viewRecent)
	strip := lineWith(actions, "Waiting")
	wantAll(t, strip, "Failing", "Running", "Waiting", "Last 7 days")
	for _, gone := range []string{"Queued", "Needs approval", "Shared", "Warnings", "Scheduled", "Runners", "Time"} {
		if strings.Contains(strip, gone) {
			t.Errorf("the Actions strip should not hold %q: %q", gone, strip)
		}
	}
	_, platform := sharedTab(a, viewSharedWorkflows)
	wantAll(t, lineWith(platform, "Shared actions"), "Shared workflows", "Warnings", "Scheduled", "Runners", "Time")
	if strings.Contains(lineWith(platform, "Shared actions"), "Failing") {
		t.Errorf("the Platform strip should not hold the runs")
	}
}

func TestFiveOpensThePlatformPlaceAndIssuesFollow(t *testing.T) {
	a := sharedApp(t)
	press(a, "5")
	if a.tab != tabPlatform {
		t.Fatalf("5 went to place %d", a.tab)
	}
	if got := strings.Join(tabNames[4:], " "); got != "Platform Issues Achievements" {
		t.Errorf("the last places are %q", got)
	}
	wantAll(t, ansi.Strip(a.View()), "Platform", "Shared workflows")
}

func TestWaitingHoldsTheQueuedRunsAndThoseThatWaitForYou(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "queued", now), activityRun(2, "waiting", now), activityRun(3, "completed", now))
	a.st.canApprove = map[int64]bool{2: true}
	items := v.stripItems(a, recentRuns(a, []string{"o/r"}), now)
	for _, it := range items {
		if it.Label == "Waiting" && it.Count != "2" {
			t.Errorf("Waiting should count the queued run and the one waiting for you, got %s", it.Count)
		}
	}
}

func TestAViewNotReadYetCountsAsUnknownAndNotAsNone(t *testing.T) {
	a := sharedApp(t)
	_, out := sharedTab(a, viewSharedWorkflows)
	strip := lineWith(out, "Shared actions")
	wantAll(t, strip, "Warnings …", "Scheduled …", "Runners …", "Time …")
	if strings.Contains(strip, "Warnings 0") {
		t.Errorf("a view not read yet should not count 0: %q", strip)
	}
}

func TestTheReposPanelSaysWhichWorkflowBreaksMainAndCountsBrokenRepos(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	a.repos = []gh.Repo{{Name: "webshop", FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: time.Now(), Health: []string{"SUCCESS", "FAILURE"}}}
	deploy, ci := mainRun(1, 10, "failure", time.Now().Add(-3*time.Hour)), mainRun(2, 20, "success", time.Now())
	deploy.Name, ci.Name = "Deploy", "CI"
	a.st.status["acme/webshop"] = []gh.Run{ci, deploy}
	a.setTab(tabRepos)
	out := screen(a)
	wantAll(t, out, "1 failing on main", "Default branch", "Deploy", "failed 3h", "CI", "Last 2 commits")
	if strings.Contains(out, "1 commits ago") {
		t.Errorf("one commit is not plural:\n%s", out)
	}
}

func TestIExplainsTheColumnsAndChipsOfTheScreenAndClosesAgain(t *testing.T) {
	a := sharedApp(t)
	a.setTab(tabRepos)
	if !strings.Contains(screen(a), "explain") {
		t.Errorf("the key bar should offer i:\n%s", screen(a))
	}
	press(a, "i")
	out := screen(a)
	wantAll(t, out, "What Repos shows", "N failing on", "DEFAULT BRANCH", "how the default branch is doing now", "PUSHED", "i or esc closes")
	press(a, "esc")
	if strings.Contains(screen(a), "What Repos shows") {
		t.Errorf("esc should close the explanation")
	}
}

func TestEveryPlaceExplainsItself(t *testing.T) {
	a := sharedApp(t)
	for i, v := range a.roots {
		e, ok := v.(explainer)
		if !ok {
			t.Errorf("%s does not explain itself", tabNames[i])
			continue
		}
		if about := e.Explain(a); about.title == "" || len(about.items) == 0 {
			t.Errorf("%s explains nothing", tabNames[i])
		}
	}
}

func TestTheFailingViewExplainsAFailureWithoutAnErrorLine(t *testing.T) {
	a := failingApp(t)
	v, _ := groupedFailing(a)
	about := v.Explain(a)
	var terms []string
	for _, it := range about.items {
		terms = append(terms, it.term)
	}
	wantAll(t, about.title+" "+strings.Join(terms, " | "), "Actions › Failing", "? no error line", "ERROR", "SINCE", "Waiting")
}

func TestTheKeyBarOfAListLeadsWithWhatYouDoWithTheRowAndNotWithTicking(t *testing.T) {
	now := time.Now()
	a, v := activityApp(activityRun(1, "completed", now))
	v.setFilter(viewRecent)
	v.rebuild(a, recentRuns(a, []string{"o/r"}), now)
	bar := barOf(a, v.Keys(a))
	if strings.Index(bar, " enter ") < 0 || strings.Index(bar, " space ") < strings.Index(bar, " enter ") {
		t.Errorf("enter should come before ticking: %q", bar)
	}
}

func TestSecurityAlertsAreInTheRepoMenuWithoutTakingTheBar(t *testing.T) {
	a, rv := openRepoView(t, 160)
	rv.section = sectionActions
	if bar := barOf(a, rv.Keys(a)); strings.Contains(bar, " S ") {
		t.Errorf("security alerts have no key of their own any more: %q", bar)
	}
	found := false
	for _, b := range rv.Keys(a) {
		found = found || strings.HasPrefix(b.Help, "Dependabot alerts")
	}
	if !found {
		t.Errorf("the menu of a repo should offer its security alerts")
	}
}

func TestThePanelTakesTheRoomAListLeavesEmpty(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	panelWidth := func(listUses int) int {
		got := 0
		listWithPanel(a, 190, 6, nil,
			func(w, h int) string { return fit(strings.Repeat("x", min(listUses, w)), w) },
			func(w, h int) []string { got = w; return make([]string, h) })
		return got
	}
	if w := panelWidth(40); w != maxPanelWidth {
		t.Errorf("a narrow list should leave the panel %d cells, got %d", maxPanelWidth, w)
	}
	if w := panelWidth(190); w != 46 {
		t.Errorf("a list that fills its room keeps the panel at 46 cells, got %d", w)
	}
}

func TestReposWithDifferentDefaultBranchesShareShortColumnTitles(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	a.repos = []gh.Repo{
		{FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: time.Now(), Health: []string{"SUCCESS"}},
		{FullName: "acme/payments", HasActions: true, DefaultBranch: "develop", PushedAt: time.Now(), Health: []string{"SUCCESS"}},
	}
	a.setTab(tabRepos)
	wantAll(t, screen(a), "DEFAULT BRANCH", "RECENT COMMITS")
}

func TestTheHeaderNamesTheViewOnDisplay(t *testing.T) {
	a := sharedApp(t)
	a.setTab(tabActions)
	v := a.roots[tabActions].(*activityView)
	v.setFilter(viewRunning)
	if head := ansi.Strip(a.renderTopBar()); !strings.Contains(head, "› Running") {
		t.Errorf("the header should name the view: %s", head)
	}
	v.setFilter(viewWaiting)
	if head := ansi.Strip(a.renderTopBar()); !strings.Contains(head, "› Waiting") {
		t.Errorf("the header should follow tab: %s", head)
	}
}

func TestTheHeaderNamesTheSectionOfARepo(t *testing.T) {
	a, rv := openRepoView(t, 160)
	rv.section = sectionCommits
	if head := ansi.Strip(a.renderTopBar()); !strings.Contains(head, "› o/r › Commits") {
		t.Errorf("the header should name the section: %s", head)
	}
}

func TestEscSaysWhereItGoesAndTheTabOfAPullRequestIsInTheHeader(t *testing.T) {
	a, _, _ := reviewedPRApp(t)
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab})
	out := screen(a)
	wantAll(t, out, "PR #12 › Changes", "back to "+tabNames[a.tab])
}

func TestAScreenOfNoPlaceLightsNoPlaceAndEscNamesWhereYouCameFrom(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	a := sharedApp(t)
	a.setTab(tabIssues)
	lit := sWhite.Background(cBg).Bold(true).Render("Issues  ")
	if !strings.Contains(a.renderTopBar(), lit) {
		t.Fatalf("Issues should be lit on its own place")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	head := a.renderTopBar()
	if strings.Contains(head, lit) {
		t.Errorf("GitHub status belongs to no place, so Issues should not be lit")
	}
	wantAll(t, ansi.Strip(head), "Issues", "› GitHub status")
	wantAll(t, screen(a), "back to Issues › Assigned")
}

func TestALongTrailKeepsItsEnds(t *testing.T) {
	got := strings.Join(shortTrail([]string{"Failing", "acme/webshop", "CI #128", "PR #57", "Changes"}), " › ")
	if got != "Failing › … › PR #57 › Changes" {
		t.Errorf("trail = %q", got)
	}
}

func TestEscSaysItClearsTheFilterFirst(t *testing.T) {
	a := sharedApp(t)
	a.setTab(tabRepos)
	a.roots[tabRepos].(*reposView).SetFilter(a, "api")
	wantAll(t, screen(a), "clear filter")
}

// runApp is the app with the run CI #128 of acme/webshop opened from Actions.
func runApp(t *testing.T) (*App, *runView) {
	a := sharedApp(t)
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	a.setTab(tabActions)
	run := gh.Run{ID: 128, Name: "CI", Path: ".github/workflows/ci.yml", RunNumber: 128, HeadBranch: "main", Status: "completed", Conclusion: "failure", DisplayTitle: "feat: show delivery estimates"}
	v := newRunView(a, "acme/webshop", run)
	v.loaded, v.jobsLoaded = true, true
	a.push(v)
	return a, v
}

func TestARunNamesItsRepoWorkflowAndFileOnTop(t *testing.T) {
	a, _ := runApp(t)
	out := screen(a)
	wantAll(t, out, "acme/webshop › ", "CI ci.yml › #128 · ", "main")
	if head := ansi.Strip(a.renderTopBar()); !strings.Contains(head, "webshop › CI #128") {
		t.Errorf("the trail should name the repo of the run: %s", head)
	}
}

func TestTheRepoIsNotNamedTwiceInTheTrail(t *testing.T) {
	a, _ := openRepoView(t, 160)
	segs := a.trail()
	run := gh.Run{ID: 9, Name: "CI", RunNumber: 9}
	a.push(newRunView(a, "o/r", run))
	after := a.trail()
	if len(after) != len(segs)+1 {
		t.Errorf("a run of the repo on display adds only itself to the trail: %q then %q", segs, after)
	}
}

func TestALogKeepsTheContextOfItsRun(t *testing.T) {
	a, v := runApp(t)
	job := gh.Job{ID: 1, Name: "Tests"}
	a.push(newLogView(a, v.repo, v.run, &job, 0))
	wantAll(t, screen(a), "acme/webshop › ", "#128", "› Tests")
}

func TestTheTerminalTitleFollowsWhereYouAre(t *testing.T) {
	a, _ := runApp(t)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if a.windowTitle != "webshop › CI #128 · Calamares" {
		t.Errorf("title = %q", a.windowTitle)
	}
	press(a, "esc")
	if !strings.HasPrefix(a.windowTitle, "Actions") {
		t.Errorf("back on Actions the title should say so: %q", a.windowTitle)
	}
}
