package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// noticeableDifference is how far a job has to be from its typical duration
// before the difference is shown.
const noticeableDifference = 5 * time.Second

// A slower job is amber or red only when it is both a good deal slower in
// proportion and by the clock, so short jobs that vary a little stay quiet.
const (
	slowRatio, slowDifference         = 1.25, 15 * time.Second
	verySlowRatio, verySlowDifference = 2.0, 30 * time.Second
)

// States of reading the typical durations.
const (
	typicalIdle = iota
	typicalLoading
	typicalDone
	typicalFailed
)

// typicalTimeout bounds reading the earlier runs, so a slow answer cannot
// keep the background requests busy.
const typicalTimeout = 30 * time.Second

// typicalKey identifies the runs a job's typical duration comes from.
type typicalKey struct {
	repo     string
	workflow int64
	event    string
	scope    gh.BranchScope
}

type typicalMsg struct {
	id       int
	key      typicalKey
	baseline gh.Baseline
	err      error
}

func (m typicalMsg) viewID() int { return m.id }

// versusTypical compares how long a job has taken with how long it usually
// takes: how much slower or faster, or how far a running job is.
func versusTypical(elapsed, typical time.Duration, running bool) (string, lipgloss.Color) {
	if typical <= 0 || elapsed <= 0 {
		return "", cDim
	}
	if running && elapsed < typical {
		return fmt.Sprintf("%d%%", int(elapsed*100/typical)), cDim
	}
	diff := elapsed - typical
	if diff > -noticeableDifference && diff < noticeableDifference {
		return "≈", cDim
	}
	ratio := float64(elapsed) / float64(typical)
	if diff < 0 {
		note := "−" + dur(-diff) + " " + arrow(false)
		if ratio <= 0.8 {
			return note, cGreen
		}
		return note, cDim
	}
	note := "+" + dur(diff) + " " + arrow(true)
	switch {
	case ratio >= verySlowRatio && diff >= verySlowDifference:
		return note, cRed
	case ratio >= slowRatio && diff >= slowDifference:
		return note, cAmber
	}
	return note, cDim
}

func arrow(up bool) string {
	switch {
	case ascii && up:
		return "^"
	case ascii:
		return "v"
	case up:
		return "▲"
	}
	return "▼"
}

// byPrevious says durations are compared with the previous successful run
// rather than the median.
func (a *App) byPrevious() bool { return a.cfg.CompareDurations == config.ComparePrevious }

// base is what a job or step is compared with in the mode on: its median,
// or how long it took in the previous successful run; 0 when unknown.
func (v *runView) base(a *App, t gh.TypicalJob) time.Duration {
	if a.byPrevious() {
		if v.previous == nil {
			return 0
		}
		return t.Previous
	}
	if t.Samples == 0 {
		return 0
	}
	return t.Median
}

// compare compares how long something took with its base; only what ran
// fine is compared, as a failure stops early.
func compare(elapsed, base time.Duration, k Kind) (string, lipgloss.Color) {
	if base <= 0 || (k != KOK && k != KRun) {
		return "", cDim
	}
	return versusTypical(elapsed, base, k == KRun)
}

// jobLine is how long a job of the run took and how that compares with its base.
func (v *runView) jobLine(a *App, j gh.Job, now time.Time) string {
	t, _ := gh.TypicalFor(v.typical, j.Name)
	if j.StartedAt.IsZero() {
		return ""
	}
	k := jobKind(j)
	vs := v.versus(a, j.Duration(now), v.base(a, t), k, t.Samples)
	if vs == "" {
		return ""
	}
	return tookFact(j.Duration(now), k, k == KRun) + " " + vs
}

// jobNote compares a job of this run with its base.
func (v *runView) jobNote(a *App, j gh.Job, now time.Time) (string, lipgloss.Color) {
	t, ok := gh.TypicalFor(v.typical, j.Name)
	if !ok || j.StartedAt.IsZero() {
		return "", cDim
	}
	return compare(j.Duration(now), v.base(a, t), jobKind(j))
}

