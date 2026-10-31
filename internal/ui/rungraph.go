package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// pipelineNow is the pipeline of the run as far as it is known.
// graphState is where the pipeline of a run is looked at, and how.
type graphState struct {
	sel      string // the key of the selected job
	offX     int
	offY     int
	zoom     int  // zoomFit, or the zoom the user picked
	full     bool // the pipeline fills the screen
	zoomNow  int  // the zoom the pipeline was last drawn at
	center   bool // put the selected job in the middle on the next draw
	matrices int  // how matrices show: matrixAuto, matrixLegs or matrixOne
	runners  bool // boxes show the runner of each job
}

func (v *runView) pipelineNow() *pipeline {
	prefix := v.jobPrefix()
	p := buildPipeline(v.jobs, v.plan, prefix, !v.shownRun().Active(), v.g.matrices)
	p.applyTypical(v.jobs, v.typical, v.previous != nil && v.byPrevious)
	r := v.shownRun()
	p.id = fmt.Sprintf("%d/%d:", r.ID, r.RunAttempt)
	p.zoom = v.g.zoomNow
	return p
}

// toggleGraph switches between the job list and the pipeline, reading the
// workflow file the first time to learn what depends on what.
func (v *runView) toggleGraph(a *App) tea.Cmd {
	v.graph = !v.graph
	if !v.graph {
		return nil
	}
	v.selectNodeAtCursor()
	v.g.center = true
	return v.loadPlan(a)
}

// workflowPath is the run's workflow file, without the ref GitHub may add to it.
func (v *runView) workflowPath() string {
	path, _, _ := strings.Cut(v.run.Path, "@")
	return path
}

// loadPlan reads the workflow file and the workflows it calls, once.
func (v *runView) loadPlan(a *App) tea.Cmd {
	if v.planLoad != 0 {
		return nil
	}
	v.planLoad = 1
	id, repo, path, sha := v.id, v.repo, v.workflowPath(), v.run.HeadSHA
	return func() tea.Msg {
		plan, _ := a.client.ResolvePipeline(context.Background(), repo, sha, path)
		return planMsg{id: id, plan: plan}
	}
}

// selectedNodeKey is the pipeline job the cursor is on, in the pipeline or
// in the job list.
func (v *runView) selectedNodeKey() string {
	if v.graph {
		return v.g.sel
	}
	job := v.selectedJob()
	if job == nil {
		return ""
	}
	for i := range v.jobs {
		if v.jobs[i].ID == job.ID {
			if n := v.pipelineNow().nodeOfJob(i); n != nil {
				return n.key
			}
		}
	}
	return ""
}

// showJobSource opens the YAML that defines the selected job, reading the
// workflow file first if it hasn't been yet.
func (v *runView) showJobSource(a *App) tea.Cmd {
	if v.planLoad != 2 {
		v.sourceWanted = true
		return tea.Batch(v.loadPlan(a), a.flashCmd(nInfo, "Reading the workflow file…"))
	}
	key := plannedKey(v.selectedNodeKey())
	for _, pj := range v.plan {
		if pj.ID == key && pj.File != nil {
			return a.push(newJobSourceView(a, pj, pj.Name))
		}
	}
	return tea.Batch(a.flashCmd(nHint, "This job isn't in the workflow file as such; showing the whole file."), v.showWorkflowSource(a))
}

// showWorkflowSource opens the run's whole workflow file, as it was when the run started.
func (v *runView) showWorkflowSource(a *App) tea.Cmd {
	return a.push(newWorkflowSourceView(a, v.repo, v.workflowPath(), v.run.HeadSHA))
}

// selectNodeAtCursor puts the pipeline's cursor on the job the list is on.
func (v *runView) selectNodeAtCursor() {
	it, ok := v.selected()
	if !ok {
		return
	}
	p := v.pipelineNow()
	if n := p.nodeOfJob(it.job); n != nil && it.kind != tGroup {
		v.g.sel = n.key
		return
	}
	if it.kind == tGroup {
		for _, n := range p.real() {
			for _, j := range n.jobs {
				if groupName(v.jobs[j].Name) == it.group {
					v.g.sel = n.key
					return
				}
			}
		}
	}
}

