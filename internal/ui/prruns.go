package ui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// The Runs tab of a pull request: every workflow run of its commits, newest
// commit first, each commit named once on its first run.

type prRunsMsg struct {
	id   int
	runs []gh.Run
	err  error
}

func (m prRunsMsg) viewID() int { return m.id }

func newPRRunsTable() *Table {
	t := newTable(
		Column{Title: "COMMIT", Max: 40, Flex: true},
		Column{Title: "STATUS"},
		Column{Title: "WORKFLOW", Max: 26, Flex: true, Drop: 1},
		Column{Title: "RUN", Right: true},
		Column{Title: "DURATION", Right: true, Min: 7},
		Column{Title: "AGE", Right: true, Min: 3},
	)
	t.Empty = "Loading the runs…"
	return t
}

// loadRuns reads the latest runs on the pull request's branch.
func (v *prView) loadRuns(a *App) tea.Cmd {
	if v.runsBusy || !v.loaded {
		return nil
	}
	v.runsBusy = true
	id, repo, branch := v.id, v.repo, v.pr.Branch
	return func() tea.Msg {
		runs, _, err := a.client.ListRuns(context.Background(), repo, gh.RunFilter{Branch: branch, PerPage: 50})
		return prRunsMsg{id, runs, err}
	}
}

// ownRuns are the runs that belong to this pull request: a run on its branch
// that GitHub links to another pull request is one of a pull request from a
// fork's branch of the same name.
func (v *prView) ownRuns(runs []gh.Run) []gh.Run {
	var out []gh.Run
	for _, r := range runs {
		linked := len(r.PullRequests) == 0
		for _, pr := range r.PullRequests {
			linked = linked || pr.Number == v.number
		}
		if linked {
			out = append(out, r)
		}
	}
	return out
}

// rebuildRuns lists the runs grouped by commit, newest commit first.
func (v *prView) rebuildRuns() {
	now := time.Now()
	var order []string
	bySHA := map[string][]gh.Run{}
	for _, r := range v.runList {
		if _, ok := bySHA[r.HeadSHA]; !ok {
			order = append(order, r.HeadSHA)
		}
		bySHA[r.HeadSHA] = append(bySHA[r.HeadSHA], r)
	}
	var rows []Row
	for _, sha := range order {
		for i, r := range bySHA[sha] {
			commit := Cell{}
			if i == 0 {
				commit = v.commitCell(sha)
			}
			k := StatusKind(r.Status, r.Conclusion)
			quiet := k.RowColor() == cDim
			took := steelCell(dur(r.Duration(now)), quiet)
			if r.Active() {
				took = Cell{Text: took.Text, Styled: sAmber.Render(took.Text)}
			}
			rows = append(rows, Row{Key: fmt.Sprint(r.ID), Kind: k, Cells: []Cell{
				commit,
				statusCell(k, 0, ""),
				iconText("workflow", r.Name, quiet),
				numberCell(r.RunNumber, quiet),
				took,
				steelCell(age(r.CreatedAt, now), quiet),
			}})
		}
	}
	v.runs.SetRows(rows)
	v.runs.Empty = "No workflow runs on this pull request."
}

// commitCell is a commit's short SHA and, once the commits are read, its subject.
func (v *prView) commitCell(sha string) Cell {
	short := sha[:min(7, len(sha))]
	subject := ""
	if i := slices.IndexFunc(v.commitList, func(c gh.PRCommit) bool { return c.SHA == sha }); i >= 0 {
		subject = v.commitList[i].Message
	}
	return Cell{Text: short + " " + subject, Styled: sTan.Render(short) + " " + sWhite.Render(subject)}
}

func (v *prView) selectedRun() (gh.Run, bool) {
	r := v.runs.Selected()
	if r == nil {
		return gh.Run{}, false
	}
	i := slices.IndexFunc(v.runList, func(run gh.Run) bool { return fmt.Sprint(run.ID) == r.Key })
	if i < 0 {
		return gh.Run{}, false
	}
	return v.runList[i], true
}

// runsKeys are the keys of the Runs tab.
func (v *prView) runsKeys(a *App) []Binding {
	var bs []Binding
	if run, ok := v.selectedRun(); ok {
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run", Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, run)) })},
			Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, v.repo, run, nil, 0)) })},
		)
		bs = append(bs, without(a.runKeys(v.repo, run), "#", "a", "o", "y")...)
	}
	return grouped("RUNS", append(bs, v.runs.moveKeys())...)
}
