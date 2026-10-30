package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// rerunItem is one choice of the rerun picker.
type rerunItem struct {
	label string
	note  string
	off   string // why it can't be chosen now
	pick  func(debug bool) tea.Cmd
}

// rerunModal is what R opens on a run: rerun the failed jobs, the job under
// the cursor, one job of your choice, or everything, with debug logging as a
// switch under the choices.
type rerunModal struct {
	title string
	items []rerunItem
	sel   int
	debug bool
}

// rerunPicker opens the picker for run. jobs are the run's jobs when the
// screen holds them, job the one under the cursor.
func (a *App) rerunPicker(repo string, run gh.Run, jobs []gh.Job, job *gh.Job) tea.Cmd {
	label := fmt.Sprintf("%s #%d", run.Name, run.RunNumber)
	off := ""
	if run.Active() {
		off = "the run is still going"
	}
	failedJobs := 0
	for _, j := range jobs {
		if StatusKind(j.Status, j.Conclusion) == KFail {
			failedJobs++
		}
	}
	failedNote := ""
	if len(jobs) > 0 {
		failedNote = fmt.Sprintf("%d of %d", failedJobs, len(jobs))
	}
	failedOff := off
	if failedOff == "" && ((len(jobs) > 0 && failedJobs == 0) || (len(jobs) == 0 && StatusKind(run.Status, run.Conclusion) != KFail)) {
		failedOff = "no job failed"
	}
	m := &rerunModal{title: "Rerun " + label}
	m.items = append(m.items, rerunItem{label: "Rerun failed jobs", note: failedNote, off: failedOff,
		pick: func(debug bool) tea.Cmd { return a.rerunConfirm(repo, run, rerunFailed, nil, debug) }})
	if job != nil {
		j := *job
		jobOff := off
		if jobOff == "" && j.Status != "completed" {
			jobOff = "the job is still going"
		}
		m.items = append(m.items, rerunItem{label: "Rerun this job: " + j.Name, off: jobOff,
			pick: func(debug bool) tea.Cmd { return a.rerunConfirm(repo, run, rerunOne, &j, debug) }})
	}
	m.items = append(m.items,
		rerunItem{label: "Rerun one job…", off: off, pick: func(debug bool) tea.Cmd { return a.pickJobToRerun(repo, run, jobs, debug) }},
		rerunItem{label: "Rerun all jobs", off: off, pick: func(debug bool) tea.Cmd { return a.rerunConfirm(repo, run, rerunAll, nil, debug) }},
	)
	for i, it := range m.items {
		if it.off == "" {
			m.sel = i
			break
		}
	}
	return openModal(m)
}

func (m *rerunModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return true, nil
	case "up", "k", "shift+tab":
		m.sel = max(m.sel-1, 0)
	case "down", "j", "tab":
		m.sel = min(m.sel+1, len(m.items)-1)
	case " ", "d":
		m.debug = !m.debug
	case "enter":
		it := m.items[m.sel]
		if it.off != "" {
			return true, a.flashCmd(nHint, capFirst(it.off)+": "+strings.ToLower(it.label[:1])+it.label[1:]+" isn't possible now.")
		}
		return true, it.pick(m.debug)
	}
	return false, nil
}

func (m *rerunModal) Render(a *App, w, h int) string {
	var lines []string
	labelW := 0
	for _, it := range m.items {
		labelW = max(labelW, width(it.label))
	}
	for i, it := range m.items {
		note := it.note
		if it.off != "" {
			note = it.off
		} else if note != "" {
			note = "(" + note + ")"
		}
		line := fit(it.label, labelW) + "  " + note
		switch {
		case i == m.sel && it.off == "":
			lines = append(lines, sSel.Bold(true).Render(fit("▸ "+line, labelW+34)))
		case i == m.sel:
			lines = append(lines, sDim.Render(fit("▸ "+line, labelW+34)))
		case it.off != "":
			lines = append(lines, sDim.Render("  "+line))
		default:
			lines = append(lines, "  "+sText.Render(fit(it.label, labelW))+"  "+sDim.Render(note))
		}
	}
	state := sText.Render("off")
	if m.debug {
		state = sAmber.Render("on")
	}
	lines = append(lines, "", "  "+sDim.Render("debug logging: ")+state+sDim.Render("   ")+keycap("space")+sDim.Render(" switches"), "",
		sDim.Render("↑↓ choose · enter go on · esc cancel"))
	return modalBox(title(m.title, "", -1, ""), lines, cCyan, 50, w-4)
}

type rerunMode int

const (
	rerunAll rerunMode = iota
	rerunFailed
	rerunOne
)

// pickJobToRerun lets you choose one of the run's jobs, reading them first
// when the screen didn't hold them.
func (a *App) pickJobToRerun(repo string, run gh.Run, jobs []gh.Job, debug bool) tea.Cmd {
	open := func(jobs []gh.Job) tea.Msg {
		var items []pickItem
		byID := map[string]gh.Job{}
		for _, j := range jobs {
			id := strconv.FormatInt(j.ID, 10)
			byID[id] = j
			k := StatusKind(j.Status, j.Conclusion)
			items = append(items, pickItem{value: id, label: j.Name, note: fg(k.Color()).Render(k.Glyph(0) + " " + k.Label())})
		}
		return modalMsg{newPicker("rerun-job", "Rerun which job?", "", items, func(id string) tea.Cmd {
			j := byID[id]
			return a.rerunConfirm(repo, run, rerunOne, &j, debug)
		})}
	}
	if len(jobs) > 0 {
		return func() tea.Msg { return open(jobs) }
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		jobs, err := a.client.ListJobs(ctx, repo, run.ID)
		if err != nil {
			return flashMsg{nError, "Could not read the jobs of the run: " + err.Error()}
		}
		return open(jobs)
	}
}

// rerunConfirm asks, then reruns what was chosen and watches the new attempt.
func (a *App) rerunConfirm(repo string, run gh.Run, mode rerunMode, job *gh.Job, debug bool) tea.Cmd {
	if mode != rerunOne {
		return a.rerun(repo, run, mode == rerunFailed, debug)
	}
	label := fmt.Sprintf("%s #%d", run.Name, run.RunNumber)
	title := "Rerun this job?"
	lines := []string{
		sWhite.Render(label) + sDim.Render(" · "+run.DisplayTitle),
		sDim.Render("job: ") + sText.Render(job.Name),
		"",
		"Only this job, and the jobs that depend on it, will run again.",
	}
	if debug {
		title = "Rerun this job with debug logging?"
		lines = append(lines, "Runner and step debug logging (ACTIONS_STEP_DEBUG) will be on.")
	}
	lines = append(lines, sDim.Render("You'll get a notification when it finishes (it shows on the Watching tab)."))
	return confirm(title, lines, "Rerun", false, func() tea.Cmd {
		return a.actThen(repo, "Rerun of job "+job.Name+" of "+label+" requested · watching it", func(ctx context.Context) error {
			return a.client.RerunJob(ctx, repo, job.ID, debug)
		}, func() tea.Cmd {
			a.earnRerun(run, debug)
			return a.watchRerun(repo, run)
		})
	})
}
