package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The zoom levels of a pipeline: one line per job, boxes, and boxes that also
// say how far each job is.
const (
	zoomCompact = iota
	zoomBoxes
	zoomDetail
	zoomLevels
)

var zoomNames = [zoomLevels]string{"compact", "boxes", "detailed"}

// Directions of a line through a cell, combined into one box-drawing glyph.
const (
	dirN uint8 = 1 << iota
	dirS
	dirE
	dirW
)

var lineGlyphs = map[uint8]rune{
	dirN: '│', dirS: '│', dirN | dirS: '│',
	dirE: '─', dirW: '─', dirE | dirW: '─',
	dirS | dirE: '╭', dirS | dirW: '╮', dirN | dirE: '╰', dirN | dirW: '╯',
	dirN | dirS | dirE: '├', dirN | dirS | dirW: '┤',
	dirE | dirW | dirS: '┬', dirE | dirW | dirN: '┴',
	dirN | dirS | dirE | dirW: '┼',
}

// Lines are drawn in one quiet colour, so the boxes carry the status. The
// lines of the selected job's chain are light, not a colour of their own, so
// the light that runs along them stays the only colour on a line; the rest
// of the pipeline steps back. They are colours of the theme on display, so
// they are made when a pipeline is drawn.
func chainColor() lipgloss.Color { return cText }
func asideColor() lipgloss.Color { return lipgloss.Color(mix(string(cDim), string(cBg), 0.5)) }

// asideFade is how far a job outside the selected job's chain fades towards
// the background: still readable, but out of the spotlight.
const asideFade = 0.5

// selColor is the colour a selected job is lit in: its status, or light
// text for a job without one.
func selColor(n *pnode) lipgloss.Color {
	if n.kind == KNone || n.kind == KSkip {
		return cText
	}
	return n.kind.Color()
}

// Motion in a pipeline: light runs along the lines into a job that is
// running, the box of a running job breathes, the chain of a job you select
// lights up in a wave from it, and a job that just passed or failed flashes
// in its colour. The pipeline is drawn in its final state at once; the
// motion is only colour on top of it.
const (
	pipeWaveMs  = 600
	pipeFlashMs = 1600
)

// A comet of light runs along each line into a running job, from where the
// line starts to the job, with a tail of flowTail cells behind it. It takes
// flowCellMs per cell, and at least flowEveryMs for the whole line.
const (
	flowEveryMs = 900
	flowCellMs  = 25
	flowTail    = 4
)

// flowLights is how lit each cell of a line into a running job is at ms.
// Where lines join on their way into the job, their comets meet at the join.
func flowLights(c *canvas, ms int64) map[[2]int]float64 {
	lights := map[[2]int]float64{}
	for _, sink := range c.sinks {
		dist := c.flowPath(sink)
		far := 0
		for _, d := range dist {
			far = max(far, d)
		}
		period := int64(max(flowEveryMs, (far+flowTail)*flowCellMs))
		head := float64(ms%period)/float64(period)*float64(far+1+flowTail) - 1
		for at, d := range dist {
			if k := head - float64(far-d); k >= 0 && k < flowTail {
				lights[at] = max(lights[at], 1-k/flowTail)
			}
		}
	}
	return lights
}

// upstream are the ways back along a line into a job: lines run from left
// to right, so back is west, or up or down where a line turns.
var upstream = []struct {
	dir, from uint8
	dx, dy    int
}{{dirW, dirE, -1, 0}, {dirN, dirS, 0, -1}, {dirS, dirN, 0, 1}}

// flowPath is how far each cell of the line flowing into sink is from it,
// following the line back to where it starts. Lines that meet in a cell join,
// except at a crossing, which each line passes straight through.
func (c *canvas) flowPath(sink [2]int) map[[2]int]int {
	type step struct {
		at      [2]int
		heading uint8
	}
	dist := map[[2]int]int{sink: 0}
	queue := []step{{sink, dirW}}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		parts := c.parts[s.at[1]][s.at[0]]
		ways := flowDirs(parts)
		if crossing(parts) {
			ways = s.heading
		}
		for _, u := range upstream {
			next := [2]int{s.at[0] + u.dx, s.at[1] + u.dy}
			if ways&u.dir == 0 || next[0] < 0 || next[1] < 0 || next[1] >= c.h {
				continue
			}
			if _, seen := dist[next]; seen || flowDirs(c.parts[next[1]][next[0]])&u.from == 0 {
				continue
			}
			dist[next] = dist[s.at] + 1
			queue = append(queue, step{next, u.dir})
		}
	}
	return dist
}

func flowDirs(parts []part) uint8 {
	var d uint8
	for _, p := range parts {
		if p.flow {
			d |= p.dirs
		}
	}
	return d
}

func hasFlow(parts []part) bool { return flowDirs(parts) != 0 }

