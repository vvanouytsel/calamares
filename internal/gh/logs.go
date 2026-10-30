package gh

import (
	"regexp"
	"strings"
	"time"
)

// LineKind classifies a log line by the workflow command it carries.
type LineKind int

const (
	LineNormal LineKind = iota
	LineGroup
	LineEndGroup
	LineError
	LineWarning
	LineNotice
	LineDebug
	LineCommand
)

// LogLine is one line of a job log.
type LogLine struct {
	Time time.Time
	Text string
	Kind LineKind
}

// StepLog is the part of a job log that belongs to one step.
type StepLog struct {
	Step  Step
	Lines []LogLine
}

var markers = []struct {
	prefix string
	kind   LineKind
}{
	{"##[group]", LineGroup},
	{"##[endgroup]", LineEndGroup},
	{"##[error]", LineError},
	{"##[warning]", LineWarning},
	{"##[notice]", LineNotice},
	{"##[debug]", LineDebug},
	{"[command]", LineCommand},
}

const bom = "\uFEFF"

// ParseLog splits a raw job log into timestamped, classified lines.
func ParseLog(raw string) []LogLine {
	raw = strings.TrimPrefix(raw, bom)
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\n")
	lines := make([]LogLine, 0, len(parts))
	for _, p := range parts {
		p = cleanLogLine(strings.TrimPrefix(p, bom))
		var ll LogLine
		if sp := strings.IndexByte(p, ' '); sp > 0 && sp <= 40 {
			if t, err := time.Parse(time.RFC3339Nano, p[:sp]); err == nil {
				ll.Time = t
				p = p[sp+1:]
			}
		}
		for _, m := range markers {
			if strings.HasPrefix(p, m.prefix) {
				ll.Kind = m.kind
				p = strings.TrimPrefix(p, m.prefix)
				break
			}
		}
		ll.Text = p
		lines = append(lines, ll)
	}
	return lines
}

// SplitByStep assigns log lines to the steps of a job using their timestamps.
//
// The runner opens most steps with a recognisable line ("##[group]Run …",
// "Post job cleanup.", "Cleaning up orphan processes"); each such line moves
// on by exactly one step once the next step has started. Step timestamps only
// have second precision and several steps often start in the same second, so
// time alone is only used as a fallback: a line more than a second after the
// current step completed belongs to a later step.
func SplitByStep(lines []LogLine, steps []Step) []StepLog {
	var ran []Step
	for _, s := range steps {
		if !s.StartedAt.IsZero() && s.Conclusion != "skipped" {
			ran = append(ran, s)
		}
	}
	if len(ran) == 0 {
		return []StepLog{{Step: Step{Name: "Log", Number: 0, Status: "completed"}, Lines: lines}}
	}
	out := make([]StepLog, len(ran))
	for i := range ran {
		out[i].Step = ran[i]
	}
	cur := 0
	for _, l := range lines {
		for cur+1 < len(ran) && !l.Time.IsZero() {
			if l.Time.Truncate(time.Second).Before(ran[cur+1].StartedAt) {
				break
			}
			if isStepStart(l) {
				cur++
				break
			}
			done := ran[cur].CompletedAt
			if !done.IsZero() && !l.Time.Before(done.Add(time.Second)) {
				cur++
				continue
			}
			break
		}
		out[cur].Lines = append(out[cur].Lines, l)
	}
	// Keep steps that were skipped visible, without lines.
	if len(ran) != len(steps) {
		full := make([]StepLog, 0, len(steps))
		j := 0
		for _, s := range steps {
			if j < len(out) && out[j].Step.Number == s.Number {
				full = append(full, out[j])
				j++
			} else {
				full = append(full, StepLog{Step: s})
			}
		}
		return full
	}
	return out
}

func isStepStart(l LogLine) bool {
	switch l.Kind {
	case LineGroup:
		return strings.HasPrefix(l.Text, "Run ")
	case LineNormal:
		return l.Text == "Post job cleanup." || strings.HasPrefix(l.Text, "Cleaning up orphan processes")
	}
	return false
}

var errorish = regexp.MustCompile(`(?i)(^\s*(error|fatal|panic)\b[:\s!]|--- FAIL|^\s*FAIL\b|npm ERR!|Traceback \(most recent call last\)|^\s*E\s{2,}|\bAssertionError\b|^\s*[✗✖×]\s|\bexited with (code|status) [1-9]|\bexit code [1-9]|\bBUILD FAILED\b|\bfailed with exit code\b)`)

// IsErrorLine reports whether a log line looks like an error.
func IsErrorLine(l LogLine) bool {
	if l.Kind == LineError {
		return true
	}
	if l.Kind != LineNormal {
		return false
	}
	return errorish.MatchString(l.Text)
}

// genericFailure matches what the runner says of any step that failed, with
// nothing about why: the exit code and, with container hooks on a
// self-hosted runner, that the script step or the container implementation
// failed.
var genericFailure = regexp.MustCompile(`^(?:Error: )*(?:Process completed with exit code \d+|Executing the custom container implementation failed\. Please contact your self hosted runner administrator|failed to run script step: command terminated with non-zero exit code: .*exit code \d+)\.?$`)

// IsGenericFailure reports whether the line is one of the runner's generic
// messages for a failed step, such as "Process completed with exit code N.".
func IsGenericFailure(l LogLine) bool { return genericFailure.MatchString(strings.TrimSpace(l.Text)) }

// RunnerImage names the image a job ran on and its version, as the runner
// logs them before the first step ("ubuntu-24.04 20261012.1"), or "" for a
// self-hosted runner, which logs none.
func RunnerImage(lines []LogLine) string {
	image, version := "", ""
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if l.Kind == LineGroup && strings.HasPrefix(text, "Run ") {
			break
		}
		if v, ok := strings.CutPrefix(text, "Image: "); ok && image == "" {
			image = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(text, "Version: "); ok && image != "" && version == "" {
			version = strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(image + " " + version)
}
