package ui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// A slab is how a card floats over the screen: a raised surface without an
// outline, rounded by half blocks at its top and bottom, a rail in the colour
// of what it stands for at the left, a tile with its icon, and a shadow to
// the right and below. Its bottom edge is its clock: lit in its colour for
// the time it has left on screen. The cards before the newest tuck behind it
// as a deck, each a narrower, dimmer layer that still says its one line.
type slab struct {
	at       time.Time
	life     time.Duration
	color    lipgloss.Color // the colour of what it stands for, lit as it arrives
	fill     lipgloss.Color // its surface
	body     []string       // its lines, as wide as the card
	peek     string         // the card as one line, for when it sits behind another
	confetti bool
}

const (
	cardWidth = 64
	tileW     = 5 // the tile with the icon, at the left of a card
	maxBehind = 2 // how many cards show behind the newest
	arriveMs  = 700
)

// raisedBg is the surface of what floats over the screen: a step lighter
// than a panel on a dark theme, nearly white on a light one.
func raisedBg() lipgloss.Color {
	if currentTheme.Light {
		return lipgloss.Color(mix(string(cPanel), "#ffffff", 0.8))
	}
	return lipgloss.Color(mix(string(cPanel), string(cText), 0.07))
}

// shadowDepth is how much a shadow darkens what it falls on.
func shadowDepth() float64 {
	if currentTheme.Light {
		return 0.18
	}
	return 0.55
}

// arriving is the colour and surface of a card: it comes on screen lit white
// on a tint of its colour, and settles in its colour on the raised surface.
func arriving(at time.Time, c lipgloss.Color) (lipgloss.Color, lipgloss.Color) {
	fill := raisedBg()
	if p := glowProgress(at.UnixMilli(), arriveMs); p < 1 {
		e := ease(p)
		return lipgloss.Color(mix(string(cWhite), string(c), e)),
			lipgloss.Color(mix(mix(string(fill), string(c), 0.16), string(fill), e))
	}
	return c, fill
}

// rail is the edge of a card in its colour.
func rail(c lipgloss.Color) string {
	if ascii {
		return lipgloss.NewStyle().Background(c).Render(" ")
	}
	return fg(c).Render("▎")
}

// tile is the icon of a card: its glyph, dark on a rounded square of its
// colour, three lines high so it sits beside the title and the facts.
func tile(c lipgloss.Color, glyph string) []string {
	mid := lipgloss.NewStyle().Background(c).Foreground(cOnAccent).Bold(true).Render(fit("  "+glyph, tileW))
	if !pillsOn() {
		return []string{"", mid, ""}
	}
	return []string{fg(c).Render("▗▄▄▄▖"), mid, fg(c).Render("▝▀▀▀▘")}
}

// tileGlyph is the mark of a status on a tile: a check, a cross, the spinner.
func tileGlyph(k Kind, frame int) string {
	switch {
	case ascii:
	case k == KOK && nerd:
		return ""
	case k == KOK:
		return "✓"
	case k == KFail && nerd:
		return ""
	case k == KFail:
		return "✗"
	}
	return k.Glyph(frame)
}

// slabBody lays out the inside of a card w cells wide: the rail, the art
// (a tile or a medal) beside the first lines, and the text right of it.
func slabBody(c, fill lipgloss.Color, art []string, artW int, text []string, w int) []string {
	tw := w - artW - 5
	var out []string
	for i := range max(len(art), len(text)) {
		pic := strings.Repeat(" ", artW)
		if i < len(art) {
			pic = fit(art[i], artW)
		}
		t := ""
		if i < len(text) {
			t = text[i]
		}
		out = append(out, withBg(rail(c)+" "+pic+"  "+fit(t, tw)+" ", fill))
	}
	return out
}

// lineLR puts left and right on one line of w cells, right at its end.
func lineLR(left, right string, w int) string {
	if right == "" {
		return fit(left, w)
	}
	return fit(left, max(w-width(right)-1, 1)) + " " + right
}

// inset is a line set into a card, on a darker well with a rail in c: what
// broke, under the failed card's facts.
func inset(s string, c, fill lipgloss.Color, w int) string {
	well := lipgloss.Color(mix(string(fill), string(cBg), 0.6))
	return withBg(fit(rail(c)+" "+s, w), well)
}

// roundKey is a key as a round cap, a step lighter than what it sits on.
func roundKey(k string, under lipgloss.Color) string {
	bg := lipgloss.Color(mix(string(under), string(cText), 0.12))
	face := lipgloss.NewStyle().Foreground(cWhite).Background(bg).Bold(true)
	if !pillsOn() {
		return face.Render(" " + k + " ")
	}
	return fg(bg).Render(pillLeft) + face.Render(k) + fg(bg).Render(pillRight)
}

// drainCells is how many of n cells of a bottom edge are still lit: the
// share of its time a card has left on screen.
func drainCells(at time.Time, life time.Duration, n int) int {
	if animLevel < animSubtle || life <= 0 {
		return n
	}
	animated = true
	left := 1 - float64(time.Since(at))/float64(life)
	return min(max(int(math.Round(left*float64(n))), 0), n)
}