// runTook is how long the run took, or has been going, and how that
// compares with the whole run's base.
func (v *runView) runTook(a *App, run gh.Run, now time.Time) string {
	d := run.Duration(now)
	k := StatusKind(run.Status, run.Conclusion)
	out := tookFact(d, k, run.Active())
	if vs := v.versus(a, d, v.base(a, v.runTypical), k, 0); vs != "" {
		out += " " + vs
	}
	return out
}

// tookFact is a duration with its clock: "took 4m 13s", "running 2m 10s" in
// amber, or for what waits to run only the time.
func tookFact(d time.Duration, k Kind, active bool) string {
	switch {
	case k == KRun:
		return icoIn("history", cAmber) + sAmber.Render("running "+dur(d))
	case active:
		return fact("history", dur(d))
	}
	return fact("history", "took "+dur(d))
}

// versus says how something that took elapsed compares with base: the
// difference as a pill and what it is compared with, or for what is still
// going and short of its base, a meter of how far it is. samples is how many
// runs a median comes from, 0 to leave it unsaid.
func (v *runView) versus(a *App, elapsed, base time.Duration, k Kind, samples int) string {
	if base <= 0 {
		return ""
	}
	against := sDim.Render("median " + dur(base))
	if samples > 0 {
		against += sDim.Render(fmt.Sprintf(" of %d runs", samples))
	}
	if a.byPrevious() {
		against = sGold.Render(fmt.Sprintf("#%d", v.previous.RunNumber)) + sDim.Render(" "+dur(base))
	}
	note, color := compare(elapsed, base, k)
	switch {
	case note == "":
		return sDim.Render("vs ") + against
	case k == KRun && elapsed < base:
		return sAmber.Render(progressCells(float64(elapsed)/float64(base), 8)+" "+note) + sDim.Render(" of ") + against
	}
	return deltaPill(note, color) + sDim.Render(" vs ") + against
}

// deltaPill is a difference as a pill in its colour on a tint of it, the
// arrow first: "▲ +48s".
func deltaPill(note string, c lipgloss.Color) string {
	if rest, mark, ok := cutLast(note, " "); ok && (mark == arrow(true) || mark == arrow(false)) {
		note = mark + " " + rest
	}
	return roundPill(note, c, lipgloss.Color(mix(string(cBg), string(c), 0.14)))
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// progressCells is how far something is, from 0 to 1, as a row of n cells.
func progressCells(done float64, n int) string {
	filled := min(max(int(done*float64(n)), 0), n)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", n-filled)
}

// stepTypical is the base of a step of a job and how the step compares with it.
func (v *runView) stepTypical(a *App, job gh.Job, step int, now time.Time) (base string, note string, color lipgloss.Color) {
	s := job.Steps[step]
	jt, _ := gh.TypicalFor(v.typical, job.Name)
	b := v.base(a, jt.Steps[gh.StepKeys(job.Steps)[step]])
	if b <= 0 || s.StartedAt.IsZero() {
		return "", "", cDim
	}
	note, color = compare(s.Duration(now), b, stepKind(s))
	return dur(b), note, color
}

// stepLine is a step of a job in a list of steps, with the comparison
// columns while durations are compared.
func (v *runView) stepLine(a *App, job gh.Job, step int, duration string, w int, now time.Time) string {
	s := job.Steps[step]
	k := stepKind(s)
	left := glyph(k, a.frame) + " " + fg(k.RowColor()).Render(s.Name)
	if !a.cfg.Comparing() {
		return " " + fit(left, w-12) + sDim.Render(fmt.Sprintf("%10s", duration))
	}
	base, note, color := v.stepTypical(a, job, step, now)
	return " " + fit(left, w-typicalColumnsWidth-2) + " " + typicalColumns(duration, base, note, color)
}

// loadTypical reads how long the jobs of this run's workflow usually take,
// once per workflow and trigger for as long as the app runs.
func (v *runView) loadTypical(a *App) tea.Cmd {
	if !a.cfg.Comparing() || v.typicalLoad != typicalIdle || v.run.WorkflowID == 0 {
		return nil
	}
	v.typicalLoad = typicalLoading
	key := typicalKey{v.repo, v.run.WorkflowID, v.run.Event, a.branchScope(v.repo, v.run)}
	if b, ok := a.typicals[key]; ok && v.previousFits(b) {
		v.typical, v.previous, v.runTypical, v.typicalLoad = b.Jobs, b.Previous, b.Run, typicalDone
		return nil
	}
	id, runID, created := v.id, v.run.ID, v.run.CreatedAt
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(gh.Background(context.Background()), typicalTimeout)
		defer cancel()
		b, err := a.client.JobBaseline(ctx, key.repo, key.workflow, key.event, key.scope, runID, created)
		return typicalMsg{id: id, key: key, baseline: b, err: err}
	}
}

