package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// cachesStale is how old what the Caches view knows of a repo may get before
// showing it reads it again.
const cachesStale = 30 * time.Minute

// Shares of the limit from which a repo's cache is near it, and full: above
// the limit GitHub evicts the caches used least recently, so builds lose them.
const (
	cacheLimitText = "10 GB" // gh.CacheLimit, as said
	cacheNearPct   = 80
	cacheFullPct   = 95
)

type cacheData struct {
	byRepo map[string]gh.Caches
	state  map[string]*loadState
}

func newCacheData() cacheData {
	return cacheData{byRepo: map[string]gh.Caches{}, state: map[string]*loadState{}}
}

type cachesMsg struct {
	repo   string
	caches gh.Caches
	err    error
}

// loadCaches reads the Actions cache of each repo that is due: two requests a
// repo, in the background lane.
func (a *App) loadCaches(repos []string, force bool) tea.Cmd {
	d := &a.st.caches
	ctx := gh.Background(context.Background())
	var cmds []tea.Cmd
	for _, repo := range repos {
		l := stateIn(d.state, repo)
		if !(force || (time.Since(l.at) > cachesStale && time.Since(l.tried) > cachesStale)) || !l.begin() {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			c, err := a.client.RepoCaches(ctx, repo)
			return cachesMsg{repo, c, err}
		})
	}
	return tea.Batch(cmds...)
}

func (s *store) gotCaches(m cachesMsg) {
	stateIn(s.caches.state, m.repo).end(m.err)
	if m.err == nil {
		s.caches.byRepo[m.repo] = m.caches
	}
}

// repoCache is a repo's Actions cache as the view lists it.
type repoCache struct {
	repo string
	gh.Caches
	pulls     int64 // bytes in caches made for pull requests
	pullCount int
}

func (c repoCache) pct() int { return int(c.Size * 100 / gh.CacheLimit) }

func (c repoCache) kind() Kind {
	switch p := c.pct(); {
	case p >= cacheFullPct:
		return KFail
	case p >= cacheNearPct:
		return KWait
	}
	return KOK
}

// status is the status word: Full, Near limit or Healthy.
func (c repoCache) status() string {
	return map[Kind]string{KFail: "Full", KWait: "Near limit", KOK: "Healthy"}[c.kind()]
}

// pullCaches are the caches made for pull requests, of the largest read.
func (c repoCache) pullCaches() []gh.Cache {
	var out []gh.Cache
	for _, x := range c.Largest {
		if x.PullNumber() != 0 {
			out = append(out, x)
		}
	}
	return out
}

// cachesView is the Caches view of Platform: how full each repo's Actions
// cache is, and how much of it only pull requests can use.
type cachesView struct {
	repos func(a *App) []string
	t     *Table
	list  []repoCache
}

func newCachesView(repos func(a *App) []string) *cachesView {
	return &cachesView{repos: repos, t: newTable(
		Column{Title: "STATUS", Help: "Full from 95% of the 10 GB a repo may cache, Near limit from 80%: above the limit GitHub evicts the caches used least recently, so builds lose them; Healthy below that"},
		Column{Title: "REPO", Max: 30, Flex: true, MidCut: true},
		Column{Title: "USED", Right: true, Help: "how much its active caches hold, of the 10 GB a repo may cache"},
		Column{Title: "CACHES", Right: true, Help: "how many caches it has"},
		Column{Title: "PULL REQUESTS", Right: true, Drop: 1, Help: "how much of it is caches made for pull requests, which only that pull request can use"},
		Column{Title: "LARGEST", Max: 40, Flex: true, Drop: 2, Help: "the key of its largest cache, and its size"},
	)}
}

func (v *cachesView) table() *Table    { return v.t }
func (v *cachesView) count(a *App) int { return len(v.list) }
func (v *cachesView) listHead(a *App) string {
	return a.filterLine(v.t.Filter(), v.FilterInfo(a))
}

