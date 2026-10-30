package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runCause is why a failed run failed, as far as its first failed job tells:
// the job and step, and the most telling line of what went wrong.
type runCause struct {
	Job        string
	Step       string
	Text       string
	NotStarted bool   // the job never got to run a step
	Image      string // the runner image, when the log was read
	Err        string // why the cause could not be read
}

// causeID identifies a run's cause: a rerun can fail for another reason.
func causeID(repo string, r gh.Run) string {
	return fmt.Sprintf("%s#%d/%d", strings.ToLower(repo), r.ID, max(r.RunAttempt, 1))
}

var (
	causeUUID  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	causeHex   = regexp.MustCompile(`(?i)\b[0-9a-f]{7,64}\b`)
	causeTime  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2})?(\.\d+)?Z?`)
	causeAbs   = regexp.MustCompile(`(?:[A-Za-z]:\\|/)[^\s'"():,]+[/\\][^\s'"():,]*`)
	causeNum   = regexp.MustCompile(`\d+(\.\d+)*`)
	causeSpace = regexp.MustCompile(`\s+`)
)

// causeKey is the text of a cause with what differs between repos and runs
// taken out (lists, quoted values, ids, hashes, times, absolute paths and
// numbers), so the same failure in two repos has the same key. Relative paths
// stay: two failing test files are two failures. A bare exit code says
// nothing on its own, so it is told apart by the step it ended.
func (c runCause) key() string {
	if c.Text == "" {
		return ""
	}
	t := ansi.Strip(c.Text)
	t = valueList.ReplaceAllString(t, "[…]")
	t = quotedValue.ReplaceAllString(t, "…")
	t = causeUUID.ReplaceAllString(t, "…")
	t = causeTime.ReplaceAllString(t, "…")
	t = causeAbs.ReplaceAllString(t, "…")
	t = causeHex.ReplaceAllStringFunc(t, func(s string) string {
		if !strings.ContainsAny(s, "0123456789") {
			return s // a word such as "defaced"
		}
		return "…"
	})
	t = causeNum.ReplaceAllString(t, "N")
	t = strings.ToLower(strings.TrimSpace(causeSpace.ReplaceAllString(t, " ")))
	if c.Text == noErrorLine || gh.IsGenericFailure(gh.LogLine{Text: c.Text}) {
		t += " · " + strings.ToLower(c.Step)
	}
	return t
}

