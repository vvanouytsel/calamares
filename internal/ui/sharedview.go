package ui

import (
	"fmt"
	"path"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// sharedView lists shared workflows or actions as a tree: each one, then a
// line per version in use. It is a view of the Actions tab for all repos, or
// a screen of its own for the workflows one repo uses.
type sharedView struct {
	id       int
	repo     string // only what this repo uses, or "" for all
	actions  bool
	everyone bool // actions: also those of other owners, not only yours
	t        *Table
	items    []*sharedItem // those listed, in their order
	total    int           // those there are before the filter
	held     sharedHeld
	query    string
	behind   bool                // only what some repo is behind on
	grouped  bool                // under the repo they live in
	order    string              // a key of sharedOrders
	matched  map[string][]string // per version line, the repos the query matched
}

// sharedOrders are the ways the list can be sorted, the first the default.
var sharedOrders = []struct{ key, label string }{
	{"used", "most used"}, {"behind", "most behind"}, {"failing", "failing first"}, {"last", "last run"}, {"name", "name"},
}

func newSharedView(a *App, repo string, actions bool) *sharedView {
	v := &sharedView{id: a.newID(), repo: repo, actions: actions, order: "used"}
	if actions {
		v.t = newTable(
			Column{Title: "ACTION", Max: 50, Flex: true, Help: "an action your repos use, on the line of the first version in use"},
			Column{Title: "VERSION", Max: 24, Help: "a version in use; latest marks the newest of them"},
			Column{Title: "REPOS", Right: true, Help: "how many repositories use that version"},
			Column{Title: "STATUS", Max: 40, Flex: true, Help: "Outdated: other repositories use a newer version; Up to date: none does"},
		)
	} else {
		v.t = newTable(
			Column{Title: "WORKFLOW", Max: 40, Flex: true, Help: "a reusable workflow your repos call, on the line of the first version in use"},
			Column{Title: "VERSION", Max: 24, Help: "a version in use; latest marks the newest of them"},
			Column{Title: "REPOS", Right: true, Help: "how many repositories call that version"},
			Column{Title: "PASSED", Help: "how many of the finished runs that called it passed; a calling run counts as a whole, its own jobs too"},
			Column{Title: "STATUS", Max: 40, Flex: true, Help: "Fails more than v8: passes clearly less often than the version before it; Slower than v8: takes clearly longer; Outdated: other repositories use a newer version; Fails often: fewer than 3 in 4 runs pass"},
		)
	}
	return v
}

func (v *sharedView) ID() int             { return v.id }
func (v *sharedView) Init(a *App) tea.Cmd { return a.st.loadUses(a, false) }

func (v *sharedView) Crumb() string {
	if v.actions {
		return "shared actions"
	}
	return "shared workflows"
}

func (v *sharedView) title() string {
	if v.actions {
		return "Shared actions"
	}
	return "Shared workflows"
}

// Refresh reads the workflow files again when asked.
func (v *sharedView) Refresh(a *App, force bool) tea.Cmd { return a.st.loadUses(a, force) }

func (v *sharedView) Loading(a *App) bool { return a.st.usesState.busy }

func (v *sharedView) table() *Table                   { return v.t }
func (v *sharedView) load(a *App, force bool) tea.Cmd { return a.st.loadUses(a, force) }
func (v *sharedView) busy(a *App) bool                { return a.st.usesState.busy }
func (v *sharedView) count(a *App) int                { return len(v.items) }

func (v *sharedView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return tea.Batch(cmd, v.afterMove(a))
	}
	return nil
}

func (v *sharedView) Keys(a *App) []Binding {
	return grouped(strings.ToUpper(v.title()), append(v.keys(a), filterHint, v.t.moveKeys())...)
}

func (v *sharedView) SetFilter(a *App, q string) { v.query = q; v.t.Top() }
func (v *sharedView) FilterValue() string        { return v.query }
func (v *sharedView) FilterInfo(a *App) filterInfo {
	return filterInfo{shown: len(v.items), total: v.total, fields: []string{"repo", "source", "version", "is"}, values: v.fieldValues}
}

// fieldValues lists what repo:, source:, version: and is: can be set to.
func (v *sharedView) fieldValues(field string) []filterOption {
	items := v.held.workflows
	if v.actions {
		items = v.held.actions
	}
	var values []string
	for _, it := range items {
		if field == "source" {
			values = append(values, it.repo)
		}
		for _, ver := range it.versions {
			if field == "version" {
				values = append(values, ver.version)
			}
			for _, u := range ver.users {
				if field == "repo" {
					values = append(values, u.repo)
				}
			}
		}
	}
	if field == "is" {
		return fixedSuggestions("behind", "failing", "pinned")
	}
	return countSuggestions(values)
}