// flowColor lights col with the comet: amber, white-hot at its head.
func flowColor(col lipgloss.Color, k float64) lipgloss.Color {
	c := mix(string(col), string(cAmber), math.Min(k*1.2, 1))
	if k > 0.75 {
		c = mix(c, string(cWhite), (k-0.75)*2.4)
	}
	return lipgloss.Color(c)
}

// flowing is set while a pipeline with light running along its lines is
// drawn, so the screen redraws often enough for it to look smooth.
var flowing bool

type cell struct {
	r  rune
	st lipgloss.Style
}

// part is a piece of line through a cell, and which line it belongs to.
type part struct {
	line int
	dirs uint8
	hot  bool
	flow bool // the line leads into a job that is running
}

type canvas struct {
	w, h  int
	cells [][]cell
	parts [][][]part
	flow  bool     // lines marked now lead into a running job
	sinks [][2]int // where lines into a running job end
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h}
	c.cells = make([][]cell, h)
	c.parts = make([][][]part, h)
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		c.parts[y] = make([][]part, w)
	}
	return c
}

func (c *canvas) put(x, y int, r rune, st lipgloss.Style) {
	if x >= 0 && x < c.w && y >= 0 && y < c.h {
		c.cells[y][x] = cell{r, st}
	}
}

func (c *canvas) text(x, y int, s string, st lipgloss.Style) int {
	for _, r := range s {
		c.put(x, y, r, st)
		x++
	}
	return x
}

func (c *canvas) mark(x, y, line int, d uint8, hot bool) {
	if x < 0 || x >= c.w || y < 0 || y >= c.h {
		return
	}
	for i, p := range c.parts[y][x] {
		if p.line == line {
			c.parts[y][x][i].dirs |= d
			c.parts[y][x][i].hot = p.hot || hot
			c.parts[y][x][i].flow = p.flow || c.flow
			return
		}
	}
	c.parts[y][x] = append(c.parts[y][x], part{line, d, hot, c.flow})
}

func (c *canvas) hline(x1, x2, y, line int, hot bool) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		var d uint8
		if x > x1 {
			d |= dirW
		}
		if x < x2 {
			d |= dirE
		}
		c.mark(x, y, line, d, hot)
	}
}

func (c *canvas) vline(x, y1, y2, line int, hot bool) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		var d uint8
		if y > y1 {
			d |= dirN
		}
		if y < y2 {
			d |= dirS
		}
		c.mark(x, y, line, d, hot)
	}
}

// lineGlyph draws the lines through a cell. Lines that meet join; a line
// across that passes a line up and down it has nothing to do with is drawn
// unbroken, leaving a gap in the other, so a crossing never reads as a join.
func lineGlyph(parts []part) (rune, bool) {
	hotAcross := false
	var d uint8
	hot := false
	for _, p := range parts {
		if p.dirs == dirE|dirW {
			hotAcross = hotAcross || p.hot
		}
		d |= p.dirs
		hot = hot || p.hot
	}
	if crossing(parts) {
		return '─', hotAcross
	}
	return lineGlyphs[d], hot
}

// crossing reports whether the lines through a cell only pass each other:
// lines across, and lines up and down, none turning or ending there.
func crossing(parts []part) bool {
	across, upDown := 0, 0
	for _, p := range parts {
		switch p.dirs {
		case dirE | dirW:
			across++
		case dirN | dirS:
			upDown++
		}
	}
	return across > 0 && upDown > 0 && across+upDown == len(parts)
}

// geometry is where the columns and rows of a pipeline are drawn.
type geometry struct {
	colX, colW, gutter []int
	w, h               int
	boxH               int // lines a job takes
	zoom               int
	inset              []int // per column: room left for a matrix frame on each side
	pad                int   // lines above and below everything, for frames on the first and last row
}

// maxBoxWidth is the widest a job gets at each zoom; a longer name is cut
// with an ellipsis, and shown in full where the job is described.
var maxBoxWidth = [zoomLevels]int{44, 40, 44}

// names splits a job's name into the workflows it was called through and its
// own name: "Publish documentation / techdocs" is ("Publish documentation",
// "techdocs"). A leg of a matrix is named by what sets it apart; the frame
// around the legs names the matrix.
func (p *pipeline) names(n *pnode) (path, name string) {
	if n.leg != "" {
		return "", n.leg // the frame around the legs names the matrix
	}
	full := strings.TrimPrefix(n.label, p.trim)
	if i := strings.LastIndex(full, " / "); i >= 0 {
		path, full = full[:i], full[i+3:]
	}
	return path, full + nodeExtra(n)
}

func (p *pipeline) displayLabel(n *pnode) string {
	path, name := p.names(n)
	if path == "" {
		return name
	}
	return path + " / " + name
}

func runnerLine(n *pnode) string {
	if n.runner == "" {
		return "no runner yet"
	}
	return n.runner
}

