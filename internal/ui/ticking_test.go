package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// tickingApp is on the list of pull requests, with three of them.
func tickingApp(t *testing.T) (*App, *pullsView) {
	a := apiApp(t)
	now := time.Now()
	a.st.prs = []gh.PullRequest{
		{Number: 1, Title: "One", Repo: "acme/webshop", Why: gh.WhyReview, UpdatedAt: now},
		{Number: 2, Title: "Two", Repo: "acme/webshop", Why: gh.WhyReview, UpdatedAt: now.Add(-time.Minute)},
		{Number: 3, Title: "Three", Repo: "acme/webshop", Why: gh.WhyReview, UpdatedAt: now.Add(-2 * time.Minute)},
	}
	a.st.prsState.at = now
	a.setTab(tabPulls)
	_ = a.View()
	return a, a.roots[tabPulls].(*pullsView)
}

func TestSpaceTicksRowsAndTheListCountsThem(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, runes(" "), tea.KeyMsg{Type: tea.KeyDown}, runes(" "))
	out := screen(a)
	if n := len(v.t.Marks()); n != 2 || !strings.Contains(out, "2 ticked") || strings.Count(out, ansiStrip(checkbox(true))) != 2 {
		t.Errorf("two rows should be ticked, %d are:\n%s", n, out)
	}
}

func TestCtrlATicksEveryRowAndEscClearsThemFirst(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, tea.KeyMsg{Type: tea.KeyCtrlA})
	if len(v.t.Marks()) != 3 {
		t.Fatalf("ctrl+a should tick the three rows, got %d", len(v.t.Marks()))
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEsc})
	if len(v.t.Marks()) != 0 || a.top() != View(v) {
		t.Errorf("esc should clear the selection first, and stay here")
	}
}

func TestShiftDownTicksARangeAndShiftUpShrinksIt(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, tea.KeyMsg{Type: tea.KeyShiftDown}, tea.KeyMsg{Type: tea.KeyShiftDown})
	if n := len(v.t.Marks()); n != 3 {
		t.Fatalf("two shift+down from the top should tick three rows, got %d", n)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyShiftUp})
	if n := len(v.t.Marks()); n != 2 || v.t.Marked(v.t.rows[v.t.view[2]].Key) {
		t.Errorf("shift+up should untick the last row again, %d ticked", n)
	}
}

func TestRowsTickedBeforeARangeStayTicked(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, runes("j"), runes("j"), runes(" "), runes("k"), runes("k"))
	pressKeys(a, tea.KeyMsg{Type: tea.KeyShiftDown}, tea.KeyMsg{Type: tea.KeyShiftUp})
	if n := len(v.t.Marks()); n != 2 || !v.t.Marked(v.t.rows[v.t.view[2]].Key) {
		t.Errorf("the range shrank to the first row; the row ticked before stays: %d ticked", n)
	}
}

func TestShiftEndTicksEverythingBelow(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, runes("j"), tea.KeyMsg{Type: tea.KeyShiftEnd})
	if n := len(v.t.Marks()); n != 2 || v.t.Marked(v.t.rows[v.t.view[0]].Key) {
		t.Errorf("shift+end from the second row should tick it and the one below, %d ticked", n)
	}
}

func TestJAndKTickARangeWhenTheTerminalKeepsShiftArrows(t *testing.T) {
	a, v := tickingApp(t)
	pressKeys(a, runes("J"), runes("J"))
	if n := len(v.t.Marks()); n != 3 {
		t.Fatalf("two J from the top should tick three rows, got %d", n)
	}
	pressKeys(a, runes("K"))
	if n := len(v.t.Marks()); n != 2 {
		t.Errorf("K should shrink the range again, %d ticked", n)
	}
}