// selected is the item under the cursor and, on a version line, the version.
func (v *sharedView) selected() (*sharedItem, *sharedVersion) {
	r := v.t.Selected()
	if r == nil {
		return nil, nil
	}
	if strings.HasPrefix(r.Key, groupKeyPrefix) {
		return nil, nil
	}
	key, version, onVersion := strings.Cut(r.Key, "\x00")
	for _, it := range v.items {
		if it.key() == key {
			if onVersion {
				return it, it.version(version)
			}
			return it, nil
		}
	}
	return nil, nil
}

// keys act on the item or the version under the cursor.
func (v *sharedView) keys(a *App) []Binding {
	it, ver := v.selected()
	if it == nil {
		return v.treeKeys()
	}
	version := it.versions[0].version
	if ver != nil {
		version = ver.version
	}
	bs := []Binding{{Keys: []string{"enter", "right"}, Label: "enter", Desc: "used by", Help: "the workflows that use it, at every version",
		Do: do(func() tea.Cmd { return a.push(newSharedUsersView(a, it, nil)) })}}
	if !v.actions {
		desc := "runs"
		if ver != nil {
			desc = "runs at " + shortVersion(ver.version)
		}
		bs = append(bs, Binding{Keys: []string{"r"}, Desc: desc, Help: "the runs that called it",
			Do: do(func() tea.Cmd { return a.push(newSharedRunsView(a, it, ver)) })})
	}
	if c, ok := v.compared(it, ver); ok {
		bs = append(bs, Binding{Keys: []string{"="}, Desc: shortVersion(c.prev.version) + "…" + shortVersion(c.ver.version),
			Help: "what changed since the version before it: commits and files",
			Do:   do(func() tea.Cmd { return a.push(newCompareView(a, it.repo, c.prev.version, c.ver.version)) })})
	}
	bs = append(bs, Binding{Keys: []string{"s"}, Desc: "source repo", Help: "open the repo it lives in",
		Do: do(func() tea.Cmd { return a.push(newRepoView(a, it.repo, "")) })})
	if !it.action {
		bs = append(bs, Binding{Keys: []string{"f"}, Desc: "file", Help: "show the workflow file at this version",
			Do: do(func() tea.Cmd { return a.push(newWorkflowSourceView(a, it.repo, it.path, version)) })})
	}
	bs = append(bs, Binding{Keys: []string{"o"}, Desc: "browser", Help: "open it in the browser",
		Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(it.repo + "/tree/" + version + "/" + it.path)) })})
	return append(bs, v.treeKeys()...)
}

// compared is the check of the version under the cursor, or on an item line
// of its newest version, when there is a version before it to compare with.
func (v *sharedView) compared(it *sharedItem, ver *sharedVersion) (versionCheck, bool) {
	if it == nil || it.action {
		return versionCheck{}, false
	}
	if ver == nil {
		if ver = it.newest(); ver == nil {
			return versionCheck{}, false
		}
	}
	c := it.check(ver)
	return c, c.prev != nil
}

// afterMove reads what explains the comparison under the cursor.
func (v *sharedView) afterMove(a *App) tea.Cmd {
	it, ver := v.selected()
	if c, ok := v.compared(it, ver); ok {
		return a.st.readCheck(a, it, c.ver)
	}
	return nil
}

func (v *sharedView) treeKeys() []Binding {
	bs := []Binding{
		{Keys: []string{"b"}, Desc: "outdated only", Help: "only the versions other repos have moved past / everything",
			Do: do(func() tea.Cmd { v.behind = !v.behind; v.t.Top(); return nil })},
		{Keys: []string{"g"}, Desc: "group by source", Help: "under the repo they live in / one list",
			Do: do(func() tea.Cmd { v.grouped = !v.grouped; v.t.Top(); return nil })},
		{Keys: []string{"S"}, Desc: "sort", Help: "sort by use, how far behind, failing, last run or name",
			Do: do(func() tea.Cmd { return v.pickOrder() })},
	}
	if v.actions {
		desc := "everyone's"
		if v.everyone {
			desc = "only yours"
		}
		bs = append(bs, Binding{Keys: []string{"e"}, Desc: desc, Help: "actions of every owner / only of yours",
			Do: do(func() tea.Cmd { v.everyone = !v.everyone; v.t.Top(); return nil })})
	}
	return bs
}

func (v *sharedView) pickOrder() tea.Cmd {
	var items []pickItem
	for _, o := range sharedOrders {
		items = append(items, pickItem{value: o.key, label: o.label})
	}
	return openModal(newPicker("shared-order", "Sort by", v.order, items, func(key string) tea.Cmd {
		v.order = key
		v.t.Top()
		return nil
	}))
}

