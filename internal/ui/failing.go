package ui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// defaultRunsFilter is the sample of runs on a repo's default branch the
// Failing view reads, besides the latest runs of all branches.
func defaultRunsFilter(branch string) gh.RunFilter { return gh.RunFilter{Branch: branch, PerPage: 20} }

// defaultBranch is the default branch of repo, or "" when it isn't known.
func (a *App) defaultBranch(repo string) string {
	if r, ok := a.repoByName(repo); ok {
		return r.DefaultBranch
	}
	return ""
}

type defaultRunsMsg struct {
	repo string
	runs []gh.Run
	err  error
}

func (s *store) defaultState(repo string) *loadState {
	l, ok := s.defaultsState[repo]
	if !ok {
		l = &loadState{}
		s.defaultsState[repo] = l
	}
	return l
}

// loadDefaults reads the runs on the default branch of repos, one request
// each, revalidated with ETags.
func (s *store) loadDefaults(a *App, ctx context.Context, repos []string) tea.Cmd {
	var cmds []tea.Cmd
	for _, repo := range repos {
		branch := a.defaultBranch(repo)
		if branch == "" || !s.defaultState(repo).begin() {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			runs, _, err := a.client.ListRuns(ctx, repo, defaultRunsFilter(branch))
			return defaultRunsMsg{repo, runs, err}
		})
	}
	return tea.Batch(cmds...)
}

// latestPerWorkflow is the newest finished run of each workflow on the
// default branch of repo that passed or failed; cancelled and skipped runs
// say nothing about how a workflow is doing.
func (a *App) latestPerWorkflow(repo string) []gh.Run {
	branch := a.defaultBranch(repo)
	if branch == "" {
		return nil
	}
	latest := map[int64]gh.Run{}
	for _, list := range [][]gh.Run{a.st.defaults[repo], a.st.status[repo]} {
		for _, r := range a.visibleRuns(repo, list) {
			if r.HeadBranch != branch {
				continue
			}
			if k := StatusKind(r.Status, r.Conclusion); k != KOK && k != KFail {
				continue
			}
			if old, ok := latest[r.WorkflowID]; !ok || r.CreatedAt.After(old.CreatedAt) || (r.CreatedAt.Equal(old.CreatedAt) && r.ID > old.ID) {
				latest[r.WorkflowID] = r
			}
		}
	}
	out := make([]gh.Run, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// workflowsOnDefault is one run of each workflow on the default branch of
// repo, sorted by workflow name: its newest unfinished run, or else its latest
// finished one that passed or failed.
func (a *App) workflowsOnDefault(repo string) []gh.Run {
	branch := a.defaultBranch(repo)
	if branch == "" {
		return nil
	}
	byWorkflow := map[int64]gh.Run{}
	for _, r := range a.latestPerWorkflow(repo) {
		byWorkflow[r.WorkflowID] = r
	}
	active := map[int64]gh.Run{}
	for _, list := range [][]gh.Run{a.st.defaults[repo], a.st.status[repo]} {
		for _, r := range a.visibleRuns(repo, list) {
			if r.HeadBranch != branch || !r.Active() {
				continue
			}
			if old, ok := active[r.WorkflowID]; !ok || r.CreatedAt.After(old.CreatedAt) || (r.CreatedAt.Equal(old.CreatedAt) && r.ID > old.ID) {
				active[r.WorkflowID] = r
			}
		}
	}
	for id, r := range active {
		byWorkflow[id] = r
	}
	out := make([]gh.Run, 0, len(byWorkflow))
	for _, r := range byWorkflow {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].WorkflowID < out[j].WorkflowID
	})
	return out
}

// failingRuns are the latest runs of each workflow on the default branches
// of repos that failed, newest first.
func failingRuns(a *App, repos []string) []repoRun {
	var out []repoRun
	for _, repo := range repos {
		for _, r := range a.latestPerWorkflow(repo) {
			if StatusKind(r.Status, r.Conclusion) == KFail {
				out = append(out, repoRun{repo, r})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].run.CreatedAt.After(out[j].run.CreatedAt) })
	return out
}

// failGroup is one cause and the failed runs it explains, newest first.
type failGroup struct {
	key   string
	cause runCause
	known bool // the cause was read; until then each run is a group of its own
	runs  []repoRun
}

