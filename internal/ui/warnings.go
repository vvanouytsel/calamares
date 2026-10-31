package ui

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runWarning is a warning annotation of a job of a run.
type runWarning struct {
	Job     string
	Message string
	Path    string
}

// warningsStale is how often the Warnings view reads the default-branch runs
// again to find new ones; the warnings of a run are read once.
const warningsStale = 24 * time.Hour

const snapshotWarnings = "warnings"

type warningsMsg struct {
	ids  map[string]string // node id → causeID of the runs asked for
	read map[string][]gh.JobAnnotation
	err  error
}

// warningsPerRead is how many runs one read asks for: four GraphQL requests.
const warningsPerRead = 100

// loadWarnings reads the warnings of the runs not read yet, in GraphQL
// batches, one read at a time. A run listed from a cache older than node ids
// waits until its list is read again.
func (s *store) loadWarnings(a *App, runs []repoRun) tea.Cmd {
	for _, x := range runs {
		id := causeID(x.repo, x.run)
		if _, ok := s.warnings[id]; !ok && !s.warningBusy[id] && x.run.NodeID != "" {
			s.warningQueue = append(s.warningQueue, x)
		}
	}
	return s.nextWarnings(a)
}

func (s *store) nextWarnings(a *App) tea.Cmd {
	if len(s.warningBusy) > 0 {
		return nil
	}
	ids := map[string]string{}
	var nodes []string
	for len(s.warningQueue) > 0 && len(nodes) < warningsPerRead {
		x := s.warningQueue[0]
		s.warningQueue = s.warningQueue[1:]
		id := causeID(x.repo, x.run)
		if _, ok := s.warnings[id]; ok || s.warningBusy[id] {
			continue
		}
		s.warningBusy[id] = true
		ids[x.run.NodeID] = id
		nodes = append(nodes, x.run.NodeID)
	}
	if len(nodes) == 0 {
		return nil
	}
	return func() tea.Msg {
		read, err := a.client.RunAnnotations(gh.Background(context.Background()), nodes)
		return warningsMsg{ids, read, err}
	}
}

func (s *store) gotWarnings(a *App, m warningsMsg) tea.Cmd {
	for node, id := range m.ids {
		delete(s.warningBusy, id)
		if m.err != nil {
			continue
		}
		anns := m.read[node] // a run GitHub no longer has warns of nothing
		list := []runWarning{}
		for _, an := range anns {
			if strings.EqualFold(an.Level, "warning") {
				list = append(list, runWarning{Job: an.Job, Message: strings.TrimSpace(an.Message), Path: an.Path})
			}
		}
		s.warnings[id] = list
	}
	if m.err == nil {
		a.client.Remember(snapshotWarnings, s.warnings)
	}
	return s.nextWarnings(a)
}

var (
	actionRef      = regexp.MustCompile(`\b[\w.-]+/[\w.-]+(?:/[\w./-]+)?@[\w.-]*[\w-]`) // a full stop after it ends the sentence
	sentenceEnd    = regexp.MustCompile(`[.!]\s`)
	warningNoise   = regexp.MustCompile(`(?i)\b[0-9a-f]{7,64}\b`)
	warningSpace   = regexp.MustCompile(`\s+`)
	trailingPunct  = regexp.MustCompile(`[\s,:;]+$`)
	warningPathish = regexp.MustCompile(`(?:[A-Za-z]:\\|/)[^\s'"():,]+[/\\][^\s'"():,]*`)
	// valueList is a list in square brackets; quotedValue a value in quotes.
	valueList   = regexp.MustCompile(`\[[^\]]*\]?`)
	quotedValue = regexp.MustCompile("'[^']*'|\"[^\"]*\"|`[^`]*`")
	numberValue = regexp.MustCompile(`\d+(?:[.,]\d+)*`)
)

// warningHeadline is the first sentence of the first line of a warning.
func warningHeadline(msg string) string {
	line := strings.TrimSpace(strings.SplitN(ansi.Strip(msg), "\n", 2)[0])
	if loc := sentenceEnd.FindStringIndex(line); loc != nil {
		line = line[:loc[0]+1]
	}
	return line
}

// warningPattern is the headline with every value in it as "…": the actions
// it names, lists, quoted values, numbers, hashes and paths. Warnings that
// differ only in those are the same warning.
func warningPattern(msg string) string {
	t := warningHeadline(msg)
	t = actionRef.ReplaceAllString(t, "…")
	t = valueList.ReplaceAllString(t, "[…]")
	t = quotedValue.ReplaceAllString(t, "…")
	t = warningPathish.ReplaceAllString(t, "…")
	t = warningNoise.ReplaceAllString(t, "…")
	t = numberValue.ReplaceAllString(t, "…")
	t = trailingPunct.ReplaceAllString(t, "")
	return strings.TrimSpace(warningSpace.ReplaceAllString(t, " "))
}