// graphKey moves the cursor through the pipeline.
// graphKeys are the keys of the pipeline; they come before the job list's.
func (v *runView) graphKeys(a *App) []Binding {
	return grouped("THE PIPELINE",
		Binding{Keys: []string{"up", "down", "left", "right", "k", "j"}, Label: "←↑↓→", Desc: "move", Help: "move along the jobs",
			Do: func(k string) tea.Cmd { return v.graphMove(a, k) }},
		Binding{Keys: []string{"enter"}, Desc: "logs", Help: "logs of the selected job", Do: do(func() tea.Cmd { return v.openLogs(a) })},
		Binding{Keys: []string{"=", "-"}, Label: "= -", Desc: "zoom", Help: "zoom in / out", Do: func(k string) tea.Cmd {
			if k == "+" || k == "=" {
				return v.zoomBy(a, 1)
			}
			return v.zoomBy(a, -1)
		}},
		Binding{Keys: []string{"0"}, Desc: "fit", Help: "fit the pipeline to the screen", Do: do(func() tea.Cmd {
			v.g.zoom, v.g.center = zoomFit, true
			return a.flashCmd(nInfo, "Zoom: fit to the screen")
		})},
		Binding{Keys: []string{"z"}, Desc: "matrices", Help: "how matrices show: framed legs, every leg, or one job", Do: do(func() tea.Cmd {
			v.g.matrices = (v.g.matrices + 1) % matrixModes
			v.g.center = true
			return a.flashCmd(nInfo, "Matrices: "+matrixModeNames[v.g.matrices]+" · m changes it")
		})},
		Binding{Help: "show the runner of each job", Do: do(func() tea.Cmd {
			v.g.runners = !v.g.runners
			if v.g.runners {
				return a.flashCmd(nInfo, "Boxes show the runner of each job · n hides it")
			}
			return a.flashCmd(nInfo, "Boxes show the workflow of each job again")
		})},
		Binding{Keys: []string{"Z"}, Desc: "full screen", Do: do(func() tea.Cmd {
			v.g.full, v.g.center = !v.g.full, true
			return nil
		})},
	)
}

// graphMove moves the selection to the next job in the direction of k.
func (v *runView) graphMove(a *App, k string) tea.Cmd {
	p := v.pipelineNow()
	n := p.byKey[v.g.sel]
	if n == nil {
		if real := p.real(); len(real) > 0 {
			n = real[0]
			v.g.sel = n.key
		}
	}
	if n == nil {
		return nil
	}
	v.follow = false
	dir := map[string]string{"k": "up", "j": "down", "h": "left", "l": "right"}[k]
	if dir == "" {
		dir = k
	}
	v.g.sel = p.step(n, dir).key
	return v.syncListToNode(a, p.byKey[v.g.sel])
}

// syncListToNode puts the job list's cursor on a node, so the details below
// the pipeline and the keys that act on a job follow it.
func (v *runView) syncListToNode(a *App, n *pnode) tea.Cmd {
	if n == nil || len(n.jobs) == 0 {
		return nil
	}
	first := v.jobs[n.jobs[0]]
	group := groupName(first.Name)
	if len(n.jobs) > 1 {
		v.expanded["g:"+group] = false
	} else {
		v.expanded["g:"+group] = true
	}
	v.rebuild()
	for i, it := range v.items {
		switch {
		case len(n.jobs) > 1 && it.kind == tGroup && it.group == group:
			v.cursor = i
			return v.ensure(a)
		case len(n.jobs) == 1 && it.kind == tJob && it.job == n.jobs[0]:
			v.cursor = i
			return v.ensure(a)
		}
	}
	return nil
}