func (g *failGroup) repos() int {
	seen := map[string]bool{}
	for _, x := range g.runs {
		seen[strings.ToLower(x.repo)] = true
	}
	return len(seen)
}

// oldest is when the earliest of the group's runs was started.
func (g *failGroup) oldest() time.Time {
	t := g.runs[0].run.CreatedAt
	for _, x := range g.runs {
		if x.run.CreatedAt.Before(t) {
			t = x.run.CreatedAt
		}
	}
	return t
}

// groupFailures puts failed runs with the same cause together, the groups
// with the most repos first.
func groupFailures(runs []repoRun, causes map[string]runCause) []*failGroup {
	byKey := map[string]*failGroup{}
	var out []*failGroup
	for _, x := range runs {
		c, known := causes[causeID(x.repo, x.run)]
		key := c.key()
		if !known || key == "" {
			key, known = "run\x00"+causeID(x.repo, x.run), false
		}
		g, ok := byKey[key]
		if !ok {
			g = &failGroup{key: key, cause: c, known: known}
			byKey[key] = g
			out = append(out, g)
		}
		g.runs = append(g.runs, x)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := out[i].repos(), out[j].repos(); ri != rj {
			return ri > rj
		}
		return out[i].runs[0].run.CreatedAt.After(out[j].runs[0].run.CreatedAt)
	})
	return out
}

// commonWorkflows are the reusable workflows, with their version, that every
// run of the group called.
func (g *failGroup) commonWorkflows() []string {
	count := map[string]int{}
	var order []string
	for _, x := range g.runs {
		seen := map[string]bool{}
		for _, rw := range x.run.ReferencedWorkflows {
			p := strings.ToLower(rw.Path)
			if seen[p] {
				continue
			}
			seen[p] = true
			if count[p] == 0 {
				order = append(order, rw.Path)
			}
			count[p]++
		}
	}
	var out []string
	for _, p := range order {
		if count[strings.ToLower(p)] == len(g.runs) {
			out = append(out, p)
		}
	}
	return out
}

// failingView is the Failing view of the Actions tab: the failed runs of the
// default branches, each on a line of its own or grouped by why they failed.
type failingView struct {
	t      *Table
	list   *Table // every run on a line of its own, when flat
	flat   bool
	groups []*failGroup
	runs   map[string]repoRun    // by row key: the run of a row of a run, or of a cause with one run
	group  map[string]*failGroup // by row key
	folded map[string]bool
	repos  func(a *App) []string
	// cardTop is the first line of the cards on display.
	cardTop int
}

func newFailingView(repos func(a *App) []string) *failingView {
	return &failingView{flat: true, folded: map[string]bool{}, repos: repos, t: newTable(
		Column{Title: "ERROR", Max: 80, Flex: true, Help: "the first line of the failed step that reads like an error; ids, hashes and numbers are left out, so one failure in several repos is one row"},
		Column{Title: "REPOS", Right: true, Help: "how many repos fail with this cause"},
		Column{Title: "STEP", Max: 28, Flex: true, Drop: 1, Help: "the step that failed, or how many different steps when its runs failed in different ones"},
		Column{Title: "SINCE", Right: true, Min: 5, Help: "how long ago the first of its runs failed"},
	), list: newTable(flatColumns("ERROR", true)...)}
}

// flatColumns are the columns of a list of runs with one more about each.
func flatColumns(what string, step bool) []Column {
	cols := []Column{
		{Title: "REPO", Max: 30, Flex: true, MidCut: true},
		{Title: "WORKFLOW", Max: 26, Flex: true, Drop: 2},
		{Title: "RUN", Right: true},
		{Title: what, Max: 70, Flex: true},
	}
	if step {
		cols = append(cols, Column{Title: "STEP", Max: 26, Flex: true, Drop: 1})
	}
	return append(cols, Column{Title: "BRANCH", Max: 20, Flex: true, Drop: 3}, Column{Title: "AGE", Right: true, Min: 3})
}

