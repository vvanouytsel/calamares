package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// reposView is the Repos tab: your repositories that use GitHub Actions
// (all of them with "A"), with how the last commits on their default branch
// did. One GraphQL sweep reads it all.
type reposView struct {
	id      int
	t       *Table
	showAll bool
	order   string    // how the list is sorted; see repoOrders
	listed  []gh.Repo // the repos in the table, in order
}

// repoOrders are the ways Repos sorts, the first the default.
var repoOrders = []struct{ key, label string }{
	{"pinned", "pinned, then pushed"}, {"pushed", "last pushed"}, {"failing", "failing first"}, {"name", "name"},
}

// reposLiveEvery is how often the repo list refreshes while live.
const reposLiveEvery = 5 * time.Minute

func newReposView(a *App) *reposView {
	v := &reposView{id: a.newID(), order: repoOrders[0].key}
	v.t = newTable(
		Column{Title: ""},
		Column{Title: "REPO", Max: 44, Flex: true, MidCut: true, Help: "the repository: a star marks one you pinned, a lock one that is private, and a lock after the name one you can only read"},
		Column{Title: "DEFAULT BRANCH", Help: "how the default branch is doing now: failing when the latest run of one of its workflows failed, running while a workflow runs or the checks of its last commit do, and passing otherwise"},
		Column{Title: "RECENT COMMITS", Help: "a tick per recent commit on the default branch, oldest at the left, and how many of the finished ones passed all their checks"},
		Column{Title: "OWNER", Max: 24, Drop: 4, Help: "who owns the repo: the first owner of the * rule in its CODEOWNERS file (or the owner named in most rules) and how many others share it; red when you can push to a repo that has no owners; owner:@acme/billing in the filter keeps the repos of one owner"},
		Column{Title: "ALERTS", Right: true, Drop: 2, Help: "open Dependabot alerts; S opens them in the browser"},
		Column{Title: "PUSHED", Right: true, Drop: 1, Help: "how long ago someone last pushed to it"},
	)
	v.t.Empty = "Loading your repositories…"
	return v
}

func (v *reposView) ID() int             { return v.id }
func (v *reposView) Crumb() string       { return "Repos" }
func (v *reposView) Init(a *App) tea.Cmd { return a.st.loadOwners(a, false) }

// RefreshRepo does nothing: an action in one repo isn't worth sweeping them all.
func (v *reposView) RefreshRepo(a *App, repo string) tea.Cmd { return nil }

func (v *reposView) Updated(a *App) time.Time { return a.st.reposState.at }
func (v *reposView) Loading(a *App) bool      { return a.st.reposState.busy }

// Refresh sweeps your repositories again: when asked, or every 5 minutes while live.
func (v *reposView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !a.st.reposState.waited(reposLiveEvery) {
		return nil
	}
	owners := a.st.loadOwners(a, force)
	if names := v.filteredRepos(); names != nil {
		return tea.Batch(a.st.loadSomeRepos(a, names), owners)
	}
	return tea.Batch(a.st.loadRepos(a), owners)
}

// filteredRepos are the repos the text filter leaves, or nil when no filter
// narrows the list.
func (v *reposView) filteredRepos() []string {
	if v.t.Filter() == "" || v.t.Len() == 0 {
		return nil
	}
	return v.t.FilteredKeys()
}

// RefreshScope says which part of the list a refresh reads, when not all of it.
func (v *reposView) RefreshScope(a *App) string {
	if names := v.filteredRepos(); names != nil {
		return fmt.Sprintf("%d repos matching the filter", len(names))
	}
	return ""
}

func (v *reposView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *reposView) FilterValue() string          { return v.t.Filter() }
func (v *reposView) FilterInfo(a *App) filterInfo { return v.t.Info() }

func (v *reposView) selectedRepo() string {
	if r := v.t.Selected(); r != nil {
		return r.Key
	}
	return ""
}

func (v *reposView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

func (v *reposView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *reposView) Keys(a *App) []Binding {
	all := "show all"
	if v.showAll {
		all = "only CI repos"
	}
	var bs []Binding
	repo := v.selectedRepo()
	if repo != "" {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Do: do(func() tea.Cmd { return a.push(newRepoView(a, repo, "")) })})
	}
	bs = append(bs, filterHint)
	if repo != "" {
		bs = append(bs, a.pinKey(repo), a.watchRepoKey(repo), a.alertsKey(repo),
			Binding{Keys: []string{"w"}, Desc: "shared workflows", Help: "the shared workflows the repo uses", Off: len(sharedUsedBy(a, repo)) == 0,
				Do: do(func() tea.Cmd { return a.push(newSharedView(a, repo, false)) })})
	}
	bs = append(bs, Binding{Keys: []string{"s"}, Desc: "sort", Help: "sort by pinned and pushed, last pushed, failing first or name",
		Do: do(func() tea.Cmd { return v.pickOrder() })})
	bs = append(bs, Binding{Keys: []string{"A"}, Desc: all, Help: "all repos / only those with Actions", Do: do(func() tea.Cmd {
		v.showAll = !v.showAll
		v.t.Top()
		v.t.ResetWidths()
		return nil
	})})
	if repo != "" {
		bs = append(bs,
			Binding{Keys: []string{"o"}, Desc: "browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(repo + "/actions")) })},
			Binding{Keys: []string{"y"}, Help: "copy the URL", Do: do(func() tea.Cmd { return a.copyText(a.client.WebURL(repo), "repo URL") })},
		)
	}
	return grouped("REPOS", append(bs, v.t.moveKeys())...)
}

