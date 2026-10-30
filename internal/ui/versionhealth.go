package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// A version of a shared workflow is compared with the version before it: how
// often the runs that called it passed and how long they took. A fresh
// release that breaks its callers so shows within the hour.
const (
	freshFor      = 14 * 24 * time.Hour // a version is new this long after its first call
	freshCalls    = 50                  // or until it has this many calls
	checkMinCalls = 5                   // fewer finished calls are too early to judge
	worsePoints   = 10                  // this many points less passing is worse
	slowerRatio   = 1.25                // a median this much slower, and
	slowerBy      = 30 * time.Second    // at least this much, is slower
)

// verdict is how a version compares with the one before it.
type verdict int

const (
	verdictNone   verdict = iota // nothing to compare with
	verdictEarly                 // too few finished calls
	verdictFine                  // as good as the one before
	verdictSlower                // passes as often, takes clearly longer
	verdictWorse                 // fails clearly more often
)

// versionCheck compares a version with the previous one runs called.
type versionCheck struct {
	ver, prev      *sharedVersion
	fresh          bool      // the newest version, first called a short while ago
	first          time.Time // its first call we hold
	done, prevDone int       // finished calls
	pass, prevPass int       // the share that passed, or -1
	med, prevMed   time.Duration
	verdict        verdict
}

// finishedCalls counts the calls that passed or failed.
func finishedCalls(calls []sharedCall) int {
	n := 0
	for _, c := range calls {
		if k := StatusKind(c.run.Status, c.run.Conclusion); k == KOK || k == KFail {
			n++
		}
	}
	return n
}

// previous is the newest tag older than ver that runs we hold called, or nil.
func (s *sharedItem) previous(ver *sharedVersion) *sharedVersion {
	mine := versionParts(ver.version)
	if mine == nil {
		return nil
	}
	for _, o := range s.versions {
		theirs := versionParts(o.version)
		if theirs == nil || compareVersions(theirs, mine) >= 0 {
			continue
		}
		if finishedCalls(o.calls) > 0 {
			return o
		}
	}
	return nil
}

// newest is the newest tag in use, or nil.
func (s *sharedItem) newest() *sharedVersion {
	if len(s.versions) == 0 || versionParts(s.versions[0].version) == nil {
		return nil
	}
	return s.versions[0]
}

// check compares ver with the version before it.
func (s *sharedItem) check(ver *sharedVersion) versionCheck {
	c := versionCheck{ver: ver, prev: s.previous(ver), done: finishedCalls(ver.calls),
		pass: passingRate(callKinds(ver.calls)), med: callMedian(ver.calls)}
	if n := len(ver.calls); n > 0 {
		c.first = ver.calls[n-1].run.CreatedAt
	}
	c.fresh = ver == s.newest() && !c.first.IsZero() && time.Since(c.first) < freshFor && len(ver.calls) < freshCalls
	if c.prev == nil {
		return c
	}
	c.prevDone, c.prevPass, c.prevMed = finishedCalls(c.prev.calls), passingRate(callKinds(c.prev.calls)), callMedian(c.prev.calls)
	switch {
	case c.done < checkMinCalls:
		c.verdict = verdictEarly
	case c.pass >= 0 && c.prevPass >= 0 && c.prevPass-c.pass >= worsePoints:
		c.verdict = verdictWorse
	case clearlySlower(c.med, c.prevMed):
		c.verdict = verdictSlower
	default:
		c.verdict = verdictFine
	}
	return c
}

// clearlySlower says d is a good deal slower than before, in proportion and
// by the clock.
func clearlySlower(d, before time.Duration) bool {
	return d > 0 && before > 0 && float64(d) >= float64(before)*slowerRatio && d-before >= slowerBy
}

// newCheck is the check of an item's newest version while it is new, and
// whether there is one.
func (s *sharedItem) newCheck() (versionCheck, bool) {
	ver := s.newest()
	if ver == nil {
		return versionCheck{}, false
	}
	c := s.check(ver)
	return c, c.fresh && c.verdict != verdictNone
}

// verdictColor is the colour a verdict is shown in.
func (c versionCheck) color() lipgloss.Color {
	switch c.verdict {
	case verdictWorse:
		return cRed
	case verdictSlower:
		return cAmber
	case verdictFine:
		return cGreen
	}
	return cDim
}

// worseNew counts the items whose new version fails clearly more.
func worseNew(items []*sharedItem) int {
	n := 0
	for _, it := range items {
		if c, ok := it.newCheck(); ok && c.verdict == verdictWorse {
			n++
		}
	}
	return n
}

// What is read to explain a difference between two versions, only for the
// version line under the cursor: why its failed calls failed, the commits
// between the two tags and how long the jobs of a few calls took.
const (
	checkCauses = 10 // failed calls per version whose cause is read
	checkJobs   = 5  // passed calls per version whose jobs are read
)

// versionDiff is what changed between two tags of a shared workflow.
type versionDiff struct {
	busy    bool
	err     error
	commits int
	bumps   []string // the actions the file moved to another version, as "setup-node v3 → v4"
}

