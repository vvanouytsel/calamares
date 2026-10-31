package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// commitsView lists the commits of one branch of a repo (a section of the
// repo screen); opening one shows the workflow runs started for it.
type commitsView struct {
	id      int
	repo    string
	branch  string // "" is the default branch
	t       *Table
	commits []gh.Commit
	loadState
	more bool // loading an older page
	done bool // the oldest commit is loaded
	page int  // last page loaded
	gen  int  // bumped when the branch changes; older responses are dropped
}

// commitsLiveEvery is how often the commits refresh while live.
const commitsLiveEvery = time.Minute

type commitsMsg struct {
	id, gen, page int
	commits       []gh.Commit
	err           error
}

func (m commitsMsg) viewID() int { return m.id }

func newCommitsView(a *App, repo, branch string) *commitsView {
	v := &commitsView{id: a.newID(), repo: repo, branch: branch, page: 1}
	v.t = newTable(
		Column{Title: "COMMIT", Help: "the short SHA of the commit"},
		Column{Title: "MESSAGE", Max: 80, Flex: true, Below: true, Help: "the first line of its message; v shows all of it"},
		Column{Title: "AUTHOR", Max: 20, Flex: true, Drop: 2, Help: "who wrote it"},
		Column{Title: "AGE", Right: true, Min: 3},
	)
	v.t.Empty = "Loading commits…"
	v.seed(a)
	return v
}

func (v *commitsView) ID() int          { return v.id }
func (v *commitsView) RepoName() string { return v.repo }
func (v *commitsView) Crumb() string    { return "commits" }
func (v *commitsView) Init(a *App) tea.Cmd {
	return nil
}

func (v *commitsView) Updated(a *App) time.Time { return v.at }
func (v *commitsView) Loading(a *App) bool      { return v.busy }

func (v *commitsView) seed(a *App) {
	if commits, ok := a.client.CachedCommits(v.repo, v.branch); ok {
		v.commits = commits
		v.done = len(commits) < gh.CommitsPageSize
		v.at, _ = a.client.CachedCommitsAt(v.repo, v.branch)
	}
}

// Refresh reads the newest page again: when asked, or every minute while live.
func (v *commitsView) Refresh(a *App, force bool) tea.Cmd {
	if (!force && !v.waited(commitsLiveEvery)) || !v.begin() {
		return nil
	}
	return v.fetch(a, 1)
}

func (v *commitsView) fetch(a *App, page int) tea.Cmd {
	id, gen, repo, branch := v.id, v.gen, v.repo, v.branch
	ctx := context.Background()
	if page == 1 && len(v.commits) > 0 {
		ctx = gh.Background(ctx)
	}
	return func() tea.Msg {
		commits, err := a.client.ListCommits(ctx, repo, branch, page)
		return commitsMsg{id, gen, page, commits, err}
	}
}

func (v *commitsView) loadMore(a *App) tea.Cmd {
	if v.more || v.done || len(v.commits) == 0 {
		return nil
	}
	v.more = true
	return v.fetch(a, v.page+1)
}

// merge folds a fetched page in by SHA: a refreshed first page goes in front
// of the older commits already loaded, an older page goes after them.
func (v *commitsView) merge(page int, commits []gh.Commit) {
	if page > 1 {
		v.commits = append(v.commits, commits...)
		return
	}
	fresh := map[string]bool{}
	for _, c := range commits {
		fresh[c.SHA] = true
	}
	merged := append([]gh.Commit{}, commits...)
	for _, c := range v.commits {
		if !fresh[c.SHA] {
			merged = append(merged, c)
		}
	}
	v.commits = merged
}

