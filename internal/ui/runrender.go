package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// How a run is drawn: two summary lines, the job tree on the left and what
// is selected in it on the right.

func (v *runView) Render(a *App, w, h int) string {
	now := time.Now()
	v.byPrevious = a.byPrevious()
	if !a.cfg.Comparing() {
		v.typical, v.previous, v.runTypical, v.typicalLoad = nil, nil, gh.TypicalJob{}, typicalIdle
	}
	if !v.loaded {
		msg := "Loading run…"
		if v.err != nil {
			msg = "Could not load run: " + v.err.Error()
		}
		return box(title("Run", v.repo, -1, ""), " "+sDim.Render(msg), w, h, cFrame)
	}
	shown := v.shownRun()
	k := StatusKind(shown.Status, shown.Conclusion)
	sha := v.run.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	trig := sDim.Render(" · " + v.run.Event)
	if prs := a.runPRNumbers(v.repo, v.run); len(prs) > 0 {
		trig = sDim.Render(" · ") + a.prMark(v.repo, prs[0]) + " " + keycap("#")
	}
	bar := fg(k.Color()).Render("▌")
	head := bar + " " + statusPill(k, a.frame, "") + "  "
	titleLines := wrapTitle(v.run.DisplayTitle, w-width(head), w-2, maxTitleLines)
	sum := head + sWhite.Render(titleLines[0])
	for _, l := range titleLines[1:] {
		sum += "\n" + bar + " " + sWhite.Render(l)
	}
	facts := bar + " " + ico("commit") + sTan.Render(sha) + "   " +
		fact("user", v.run.Actor.Login) + trig + "   "
	if label := v.attemptLabel(); label != "" {
		facts += label + "   "
	}
	facts += v.runTook(a, shown, now) + "   " + fact("clock", age(v.run.CreatedAt, now)+" ago")
	if q := v.queued(now); q >= queueWorthSaying {
		facts += "   " + ico("clock") + sAmber.Render("queued "+dur(q))
	}
	if called := calledWorkflows(a, v.run); called != "" {
		facts += "   " + called
	}
	if v.follow {
		facts += sAmber.Bold(true).Render("   ▶ following")
	}
	if chip := v.typicalChip(a); chip != "" {
		facts += sDim.Render("  ·  ") + chip
	}
	if c := a.roleChip(v.repo); c != "" {
		facts += "  " + c
	}
	lines := strings.Split(sum, "\n")
	for i := range lines {
		lines[i] = fit(lines[i], w)
	}
	sum = strings.Join(lines, "\n") + "\n" + fit(facts, w)

	ph := h - len(lines) - 1
	if v.graph {
		return sum + "\n" + v.renderGraph(a, w, ph, now)
	}
	lw := min(max(w*2/5, 34), 64)
	if a.cfg.Comparing() {
		lw = min(max(lw, typicalTreeWidth), max(w-30, 34))
	}
	v.treeW = lw
	rw := w - lw
	scope := fmt.Sprintf("#%d", v.run.RunNumber)
	if p := strings.TrimSuffix(v.jobPrefix(), " / "); p != "" {
		scope += " · " + p
	}
	arts := v.artifactLines(lw - 2)
	whole := v.wholeRunRows(a, lw-2, now)
	tree := v.renderTree(a, lw-2, ph-2-v.headerRows(a)-len(arts)-len(whole), now)
	if header := v.treeHeader(a, lw-2); header != "" {
		tree = header + "\n" + tree
	}
	if len(whole) > 0 {
		tree += "\n" + strings.Join(whole, "\n")
	}
	if len(arts) > 0 {
		tree += "\n" + strings.Join(arts, "\n")
	}
	left := strings.Join(block(" "+title("Jobs", scope, len(v.jobs), "")+"\n"+tree, lw, ph), "\n")
	rt, rb, border := v.renderDetail(a, rw-2, ph-2, now)
	right := box(rt, rb, rw, ph, border)
	return sum + "\n" + hjoin(left, right)
}

// maxTitleLines is how many lines a long commit title may take at the top of a run.
const maxTitleLines = 3

// queueWorthSaying is how long a job must have waited for a runner before
// the run says so.
const queueWorthSaying = time.Minute

