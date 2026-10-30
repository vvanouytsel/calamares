package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// The tabs of a commit.
const (
	commitTabRuns = iota
	commitTabChanges
	commitTabCount
)

// commitView is one commit in two tabs: the workflow runs started for it,
// and the files it changed with their diffs.
type commitView struct {
	id     int
	repo   string
	commit gh.Commit
	tab    int
	runs   *runsView
	chg    changes
}

type commitFilesMsg struct {
	id    int
	files []gh.FileDiff
	err   error
}

func (m commitFilesMsg) viewID() int { return m.id }

func newCommitView(a *App, repo string, c gh.Commit) *commitView {
	v := &commitView{id: a.newID(), repo: repo, commit: c, runs: newCommitRunsView(a, repo, c.SHA)}
	v.chg.readOnly = true
	return v
}

func (v *commitView) ID() int          { return v.id }
func (v *commitView) RepoName() string { return v.repo }
func (v *commitView) Crumb() string    { return v.commit.Short() }
func (v *commitView) Children() []View { return []View{v.runs} }

func (v *commitView) Init(a *App) tea.Cmd {
	return tea.Batch(v.runs.Init(a), v.loadFiles(a))
}

func (v *commitView) loadFiles(a *App) tea.Cmd {
	if v.chg.busy {
		return nil
	}
	v.chg.busy = true
	id, repo, sha := v.id, v.repo, v.commit.SHA
	return func() tea.Msg {
		files, err := a.client.CommitFiles(context.Background(), repo, sha)
		return commitFilesMsg{id, files, err}
	}
}

func (v *commitView) Updated(a *App) time.Time { return v.runs.at }
func (v *commitView) Loading(a *App) bool      { return v.runs.busy || v.chg.busy }

func (v *commitView) Refresh(a *App, force bool) tea.Cmd { return v.runs.Refresh(a, force) }

func (v *commitView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case commitFilesMsg:
		v.chg.busy, v.chg.err = false, msg.err
		if msg.err == nil {
			v.chg.setFiles(msg.files)
		}
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *commitView) Keys(a *App) []Binding {
	bs := grouped("A COMMIT",
		viewKeys("runs / changes", "its workflow runs / its changes", "", func(d int) tea.Cmd {
			v.tab = (v.tab + d + commitTabCount) % commitTabCount
			return nil
		}),
		Binding{Keys: []string{"m"}, Desc: "message", Help: "the whole commit message", Do: do(func() tea.Cmd { return commitMessage(v.commit) })},
	)
	if v.tab == commitTabChanges {
		return append(bs, v.chg.keys(a, v.commit.URL, nil)...)
	}
	return append(bs, v.runs.Keys(a)...)
}

func (v *commitView) sync(a *App) { v.runs.sync(a) }

func (v *commitView) Render(a *App, w, h int) string {
	strip := v.renderStrip(w)
	if v.tab == commitTabChanges {
		return strip + "\n" + v.chg.renderHost(a, changesHost{}, w, h-2)
	}
	return strip + "\n" + v.runs.Render(a, w, h-2)
}

// renderStrip is the commit's header: a line of facts and the strip of its views.
func (v *commitView) renderStrip(w int) string {
	items := []stripItem{{Label: "Runs", Icon: "actions"}, {Label: "Changes", Icon: "code"}}
	if v.chg.loaded {
		items[commitTabChanges].Count = fmt.Sprint(len(v.chg.files))
	}
	facts := " " + ico("commit") + sTan.Render(v.commit.Short()) + " " + sWhite.Bold(true).Render(v.commit.Subject()) + "   " + fact("user", v.commit.Author)
	return fit(facts, w) + "\n" + viewStrip(items, v.tab, w)
}