func nodeExtra(n *pnode) string {
	if len(n.jobs) > 1 {
		return fmt.Sprintf(" ×%d", len(n.jobs))
	}
	return ""
}

func runeLen(s string) int { return len([]rune(s)) }

func (p *pipeline) geometry() geometry {
	g := geometry{zoom: p.zoom}
	switch p.zoom {
	case zoomCompact:
		g.boxH = 1
	case zoomBoxes:
		g.boxH = 4
	default:
		g.boxH = 5
	}
	x, rows := 0, 1
	for _, col := range p.layers {
		for _, n := range col {
			if n.group != "" {
				g.pad = 1
			}
		}
	}
	for l, col := range p.layers {
		inset := 0
		for _, n := range col {
			if n.group != "" {
				inset = 2 // the frame, and a space between it and the legs
			}
		}
		g.inset = append(g.inset, inset)
		cw := 14
		for _, n := range col {
			rows = max(rows, n.row+1)
			if n.virtual {
				continue
			}
			path, name := p.names(n)
			if p.runners {
				path = runnerLine(n)
			}
			var want int
			switch p.zoom {
			case zoomCompact:
				want = runeLen(p.displayLabel(n)) + 13
				if p.runners {
					want += runeLen(n.runner) + 1
				}
			case zoomBoxes:
				want = max(runeLen(name)+6, runeLen(path)+15)
			default:
				want = max(runeLen(name)+6, runeLen(path)+15, runeLen(n.detail())+11)
			}
			cw = max(cw, min(want, maxBoxWidth[p.zoom]))
			if n.group != "" {
				cw = max(cw, runeLen(frameTitle(p, n, 99))+runeLen(" 99 legs ")+6-2*inset)
			}
		}
		cw += 2 * inset
		g.colX = append(g.colX, x)
		g.colW = append(g.colW, cw)
		x += cw
		if l < len(p.layers)-1 {
			width := 2*bentTrunks(p.trunks(l, g), g) + 3
			g.gutter = append(g.gutter, width)
			x += width
		}
	}
	g.w, g.h = x, rows*g.rowH()-1+2*g.pad
	return g
}

func (g geometry) rowH() int { return g.boxH + 1 }

// rect is where a node's box sits; for a matrix's port, the frame around its legs.
func (g geometry) rect(n *pnode) (x0, y0, x1, y1 int) {
	if n.port {
		y0, y1 = g.frameRows(n)
		return g.colX[n.layer], y0, g.colX[n.layer] + g.colW[n.layer] - 1, y1
	}
	in := g.inset[n.layer]
	x0, y0 = g.colX[n.layer]+in, g.pad+n.row*g.rowH()
	return x0, y0, g.colX[n.layer] + g.colW[n.layer] - 1 - in, y0 + g.boxH - 1
}

// frameRows are the top and bottom lines of the frame around a matrix's legs.
func (g geometry) frameRows(port *pnode) (top, bottom int) {
	return g.pad + port.row*g.rowH() - 1, g.pad + port.lastRow*g.rowH() + g.boxH
}

// lineY is the row of a node where its edges join it: its name, or for a
// matrix's port the middle of its frame.
func (g geometry) lineY(n *pnode) int {
	if n.port {
		y0, y1 := g.frameRows(n)
		return (y0 + y1) / 2
	}
	return g.pad + n.row*g.rowH() + min(1, g.boxH-1)
}

// chain is the selected job with everything it waits for and everything
// that waits for it, stand-ins included.
type chain struct{ up, down map[string]bool }

func (p *pipeline) chainOf(key string) chain {
	c := chain{up: map[string]bool{}, down: map[string]bool{}}
	if p.byKey[key] == nil {
		return c
	}
	var walk func(k string, seen map[string]bool, next func(*pnode) []string)
	walk = func(k string, seen map[string]bool, next func(*pnode) []string) {
		if seen[k] {
			return
		}
		seen[k] = true
		for _, n := range next(p.byKey[k]) {
			walk(n, seen, next)
		}
	}
	walk(key, c.up, func(n *pnode) []string { return n.preds })
	walk(key, c.down, func(n *pnode) []string { return n.succs })
	return c
}

// lit reports whether the edge from s to t is part of the chain.
func (c chain) lit(s, t string) bool { return c.up[t] || c.down[s] }

// stretch is the rows a vertical line runs between.
type stretch struct {
	top, bottom int
	set         bool
}

func (s *stretch) add(y int) {
	if !s.set {
		*s = stretch{y, y, true}
		return
	}
	s.top, s.bottom = min(s.top, y), max(s.bottom, y)
}