// wrapTitle breaks a title into lines: the first first cells wide, the rest
// w wide, at most n lines, the last one cut.
func wrapTitle(title string, first, w, n int) []string {
	if width(title) <= first || first < 10 {
		return []string{title}
	}
	words := strings.Fields(title)
	var out []string
	line, room := "", first
	for _, word := range words {
		switch {
		case line == "":
			line = word
		case width(line)+1+width(word) <= room:
			line += " " + word
		default:
			out = append(out, line)
			line, room = word, w
		}
	}
	out = append(out, line)
	if len(out) > n {
		out = append(out[:n-1], clip(strings.Join(out[n-1:], " "), w))
	}
	return out
}

// queued is how long the run waited for its first job to start, or so far
// when none has.
func (v *runView) queued(now time.Time) time.Duration {
	var first time.Time
	for _, j := range v.jobs {
		if !j.StartedAt.IsZero() && (first.IsZero() || j.StartedAt.Before(first)) {
			first = j.StartedAt
		}
	}
	created := v.shownRun().RunStartedAt
	if created.IsZero() {
		created = v.run.CreatedAt
	}
	switch {
	case created.IsZero():
		return 0
	case first.IsZero() && v.run.Active() && v.jobsLoaded:
		return now.Sub(created)
	case first.IsZero() || first.Before(created):
		return 0
	}
	return first.Sub(created)
}

// calledWorkflows names the reusable workflows a run called, with the version.
func calledWorkflows(a *App, run gh.Run) string {
	var names []string
	seen := map[string]bool{}
	for _, rw := range run.ReferencedWorkflows {
		target, version, _ := strings.Cut(rw.Path, "@")
		name := pathBase(target)
		if seen[name+version] {
			continue
		}
		seen[name+version] = true
		names = append(names, sText.Render(name)+fg(cRef).Render("@"+shortRef(version)))
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) > 2 {
		names = append(names[:2], sDim.Render(fmt.Sprintf("+%d", len(names)-2)))
	}
	return ico("workflow") + sDim.Render("calls ") + strings.Join(names, sDim.Render(", "))
}

// foldGlyph marks a job or group that opens to show more: + closed, − open.
func foldGlyph(open bool) string {
	switch {
	case ascii && open:
		return "- "
	case open:
		return "− "
	}
	return "+ "
}

