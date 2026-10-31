package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// inColour renders in true colour for the rest of the test.
func inColour(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

func TestDoneForNowSetsAnIssueAsideUntilSomeoneWritesAgain(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	if n := a.issuesWaitingIn(0); n != 1 {
		t.Fatalf("the question to sam should wait for them, got %d", n)
	}
	if !strings.Contains(screen(a), "set aside") {
		t.Fatalf("the panel should offer a:\n%s", screen(a))
	}
	pressKeys(a, runes("a"))
	if n := a.issuesWaitingIn(0); n != 0 {
		t.Fatalf("a should set the issue aside, %d still wait", n)
	}
	out := screen(a)
	for _, want := range []string{"Set aside until someone writes again", "bring back"} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel lacks %q:\n%s", want, out)
		}
	}

	// A new comment brings it back.
	i := a.st.issues.Assigned[0]
	i.LastComment.CreatedAt = a.aside[issueAsideKey(i)].Add(1)
	a.st.issues.Assigned[0] = i
	if n := a.issuesWaitingIn(0); n != 1 {
		t.Fatalf("a newer comment should count again, got %d", n)
	}

	// d again brings a set-aside issue back by hand.
	pressKeys(a, runes("d"))
	pressKeys(a, runes("d"))
	if n := a.issuesWaitingIn(0); n != 1 {
		t.Fatalf("d on a set-aside issue should bring it back, got %d", n)
	}
}

func TestThePlacesInTheHeaderCarryNoCounts(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	a.st.prs = []gh.PullRequest{{Number: 5, Repo: "acme/webshop", Why: gh.WhyReview, UpdatedAt: time.Now()}}
	a.st.status = map[string][]gh.Run{"acme/webshop": {{ID: 1, Name: "Deploy", Status: "waiting"}, {ID: 2, Name: "CI", Status: "in_progress"}}}
	b := ansiHeader(a)
	for _, c := range []struct{ from, to, want string }{
		{"Actions", "Pull requests", "3"},
		{"Pull requests", "Watching", "4"},
		{"Watching", "Platform", "5"},
		{"Platform", "Issues", "6"},
		{"Issues", "Achievements", "7"},
	} {
		if got := digitsBetween(b, c.from, c.to); got != c.want {
			t.Errorf("between %s and %s the header shows %q, want only %q: %s", c.from, c.to, got, c.want, b)
		}
	}
}

// digitsBetween returns the digits in s between from and the next to.
func digitsBetween(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	rest, _, _ = strings.Cut(rest, to)
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, rest)
}

// column is the cell at which want starts in s.
func column(s, want string) int { return width(s[:max(strings.Index(s, want), 0)]) }

func TestTheViewsAreOneCapsuleAndKeepTheirPlace(t *testing.T) {
	inColour(t)
	items := []stripItem{{Label: "Assigned", Count: "3"}, {Label: "Created", Count: "2"}, {Label: "Mentioned", Count: "1"}}
	first := ansi.Strip(viewStrip(items, 0, 100))
	second := ansi.Strip(viewStrip(items, 1, 100))
	if !strings.Contains(first, pillLeft+" Assigned 3 ") || !strings.Contains(second, " Created 2 ") || !strings.Contains(second, " Mentioned 1 "+pillRight) {
		t.Fatalf("the views should be one capsule:\n%q\n%q", first, second)
	}
	if lit := lipgloss.NewStyle().Background(cAccentBg).Render(" "); !strings.Contains(viewStrip(items, 1, 100), lit) {
		t.Errorf("the active view should be filled")
	}
	if column(first, "Mentioned") != column(second, "Mentioned") {
		t.Errorf("a view should not move when another becomes active:\n%q\n%q", first, second)
	}
}

func TestLabelsAreChipsAndIconsCarryTheirColour(t *testing.T) {
	inColour(t)
	c := labelChip("bug", "d73a4a")
	if !strings.Contains(ansi.Strip(c), "● bug") || !strings.Contains(c, "\x1b[") {
		t.Errorf("a label should be a coloured chip: %q", c)
	}
	if iconColor("user") != cTeal || iconColor("branch") != cRef || iconColor("merge") != cViolet || iconColor("comment") != cPink {
		t.Errorf("icons should carry the colour of what they stand for")
	}
	if personColor("robin") != personColor("Robin") {
		t.Errorf("a person keeps their colour")
	}
}

