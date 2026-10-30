package gh

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// baselineRuns caps how many earlier runs a baseline reads; each one costs a
// request for its jobs.
const baselineRuns = 8

// minBaselineSamples is how many earlier runs a job needs to have finished
// in before its typical duration is trusted.
const minBaselineSamples = 3

// TypicalJob is how long a job usually takes, and how long it took in the
// previous successful run.
type TypicalJob struct {
	Median  time.Duration
	Samples int
	// Previous is how long it took in Baseline.Previous, or 0 when it didn't
	// run or pass there.
	Previous time.Duration
	// Steps is how long each step of the job usually takes, by StepKeys.
	Steps map[string]TypicalJob
}

// Baseline is how long the jobs of a workflow usually take, by job name, and
// the previous successful run they are also compared with.
type Baseline struct {
	Jobs     map[string]TypicalJob
	Previous *Run
	// Run is how long the whole run usually takes and took in Previous.
	Run TypicalJob
}

// StepKeys names the steps of a job so a step can be found again in another
// run: a step's name, with a count after it when a name occurs more than once.
func StepKeys(steps []Step) []string {
	seen := map[string]int{}
	keys := make([]string, len(steps))
	for i, s := range steps {
		keys[i] = s.Name
		if seen[s.Name] > 0 {
			keys[i] = fmt.Sprintf("%s #%d", s.Name, seen[s.Name]+1)
		}
		seen[s.Name]++
	}
	return keys
}

var nameNumbers = regexp.MustCompile(`\d+`)

// looseName is a job's name with its numbers taken out, so a job named after
// a version ("deploy (2.45.0)") matches itself in runs of other versions.
// It starts with a mark no job name has.
func looseName(name string) string { return "\x00" + nameNumbers.ReplaceAllString(name, "N") }

// TypicalFor is how long the job named name usually takes and took in the
// previous run: from the runs that had a job of exactly that name or, for
// what those cannot tell, from those whose job differs from it only in its
// numbers.
func TypicalFor(jobs map[string]TypicalJob, name string) (TypicalJob, bool) {
	t, ok := jobs[name]
	loose, looseOK := jobs[looseName(name)]
	if !looseOK {
		return t, ok
	}
	steps := make(map[string]TypicalJob, len(loose.Steps))
	for key, s := range loose.Steps {
		steps[key] = fillIn(t.Steps[key], s)
	}
	for key, s := range t.Steps {
		if _, both := steps[key]; !both {
			steps[key] = s
		}
	}
	t = fillIn(t, loose)
	t.Steps = steps
	return t, true
}

// fillIn is t with what it lacks, a median or a previous duration, taken
// from loose.
func fillIn(t, loose TypicalJob) TypicalJob {
	if t.Samples == 0 {
		t.Median, t.Samples = loose.Median, loose.Samples
	}
	if t.Previous == 0 {
		t.Previous = loose.Previous
	}
	return t
}

func medianOf(ds []time.Duration) TypicalJob {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return TypicalJob{Median: ds[len(ds)/2], Samples: len(ds)}
}

// BranchScope is which runs a run is compared with. A workflow often does
// more on the default branch (a release, a deploy) than on other branches,
// so a run on the default branch is compared with runs on it, and a run on
// another branch with runs on any branch but the default one. Without a
// known default branch every run counts.
type BranchScope struct {
	Default   string // the repo's default branch, or "" when unknown
	OnDefault bool   // the run compared is on the default branch
}

// Keeps says r is a run to compare with.
func (s BranchScope) Keeps(r Run) bool {
	return s.Default == "" || (r.HeadBranch == s.Default) == s.OnDefault
}

// branchRunsRead is how many runs a baseline for a run on another branch
// reads in its one request, as the default branch's runs are left out after.
const branchRunsRead = 50

