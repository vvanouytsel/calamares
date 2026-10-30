package ui

import "sort"

// trunk is a vertical line between two columns: a set of jobs that all lead
// to the same set of jobs, drawn as one line their lines join and branch off.
type trunk struct {
	targets []*pnode
	sources []*pnode
	exits   map[string]int // which line of each source's box its line leaves from
}

// straight reports whether the trunk is one source on its one target's row:
// a plain line across with no vertical part.
func (t trunk) straight(g geometry) bool {
	return len(t.sources) == 1 && len(t.targets) == 1 &&
		g.exitY(t.sources[0], t.exits[t.sources[0].key]) == g.lineY(t.targets[0])
}

func (t trunk) span(g geometry) (top, bottom int) {
	top, bottom = g.lineY(t.targets[0]), g.lineY(t.targets[0])
	for _, n := range t.targets {
		top, bottom = min(top, g.lineY(n)), max(bottom, g.lineY(n))
	}
	for _, s := range t.sources {
		y := g.exitY(s, t.exits[s.key])
		top, bottom = min(top, y), max(bottom, y)
	}
	return top, bottom
}

func (t trunk) feeds(n *pnode) bool {
	for _, x := range t.targets {
		if x == n {
			return true
		}
	}
	return false
}

// exits is how many lines a job can send lines out from: each line of a box
// inside its border, and for a one-line job its own line and the gap below.
func (g geometry) exits(n *pnode) int {
	switch {
	case n.virtual || n.port:
		return 1
	case g.zoom == zoomCompact:
		return 2
	}
	return g.boxH - 2
}

func (g geometry) exitY(n *pnode, exit int) int { return g.lineY(n) + exit }

// trunks are the lines between column l and the next, straight ones first,
// then the vertical ones left to right. They depend on the zoom, which sets
// how many lines a job can send lines out from, so they are kept per zoom.
func (p *pipeline) trunks(l int, g geometry) []trunk {
	if p.trunkCache == nil {
		p.trunkCache = map[int][][]trunk{}
	}
	all, ok := p.trunkCache[g.zoom]
	if !ok {
		all = make([][]trunk, len(p.layers))
		for i := range p.layers {
			all[i] = p.buildTrunks(i, g)
		}
		p.trunkCache[g.zoom] = all
	}
	if l < len(all) {
		return all[l]
	}
	return nil
}

func (p *pipeline) buildTrunks(l int, g geometry) []trunk {
	trunks := p.groupEdges(l)
	for _, s := range p.lineCol(l) {
		var mine []int
		for i, tr := range trunks {
			for _, src := range tr.sources {
				if src == s {
					mine = append(mine, i)
				}
			}
		}
		for i, exit := range assignExits(s, trunks, mine, g.exits(s)) {
			trunks[i].exits[s.key] = exit
		}
	}
	var straight, bent []trunk
	for _, t := range trunks {
		if t.straight(g) {
			straight = append(straight, t)
		} else {
			bent = append(bent, t)
		}
	}
	return append(straight, orderTrunks(bent, g)...)
}

// groupEdges splits the lines from column l to the next into trunks: sets of
// jobs that each lead to every job of a set. The trunk covering most lines is
// taken first, from what is still to draw, so lines share as much as they
// can, and every line is drawn once. Ties go to the trunk reaching more jobs,
// which keeps a job fanning out as one line.
func (p *pipeline) groupEdges(l int) []trunk {
	left := map[*pnode]map[*pnode]bool{} // target → its sources still to draw
	var targets []*pnode
	for _, s := range p.lineCol(l) {
		for _, t := range p.lineSuccs(s) {
			if left[t] == nil {
				left[t] = map[*pnode]bool{}
				targets = append(targets, t)
			}
			left[t][s] = true
		}
	}
	col := p.lineCol(l)
	setOf := func(m map[*pnode]bool) []*pnode {
		var out []*pnode
		for _, s := range col {
			if m[s] {
				out = append(out, s)
			}
		}
		return out
	}
	reaches := func(sources []*pnode) []*pnode {
		var out []*pnode
		for _, t := range targets {
			all := len(sources) > 0
			for _, s := range sources {
				all = all && left[t][s]
			}
			if all {
				out = append(out, t)
			}
		}
		return out
	}
	var trunks []trunk
	for {
		var candidates [][]*pnode
		for i, t := range targets {
			if len(left[t]) == 0 {
				continue
			}
			candidates = append(candidates, setOf(left[t]))
			for _, u := range targets[i+1:] {
				both := map[*pnode]bool{}
				for s := range left[t] {
					if left[u][s] {
						both[s] = true
					}
				}
				if len(both) > 0 {
					candidates = append(candidates, setOf(both))
				}
			}
		}
		if len(candidates) == 0 {
			return trunks
		}
		var best trunk
		bestScore := -1
		for _, sources := range candidates {
			reached := reaches(sources)
			score := len(sources) * len(reached)
			better := score > bestScore ||
				score == bestScore && len(reached) > len(best.targets) ||
				score == bestScore && len(reached) == len(best.targets) && len(sources) > len(best.sources)
			if better {
				best, bestScore = trunk{sources: sources, targets: reached}, score
			}
		}
		for _, t := range best.targets {
			for _, s := range best.sources {
				delete(left[t], s)
			}
		}
		best.exits = map[string]int{}
		trunks = append(trunks, best)
	}
}

