package ui

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// pnode is one box of the pipeline: a job of the workflow, or a stretch of
// the line that carries a dependency across the columns it skips.
type pnode struct {
	key        string
	label      string
	kind       Kind
	jobs       []int // indexes into the run's jobs
	preds      []string
	succs      []string
	virtual    bool
	layer, row int
	started    time.Time
	ended      time.Time
	stepsDone  int
	stepsTotal int
	current    string        // the step a running job is on
	failedStep string        // the first step that failed
	runner     string        // the runner the job is on, or how many runners a matrix uses
	legs       map[Kind]int  // how the jobs of a matrix are doing
	leg        string        // for a leg of a matrix: what sets it apart from the other legs
	matrix     string        // for a leg of a matrix: the matrix's name
	group      string        // for a leg of a matrix: the matrix, which its legs are framed as
	port       bool          // stands for a matrix where lines meet the frame around its legs
	lastRow    int           // for a port: the row of the matrix's last leg; row is its first
	typical    time.Duration // how long the job usually takes, 0 when unknown
	samples    int           // how many earlier runs typical comes from
}

// pipeline is the shape of a run: its jobs in columns, left to right, each
// column holding the jobs that wait only for jobs in the columns before it.
type pipeline struct {
	id         string // the run and attempt it draws, so its motion is remembered across redraws
	byKey      map[string]*pnode
	layers     [][]*pnode
	known      bool   // the dependencies came from the workflow file
	trim       string // the start every job name shares, left out of the boxes
	zoom       int
	runners    bool              // boxes show the runner instead of the workflow a job was called through
	trunkCache map[int][][]trunk // the lines between the columns, per zoom
	ports      map[string]*pnode // per matrix shown as legs: where lines meet its frame
	legsOf     map[string][]*pnode
}

// applyTypical sets how long each job usually takes. A job made of several
// legs usually takes as long as its slowest leg, and is only known when all
// its legs are.
func (p *pipeline) applyTypical(jobs []gh.Job, typical map[string]gh.TypicalJob, previous bool) {
	for _, n := range p.real() {
		n.typical, n.samples = 0, 0
		for _, i := range n.jobs {
			t, ok := gh.TypicalFor(typical, jobs[i].Name)
			base := t.Median
			if previous {
				base = t.Previous
			}
			if !ok || base == 0 || (!previous && t.Samples == 0) {
				n.typical, n.samples = 0, 0
				break
			}
			n.typical = max(n.typical, base)
			if n.samples == 0 || t.Samples < n.samples {
				n.samples = t.Samples
			}
		}
	}
}

func (p *pipeline) real() []*pnode {
	var out []*pnode
	for _, col := range p.layers {
		for _, n := range col {
			if !n.virtual {
				out = append(out, n)
			}
		}
	}
	return out
}

// matchPlanned finds the planned job a run's job belongs to, "" if none.
// Matrix legs are "name (value)", jobs of a called workflow are "caller / job"
// and a name may hold an expression that is only known when the job runs.
func matchPlanned(plan []gh.PlannedJob, jobName string) string {
	base := groupName(jobName)
	caller, _, isCalled := strings.Cut(base, " / ")
	for _, pj := range plan {
		if jobName == pj.Name || base == pj.Name || base == pj.ID || jobName == pj.ID {
			return pj.ID
		}
	}
	for _, pj := range plan {
		if i := strings.Index(pj.Name, "${{"); i > 0 && strings.HasPrefix(base, pj.Name[:i]) {
			return pj.ID
		}
	}
	if isCalled {
		for _, pj := range plan {
			if caller == pj.Name || caller == pj.ID {
				return pj.ID
			}
		}
	}
	return ""
}

// calledThrough finds the planned jobs a job stands for when it is a call of
// a reusable workflow that never started (skipped, or cancelled before):
// GitHub then reports the call as one job, named after the caller.
func calledThrough(plan []gh.PlannedJob, jobName string) []string {
	var keys []string
	for _, pj := range plan {
		if strings.HasPrefix(pj.Name, jobName+" / ") {
			keys = append(keys, pj.ID)
		}
	}
	return keys
}