// draw paints the pipeline; selected is the key of the node with the cursor.
func (p *pipeline) draw(g geometry, selected string, frame int, now time.Time) *canvas {
	c := newCanvas(g.w, g.h)
	ch := p.chainOf(selected)
	selP := glowProgress(changedAtS("pipesel:"+p.id, selected), pipeWaveMs)
	selX := -1
	sel := p.byKey[selected]
	if sel != nil {
		x0, _, x1, _ := g.rect(sel)
		selX = (x0 + x1) / 2
	}
	for l, col := range p.layers {
		for _, n := range col {
			if !n.virtual {
				p.drawNode(c, g, n, n.key == selected, ch, frame, now)
			}
		}
		if l == len(p.layers)-1 {
			continue
		}
		gx := g.colX[l] + g.colW[l]
		bent := 0
		for i, tr := range p.trunks(l, g) {
			line := (l+1)*1000 + i
			lx := gx
			if !tr.straight(g) {
				lx = gx + 2 + 2*bent
				bent++
			}
			lit := func(s, t *pnode) bool {
				for _, a := range p.legKeys(s) {
					for _, b := range p.legKeys(t) {
						if ch.lit(a, b) {
							return true
						}
					}
				}
				return false
			}
			var all, lights, flows stretch
			running := false
			for _, t := range tr.targets {
				running = running || p.flowsInto(t)
			}
			c.flow = running
			for _, s := range tr.sources {
				hot := false
				for _, t := range tr.targets {
					hot = hot || lit(s, t)
				}
				exit := tr.exits[s.key]
				y := g.exitY(s, exit)
				all.add(y)
				flows.add(y)
				if hot {
					lights.add(y)
				}
				_, _, sx1, _ := g.rect(s)
				start := sx1 + 1
				switch {
				case s.virtual:
					start = g.colX[l]
				case exit > 0 && g.zoom == zoomCompact:
					c.vline(start, g.lineY(s), y, ownLine(s), hot)
				case exit > 0 && (s.key != selected || ascii):
					_, _, x1, _ := g.rect(s)
					c.put(x1, y, '├', p.borderStyle(s, s.key == selected))
				}
				c.hline(start, lx, y, line, hot)
			}
			for _, t := range tr.targets {
				hot := false
				for _, s := range tr.sources {
					hot = hot || lit(s, t)
				}
				y := g.lineY(t)
				all.add(y)
				if hot {
					lights.add(y)
				}
				c.flow = p.flowsInto(t)
				tx0, _, _, _ := g.rect(t)
				stop := tx0 - 1
				if t.virtual {
					stop = g.colX[l+1] + g.colW[l+1] - 1
				}
				c.hline(lx, stop, y, line, hot)
				if c.flow {
					flows.add(y)
					if !t.virtual {
						c.sinks = append(c.sinks, [2]int{stop, y})
					}
				}
			}
			// The vertical line is only lit, and only carries light, as far
			// as the lines that join it are.
			c.flow = false
			if all.top != all.bottom {
				c.vline(lx, all.top, all.bottom, line, false)
				if lights.set {
					c.vline(lx, lights.top, lights.bottom, line, true)
				}
				if running {
					c.flow = true
					c.vline(lx, flows.top, flows.bottom, line, false)
					c.flow = false
				}
			}
		}
	}
	front := ease(selP) * float64(g.w)
	var lights map[[2]int]float64
	if animLevel >= animFull {
		lights = flowLights(c, nowMs())
	}
	for y := range c.cells {
		for x := range c.cells[y] {
			if parts := c.parts[y][x]; len(parts) > 0 && c.cells[y][x].r == 0 {
				r, hot := lineGlyph(parts)
				flow := hasFlow(parts)
				col := cDim
				switch {
				case hot:
					col = chainColor()
				case sel != nil:
					col = asideColor()
				}
				// The chain of a job just selected lights up from it outwards,
				// with a white front.
				if hot && selP < 1 && selX >= 0 {
					switch d := math.Abs(float64(x - selX)); {
					case d > front:
						col = asideColor()
					case front-d < 2.5:
						col = cWhite
					}
				}
				if flow && lights != nil {
					flowing, animated, smooth = true, true, true
					if k := lights[[2]int{x, y}]; k > 0 {
						col = flowColor(col, k)
					}
				}
				c.cells[y][x] = cell{r, fg(col)}
			}
		}
	}
	p.drawMatrixFrames(c, g, ch, sel != nil)
	if sel != nil {
		p.spotlight(c, g, ch)
	}
	return c
}

// onChain reports whether a job is the selected one, waits for it, or is
// waited for by it.
func (c chain) onChain(key string) bool { return c.up[key] || c.down[key] }

// spotlight fades the jobs outside the selected job's chain towards the
// background: still readable, but out of the light.
func (p *pipeline) spotlight(c *canvas, g geometry, ch chain) {
	for _, n := range p.real() {
		x0, y0, x1, y1 := g.rect(n)
		if ch.onChain(n.key) {
			continue
		}
		for y := max(y0, 0); y <= y1 && y < c.h; y++ {
			for x := max(x0, 0); x <= x1 && x < c.w; x++ {
				cl := &c.cells[y][x]
				if col, ok := cl.st.GetForeground().(lipgloss.Color); ok && strings.HasPrefix(string(col), "#") {
					cl.st = cl.st.Foreground(lipgloss.Color(mix(string(col), string(cBg), asideFade)))
				}
			}
		}
	}
}