func (v *sharedView) orderLabel() string {
	for _, o := range sharedOrders {
		if o.key == v.order {
			return o.label
		}
	}
	return sharedOrders[0].label
}

// yours says whether an action belongs to the owner of most of your repos.
func (v *sharedView) yours(a *App, it *sharedItem) bool {
	owner := a.owner()
	return owner == "" || strings.HasPrefix(strings.ToLower(it.repo), strings.ToLower(owner)+"/")
}

func (v *sharedView) sync(a *App) {
	v.held = heldShared(a)
	list := v.held.workflows
	if v.actions {
		list = v.held.actions
	}
	q := parseSharedQuery(v.query)
	v.items, v.total, v.matched = nil, 0, map[string][]string{}
	shown := map[string][]*sharedVersion{}
	for _, it := range list {
		if (v.repo != "" && !it.usedBy(v.repo)) || (v.actions && !v.everyone && !v.yours(a, it)) {
			continue
		}
		v.total++
		var vers []*sharedVersion
		for _, ver := range it.versions {
			if v.repo != "" && !ver.usedBy(v.repo) || v.behind && !ver.behind {
				continue
			}
			if repos, ok := q.match(a, it, ver); ok {
				vers = append(vers, ver)
				v.matched[it.key()+"\x00"+ver.version] = repos
			}
		}
		if len(vers) > 0 {
			v.items = append(v.items, it)
			shown[it.key()] = vers
		}
	}
	sortShared(v.items, v.order)
	if v.grouped {
		sortByGroup(v.items)
	}
	now := time.Now()
	var rows []Row
	group := ""
	for _, it := range v.items {
		if v.grouped && !strings.EqualFold(it.repo, group) {
			group = it.repo
			rows = append(rows, v.groupRow(a, it.repo))
		}
		for i, ver := range shown[it.key()] {
			rows = append(rows, v.versionRow(a, it, ver, i == 0, now))
		}
	}
	v.t.Empty = v.empty(a)
	v.t.SetRows(rows)
}

func (ver *sharedVersion) usedBy(repo string) bool {
	for _, u := range ver.users {
		if strings.EqualFold(u.repo, repo) {
			return true
		}
	}
	return false
}

func (v *sharedView) empty(a *App) string {
	switch {
	case !a.st.loaded:
		return "Loading…"
	case v.actions && !v.held.fromFiles && a.st.usesState.busy:
		return "Reading the workflow files of your repos…"
	case v.actions && !v.held.fromFiles:
		return "The workflow files are not read yet; ctrl+r reads them."
	case v.actions:
		return "None of the workflow files use an action of yours; e shows everyone's."
	}
	return "None of your repos use a shared workflow."
}

// adoptionWidth is how many cells an adoption bar takes.
const adoptionWidth = 18

// groupKeyPrefix starts the key of a group's heading line.
const groupKeyPrefix = "group\x00"

// groupRow heads the items of one source repo: its name, how many it holds
// and how many some repo is behind on.
func (v *sharedView) groupRow(a *App, repo string) Row {
	n, behind := 0, 0
	for _, it := range v.items {
		if strings.EqualFold(it.repo, repo) {
			n++
			if it.reposBehind() > 0 {
				behind++
			}
		}
	}
	what := "workflow"
	if v.actions {
		what = "action"
	}
	name := icoIn("repo", cGold) + sGold.Bold(true).Render(a.short(repo))
	about := sDim.Render(fmt.Sprintf("%d %s", n, plural(what, n)))
	if behind > 0 {
		about += sDim.Render("  ·  ") + sWait.Render(fmt.Sprintf("%d with an outdated version in use", behind))
	}
	// The name, then what it holds in the last column, STATUS.
	cells := make([]Cell, len(v.t.Cols))
	cells[0], cells[len(cells)-1] = Cell{Text: ansi.Strip(name), Styled: name}, Cell{Text: ansi.Strip(about), Styled: about}
	return Row{Key: groupKeyPrefix + strings.ToLower(repo), Kind: KOK, Cells: cells}
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	if stem, ok := strings.CutSuffix(word, "y"); ok && stem != "" && !strings.ContainsAny(stem[len(stem)-1:], "aeiou") {
		return stem + "ies" // repository, repositories
	}
	return word + "s"
}