// pinKey pins or unpins repo: pinned repos come first in Repos and are
// always part of the Actions tab.
func (a *App) pinKey(repo string) Binding {
	return Binding{Keys: []string{"f"}, Desc: "pin", Help: "pin / unpin the repo", Do: do(func() tea.Cmd {
		on := a.cfg.ToggleFavourite(repo)
		if err := a.cfg.Save(); err != nil {
			return a.flashCmd(nError, "Could not save config: "+err.Error())
		}
		if on {
			return a.flashCmd(nDone, "★ Pinned "+repo+": first in Repos, always in Actions")
		}
		return a.flashCmd(nDone, "Unpinned "+repo)
	})}
}

func (v *reposView) rebuild(a *App) {
	now := time.Now()
	list := a.ciRepos()
	if v.showAll {
		list = a.repos
	}
	list = v.sorted(a, list)
	v.listed = list

	rows := make([]Row, 0, len(list))
	for _, r := range list {
		row := Row{Key: r.FullName, Kind: KOK}
		star := Cell{Text: " ", Styled: fg(cSteel).Render(strings.TrimSpace(icon("repo")))}
		if r.Private {
			star = Cell{Text: " ", Styled: sDim.Render(strings.TrimSpace(icon("lock")))}
		}
		if a.cfg.IsFavourite(r.FullName) {
			star = Cell{Text: " ", Styled: sGold.Render(strings.TrimSpace(icon("star")))}
		}
		now1, sp := Cell{}, Cell{}
		kinds := healthKinds(r.Health)
		switch {
		case len(kinds) > 0:
			k, label := a.mainNow(r)
			now1 = statusCell(k, a.frame, label)
			st, pl := history(kinds, a.frame)
			if ok, done := passedOf(kinds); done > 0 {
				t := fmt.Sprintf(" %d/%d", ok, done)
				st, pl = st+rateStyle(ok*100/done).Render(t), pl+t
			}
			sp = Cell{Text: pl, Styled: st}
		case !r.HasActions:
			now1 = Cell{Text: "no Actions", Styled: sDim.Render("no Actions")}
			row.Kind = KNone
		default:
			now1 = Cell{Text: "no checks", Styled: sDim.Render("no checks")}
		}
		if r.Archived {
			row.Kind = KNone
			now1 = Cell{Text: "archived", Styled: sDim.Render("archived")}
		}
		name := titleCell(a.short(r.FullName), false)
		// A dim lock after the name when your role there is below write.
		if strings.EqualFold(r.Permission, "READ") || strings.EqualFold(r.Permission, "TRIAGE") {
			lock := strings.TrimSpace(icon("lock"))
			name = Cell{Text: name.Text + " " + lock, Styled: sWhite.Render(name.Text) + " " + sDim.Render(lock)}
		}
		// The age is padded to its widest ("11mo"), so the clocks line up in the right-aligned column.
		ago := fmt.Sprintf("%4s", age(r.PushedAt, now))
		row.Cells = []Cell{star, name, now1, sp, a.ownerCell(r), a.alertCell(r.FullName), {Text: icon("clock") + ago, Styled: icoIn("clock", cSteel) + fg(cSteel).Render(ago)}}
		rows = append(rows, row)
	}
	v.t.Empty = "No repositories with GitHub Actions found. Press A to show all repositories."
	switch {
	case !a.st.loaded && a.st.reposState.err != nil:
		v.t.Empty = "Could not load your repositories: " + a.st.reposState.err.Error()
	case !a.st.loaded:
		v.t.Empty = "Loading your repositories…"
	}
	v.t.SetRows(rows)
}