// How matrices show: each leg as a job unless the matrix is big, every leg,
// or each matrix as one job.
const (
	matrixAuto = iota
	matrixLegs
	matrixOne
	matrixModes
)

var matrixModeNames = [matrixModes]string{"legs of small matrices", "every leg", "one job per matrix"}

// maxLegs is the most legs a matrix shows as jobs of their own by default; a
// bigger one would push the rest of the pipeline off the screen.
const maxLegs = 6

// splitMatrices gives each leg of a matrix a job of its own, waiting for what
// the matrix waits for; what waits for the matrix waits for every leg. Unless
// all is set, a matrix of more than maxLegs legs stays one job.
func (p *pipeline) splitMatrices(order []*pnode, jobs []gh.Job, all bool) []*pnode {
	var out []*pnode
	legsOf := map[string][]string{}
	for _, n := range order {
		if len(n.jobs) < 2 || (!all && len(n.jobs) > maxLegs) {
			out = append(out, n)
			continue
		}
		names := make([]string, len(n.jobs))
		for k, i := range n.jobs {
			names[k] = jobs[i].Name
		}
		apart := differences(names)
		matrix := n.label
		if i := strings.LastIndex(matrix, " / "); i >= 0 {
			matrix = matrix[i+3:]
		}
		if strings.Contains(matrix, "${{") {
			matrix = expression.ReplaceAllString(matrix, "…")
		} else if strings.HasPrefix(n.key, "job:") {
			matrix = groupName(names[0])
		}
		for k, i := range n.jobs {
			leg := &pnode{key: fmt.Sprintf("%s#%d", n.key, jobs[i].ID), label: jobs[i].Name,
				preds: append([]string(nil), n.preds...), jobs: []int{i}, leg: apart[k], matrix: matrix, group: n.key}
			p.byKey[leg.key] = leg
			out = append(out, leg)
			legsOf[n.key] = append(legsOf[n.key], leg.key)
		}
		delete(p.byKey, n.key)
	}
	for _, n := range out {
		var preds []string
		for _, k := range n.preds {
			if legs, ok := legsOf[k]; ok {
				preds = append(preds, legs...)
			} else {
				preds = append(preds, k)
			}
		}
		n.preds = preds
	}
	return out
}

// differences cuts the start and end that all names share, at a word, and
// returns what is left of each: "Deploy api (1.2) to prod" and "Deploy web
// (2.0) to prod" become "api (1.2)" and "web (2.0)". A name with nothing left
// stays whole.
func differences(names []string) []string {
	rs := make([][]rune, len(names))
	shortest := -1
	for i, n := range names {
		rs[i] = []rune(n)
		if shortest < 0 || len(rs[i]) < shortest {
			shortest = len(rs[i])
		}
	}
	same := func(at func(r []rune) rune) bool {
		for _, r := range rs[1:] {
			if at(r) != at(rs[0]) {
				return false
			}
		}
		return true
	}
	pre := 0
	for pre < shortest && same(func(r []rune) rune { return r[pre] }) {
		pre++
	}
	suf := 0
	for suf < shortest-pre && same(func(r []rune) rune { return r[len(r)-1-suf] }) {
		suf++
	}
	const breaks = " (,/"
	for pre > 0 && !strings.ContainsRune(breaks, rs[0][pre-1]) {
		pre--
	}
	// A shared end stops at a space or a comma, or is the closing bracket
	// alone: "Test (windows-latest)" and "Test (macos-latest)" share
	// "-latest)", and differ in "windows-latest" and "macos-latest".
	for suf > 0 && !strings.ContainsRune(" ,", rs[0][len(rs[0])-suf]) && (suf != 1 || rs[0][len(rs[0])-1] != ')') {
		suf--
	}
	out := make([]string, len(names))
	for i, r := range rs {
		part := strings.Trim(string(r[pre:len(r)-suf]), " ,")
		if strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")") {
			part = part[1 : len(part)-1]
		}
		if part == "" {
			part = names[i]
		}
		out[i] = part
	}
	return out
}

// plannedKey is the planned job a node of the pipeline stands for: a leg of a
// matrix stands for the matrix.
func plannedKey(key string) string {
	planned, _, _ := strings.Cut(key, "#")
	return planned
}