// flatCells are the cells of a run in a flat list, about it the text of what.
func (a *App) flatCells(x repoRun, k Kind, about Cell, step *Cell, now time.Time) []Cell {
	cells := []Cell{
		{Text: k.Glyph(0) + " " + icon("repo") + a.short(x.repo), Styled: fg(k.Color()).Render(k.Glyph(0)) + " " + ico("repo") + sText.Render(a.short(x.repo))},
		iconText("workflow", x.run.Name, false),
		numberCell(x.run.RunNumber, false),
		about,
	}
	if step != nil {
		cells = append(cells, *step)
	}
	return append(cells, iconText("branch", x.run.HeadBranch, false), steelCell(age(x.run.CreatedAt, now), false))
}

const failRunPrefix = "failrun\x00"

func (v *failingView) table() *Table {
	if v.flat {
		return v.list
	}
	return v.t
}

func (v *failingView) count(a *App) int {
	n := 0
	for _, g := range v.groups {
		n += len(g.runs)
	}
	return n
}

func (v *failingView) sync(a *App) {
	runs := failingRuns(a, v.repos(a))
	v.groups = groupFailures(runs, a.st.causes)
	now := time.Now()
	var rows []Row
	v.runs, v.group = map[string]repoRun{}, map[string]*failGroup{}
	for _, g := range v.groups {
		rows = append(rows, v.groupRow(a, g, now))
		v.group[g.key] = g
		if len(g.runs) < 2 || v.folded[g.key] {
			continue
		}
		for i, x := range g.runs {
			key := failRunPrefix + causeID(x.repo, x.run)
			v.runs[key], v.group[key] = x, g
			rows = append(rows, v.runRow(a, key, x, i == len(g.runs)-1, len(g.steps(a.st.causes)) > 1, now))
		}
	}
	v.t.Empty = v.empty(a)
	v.t.SetRows(rows)
	v.syncList(a, runs, now)
}

// syncList fills the flat list: every failed run, newest first.
func (v *failingView) syncList(a *App, runs []repoRun, now time.Time) {
	var rows []Row
	for _, x := range runs {
		c, known := a.st.causes[causeID(x.repo, x.run)]
		g := &failGroup{cause: c, known: known, runs: []repoRun{x}}
		about := Cell{Text: g.causeText(), Styled: sWhite.Render(g.causeText())}
		if !known {
			about.Styled = sDim.Render(about.Text)
		}
		step := steelCell(stepOf(c), false)
		key := failRunPrefix + causeID(x.repo, x.run)
		v.runs[key] = x
		if v.group[key] == nil {
			v.group[key] = v.groupOf(x)
		}
		rows = append(rows, Row{Key: key, Kind: KFail, Cells: a.flatCells(x, KFail, about, &step, now)})
	}
	v.list.Empty = v.empty(a)
	v.list.SetRows(rows)
}

// groupOf is the group a run is in.
func (v *failingView) groupOf(x repoRun) *failGroup {
	for _, g := range v.groups {
		for _, y := range g.runs {
			if y.run.ID == x.run.ID {
				return g
			}
		}
	}
	return nil
}

func (v *failingView) empty(a *App) string {
	if !a.st.loaded {
		return "Loading…"
	}
	return "Nothing fails on the default branch of these repos."
}

// causeText is how a group's cause reads in a row.
func (g *failGroup) causeText() string {
	if !g.known {
		x := g.runs[0]
		return fmt.Sprintf("%s · reading why it failed…", x.run.Name)
	}
	if gh.IsGenericFailure(gh.LogLine{Text: g.cause.Text}) && g.cause.Step != "" {
		return g.cause.Step + " failed · " + g.cause.Text
	}
	return g.cause.Text
}

func (v *failingView) groupRow(a *App, g *failGroup, now time.Time) Row {
	fold := "·"
	if len(g.runs) > 1 {
		fold = "▾"
		if v.folded[g.key] {
			fold = "▸"
		}
	}
	text := g.causeText()
	st := sWhite.Bold(true)
	glyph, glyphSt := KFail.Glyph(0), fg(cRed)
	switch {
	case !g.known:
		st = sDim
	case g.cause.Text == noErrorLine:
		text, st = "no error line in the log", sDim
		glyph, glyphSt = "?", sDim
	}
	styled := sDim.Render(fold) + " " + glyphSt.Render(glyph) + " " + st.Render(text)
	plain := fold + " " + glyph + " " + text
	if len(g.runs) == 1 {
		where := "  " + a.short(g.runs[0].repo)
		styled += sDim.Render(where)
		plain += where
	}
	if _, ok := a.incidentAt(g.oldest()); ok {
		styled += " " + sAmber.Render(icon("warning"))
		plain += " " + icon("warning")
	}
	steps := g.steps(a.st.causes)
	step := Cell{}
	switch {
	case len(steps) == 1:
		step = steelCell(steps[0], false)
	case len(steps) > 1:
		step = steelCell(fmt.Sprintf("%d steps", len(steps)), true)
	}
	return Row{Key: g.key, Kind: KFail, Cells: []Cell{
		{Text: plain, Styled: styled},
		reposCell(g.repos(), false),
		step,
		steelCell(age(g.oldest(), now), false),
	}}
}