// failedJob is the first job of jobs that failed, preferring one that ran.
func failedJob(jobs []gh.Job) (gh.Job, bool) {
	var first *gh.Job
	for i, j := range jobs {
		if jobKind(j) != KFail {
			continue
		}
		if len(j.Steps) > 0 {
			return j, true
		}
		if first == nil {
			first = &jobs[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return gh.Job{}, false
}

// failedStep is the name of the first step of job that failed.
func failedStep(job gh.Job) string {
	for _, s := range job.Steps {
		if stepKind(s) == KFail {
			return s.Name
		}
	}
	return ""
}

// annotationCause is the first failure annotation that says more than the
// runner's exit code.
func annotationCause(anns []gh.Annotation) string {
	for _, an := range anns {
		if an.AnnotationLevel != "failure" {
			continue
		}
		msg := strings.TrimSpace(strings.SplitN(strings.TrimSpace(an.Message), "\n", 2)[0])
		if msg != "" && !gh.IsGenericFailure(gh.LogLine{Text: msg}) {
			return msg
		}
	}
	return ""
}

// noErrorLine is the cause of a run whose failed step logged nothing that
// reads like an error.
const noErrorLine = "No error line in the log"

// failureWords are what a line says when it tells why a step failed, though
// the runner did not mark it as an error.
var failureWords = regexp.MustCompile(`(?i)\b(fail\w*|errors?|denied|not found|invalid|cannot|can't|unable|missing|timed out|timeout|refused|exception|fatal|panic|abort\w*|no such|unauthori[sz]ed|forbidden|violations?)\b`)

// fallbackLines is how far back from the end of a failed step logCause looks
// for a line that reads like a failure.
const fallbackLines = 15

// logCause is the most telling error line of the failed step in a job's log,
// or the last line of its output that reads like a failure when the runner
// marked none, or "" when there is no such line.
func logCause(l *jobLog, job gh.Job) string {
	for si, s := range l.steps {
		if stepKind(s.Step) != KFail {
			continue
		}
		errs := l.errorLines(si, 1)
		if len(errs) == 1 && !gh.IsGenericFailure(errs[0]) {
			return strings.TrimSpace(ansi.Strip(errs[0].Text))
		}
		return failureLine(s.Lines)
	}
	return ""
}

// failureLine is the last of the final lines of a step's output that reads
// like a failure. Runner markers, the generic exit code and the step's own
// header (its script and environment) are passed over.
func failureLine(lines []gh.LogLine) string {
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < fallbackLines; i-- {
		l := lines[i]
		if l.Kind == gh.LineEndGroup && headerGroup(lines[:i]) {
			return ""
		}
		t := strings.TrimSpace(ansi.Strip(l.Text))
		if t == "" || l.Kind == gh.LineGroup || l.Kind == gh.LineEndGroup || strings.HasPrefix(t, "##[") || gh.IsGenericFailure(l) {
			continue
		}
		seen++
		if failureWords.MatchString(t) {
			return t
		}
	}
	return ""
}

// headerGroup says the group that ends after lines is the step's header, the
// "Run …" group the runner opens with the script it runs.
func headerGroup(lines []gh.LogLine) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Kind == gh.LineGroup {
			return strings.HasPrefix(strings.TrimSpace(lines[i].Text), "Run ")
		}
	}
	return false
}

// readCause works out why run failed: its jobs, the annotations of the first
// failed job and, when those only give the exit code, its log. Everything is
// read in the background lane.
func readCause(a *App, repo string, run gh.Run) runCause {
	ctx := gh.Background(context.Background())
	if run.Conclusion == "startup_failure" {
		return runCause{Text: "The workflow could not start: its file is invalid or refers to something that is missing"}
	}
	jobs, err := a.client.ListJobs(ctx, repo, run.ID)
	if err != nil {
		return runCause{Err: err.Error()}
	}
	job, ok := failedJob(jobs)
	if !ok {
		return runCause{Text: "Failed without a failed job"}
	}
	c := runCause{Job: job.Name, Step: failedStep(job), NotStarted: len(job.Steps) == 0 || job.StartedAt.IsZero()}
	anns, err := a.client.Annotations(ctx, repo, job.ID)
	if err == nil {
		c.Text = annotationCause(anns)
	}
	if c.NotStarted {
		if c.Text == "" {
			c.Text = "The job never started"
		}
		return c
	}
	raw, err := a.client.JobLog(ctx, repo, job.ID)
	if err != nil {
		if c.Text == "" {
			c.Text = "Failed in " + c.Step
		}
		return c
	}
	lines := gh.ParseLog(raw)
	c.Image = gh.RunnerImage(lines)
	if c.Text == "" {
		c.Text = logCause(newJobLog(gh.SplitByStep(lines, job.Steps), len(lines)), job)
	}
	if c.Text == "" {
		c.Text = noErrorLine
	}
	return c
}

type causeMsg struct {
	id    string
	cause runCause
}

// snapshotCauses keeps the causes read, so the Failing view groups at once
// next time; a cause of a finished attempt never changes. Its version goes up
// when the way a cause is read does, so old causes are read again.
const snapshotCauses = "causes/4"

// maxCauseReads is how many causes are read at the same time.
const maxCauseReads = 4

// loadCauses reads the causes of the failed runs not known yet, a few at a
// time; each one read starts the next. Nothing is read until the Failing
// view was shown, as it may take a log per run.
func (s *store) loadCauses(a *App, runs []repoRun) tea.Cmd {
	if !s.wantCauses {
		return nil
	}
	return s.readCauses(a, runs)
}

// readCauses reads the causes of runs not known yet, whether the Failing
// view was shown or not.
func (s *store) readCauses(a *App, runs []repoRun) tea.Cmd {
	for _, x := range runs {
		id := causeID(x.repo, x.run)
		if _, ok := s.causes[id]; ok || s.causeBusy[id] {
			continue
		}
		s.causeQueue = append(s.causeQueue, x)
	}
	var cmds []tea.Cmd
	for len(s.causeBusy) < maxCauseReads {
		cmd := s.nextCause(a)
		if cmd == nil {
			break
		}
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (s *store) nextCause(a *App) tea.Cmd {
	for len(s.causeQueue) > 0 {
		x := s.causeQueue[0]
		s.causeQueue = s.causeQueue[1:]
		id := causeID(x.repo, x.run)
		if _, ok := s.causes[id]; ok || s.causeBusy[id] {
			continue
		}
		s.causeBusy[id] = true
		return func() tea.Msg { return causeMsg{id, readCause(a, x.repo, x.run)} }
	}
	return nil
}

func (s *store) gotCause(a *App, m causeMsg) tea.Cmd {
	delete(s.causeBusy, m.id)
	if m.cause.Err == "" {
		s.causes[m.id] = m.cause
		a.client.Remember(snapshotCauses, s.causes)
	}
	return s.nextCause(a)
}