var expression = regexp.MustCompile(`\$\{\{[^}]*\}\}`)

// matrixLabel names a job whose name holds an expression: as its one job is
// named, or with each expression shown as … when it ran several times.
func matrixLabel(planned string, jobs []gh.Job, idx []int) string {
	if len(idx) == 1 && !strings.Contains(jobs[idx[0]].Name, "${{") {
		return jobs[idx[0]].Name
	}
	return expression.ReplaceAllString(planned, "…")
}

// buildPipeline lays out the jobs of a run. With the planned jobs of its
// workflow, jobs that haven't started yet show too, and the dependencies are
// known; without, only the jobs that exist are shown, in one column. How a
// matrix shows, as one job or a job per leg, is up to matrices.
func buildPipeline(jobs []gh.Job, plan []gh.PlannedJob, prefix string, finished bool, matrices int) *pipeline {
	p := &pipeline{byKey: map[string]*pnode{}, known: len(plan) > 0, trim: prefix}
	var order []*pnode
	add := func(key, label string, needs []string) *pnode {
		n := &pnode{key: key, label: label, preds: needs}
		p.byKey[key] = n
		order = append(order, n)
		return n
	}
	for _, pj := range plan {
		add(pj.ID, pj.Name, pj.Needs)
	}
	for i, j := range jobs {
		name := j.Name
		keys := []string{matchPlanned(plan, name)}
		if keys[0] == "" {
			keys = calledThrough(plan, name)
		}
		if len(keys) == 0 {
			key := "job:" + groupName(name)
			if p.byKey[key] == nil {
				add(key, groupName(name), nil)
			}
			keys = []string{key}
		}
		for _, key := range keys {
			p.byKey[key].jobs = append(p.byKey[key].jobs, i)
		}
	}
	if matrices != matrixOne {
		order = p.splitMatrices(order, jobs, matrices == matrixLegs)
	}
	for _, n := range order {
		if strings.Contains(n.label, "${{") {
			n.label = matrixLabel(n.label, jobs, n.jobs)
		}
	}
	for _, n := range order {
		var kinds []Kind
		runners := map[string]bool{}
		n.legs = map[Kind]int{}
		for _, i := range n.jobs {
			j := jobs[i]
			kinds = append(kinds, jobKind(j))
			n.legs[jobKind(j)]++
			if j.RunnerName != "" {
				runners[j.RunnerName] = true
				n.runner = j.RunnerName
			}
			if !j.StartedAt.IsZero() && (n.started.IsZero() || j.StartedAt.Before(n.started)) {
				n.started = j.StartedAt
			}
			if j.Status == "completed" && j.CompletedAt.After(n.ended) {
				n.ended = j.CompletedAt
			}
			for _, st := range j.Steps {
				n.stepsTotal++
				if st.Status == "completed" {
					n.stepsDone++
				} else if st.Status == "in_progress" && n.current == "" {
					n.current = st.Name
				}
				if stepKind(st) == KFail && n.failedStep == "" {
					n.failedStep = st.Name
				}
			}
		}
		if len(runners) > 1 {
			n.runner = fmt.Sprintf("%d runners", len(runners))
		}
		switch {
		case len(kinds) > 0:
			n.kind = aggregate(kinds)
			if n.kind == KRun {
				n.ended = time.Time{}
			}
		case finished:
			n.kind = KSkip
		default:
			n.kind = KNone
		}
		var needs []string
		for _, k := range n.preds {
			if p.byKey[k] != nil && k != n.key {
				needs = append(needs, k)
			}
		}
		n.preds = needs
	}
	p.dropImpliedNeeds(order)

	p.assignLayers(order)
	p.spanLayers(order)
	p.order()
	p.buildPorts()
	return p
}