type versionDiffMsg struct {
	key   string
	cmp   gh.Comparison
	err   error
	file  string
	since time.Time
}

type callJobsMsg struct {
	runID int64
	jobs  []gh.Job
	err   error
}

func diffKey(repo, from, to string) string {
	return strings.ToLower(repo) + "\x00" + from + "\x00" + to
}

// readCheck starts reading what explains how ver compares with the version
// before it: the causes of failed calls, the diff and the jobs of passed calls.
func (s *store) readCheck(a *App, it *sharedItem, ver *sharedVersion) tea.Cmd {
	if it == nil || ver == nil || it.action {
		return nil
	}
	prev := it.previous(ver)
	if prev == nil {
		return nil
	}
	var failed []repoRun
	var cmds []tea.Cmd
	for _, v := range []*sharedVersion{ver, prev} {
		n, m := 0, 0
		for _, c := range v.calls {
			switch StatusKind(c.run.Status, c.run.Conclusion) {
			case KFail:
				if n < checkCauses {
					failed = append(failed, *c.repoRun)
					n++
				}
			case KOK:
				if m < checkJobs {
					cmds = append(cmds, s.readCallJobs(a, c.repo, c.run.ID))
					m++
				}
			}
		}
	}
	cmds = append(cmds, s.readCauses(a, failed))
	key := diffKey(it.repo, prev.version, ver.version)
	if _, ok := s.diffs[key]; !ok {
		s.diffs[key] = &versionDiff{busy: true}
		repo, file, from, to := it.repo, it.path, prev.version, ver.version
		cmds = append(cmds, func() tea.Msg {
			cmp, err := a.client.Compare(gh.Background(context.Background()), repo, from, to)
			return versionDiffMsg{key: key, cmp: cmp, err: err, file: file}
		})
	}
	return tea.Batch(cmds...)
}

// readCallJobs reads the jobs of a call once.
func (s *store) readCallJobs(a *App, repo string, runID int64) tea.Cmd {
	if _, ok := s.callJobs[runID]; ok {
		return nil
	}
	if jobs, ok := a.client.CachedJobs(repo, runID); ok {
		s.callJobs[runID] = jobs
		return nil
	}
	s.callJobs[runID] = nil
	return func() tea.Msg {
		jobs, err := a.client.ListJobs(gh.Background(context.Background()), repo, runID)
		return callJobsMsg{runID, jobs, err}
	}
}

func (s *store) gotDiff(m versionDiffMsg) {
	d := &versionDiff{err: m.err}
	if m.err == nil {
		d.commits = max(m.cmp.Ahead, len(m.cmp.Commits))
		for _, f := range m.cmp.Files {
			if f.Path == m.file {
				d.bumps = usesBumps(f.Patch)
			}
		}
	}
	s.diffs[m.key] = d
}

var patchUses = regexp.MustCompile(`^([-+])\s*(?:-\s*)?uses:\s*["']?([^\s"'#@]+)@([^\s"'#]+)`)

// usesBumps lists the actions and workflows a patch moves to another version.
func usesBumps(patch string) []string {
	before := map[string]string{}
	var out []string
	for _, l := range strings.Split(patch, "\n") {
		m := patchUses.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		target, version := m[2], m[3]
		if m[1] == "-" {
			before[target] = version
			continue
		}
		if old, ok := before[target]; ok && old != version {
			name := target[strings.LastIndex(target, "/")+1:]
			out = append(out, name+" "+shortVersion(old)+" → "+shortVersion(version))
			delete(before, target)
		}
	}
	return out
}

// onlyOn are the causes of the failed calls of ver that none of the failed
// calls of prev has, with how many runs and which repos; read says how many
// of the causes are known yet.
type onlyCause struct {
	text  string
	runs  int
	repos []string
}

func onlyOn(a *App, ver, prev *sharedVersion) (causes []onlyCause, read, failed int) {
	seen := map[string]bool{}
	for _, c := range limitFailed(prev.calls) {
		if rc, ok := a.st.causes[causeID(c.repo, c.run)]; ok {
			seen[rc.key()] = true
		}
	}
	byKey := map[string]*onlyCause{}
	var order []string
	for _, c := range limitFailed(ver.calls) {
		failed++
		rc, ok := a.st.causes[causeID(c.repo, c.run)]
		if !ok {
			continue
		}
		read++
		k := rc.key()
		if k == "" || seen[k] {
			continue
		}
		oc, ok := byKey[k]
		if !ok {
			oc = &onlyCause{text: rc.Text}
			byKey[k] = oc
			order = append(order, k)
		}
		oc.runs++
		if !containsFold(oc.repos, c.repo) {
			oc.repos = append(oc.repos, c.repo)
		}
	}
	for _, k := range order {
		causes = append(causes, *byKey[k])
	}
	sort.SliceStable(causes, func(i, j int) bool { return causes[i].runs > causes[j].runs })
	return causes, read, failed
}