// versionRow is the line of a version in use: the name of the item on the
// line of its first version, how many repos use the version, how many of the
// runs that called it passed and its status.
func (v *sharedView) versionRow(a *App, it *sharedItem, ver *sharedVersion, first bool, now time.Time) Row {
	name := Cell{}
	if first {
		name = Cell{Text: it.name(), Styled: sWhite.Bold(true).Render(it.name())}
	}
	version := versionText(ver.version)
	switch {
	case ver == it.newest() && len(it.versions) > 1:
		version += sDim.Render(" latest")
	case refKind(ver.version) != "":
		version += sDim.Render(" " + refKind(ver.version))
	}
	if repos := v.matched[it.key()+"\x00"+ver.version]; len(repos) > 0 {
		version += "  " + lipgloss.NewStyle().Foreground(cWhite).Background(cMatchBg).Render(a.short(repos[0]))
		if len(repos) > 1 {
			version += sDim.Render(fmt.Sprintf(" and %d more", len(repos)-1))
		}
	}
	status, k := v.status(it, ver)
	cells := []Cell{name, {Text: ansi.Strip(version), Styled: version}, reposCell(len(ver.users), false)}
	if !it.action {
		cells = append(cells, passedCell(ver.calls, k == KFail))
	}
	cells = append(cells, Cell{Text: status, Styled: fg(k.Color()).Render(status)})
	return Row{Key: it.key() + "\x00" + ver.version, Kind: KOK, Cells: cells}
}

// status says in a word or two how a version does, and in which colour.
func (v *sharedView) status(it *sharedItem, ver *sharedVersion) (string, Kind) {
	c := it.check(ver)
	pass := passingRate(callKinds(ver.calls))
	switch {
	case !it.action && c.prev != nil && c.verdict == verdictWorse:
		return "Fails more than " + shortVersion(c.prev.version), KFail
	case !it.action && c.prev != nil && c.verdict == verdictSlower:
		return "Slower than " + shortVersion(c.prev.version), KRun
	case ver.behind:
		return "Outdated", KWait
	case it.action:
		return "Up to date", KOK
	case len(ver.calls) == 0:
		return "Not run lately", KNone
	case c.fresh && c.verdict == verdictEarly:
		return "Too new to compare", KNone
	case pass >= 0 && finishedCalls(ver.calls) >= checkMinCalls && pass < oftenFails:
		return "Fails often", KFail
	}
	return "Healthy", KOK
}

// oftenFails is the share of passed runs under which a version fails often.
const oftenFails = 75

// passedCell is "9 of 17": the calls that passed of those that finished, in
// red when the version fails.
func passedCell(calls []sharedCall, failing bool) Cell {
	passed, done := passedOf(callKinds(calls))
	if done == 0 {
		return Cell{Text: "—", Styled: sDim.Render("—")}
	}
	t := fmt.Sprintf("%d of %d", passed, done)
	st := sText
	if failing {
		st = sRed
	}
	return Cell{Text: t, Styled: st.Render(t)}
}

// refKind names what a version is when it is not a tag.
func refKind(version string) string {
	switch {
	case commitSHA.MatchString(version):
		return "commit"
	case version != "" && versionParts(version) == nil:
		return "branch"
	}
	return ""
}

// behindPill marks a version others have moved past, and to which one when
// ahead is set.
func behindPill(ahead string) string {
	label := "outdated"
	if ahead != "" {
		label += ", " + shortVersion(ahead) + " is newer"
	}
	return sWait.Render(label)
}

// reposCell counts repos: with the word on a shared item, bare on a version.
func reposCell(n int, words bool) Cell {
	if !words {
		t := fmt.Sprint(n)
		return Cell{Text: t, Styled: sText.Render(t)}
	}
	if n == 0 {
		return Cell{Text: "no repos", Styled: sDim.Render("no repos")}
	}
	t := repoCount(n)
	num, rest, _ := strings.Cut(t, " ")
	return Cell{Text: t, Styled: sWhite.Render(num) + " " + sText.Render(rest)}
}

// barCells draws n cells of a bar, each 7/8 wide so a thin seam parts them.
func barCells(n int) string {
	if ascii {
		return strings.Repeat("#", n)
	}
	return strings.Repeat("▉", n)
}

// upToDate is the share of the repos using it that are on a version nobody is
// ahead of, and that version when there is one.
func (s *sharedItem) upToDate() (pct int, on string) {
	total := s.repoCount()
	if total == 0 {
		return -1, ""
	}
	current := map[string]bool{}
	var versions []string
	for _, ver := range s.versions {
		if ver.behind || (versionParts(ver.version) == nil && ver != s.versions[0]) {
			continue
		}
		versions = append(versions, ver.version)
		for _, u := range ver.users {
			current[strings.ToLower(u.repo)] = true
		}
	}
	if len(versions) == 1 {
		on = versions[0]
	}
	return len(current) * 100 / total, on
}