// renderGraph draws the pipeline on top and the details of the selected job below.
func (v *runView) renderGraph(a *App, w, h int, now time.Time) string {
	p := v.pipelineNow()
	if len(p.real()) == 0 {
		msg := "Waiting for jobs to start…"
		if !v.jobsLoaded {
			msg = "Loading jobs…"
		}
		return box(title("Pipeline", "", -1, ""), " "+sDim.Render(msg), w, h, cFrame)
	}
	if p.byKey[v.g.sel] == nil {
		v.selectNodeAtCursor()
	}
	if p.byKey[v.g.sel] == nil {
		v.g.sel = p.real()[0].key
	}
	maxH := h - 10
	if v.g.full {
		maxH = h - 1
	}
	maxH = max(maxH, 9)
	p.zoom = v.g.zoom
	p.runners = v.g.runners
	if v.g.zoom == zoomFit {
		p.zoom = p.bestZoom(w-4, maxH-2)
	}
	v.g.zoomNow = p.zoom
	g := p.geometry()
	graphH := min(g.h+2, maxH)
	if v.g.full {
		graphH = h
	}
	note := ""
	switch {
	case v.planLoad == 1:
		note = "reading the workflow file…"
	case !p.known:
		note = "dependencies unknown: the workflow file could not be read"
	}
	mode := viewKeep
	switch {
	case v.follow || v.g.center:
		mode = viewCenter
	}
	v.g.center = false
	areaH := graphH - 2
	if v.g.full {
		areaH--
	}
	lines := p.render(a, v.g.sel, w-4, areaH, &v.g.offX, &v.g.offY, mode, now)
	for i, l := range lines {
		lines[i] = " " + l
	}
	if v.g.full {
		for len(lines) < areaH {
			lines = append(lines, "")
		}
		lines = append(lines, v.graphFooter(a, p, w-2, now))
	}
	zoom := zoomNames[p.zoom]
	if v.g.zoom == zoomFit {
		zoom += ", to fit"
	}
	if note != "" {
		note = " · " + note
	}
	top := box(title("Pipeline", p.summary(), -1, "")+sDim.Render("  "+zoom+note), strings.Join(lines, "\n"), w, graphH, cFrame)
	if v.g.full {
		return top
	}
	rt, rb, border := v.renderDetail(a, w-2, h-graphH-2, now)
	return top + "\n" + box(rt, rb, w, h-graphH, border)
}

// graphFooter describes the selected job, for when the pipeline fills the
// screen and the details below it are hidden.
func (v *runView) graphFooter(a *App, p *pipeline, w int, now time.Time) string {
	n := p.byKey[v.g.sel]
	if n == nil {
		return ""
	}
	parts := []string{glyph(n.kind, 0) + " " + sWhite.Bold(true).Render(strings.TrimPrefix(n.label, p.trim))}
	if d := nodeDuration(n, now); d != "" {
		parts = append(parts, sText.Render(d))
	}
	if n.typical > 0 && !n.started.IsZero() {
		end := n.ended
		if end.IsZero() {
			end = now
		}
		if vs := v.versus(a, end.Sub(n.started), n.typical, n.kind, n.samples); vs != "" {
			parts = append(parts, vs)
		}
	}
	parts = append(parts, detailStyle(n).Render(n.detail()))
	if prog := n.progress(); prog != "" {
		parts = append(parts, sDim.Render(prog+" steps"))
	}
	return fit(" "+strings.Join(parts, sDim.Render("  ·  ")), w)
}

// zoomFit picks the most detailed zoom that fits, again on every draw, so
// the pipeline stays whole on screen as it grows.
const zoomFit = -1

// zoomBy changes the zoom and puts the selected job in the middle, since
// every job moves when the boxes change size.
func (v *runView) zoomBy(a *App, d int) tea.Cmd {
	next := min(max(v.g.zoomNow+d, 0), zoomLevels-1)
	if next == v.g.zoomNow && v.g.zoom != zoomFit {
		return nil
	}
	v.g.zoom, v.g.zoomNow, v.g.center = next, next, true
	return a.flashCmd(nInfo, "Zoom: "+zoomNames[next]+"  ·  + − zoom, 0 fits it to the screen")
}
