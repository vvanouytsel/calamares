package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func achievementsApp(t *testing.T) *App {
	t.Helper()
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return a
}

func TestEveryAchievementIsCompleteAndHasAWayToBeEarned(t *testing.T) {
	if len(achievements) < 50 {
		t.Errorf("there are %d achievements, want at least 50", len(achievements))
	}
	var source strings.Builder
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			b, _ := os.ReadFile(f)
			source.Write(b)
		}
	}
	seen := map[string]bool{}
	for _, x := range achievements {
		switch {
		case seen[x.id]:
			t.Errorf("%s is listed twice", x.id)
		case x.name == "" || x.text == "" || x.quip == "":
			t.Errorf("%s has no name, text or quip", x.id)
		case x.glyph[0] == "" || x.glyph[1] == "":
			t.Errorf("%s has no glyph for one of the icon sets", x.id)
		case len(x.tiers) == 0 || len(x.tiers) > 4:
			t.Errorf("%s has %d tiers", x.id, len(x.tiers))
		case x.oneOff() && x.medal == tierNone:
			t.Errorf("%s is earned once but is worth no tier", x.id)
		case x.secret() && x.hint == "":
			t.Errorf("secret %s has no hint", x.id)
		}
		for i := 1; i < len(x.tiers); i++ {
			if x.tiers[i] <= x.tiers[i-1] {
				t.Errorf("%s: tier %d needs no more than the one before it", x.id, i+1)
			}
		}
		seen[x.id] = true
		if n := strings.Count(source.String(), `"`+x.id+`"`); n < 2 {
			t.Errorf("%s is never counted anywhere", x.id)
		}
	}
}

func TestACounterUnlocksEachTierItReachesWithACardForTheHighest(t *testing.T) {
	a := achievementsApp(t)
	a.awards = nil
	a.earn("second-wind", 12)
	x := achievementByID("second-wind")
	if got := a.ach.tierOf(x); got != tierSilver {
		t.Fatalf("12 reruns that passed reach %s, want silver", got)
	}
	if n := len(a.ach.Earned[x.id]); n != 2 {
		t.Errorf("bronze and silver should both be recorded, got %d tiers", n)
	}
	if len(a.awards) == 0 || a.awards[0].x != x || a.awards[0].tier != tierSilver {
		t.Fatalf("one card for silver should be queued, got %+v", a.awards)
	}
	if got := a.ach.xp(); got < 35 {
		t.Errorf("bronze and silver are worth 35 XP, the app counts %d", got)
	}
	a.earn("second-wind", 1)
	if n := len(a.awards); n != 1 {
		t.Errorf("a count that reaches no new tier should show no card, %d queued", n)
	}
}

