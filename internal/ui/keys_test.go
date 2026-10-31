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

func keysApp(t *testing.T, readOnly bool) *App {
	withGitHub(t, map[string]string{})
	a := New(gh.NewWithToken("github.com", "x", "test"), config.Default(), Options{Version: "dev", ReadOnly: readOnly})
	a.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	return a
}

func hintKeys(a *App, bs []Binding) string {
	var ks []string
	for _, h := range a.hints(bs) {
		ks = append(ks, h.Key)
	}
	return " " + strings.Join(ks, " ") + " "
}

func TestAKeyThatDoesNotApplyIsLeftOutOfTheKeyBarButSaysWhyWhenPressed(t *testing.T) {
	a := keysApp(t, false)
	running := gh.Run{ID: 1, Name: "CI", Status: "in_progress"}
	bs := a.runKeys("o/r", running)
	if got := hintKeys(a, bs); strings.Contains(got, " R ") {
		t.Errorf("a running run offers rerun in the key bar: %q", got)
	}
	cmd, ok := a.press(bs, "R")
	drive(a, cmd, time.Second)
	m, isPicker := a.modal.(*rerunModal)
	if !ok || !isPicker || !strings.Contains(m.items[0].off, "still going") {
		t.Errorf("pressing R on a running run should open the picker and say why nothing can be chosen: handled %v, modal %T", ok, a.modal)
	}
}

func TestReadOnlyModeRefusesAndHidesEveryKeyThatChangesSomething(t *testing.T) {
	a := keysApp(t, true)
	failed := gh.Run{ID: 1, Name: "CI", Status: "completed", Conclusion: "failure"}
	bs := a.runKeys("o/r", failed)
	if got := hintKeys(a, bs); strings.Contains(got, " R ") || !strings.Contains(got, " o ") {
		t.Errorf("read-only key bar: %q, want no rerun keys but still o", got)
	}
	for _, b := range bs {
		if !b.Mutates {
			continue
		}
		a.flash, a.modal = "", nil
		cmd := a.invoke(b, "")
		drive(a, cmd, time.Second)
		if a.flash != readOnlyText || a.modal != nil {
			t.Errorf("key %q in read-only mode: flash %q, dialog open %v", b.help(), a.flash, a.modal != nil)
		}
	}
}

func TestTheFirstBindingForAKeyWins(t *testing.T) {
	a := keysApp(t, false)
	got := ""
	bs := []Binding{
		{Keys: []string{"o"}, Desc: "the job", Do: do(func() tea.Cmd { got = "job"; return nil })},
		{Keys: []string{"o"}, Desc: "the run", Do: do(func() tea.Cmd { got = "run"; return nil })},
	}
	a.press(bs, "o")
	if hints := a.hints(bs); got != "job" || len(hints) != 1 || hints[0].Desc != "the job" {
		t.Errorf("pressed %q, key bar %v: want the first binding only", got, hints)
	}
}

func TestHelpListsTheKeysOfTheScreenItWasOpenedOn(t *testing.T) {
	a := keysApp(t, false)
	rv := newRunView(a, "o/r", gh.Run{ID: 1, Name: "CI", RunNumber: 7, Status: "completed", Conclusion: "failure"})
	a.stack = append(a.stack, rv)
	a.Update(runes("?"))
	help := ansi.Strip(a.View())
	for _, want := range []string{"Keys of CI #7", "A RUN", "rerun the failed jobs", "EVERYWHERE", "refresh this screen", "MOVE"} {
		if !strings.Contains(help, want) {
			t.Errorf("help of a run doesn't mention %q:\n%s", want, help)
		}
	}
	a.Update(runes("?"))
	if a.top() != View(rv) {
		t.Errorf("? again should close the help")
	}
}

func TestHelpScrollsWhenItDoesNotFit(t *testing.T) {
	a := keysApp(t, false)
	a.Update(tea.WindowSizeMsg{Width: 90, Height: 14})
	a.stack = append(a.stack, newRunView(a, "o/r", gh.Run{ID: 1, Name: "CI", Status: "completed", Conclusion: "failure"}))
	a.Update(runes("?"))
	before := ansi.Strip(a.View())
	a.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if after := ansi.Strip(a.View()); after == before || !strings.Contains(before, "scrolls") {
		t.Errorf("help on a small screen should scroll:\n%s", before)
	}
}

// Every screen describes its keys well enough for the key bar and the help.
func TestEveryBindingOfEveryScreenCanBeShown(t *testing.T) {
	a := keysApp(t, false)
	run := gh.Run{ID: 1, Name: "CI", Status: "completed", Conclusion: "failure"}
	screens := append([]View{}, a.roots...)
	screens = append(screens,
		newRepoView(a, "o/r", ""), newRunView(a, "o/r", run), newLogView(a, "o/r", run, nil, 0),
		newPRView(a, "o/r", 1), newHelpView(a, a.roots[0]))
	for _, v := range screens {
		for _, b := range append(v.Keys(a), a.globalKeys(v)...) {
			if b.label() == "" && b.help() == "" {
				t.Errorf("%T: a binding without a key or label: %+v", v, b)
			}
			if len(b.Keys) > 0 && b.help() != "" && b.Group == "" {
				t.Errorf("%T: key %q has no heading for the help screen", v, b.label())
			}
		}
	}
}

// What a key acts on must not depend on the screen having been drawn first.
func TestAKeyActsOnTheListWithoutTheScreenHavingBeenDrawn(t *testing.T) {
	a := keysApp(t, false)
	a.repos = []gh.Repo{{Name: "r", FullName: "o/r", HasActions: true, PushedAt: time.Now()}}
	a.st.loaded, a.st.reposState.at = true, time.Now()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drive(a, cmd, time.Second)
	if rv, ok := a.top().(*repoView); !ok || rv.repo != "o/r" {
		t.Errorf("enter on the first repo, before anything was drawn, opened %T", a.top())
	}
}

func TestTheScreenIsOnlyDrawnAgainOnAnAnimationFrameWhenSomethingMoves(t *testing.T) {
	a := keysApp(t, false)
	a.repos = []gh.Repo{{Name: "r", FullName: "o/r", HasActions: true, PushedAt: time.Now()}}
	a.st.loaded, a.st.reposState.at = true, time.Now()
	s := NewScreen(a)
	first := s.View()
	a.setFlash(nInfo, "drawn again") // changed behind the screen's back, so a redraw shows
	s.Update(animMsg(time.Now()))
	if s.View() != first {
		t.Errorf("nothing moves on the repo list, yet an animation frame drew it again")
	}
	s.Update(tickMsg(time.Now()))
	if !strings.Contains(s.View(), "drawn again") {
		t.Errorf("a clock tick should draw the screen again")
	}

	rv := newRunView(a, "o/r", gh.Run{ID: 1, Name: "CI", Status: "in_progress"})
	rv.loaded = true
	a.stack = append(a.stack, rv)
	s.Update(tickMsg(time.Now()))
	before := s.View()
	a.frameAt = time.Time{} // a frame is a quarter of a second apart
	s.Update(animMsg(time.Now()))
	if s.View() == before {
		t.Errorf("a running run spins: every animation frame should draw it again")
	}
}