// stepOf is where a cause failed, as a column shows it.
func stepOf(c runCause) string {
	if c.NotStarted {
		return "(not started)"
	}
	return c.Step
}

// steps are the steps the runs of the group failed in, each once.
func (g *failGroup) steps(causes map[string]runCause) []string {
	var out []string
	for _, x := range g.runs {
		c, ok := causes[causeID(x.repo, x.run)]
		if !ok {
			continue
		}
		if s := stepOf(c); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// runRow is a run of a group; it names its step when the runs of the group
// failed in different ones.
func (v *failingView) runRow(a *App, key string, x repoRun, last, ownStep bool, now time.Time) Row {
	guide := "├─ "
	if last {
		guide = "└─ "
	}
	label := fmt.Sprintf("%s #%d", x.run.Name, x.run.RunNumber)
	styled := "  " + sDim.Render(guide) + ico("repo") + sText.Render(a.short(x.repo)) + "  " + sDim.Render(label) + "  " + ico("user") + sText.Render(x.run.Actor.Login)
	plain := "  " + guide + a.short(x.repo) + "  " + label + "  " + x.run.Actor.Login
	step := Cell{}
	if ownStep {
		step = steelCell(stepOf(a.st.causes[causeID(x.repo, x.run)]), false)
	}
	return Row{Key: key, Kind: KFail, Cells: []Cell{
		{Text: plain, Styled: styled}, {}, step,
		steelCell(age(x.run.CreatedAt, now), false),
	}}
}

// selected is the group under the cursor and, on the row of a run, the run.
func (v *failingView) selected() (*failGroup, *repoRun) {
	r := v.table().Selected()
	if r == nil {
		return nil, nil
	}
	if x, ok := v.runs[r.Key]; ok {
		return v.group[r.Key], &x
	}
	return v.group[r.Key], nil
}

// load reads the default-branch runs of the repos that are due, and the
// causes of the failed runs not known yet.
func (v *failingView) load(a *App, force bool) tea.Cmd {
	a.st.wantCauses = true
	now := time.Now()
	var due []string
	for _, repo := range v.repos(a) {
		l := a.st.defaultState(repo)
		if force || a.readDue(l, repo, now, staleAfter) {
			due = append(due, repo)
		}
	}
	return tea.Batch(a.st.loadDefaults(a, gh.Background(context.Background()), due), a.st.loadCauses(a, failingRuns(a, v.repos(a))))
}

func (v *failingView) busy(a *App) bool {
	for _, repo := range v.repos(a) {
		if a.st.defaultState(repo).busy {
			return true
		}
	}
	return len(a.st.causeBusy) > 0
}

func (v *failingView) chips(a *App) []string {
	runs, yours := 0, 0
	for _, g := range v.groups {
		runs += len(g.runs)
		if len(a.yourWorkflows(g.commonWorkflows())) > 0 {
			yours++
		}
	}
	var chips []string
	if runs > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failing", runs)))
	}
	if len(v.groups) > 0 && v.groups[0].repos() > 1 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d same cause", v.groups[0].repos())))
	}
	if yours > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d via your workflows", yours)))
	}
	during := 0
	for _, g := range v.groups {
		if _, ok := a.incidentAt(g.oldest()); ok {
			during++
		}
	}
	if during > 0 {
		chips = append(chips, roundPill(fmt.Sprintf("%s %d since GitHub's incident", icon("warning"), during), cAmber, tint[KRun]))
	}
	return chips
}