func TestCardsForUnlocksWaitTheirTurn(t *testing.T) {
	a := achievementsApp(t)
	a.awards = nil
	a.earnOnce("debug-mode")
	a.earnOnce("night-owl")
	if len(a.awards) != 2 || a.awards[1].at != (time.Time{}) {
		t.Fatalf("the second card should wait while the first shows: %+v", a.awards)
	}
	a.awards[0].at = time.Now().Add(-awardLife - time.Second)
	a.expireAwards()
	if len(a.awards) != 1 || a.awards[0].x.id != "night-owl" || a.awards[0].at.IsZero() {
		t.Errorf("when the first card's time is up the next should show: %+v", a.awards)
	}
	out := ansi.Strip(a.View())
	wantAll(t, out, "Achievement unlocked", "Night owl", "ctrl+x")
	pressKeys(a, tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(a.awards) != 0 {
		t.Errorf("ctrl+x should dismiss the card")
	}
}

func TestARerunWatchedToTheEndCountsAsASecondWindAndBringsMainBackToGreen(t *testing.T) {
	a := achievementsApp(t)
	a.repos = []gh.Repo{{FullName: "acme/webshop", DefaultBranch: "main"}}
	now := time.Now()
	run := gh.Run{ID: 7, Name: "CI", HeadBranch: "main", Status: "completed", Conclusion: "success", RunAttempt: 3,
		RunStartedAt: now.Add(-2 * time.Minute), UpdatedAt: now}
	w := &watch{repo: "acme/webshop", run: run, since: now.Add(-time.Minute), after: 2}
	a.watches[run.ID] = w
	a.watchFinished(w)
	for _, id := range []string{"first-light", "second-wind", "flake-hunter", "green-again"} {
		if a.ach.tierOf(achievementByID(id)) == tierNone {
			t.Errorf("%s should be unlocked", id)
		}
	}
	if n := a.ach.Counts["pipeline-whisperer"]; n != 1 {
		t.Errorf("a run watched to its end counts once, got %d", n)
	}
}

func TestARunTheWatchOfItsRepoFoundCountsForNothing(t *testing.T) {
	a := achievementsApp(t)
	run := gh.Run{ID: 8, Name: "CI", Status: "completed", Conclusion: "success", UpdatedAt: time.Now()}
	w := &watch{repo: "acme/webshop", run: run, fromRepo: true}
	a.watches[run.ID] = w
	a.watchFinished(w)
	if n := a.ach.Counts["pipeline-whisperer"]; n != 0 {
		t.Errorf("you didn't watch it: counted %d", n)
	}
}

func TestTheKonamiCodeIsASecret(t *testing.T) {
	a := achievementsApp(t)
	keys := map[string]tea.KeyType{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight}
	for _, k := range konamiCode {
		if kt, ok := keys[k]; ok {
			pressKeys(a, tea.KeyMsg{Type: kt})
		} else {
			pressKeys(a, runes(k))
		}
	}
	if a.ach.tierOf(achievementByID("konami")) != tierGold {
		t.Errorf("the Konami code should unlock its secret")
	}
}

func TestMergesCountByDayAndBySizeAndWhen(t *testing.T) {
	a := achievementsApp(t)
	friday := time.Date(2026, 10, 23, 17, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		a.earnMerge(friday, nil)
	}
	a.earnMerge(friday, &gh.PullRequestDetail{Additions: 1, Deletions: 1, Files: 1})
	for _, id := range []string{"merge-train", "living-dangerously", "one-liner"} {
		if a.ach.tierOf(achievementByID(id)) == tierNone {
			t.Errorf("%s should be unlocked", id)
		}
	}
	if a.ach.tierOf(achievementByID("weekend-warrior")) != tierNone {
		t.Errorf("a Friday is no weekend")
	}
	a.earnMerge(friday.AddDate(0, 0, 1), &gh.PullRequestDetail{Additions: 10, Deletions: 300, Files: 4})
	for _, id := range []string{"weekend-warrior", "spring-cleaning"} {
		if a.ach.tierOf(achievementByID(id)) == tierNone {
			t.Errorf("%s should be unlocked", id)
		}
	}
	if n := a.ach.Daily["merges"].N; n != 1 {
		t.Errorf("a new day starts the count of merges again, got %d", n)
	}
}

func TestTheStreakCountsWorkingDaysAndSkipsTheWeekend(t *testing.T) {
	a := achievementsApp(t)
	a.ach = newProgress()
	thursday := time.Date(2026, 10, 22, 9, 0, 0, 0, time.Local)
	for _, d := range []int{0, 1, 4, 4, 5} { // Thursday, Friday, Monday twice, Tuesday
		a.earnOnStart(thursday.AddDate(0, 0, d))
	}
	if a.ach.Streak != 4 {
		t.Errorf("four working days in a row, the streak is %d", a.ach.Streak)
	}
	a.earnOnStart(thursday.AddDate(0, 0, 7))
	if a.ach.Streak != 1 {
		t.Errorf("a missed working day starts the streak again, it is %d", a.ach.Streak)
	}
	if a.ach.tierOf(achievementByID("on-a-roll")) != tierBronze {
		t.Errorf("three working days in a row is bronze")
	}
}

func TestProgressIsKeptNextToTheConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("CALAMARES_CONFIG", cfgPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(gh.NewWithToken("github.com", "x", "test"), cfg, Options{Version: "dev"})
	a.earn("rubber-stamp", 3)
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfgPath), achievementsFile)); err != nil {
		t.Fatalf("achievements.json was not written: %v", err)
	}
	b := New(gh.NewWithToken("github.com", "x", "test"), cfg, Options{Version: "dev"})
	if b.ach.Counts["rubber-stamp"] != 3 || b.ach.tierOf(achievementByID("rubber-stamp")) != tierBronze {
		t.Errorf("a new session should read the progress back: %v", b.ach.Counts)
	}
}