// frameTitle is the name on a matrix's frame: the whole name of the matrix.
func frameTitle(p *pipeline, leg *pnode, legs int) string {
	return " ⊞ " + strings.TrimPrefix(leg.matrix, p.trim) + " "
}

// drawMatrixFrames draws a frame around the legs of each matrix: its whole
// name on the left of the top edge and how many legs it has on the right.
// Lines in and out meet the frame in the middle of its sides. While a job is
// selected, the frame of a matrix none of whose legs are in its chain fades.
func (p *pipeline) drawMatrixFrames(c *canvas, g geometry, ch chain, spot bool) {
	put := func(x, y int, r rune, s lipgloss.Style) {
		if x >= 0 && x < c.w && y >= 0 && y < c.h && c.cells[y][x].r == 0 {
			c.put(x, y, r, s)
		}
	}
	text := func(x, y int, s string, st lipgloss.Style) {
		for _, r := range s {
			put(x, y, r, st)
			x++
		}
	}
	for l, col := range p.layers {
		var legs []*pnode
		flush := func() {
			if len(legs) == 0 {
				return
			}
			lit := !spot
			for _, leg := range legs {
				lit = lit || ch.onChain(leg.key)
			}
			frameC, countC := string(cTan), string(cDim)
			if !lit {
				frameC, countC = mix(frameC, string(cBg), asideFade), mix(countC, string(cBg), asideFade)
			}
			edge, title, dim := fg(lipgloss.Color(frameC)), fg(lipgloss.Color(frameC)).Bold(true), fg(lipgloss.Color(countC))
			_, top, _, _ := g.rect(legs[0])
			_, _, _, bottom := g.rect(legs[len(legs)-1])
			x0, x1, y0, y1 := g.colX[l], g.colX[l]+g.colW[l]-1, top-1, bottom+1
			mid := (y0 + y1) / 2
			if len(legs[0].preds) > 0 {
				put(x0, mid, '┤', edge)
			}
			if len(legs[0].succs) > 0 {
				put(x1, mid, '├', edge)
			}
			count := fmt.Sprintf(" %d legs ", len(legs))
			text(x1-1-runeLen(count), y0, count, dim)
			text(x0+1, y0, cut(frameTitle(p, legs[0], len(legs)), x1-x0-3-runeLen(count)), title)
			put(x0, y0, '┌', edge)
			put(x1, y0, '┐', edge)
			put(x0, y1, '└', edge)
			put(x1, y1, '┘', edge)
			for x := x0 + 1; x < x1; x++ {
				put(x, y0, '─', edge)
				put(x, y1, '─', edge)
			}
			for y := y0 + 1; y < y1; y++ {
				put(x0, y, '│', edge)
				put(x1, y, '│', edge)
			}
			legs = nil
		}
		for _, n := range col {
			if n.group == "" || (len(legs) > 0 && legs[0].group != n.group) {
				flush()
			}
			if n.group != "" {
				legs = append(legs, n)
			}
		}
		flush()
	}
}

// ownLine is the line a one-line job draws from its name to its lines out.
func ownLine(n *pnode) int { return -1 - n.layer*1000 - n.row }

func (p *pipeline) drawNode(c *canvas, g geometry, n *pnode, selected bool, ch chain, frame int, now time.Time) {
	if g.zoom == zoomCompact {
		p.drawCompact(c, g, n, selected, ch, frame, now)
		return
	}
	p.drawBox(c, g, n, selected, frame, now)
}

func nodeDuration(n *pnode, now time.Time) string {
	if n.started.IsZero() {
		return ""
	}
	end := n.ended
	if end.IsZero() {
		end = now
	}
	return dur(end.Sub(n.started))
}

// nodeNote compares how long a job has taken with how long it usually does.
func nodeNote(n *pnode, now time.Time) (string, lipgloss.Color) {
	if n.started.IsZero() || n.typical <= 0 || (n.kind != KOK && n.kind != KRun) {
		return "", cDim
	}
	end := n.ended
	if end.IsZero() {
		end = now
	}
	return versusTypical(end.Sub(n.started), n.typical, n.ended.IsZero())
}

// cut shortens s to at most w cells, ending in an ellipsis when it had to.
func cut(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:max(w, 0)])
	}
	return string(r[:w-1]) + "…"
}

// splitAt breaks s into a first part of at most w cells, at a space or
// before a bracket when there is one in its second half, and the rest.
func splitAt(s string, w int) (string, string) {
	r := []rune(s)
	at := w
	for i := w; i > w/2; i-- {
		if r[i] == ' ' || r[i] == '(' {
			at = i
			break
		}
	}
	return strings.TrimRight(string(r[:at]), " "), strings.TrimLeft(string(r[at:]), " ")
}