// dropImpliedNeeds leaves out a dependency that another one already implies:
// when a job needs A and B and B needs A, the line from A is left out. The
// order the jobs run in reads the same, with far fewer lines.
func (p *pipeline) dropImpliedNeeds(order []*pnode) {
	ancestors := map[string]map[string]bool{}
	var above func(n *pnode, seen map[string]bool) map[string]bool
	above = func(n *pnode, seen map[string]bool) map[string]bool {
		if a, ok := ancestors[n.key]; ok {
			return a
		}
		if seen[n.key] {
			return nil
		}
		seen[n.key] = true
		a := map[string]bool{}
		for _, k := range n.preds {
			a[k] = true
			for x := range above(p.byKey[k], seen) {
				a[x] = true
			}
		}
		ancestors[n.key] = a
		return a
	}
	for _, n := range order {
		above(n, map[string]bool{})
	}
	for _, n := range order {
		var kept []string
		for _, k := range n.preds {
			implied := false
			for _, other := range n.preds {
				if other != k && ancestors[other][k] {
					implied = true
					break
				}
			}
			if !implied {
				kept = append(kept, k)
			}
		}
		n.preds = kept
	}
}

func (p *pipeline) assignLayers(order []*pnode) {
	for range order {
		changed := false
		for _, n := range order {
			for _, k := range n.preds {
				if m := p.byKey[k]; n.layer < m.layer+1 && m.layer < len(order) {
					n.layer, changed = m.layer+1, true
				}
			}
		}
		if !changed {
			break
		}
	}
}

// spanLayers puts a stand-in in every column an edge passes over, so that
// each edge joins neighbouring columns only.
func (p *pipeline) spanLayers(order []*pnode) {
	width := 0
	for _, n := range order {
		width = max(width, n.layer+1)
	}
	p.layers = make([][]*pnode, width)
	for _, n := range order {
		p.layers[n.layer] = append(p.layers[n.layer], n)
	}
	// A long line from a job runs on for all the jobs further on that wait
	// for it, branching off at each. When several jobs wait far back for one
	// job, their lines instead join early and run on to it as one.
	standIn := func(from *pnode, layer int) *pnode {
		prev := from
		for l := from.layer + 1; l <= layer; l++ {
			key := fmt.Sprintf("~%s@%d", from.key, l)
			v := p.byKey[key]
			if v == nil {
				v = &pnode{key: key, virtual: true, layer: l, kind: from.kind, preds: []string{prev.key}}
				p.byKey[key] = v
				p.layers[l] = append(p.layers[l], v)
			}
			prev = v
		}
		return prev
	}
	joined := func(to *pnode, sources []*pnode) *pnode {
		first := to.layer
		for _, s := range sources {
			first = min(first, s.layer+1)
		}
		var prev *pnode
		for l := first; l < to.layer; l++ {
			v := &pnode{key: fmt.Sprintf("~>%s@%d", to.key, l), virtual: true, layer: l}
			if prev != nil {
				v.preds = append(v.preds, prev.key)
			}
			var kinds []Kind
			for _, s := range sources {
				if s.layer == l-1 {
					v.preds = append(v.preds, s.key)
				}
				kinds = append(kinds, s.kind)
			}
			v.kind = aggregate(kinds)
			p.byKey[v.key] = v
			p.layers[l] = append(p.layers[l], v)
			prev = v
		}
		return prev
	}
	for _, n := range order {
		var far []*pnode
		for _, k := range n.preds {
			if from := p.byKey[k]; n.layer-from.layer > 1 {
				far = append(far, from)
			}
		}
		var preds []string
		for _, k := range n.preds {
			from := p.byKey[k]
			switch {
			case n.layer-from.layer <= 1:
				preds = append(preds, k)
			case len(far) < 2:
				preds = append(preds, standIn(from, n.layer-1).key)
			}
		}
		if len(far) >= 2 {
			preds = append(preds, joined(n, far).key)
		}
		n.preds = preds
	}
	for _, col := range p.layers {
		for _, n := range col {
			for _, k := range n.preds {
				m := p.byKey[k]
				m.succs = append(m.succs, n.key)
			}
		}
	}
}