// chips count the versions that fail and the repos on an outdated version.
func (v *sharedView) chips(a *App) []string {
	failing := 0
	outdated := map[string]bool{}
	for _, it := range v.items {
		for _, ver := range it.versions {
			if _, k := v.status(it, ver); k == KFail {
				failing++
			}
			if ver.behind {
				for _, u := range ver.users {
					outdated[strings.ToLower(u.repo)] = true
				}
			}
		}
	}
	var chips []string
	if failing > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failing %s", failing, plural("version", failing))))
	}
	if n := len(outdated); n > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d outdated %s", n, plural("repository", n))))
	}
	return chips
}

// coverage says where the list comes from.
func (v *sharedView) coverage(a *App) string {
	if !v.held.fromFiles {
		s := fmt.Sprintf("seen in the last %d runs of %s", repoStatusFilter.PerPage, repoCount(v.held.repos))
		if a.st.usesState.busy {
			s += " · reading the workflow files…"
		}
		return s
	}
	s := fmt.Sprintf("from the workflow files of %s · read %s ago", repoCount(v.held.repos), age(a.st.usesState.at, time.Now()))
	if a.st.usesState.busy {
		s += " · reading again…"
	}
	return s
}

// listHead is the line above the list: what it shows, the filter and where
// it comes from.
func (v *sharedView) listHead(a *App) string {
	var parts []string
	if v.actions {
		which := "only yours"
		if v.everyone {
			which = "everyone's"
		}
		parts = append(parts, chip("e", which+" ▾", v.everyone))
	}
	parts = append(parts, chip("b", "behind only", v.behind), chip("g", "group by source", v.grouped),
		chip("S", "sort: "+v.orderLabel()+" ▾", v.order != sharedOrders[0].key),
		a.filterLine(v.query, v.FilterInfo(a)))
	return strings.Join(parts, "   ")
}

func (v *sharedView) Render(a *App, w, h int) string {
	scope := v.coverage(a)
	if v.repo != "" {
		scope = "used by " + a.short(v.repo) + " · " + scope
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("workflow", v.title(), scope, len(v.items), v.chips(a), w), " " + v.listHead(a)},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

// panel describes the version under the cursor, or a source repo when the
// list is grouped.
func (v *sharedView) panel(a *App, w, h int) []string {
	it, ver := v.selected()
	if r := v.t.Selected(); it == nil && r != nil && strings.HasPrefix(r.Key, groupKeyPrefix) {
		return v.groupPanel(a, strings.TrimPrefix(r.Key, groupKeyPrefix), w, h)
	}
	if it == nil {
		return emptyPanel(w, h, "Nothing selected.")
	}
	if ver == nil {
		ver = it.versions[0]
	}
	p := newLayerPanel(w)
	v.versionPanel(a, p, it, ver)
	return p.render(h)
}

// groupPanel sums up what one source repo shares: each item with how many
// repos use it and how many of those are on an outdated version.
func (v *sharedView) groupPanel(a *App, repo string, w, h int) []string {
	p := newLayerPanel(w)
	p.add(icoIn("repo", cGold) + sGold.Bold(true).Render(a.short(repo)))
	p.add(sDim.Render("Source repository"))
	p.label(v.title())
	for _, it := range v.items {
		if !strings.EqualFold(it.repo, repo) {
			continue
		}
		right := ""
		if n := it.reposBehind(); n > 0 {
			right = sWait.Render(fmt.Sprintf("%d outdated", n))
		}
		p.row(sWhite.Render(it.name())+sDim.Render("  "+repositories(it.repoCount())), right)
	}
	return p.render(h)
}

// namesSection lists shared items by key, under title.
func (v *sharedView) namesSection(a *App, p *panelText, title string, keys []string) {
	if len(keys) == 0 {
		return
	}
	p.label(title)
	var names []string
	for _, key := range keys {
		name := path.Base(key)
		if it, ok := v.held.byKey[key]; ok {
			name = it.name()
		}
		names = append(names, sWhite.Render(name))
	}
	p.add(strings.Join(names, sDim.Render(" · ")))
}

// callsSummary draws how the calls went, under title.
func callsSummary(a *App, p *panelText, title string, calls []sharedCall, usual time.Duration) {
	var points []historyPoint
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		points = append(points, historyPoint{kind: StatusKind(c.run.Status, c.run.Conclusion), at: c.run.CreatedAt, took: c.run.Duration(time.Now()),
			label: fmt.Sprintf("%s #%d", a.short(c.repo), c.run.RunNumber)})
	}
	historySection(a, p, title, points, usual)
}