// assignExits picks the line of a source's box each of its trunks is fed
// from. Trunks only this source feeds may share a line, as they only touch
// each other; a trunk shared with other sources needs a line of its own, so
// their lines never seem to lead where this source's do. The trunk into the
// job on the source's own row gets the first line, so it runs straight; the
// rest follow from top to bottom, sharing the last line when they run out.
func assignExits(s *pnode, trunks []trunk, mine []int, exits int) map[int]int {
	type group struct {
		trunks []int
		top    int
		level  bool // holds the trunk into the job on the source's row
	}
	var groups []*group
	private := &group{top: -1}
	for _, i := range mine {
		tr := trunks[i]
		level, top := false, tr.targets[0].row
		for _, t := range tr.targets {
			level = level || t.row == s.row
			top = min(top, t.row)
		}
		g := private
		if len(tr.sources) > 1 {
			g = &group{top: top}
			groups = append(groups, g)
		} else if private.top < 0 || top < private.top {
			private.top = top
		}
		g.trunks = append(g.trunks, i)
		g.level = g.level || level
	}
	if len(private.trunks) > 0 {
		groups = append(groups, private)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].level != groups[j].level {
			return groups[i].level
		}
		return groups[i].top < groups[j].top
	})
	out := map[int]int{}
	for row, g := range groups {
		for _, i := range g.trunks {
			out[i] = min(row, exits-1)
		}
	}
	return out
}

// trunkCrossings counts the lines that cross when trunk a is left of trunk
// b: b's lines in from its sources pass a's vertical line, and a's lines on
// to its jobs pass b's.
func trunkCrossings(a, b trunk, g geometry) int {
	count := 0
	at, ab := a.span(g)
	bt, bb := b.span(g)
	for _, s := range b.sources {
		if y := g.exitY(s, b.exits[s.key]); y >= at && y <= ab {
			count++
		}
	}
	for _, t := range a.targets {
		if y := g.lineY(t); y >= bt && y <= bb && !b.feeds(t) {
			count++
		}
	}
	return count
}

func trunkCost(order []trunk, g geometry) int {
	cost := 0
	for i := range order {
		for j := i + 1; j < len(order); j++ {
			cost += trunkCrossings(order[i], order[j], g)
		}
	}
	return cost
}

// maxExhaustiveTrunks is up to how many vertical lines every order is tried;
// past it, neighbours are swapped while that removes crossings.
const maxExhaustiveTrunks = 7

// orderTrunks puts the vertical lines in the order that crosses fewest lines.
func orderTrunks(trunks []trunk, g geometry) []trunk {
	sort.SliceStable(trunks, func(i, j int) bool { return trunks[i].targets[0].row < trunks[j].targets[0].row })
	if len(trunks) < 2 {
		return trunks
	}
	best := append([]trunk(nil), trunks...)
	bestCost := trunkCost(best, g)
	if len(trunks) <= maxExhaustiveTrunks {
		perm := append([]trunk(nil), trunks...)
		var try func(k int)
		try = func(k int) {
			if bestCost == 0 {
				return
			}
			if k == len(perm) {
				if c := trunkCost(perm, g); c < bestCost {
					best, bestCost = append([]trunk(nil), perm...), c
				}
				return
			}
			for i := k; i < len(perm); i++ {
				perm[k], perm[i] = perm[i], perm[k]
				try(k + 1)
				perm[k], perm[i] = perm[i], perm[k]
			}
		}
		try(0)
		return best
	}
	for improved := true; improved; {
		improved = false
		for i := 0; i+1 < len(best); i++ {
			if trunkCrossings(best[i+1], best[i], g) < trunkCrossings(best[i], best[i+1], g) {
				best[i], best[i+1] = best[i+1], best[i]
				improved = true
			}
		}
	}
	return best
}

func bentTrunks(trunks []trunk, g geometry) int {
	n := 0
	for _, t := range trunks {
		if !t.straight(g) {
			n++
		}
	}
	return n
}
