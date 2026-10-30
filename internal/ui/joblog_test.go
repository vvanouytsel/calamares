package ui

import (
	"testing"

	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestJobLogPrefersTellingErrorsOverTheGenericExitMessage(t *testing.T) {
	l := newJobLog([]gh.StepLog{
		{Step: gh.Step{Number: 1}, Lines: []gh.LogLine{{Text: "compiling"}}},
		{Step: gh.Step{Number: 2}, Lines: []gh.LogLine{
			{Text: "running tests"},
			{Text: "Process completed with exit code 1.", Kind: gh.LineError},
			{Text: "--- FAIL: TestSomething"},
		}},
	}, 4)
	if got := l.firstError(0); got != -1 {
		t.Errorf("a step without errors has its first error at %d, want -1", got)
	}
	if got := l.firstError(1); got != 2 {
		t.Errorf("first telling error at %d, want 2", got)
	}
	errs := l.errorLines(1, 4)
	if len(errs) != 1 || errs[0].Text != "--- FAIL: TestSomething" {
		t.Errorf("error lines %v, want only the failing test", errs)
	}
}

func TestJobLogFallsBackToTheGenericExitMessage(t *testing.T) {
	l := newJobLog([]gh.StepLog{{Step: gh.Step{Number: 1}, Lines: []gh.LogLine{
		{Text: "Process completed with exit code 2.", Kind: gh.LineError},
	}}}, 1)
	if errs := l.errorLines(0, 4); len(errs) != 1 || l.firstError(0) != 0 {
		t.Errorf("with only the exit message got %v and first error %d, want that message", errs, l.firstError(0))
	}
}