// warningKey is the pattern of a warning, to compare.
func warningKey(msg string) string { return strings.ToLower(warningPattern(msg)) }

// warningVia names what a warning comes from: the actions or reusable
// workflows it mentions, or else the workflow file it points at.
func warningVia(w runWarning) []string {
	refs := actionRef.FindAllString(w.Message, -1)
	if len(refs) > 0 {
		return refs
	}
	if strings.HasPrefix(w.Path, ".github/") && w.Path != ".github" {
		return []string{w.Path}
	}
	return nil
}

// warnKind is one exact warning: a headline, the full message of the first
// one seen, and where it shows.
type warnKind struct {
	text   string
	full   string
	repos  []string
	byRepo map[string][]string // repo → workflow names
}

// warnGroup is the warnings that differ only in their values.
type warnGroup struct {
	key     string
	pattern string
	kinds   []*warnKind
	repos   []string
	via     map[string]map[string]bool // what causes it → the repos
	hits    []warnHit                  // the runs that warned, newest first
	newest  time.Time
	example repoRun
}

// warnHit is a run that gave one kind of a warning.
type warnHit struct {
	x    repoRun
	kind *warnKind
}

func (h warnHit) key() string {
	return "warnrun\x00" + causeID(h.x.repo, h.x.run) + "\x00" + h.kind.text
}

func (g *warnGroup) viaList() []string {
	out := make([]string, 0, len(g.via))
	for v := range g.via {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if ni, nj := len(g.via[out[i]]), len(g.via[out[j]]); ni != nj {
			return ni > nj
		}
		return out[i] < out[j]
	})
	return out
}

func (g *warnGroup) kind(text string) *warnKind {
	for _, k := range g.kinds {
		if k.text == text {
			return k
		}
	}
	k := &warnKind{text: text, byRepo: map[string][]string{}}
	g.kinds = append(g.kinds, k)
	return k
}

// groupWarnings gathers the warnings of runs by their pattern, those in the
// most repos first, and within a group by their exact headline.
func groupWarnings(runs []repoRun, warnings map[string][]runWarning) []*warnGroup {
	byKey := map[string]*warnGroup{}
	var out []*warnGroup
	for _, x := range runs {
		for _, w := range warnings[causeID(x.repo, x.run)] {
			key := warningKey(w.Message)
			if key == "" {
				continue
			}
			g, ok := byKey[key]
			if !ok {
				g = &warnGroup{key: key, pattern: warningPattern(w.Message), via: map[string]map[string]bool{}, example: x}
				byKey[key] = g
				out = append(out, g)
			}
			if !containsFold(g.repos, x.repo) {
				g.repos = append(g.repos, x.repo)
			}
			k := g.kind(warningHeadline(w.Message))
			if k.full == "" {
				k.full = w.Message
			}
			if hit := (warnHit{x, k}); !slices.ContainsFunc(g.hits, func(h warnHit) bool { return h.key() == hit.key() }) {
				g.hits = append(g.hits, hit)
			}
			if _, seen := k.byRepo[x.repo]; !seen {
				k.repos = append(k.repos, x.repo)
			}
			if !containsFold(k.byRepo[x.repo], x.run.Name) {
				k.byRepo[x.repo] = append(k.byRepo[x.repo], x.run.Name)
			}
			for _, v := range warningVia(w) {
				if g.via[v] == nil {
					g.via[v] = map[string]bool{}
				}
				g.via[v][x.repo] = true
			}
			if x.run.CreatedAt.After(g.newest) {
				g.newest = x.run.CreatedAt
			}
		}
	}
	for _, g := range out {
		sort.SliceStable(g.kinds, func(i, j int) bool { return len(g.kinds[i].repos) > len(g.kinds[j].repos) })
		sort.SliceStable(g.hits, func(i, j int) bool { return g.hits[i].x.run.CreatedAt.After(g.hits[j].x.run.CreatedAt) })
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].repos) != len(out[j].repos) {
			return len(out[i].repos) > len(out[j].repos)
		}
		return out[i].key < out[j].key
	})
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// warningValue is what tells one kind of a warning from the others: its
// first quoted value, else its first number.
func warningValue(text string) string {
	if m := quotedValue.FindString(text); m != "" {
		return m
	}
	return numberValue.FindString(text)
}

