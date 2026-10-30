package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// The look of a list: a status is its glyph and its word in its colour, with
// no block behind it, so a long list stays crisp; every column carries the
// icon of what it holds in that thing's colour, and the text beside it stays
// light. A quiet row (cancelled, skipped, already read) is dim from end to end.

// statusCell is a status in a row: the glyph and the word in its colour.
func statusCell(k Kind, frame int, label string) Cell {
	if label == "" {
		label = k.Label()
	}
	text := k.Glyph(frame) + " " + label
	c := k.Color()
	if k == KRun {
		c = lipgloss.Color(mix(string(cAmber), string(cWhite), 0.25*breatheLevel()))
	}
	return Cell{Text: text, Styled: fg(c).Render(text)}
}

// breatheLevel is how far the pulse of what is running has come (0..1).
func breatheLevel() float64 {
	if animLevel < animSubtle {
		return 0
	}
	return pulse(1800e6)
}

// quietOr is st, or dim for a quiet row.
func quietOr(st lipgloss.Style, quiet bool) lipgloss.Style {
	if quiet {
		return sDim
	}
	return st
}

// iconText is a cell with the icon for name in its colour, then text.
func iconText(name, text string, quiet bool) Cell {
	c := iconColor(name)
	if quiet {
		c = cDim
	}
	return iconCell(name, text, quietOr(sText, quiet), c)
}

// numberCell is "#129" in gold.
func numberCell(n int, quiet bool) Cell {
	t := fmt.Sprintf("#%d", n)
	return Cell{Text: t, Styled: quietOr(sGold, quiet).Render(t)}
}

// titleCell is what you read first in a row: white.
func titleCell(text string, quiet bool) Cell {
	return Cell{Text: text, Styled: quietOr(sWhite, quiet).Render(text)}
}

// steelCell is a quiet fact, such as an age or a duration.
func steelCell(text string, quiet bool) Cell {
	return Cell{Text: text, Styled: quietOr(fg(cSteel), quiet).Render(text)}
}

// pillCaps are the rounded ends of a pill, from a Nerd Font.
const pillLeft, pillRight = "", ""

// pill-shaped chips need a Nerd Font for their rounded ends.
func pillsOn() bool { return nerd && !ascii }

// roundPill draws text on bg in fg, with rounded ends when the font has them.
func roundPill(text string, fgc, bg lipgloss.Color) string {
	body := lipgloss.NewStyle().Foreground(fgc).Background(bg).Render(text)
	if !pillsOn() {
		return lipgloss.NewStyle().Background(bg).Render(" ") + body + lipgloss.NewStyle().Background(bg).Render(" ")
	}
	cap := fg(bg)
	return cap.Render(pillLeft) + body + cap.Render(pillRight)
}
