package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// jobLog is a parsed job log, split per step. Finished logs never change, so
// they are cached for the whole session.
type jobLog struct {
	steps  []gh.StepLog
	errors [][]int // per step: the lines that look like errors
	count  int
	err    error
}

// newJobLog finds the error lines of each step once, as a long log takes
// too long to search on every draw.
func newJobLog(steps []gh.StepLog, count int) *jobLog {
	l := &jobLog{steps: steps, count: count, errors: make([][]int, len(steps))}
	for si, s := range steps {
		for li, line := range s.Lines {
			if gh.IsErrorLine(line) {
				l.errors[si] = append(l.errors[si], li)
			}
		}
	}
	return l
}

type jobLogMsg struct {
	id    int
	jobID int64
	log   *jobLog
}

func (m jobLogMsg) viewID() int { return m.id }

// fetchJobLog loads (or returns the cached) log of a finished job and
// delivers it to the view with id viewID.
func (a *App) fetchJobLog(viewID int, repo string, job gh.Job) tea.Cmd {
	if l, ok := a.logs[job.ID]; ok {
		return func() tea.Msg { return jobLogMsg{viewID, job.ID, l} }
	}
	return func() tea.Msg {
		raw, err := a.client.JobLog(context.Background(), repo, job.ID)
		if err != nil {
			return jobLogMsg{viewID, job.ID, &jobLog{err: err}}
		}
		lines := gh.ParseLog(raw)
		return jobLogMsg{viewID, job.ID, newJobLog(gh.SplitByStep(lines, job.Steps), len(lines))}
	}
}

// storeLog caches successful logs of completed jobs.
func (a *App) storeLog(jobID int64, l *jobLog, job *gh.Job) {
	if l.err == nil && job != nil && job.Status == "completed" {
		a.logs[jobID] = l
	}
}

func (l *jobLog) step(number int) *gh.StepLog {
	if i := l.stepIndex(number); i >= 0 {
		return &l.steps[i]
	}
	return nil
}

func (l *jobLog) stepIndex(number int) int {
	if l == nil {
		return -1
	}
	for i := range l.steps {
		if l.steps[i].Step.Number == number {
			return i
		}
	}
	return -1
}

// styleLogLine renders a log line in w cells.
func styleLogLine(l gh.LogLine, w int, showTime bool) string {
	prefix := ""
	if showTime && !l.Time.IsZero() {
		prefix = sDim.Render(l.Time.Format("15:04:05") + " ")
		w -= 9
	}
	text := strings.ReplaceAll(l.Text, "\t", "    ")
	switch l.Kind {
	case gh.LineGroup:
		return prefix + sCyan.Render(clip("▾ "+ansi.Strip(text), w))
	case gh.LineEndGroup:
		return prefix + sDim.Render("")
	case gh.LineError:
		return prefix + sRed.Bold(true).Render(clip("Error: "+ansi.Strip(text), w))
	case gh.LineWarning:
		return prefix + sAmber.Render(clip("Warning: "+ansi.Strip(text), w))
	case gh.LineNotice:
		return prefix + sBlue.Render(clip("Notice: "+ansi.Strip(text), w))
	case gh.LineDebug:
		return prefix + sDim.Render(clip(ansi.Strip(text), w))
	case gh.LineCommand:
		return prefix + sBlue.Render(clip(ansi.Strip(text), w))
	}
	if gh.IsErrorLine(l) {
		return prefix + sRed.Render(clip(ansi.Strip(text), w))
	}
	return prefix + clip(text, w)
}

// errorLines returns the most telling error lines of step si: the ones
// besides the runner's generic exit code message, unless there are none.
func (l *jobLog) errorLines(si, n int) []gh.LogLine {
	var errs, generic []gh.LogLine
	for _, li := range l.errors[si] {
		line := l.steps[si].Lines[li]
		if gh.IsGenericFailure(line) {
			generic = append(generic, line)
			continue
		}
		errs = append(errs, line)
	}
	if len(errs) == 0 {
		errs = generic
	}
	if len(errs) > n {
		errs = errs[:n]
	}
	return errs
}

// firstError is the index of the first telling error line of step si, or -1.
func (l *jobLog) firstError(si int) int {
	for _, li := range l.errors[si] {
		if !gh.IsGenericFailure(l.steps[si].Lines[li]) {
			return li
		}
	}
	if len(l.errors[si]) > 0 {
		return l.errors[si][0]
	}
	return -1
}