// warningsView is the Warnings view of the Actions tab: what the latest runs
// on the default branches warn about, deprecations first of all, grouped by
// what they say with the values left out. A group opens to the runs that
// warned; g lists every run on a line of its own instead.
type warningsView struct {
	t      *Table
	list   *Table // every run and warning on a line of its own, when flat
	flat   bool
	groups []*warnGroup
	open   map[string]bool       // groups opened to their runs
	rows   map[string]*warnGroup // by row key
	hitOf  map[string]warnHit    // row key of a run's line → the run and its warning
	repos  func(a *App) []string
}

func newWarningsView(repos func(a *App) []string) *warningsView {
	return &warningsView{open: map[string]bool{}, repos: repos, t: newTable(
		Column{Title: "WARNING", Max: 90, Flex: true, Help: "a warning of the latest runs on the default branch; warnings that differ only in their values are one row, with the values shown as …"},
		Column{Title: "REPOS", Right: true, Help: "how many repos gave it"},
		Column{Title: "VARIANTS", Right: true, HideEmpty: true, Help: "how many different values it came with"},
		Column{Title: "VIA", Max: 34, Flex: true, Drop: 2, Help: "the action it names"},
		Column{Title: "BRANCH", Max: 20, Flex: true, Drop: 1, HideEmpty: true},
		Column{Title: "SINCE", Right: true, Min: 3, Help: "how long ago it was last seen"},
	), list: newTable(flatColumns("WARNING", false)...)}
}

func (v *warningsView) table() *Table {
	if v.flat {
		return v.list
	}
	return v.t
}

func (v *warningsView) count(a *App) int             { return len(v.groups) }
func (v *warningsView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me); v.list.SetFilter(q, a.me) }
func (v *warningsView) FilterValue() string          { return v.t.Filter() }
func (v *warningsView) FilterInfo(a *App) filterInfo { return v.table().Info() }
func (v *warningsView) listHead(a *App) string {
	return groupChip(v.flat, "group by warning") + "   " + a.filterLine(v.FilterValue(), v.FilterInfo(a))
}

// latestRuns is the newest finished run of each workflow on the default
// branch of the tab's repos.
func (v *warningsView) latestRuns(a *App) []repoRun {
	var out []repoRun
	for _, repo := range v.repos(a) {
		for _, r := range a.latestPerWorkflow(repo) {
			out = append(out, repoRun{repo, r})
		}
	}
	return out
}

func (v *warningsView) load(a *App, force bool) tea.Cmd {
	var due []string
	for _, repo := range v.repos(a) {
		l := a.st.defaultState(repo)
		if force || (time.Since(l.at) > warningsStale && time.Since(l.tried) > staleAfter) {
			due = append(due, repo)
		}
	}
	a.st.wantWarnings = true
	return tea.Batch(a.st.loadDefaults(a, gh.Background(context.Background()), due), a.st.loadWarnings(a, v.latestRuns(a)))
}

func (v *warningsView) busy(a *App) bool { return len(a.st.warningBusy) > 0 }

func (v *warningsView) sync(a *App) {
	v.groups = groupWarnings(v.latestRuns(a), a.st.warnings)
	v.rows, v.hitOf = map[string]*warnGroup{}, map[string]warnHit{}
	now := time.Now()
	var rows, flat []Row
	var hits []warnHit
	for _, g := range v.groups {
		v.rows[g.key] = g
		rows = append(rows, v.groupRow(a, g, now))
		for i, h := range g.hits {
			v.rows[h.key()], v.hitOf[h.key()] = g, h
			hits = append(hits, h)
			if v.open[g.key] {
				rows = append(rows, v.hitRow(a, g, h, i == len(g.hits)-1, now))
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].x.run.CreatedAt.After(hits[j].x.run.CreatedAt) })
	for _, h := range hits {
		about := Cell{Text: h.kind.text, Styled: sWhite.Render(h.kind.text)}
		flat = append(flat, Row{Key: h.key(), Kind: KWait, Cells: a.flatCells(h.x, KWait, about, nil, now)})
	}
	empty := "The latest runs on the default branches warn about nothing."
	if v.busy(a) && len(rows) == 0 {
		empty = "Reading the warnings of the latest runs…"
	}
	v.t.Empty, v.list.Empty = empty, empty
	v.t.SetRows(rows)
	v.list.SetRows(flat)
}

// patternStyled draws a pattern with its "…" placeholders in the warning colour.
func patternStyled(p string) string {
	parts := strings.Split(p, "…")
	for i, part := range parts {
		parts[i] = sWhite.Bold(true).Render(part)
	}
	return strings.Join(parts, fg(cWait).Render("…"))
}