// drawCompact draws a job as one line: its status, the workflow it was
// called through, dimmed, its name and how long it took, then a line on to
// the jobs that wait for it.
func (p *pipeline) drawCompact(c *canvas, g geometry, n *pnode, selected bool, ch chain, frame int, now time.Time) {
	x0, y0, x1, _ := g.rect(n)
	base := lipgloss.NewStyle()
	if selected {
		sp := glowProgress(changedAtS("pipesel:"+p.id, n.key), pipeWaveMs)
		base = base.Background(selFill(n, true, sp)).Bold(true)
	}
	right := nodeDuration(n, now)
	_, rightColor := nodeNote(n, now)
	room := x1 - x0 - 4 - runeLen(right)
	path, name := p.names(n)
	c.put(x0, y0, ' ', base)
	c.text(x0+1, y0, n.kind.Glyph(frame), base.Foreground(n.kind.Color()))
	c.put(x0+2, y0, ' ', base)
	x := x0 + 3
	if path != "" && runeLen(path)+3+runeLen(name) <= room {
		x = c.text(x, y0, path+" / ", base.Foreground(cDim))
		room -= runeLen(path) + 3
	}
	nameStyle := base.Foreground(n.kind.RowColor())
	if selected {
		nameStyle = base.Foreground(cWhite)
	}
	x = c.text(x, y0, cut(name, room), nameStyle)
	if right != "" {
		x = c.text(x, y0, " "+right, base.Foreground(rightColor))
	}
	if p.runners && n.runner != "" && x+runeLen(n.runner)+2 <= x1 {
		x = c.text(x, y0, " "+n.runner, base.Foreground(cDim))
	}
	x = c.text(x, y0, " ", base)
	if len(n.succs) > 0 && n.group == "" {
		hot := false
		for _, s := range n.succs {
			hot = hot || ch.lit(n.key, s)
		}
		for _, s := range p.lineSuccs(n) {
			c.flow = c.flow || p.flowsInto(s)
		}
		c.hline(min(x, x1), x1+1, y0, ownLine(n), hot) // marks under the name are not drawn
		c.flow = false
	}
}

// drawBox draws a job as a box: its status and name with how long it took,
// the workflow it was called through, and at the detailed zoom how far it is.
// borderStyle is the colour of a job's box: its status, dim while it has
// not run. A selected job's box is a card whose solid edge is a deep tint
// of its colour, a step above its inside.
func (p *pipeline) borderStyle(n *pnode, selected bool) lipgloss.Style {
	quiet := n.kind == KNone || n.kind == KSkip
	switch {
	case selected && ascii:
		return fg(selColor(n)).Bold(true)
	case selected:
		return fg(lipgloss.Color(mix(string(cBg), string(selColor(n)), cardEdge)))
	case quiet:
		return fg(cFrameDim)
	}
	return fg(n.kind.Color())
}

// cardEdge is how much of its colour the edge of a selected job's card has.
const cardEdge = 0.45

// selFill is the inside of a selected job: a tint of its colour, a little
// stronger behind its name, like the head of a card, and brighter for a
// moment as it is selected (p runs from 0 to 1).
func selFill(n *pnode, head bool, p float64) lipgloss.Color {
	t := 0.09
	if head {
		t = 0.17
	}
	return lipgloss.Color(mix(string(cBg), string(selColor(n)), t+0.18*(1-ease(p))))
}