func (v *failingView) coverage(a *App) string {
	repos := map[string]bool{}
	for _, g := range v.groups {
		for _, x := range g.runs {
			repos[x.repo] = true
		}
	}
	s := fmt.Sprintf("latest run per workflow on the default branch · %d %s in %s", len(v.groups), plural("cause", len(v.groups)), repoCount(len(repos)))
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

func (v *failingView) listHead(a *App) string {
	return groupChip(v.flat, "group by cause") + "   " + a.filterLine(v.FilterValue(), v.FilterInfo(a))
}

func (v *failingView) SetFilter(a *App, q string) { v.t.SetFilter(q, a.me); v.list.SetFilter(q, a.me) }
func (v *failingView) FilterValue() string        { return v.t.Filter() }
func (v *failingView) FilterInfo(a *App) filterInfo {
	return v.table().Info()
}

// yourWorkflows keeps the reusable workflows that belong to the owner of
// most of your repos.
func (a *App) yourWorkflows(paths []string) []string {
	owner := strings.ToLower(a.owner())
	var out []string
	for _, p := range paths {
		if owner != "" && strings.HasPrefix(strings.ToLower(p), owner+"/") {
			out = append(out, p)
		}
	}
	return out
}

func (v *failingView) keys(a *App) []Binding {
	g, x := v.selected()
	var bs []Binding
	if g != nil {
		run := g.runs[0]
		if x != nil {
			run = *x
		}
		open := Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, run.repo, run.run)) })}
		if x == nil && len(g.runs) > 1 {
			open = Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Help: "open / close the runs of the cause",
				Do: do(func() tea.Cmd { v.fold(); return nil })}
		}
		bs = append(bs, open,
			Binding{Keys: []string{"l"}, Desc: "logs", Help: "the log of the run", Do: do(func() tea.Cmd { return a.push(newLogView(a, run.repo, run.run, nil, 0)) })})
		if x == nil && len(g.runs) > 1 {
			bs = append(bs, Binding{Keys: []string{"R"}, Desc: fmt.Sprintf("rerun all %d", len(g.runs)), Help: "rerun the failed jobs of every run with this cause",
				Mutates: true, Need: gh.CapWrite, Do: do(func() tea.Cmd { return a.rerunFailedOf(g.runs, v.t.ClearMarks) })})
		} else {
			bs = append(bs, without(a.runKeys(run.repo, run.run), "o", "y")...)
		}
		if x != nil || len(g.runs) == 1 {
			bs = append(bs, a.runRepoKey(run.repo, run.run))
		} else if common := g.commonWorkflows(); len(common) > 0 {
			src := common[0]
			bs = append(bs, Binding{Keys: []string{"r"}, Desc: "shared workflow repo", Help: "open the repo of the reusable workflow every run called",
				Do: do(func() tea.Cmd {
					return a.push(newRepoView(a, gh.Use{Target: strings.SplitN(src, "@", 2)[0]}.TargetRepo(), ""))
				})})
		}
		bs = append(bs, Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open the run in the browser", Do: do(func() tea.Cmd { return a.openURL(run.run.HTMLURL) })})
	}
	if v.flat {
		return append(bs, groupKey(&v.flat, v.t, v.list))
	}
	return append(bs,
		Binding{Keys: []string{" "}, Label: "space", Desc: "fold", Help: "fold / unfold the runs of a cause", Do: do(func() tea.Cmd { v.fold(); return nil })},
		Binding{Keys: []string{"v"}, Desc: "fold all", Help: "fold / unfold the runs of every cause", Do: do(func() tea.Cmd { v.foldAll(); return nil })},
		groupKey(&v.flat, v.t, v.list),
	)
}

func (v *failingView) fold() {
	g, x := v.selected()
	if g == nil || len(g.runs) < 2 {
		return
	}
	v.folded[g.key] = !v.folded[g.key] || x != nil
	v.cursorTo(g.key)
}

func (v *failingView) foldAll() {
	all := true
	for _, g := range v.groups {
		all = all && (v.folded[g.key] || len(g.runs) < 2)
	}
	for _, g := range v.groups {
		v.folded[g.key] = !all
	}
	if g, _ := v.selected(); g != nil {
		v.cursorTo(g.key)
	}
}

// cursorTo moves the cursor up to the row with key.
func (v *failingView) cursorTo(key string) {
	for i := v.t.Cursor(); i >= 0; i-- {
		v.t.SetCursor(i)
		if r := v.t.Selected(); r != nil && r.Key == key {
			return
		}
	}
}