func (v *cachesView) subtitle(a *App, repos string) string {
	s := "Actions caches of " + repos + ", against the " + cacheLimitText + " a repository may cache"
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

func (v *cachesView) state(a *App) []string        { return []string{"Fullest first"} }
func (v *cachesView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *cachesView) FilterValue() string          { return v.t.Filter() }
func (v *cachesView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *cachesView) load(a *App, force bool) tea.Cmd {
	return a.loadCaches(v.repos(a), force)
}

func (v *cachesView) busy(a *App) bool {
	for _, repo := range v.repos(a) {
		if stateIn(a.st.caches.state, repo).busy {
			return true
		}
	}
	return false
}

func (v *cachesView) sync(a *App) {
	v.list = v.list[:0]
	for _, repo := range v.repos(a) {
		c, ok := a.st.caches.byRepo[repo]
		if !ok || c.Count == 0 {
			continue
		}
		rc := repoCache{repo: repo, Caches: c}
		for _, x := range c.Largest {
			if x.PullNumber() != 0 {
				rc.pulls += x.Size
				rc.pullCount++
			}
		}
		v.list = append(v.list, rc)
	}
	sort.SliceStable(v.list, func(i, j int) bool { return v.list[i].Size > v.list[j].Size })
	var rows []Row
	for _, c := range v.list {
		largest := Cell{Text: "–", Styled: sDim.Render("–")}
		if len(c.Largest) > 0 {
			x := c.Largest[0]
			t := x.Key + " · " + bytesText(x.Size)
			largest = Cell{Text: icon("key") + t, Styled: ico("key") + sText.Render(x.Key) + fg(cSteel).Render(" · "+bytesText(x.Size))}
		}
		pulls := steelCell("–", true)
		if c.pulls > 0 {
			pulls = Cell{Text: bytesText(c.pulls), Styled: sText.Render(bytesText(c.pulls))}
		}
		used := fmt.Sprintf("%s of %s", bytesText(c.Size), cacheLimitText)
		rows = append(rows, Row{Key: c.repo, Kind: c.kind(), Cells: []Cell{
			statusCell(c.kind(), 0, c.status()),
			iconText("repo", a.short(c.repo), false),
			{Text: used, Styled: fg(c.kind().Color()).Render(bytesText(c.Size)) + sDim.Render(" of "+cacheLimitText)},
			{Text: fmt.Sprint(c.Count), Styled: sText.Render(fmt.Sprint(c.Count))},
			pulls,
			largest,
		}})
	}
	v.t.Empty = "None of these repos has anything in its Actions cache."
	if v.busy(a) {
		v.t.Empty = "Reading the Actions caches…"
	}
	v.t.SetRows(rows)
}

func (v *cachesView) selected() (repoCache, bool) {
	r := v.t.Selected()
	if r == nil {
		return repoCache{}, false
	}
	for _, c := range v.list {
		if c.repo == r.Key {
			return c, true
		}
	}
	return repoCache{}, false
}

// counts are how many repos have a full cache, and how many one near the limit.
func (v *cachesView) counts() (full, near int) {
	for _, c := range v.list {
		switch c.kind() {
		case KFail:
			full++
		case KWait:
			near++
		}
	}
	return full, near
}

func (v *cachesView) chips(a *App) []string {
	full, near := v.counts()
	var chips []string
	if full > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d full", full)))
	}
	if near > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d near the limit", near)))
	}
	return chips
}

func (v *cachesView) coverage(a *App) string {
	s := fmt.Sprintf("the Actions cache of %s, read every 30 minutes", repoCount(len(v.repos(a))))
	if v.busy(a) {
		s += " · reading…"
	}
	return s
}

func (v *cachesView) keys(a *App) []Binding {
	c, ok := v.selected()
	if !ok {
		return nil
	}
	pulls := c.pullCaches()
	return []Binding{
		{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open repo", Do: do(func() tea.Cmd { return a.openRepo(c.repo, "", 0) })},
		{Keys: []string{"X"}, Desc: "clear pull request caches", Help: "delete the caches made for pull requests: only those pull requests could use them, and their next run makes them again",
			Off: len(pulls) == 0, Mutates: true, Need: gh.CapWrite, Repo: c.repo, Do: do(func() tea.Cmd { return a.clearPullCaches(c.repo, pulls) })},
		{Keys: []string{"o"}, Desc: "open in browser", Help: "open the repo's caches in the browser", Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(c.repo + "/actions/caches")) })},
	}
}