func (v *runView) typicalLoaded(a *App, msg typicalMsg) {
	if msg.err != nil {
		v.typicalLoad = typicalFailed
		return
	}
	a.typicals[msg.key] = msg.baseline
	v.typical, v.previous, v.runTypical, v.typicalLoad = msg.baseline.Jobs, msg.baseline.Previous, msg.baseline.Run, typicalDone
}

// previousFits says a baseline read for another run of the workflow has the
// right previous run for this one: the newest that passed before it, and not
// this run itself.
func (v *runView) previousFits(b gh.Baseline) bool {
	return b.Previous == nil || (b.Previous.ID != v.run.ID && b.Previous.CreatedAt.Before(v.run.CreatedAt))
}

// toggleTypical moves to the next way of comparing durations, for every
// run: off, the median, the previous successful run, and off again.
func (v *runView) toggleTypical(a *App) tea.Cmd {
	next, text := config.CompareMedian, "Comparing durations with the median of earlier runs"
	switch a.cfg.CompareDurations {
	case config.CompareMedian:
		next, text = config.ComparePrevious, "Comparing durations with the previous successful run"
	case config.ComparePrevious:
		next, text = config.CompareOff, "Not comparing durations"
	}
	a.cfg.CompareDurations = next
	if err := a.cfg.Save(); err != nil {
		return a.flashCmd(nError, "Could not save config: "+err.Error())
	}
	return tea.Batch(v.loadTypical(a), a.flashCmd(nInfo, text))
}

// typicalChip says in the run header what durations are compared with.
func (v *runView) typicalChip(a *App) string {
	switch v.typicalLoad {
	case typicalLoading:
		return sAmber.Render(spin(spinner, a.frame)) + sDim.Render(" reading earlier runs…")
	case typicalFailed:
		return sRed.Render("compare: could not read earlier runs") + sDim.Render(" · t to the next mode and back to retry")
	case typicalDone:
		if a.byPrevious() {
			if v.previous == nil {
				return keycap("t") + sDim.Render(" compare: no earlier successful run "+scopeText(a.branchScope(v.repo, v.run)))
			}
			return keycap("t") + sDim.Render(" previous run "+scopeText(a.branchScope(v.repo, v.run)))
		}
		runs := v.runTypical.Samples
		for _, t := range v.typical {
			runs = max(runs, t.Samples)
		}
		if runs == 0 {
			return keycap("t") + sDim.Render(" compare: too few earlier runs for a median "+scopeText(a.branchScope(v.repo, v.run)))
		}
		return keycap("t") + sDim.Render(fmt.Sprintf(" median of %d runs %s", runs, scopeText(a.branchScope(v.repo, v.run))))
	}
	return ""
}

// typicalTreeWidth is the width the job list needs to fit the comparison
// columns next to the job names.
const typicalTreeWidth = 62

