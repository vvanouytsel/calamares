package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// compareView is a branch against another: the commits it has and the files
// it changes, with their diffs.
type compareView struct {
	id         int
	repo       string
	base, head string
	cmp        gh.Comparison
	loaded     bool
	tab        int // 0 changes, 1 commits
	chg        changes
	commits    *Table
	loadState
}

type compareMsg struct {
	id  int
	cmp gh.Comparison
	err error
}

func (m compareMsg) viewID() int { return m.id }

func newCompareView(a *App, repo, base, head string) *compareView {
	v := &compareView{id: a.newID(), repo: repo, base: base, head: head}
	v.chg.readOnly = true
	v.commits = newTable(Column{Title: "SHA", Min: 7}, Column{Title: "COMMIT", Max: 90, Flex: true}, Column{Title: "WHO", Max: 20}, Column{Title: "AGE", Right: true, Min: 3})
	v.commits.Empty = "Loading…"
	return v
}

func (v *compareView) ID() int          { return v.id }
func (v *compareView) RepoName() string { return v.repo }
func (v *compareView) Crumb() string    { return v.head + " ↔ " + v.base }

func (v *compareView) Init(a *App) tea.Cmd { return v.load(a) }

func (v *compareView) load(a *App) tea.Cmd {
	if !v.begin() {
		return nil
	}
	id, repo, base, head := v.id, v.repo, v.base, v.head
	v.chg.busy = true
	return func() tea.Msg {
		c, err := a.client.Compare(context.Background(), repo, base, head)
		return compareMsg{id, c, err}
	}
}

func (v *compareView) Updated(a *App) time.Time { return v.at }
func (v *compareView) Loading(a *App) bool      { return v.busy }
func (v *compareView) Refresh(a *App, force bool) tea.Cmd {
	if !force {
		return nil
	}
	return v.load(a)
}

func (v *compareView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case compareMsg:
		v.end(msg.err)
		v.chg.busy, v.chg.err = false, msg.err
		if msg.err != nil {
			v.commits.Empty = "Could not compare: " + msg.err.Error()
			return nil
		}
		v.cmp, v.loaded = msg.cmp, true
		v.chg.setFiles(msg.cmp.Files)
		now := time.Now()
		var rows []Row
		for _, c := range msg.cmp.Commits {
			short := c.SHA[:min(7, len(c.SHA))]
			rows = append(rows, Row{Key: c.SHA, Cells: []Cell{{Text: short, Styled: sTan.Render(short)}, {Text: c.Message}, {Text: c.Author}, {Text: age(c.At, now)}}})
		}
		v.commits.SetRows(rows)
		v.commits.Empty = "No commits that the base lacks."
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *compareView) Keys(a *App) []Binding {
	bs := grouped("A COMPARISON",
		viewKeys("changes / commits", "its changes / its commits", "", func(d int) tea.Cmd {
			v.tab = (v.tab + d + 2) % 2
			return nil
		}),
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd {
			return a.openURL(a.client.WebURL(fmt.Sprintf("%s/compare/%s...%s", v.repo, v.base, v.head)))
		})},
	)
	if v.tab == 0 {
		return append(bs, v.chg.keys(a, a.client.WebURL(fmt.Sprintf("%s/compare/%s...%s", v.repo, v.base, v.head)), nil)...)
	}
	if r := v.commits.Selected(); r != nil {
		for _, c := range v.cmp.Commits {
			if c.SHA == r.Key {
				commit := gh.Commit{SHA: c.SHA, Message: c.Message, Author: c.Author, Date: c.At}
				bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open commit", Do: do(func() tea.Cmd { return a.push(newCommitView(a, v.repo, commit)) })})
			}
		}
	}
	return append(bs, v.commits.moveKeys())
}

func (v *compareView) sync(a *App) {}

func (v *compareView) Render(a *App, w, h int) string {
	items := []stripItem{{Label: "Changes", Icon: "code"}, {Label: "Commits", Icon: "commit"}}
	if v.loaded {
		items[0].Count = fmt.Sprint(len(v.cmp.Files))
		items[1].Count = fmt.Sprint(len(v.cmp.Commits))
	}
	facts := " " + fact("branch", v.head) + sDim.Render(" against ") + fact("branch", v.base)
	if v.loaded {
		facts += "  " + sGreen.Render(fmt.Sprintf("↑ %d ahead", v.cmp.Ahead)) + sDim.Render("  ·  ") + sAmber.Render(fmt.Sprintf("↓ %d behind", v.cmp.Behind))
	}
	head := fit(facts, w) + "\n" + viewStrip(items, v.tab, w)
	if v.tab == 0 {
		return head + "\n" + v.chg.renderHost(a, changesHost{}, w, h-2)
	}
	return head + "\n" + box(title("Commits", v.head, len(v.cmp.Commits), ""), v.commits.Render(w-2, h-4), w, h-2, cFrame)
}