// jobPrefix is the start shared by every job name, up to a " / ", as in
// "build / Test" and "build / Deploy" when they come from one reusable
// workflow. The tree leaves it out and the title shows it once.
func (v *runView) jobPrefix() string {
	if len(v.jobs) < 2 {
		return ""
	}
	prefix := v.jobs[0].Name
	for _, j := range v.jobs[1:] {
		for !strings.HasPrefix(j.Name, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	if i := strings.LastIndex(prefix, " / "); i >= 0 {
		return prefix[:i+3]
	}
	return ""
}

func (v *runView) renderTree(a *App, w, h int, now time.Time) string {
	prefix := v.jobPrefix()
	if len(v.items) == 0 {
		if !v.jobsLoaded {
			return " " + sDim.Render("Loading jobs…")
		}
		if v.run.Active() {
			return " " + sDim.Render("Waiting for jobs to start…")
		}
		return " " + sDim.Render("This run has no jobs.")
	}
	_, members := v.groups()
	var lines []string
	at := map[int]int{}
	var card *panelCard
	var head jobHead
	flush := func() {
		switch {
		case card == nil:
		case len(card.lines) == 0:
			lines = append(lines, jobPill(head, card, w-1))
		default:
			card.title = head.title(card, w-1)
			lines = append(lines, cardLines(card, w-1)...)
		}
		card = nil
	}
	for i := range v.items {
		left, right, rightWidth, k, depth := v.treeParts(a, i, prefix, members, now)
		if depth == 0 {
			flush()
			card, head = jobCard(k), jobHead{left, right, rightWidth}
			if i == v.cursor {
				card.fill = lipgloss.Color(mix(string(cBg), string(card.border), 0.15))
			}
			at[i] = len(lines)
			continue
		}
		line := fit(left, w-1-cardInset-rightWidth-1) + " " + right
		if i == v.cursor {
			line = withSelection(line, false)
		}
		at[i] = len(lines) + 1 + len(card.lines)
		card.lines = append(card.lines, line)
	}
	flush()
	cur := at[v.cursor]
	v.offset = min(max(v.offset, cur-h+2), cur)
	v.offset = max(min(v.offset, len(lines)-h), 0)
	return strings.Join(lines[v.offset:min(len(lines), v.offset+h)], "\n")
}

// jobHead is what a job's card says of it: what it is, and its time.
type jobHead struct {
	left, right string
	rightWidth  int
}

// title is the top edge of the job's card w cells wide: the job at the left,
// its time at the right, the edge running between them.
func (h jobHead) title(c *panelCard, w int) string {
	room := w - 6
	left := clip(h.left, max(room-h.rightWidth-3, 4))
	return left + " " + fg(c.border).Render(strings.Repeat(cardStroke(c), max(room-width(left)-h.rightWidth-2, 1))) + " " + h.right
}

// jobPill is a job that is folded, on one line: a pill w cells wide in the
// colour of how it went, brighter while the cursor is on it.
func jobPill(h jobHead, c *panelCard, w int) string {
	bg := lipgloss.Color(mix(string(cBg), string(c.border), 0.1))
	if c.fill != "" {
		bg = c.fill
	}
	inner := w - 3
	left := clip(h.left, max(inner-h.rightWidth-2, 4))
	body := withBg(" "+fit(left, inner-h.rightWidth-1)+" "+h.right, bg)
	if !pillsOn() {
		return " " + withBg(" ", bg) + body + withBg(" ", bg)
	}
	return " " + fg(bg).Render(pillLeft) + body + fg(bg).Render(pillRight)
}

// jobCard is the card of a job, as the pipeline draws it: the border in the
// colour of how the job went, dashed for one that did not run.
func jobCard(k Kind) *panelCard {
	c := &panelCard{border: k.Color()}
	switch k {
	case KSkip, KCancel, KNone:
		c.border, c.dashed = cDim, true
	case KQueue:
		c.border = cGrey
	}
	return c
}

// treeParts are the parts of line i of the job tree: what it is at the left,
// its time (or the comparison with typical runs) at the right and how wide
// that is, how it went, and how deep it sits under a job.
func (v *runView) treeParts(a *App, i int, prefix string, members map[string][]int, now time.Time) (left, right string, rightWidth int, k Kind, depth int) {
	it := v.items[i]
	indent := strings.Repeat("  ", max(it.depth-1, 0))
	var name, d, arrow, note string
	noteColor := cDim
	var typical string
	switch it.kind {
	case tGroup:
		var kinds []Kind
		failed := 0
		for _, ji := range members[it.group] {
			jk := jobKind(v.jobs[ji])
			kinds = append(kinds, jk)
			if jk == KFail {
				failed++
			}
		}
		k = aggregate(kinds)
		name = fmt.Sprintf("%s · matrix (%d)", strings.TrimPrefix(it.group, prefix), len(members[it.group]))
		if failed > 0 {
			name += fmt.Sprintf(" %d failed", failed)
		}
		arrow = foldGlyph(v.expanded["g:"+it.group])
	case tJob:
		j := v.jobs[it.job]
		k = jobKind(j)
		name = strings.TrimPrefix(j.Name, prefix)
		if it.depth > 0 {
			if i := strings.Index(name, " ("); i > 0 {
				name = strings.TrimSuffix(name[i+2:], ")")
			}
		}
		d = jobDur(j, now)
		note, noteColor = v.jobNote(a, j, now)
		t, _ := gh.TypicalFor(v.typical, j.Name)
		if b := v.base(a, t); b > 0 {
			typical = dur(b)
		}
		arrow = foldGlyph(v.expanded[fmt.Sprintf("j:%d", j.ID)])
	case tStep:
		s := v.jobs[it.job].Steps[it.step]
		k = stepKind(s)
		name = s.Name
		d = stepDur(s, now)
		if k == KSkip {
			d = "skip"
		}
		typical, note, noteColor = v.stepTypical(a, v.jobs[it.job], it.step, now)
		arrow = "  "
	}
	nameStyle := sRow
	switch k {
	case KFail:
		nameStyle = sRed
	case KRun:
		nameStyle = sAmber
	case KSkip, KCancel, KNone:
		nameStyle = sDim
	case KQueue:
		nameStyle = fg(cGrey)
	case KWait:
		nameStyle = sWait
	}
	left = sDim.Render(indent+arrow) + glyph(k, a.frame) + " " + nameStyle.Render(name)
	if it.kind == tJob {
		if q := v.jobs[it.job].Queued(now); q >= queueWorthSaying {
			left += sDim.Render(" · queued ") + queueText(q)
		}
	}
	right, rightWidth = sDim.Render(d), runeLen(d)
	if a.cfg.Comparing() {
		right, rightWidth = typicalColumns(d, typical, note, noteColor), typicalColumnsWidth
	}
	return left, right, rightWidth, k, it.depth
}

func (v *runView) renderDetail(a *App, w, h int, now time.Time) (string, string, lipgloss.Color) {
	it, ok := v.selected()
	if !ok {
		return title("Details", "", -1, ""), strings.Join(v.concurrencyBox(w), "\n"), cFrame
	}
	if it.kind == tGroup {
		return v.groupDetail(a, it.group, w, now)
	}
	job := v.jobs[it.job]
	if job.Status != "completed" {
		return v.liveDetail(a, job, w, h, now)
	}
	l := a.logs[job.ID]
	var t string
	if it.kind == tStep {
		t = title("Log", job.Name+" › "+job.Steps[it.step].Name, -1, "")
	} else {
		t = title("Job", job.Name, -1, "")
	}
	out := v.concurrencyBox(w)
	why := v.whyBox(a, job, w)
	if why != "" {
		out = append(out, strings.Split(why, "\n")...)
	}
	switch {
	case l == nil:
		out = append(out, " "+sDim.Render("Loading log…"))
	case l.err != nil:
		if gh.IsNotFound(l.err) {
			out = append(out, " "+sDim.Render("The log for this job is no longer available (expired or deleted)."))
		} else {
			out = append(out, " "+sRed.Render("Could not load log: "+l.err.Error()))
		}
	case it.kind == tStep:
		si := l.stepIndex(job.Steps[it.step].Number)
		if si < 0 || len(l.steps[si].Lines) == 0 {
			out = append(out, " "+sDim.Render("This step didn't log anything."))
			break
		}
		room := h - len(out) - 1
		sl := l.steps[si]
		start := max(len(sl.Lines)-room, 0)
		if stepKind(sl.Step) == KFail {
			if fe := l.firstError(si); fe >= 0 {
				start = min(max(fe-3, 0), start)
			}
		}
		out = append(out, v.logLines(sl.Lines, start, room, w)...)
	default:
		out = append(out, v.jobSummary(a, job, w, now)...)
	}
	if h < 2 {
		return t, strings.Join(out, "\n"), cFrame
	}
	border := cFrame
	if jobKind(job) == KFail {
		border = cFailFrame
	}
	return t, strings.Join(out, "\n"), border
}

func (v *runView) logLines(lines []gh.LogLine, start, n, w int) []string {
	var out []string
	for i := start; i < len(lines) && len(out) < n; i++ {
		if lines[i].Kind == gh.LineEndGroup {
			continue
		}
		out = append(out, " "+styleLogLine(lines[i], w-1, false))
	}
	return out
}

// whyBox summarises why a failed job failed: annotations, error lines and matrix context.
func (v *runView) whyBox(a *App, job gh.Job, w int) string {
	if jobKind(job) != KFail {
		return ""
	}
	var lines []string
	for _, an := range v.annotations[job.ID] {
		if an.AnnotationLevel != "failure" || len(lines) >= 3 || gh.IsGenericFailure(gh.LogLine{Text: an.Message}) {
			continue
		}
		msg := strings.SplitN(strings.TrimSpace(an.Message), "\n", 2)[0]
		loc := ""
		if an.Path != "" && !strings.HasPrefix(an.Path, ".github") {
			loc = sCyan.Render(fmt.Sprintf("%s:%d", an.Path, an.StartLine)) + "  "
		}
		lines = append(lines, loc+sRed.Render(msg))
	}
	if l := a.logs[job.ID]; l != nil && l.err == nil {
		for si, s := range l.steps {
			if stepKind(s.Step) != KFail {
				continue
			}
			for _, el := range l.errorLines(si, 4) {
				if len(lines) >= 5 {
					break
				}
				txt := strings.TrimSpace(el.Text)
				dup := false
				for _, x := range lines {
					if strings.Contains(x, txt) {
						dup = true
					}
				}
				if !dup {
					lines = append(lines, sWhite.Render(txt))
				}
			}
			lines = append([]string{sDim.Render("in step ") + sText.Render(s.Step.Name)}, lines...)
			break
		}
	}
	// matrix context
	_, members := v.groups()
	if legs := members[groupName(job.Name)]; len(legs) > 1 {
		var ok, failed []string
		for _, i := range legs {
			name := v.jobs[i].Name
			if p := strings.Index(name, " ("); p > 0 {
				name = strings.TrimSuffix(name[p+2:], ")")
			}
			switch jobKind(v.jobs[i]) {
			case KOK:
				ok = append(ok, name)
			case KFail:
				failed = append(failed, name)
			}
		}
		ctx := ""
		if len(ok) > 0 {
			ctx = sDim.Render("Passed on ") + sGreen.Render(strings.Join(ok, ", "))
		}
		if len(failed) > 1 {
			if ctx != "" {
				ctx += sDim.Render(" · ")
			}
			ctx += sDim.Render("failed on ") + sRed.Render(strings.Join(failed, ", "))
		}
		if ctx != "" {
			lines = append(lines, ctx)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append(lines, keycap("d")+sDim.Render(" diff with the last run that passed"))
	body := make([]string, len(lines))
	for i, l := range lines {
		body[i] = " " + l
	}
	return boxLeft(sRed.Bold(true).Render("✗ Why it failed"), strings.Join(body, "\n"), w, len(body)+2, cFailFrame)
}

func (v *runView) jobSummary(a *App, job gh.Job, w int, now time.Time) []string {
	k := jobKind(job)
	head := " " + badge(k, a.frame, "") + sDim.Render("  ·  ") + sText.Render(jobDur(job, now))
	if q := job.Queued(now); q > 0 {
		head += sDim.Render("  ·  queued ") + queueText(q)
	}
	out := []string{head + sDim.Render("  ·  runner ") + sText.Render(job.RunnerName)}
	if line := v.jobLine(a, job, now); line != "" {
		out = append(out, " "+line)
	}
	if len(job.Labels) > 0 {
		out = append(out, " "+sDim.Render("labels: ")+sText.Render(strings.Join(job.Labels, ", ")))
	}
	out = append(out, "")
	out = append(out, v.stepsHeader(a, w)...)
	for i, s := range job.Steps {
		d := stepDur(s, now)
		if stepKind(s) == KSkip {
			d = "skipped"
		}
		out = append(out, v.stepLine(a, job, i, d, w, now))
	}
	return out
}

func (v *runView) liveDetail(a *App, job gh.Job, w, h int, now time.Time) (string, string, lipgloss.Color) {
	k := jobKind(job)
	var out []string
	switch k {
	case KQueue:
		waited := job.Queued(now)
		if waited == 0 {
			waited = now.Sub(v.run.CreatedAt)
		}
		out = append(out, " "+badge(k, a.frame, "queued")+sDim.Render(" · waiting for a runner ")+queueText(waited))
		if len(job.Labels) > 0 {
			out = append(out, " "+sDim.Render("labels: ")+sText.Render(strings.Join(job.Labels, ", ")))
		}
	case KWait:
		out = append(out, " "+badge(k, a.frame, "waiting")+sDim.Render(" · needs approval — press ")+sTan.Render("a"))
	default:
		out = append(out, " "+badge(k, a.frame, "running for "+jobDur(job, now))+sDim.Render(" · on "+job.RunnerName))
	}
	if line := v.jobLine(a, job, now); line != "" && k == KRun {
		out = append(out, " "+line)
	}
	out = append(out, " "+sDim.Render("The full log becomes available as soon as this job finishes."), "")
	out = append(out, v.stepsHeader(a, w)...)
	for i, s := range job.Steps {
		d := stepDur(s, now)
		if s.StartedAt.IsZero() {
			d = ""
		}
		out = append(out, v.stepLine(a, job, i, d, w, now))
	}
	return title("Live", job.Name, -1, ""), strings.Join(out, "\n"), cAmber
}

func (v *runView) groupDetail(a *App, group string, w int, now time.Time) (string, string, lipgloss.Color) {
	_, members := v.groups()
	var out []string
	out = append(out, " "+sDim.Render("Matrix legs of ")+sWhite.Render(group), "")
	border := cFrame
	for _, i := range members[group] {
		j := v.jobs[i]
		k := jobKind(j)
		if k == KFail {
			border = cFailFrame
		}
		out = append(out, " "+fit(glyph(k, a.frame)+" "+fg(k.RowColor()).Render(j.Name), w-12)+sDim.Render(fmt.Sprintf("%10s", jobDur(j, now))))
	}
	out = append(out, "", " "+sDim.Render("enter expands · l opens the log of the first failed leg"))
	return title("Matrix", group, len(members[group]), ""), strings.Join(out, "\n"), border
}

// queueText is a wait for a runner: amber once it is long enough to say so.
func queueText(d time.Duration) string {
	if d >= queueWorthSaying {
		return sAmber.Render(dur(d))
	}
	return sText.Render(dur(d))
}
