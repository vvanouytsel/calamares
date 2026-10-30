package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runTickKeys tick runs, so R and c act on all of them.
func runTickKeys(t *Table) []Binding {
	return []Binding{
		{Keys: []string{" "}, Label: "space", Desc: "tick", Help: "tick the run: then R and c act on all ticked ones",
			Do: do(func() tea.Cmd { t.ToggleMark(); return nil })},
		t.rangeKeys(),
		{Keys: []string{"*"}, Help: "tick every run shown (again: untick them)", Do: do(func() tea.Cmd { t.MarkAll(); return nil })},
	}
}

// bulkRunKeys rerun the failed jobs of the ticked runs that failed, or cancel
// the ticked runs that haven't finished.
func (a *App) bulkRunKeys(t *Table, ticked []repoRun) []Binding {
	var failed, active []repoRun
	for _, x := range ticked {
		switch {
		case x.run.Active():
			active = append(active, x)
		case StatusKind(x.run.Status, x.run.Conclusion) == KFail:
			failed = append(failed, x)
		}
	}
	return []Binding{
		{Keys: []string{"R"}, Desc: fmt.Sprintf("rerun failed (%d)", len(failed)), Help: "rerun the failed jobs of every ticked run that failed",
			Off: len(failed) == 0, Mutates: true, Need: gh.CapWrite,
			Do: do(func() tea.Cmd { return a.rerunFailedOf(failed, t.ClearMarks) })},
		{Keys: []string{"c"}, Desc: fmt.Sprintf("cancel (%d)", len(active)), Help: "cancel every ticked run that hasn't finished",
			Off: len(active) == 0, Mutates: true, Need: gh.CapWrite,
			Do: do(func() tea.Cmd { return a.cancelRuns(active, t.ClearMarks) })},
	}
}

// tickedRunsLine is the line above a list of runs while some are ticked.
func tickedRunsLine(n, w int) string {
	return tickedLine(n, w, "R", "rerun failed", "c", "cancel")
}

// tickedLine says what the ticked rows are for, in a band of the accent:
// keysAndWhat are pairs of a key and what it does to all of them.
func tickedLine(n, w int, keysAndWhat ...string) string {
	bar := fg(cCyan).Render("▌")
	if ascii {
		bar = fg(cCyan).Render("|")
	}
	var keys []string
	for i := 0; i+1 < len(keysAndWhat); i += 2 {
		keys = append(keys, keycap(keysAndWhat[i])+sText.Render(" "+keysAndWhat[i+1]))
	}
	keys = append(keys, keycap("shift+↑↓")+sText.Render(" range"), keycap("esc")+sText.Render(" clear"))
	text := bar + " " + sWhite.Bold(true).Render(fmt.Sprintf("%d ticked", n)) + sDim.Render("  ·  ") + strings.Join(keys, "  ")
	return withBg(fit(text, w), markedBg())
}
