package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// openRunRepo shows the repo a run belongs to, on its Actions section with
// the run's workflow selected; esc comes back here.
func (a *App) openRunRepo(repo string, run gh.Run) tea.Cmd {
	v := newRepoView(a, repo, "")
	if run.WorkflowID != 0 {
		v.wantWF, v.focus = run.WorkflowID, paneRuns
	}
	return a.push(v)
}

// runRepoKey is s on a run: the repo it belongs to.
func (a *App) runRepoKey(repo string, run gh.Run) Binding {
	return Binding{Keys: []string{"s"}, Desc: "repo", Help: "the repo of the run, at its workflow",
		Do: do(func() tea.Cmd { return a.openRunRepo(repo, run) })}
}

// groupKey is g on the Failing and Warnings views: grouped, or one row per run.
func groupKey(flat *bool, t ...*Table) Binding {
	desc := "all runs"
	if *flat {
		desc = "group"
	}
	return Binding{Keys: []string{"g"}, Desc: desc, Help: "grouped, or every run on a line of its own",
		Do: do(func() tea.Cmd {
			*flat = !*flat
			for _, x := range t {
				x.Top()
			}
			return nil
		})}
}

// groupChip says how the list is shown, with g to change it.
func groupChip(flat bool, grouped string) string {
	if flat {
		return chip("g", "all runs ▾", true)
	}
	return chip("g", grouped+" ▾", false)
}