// limitFailed are the newest failed calls whose cause is read.
func limitFailed(calls []sharedCall) []sharedCall {
	var out []sharedCall
	for _, c := range calls {
		if StatusKind(c.run.Status, c.run.Conclusion) == KFail && len(out) < checkCauses {
			out = append(out, c)
		}
	}
	return out
}

// jobName is a job's name without the calling job in front, so the jobs of a
// shared workflow have the same name in every repo that calls it.
func jobName(name string) string {
	if i := strings.LastIndex(name, " / "); i >= 0 {
		return name[i+3:]
	}
	return name
}

// jobMedians are the median durations of the jobs of a version's passed
// calls, by name; only jobs of called workflows count.
func jobMedians(a *App, ver *sharedVersion) map[string]time.Duration {
	ds := map[string][]time.Duration{}
	n := 0
	for _, c := range ver.calls {
		if StatusKind(c.run.Status, c.run.Conclusion) != KOK || n >= checkJobs {
			continue
		}
		n++
		for _, j := range a.st.callJobs[c.run.ID] {
			if !strings.Contains(j.Name, " / ") || j.StartedAt.IsZero() || j.CompletedAt.IsZero() {
				continue
			}
			ds[jobName(j.Name)] = append(ds[jobName(j.Name)], j.CompletedAt.Sub(j.StartedAt))
		}
	}
	out := map[string]time.Duration{}
	for name, list := range ds {
		out[name] = median(list)
	}
	return out
}

// slowerJob is a job that takes clearly longer on a version than before.
type slowerJob struct {
	name       string
	now, prior time.Duration
}

func slowerJobs(a *App, ver, prev *sharedVersion) []slowerJob {
	now, before := jobMedians(a, ver), jobMedians(a, prev)
	var out []slowerJob
	for name, d := range now {
		if b, ok := before[name]; ok && d-b >= noticeableDifference && float64(d) >= float64(b)*1.1 {
			out = append(out, slowerJob{name, d, b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].now-out[i].prior > out[j].now-out[j].prior })
	return out
}

// comparePanel draws how ver compares with the version before it: both side
// by side, the failures only the newer one has, the jobs that got slower and
// what changed between the tags.
func comparePanel(a *App, p *panelText, it *sharedItem, c versionCheck) {
	ver, prev := c.ver, c.prev
	name, before := shortVersion(ver.version), shortVersion(prev.version)
	if c.verdict == verdictEarly {
		p.add("")
		p.add(sDim.Render(fmt.Sprintf("Too new to compare with %s: %d of the %d finished runs needed.", before, c.done, checkMinCalls)))
	}

	causes, read, failed := onlyOn(a, ver, prev)
	if failed > 0 {
		p.label("New failures in " + name)
		switch {
		case read < failed && len(causes) == 0:
			p.add(sDim.Render(fmt.Sprintf("Reading why %d %s failed…", failed-read, plural("run", failed-read))))
		case len(causes) == 0:
			p.add(sDim.Render("None: " + before + " fails the same way."))
		}
		for i, oc := range causes {
			if i == 4 {
				p.add(sDim.Render(fmt.Sprintf("… %d more", len(causes)-i)))
				break
			}
			p.add(glyph(KFail, a.frame) + " " + sWhite.Render(oc.text))
			who := icoIn("repo", cGold) + sText.Render(a.short(oc.repos[0]))
			if len(oc.repos) > 1 {
				who += sDim.Render(fmt.Sprintf(" and %d more", len(oc.repos)-1))
			}
			p.add("  " + sText.Render(fmt.Sprintf("Seen in %d %s", oc.runs, plural("run", oc.runs))) + sDim.Render(" · ") + who)
		}
	}

	if jobs := slowerJobs(a, ver, prev); len(jobs) > 0 {
		p.label("Slower jobs in " + name)
		for i, j := range jobs {
			if i == 4 {
				break
			}
			p.row(fg(cAmber).Render("▲ ")+sWhite.Render(j.name), sDim.Render(dur(j.prior)+" → ")+fg(cSteel).Render(dur(j.now)))
		}
	}

	p.label("Changes from " + before + " to " + name)
	d := a.st.diffs[diffKey(it.repo, prev.version, ver.version)]
	switch {
	case d == nil || d.busy:
		p.add(sDim.Render("Comparing the tags…"))
	case d.err != nil:
		p.add(sDim.Render("The tags could not be compared."))
	default:
		line := sText.Render(fmt.Sprintf("%d %s", d.commits, plural("commit", d.commits)))
		if len(d.bumps) > 0 {
			line += sDim.Render(" · ") + sWhite.Render(strings.Join(d.bumps, ", "))
		}
		p.add(line)
	}
}

// callDots are the results of the newest n calls, oldest first.
func callDots(a *App, calls []sharedCall, n int) string {
	kinds := callKinds(calls)
	kinds = kinds[max(len(kinds)-n, 0):]
	var b strings.Builder
	for _, k := range kinds {
		b.WriteString(fg(k.Color()).Render(barCells(1)))
	}
	return b.String() + strings.Repeat(" ", n-len(kinds))
}

func orDash(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return dur(d)
}