// sorted orders list as v.order says. Every order keeps the most recently
// pushed first among repos that are otherwise alike.
func (v *reposView) sorted(a *App, list []gh.Repo) []gh.Repo {
	out := slices.Clone(list)
	pushed := func(x, y gh.Repo) int { return y.PushedAt.Compare(x.PushedAt) }
	// failing first, then running, passing, unknown, and those without checks
	rank := map[string]int{}
	if v.order == "failing" {
		for _, r := range out {
			n := 4
			if !r.Archived && len(healthKinds(r.Health)) > 0 {
				switch k, _ := a.mainNow(r); k {
				case KFail:
					n = 0
				case KRun:
					n = 1
				case KOK:
					n = 2
				default:
					n = 3
				}
			}
			rank[r.FullName] = n
		}
	}
	slices.SortStableFunc(out, func(x, y gh.Repo) int {
		switch v.order {
		case "pushed":
			return pushed(x, y)
		case "name":
			return cmp.Or(strings.Compare(strings.ToLower(x.FullName), strings.ToLower(y.FullName)), pushed(x, y))
		case "failing":
			return cmp.Or(cmp.Compare(rank[x.FullName], rank[y.FullName]), pushed(x, y))
		}
		return cmp.Or(cmp.Compare(pinRank(a, x), pinRank(a, y)), pushed(x, y))
	})
	return out
}

func pinRank(a *App, r gh.Repo) int {
	if a.cfg.IsFavourite(r.FullName) {
		return 0
	}
	return 1
}

func (v *reposView) pickOrder() tea.Cmd {
	var items []pickItem
	for _, o := range repoOrders {
		items = append(items, pickItem{value: o.key, label: o.label})
	}
	return openModal(newPicker("repo-order", "Sort by", v.order, items, func(key string) tea.Cmd {
		v.order = key
		v.t.Top()
		return nil
	}))
}

func (v *reposView) orderLabel() string {
	for _, o := range repoOrders {
		if o.key == v.order {
			return o.label
		}
	}
	return repoOrders[0].label
}

// healthKinds turns the check states of a branch's last commits, newest
// first, into kinds oldest first; commits without checks are left out.
func healthKinds(states []string) []Kind {
	var out []Kind
	for i := len(states) - 1; i >= 0; i-- {
		switch states[i] {
		case "SUCCESS":
			out = append(out, KOK)
		case "FAILURE", "ERROR":
			out = append(out, KFail)
		case "PENDING", "EXPECTED":
			out = append(out, KRun)
		}
	}
	return out
}

// passedOf counts the finished commits and those whose checks passed.
func passedOf(kinds []Kind) (ok, done int) {
	for _, k := range kinds {
		switch k {
		case KOK:
			ok++
			done++
		case KFail:
			done++
		}
	}
	return ok, done
}

// passingRate is the share of finished commits whose checks passed, or -1.
func passingRate(kinds []Kind) int {
	ok, done := passedOf(kinds)
	if done == 0 {
		return -1
	}
	return ok * 100 / done
}

// mixedBranches names the default branches of repos that differ in it.
const mixedBranches = "default branch"

// commonBranch is the default branch the repos share, or mixedBranches when
// they have different ones.
func commonBranch(repos []gh.Repo) string {
	branch := ""
	for _, r := range repos {
		switch {
		case r.DefaultBranch == "":
		case branch == "":
			branch = r.DefaultBranch
		case r.DefaultBranch != branch:
			return mixedBranches
		}
	}
	if branch == "" {
		return mixedBranches
	}
	return branch
}

// mainNow is how the default branch of r is doing right now, as one status:
// the worst of its workflows. A workflow whose latest run failed makes it
// failing, whatever the last commit did; otherwise it is running while a
// workflow runs or the last commit's checks do, and passes when they passed. Without the runs of
// r it says what the checks of the last commit did.
func (a *App) mainNow(r gh.Repo) (Kind, string) {
	kinds := healthKinds(r.Health)
	last := KNone
	if len(kinds) > 0 {
		last = kinds[len(kinds)-1]
	}
	latest := a.latestPerWorkflow(r.FullName)
	broken := 0
	for _, run := range latest {
		if StatusKind(run.Status, run.Conclusion) == KFail {
			broken++
		}
	}
	running := 0
	for _, run := range a.workflowsOnDefault(r.FullName) {
		if StatusKind(run.Status, run.Conclusion) == KRun {
			running++
		}
	}
	switch {
	case broken > 0:
		return KFail, fmt.Sprintf("%d failing", broken)
	case running > 1:
		return KRun, fmt.Sprintf("%d running", running)
	case last == KRun || running == 1:
		return KRun, "running"
	case len(latest) > 0 && last != KFail:
		return KOK, "all pass"
	case last == KFail:
		return KFail, "failed"
	case last == KOK:
		return KOK, "passed"
	}
	return KNone, ""
}

func (v *reposView) sync(a *App) { v.rebuild(a) }

// ansiStrip removes styling (used where a styled glyph sits in a selected row).
func ansiStrip(s string) string { return strings.TrimSpace(ansi.Strip(s)) }