const (
	durationWidth       = 9
	typicalWidth        = 9
	differenceWidth     = 11
	typicalColumnsWidth = durationWidth + typicalWidth + differenceWidth + 4
)

// typicalColumns lays out a row's duration, what it is compared with and the
// difference under the headers of treeHeader.
func typicalColumns(duration, base, note string, noteColor lipgloss.Color) string {
	pad := func(s string, w int) string { return fmt.Sprintf("%*s", w, s) }
	return sDim.Render(pad(duration, durationWidth)+"  "+pad(base, typicalWidth)+"  ") +
		fg(noteColor).Render(fmt.Sprintf("%-*s", differenceWidth, note))
}

// wholeRunRows close the job list with the whole run in its columns, on a
// band under a hairline, while durations are compared.
func (v *runView) wholeRunRows(a *App, w int, now time.Time) []string {
	if !a.cfg.Comparing() || len(v.items) == 0 {
		return nil
	}
	run := v.shownRun()
	took := run.Duration(now)
	base := v.base(a, v.runTypical)
	note, color := compare(took, base, StatusKind(run.Status, run.Conclusion))
	shown := ""
	if base > 0 {
		shown = dur(base)
	}
	left := " " + ico("history") + sWhite.Bold(true).Render("the whole run")
	line := fit(left, w-typicalColumnsWidth-1) + " " + typicalColumns(dur(took), shown, note, color)
	return []string{sDim.Render(" ╶" + strings.Repeat("─", max(w-4, 0)) + "╴"), withBg(fit(line, w), cBand)}
}

// headerRows is how many lines the job list spends on column headers.
func (v *runView) headerRows(a *App) int {
	if a.cfg.Comparing() {
		return 1
	}
	return 0
}

// treeHeader names the columns of the job list.
func (v *runView) treeHeader(a *App, w int) string {
	if !a.cfg.Comparing() {
		return ""
	}
	return v.typicalHeader(a, "JOB", w)
}

// typicalHeader names the columns of a list of jobs or steps; the second
// names what they are compared with: MEDIAN, or the previous run.
func (v *runView) typicalHeader(a *App, first string, w int) string {
	base := "MEDIAN"
	if a.byPrevious() {
		base = "PREVIOUS"
		if v.previous != nil {
			base = fmt.Sprintf("#%d", v.previous.RunNumber)
		}
	}
	columns := fmt.Sprintf("%*s  %*s  %-*s", durationWidth, "DURATION", typicalWidth, base, differenceWidth, "Δ")
	return fit(" "+sDim.Render(first), w-typicalColumnsWidth-1) + " " + sDim.Render(columns)
}

// stepsHeader names the columns of a job's steps, while the typical durations are on.
func (v *runView) stepsHeader(a *App, w int) []string {
	if !a.cfg.Comparing() {
		return nil
	}
	return []string{v.typicalHeader(a, "STEP", w)}
}

// branchScope is which runs run is compared with: those on the default
// branch when it ran there, otherwise those on the other branches.
func (a *App) branchScope(repo string, run gh.Run) gh.BranchScope {
	def := a.defaultBranch(repo)
	return gh.BranchScope{Default: def, OnDefault: run.HeadBranch == def}
}

// scopeText says which runs a comparison is made with.
func scopeText(s gh.BranchScope) string {
	switch {
	case s.Default == "":
		return ""
	case s.OnDefault:
		return "on " + s.Default
	}
	return "on branches other than " + s.Default
}

// comparable are the runs of runs that run is compared with: the same
// branches as its own, by branchScope.
func (a *App) comparable(repo string, run gh.Run, runs []gh.Run) []gh.Run {
	scope := a.branchScope(repo, run)
	var out []gh.Run
	for _, r := range runs {
		if scope.Keeps(r) {
			out = append(out, r)
		}
	}
	return out
}