// clearPullCaches asks, then deletes the caches made for pull requests.
func (a *App) clearPullCaches(repo string, caches []gh.Cache) tea.Cmd {
	var size int64
	prs := map[int]bool{}
	for _, x := range caches {
		size += x.Size
		prs[x.PullNumber()] = true
	}
	return confirm("Clear pull request caches?", []string{
		sWhite.Render(repo),
		"",
		fmt.Sprintf("%d %s of %d pull %s, %s in all.", len(caches), plural("cache", len(caches)), len(prs), plural("request", len(prs)), bytesText(size)),
		"Their next run makes them again.",
	}, "Clear", true, func() tea.Cmd {
		return a.actThen(repo, fmt.Sprintf("Cleared %d pull request caches of %s, %s", len(caches), repo, bytesText(size)), func(ctx context.Context) error {
			for _, x := range caches {
				if err := a.client.DeleteCache(ctx, repo, x.ID); err != nil && !gh.IsNotFound(err) {
					return err
				}
			}
			return nil
		}, func() tea.Cmd { return a.loadCaches([]string{repo}, true) })
	})
}

func (v *cachesView) panel(a *App, w, h int) []string {
	c, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "Nothing selected.")
	}
	now := time.Now()
	k := c.kind()
	p := newLayerPanel(w)
	p.add(icoIn("repo", cGold) + sWhite.Bold(true).Render(c.repo))
	p.add(sDim.Render("Actions cache"))
	p.add("")
	p.field("Status", fg(k.Color()).Render(c.status()))
	p.field("Used", fg(k.Color()).Render(bytesText(c.Size))+sDim.Render(fmt.Sprintf(" of %s, %d%%", cacheLimitText, c.pct())))
	p.field("Caches", sText.Render(fmt.Sprint(c.Count)))
	if c.pulls > 0 {
		p.field("PR caches", sText.Render(fmt.Sprintf("%s in %d %s", bytesText(c.pulls), c.pullCount, plural("cache", c.pullCount))))
	}

	// Where the space goes: by branch, or pull request.
	byRef := map[string]int64{}
	for _, x := range c.Largest {
		byRef[refLabel(x.Ref)] += x.Size
	}
	refs := make([]string, 0, len(byRef))
	for r := range byRef {
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool { return byRef[refs[i]] > byRef[refs[j]] })
	p.label("Space by branch")
	p.add(sDim.Render("of the largest " + fmt.Sprint(len(c.Largest)) + " caches"))
	for _, r := range refs[:min(len(refs), 6)] {
		icon := "branch"
		if strings.HasPrefix(r, "#") {
			icon = "pr"
		}
		p.row(ico(icon)+sText.Render(r), fg(cSteel).Render(bytesText(byRef[r])))
	}

	p.label("Largest caches")
	for _, x := range c.Largest[:min(len(c.Largest), 5)] {
		p.row(ico("key")+sText.Render(x.Key), fg(cSteel).Render(bytesText(x.Size)))
		p.add("  " + sDim.Render(refLabel(x.Ref)+" · used "+age(x.LastAccessed, now)+" ago"))
	}

	p.label("Next step")
	switch {
	case k != KOK && c.pulls > 0:
		p.callout(k.Color(), fmt.Sprintf("Above 10 GB GitHub evicts the caches used least recently, so builds lose them. Caches for pull requests take %s of it.", bytesText(c.pulls)),
			keycap("X")+sText.Render(" clear pull request caches"))
	case k != KOK:
		p.callout(k.Color(), "Above 10 GB GitHub evicts the caches used least recently, so builds lose them. Look at the largest keys: a key that changes on every run makes a new cache each time.")
	default:
		p.add(sDim.Render("Well below the limit."))
	}
	return p.render(h)
}

// refLabel names the ref of a cache: the branch, or the pull request.
func refLabel(ref string) string {
	if rest, ok := strings.CutPrefix(ref, "refs/pull/"); ok {
		n, _, _ := strings.Cut(rest, "/")
		return "#" + n
	}
	return strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/tags/")
}

func (v *cachesView) explain(a *App) []explanation {
	return []explanation{
		{"N full, N near the limit", "repos whose Actions cache holds 95% or more, or 80% or more, of the 10 GB a repo may cache"},
		{"panel: Space by branch", "how much of the largest caches each branch or pull request holds"},
		{"panel: Largest caches", "the largest caches by key, with their branch and when a run last used them"},
		{"X", "deletes the caches made for pull requests; their next run makes them again"},
	}
}