// edge draws the rounded top (or bottom) of a slab w cells wide on row y,
// on whatever lies under it.
func edge(cv *patch, x, y, w int, fill lipgloss.Color, top bool) {
	for i := range w {
		d := cv.at(x+i, y)
		if d == nil {
			continue
		}
		if !pillsOn() {
			*d = pcell{ch: " ", bg: string(fill)}
			continue
		}
		ch := "▄"
		switch {
		case top && i == 0:
			ch = "▗"
		case top && i == w-1:
			ch = "▖"
		case !top && i == 0:
			ch = "▝"
		case !top && i == w-1:
			ch = "▘"
		case !top:
			ch = "▀"
		}
		*d = pcell{ch: ch, fg: string(fill), bg: d.bg}
	}
}

// paintSlab lays the card s, w cells wide, with its top edge on row y: the
// edges, the body, the bottom edge lit for the time it has left, and the
// shadow it casts to the right and below.
func paintSlab(cv *patch, s *slab, x, y, w int) {
	edge(cv, x, y, w, s.fill, true)
	for j, l := range s.body {
		cv.text(x, y+1+j, l)
	}
	yb := y + len(s.body) + 1
	dark := shadowDepth()
	for j := y + 2; j <= yb; j++ {
		cv.shade(x+w, j, dark)
	}
	lit := drainCells(s.at, s.life, w-2)
	for i := range w {
		d := cv.at(x+i, yb)
		if d == nil {
			continue
		}
		on := i >= 1 && i <= lit
		if !pillsOn() {
			*d = pcell{ch: " ", bg: string(s.fill)}
			if on {
				*d = pcell{ch: "━", fg: string(s.color), bg: string(s.fill)}
			}
			continue
		}
		under := d.bg
		if i > 0 {
			under = mix(orBase(under), "#000000", dark)
		}
		ch, c := "▀", s.fill
		switch {
		case i == 0:
			ch = "▝"
		case i == w-1:
			ch = "▘"
		case on:
			c = lipgloss.Color(mix(string(s.color), string(s.fill), 0.25))
		}
		*d = pcell{ch: ch, fg: string(c), bg: under}
	}
}

// paintPeek lays a card that sits d places behind the newest: a narrower,
// dimmer slab whose first line shows above the one in front of it.
func paintPeek(cv *patch, s *slab, x, y, w, d int) {
	fill := lipgloss.Color(mix(string(raisedBg()), string(cBg), 0.3*float64(d)))
	c := lipgloss.Color(mix(string(s.color), string(cBg), 0.3*float64(d)))
	edge(cv, x, y, w, fill, true)
	for j := 1; j <= 2; j++ {
		for i := range w {
			if e := cv.at(x+i, y+j); e != nil {
				*e = pcell{ch: " ", bg: string(fill)}
			}
		}
		cv.text(x, y+j, rail(c))
	}
	cv.text(x+3, y+1, fit(s.peek, max(w-5, 1)))
	cv.fade(x+3, y+1, max(w-5, 1), fill, 0.28*float64(d))
}

// confettiGlyphs fall around a card for something you unlocked.
var confettiGlyphs = []string{"▪", "•", "▴", "◆", "▾", "✦"}

// paintConfetti throws confetti up from the top of a card w cells wide at
// (x, y) for its first two seconds.
func paintConfetti(cv *patch, at time.Time, x, y, w int) {
	since := time.Since(at)
	if animLevel < animSubtle || since > 2300*time.Millisecond {
		return
	}
	animated = true
	t := since.Seconds()
	seed := uint32(at.UnixNano())
	rnd := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed>>8) / float64(1<<24)
	}
	for i := range 30 {
		x0, vx, vy := rnd()*float64(w), (rnd()-0.5)*40, -(9 + rnd()*12)
		px := x + int(math.Round(x0+vx*t))
		py := y + 2 + int(math.Round(vy*t+15*t*t))
		g := confettiGlyphs[i%len(confettiGlyphs)]
		if ascii {
			g = "*"
		}
		cv.glyph(px, py, g, confettiColors[i%len(confettiColors)])
	}
}

// layDeck lays the deck of cards over the bottom right of the main area:
// the newest in full, up to two before it as layers behind it.
func layDeck(main string, deck []*slab, w, h int) string {
	if len(deck) == 0 || w < 20 {
		return main
	}
	front := deck[len(deck)-1]
	var behind []*slab
	for i := len(deck) - 2; i >= 0 && len(behind) < maxBehind; i-- {
		behind = append(behind, deck[i])
	}
	cw := width(front.body[0])
	x := max(w-cw-2, 0)
	yt := h - 2 - len(front.body)
	top := yt - 2*len(behind)
	if front.confetti {
		top -= 6
	}
	top = max(top, 0)
	rows := strings.Split(main, "\n")
	for len(rows) < h {
		rows = append(rows, "")
	}
	cv := newPatch(rows[top:h], w)
	for d := len(behind); d >= 1; d-- {
		paintPeek(cv, behind[d-1], x+2*d, yt-2*d-top, cw-4*d, d)
	}
	dx := 0
	if animLevel >= animFull {
		dx = int((1 - ease(progress01(front.at.UnixMilli(), 260))) * float64(cw+2))
	}
	paintSlab(cv, front, x+dx, yt-top, cw)
	if front.confetti {
		paintConfetti(cv, front.at, x, yt-top, cw)
	}
	copy(rows[top:h], cv.lines())
	return strings.Join(rows, "\n")
}