func (v *failingView) panel(a *App, w, h int) []string {
	g, x := v.selected()
	if g == nil {
		return emptyPanel(w, h, "Nothing selected.")
	}
	if x != nil {
		return a.runPanel(*x, w, h)
	}
	now := time.Now()
	p := newPanel(w)
	if !g.known {
		return a.runPanel(g.runs[0], w, h)
	}
	for i, l := range strings.Split(ansi.Wordwrap(g.causeText(), max(p.inner(), 10), ""), "\n") {
		if i == 4 {
			break
		}
		p.add(sWhite.Bold(true).Render(l))
	}
	if g.cause.Text == noErrorLine {
		p.wrap("The step failed without logging a line that reads like an error. l opens its log.", sDim, 4)
	}
	first := g.runs[len(g.runs)-1]
	for _, y := range g.runs {
		if y.run.CreatedAt.Before(first.run.CreatedAt) {
			first = y
		}
	}
	p.add("")
	p.add(statusChip(KFail, 0, repoCount(g.repos())) + sDim.Render(" · first ") + sText.Render(age(first.run.CreatedAt, now)+" ago") + sDim.Render(" in ") + sText.Render(a.short(first.repo)))

	label := "Shared cause"
	if len(g.runs) == 1 {
		label = "Location"
	}
	p.label(label)
	if steps := g.steps(a.st.causes); len(steps) == 1 {
		p.add(fact("workflow", g.cause.Job) + sDim.Render(" › ") + sText.Render(steps[0]))
	} else if len(steps) > 1 {
		p.add(fact("workflow", fmt.Sprintf("%d different steps", len(steps))))
	}
	common := g.commonWorkflows()
	for _, wf := range common {
		target, version, _ := strings.Cut(wf, "@")
		u := gh.Use{Target: target}
		p.add(ico("repo") + sText.Render(a.short(u.TargetRepo())+" "+pathBase(u.TargetFile())+" ") + versionText(version))
	}
	if img, all := v.commonImage(a, g); img != "" {
		note := ""
		if !all {
			note = sDim.Render(" (where the log was read)")
		}
		p.add(fact("server", img) + note)
	}
	if len(g.runs) > 1 {
		shas, people := map[string]bool{}, map[string]bool{}
		for _, y := range g.runs {
			shas[y.run.HeadSHA] = true
			people[strings.ToLower(y.run.Actor.Login)] = true
		}
		p.add(sDim.Render(fmt.Sprintf("%d %s, %d %s", len(shas), plural("commit", len(shas)), len(people), plural("author", len(people)))))
	}

	p.label("Next step")
	if in, ok := a.incidentAt(g.oldest()); ok {
		what := "Started during GitHub's incident: " + in.Name + "."
		keys := keycap("ctrl+g") + sText.Render(" the incident")
		if in.Open() {
			keys = sText.Render("Wait for it, then ") + keycap("R") + sText.Render(" reruns them  ") + keys
		} else {
			keys = sText.Render("It is over: ") + keycap("R") + sText.Render(" reruns them  ") + keys
		}
		p.callout(cAmber, sWhite.Render(what), keys)
		p.add("")
	}
	switch yours := a.yourWorkflows(common); {
	case len(yours) > 0 && g.repos() > 1:
		p.callout(cWait, "Likely in your workflow: the same step fails in every repo that calls "+pathBase(strings.SplitN(yours[0], "@", 2)[0])+".", keycap("r")+sText.Render(" open its repo"))
	case g.repos() > 1:
		p.callout(cWait, fmt.Sprintf("The same failure in %d repos, with different commits: likely not their code.", g.repos()), keycap("R")+sText.Render(" rerun them all")+"  "+keycap("enter")+sText.Render(" open the newest"))
	default:
		p.callout(cRed, "Only this repo fails this way.", keycap("enter")+sText.Render(" open the run")+"  "+keycap("l")+sText.Render(" logs"))
	}

	p.label("Runs")
	for _, y := range g.runs {
		p.row(ico("repo")+sText.Render(a.short(y.repo))+sDim.Render(fmt.Sprintf("  %s #%d", y.run.Name, y.run.RunNumber)), steelCell(age(y.run.CreatedAt, now), false).Styled)
	}
	return p.render(h)
}