// versionPanel tells about a version: its status and facts, how it compares
// with the version before it, the repos on it, how its runs went and what
// you can do with it.
func (v *sharedView) versionPanel(a *App, p *panelText, it *sharedItem, ver *sharedVersion) {
	now := time.Now()
	p.add(sWhite.Bold(true).Render(it.name()) + " " + versionText(ver.version))
	where := "Shared workflow · " + it.repo + " · " + path.Dir(it.path)
	if it.action {
		where = "Action · " + it.repo
	}
	p.add(sDim.Render(where))
	p.add("")
	status, k := v.status(it, ver)
	p.field("Status", fg(k.Color()).Render(status))
	c := it.check(ver)
	if c.fresh {
		p.field("First used", sText.Render(agoWords(c.first, now)))
	}
	p.field("Used by", sText.Render(repositories(len(ver.users))))
	if ver.behind && ver.ahead != "" {
		newest := versionText(ver.ahead)
		if nv := it.version(ver.ahead); nv != nil {
			newest += sDim.Render(", in " + repositories(len(nv.users)))
		}
		p.field("Newer", newest)
	}
	if !it.action {
		if _, done := passedOf(callKinds(ver.calls)); done > 0 {
			p.field("Passed", passedCell(ver.calls, k == KFail).Styled+sText.Render(" "+plural("run", done)))
		}
		if c.prev != nil {
			if _, done := passedOf(callKinds(c.prev.calls)); done > 0 {
				p.field("On "+shortVersion(c.prev.version), passedCell(c.prev.calls, false).Styled+sText.Render(" "+plural("run", done)))
			}
		}
		if c.med > 0 {
			typical := sText.Render(dur(c.med))
			if c.prev != nil && c.prevMed > 0 {
				typical += sDim.Render(", " + shortVersion(c.prev.version) + " " + dur(c.prevMed))
			}
			p.field("Typical run", typical)
		}
		if last, ok := lastCall(ver.calls, ""); ok {
			p.field("Last run", sText.Render(agoWords(last.run.CreatedAt, now)))
		}
	}
	cmp, compared := v.compared(it, ver)
	if compared {
		comparePanel(a, p, it, cmp)
	}
	p.label("Repositories on " + shortVersion(ver.version))
	for i, u := range ver.users {
		if i == 12 {
			p.add(sDim.Render(fmt.Sprintf("… %d more", len(ver.users)-i)))
			break
		}
		file := sDim.Render(path.Base(u.file))
		if it.action {
			p.row(fact("repo", a.short(u.repo))+" "+file, "")
			continue
		}
		if c, ok := lastCall(ver.calls, u.repo); ok {
			k := StatusKind(c.run.Status, c.run.Conclusion)
			p.row(glyph(k, a.frame)+" "+fact("repo", a.short(u.repo))+" "+file, fg(cSteel).Render(age(c.run.CreatedAt, now)))
		} else {
			p.row(sDim.Render("·")+" "+fact("repo", a.short(u.repo))+" "+file, sDim.Render("not run lately"))
		}
	}
	v.namesSection(a, p, "Shared workflows it calls", it.nested)
	v.namesSection(a, p, "Used in shared workflows", it.inside)
	if !it.action && len(ver.calls) > 0 {
		callsSummary(a, p, "Run history", ver.calls, callMedian(ver.calls))
	}
	p.label("Next steps")
	if compared {
		p.add(keycap("=") + sText.Render(" Compare "+shortVersion(cmp.prev.version)+" and "+shortVersion(cmp.ver.version)))
	}
	if it.action {
		p.add(keycap("s") + sText.Render(" Open the source repository"))
		return
	}
	p.add(keycap("r") + sText.Render(" View the runs at "+shortVersion(ver.version)))
	p.add(keycap("f") + sText.Render(" Show the file") + "   " + keycap("s") + sText.Render(" Open the source repository"))
}

// sharedRunsView lists the runs we hold that called a shared workflow, at
// one version or at any.
type sharedRunsView struct {
	id      int
	key     string
	name    string
	version string
	any     bool // every version
	t       *Table
	peek    peeker
	runs    map[string]repoRun
}

func newSharedRunsView(a *App, it *sharedItem, ver *sharedVersion) *sharedRunsView {
	v := &sharedRunsView{id: a.newID(), key: it.key(), name: it.name(), any: ver == nil}
	if ver != nil {
		v.version = ver.version
		v.name += " " + shortVersion(ver.version)
	}
	v.t = newTable(runColumns()...)
	return v
}

func (v *sharedRunsView) ID() int                            { return v.id }
func (v *sharedRunsView) Crumb() string                      { return v.name }
func (v *sharedRunsView) Init(a *App) tea.Cmd                { return nil }
func (v *sharedRunsView) Refresh(a *App, force bool) tea.Cmd { return nil }
func (v *sharedRunsView) SetFilter(a *App, q string)         { v.t.SetFilter(q, a.me) }
func (v *sharedRunsView) FilterValue() string                { return v.t.Filter() }
func (v *sharedRunsView) FilterInfo(a *App) filterInfo       { return v.t.Info() }

