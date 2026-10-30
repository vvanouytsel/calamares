package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func ctrlS() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlS} }

func runningRunApp(t *testing.T) *App {
	a := apiApp(t)
	run := ghRun(7, "in_progress")
	run.RunStartedAt = time.Now().Add(-time.Minute)
	v := newRunView(a, "o/r", run)
	v.loaded = true
	a.stack = append(a.stack, v)
	return a
}

func TestCtrlSFreezesTheScreenWhileTimePasses(t *testing.T) {
	a := runningRunApp(t)
	a.Update(ctrlS())
	frozen := a.View()
	if !strings.Contains(ansi.Strip(frozen), "frozen") {
		t.Errorf("the frozen screen does not say it is frozen:\n%s", ansi.Strip(frozen))
	}
	a.Update(animMsg(time.Now()))
	a.Update(animMsg(time.Now()))
	time.Sleep(1100 * time.Millisecond)
	if a.View() != frozen {
		t.Error("the screen changed while frozen")
	}
}

func TestAnyKeyUnfreezesAndStillActs(t *testing.T) {
	a := runningRunApp(t)
	a.Update(ctrlS())
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if _, ok := a.top().(*helpView); !ok {
		t.Errorf("? after freezing should open the help, top is %T", a.top())
	}
	if strings.Contains(ansi.Strip(a.View()), "frozen") {
		t.Error("the screen is still frozen after a key")
	}
}

func TestCtrlSAgainUnfreezesWithoutFreezingAgain(t *testing.T) {
	a := runningRunApp(t)
	a.Update(ctrlS())
	a.Update(ctrlS())
	if a.frozen != "" {
		t.Error("ctrl+s twice should leave the screen updating")
	}
}

func TestResizingUnfreezes(t *testing.T) {
	a := runningRunApp(t)
	a.Update(ctrlS())
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if a.frozen != "" {
		t.Error("a resized terminal needs a fresh screen, but it stayed frozen")
	}
}