// commonImage is the runner image of the runs whose log was read, when they
// all ran on the same one; all says every run's image is known.
func (v *failingView) commonImage(a *App, g *failGroup) (string, bool) {
	img, all := "", true
	for _, x := range g.runs {
		c := a.st.causes[causeID(x.repo, x.run)]
		switch {
		case c.Image == "":
			all = false
		case img == "":
			img = c.Image
		case img != c.Image:
			return "", false
		}
	}
	return img, all
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// rerunFailedOf asks, then reruns the failed jobs of each run in turn.
func (a *App) rerunFailedOf(runs []repoRun, after func() bool) tea.Cmd {
	n := len(runs)
	lines := []string{sWhite.Render(fmt.Sprintf("%d runs", n))}
	for i, x := range runs {
		if i == 6 {
			lines = append(lines, sDim.Render(fmt.Sprintf("… and %d more", n-i)))
			break
		}
		lines = append(lines, sDim.Render(fmt.Sprintf("%s · %s #%d · ", a.short(x.repo), x.run.Name, x.run.RunNumber))+sText.Render(x.run.DisplayTitle))
	}
	lines = append(lines, "", "The failed jobs of each, and the jobs that depend on them, run again.")
	return confirm(fmt.Sprintf("Rerun the failed jobs of %d runs?", n), lines, "Rerun", false, func() tea.Cmd {
		if a.opts.ReadOnly {
			return a.flashCmd(nHint, readOnlyText)
		}
		after()
		a.earnBest("mass-revival", n)
		return a.eachRun(runs, "Reran the failed jobs of", func(ctx context.Context, x repoRun) error {
			return a.client.RerunFailed(ctx, x.repo, x.run.ID, false)
		})
	})
}

// cancelRuns asks, then cancels each run in turn.
func (a *App) cancelRuns(runs []repoRun, after func() bool) tea.Cmd {
	n := len(runs)
	lines := []string{sWhite.Render(fmt.Sprintf("%d runs", n))}
	for i, x := range runs {
		if i == 6 {
			lines = append(lines, sDim.Render(fmt.Sprintf("… and %d more", n-i)))
			break
		}
		lines = append(lines, sDim.Render(fmt.Sprintf("%s · %s #%d · ", a.short(x.repo), x.run.Name, x.run.RunNumber))+sText.Render(x.run.DisplayTitle))
	}
	return confirm(fmt.Sprintf("Cancel %d runs?", n), append(lines, "", "Each stops where it is."), "Cancel runs", true, func() tea.Cmd {
		if a.opts.ReadOnly {
			return a.flashCmd(nHint, readOnlyText)
		}
		after()
		a.earn("cancel-culture", n)
		return a.eachRun(runs, "Canceled", func(ctx context.Context, x repoRun) error {
			return a.client.Cancel(ctx, x.repo, x.run.ID)
		})
	})
}

// eachRun does fn to each run and says how many it did; the lists of their
// repos are read again.
func (a *App) eachRun(runs []repoRun, done string, fn func(ctx context.Context, x repoRun) error) tea.Cmd {
	viewID := a.top().ID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var failed []string
		for _, x := range runs {
			if err := fn(ctx, x); err != nil {
				failed = append(failed, fmt.Sprintf("%s #%d: %v", a.short(x.repo), x.run.RunNumber, err))
			}
		}
		text := fmt.Sprintf("%s %d of %d runs", done, len(runs)-len(failed), len(runs))
		if len(failed) > 0 {
			return actionMsg{viewID: viewID, err: fmt.Errorf("%s · %s", text, strings.Join(failed, " · "))}
		}
		return actionMsg{viewID: viewID, text: text}
	}
}

