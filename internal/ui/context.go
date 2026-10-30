package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// where is what a screen inside a repo is about: the repo, and for a screen
// inside a run or a workflow file, the run or the file and where in it.
type where struct {
	repo string
	run  *gh.Run // nil when the screen is not about one run
	file string  // the workflow file, when there is no run
	more []string
}

// located is a screen that belongs to a repo. Every screen in a run or a
// workflow file says so, and gets a context line on top: the repo, the
// workflow and its file, the run and its branch.
type located interface {
	where() where
}

// inRun says w is about a run or a workflow file, which earns it the
// context line.
func (w where) inRun() bool { return w.run != nil || w.file != "" }

func (v *runView) where() where {
	return where{repo: v.repo, run: &v.run}
}

func (v *logView) where() where {
	w := where{repo: v.repo, run: &v.run}
	if v.job != nil {
		w.more = append(w.more, v.job.Name)
	}
	return w
}

func (v *sinceGreenView) where() where {
	return where{repo: v.repo, run: &v.run, more: []string{"since it last passed"}}
}

func (v *pagerView) where() where {
	w := where{repo: v.repo, file: v.path}
	if v.label != "" {
		w.more = append(w.more, v.label)
	}
	return w
}

func (v *prView) where() where     { return where{repo: v.repo} }
func (v *issueView) where() where  { return where{repo: v.repo} }
func (v *commitView) where() where { return where{repo: v.repo} }

// contextLine is the band under the header of a screen inside a run: the
// repo, the workflow with its file, the run and its branch, and where in it.
func (a *App) contextLine(w where, width int) string {
	sep := sDim.Render(" › ")
	parts := []string{ico("repo") + sGold.Bold(true).Render(w.repo)}
	switch {
	case w.run != nil:
		wf := ico("workflow") + sWhite.Bold(true).Render(w.run.Name)
		if w.run.Path != "" {
			wf += " " + sDim.Render(pathBase(w.run.Path))
		}
		parts = append(parts, wf, sGold.Bold(true).Render(fmt.Sprintf("#%d", w.run.RunNumber))+sDim.Render(" · ")+fact("branch", w.run.HeadBranch))
	case w.file != "":
		parts = append(parts, ico("workflow")+sWhite.Bold(true).Render(pathBase(w.file)))
	}
	for _, m := range w.more {
		parts = append(parts, sText.Render(m))
	}
	return onBand(" "+strings.Join(parts, sep), width)
}

// mentions says the trail names repo already, in full or by its short name.
func (a *App) mentions(segs []string, repo string) bool {
	for _, s := range segs {
		if strings.EqualFold(s, repo) || strings.EqualFold(s, a.short(repo)) {
			return true
		}
	}
	return false
}

// terminalTitle is what the terminal's tab says: where you are, in short.
func (a *App) terminalTitle() string {
	if l, ok := a.top().(located); ok {
		w := l.where()
		parts := []string{a.short(w.repo)}
		if w.run != nil {
			parts = append(parts, fmt.Sprintf("%s #%d", w.run.Name, w.run.RunNumber))
		} else {
			parts = append(parts, a.top().Crumb())
		}
		return strings.Join(append(parts, w.more...), " › ") + " · Calamares"
	}
	segs := append([]string{tabNames[a.tab]}, a.trail()...)
	return strings.Join(segs[max(len(segs)-2, 0):], " › ") + " · Calamares"
}

// titleCmd sets the terminal's title when where you are changed.
func (a *App) titleCmd() tea.Cmd {
	t := a.terminalTitle()
	if t == a.windowTitle {
		return nil
	}
	a.windowTitle = t
	return tea.SetWindowTitle(t)
}