func (v *warningsView) groupRow(a *App, g *warnGroup, now time.Time) Row {
	fold := "▸"
	if v.open[g.key] {
		fold = "▾"
	}
	text, styled := g.pattern, patternStyled(g.pattern)
	if len(g.kinds) == 1 {
		text = g.kinds[0].text
		styled = sWhite.Bold(true).Render(text)
	}
	mark := strings.TrimSpace(icon("warning"))
	if mark == "" {
		mark = "!"
	}
	via := Cell{}
	if list := g.viaList(); len(list) > 0 {
		t := actionName(list[0])
		if len(list) > 1 {
			t += fmt.Sprintf(" +%d", len(list)-1)
		}
		via = iconText("code", t, false)
	}
	kinds := Cell{}
	if len(g.kinds) > 1 {
		kinds = reposCell(len(g.kinds), false)
	}
	return Row{Key: g.key, Kind: KWait, Cells: []Cell{
		{Text: fold + " " + mark + " " + text, Styled: sDim.Render(fold) + " " + fg(cWait).Render(mark) + " " + styled},
		reposCell(len(g.repos), false),
		kinds,
		via,
		{},
		steelCell(age(g.newest, now), false),
	}}
}

// hitRow is a run under a warning: its repo, workflow, run and who started
// it, and the value its warning has when the group has several kinds.
func (v *warningsView) hitRow(a *App, g *warnGroup, h warnHit, last bool, now time.Time) Row {
	guide := "├─ "
	if last {
		guide = "└─ "
	}
	x := h.x
	label := fmt.Sprintf("%s #%d", x.run.Name, x.run.RunNumber)
	styled := "   " + sDim.Render(guide) + ico("repo") + sText.Render(a.short(x.repo)) + "  " + sDim.Render(label) + "  " + ico("user") + sText.Render(x.run.Actor.Login)
	plain := "   " + guide + a.short(x.repo) + "  " + label + "  " + x.run.Actor.Login
	if val := warningValue(h.kind.text); len(g.kinds) > 1 && val != "" {
		styled += "  " + fg(cWait).Render(val)
		plain += "  " + val
	}
	return Row{Key: h.key(), Kind: KWait, Cells: []Cell{
		{Text: plain, Styled: styled}, {}, {}, {},
		iconText("branch", x.run.HeadBranch, false),
		steelCell(age(x.run.CreatedAt, now), false),
	}}
}

// actionName leaves the path inside a repo out of an action reference.
func actionName(ref string) string {
	target, version, ok := strings.Cut(ref, "@")
	if !ok {
		return ref
	}
	parts := strings.SplitN(target, "/", 3)
	if len(parts) == 3 {
		return parts[0] + "/" + parts[1] + "/" + pathBase(parts[2]) + "@" + version
	}
	return ref
}

func (v *warningsView) chips(a *App) []string {
	repos := map[string]bool{}
	yours := 0
	for _, g := range v.groups {
		for _, r := range g.repos {
			repos[r] = true
		}
		for _, via := range g.viaList() {
			if len(a.yourWorkflows([]string{via})) > 0 {
				yours++
				break
			}
		}
	}
	var chips []string
	if len(v.groups) > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d %s in %s", len(v.groups), plural("warning", len(v.groups)), repoCount(len(repos)))))
	}
	if yours > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d from your actions", yours)))
	}
	return chips
}

func (v *warningsView) coverage(a *App) string {
	s := "the warnings of the latest run per workflow on the default branch"
	if v.busy(a) {
		s += fmt.Sprintf(" · reading %d…", len(a.st.warningBusy)+len(a.st.warningQueue))
	}
	return s
}

// selected is the warning under the cursor and, on the line of a run, the run.
func (v *warningsView) selected() (*warnGroup, *warnHit) {
	r := v.table().Selected()
	if r == nil {
		return nil, nil
	}
	if h, ok := v.hitOf[r.Key]; ok {
		return v.rows[r.Key], &h
	}
	return v.rows[r.Key], nil
}