// calls are the runs the view lists, and the item they called.
func (v *sharedRunsView) calls(a *App) (*sharedItem, []sharedCall) {
	it, ok := heldShared(a).byKey[v.key]
	if !ok {
		return nil, nil
	}
	if v.any {
		return it, it.calls
	}
	if ver := it.version(v.version); ver != nil {
		return it, ver.calls
	}
	return it, nil
}

func (v *sharedRunsView) sync(a *App) {
	now := time.Now()
	v.runs = map[string]repoRun{}
	var rows []Row
	_, calls := v.calls(a)
	for _, c := range calls {
		key := fmt.Sprint(c.run.ID)
		v.runs[key] = *c.repoRun
		rows = append(rows, activityRow(a, key, *c.repoRun, now, false))
	}
	v.t.Empty = "None of the loaded runs called it."
	v.t.SetRows(rows)
}

func (v *sharedRunsView) selected() (repoRun, bool) {
	r := v.t.Selected()
	if r == nil {
		return repoRun{}, false
	}
	x, ok := v.runs[r.Key]
	return x, ok
}

func (v *sharedRunsView) Keys(a *App) []Binding {
	var bs []Binding
	if x, ok := v.selected(); ok {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run",
			Do: do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })})
		bs = append(bs, Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })})
		bs = append(bs, a.runKeys(x.repo, x.run)...)
	}
	return grouped("RUNS", append(bs, filterHint, v.t.moveKeys())...)
}

func (v *sharedRunsView) Update(a *App, msg tea.Msg) tea.Cmd {
	if cmd, ok := v.peek.handle(a, v.id, msg); ok {
		return cmd
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		if x, ok := v.selected(); ok {
			return tea.Batch(cmd, v.peek.ask(a, v.id, x.repo, &x.run))
		}
		return cmd
	}
	return nil
}

