package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A medal is drawn in half blocks: each cell holds two square pixels, the
// upper one as the colour of ▀ and the lower one as its background, so a
// medal n cells wide is n/2 lines high. Without good Unicode a cell is a
// space on the colour of its upper pixel.

// medalColors are the four colours of a medal: its rim, the lit and the
// shaded parts of the rim, and its face.
type medalColors struct{ rim, light, shade, face lipgloss.Color }

var tierMedals = map[tier]medalColors{
	tierBronze:   {"#c7834a", "#f0b47a", "#7e4a22", "#2e1f14"},
	tierSilver:   {"#a9b4c6", "#eef2f8", "#646e82", "#232a36"},
	tierGold:     {"#e5b94a", "#ffe69a", "#94701a", "#33280e"},
	tierPlatinum: {"#6fe0dc", "#d6fffd", "#2f8c89", "#0f2f2e"},
}

var (
	lockedMedal = medalColors{"#343c50", "#454e66", "#262c3c", "#161b27"}
	secretMedal = medalColors{"#6e52c8", "#a98bff", "#43308a", "#1e1638"}
)

// tierColor is the colour a tier is written in.
func tierColor(t tier) lipgloss.Color {
	if m, ok := tierMedals[t]; ok {
		return m.light
	}
	return cDim
}

// tierPill is a tier as a rounded pill in its colours.
func tierPill(t tier) string {
	m := tierMedals[t]
	return roundPill(t.String(), m.light, m.face)
}

// medalLook is how x is drawn for what you have of it: its glyph and the
// glyph's colour, and the medal's colours.
func (a *App) medalLook(x *achievement) (medalColors, string, lipgloss.Color) {
	t := a.ach.tierOf(x)
	switch {
	case t > tierNone:
		return tierMedals[t], x.glyphText(), x.area.color()
	case x.secret():
		return secretMedal, glyphOf(icons["question"]), lipgloss.Color("#d9ccff")
	}
	return lockedMedal, x.glyphText(), cDim
}

// medalCache keeps drawn medals: a trophy case draws dozens of them and
// styling every cell on each frame is slow.
var medalCache = map[string][]string{}

// medal draws a medal n cells wide (n even): an octagon with its upper left
// rim lit and its lower right shaded, a glint on gold and platinum, and the
// glyph in the middle of its face. under shows around it.
func medal(n int, m medalColors, shine bool, glyph string, glyphColor, under lipgloss.Color) []string {
	key := fmt.Sprint(n, m, shine, glyph, glyphColor, under, ascii, lipgloss.ColorProfile())
	if lines, ok := medalCache[key]; ok {
		return lines
	}
	h := float64(n) / 2
	edge := float64(n) * 0.74
	pixel := func(x, y int) lipgloss.Color {
		dx, dy := float64(x)+0.5-h, float64(y)+0.5-h
		ax, ay := max(dx, -dx), max(dy, -dy)
		switch {
		case ax+ay > edge:
			return under
		case shine && x == int(h)-2 && y == 1:
			return "#ffffff"
		case ax < h-1.4 && ay < h-1.4 && ax+ay < edge-1.8:
			return m.face
		case dx+dy < -h*0.4:
			return m.light
		case dx+dy > h*0.4:
			return m.shade
		}
		return m.rim
	}
	glyphRow, glyphCol := n/4, n/2-1
	lines := make([]string, n/2)
	for row := range lines {
		var b strings.Builder
		for x := 0; x < n; x++ {
			if row == glyphRow && x == glyphCol && glyph != "" {
				b.WriteString(lipgloss.NewStyle().Foreground(glyphColor).Background(m.face).Render(glyph + " "))
				x++
				continue
			}
			b.WriteString(halfBlock(pixel(x, 2*row), pixel(x, 2*row+1)))
		}
		lines[row] = b.String()
	}
	medalCache[key] = lines
	return lines
}

// halfBlock is a cell of two pixels, top over bottom.
func halfBlock(top, bottom lipgloss.Color) string {
	if ascii {
		return lipgloss.NewStyle().Background(top).Render(" ")
	}
	return lipgloss.NewStyle().Foreground(top).Background(bottom).Render("▀")
}

// pixelArt draws rows of pixels, one character each, in half blocks: each
// character names its colour in colors; any other character lets under
// show through.
func pixelArt(rows []string, colors map[rune]lipgloss.Color, under lipgloss.Color) []string {
	key := fmt.Sprint(rows, under, ascii, lipgloss.ColorProfile())
	if lines, ok := medalCache[key]; ok {
		return lines
	}
	at := func(x, y int) lipgloss.Color {
		if y >= len(rows) || x >= len(rows[y]) {
			return under
		}
		if c, ok := colors[rune(rows[y][x])]; ok {
			return c
		}
		return under
	}
	w := 0
	for _, r := range rows {
		w = max(w, len(r))
	}
	var lines []string
	for y := 0; y < len(rows); y += 2 {
		var b strings.Builder
		for x := 0; x < w; x++ {
			b.WriteString(halfBlock(at(x, y), at(x, y+1)))
		}
		lines = append(lines, b.String())
	}
	medalCache[key] = lines
	return lines
}

// trophyRows are the pixels of the trophy at the top of the Achievements.
var trophyRows = []string{
	"..hhhhhhgd..",
	"hhhhgggggddd",
	"h.hggwgggd.d",
	"h.hggggggd.d",
	".hhggggggdd.",
	"..hggggggd..",
	"...hggggd...",
	"....hggd....",
	".....gd.....",
	"....hggd....",
	"...bbbbbb...",
	"...kkkkkk...",
}

var trophyColors = map[rune]lipgloss.Color{
	'h': "#ffe69a", 'g': "#e5b94a", 'd': "#94701a", 'w': "#ffffff", 'b': "#6b4a2b", 'k': "#4a3220",
}

// trophy is the trophy drawn on under: 12 cells wide and 6 lines high.
func trophy(under lipgloss.Color) []string { return pixelArt(trophyRows, trophyColors, under) }