func TestAchievementsOffHidesThePlaceAndTheCardsButKeepsCounting(t *testing.T) {
	cfg := config.Default()
	cfg.Achievements = "off"
	a := New(gh.NewWithToken("github.com", "x", "test"), cfg, Options{Version: "dev"})
	defer New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if len(tabNames) != tabAchievements {
		t.Errorf("there should be no Achievements place: %v", tabNames)
	}
	a.earnOnce("debug-mode")
	if len(a.awards) != 0 || a.ach.tierOf(achievementByID("debug-mode")) == tierNone {
		t.Errorf("an unlock should be counted without a card: %d cards", len(a.awards))
	}
	if strings.Contains(ansi.Strip(a.renderTopBar()), "Lv ") {
		t.Errorf("the header should not show a level")
	}
}

func TestLevelsGrowWithXPAndCarryTitles(t *testing.T) {
	if levelOf(0) != 1 || levelTitle(1) != "Intern" {
		t.Errorf("you start at level 1, an intern")
	}
	prev := 0
	for l := 2; l <= maxLevel; l++ {
		need := levelNeeds(l)
		if need-levelNeeds(l-1) <= prev {
			t.Errorf("level %d should take more XP than the one before", l)
		}
		prev = need - levelNeeds(l-1)
		if levelOf(need) != l || levelOf(need-1) != l-1 {
			t.Errorf("%d XP should reach level %d", need, l)
		}
	}
	if levelOf(1_000_000) != maxLevel {
		t.Errorf("there is no level beyond %d", maxLevel)
	}
}

func TestTheAchievementsPlaceShowsTheCaseAndTheMedalUnderTheCursor(t *testing.T) {
	a := achievementsApp(t)
	a.earn("second-wind", 12)
	press(a, "7")
	if a.tab != tabAchievements {
		t.Fatalf("7 should open Achievements, at place %d", a.tab)
	}
	for _, w := range []int{80, 120, 160} {
		a.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		for i, l := range strings.Split(a.View(), "\n") {
			if width(l) > w {
				t.Errorf("at %d wide line %d is %d wide", w, i, width(l))
			}
		}
	}
	out := ansi.Strip(a.View())
	wantAll(t, out, "Achievements", "Trophy case", "Almost there", "Timeline", "Level", "First light", "Watch a run you picked go green.")
	pressKeys(a, tea.KeyMsg{Type: tea.KeyRight})
	if v := a.top().(*achievementsView); v.sel != "second-wind" {
		t.Fatalf("right should move to the next medal, on %s", v.sel)
	}
	wantAll(t, ansi.Strip(a.View()), "Second wind", "12 of 50 for gold", "Tiers")
	press(a, "enter")
	if a.tab != tabActions {
		t.Errorf("enter should go to where it is earned, at place %d", a.tab)
	}
}

func TestASecretShowsOnlyItsHintUntilFound(t *testing.T) {
	a := achievementsApp(t)
	press(a, "7")
	v := a.top().(*achievementsView)
	v.sel = "konami"
	out := ansi.Strip(a.View())
	wantAll(t, out, "??????????", "Old consoles remember.")
	if strings.Contains(out, "Up up down down") {
		t.Errorf("a secret should not give its name away")
	}
}

func TestTheTimelineListsEveryTierReachedNewestFirst(t *testing.T) {
	a := achievementsApp(t)
	a.earn("rubber-stamp", 30)
	press(a, "7", "tab", "tab")
	v := a.top().(*achievementsView)
	if v.view != achTimeline {
		t.Fatalf("tab twice should show the timeline, on %s", achViews[v.view])
	}
	a.View()
	if v.timeline.Len() != 2 {
		t.Errorf("the bronze and silver rubber stamps should be listed, got %d", v.timeline.Len())
	}
	wantAll(t, ansi.Strip(a.View()), "Rubber stamp", "silver", "bronze")
}