// renderList draws the causes as rounded cards, as the pipeline draws its
// jobs: the border in the colour of the failure, the step and the runs
// inside, and the cause under the cursor filled. Every run on a line of its
// own (g) stays a table.
func (v *failingView) renderList(a *App, w, h int) string {
	if v.flat {
		return v.list.Render(w, h)
	}
	if v.t.Len() == 0 {
		return " " + sDim.Render(v.t.Empty)
	}
	var lines []string
	cursorLine := 0
	sel := v.t.Selected()
	now := time.Now()
	keys := v.t.FilteredKeys()
	for i := 0; i < len(keys); {
		g := v.group[keys[i]]
		var runKeys []string
		for i++; i < len(keys) && keys[i] != g.key && v.group[keys[i]] == g; i++ {
			runKeys = append(runKeys, keys[i])
		}
		onCard := sel != nil && (sel.Key == g.key || slices.Contains(runKeys, sel.Key))
		if sel != nil && sel.Key == g.key {
			cursorLine = len(lines)
		}
		card, at := v.causeCard(a, g, runKeys, sel, onCard, w, now)
		if at >= 0 {
			cursorLine = len(lines) + at
		}
		lines = append(lines, card...)
	}
	v.cardTop = min(max(v.cardTop, cursorLine-h+2), cursorLine)
	v.cardTop = max(min(v.cardTop, len(lines)-h), 0)
	return strings.Join(block(strings.Join(lines[v.cardTop:], "\n"), w, h), "\n")
}

// causeCard is the card of a cause: its text in the top edge with how many
// repos and since when, the step and what the runs call, then the runs.
// at is the line of the run under the cursor, or -1.
func (v *failingView) causeCard(a *App, g *failGroup, runKeys []string, sel *Row, onCard bool, w int, now time.Time) (lines []string, at int) {
	w-- // the margin cardLines puts before a card
	c := &panelCard{border: cRed}
	glyph, text := fg(cRed).Render(KFail.Glyph(0)), sWhite.Bold(true).Render(g.causeText())
	switch {
	case !g.known:
		c.border, glyph, text = cFrame, sDim.Render("·"), sDim.Render(g.causeText())
	case g.cause.Text == noErrorLine:
		c.border, c.dashed, glyph, text = cDim, true, sDim.Render("?"), sDim.Render("no error line in the log")
	}
	if onCard {
		c.fill = lipgloss.Color(mix(string(cBg), string(c.border), 0.12))
	}
	fold := ""
	if len(g.runs) > 1 && v.folded[g.key] {
		fold = sDim.Render("▸ ")
	}
	edge := sDim.Render(repoCount(g.repos()) + " · " + age(g.oldest(), now))
	title := fold + glyph + " " + text
	if room := w - 6 - width(edge) - 3; width(title) > room {
		title = clip(title, max(room, 8))
	}
	c.title = title + " " + fg(c.border).Render(strings.Repeat(cardStroke(c), max(w-6-width(title)-width(edge)-2, 1))) + " " + edge

	var where []string
	steps := g.steps(a.st.causes)
	if len(steps) == 1 {
		where = append(where, ico("workflow")+sText.Render(steps[0]))
	} else if len(steps) > 1 {
		where = append(where, sText.Render(fmt.Sprintf("%d steps", len(steps))))
	}
	for _, wf := range g.commonWorkflows() {
		target, version, _ := strings.Cut(wf, "@")
		where = append(where, sDim.Render("via ")+sGold.Render(pathBase(target))+" "+versionText(version))
	}
	if len(g.runs) == 1 {
		where = append(where, ico("repo")+sText.Render(a.short(g.runs[0].repo))+sDim.Render(fmt.Sprintf(" %s #%d", g.runs[0].run.Name, g.runs[0].run.RunNumber)))
	}
	if len(where) > 0 {
		c.lines = append(c.lines, strings.Join(where, sDim.Render(" · ")))
	}
	at = -1
	inner := w - cardInset
	for _, key := range runKeys {
		x := v.runs[key]
		left := ico("repo") + sText.Render(a.short(x.repo)) + "  " + sDim.Render(fmt.Sprintf("%s #%d", x.run.Name, x.run.RunNumber)) + "  " + ico("user") + sText.Render(x.run.Actor.Login)
		if len(steps) > 1 {
			left += sDim.Render("  in ") + fg(cSteel).Render(stepOf(a.st.causes[causeID(x.repo, x.run)]))
		}
		right := fg(cSteel).Render(age(x.run.CreatedAt, now))
		line := fit(left, inner-width(right)-1) + " " + right
		if sel != nil && sel.Key == key {
			at = len(c.lines) + 1
			line = withBg(sCyan.Render("▌")+fit(line, inner-1), cSelBg)
		}
		c.lines = append(c.lines, line)
	}
	if len(g.runs) > 1 && v.folded[g.key] {
		c.lines = append(c.lines, sDim.Render(fmt.Sprintf("%d runs · space shows them", len(g.runs))))
	}
	return cardLines(c, w), at
}