// straighten moves every job as close to the middle of its neighbours as the
// jobs above and below it allow, which pulls long edges back into line.
func (p *pipeline) straighten() {
	desired := func(n *pnode) int {
		keys := append(append([]string(nil), n.preds...), n.succs...)
		if n.virtual {
			keys = n.preds // a long line keeps its height, and bends once, near its end
		}
		if len(keys) == 0 {
			return n.row
		}
		sum := 0.0
		for _, k := range keys {
			sum += float64(p.byKey[k].row)
		}
		return int(math.Round(sum / float64(len(keys))))
	}
	for pass := range 6 {
		for _, col := range p.layers {
			for i := range col {
				idx := i
				if pass%2 == 1 {
					idx = len(col) - 1 - i
				}
				lo, hi := -1000, 1000
				if idx > 0 {
					lo = col[idx-1].row + 1
				}
				if idx+1 < len(col) {
					hi = col[idx+1].row - 1
				}
				col[idx].row = min(max(desired(col[idx]), lo), hi)
			}
		}
	}
}

// crossings counts the pairs of edges that cross between two neighbouring columns.
func (p *pipeline) crossings(l int) int {
	if l < 0 || l+1 >= len(p.layers) {
		return 0
	}
	at := map[string]int{}
	for i, n := range p.layers[l+1] {
		at[n.key] = i
	}
	var ends [][2]int
	for i, n := range p.layers[l] {
		for _, k := range n.succs {
			ends = append(ends, [2]int{i, at[k]})
		}
	}
	count := 0
	for i := range ends {
		for j := i + 1; j < len(ends); j++ {
			if (ends[i][0]-ends[j][0])*(ends[i][1]-ends[j][1]) < 0 {
				count++
			}
		}
	}
	return count
}

func (p *pipeline) totalCrossings() int {
	total := 0
	for l := range p.layers {
		total += p.crossings(l)
	}
	return total
}

func (p *pipeline) number() {
	for _, col := range p.layers {
		for i, n := range col {
			n.row = i
		}
	}
}

// order arranges each column to cross as few edges as it is cheap to find:
// sweeps that put jobs near the average of their neighbours, then swaps of
// neighbours that remove crossings, keeping the best arrangement seen.
func (p *pipeline) order() {
	p.number()
	average := func(n *pnode, keys []string) float64 {
		if len(keys) == 0 {
			return float64(n.row)
		}
		sum := 0.0
		for _, k := range keys {
			sum += float64(p.byKey[k].row)
		}
		return sum / float64(len(keys))
	}
	snapshot := func() [][]*pnode {
		out := make([][]*pnode, len(p.layers))
		for l, col := range p.layers {
			out[l] = append([]*pnode(nil), col...)
		}
		return out
	}
	best, bestCrossings := snapshot(), p.totalCrossings()
	for range 8 {
		for l := 1; l < len(p.layers); l++ {
			col := p.layers[l]
			sort.SliceStable(col, func(i, j int) bool { return average(col[i], col[i].preds) < average(col[j], col[j].preds) })
			p.number()
		}
		for l := len(p.layers) - 2; l >= 0; l-- {
			col := p.layers[l]
			sort.SliceStable(col, func(i, j int) bool { return average(col[i], col[i].succs) < average(col[j], col[j].succs) })
			p.number()
		}
		if c := p.totalCrossings(); c < bestCrossings {
			best, bestCrossings = snapshot(), c
		}
	}
	p.layers = best
	p.number()
	for range 10 {
		improved := false
		for l, col := range p.layers {
			for i := 0; i+1 < len(col); i++ {
				before := p.crossings(l-1) + p.crossings(l)
				col[i], col[i+1] = col[i+1], col[i]
				p.number()
				if p.crossings(l-1)+p.crossings(l) < before {
					improved = true
				} else {
					col[i], col[i+1] = col[i+1], col[i]
					p.number()
				}
			}
		}
		if !improved {
			break
		}
	}
	p.keepMatricesTogether()
	p.alignRows()
}

// buildPorts gives each matrix shown as legs one place where lines meet it:
// its legs wait for the same jobs and the same jobs wait for them, so lines
// go to the frame around them rather than to every leg.
func (p *pipeline) buildPorts() {
	p.ports, p.legsOf = map[string]*pnode{}, map[string][]*pnode{}
	for _, col := range p.layers {
		for _, n := range col {
			if n.group != "" {
				p.legsOf[n.group] = append(p.legsOf[n.group], n)
			}
		}
	}
	for group, legs := range p.legsOf {
		kinds := make([]Kind, len(legs))
		for i, n := range legs {
			kinds[i] = n.kind
		}
		p.ports[group] = &pnode{key: "port:" + group, layer: legs[0].layer, row: legs[0].row,
			lastRow: legs[len(legs)-1].row, group: group, port: true, kind: aggregate(kinds)}
	}
}