func (v *warningsView) keys(a *App) []Binding {
	g, h := v.selected()
	var bs []Binding
	switch {
	case h != nil:
		x := h.x
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run", Do: do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })},
			Binding{Keys: []string{"l"}, Desc: "logs", Help: "the log of the run", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })},
			a.runRepoKey(x.repo, x.run),
			Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open the run in the browser", Do: do(func() tea.Cmd { return a.openURL(x.run.HTMLURL) })})
	case g != nil:
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Help: "open / close the runs that gave the warning",
			Do: do(func() tea.Cmd { v.toggle(); return nil })})
		if via := g.viaList(); len(via) > 0 && strings.Contains(via[0], "@") {
			src := gh.Use{Target: strings.SplitN(via[0], "@", 2)[0]}.TargetRepo()
			bs = append(bs, Binding{Keys: []string{"r"}, Desc: "action repo", Help: "open the repo of the action it comes from",
				Do: do(func() tea.Cmd { return a.push(newRepoView(a, src, "")) })})
		}
	}
	if v.flat {
		return append(bs, groupKey(&v.flat, v.t, v.list))
	}
	return append(bs,
		Binding{Keys: []string{" "}, Label: "space", Desc: "fold", Help: "open / close the runs of a warning", Do: do(func() tea.Cmd { v.toggle(); return nil })},
		Binding{Keys: []string{"v"}, Desc: "open all", Help: "open / close the runs of every warning", Do: do(func() tea.Cmd { v.toggleAll(); return nil })},
		groupKey(&v.flat, v.t, v.list))
}

func (v *warningsView) toggle() {
	g, h := v.selected()
	if g == nil {
		return
	}
	v.open[g.key] = !v.open[g.key] && h == nil
	v.cursorTo(g.key)
}

func (v *warningsView) toggleAll() {
	all := true
	for _, g := range v.groups {
		all = all && v.open[g.key]
	}
	for _, g := range v.groups {
		v.open[g.key] = !all
	}
	if g, _ := v.selected(); g != nil {
		v.cursorTo(g.key)
	}
}

func (v *warningsView) cursorTo(key string) {
	for i := v.t.Cursor(); i >= 0; i-- {
		v.t.SetCursor(i)
		if r := v.t.Selected(); r != nil && r.Key == key {
			return
		}
	}
}

func (v *warningsView) panel(a *App, w, h int) []string {
	g, hit := v.selected()
	if g == nil {
		return emptyPanel(w, h, "Nothing selected.")
	}
	p := newLayerPanel(w)
	switch {
	case hit != nil:
		v.kindPanel(a, p, hit.kind)
		x := hit.x
		p.label("Run")
		p.add(ico("repo") + sText.Render(a.short(x.repo)+" › "+x.run.Name) + sGold.Render(fmt.Sprintf(" #%d", x.run.RunNumber)))
		p.add(fact("branch", x.run.HeadBranch) + "  " + fact("user", x.run.Actor.Login) + "  " + steelCell(age(x.run.CreatedAt, time.Now())+" ago", false).Styled)
		p.add("")
		p.add(keycap("enter") + sText.Render(" open the run  ") + keycap("r") + sText.Render(" its repo"))
		return p.render(h)
	case len(g.kinds) == 1:
		v.kindPanel(a, p, g.kinds[0])
	default:
		v.groupPanel(a, p, g)
	}
	if via := g.viaList(); len(via) > 0 {
		p.label("Source")
		for _, ref := range via {
			name := sText.Render(actionName(ref))
			if len(a.yourWorkflows([]string{ref})) > 0 {
				name = sGold.Bold(true).Render(actionName(ref)) + sDim.Render(" yours")
			}
			p.row(ico("code")+name, sText.Render(fmt.Sprint(len(g.via[ref]))))
		}
	}
	p.add("")
	p.add(keycap("enter") + sText.Render(" the runs that gave it"))
	return p.render(h)
}

func (v *warningsView) groupPanel(a *App, p *panelText, g *warnGroup) {
	for i, l := range wrapLine(patternStyled(g.pattern), max(p.inner(), 10)) {
		if i == 4 {
			break
		}
		p.add(l)
	}
	p.add("")
	p.add(statusChip(KWait, 0, repoCount(len(g.repos))) + sDim.Render(fmt.Sprintf(" · %d kinds · seen ", len(g.kinds))) + sText.Render(age(g.newest, time.Now())+" ago"))
	p.label("Variants")
	for _, k := range g.kinds {
		p.row(sText.Render(k.text), sDim.Render(repoCount(len(k.repos))))
	}
}

func (v *warningsView) kindPanel(a *App, p *panelText, k *warnKind) {
	for i, l := range strings.Split(ansi.Wordwrap(ansi.Strip(k.full), max(p.inner(), 10), ""), "\n") {
		if i == 8 {
			p.add(sDim.Render("…"))
			break
		}
		st := sText
		if i == 0 {
			st = sWhite.Bold(true)
		}
		p.add(st.Render(l))
	}
	p.add("")
	p.add(statusChip(KWait, 0, repoCount(len(k.repos))))
}