// JobBaseline reads the jobs of the latest successful runs of a workflow
// triggered by event on the branches of scope, leaving out the run excluded,
// and returns the median duration of each job by name. The newest of those
// runs started before before is the previous run, which each job and step is
// also compared with. Jobs with too few samples have no median, and a run
// whose jobs cannot be read is skipped.
func (c *Client) JobBaseline(ctx context.Context, repo string, workflowID int64, event string, scope BranchScope, excluded int64, before time.Time) (Baseline, error) {
	filter := RunFilter{WorkflowID: workflowID, Event: event, Status: "success", PerPage: baselineRuns + 1}
	switch {
	case scope.Default != "" && scope.OnDefault:
		filter.Branch = scope.Default
	case scope.Default != "":
		filter.PerPage = branchRunsRead
	}
	runs, _, err := c.ListRuns(ctx, repo, filter)
	if err != nil {
		return Baseline{}, err
	}
	durations := map[string][]time.Duration{}
	stepDurations := map[string]map[string][]time.Duration{}
	var previous *Run
	var runDurations []time.Duration
	previousJobs := map[string]Job{}
	looseInPrevious := map[string]int{}
	used, read := 0, 0
	var lastErr error
	for i, r := range runs {
		if r.ID == excluded || !scope.Keeps(r) {
			continue
		}
		if used == baselineRuns {
			break
		}
		used++
		runDurations = append(runDurations, r.Duration(r.UpdatedAt))
		jobs, err := c.ListJobs(ctx, repo, r.ID)
		if err != nil {
			lastErr = err
			continue
		}
		read++
		isPrevious := previous == nil && (before.IsZero() || r.CreatedAt.Before(before))
		if isPrevious {
			previous = &runs[i]
		}
		for _, j := range jobs {
			if isPrevious {
				looseInPrevious[looseName(j.Name)]++
			}
			if j.Conclusion == "success" && !j.StartedAt.IsZero() && !j.CompletedAt.IsZero() {
				for _, name := range []string{j.Name, looseName(j.Name)} {
					durations[name] = append(durations[name], j.CompletedAt.Sub(j.StartedAt))
					collectSteps(stepDurations, name, j)
					if isPrevious {
						previousJobs[name] = j
					}
				}
			}
		}
	}
	// Jobs of the previous run that differ only in their numbers, such as the
	// legs of a matrix, cannot tell which of them a job of this run was.
	for name, n := range looseInPrevious {
		if n > 1 {
			delete(previousJobs, name)
		}
	}
	if read == 0 && lastErr != nil {
		return Baseline{}, lastErr
	}
	typical := map[string]TypicalJob{}
	for name, ds := range durations {
		var t TypicalJob
		if len(ds) >= minBaselineSamples {
			t = medianOf(ds)
		}
		t.Steps = map[string]TypicalJob{}
		for key, steps := range stepDurations[name] {
			if len(steps) >= minBaselineSamples {
				t.Steps[key] = medianOf(steps)
			}
		}
		if pj, ok := previousJobs[name]; ok {
			t.Previous = pj.CompletedAt.Sub(pj.StartedAt)
			for k, key := range StepKeys(pj.Steps) {
				st := pj.Steps[k]
				if st.Conclusion != "success" || st.StartedAt.IsZero() || st.CompletedAt.IsZero() {
					continue
				}
				step := t.Steps[key]
				step.Previous = st.CompletedAt.Sub(st.StartedAt)
				t.Steps[key] = step
			}
		}
		if t.Samples == 0 && t.Previous == 0 {
			continue
		}
		typical[name] = t
	}
	var run TypicalJob
	if len(runDurations) >= minBaselineSamples {
		run = medianOf(runDurations)
	}
	if previous != nil {
		run.Previous = previous.Duration(previous.UpdatedAt)
	}
	return Baseline{Jobs: typical, Previous: previous, Run: run}, nil
}

func collectSteps(into map[string]map[string][]time.Duration, name string, j Job) {
	if into[name] == nil {
		into[name] = map[string][]time.Duration{}
	}
	for i, key := range StepKeys(j.Steps) {
		s := j.Steps[i]
		if s.Conclusion == "success" && !s.StartedAt.IsZero() && !s.CompletedAt.IsZero() {
			into[name][key] = append(into[name][key], s.CompletedAt.Sub(s.StartedAt))
		}
	}
}