func (p *pipeline) drawBox(c *canvas, g geometry, n *pnode, selected bool, frame int, now time.Time) {
	x0, y0, x1, y1 := g.rect(n)
	border := p.borderStyle(n, selected)
	sp := 1.0
	switch {
	case selected:
		// It lands in its full colour and settles on its tint.
		if sp = glowProgress(changedAtS("pipesel:"+p.id, n.key), pipeWaveMs); sp < 1 {
			border = fg(lipgloss.Color(mix(string(selColor(n)), string(cBg), (1-cardEdge)*ease(sp))))
		}
	case n.kind == KRun && animLevel >= animSubtle:
		border = fg(lipgloss.Color(mix(string(cAmber), string(cWhite), 0.4*pulse(1600*time.Millisecond))))
	}
	eTop, eBottom, eLeft, eRight := '─', '─', '│', '│'
	tl, tr, bl, br := '╭', '╮', '╰', '╯'
	card := selected && !ascii
	switch {
	case card:
		// The selected job is a filled card: its edge is half a cell of solid
		// colour around a tinted inside, instead of a thin line.
		eTop, eBottom, eLeft, eRight = '▄', '▀', '▐', '▌'
		tl, tr, bl, br = '▗', '▖', '▝', '▘'
	case n.kind == KNone && !selected:
		eTop, eBottom, eLeft, eRight = '┄', '┄', '┆', '┆'
	}
	c.put(x0, y0, tl, border)
	c.put(x1, y0, tr, border)
	c.put(x0, y1, bl, border)
	c.put(x1, y1, br, border)
	for x := x0 + 1; x < x1; x++ {
		c.put(x, y0, eTop, border)
		c.put(x, y1, eBottom, border)
	}
	for y := y0 + 1; y < y1; y++ {
		c.put(x0, y, eLeft, border)
		c.put(x1, y, eRight, border)
	}
	join := g.lineY(n)
	if len(n.succs) > 0 && n.group == "" && !card {
		c.put(x1, join, '├', border)
	}
	if len(n.preds) > 0 && n.group == "" && !card {
		c.put(x0, join, '┤', border)
	}

	nameStyle := fg(n.kind.RowColor())
	if selected {
		nameStyle = sWhite.Bold(true)
	}
	inner := x1 - x0 - 3
	right := nodeDuration(n, now)
	path, name := p.names(n)
	c.text(x0+2, y0+1, n.kind.Glyph(frame), fg(n.kind.Color()))
	room := inner - 2
	if path == "" && runeLen(name) > room {
		first, rest := splitAt(name, room)
		c.text(x0+4, y0+1, first, nameStyle)
		c.text(x0+4, y0+2, cut(rest, room-runeLen(right)-1), nameStyle)
	} else {
		c.text(x0+4, y0+1, cut(name, room), nameStyle)
		second := path
		if p.runners {
			second = runnerLine(n)
		}
		c.text(x0+4, y0+2, cut(second, room-runeLen(right)-1), sDim)
	}
	c.text(x1-1-runeLen(right), y0+2, right, sDim)
	if note, color := nodeNote(n, now); note != "" && runeLen(note)+4 <= x1-x0 {
		c.text(x1-runeLen(note)-3, y1, " "+note+" ", fg(color))
	}
	if g.zoom == zoomDetail {
		detail, tail := n.detail(), n.progress()
		c.text(x0+4, y0+3, cut(detail, inner-2-runeLen(tail)-1), detailStyle(n))
		c.text(x1-1-runeLen(tail), y0+3, tail, sDim)
	}
	// The selected job is filled inside its border, so its rounded corners
	// stay crisp.
	if selected {
		c.tint(x0+1, y0+1, x1-1, y1-1, selFill(n, false, sp))
		c.tint(x0+1, y0+1, x1-1, y0+1, selFill(n, true, sp))
	}
	// A job that just passed or failed flashes in its colour.
	if at := changedAt("pnode:"+p.id+n.key, int(n.kind)); at != 0 && (n.kind == KOK || n.kind == KFail) {
		if fp := glowProgress(at, pipeFlashMs); fp < 1 {
			base := cBg
			if selected {
				base = selFill(n, false, sp)
			}
			c.tint(x0, y0, x1, y1, lipgloss.Color(mix(string(base), string(n.kind.Color()), 0.35*(1-ease(fp)))))
		}
	}
}

func (c *canvas) tint(x0, y0, x1, y1 int, bg lipgloss.Color) {
	for y := max(y0, 0); y <= y1 && y < c.h; y++ {
		for x := max(x0, 0); x <= x1 && x < c.w; x++ {
			cl := &c.cells[y][x]
			if cl.r == 0 {
				cl.r = ' '
			}
			cl.st = cl.st.Background(bg)
		}
	}
}

func detailStyle(n *pnode) lipgloss.Style {
	switch n.kind {
	case KFail:
		return sRed
	case KRun:
		return sAmber
	}
	return sHint
}

// detail says what a job is doing: the step it is on, the step that failed,
// how the legs of a matrix are doing, or why it has not started.
func (n *pnode) detail() string {
	switch {
	case len(n.jobs) > 1:
		var parts []string
		for _, k := range []Kind{KOK, KFail, KRun, KQueue, KWait, KCancel, KSkip} {
			if c := n.legs[k]; c > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", c, k.Label()))
			}
		}
		return strings.Join(parts, " · ")
	case n.kind == KNone:
		return ""
	case n.kind == KSkip && len(n.jobs) == 0:
		return "did not run"
	case n.current != "":
		return "▸ " + n.current
	case n.failedStep != "":
		return "✗ " + n.failedStep
	case n.kind == KQueue:
		return "waiting for a runner"
	case n.kind == KWait:
		return "waiting for approval"
	case n.kind == KOK && n.stepsTotal > 0:
		return fmt.Sprintf("%d steps", n.stepsTotal)
	}
	return n.kind.Label()
}

// progress counts the steps done, for a job that is running.
func (n *pnode) progress() string {
	if n.stepsTotal == 0 || len(n.jobs) > 1 || n.kind != KRun {
		return ""
	}
	return fmt.Sprintf("%d/%d", n.stepsDone, n.stepsTotal)
}