func (v *commitsView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case commitsMsg:
		if msg.gen != v.gen {
			return nil
		}
		if msg.page > 1 {
			v.more = false
		} else {
			v.busy = false
		}
		if msg.err != nil {
			v.err = msg.err
			return a.flashCmd(nError, "Could not read the commits of "+v.repo+": "+msg.err.Error())
		}
		v.err = nil
		if msg.page == 1 {
			v.at = time.Now()
		} else {
			v.page = msg.page
		}
		if len(msg.commits) < gh.CommitsPageSize && msg.page >= v.page {
			v.done = true
		}
		v.merge(msg.page, msg.commits)
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

func (v *commitsView) selected() (gh.Commit, bool) {
	r := v.t.Selected()
	if r == nil {
		return gh.Commit{}, false
	}
	for _, c := range v.commits {
		if c.SHA == r.Key {
			return c, true
		}
	}
	return gh.Commit{}, false
}

func (v *commitsView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *commitsView) Keys(a *App) []Binding {
	var bs []Binding
	if c, ok := v.selected(); ok {
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "runs and changes", Do: do(func() tea.Cmd { return a.push(newCommitView(a, v.repo, c)) })},
			Binding{Keys: []string{"v"}, Desc: "whole message", Help: "the whole commit message", Do: do(func() tea.Cmd { return commitMessage(c) })},
			Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(c.URL) })},
			Binding{Keys: []string{"y"}, Desc: "copy sha", Help: "copy the commit sha", Do: do(func() tea.Cmd { return a.copyText(c.SHA, "commit SHA") })},
		)
	}
	bs = append(bs,
		Binding{Keys: []string{"b"}, Desc: "branch", Help: "pick a branch", Do: do(func() tea.Cmd { return v.pickBranch(a) })},
		filterHint,
		a.twoLineKey(),
		moveBinding("", func(k string) tea.Cmd {
			v.t.handleNav(k)
			if v.t.Cursor() >= v.t.Len()-3 && v.t.Filter() == "" {
				return v.loadMore(a)
			}
			return nil
		}),
	)
	return grouped("COMMITS", bs...)
}

func (v *commitsView) pickBranch(a *App) tea.Cmd {
	repo := v.repo
	key := "branches:" + repo
	var items []pickItem
	if r, ok := a.repoByName(repo); ok && r.DefaultBranch != "" {
		items = append(items, pickItem{value: "", label: r.DefaultBranch, note: sDim.Render("default")})
	}
	m := newPicker(key, "Branch", v.branch, items, func(b string) tea.Cmd { return v.setBranch(a, b) })
	m.free = true
	if names, ok := a.client.CachedBranches(repo); ok {
		m.add(branchChoices(names))
	}
	a.modal = m
	return func() tea.Msg {
		names, err := a.client.ListBranches(context.Background(), repo)
		if err != nil {
			return nil
		}
		return pickerItemsMsg{key, branchChoices(names)}
	}
}

func branchChoices(names []string) []pickItem {
	items := make([]pickItem, len(names))
	for i, n := range names {
		items[i] = pickItem{value: n, label: n}
	}
	return items
}

func (v *commitsView) setBranch(a *App, branch string) tea.Cmd {
	if branch == v.branch {
		return nil
	}
	v.branch = branch
	v.gen++
	v.commits, v.page, v.more, v.done, v.err = nil, 1, false, false, nil
	v.t.SetRows(nil)
	v.t.Empty = "Loading commits…"
	v.seed(a)
	v.busy, v.tried = true, time.Now() // a new branch: read it even while the old one loads
	return v.fetch(a, 1)
}

func (v *commitsView) rebuild(a *App) {
	now := time.Now()
	rows := make([]Row, 0, len(v.commits))
	for _, c := range v.commits {
		rows = append(rows, Row{Key: c.SHA, Kind: KOK, Cells: []Cell{
			{Text: icon("commit") + c.Short(), Styled: ico("commit") + sTan.Render(c.Short())},
			titleCell(c.Subject(), false),
			iconText("user", c.Author, false),
			steelCell(age(c.Date, now), false),
		}})
	}
	v.t.Empty = "No commits on this branch."
	switch {
	case v.busy && len(rows) == 0:
		v.t.Empty = "Loading commits…"
	case v.err != nil && len(rows) == 0:
		v.t.Empty = "Could not load commits: " + v.err.Error()
	}
	v.t.SetRows(rows)
}

func (v *commitsView) SetFilter(a *App, q string) { v.t.SetFilter(q, a.me) }
func (v *commitsView) FilterValue() string        { return v.t.Filter() }
func (v *commitsView) FilterInfo(a *App) filterInfo {
	return v.t.Info()
}

// branchName is the branch on display, spelled out for the default branch.
func (v *commitsView) branchName(a *App) string {
	if v.branch != "" {
		return v.branch
	}
	if r, ok := a.repoByName(v.repo); ok && r.DefaultBranch != "" {
		return r.DefaultBranch
	}
	return "default branch"
}

func (v *commitsView) sync(a *App) { v.rebuild(a) }

func (v *commitsView) Render(a *App, w, h int) string {
	v.t.TwoLine = a.twoLine
	return listWithPanel(a, w, h,
		[]string{" " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}