// asLines is the job as lines see it: a leg of a matrix is its matrix's port.
func (p *pipeline) asLines(n *pnode) *pnode {
	if n.group != "" && !n.port {
		return p.ports[n.group]
	}
	return n
}

// lineCol is column l as lines see it: each matrix once, where its first leg is.
func (p *pipeline) lineCol(l int) []*pnode {
	var out []*pnode
	seen := map[*pnode]bool{}
	for _, n := range p.layers[l] {
		if m := p.asLines(n); !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// lineSuccs are the jobs a job's lines lead to, as lines see them.
func (p *pipeline) lineSuccs(n *pnode) []*pnode {
	from := n
	if n.port {
		from = p.legsOf[n.group][0]
	}
	var out []*pnode
	seen := map[*pnode]bool{}
	for _, k := range from.succs {
		if m := p.asLines(p.byKey[k]); !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// flowsInto reports whether lines into n lead on into a running job: n is
// running, or n is a stand-in on a line to a job that is.
func (p *pipeline) flowsInto(n *pnode) bool {
	if !n.virtual {
		return n.kind == KRun
	}
	for _, m := range p.lineSuccs(n) {
		if p.flowsInto(m) {
			return true
		}
	}
	return false
}

// legKeys are the jobs a node of the lines stands for.
func (p *pipeline) legKeys(n *pnode) []string {
	if !n.port {
		return []string{n.key}
	}
	var keys []string
	for _, leg := range p.legsOf[n.group] {
		keys = append(keys, leg.key)
	}
	return keys
}

// keepMatricesTogether puts the legs of each matrix next to each other in
// their column, where the first of them is, so they can be framed as one.
func (p *pipeline) keepMatricesTogether() {
	for l, col := range p.layers {
		var out []*pnode
		placed := map[string]bool{}
		for _, n := range col {
			if n.group == "" {
				out = append(out, n)
				continue
			}
			if placed[n.group] {
				continue
			}
			placed[n.group] = true
			for _, m := range col {
				if m.group == n.group {
					out = append(out, m)
				}
			}
		}
		p.layers[l] = out
	}
	p.number()
}

// alignRows gives every job a row, leaving gaps where that lets a job sit at
// the height of the jobs it waits for or is waited for by, so chains run
// straight and lines bend as little as possible.
func (p *pipeline) alignRows() {
	median := func(keys []string, fallback int) int {
		if len(keys) == 0 {
			return fallback
		}
		sum := 0.0
		for _, k := range keys {
			sum += float64(p.byKey[k].row)
		}
		return int(math.Round(sum / float64(len(keys))))
	}
	place := func(col []*pnode, neighbours func(*pnode) []string) {
		last := -1
		for _, n := range col {
			n.row = max(median(neighbours(n), n.row), last+1)
			last = n.row
		}
	}
	for range 4 {
		for l := 1; l < len(p.layers); l++ {
			place(p.layers[l], func(n *pnode) []string { return n.preds })
		}
		for l := len(p.layers) - 2; l >= 0; l-- {
			place(p.layers[l], func(n *pnode) []string { return n.succs })
		}
	}
	for l := 1; l < len(p.layers); l++ {
		place(p.layers[l], func(n *pnode) []string { return n.preds })
	}
	p.straighten()
	p.dropEmptyRows()
}

// dropEmptyRows closes up rows no column has a job or a line in, which the
// alignment can leave behind.
func (p *pipeline) dropEmptyRows() {
	used := map[int]bool{}
	for _, col := range p.layers {
		for _, n := range col {
			used[n.row] = true
		}
	}
	rows := make([]int, 0, len(used))
	for r := range used {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	index := map[int]int{}
	for i, r := range rows {
		index[r] = i
	}
	for _, col := range p.layers {
		for _, n := range col {
			n.row = index[n.row]
		}
	}
}
