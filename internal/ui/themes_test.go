package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/config"
)

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func TestEveryThemeIsCompleteAndHasAName(t *testing.T) {
	seen := map[string]bool{}
	for _, th := range themes {
		if th.Name == "" || th.Desc == "" || seen[th.Name] {
			t.Errorf("theme %q needs a unique name and a description", th.Name)
		}
		seen[th.Name] = true
		for i, f := range th.fields() {
			if !hexColour.MatchString(*f) {
				t.Errorf("theme %s: colour %d is %q, not #rrggbb", th.Name, i, *f)
			}
		}
	}
	if themeNamed("NEON") == nil || themeNamed("troll") != nil {
		t.Errorf("themes are found by name, ignoring case, and there is no troll theme")
	}
	t.Cleanup(func() { applyTheme(themes[0]) })
	for _, th := range themes {
		applyTheme(th) // must not panic, and must leave a tint for every status
		if len(tint) != 4 {
			t.Errorf("theme %s: tints = %d", th.Name, len(tint))
		}
	}
}

func TestSlateIsLighterThanTheOtherDarkThemesAndDarkerThanPaper(t *testing.T) {
	lightness := func(hex string) int { r, g, b := rgb(hex); return r + g + b }
	slate := themeNamed("slate")
	if slate == nil || slate.Light {
		t.Fatalf("slate should be a dark theme")
	}
	for _, th := range themes {
		switch {
		case th.Name == "slate":
		case th.Light && lightness(th.Bg) <= lightness(slate.Bg):
			t.Errorf("slate (%s) should be darker than %s (%s)", slate.Bg, th.Name, th.Bg)
		case !th.Light && lightness(th.Bg) >= lightness(slate.Bg):
			t.Errorf("slate (%s) should be lighter than %s (%s)", slate.Bg, th.Name, th.Bg)
		}
	}
}

func TestMixBlendsTwoColours(t *testing.T) {
	if got := mix("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("mix half way = %s", got)
	}
	if mix("#102030", "#ffffff", 0) != "#102030" || mix("#102030", "#ffffff", 1) != "#ffffff" {
		t.Errorf("mix keeps its ends")
	}
	half := mixTheme(themes[0], themes[5], 0.5)
	if half.Bg == themes[0].Bg || half.Bg == themes[5].Bg {
		t.Errorf("a theme half way is neither end: %s", half.Bg)
	}
}

func TestTheBaseIsPaintedOverTheWholeScreen(t *testing.T) {
	out := paintBase("ab\nabcdefgh", 6, 4)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 6 {
			t.Errorf("line %d is %d cells wide, want 6", i, w)
		}
	}
}

func TestAThemeChosenIsSavedAndUsed(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[0]) })
	a := keysApp(t, false)
	a.setTheme("midnight", true)
	if a.cfg.Theme != "midnight" || currentTheme.Name != "midnight" {
		t.Errorf("theme = %q / %q, want midnight", a.cfg.Theme, currentTheme.Name)
	}
	b, err := config.Load()
	if err != nil || b.Theme != "midnight" {
		t.Errorf("saved theme = %q (%v)", b.Theme, err)
	}
	a.cycleTheme()
	if a.themeName != "ember" {
		t.Errorf("T after midnight gives %q, want ember", a.themeName)
	}
	if cmd := a.setTheme("troll", false); cmd == nil || a.themeName != "ember" {
		t.Errorf("an unknown theme says so and changes nothing")
	}
}

func TestThePickerPreviewsAndEscComesBack(t *testing.T) {
	t.Cleanup(func() { applyTheme(themes[0]) })
	a := keysApp(t, false)
	m := a.openThemes()().(modalMsg).m
	p := m.(*pickerModal)
	p.Update(a, tea.KeyMsg{Type: tea.KeyDown})
	if a.themeName != "abyss" || currentTheme.Name != "abyss" {
		t.Errorf("moving to abyss previews it, got %q", a.themeName)
	}
	p.Update(a, tea.KeyMsg{Type: tea.KeyEsc})
	if a.themeName != "cockpit" || currentTheme.Name != "cockpit" {
		t.Errorf("esc should come back to cockpit, got %q", a.themeName)
	}
}

func TestAnimationSettingIsRead(t *testing.T) {
	for in, want := range map[string]int{"": animFull, "full": animFull, "subtle": animSubtle, "off": animOff, "OFF": animOff, "none": animOff, "weird": animFull} {
		if got := parseAnim(in); got != want {
			t.Errorf("parseAnim(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestADialogOpensFromItsMiddleAndKeepsItsEdges(t *testing.T) {
	old := animLevel
	animLevel = animFull
	t.Cleanup(func() { animLevel = old })
	box := strings.Join([]string{"╭──╮", "│a │", "│b │", "│c │", "│d │", "│e │", "│f │", "╰──╯"}, "\n")
	got := strings.Split(openEase(box, nowMs()), "\n")
	if len(got) >= 8 || got[0] != "╭──╮" || got[len(got)-1] != "╰──╯" {
		t.Errorf("a dialog that just opened is cut down and keeps its top and bottom edge:\n%s", strings.Join(got, "\n"))
	}
	if all := openEase(box, nowMs()-1000); all != box {
		t.Errorf("a dialog that opened a second ago is whole")
	}
	animLevel = animOff
	if openEase(box, nowMs()) != box {
		t.Errorf("with animations off a dialog is whole at once")
	}
}

func TestDialogsFollowTheTerminal(t *testing.T) {
	if dialogWidth(200, 130) != 130 || dialogWidth(100, 130) != 80 || dialogWidth(64, 130) != 60 {
		t.Errorf("widths: %d %d %d", dialogWidth(200, 130), dialogWidth(100, 130), dialogWidth(64, 130))
	}
	if h := dialogHeight(50, 10); h < 20 || h > 34 {
		t.Errorf("height %d", h)
	}
}

func TestAPanelWrapsInsteadOfCuttingAndScrollsWhenTall(t *testing.T) {
	p := newPanel(30)
	p.add("a title that is longer than the panel can show on one line")
	joined := ansi.Strip(strings.Join(p.lines, "\n"))
	if strings.Contains(joined, "…") || !strings.Contains(joined, "line") || len(p.lines) < 2 {
		t.Errorf("a long line wraps, whole:\n%s", joined)
	}
	q := newPanel(30)
	for i := 0; i < 20; i++ {
		q.add("line")
	}
	panelScroll = 0
	first := ansi.Strip(strings.Join(q.render(8), "\n"))
	if !strings.Contains(first, "more") {
		t.Errorf("a tall panel says how much is out of sight:\n%s", first)
	}
	scrollPanelBy(5)
	later := ansi.Strip(strings.Join(q.render(8), "\n"))
	if !strings.Contains(later, "above") {
		t.Errorf("scrolled, it says how much is above:\n%s", later)
	}
	panelScroll = 0
}

func TestARowThatPassedGlowsOnce(t *testing.T) {
	old := animLevel
	animLevel = animFull
	t.Cleanup(func() { animLevel = old })
	tb := newTable(Column{Title: "A"})
	tb.SetRows([]Row{{Key: "x", Cells: []Cell{{Text: "x"}}, Kind: KRun}})
	if len(tb.flashes) != 0 {
		t.Fatalf("a first load flashes nothing")
	}
	tb.SetRows([]Row{{Key: "x", Cells: []Cell{{Text: "x"}}, Kind: KOK}})
	if _, ok := tb.flashes["x"]; !ok {
		t.Errorf("a run that passed should glow")
	}
}