func (v *sharedRunsView) Render(a *App, w, h int) string {
	scope := ""
	var chips []string
	if it, calls := v.calls(a); it != nil {
		scope = it.repo
		if pct := passingRate(callKinds(calls)); pct >= 0 {
			chips = append(chips, rateStyle(pct).Bold(true).Render(fmt.Sprintf("%d%% passing", pct))+" "+meter(pct, 8))
		}
		if med := callMedian(calls); med > 0 {
			chips = append(chips, fact("clock", "median "+dur(med)))
		}
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("workflow", v.name, scope, v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string {
			if x, ok := v.selected(); ok {
				return a.runPanel(x, pw, ph)
			}
			return emptyPanel(pw, ph, "No run selected.")
		})
}

// sharedUsersView lists the workflows that use a shared workflow or action:
// the repo, the file and the version it is used at, at one version or any.
type sharedUsersView struct {
	id      int
	key     string
	name    string
	version string
	any     bool // every version
	users   []sharedUserAt
	item    *sharedItem
	t       *Table
}

type sharedUserAt struct {
	sharedUser
	ver *sharedVersion
}

func newSharedUsersView(a *App, it *sharedItem, ver *sharedVersion) *sharedUsersView {
	v := &sharedUsersView{id: a.newID(), key: it.key(), name: it.name(), any: ver == nil}
	if ver != nil {
		v.version = ver.version
		v.name += " " + shortVersion(ver.version)
	}
	cols := []Column{
		{Title: "REPO", Max: 40, Flex: true, MidCut: true},
		{Title: "WORKFLOW", Max: 40, Flex: true},
		{Title: "VERSION", Max: 30, Flex: true},
	}
	if !it.action {
		cols = append(cols, Column{Title: "LAST RUN", Min: 10})
	}
	v.t = newTable(cols...)
	return v
}

func (v *sharedUsersView) ID() int                            { return v.id }
func (v *sharedUsersView) Crumb() string                      { return v.name }
func (v *sharedUsersView) Init(a *App) tea.Cmd                { return nil }
func (v *sharedUsersView) Refresh(a *App, force bool) tea.Cmd { return nil }
func (v *sharedUsersView) SetFilter(a *App, q string)         { v.t.SetFilter(q, a.me) }
func (v *sharedUsersView) FilterValue() string                { return v.t.Filter() }
func (v *sharedUsersView) FilterInfo(a *App) filterInfo       { return v.t.Info() }

func (v *sharedUsersView) sync(a *App) {
	v.users, v.item = nil, heldShared(a).byKey[v.key]
	var rows []Row
	if v.item != nil {
		now := time.Now()
		for _, ver := range v.item.versions {
			if !v.any && ver.version != v.version {
				continue
			}
			for _, u := range ver.users {
				rows = append(rows, v.row(a, len(v.users), u, ver, now))
				v.users = append(v.users, sharedUserAt{u, ver})
			}
		}
	}
	v.t.Empty = "No workflow uses it."
	v.t.SetRows(rows)
}

func (v *sharedUsersView) row(a *App, i int, u sharedUser, ver *sharedVersion, now time.Time) Row {
	version := versionText(ver.version)
	if ver.behind {
		version += "  " + behindPill("")
	}
	cells := []Cell{
		iconText("repo", a.short(u.repo), false),
		iconText("file", path.Base(u.file), false),
		{Text: ansi.Strip(version), Styled: version},
	}
	if !v.item.action {
		cells = append(cells, lastRunCell(a, ver, u.repo, now))
	}
	return Row{Key: fmt.Sprint(i), Kind: KOK, Cells: cells}
}

// lastRunCell is how repo's last call at ver went, and when.
func lastRunCell(a *App, ver *sharedVersion, repo string, now time.Time) Cell {
	c, ok := lastCall(ver.calls, repo)
	if !ok {
		return Cell{Text: "not run lately", Styled: sDim.Render("not run lately")}
	}
	k := StatusKind(c.run.Status, c.run.Conclusion)
	styled := fg(k.Color()).Render(k.Glyph(a.frame)+" "+k.Label()) + "  " + fg(cSteel).Render(age(c.run.CreatedAt, now))
	return Cell{Text: ansi.Strip(styled), Styled: styled}
}

func (v *sharedUsersView) selected() (sharedUserAt, bool) {
	r := v.t.Selected()
	if r == nil {
		return sharedUserAt{}, false
	}
	var i int
	fmt.Sscan(r.Key, &i)
	if i >= len(v.users) {
		return sharedUserAt{}, false
	}
	return v.users[i], true
}

// openWorkflow opens the runs of the workflow that uses it, or the repo when
// that workflow isn't known yet.
func (v *sharedUsersView) openWorkflow(a *App, u sharedUserAt) tea.Cmd {
	wfs := a.workflows[u.repo]
	if len(wfs) == 0 {
		wfs, _ = a.client.CachedWorkflows(u.repo)
	}
	for _, wf := range wfs {
		if wf.Path == u.file {
			return a.push(newRunsView(a, u.repo, &wf, ""))
		}
	}
	return a.push(newRepoView(a, u.repo, ""))
}

func (v *sharedUsersView) Keys(a *App) []Binding {
	var bs []Binding
	if u, ok := v.selected(); ok {
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "its runs", Help: "the runs of the workflow that uses it",
				Do: do(func() tea.Cmd { return v.openWorkflow(a, u) })},
			Binding{Keys: []string{"f"}, Desc: "file", Help: "show the file that uses it", Do: do(func() tea.Cmd { return a.push(newWorkflowSourceView(a, u.repo, u.file, "")) })},
			Binding{Keys: []string{"s"}, Desc: "repo", Help: "open the repo", Do: do(func() tea.Cmd { return a.push(newRepoView(a, u.repo, "")) })},
			Binding{Keys: []string{"o"}, Desc: "browser", Help: "open the file in the browser",
				Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(u.repo + "/blob/HEAD/" + u.file)) })},
		)
	}
	return grouped("USED BY", append(bs, filterHint, v.t.moveKeys())...)
}

func (v *sharedUsersView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

func (v *sharedUsersView) Render(a *App, w, h int) string {
	var chips []string
	if v.item != nil {
		behind := 0
		for _, u := range v.users {
			if u.ver.behind {
				behind++
			}
		}
		if behind > 0 {
			chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d behind", behind)))
		}
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("workflow", v.name, "used by", v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

// panel describes the workflow under the cursor and how it uses the item.
func (v *sharedUsersView) panel(a *App, w, h int) []string {
	u, ok := v.selected()
	if !ok || v.item == nil {
		return emptyPanel(w, h, "Nothing selected.")
	}
	p := newPanel(w)
	p.add(sWhite.Bold(true).Render(path.Base(u.file)))
	p.add(fact("repo", a.short(u.repo)) + sDim.Render(" · "+path.Dir(u.file)))
	p.add("")
	p.add(sDim.Render("uses ") + sText.Render(v.item.name()) + " " + versionText(u.ver.version))
	if u.ver.behind {
		p.callout(cWait, sWait.Bold(true).Render("behind "+shortVersion(u.ver.ahead)),
			keycap("f")+sText.Render(" its file  ")+keycap("o")+sText.Render(" in the browser"))
	}
	if !v.item.action {
		var calls []sharedCall
		for _, c := range u.ver.calls {
			if strings.EqualFold(c.repo, u.repo) {
				calls = append(calls, c)
			}
		}
		if len(calls) > 0 {
			callsSummary(a, p, "Run history", calls, callMedian(calls))
		}
	}
	return p.render(h)
}