// summary counts the jobs of the pipeline by how they are doing.
func (p *pipeline) summary() string {
	nodes := p.real()
	done, failed, running := 0, 0, 0
	for _, n := range nodes {
		switch n.kind {
		case KOK, KSkip, KCancel:
			done++
		case KFail:
			done++
			failed++
		case KRun:
			running++
		}
	}
	parts := []string{fmt.Sprintf("%d of %d done", done, len(nodes))}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	return strings.Join(parts, " · ")
}

// follow returns the real nodes at the other end of the given edges, passing
// through any stand-ins.
func (p *pipeline) follow(from *pnode, keys []string, forward bool) []*pnode {
	var out []*pnode
	for _, k := range keys {
		n := p.byKey[k]
		for n.virtual {
			next := n.preds
			if forward {
				next = n.succs
			}
			if len(next) == 0 {
				break
			}
			n = p.byKey[next[0]]
		}
		if !n.virtual {
			out = append(out, n)
		}
	}
	return out
}

// step moves the cursor one node in a direction, or returns n if nothing is there.
func (p *pipeline) step(n *pnode, dir string) *pnode {
	closest := func(cands []*pnode) *pnode {
		var best *pnode
		for _, c := range cands {
			if best == nil || abs(c.row-n.row) < abs(best.row-n.row) {
				best = c
			}
		}
		return best
	}
	switch dir {
	case "up", "down":
		d := map[string]int{"up": -1, "down": 1}[dir]
		col := p.layers[n.layer]
		at := 0
		for i, c := range col {
			if c == n {
				at = i
			}
		}
		for i := at + d; i >= 0 && i < len(col); i += d {
			if !col[i].virtual {
				return col[i]
			}
		}
	case "left", "right":
		forward := dir == "right"
		keys := n.preds
		if forward {
			keys = n.succs
		}
		if best := closest(p.follow(n, keys, forward)); best != nil {
			return best
		}
		l := n.layer - 1
		if forward {
			l = n.layer + 1
		}
		if l >= 0 && l < len(p.layers) {
			var cands []*pnode
			for _, c := range p.layers[l] {
				if !c.virtual {
					cands = append(cands, c)
				}
			}
			if best := closest(cands); best != nil {
				return best
			}
		}
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// nodeOfJob is the node a run's job is drawn in.
func (p *pipeline) nodeOfJob(job int) *pnode {
	for _, n := range p.real() {
		for _, j := range n.jobs {
			if j == job {
				return n
			}
		}
	}
	return nil
}

// viewMode says how a pipeline is scrolled: to keep the selected job in view
// or to put it in the middle.
type viewMode int

const (
	viewKeep viewMode = iota
	viewCenter
)

// render cuts the part of the pipeline that fits w×h, scrolled as view says.
// A pipeline smaller than the area is centred in it.
func (p *pipeline) render(a *App, selected string, w, h int, offX, offY *int, view viewMode, now time.Time) (lines []string) {
	g := p.geometry()
	c := p.draw(g, selected, a.frame, now)
	if n := p.byKey[selected]; n != nil {
		x0, y0, x1, y1 := g.rect(n)
		if view == viewCenter {
			*offX, *offY = (x0+x1)/2-w/2, (y0+y1)/2-h/2
		}
		if x1 >= *offX+w {
			*offX = x1 - w + 2
		}
		if x0 < *offX {
			*offX = max(x0-1, 0)
		}
		if y1 >= *offY+h {
			*offY = y1 - h + 2
		}
		if y0 < *offY {
			*offY = max(y0-1, 0)
		}
	}
	*offX = max(min(*offX, g.w-w), 0)
	*offY = max(min(*offY, g.h-h), 0)
	padX, padY := max((w-g.w)/2, 0), max((h-g.h)/2, 0)
	for range padY {
		lines = append(lines, "")
	}
	for y := *offY; y < min(*offY+h, g.h); y++ {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", padX))
		for x := *offX; x < min(*offX+w, g.w); x++ {
			if cl := c.cells[y][x]; cl.r != 0 {
				b.WriteString(cl.st.Render(string(cl.r)))
			} else {
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// fits reports whether the pipeline at zoom fits in w×h.
func (p *pipeline) fits(zoom, w, h int) bool {
	saved := p.zoom
	p.zoom = zoom
	g := p.geometry()
	p.zoom = saved
	return g.w <= w && g.h <= h
}

// bestZoom is the most detailed zoom at which the pipeline fits in w×h. It
// stays at boxes when even those don't fit: one line per job is there to
// pick, not to land on.
func (p *pipeline) bestZoom(w, h int) int {
	for z := zoomLevels - 1; z > zoomBoxes; z-- {
		if p.fits(z, w, h) {
			return z
		}
	}
	return zoomBoxes
}
