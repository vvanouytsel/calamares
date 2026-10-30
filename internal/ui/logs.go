package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

type dline struct {
	header bool
	step   int // index into log.steps
	line   int // index into log.steps[step].Lines
}

type logView struct {
	id       int
	repo     string
	run      gh.Run
	poll     backoff
	attempt  int // the attempt the job belongs to; 0 is the latest
	jobsFP   string
	jobs     []gh.Job
	job      *gh.Job
	stepNum  int
	log      *jobLog
	expanded map[int]bool
	disp     []dline
	lineNo   [][]int // global line numbers per step/line
	cursor   int
	offset   int
	showTime bool
	wrap     bool
	search   string
	matches  [][2]int
	busy     bool
	fetching bool
	last     time.Time
	err      error
	lastH    int
}

type logJobsMsg struct {
	id   int
	run  *gh.Run
	jobs []gh.Job
	err  error
}

func (m logJobsMsg) viewID() int { return m.id }

type logRunMsg struct {
	id  int
	run gh.Run
}

func (m logRunMsg) viewID() int { return m.id }

func (v *logView) loadRun(a *App) tea.Cmd {
	id, repo, runID := v.id, v.repo, v.run.ID
	return func() tea.Msg {
		run, err := a.client.GetRun(gh.Background(context.Background()), repo, runID)
		if err != nil {
			return nil
		}
		return logRunMsg{id, run}
	}
}

// newLogView opens the log of job (or, when nil, the most interesting job of run),
// expanded at step stepNum when non-zero.
func newLogView(a *App, repo string, run gh.Run, job *gh.Job, stepNum int) *logView {
	v := &logView{id: a.newID(), repo: repo, run: run, stepNum: stepNum, wrap: true, expanded: map[int]bool{},
		poll: newBackoff(logPollEvery, maxLiveEvery)}
	if job != nil {
		j := *job
		v.job = &j
	}
	return v
}

func (v *logView) ID() int          { return v.id }
func (v *logView) RepoName() string { return v.repo }
func (v *logView) Crumb() string {
	if v.job != nil {
		return v.job.Name
	}
	return "logs"
}

// Init fetches the log straight away when the job is known (often it's
// already cached from the run view), in parallel with the job list.
func (v *logView) Init(a *App) tea.Cmd {
	if jobs, ok := a.client.CachedJobs(v.repo, v.run.ID); ok {
		v.jobs = jobs
	}
	cmds := []tea.Cmd{v.loadJobs(a, context.Background(), true)}
	if v.job != nil && v.job.Status == "completed" {
		v.fetching = true
		cmds = append(cmds, a.fetchJobLog(v.id, v.repo, *v.job))
	}
	return tea.Batch(cmds...)
}

// logPollEvery is how often a running job is checked for having finished.
const logPollEvery = 10 * time.Second

// loadJobs refreshes the jobs, to see the job's progress, and with withRun
// the run too, so rerun and cancel act on its real state.
func (v *logView) loadJobs(a *App, ctx context.Context, withRun bool) tea.Cmd {
	v.busy = true
	v.last = time.Now()
	id, repo, runID, attempt := v.id, v.repo, v.run.ID, v.attempt
	return func() tea.Msg {
		jobs, err := a.client.ListAttemptJobs(ctx, repo, runID, attempt)
		msg := logJobsMsg{id: id, jobs: jobs, err: err}
		if withRun {
			if run, rerr := a.client.GetRun(ctx, repo, runID); rerr == nil {
				msg.run = &run
			}
		}
		return msg
	}
}

// Refresh polls while the job runs; its log can only be read once it ends.
func (v *logView) Refresh(a *App, force bool) tea.Cmd {
	if v.busy {
		return nil
	}
	running := v.job != nil && v.job.Status != "completed"
	if force || (running && time.Since(v.last) > v.poll.every()) {
		if force && v.job != nil {
			delete(a.logs, v.job.ID)
			v.log = nil
		}
		return v.loadJobs(a, gh.Background(context.Background()), force)
	}
	return nil
}

// Following reports whether the job is still running, so the screen follows it.
func (v *logView) Following() bool { return v.job != nil && v.job.Status != "completed" }