func TestTheCheckMeterKeepsEveryPartVisible(t *testing.T) {
	m := ansi.Strip(checkMeter(20, 1, 1, 10))
	if n := strings.Count(m, "━") + strings.Count(m, "╍"); n != 10 {
		t.Fatalf("the meter should be 10 cells, got %d: %q", n, m)
	}
	if !strings.Contains(m, "╍") {
		t.Errorf("one pending check should still show: %q", m)
	}
	if got := ansi.Strip(diffStat(80, 20)); got != "+80 −20 ■■■■■" {
		t.Errorf("diffStat = %q", got)
	}
}

func TestACountThatGoesUpFlares(t *testing.T) {
	inColour(t)
	old := animLevel
	animLevel = animFull
	defer func() { animLevel = old }()
	quiet := flareChip("test-flare", 1, cWait)
	loud := flareChip("test-flare", 2, cWait)
	if !strings.Contains(ansi.Strip(loud), "2") || loud == strings.Replace(quiet, "1", "2", 1) {
		t.Errorf("a count that went up should flare: %q then %q", quiet, loud)
	}
}

func TestAViewYouSwitchToIsThereAtOnceAndOnlyGlows(t *testing.T) {
	inColour(t)
	old := animLevel
	animLevel = animFull
	defer func() { animLevel = old }()
	items := []stripItem{{Label: "Glow A"}, {Label: "Glow B"}}
	settled := viewStrip(items, 0, 80)
	glowing = false
	lit := viewStrip(items, 1, 80)
	if !glowing {
		t.Fatalf("switching views should start a glow")
	}
	if !strings.Contains(ansi.Strip(lit), " Glow B "+pillRight) {
		t.Errorf("the new view should be in the capsule at once: %q", ansi.Strip(lit))
	}
	if column(ansi.Strip(lit), "Glow B") != column(ansi.Strip(settled), "Glow B") {
		t.Errorf("the glow should only change colours, never move the views")
	}
}

func TestABurstRunsOnlyWhileSomethingGlows(t *testing.T) {
	old := animLevel
	animLevel = animFull
	defer func() { animLevel = old }()
	a := &App{}
	if a.kick() == nil || !a.bursting {
		t.Fatalf("a key should start a burst")
	}
	if a.kick() != nil {
		t.Errorf("a second key should not start a second burst")
	}
	a.glowing = true
	if a.onBurst() == nil {
		t.Errorf("the burst should go on while a glow plays")
	}
	a.glowing, a.burstUntil = false, time.Time{}
	if a.onBurst() != nil || a.bursting {
		t.Errorf("the burst should stop once nothing glows")
	}
}

func TestLightRunsIntoARunningJobAndAJustFinishedOneFlashes(t *testing.T) {
	inColour(t)
	old := animLevel
	animLevel = animFull
	defer func() { animLevel = old }()
	jobs := []gh.Job{
		{ID: 1, Name: "build", Status: "completed", Conclusion: "success", StartedAt: time.Now().Add(-time.Minute)},
		{ID: 2, Name: "test", Status: "in_progress", StartedAt: time.Now()},
	}
	p := buildPipeline(jobs, samplePlan, "", false, matrixOne)
	p.zoom, p.id = zoomBoxes, "flow-test:"
	g := p.geometry()
	flowing = false
	p.draw(g, "build", 0, time.Now())
	if !flowing {
		t.Fatalf("a line into a running job should carry light")
	}

	// test passes: its box flashes green.
	jobs[1].Status, jobs[1].Conclusion = "completed", "success"
	p = buildPipeline(jobs, samplePlan, "", false, matrixOne)
	p.zoom, p.id = zoomBoxes, "flow-test:"
	glowing = false
	p.draw(p.geometry(), "build", 0, time.Now())
	if !glowing {
		t.Errorf("a job that just passed should flash")
	}
}

// capsOff turns the round ends of key caps and pills into spaces, so a test
// can read "g  all runs" as it reads on screen.
func capsOff(s string) string {
	return strings.NewReplacer(pillLeft, " ", pillRight, " ").Replace(s)
}