func pickJob(jobs []gh.Job) *gh.Job {
	for _, k := range []Kind{KFail, KRun} {
		for i := range jobs {
			if jobKind(jobs[i]) == k {
				return &jobs[i]
			}
		}
	}
	if len(jobs) > 0 {
		return &jobs[0]
	}
	return nil
}

func (v *logView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logJobsMsg:
		v.busy = false
		if msg.run != nil {
			v.run = *msg.run
		}
		if msg.err != nil {
			v.err = msg.err
			return nil
		}
		fp := jobsFingerprint(msg.jobs)
		v.poll.seen(fp != v.jobsFP)
		v.jobsFP = fp
		wasRunning := v.job != nil && v.job.Status != "completed"
		v.jobs = msg.jobs
		if v.job == nil {
			v.job = pickJob(v.jobs)
		} else {
			for i := range v.jobs {
				if v.jobs[i].ID == v.job.ID {
					v.job = &v.jobs[i]
				}
			}
		}
		var cmds []tea.Cmd
		if v.job != nil && v.job.Status == "completed" {
			if wasRunning && msg.run == nil {
				// The job just ended: rerun and cancel need the run's new state.
				cmds = append(cmds, v.loadRun(a))
			}
			if v.log == nil && !v.fetching {
				v.fetching = true
				cmds = append(cmds, a.fetchJobLog(v.id, v.repo, *v.job))
			}
		}
		return tea.Batch(cmds...)
	case logRunMsg:
		v.run = msg.run
		return nil
	case jobLogMsg:
		if v.job == nil || msg.jobID != v.job.ID {
			return nil
		}
		v.fetching = false
		a.storeLog(msg.jobID, msg.log, v.job)
		v.log = msg.log
		v.initFolds()
		v.rebuild()
		v.placeCursor()
		if v.search != "" {
			v.SetFilter(a, v.search)
		}
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

func (v *logView) initFolds() {
	v.expanded = map[int]bool{}
	if v.log == nil {
		return
	}
	any := false
	for i, s := range v.log.steps {
		if s.Step.Number == v.stepNum || stepKind(s.Step) == KFail {
			v.expanded[i] = true
			any = true
		}
	}
	if !any {
		for i := range v.log.steps {
			v.expanded[i] = true
		}
	}
	v.lineNo = make([][]int, len(v.log.steps))
	n := 0
	for i, s := range v.log.steps {
		v.lineNo[i] = make([]int, len(s.Lines))
		for j := range s.Lines {
			n++
			v.lineNo[i][j] = n
		}
	}
}

func (v *logView) rebuild() {
	v.disp = v.disp[:0]
	if v.log == nil {
		return
	}
	for i, s := range v.log.steps {
		v.disp = append(v.disp, dline{header: true, step: i})
		if !v.expanded[i] {
			continue
		}
		for j, l := range s.Lines {
			if l.Kind == gh.LineEndGroup {
				continue
			}
			v.disp = append(v.disp, dline{step: i, line: j})
		}
	}
	v.cursor = min(v.cursor, max(len(v.disp)-1, 0))
}

// placeCursor puts the cursor on the first error of the chosen step (or its header).
func (v *logView) placeCursor() {
	if len(v.disp) == 0 {
		return
	}
	for i, d := range v.disp {
		if d.header && v.log.steps[d.step].Step.Number == v.stepNum {
			v.cursor = i
			break
		}
	}
	if v.stepNum == 0 || stepKind(v.log.steps[v.disp[v.cursor].step].Step) == KFail {
		v.jumpError(1, true)
	}
	v.offset = max(v.cursor-3, 0)
}

func (v *logView) pos() (int, int) {
	if v.cursor >= len(v.disp) {
		return 0, -1
	}
	d := v.disp[v.cursor]
	if d.header {
		return d.step, -1
	}
	return d.step, d.line
}

// goTo expands step s and puts the cursor on line l of it.
func (v *logView) goTo(s, l int) {
	if !v.expanded[s] {
		v.expanded[s] = true
		v.rebuild()
	}
	for i, d := range v.disp {
		if d.step == s && !d.header && d.line == l {
			v.cursor = i
			return
		}
	}
}

func less(s1, l1, s2, l2 int) bool { return s1 < s2 || (s1 == s2 && l1 < l2) }

func (v *logView) jumpError(dir int, inclusive bool) bool {
	if v.log == nil {
		return false
	}
	cs, cl := v.pos()
	var cand [][2]int
	for si, lines := range v.log.errors {
		for _, li := range lines {
			cand = append(cand, [2]int{si, li})
		}
	}
	return v.jumpTo(cand, cs, cl, dir, inclusive)
}

func (v *logView) jumpTo(cand [][2]int, cs, cl, dir int, inclusive bool) bool {
	if len(cand) == 0 {
		return false
	}
	if dir > 0 {
		for _, c := range cand {
			if less(cs, cl, c[0], c[1]) || (inclusive && cs == c[0] && cl == c[1]) {
				v.goTo(c[0], c[1])
				return true
			}
		}
		v.goTo(cand[0][0], cand[0][1])
		return true
	}
	for i := len(cand) - 1; i >= 0; i-- {
		c := cand[i]
		if less(c[0], c[1], cs, cl) {
			v.goTo(c[0], c[1])
			return true
		}
	}
	last := cand[len(cand)-1]
	v.goTo(last[0], last[1])
	return true
}

// SetFilter is "/" in the log view: it searches instead of filtering.
func (v *logView) SetFilter(a *App, q string) {
	v.search = q
	v.matches = nil
	if v.log == nil || q == "" {
		return
	}
	lq := strings.ToLower(q)
	for si, s := range v.log.steps {
		for li, l := range s.Lines {
			if strings.Contains(strings.ToLower(ansi.Strip(l.Text)), lq) {
				v.matches = append(v.matches, [2]int{si, li})
			}
		}
	}
	cs, cl := v.pos()
	v.jumpTo(v.matches, cs, cl, 1, true)
}

func (v *logView) FilterValue() string { return v.search }

// FilterInfo counts the matches of the search among the log's lines.
func (v *logView) FilterInfo(a *App) filterInfo {
	info := filterInfo{shown: len(v.matches)}
	if v.log != nil {
		info.total = v.log.count
	}
	return info
}

func (v *logView) matchIndex() int {
	cs, cl := v.pos()
	for i, m := range v.matches {
		if m[0] == cs && m[1] == cl {
			return i + 1
		}
	}
	return 0
}

func (v *logView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *logView) Keys(a *App) []Binding {
	move := moveBinding("", func(k string) tea.Cmd { v.move(k); return nil })
	move.Keys = append(append([]string{}, move.Keys...), "ctrl+u", "ctrl+d")
	bs := []Binding{
		{Label: "/", Desc: "search"},
		{Keys: []string{"n", "N"}, Label: "n/N", Desc: "next/prev match", Help: "next / previous match of the search", Do: func(k string) tea.Cmd {
			if v.search == "" {
				return a.flashCmd(nHint, "Search first with /")
			}
			cs, cl := v.pos()
			dir := 1
			if k == "N" {
				dir = -1
			}
			if !v.jumpTo(v.matches, cs, cl, dir, false) {
				return a.flashCmd(nHint, "No matches for "+v.search)
			}
			return nil
		}},
		{Keys: []string{"e", "E"}, Label: "e/E", Desc: "next/prev error", Help: "next / previous error line", Do: func(k string) tea.Cmd {
			if k == "E" {
				v.jumpError(-1, false)
			} else if !v.jumpError(1, false) {
				return a.flashCmd(nHint, "No error lines in this log.")
			}
			return nil
		}},
		{Keys: []string{" ", "enter", "left", "right"}, Label: "space", Desc: "fold step", Help: "fold / unfold the step",
			Do: func(k string) tea.Cmd { v.fold(k); return nil }},
		{Keys: []string{"Z"}, Desc: "fold all", Help: "fold / unfold every step", Do: do(func() tea.Cmd { v.foldAll(); return nil })},
		{Keys: []string{"t"}, Desc: "timestamps", Do: do(func() tea.Cmd { v.showTime = !v.showTime; return nil })},
		{Keys: []string{"W"}, Desc: "wrap", Help: "wrap long lines", Do: do(func() tea.Cmd { v.wrap = !v.wrap; return nil })},
		{Keys: []string{"[", "]"}, Label: "[ ]", Desc: "prev/next job", Help: "the log of the previous / next job",
			Do: func(k string) tea.Cmd { return v.switchJob(a, map[string]int{"]": 1, "[": -1}[k]) }},
		{Keys: []string{"y"}, Desc: "copy line", Do: do(func() tea.Cmd {
			if l := v.currentLine(); l != nil {
				return a.copyText(ansi.Strip(l.Text), "log line")
			}
			return nil
		})},
		{Keys: []string{"S"}, Desc: "save to file", Help: "save the log to Downloads", Do: do(func() tea.Cmd { return v.save(a) })},
		{Keys: []string{"o"}, Desc: "browser", Help: "open the job in the browser", Do: do(func() tea.Cmd {
			if v.job != nil {
				return a.openURL(v.job.HTMLURL)
			}
			return nil
		})},
	}
	bs = grouped("A LOG", bs...)
	bs = append(bs, only(a.runKeys(v.repo, v.run), "r", "R", "D", "c", "w", "a", "#")...)
	bs = append(bs, grouped("A LOG", a.runRepoKey(v.repo, v.run))...)
	return append(bs, move)
}

// move moves the cursor through the log.
func (v *logView) move(k string) {
	page := max(v.lastH-3, 1)
	last := max(len(v.disp)-1, 0)
	switch k {
	case "up", "k":
		v.cursor = max(v.cursor-1, 0)
	case "down", "j":
		v.cursor = min(v.cursor+1, last)
	case "pgup", "ctrl+b", "ctrl+u":
		v.cursor = max(v.cursor-page, 0)
	case "pgdown", "ctrl+f", "ctrl+d":
		v.cursor = min(v.cursor+page, last)
	case "home", "g":
		v.cursor = 0
	case "end", "G":
		v.cursor = last
	}
}

// fold folds or unfolds the step under the cursor: → opens it, ← closes it,
// space and enter switch.
func (v *logView) fold(k string) {
	if v.cursor >= len(v.disp) {
		return
	}
	s := v.disp[v.cursor].step
	switch k {
	case "left":
		v.expanded[s] = false
	case "right":
		v.expanded[s] = true
	default:
		v.expanded[s] = !v.expanded[s]
	}
	v.rebuild()
	for i, d := range v.disp {
		if d.header && d.step == s {
			v.cursor = i
		}
	}
}

// foldAll folds every step, or unfolds them all when they are folded.
func (v *logView) foldAll() {
	if v.log == nil {
		return
	}
	all := true
	for i := range v.log.steps {
		if !v.expanded[i] {
			all = false
		}
	}
	for i := range v.log.steps {
		v.expanded[i] = !all
	}
	v.rebuild()
}

func (v *logView) switchJob(a *App, d int) tea.Cmd {
	if len(v.jobs) < 2 || v.job == nil {
		return nil
	}
	idx := 0
	for i := range v.jobs {
		if v.jobs[i].ID == v.job.ID {
			idx = i
		}
	}
	idx = (idx + d + len(v.jobs)) % len(v.jobs)
	v.job = &v.jobs[idx]
	v.stepNum = 0
	v.log = nil
	v.disp = nil
	v.cursor, v.offset = 0, 0
	v.fetching = false
	if v.job.Status == "completed" {
		v.fetching = true
		return a.fetchJobLog(v.id, v.repo, *v.job)
	}
	return nil
}

func (v *logView) currentLine() *gh.LogLine {
	s, l := v.pos()
	if l < 0 || v.log == nil {
		return nil
	}
	return &v.log.steps[s].Lines[l]
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (v *logView) save(a *App) tea.Cmd {
	if v.log == nil || v.job == nil {
		return nil
	}
	name := fmt.Sprintf("%s-run%d-%s.log", unsafeName.ReplaceAllString(strings.ReplaceAll(v.repo, "/", "_"), "_"), v.run.RunNumber, strings.Trim(unsafeName.ReplaceAllString(v.job.Name, "_"), "_"))
	var b strings.Builder
	for _, s := range v.log.steps {
		fmt.Fprintf(&b, "===== %s =====\n", s.Step.Name)
		for _, l := range s.Lines {
			if !l.Time.IsZero() {
				b.WriteString(l.Time.Format(time.RFC3339) + " ")
			}
			b.WriteString(ansi.Strip(l.Text) + "\n")
		}
	}
	dir := downloadsDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return a.flashCmd(nError, "Could not save log: "+err.Error())
	}
	return a.flashCmd(nDone, "✓ Saved log to "+path)
}

// ---------- rendering ----------

func (v *logView) Render(a *App, w, h int) string {
	now := time.Now()
	jobName := "…"
	if v.job != nil {
		jobName = v.job.Name
	}
	count := -1
	if v.log != nil {
		count = v.log.count
	}
	extra := ""
	if len(v.jobs) > 1 {
		for i := range v.jobs {
			if v.job != nil && v.jobs[i].ID == v.job.ID {
				extra = fmt.Sprintf("job %d/%d", i+1, len(v.jobs))
			}
		}
	}
	t := title("Logs", jobName, count, extra)
	inner := h - 2
	border := cFrame
	if v.job != nil && jobKind(*v.job) == KFail {
		border = cFailFrame
	}
	var body string
	switch {
	case v.err != nil:
		body = " " + sRed.Render("Could not load jobs: "+v.err.Error())
	case v.job == nil:
		body = " " + sDim.Render("Loading…")
	case v.job.Status != "completed":
		k := jobKind(*v.job)
		lines := []string{" " + badge(k, a.frame, k.Label()+" for "+jobDur(*v.job, now)) + sDim.Render(" · the log appears here as soon as the job finishes"), ""}
		for _, s := range v.job.Steps {
			sk := stepKind(s)
			lines = append(lines, " "+glyph(sk, a.frame)+" "+fg(sk.RowColor()).Render(s.Name)+"  "+sDim.Render(stepDur(s, now)))
		}
		body = strings.Join(lines, "\n")
	case v.log == nil:
		body = " " + sDim.Render("Loading log…")
	case v.log.err != nil:
		body = " " + sRed.Render("Could not load log: "+v.log.err.Error())
	default:
		body = v.renderLines(a, w-2, inner-2, now) + "\n" + v.statusLine(w-2)
	}
	return box(t, body, w, h, border)
}

func (v *logView) rows(i, w int) int {
	if !v.wrap || v.disp[i].header {
		return 1
	}
	l := v.log.steps[v.disp[i].step].Lines[v.disp[i].line]
	tw := width(ansi.Strip(l.Text))
	if v.showTime {
		tw += 9
	}
	avail := max(w-7, 10)
	return max(1, (tw+avail-1)/avail)
}

func (v *logView) renderLines(a *App, w, h int, now time.Time) string {
	v.lastH = h
	if len(v.disp) == 0 {
		return ""
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	// Walk back from the cursor until the screen is full: O(h), not O(n²).
	used := 0
	for i := min(v.cursor, len(v.disp)-1); i >= v.offset; i-- {
		used += v.rows(i, w)
		if used > h {
			v.offset = i + 1
			break
		}
	}
	var out []string
	lq := strings.ToLower(v.search)
	for i := v.offset; i < len(v.disp) && len(out) < h; i++ {
		d := v.disp[i]
		sel := i == v.cursor
		if d.header {
			s := v.log.steps[d.step]
			k := stepKind(s.Step)
			arrow := "▸"
			if v.expanded[d.step] {
				arrow = "▾"
			}
			dd := stepDur(s.Step, now)
			if k == KSkip {
				dd = "skipped"
			}
			left := fmt.Sprintf("%s %s %s", arrow, k.Glyph(a.frame), s.Step.Name)
			if !v.expanded[d.step] && len(s.Lines) > 0 {
				left += fmt.Sprintf("  (%d lines)", len(s.Lines))
			}
			if sel {
				out = append(out, sSel.Bold(true).Render(fit(fit(left, w-len(dd)-1)+" "+dd, w)))
				continue
			}
			nameStyle := sCyan.Bold(true)
			if k == KFail {
				nameStyle = sRed.Bold(true)
			} else if k == KSkip {
				nameStyle = sDim
			}
			styled := sDim.Render(arrow) + " " + glyph(k, a.frame) + " " + nameStyle.Render(s.Step.Name)
			if !v.expanded[d.step] && len(s.Lines) > 0 {
				styled += sDim.Render(fmt.Sprintf("  (%d lines)", len(s.Lines)))
			}
			out = append(out, fit(styled, w-len(dd)-1)+" "+sDim.Render(dd))
			continue
		}
		l := v.log.steps[d.step].Lines[d.line]
		gutter := fmt.Sprintf("%5d ", v.lineNo[d.step][d.line])
		cw := w - 6
		var text string
		plain := strings.ReplaceAll(ansi.Strip(l.Text), "\t", "    ")
		isMatch := lq != "" && strings.Contains(strings.ToLower(plain), lq)
		switch {
		case sel:
			p := plain
			if v.showTime && !l.Time.IsZero() {
				p = l.Time.Format("15:04:05") + " " + p
			}
			text = sSel.Render(p)
		case isMatch:
			text = highlightMatches(plain, v.search)
			if v.showTime && !l.Time.IsZero() {
				text = sDim.Render(l.Time.Format("15:04:05")+" ") + text
			}
		default:
			text = styleLogLine(l, 1<<20, v.showTime)
		}
		g := fg(cGutter).Render(gutter)
		if sel {
			g = sSel.Render(gutter)
		}
		if !v.wrap {
			line := g + clip(text, cw)
			if sel {
				line = fit(line, w)
				line = strings.TrimRight(line, " ") + sSel.Render(strings.Repeat(" ", max(w-width(line), 0)))
			}
			out = append(out, line)
			continue
		}
		parts := strings.Split(ansi.Hardwrap(text, cw, true), "\n")
		for pi, p := range parts {
			if len(out) >= h {
				break
			}
			prefix := g
			if pi > 0 {
				prefix = strings.Repeat(" ", 6)
				if sel {
					prefix = sSel.Render(prefix)
				}
			}
			line := prefix + p
			if sel {
				line += sSel.Render(strings.Repeat(" ", max(w-width(line), 0)))
			}
			out = append(out, line)
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func highlightMatches(s, q string) string {
	if q == "" {
		return s
	}
	ls, lq := strings.ToLower(s), strings.ToLower(q)
	hl := lipgloss.NewStyle().Background(cMatchBg).Foreground(cWhite).Bold(true)
	var b strings.Builder
	for {
		i := strings.Index(ls, lq)
		if i < 0 {
			b.WriteString(sText.Render(s))
			break
		}
		b.WriteString(sText.Render(s[:i]) + hl.Render(s[i:i+len(q)]))
		s, ls = s[i+len(q):], ls[i+len(q):]
	}
	return b.String()
}

func (v *logView) statusLine(w int) string {
	var parts []string
	if v.search != "" {
		parts = append(parts, sWhite.Render("/"+v.search), sDim.Render(fmt.Sprintf("match %d/%d", v.matchIndex(), len(v.matches))))
	}
	s, l := v.pos()
	if l >= 0 {
		parts = append(parts, sDim.Render(fmt.Sprintf("line %s/%s", thousands(v.lineNo[s][l]), thousands(v.log.count))))
	}
	onoff := func(b bool) string {
		if b {
			return sGreen.Render("on")
		}
		return sDim.Render("off")
	}
	right := sDim.Render("t timestamps ") + onoff(v.showTime) + sDim.Render("   W wrap ") + onoff(v.wrap)
	if len(v.jobs) > 1 {
		right += sDim.Render("   [ ] switch job")
	}
	left := strings.Join(parts, sDim.Render("  ·  "))
	pad := w - width(left) - width(right) - 2
	return sDim.Render(strings.Repeat("─", w)) + "\n" + " " + left + strings.Repeat(" ", max(pad, 1)) + right
}
