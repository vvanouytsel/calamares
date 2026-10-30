package ui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// fit truncates or pads s (which may contain ANSI codes) to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-sw)
}

// clip truncates s to at most w cells without padding.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s
}

func width(s string) int { return ansi.StringWidth(s) }

// lines splits a block into exactly h lines of exactly w cells.
func block(s string, w, h int) []string {
	var src []string
	if s != "" {
		src = strings.Split(s, "\n")
	}
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(src) {
			out[i] = fit(src[i], w)
		} else {
			out[i] = strings.Repeat(" ", max(w, 0))
		}
	}
	return out
}

// corners are the corners of a frame: rounded, or square for terminals that
// only do plain characters well.
func corners() (tl, tr, bl, br string) {
	if ascii {
		return "┌", "┐", "└", "┘"
	}
	return "╭", "╮", "╰", "╯"
}

// box draws a pane of w×h cells as a layer, not a box: its title at the
// left with a hairline that fades out beside it, in the colour of the
// border (the accent while it has the focus), its body under it, and a
// blank line to end it. Only what floats over the screen keeps a frame.
func box(title, body string, w, h int, border lipgloss.Color) string {
	if w < 4 || h < 2 {
		return ""
	}
	inner := w - 2
	line := border
	if border == cFrame {
		line = lipgloss.Color(mix(string(cFrame), string(cText), 0.25))
	}
	top := hairlineTo(w, "─", line, cBg)
	if title != "" {
		title = clip(title, inner-4)
		top = " " + title + " " + hairlineTo(max(w-width(title)-2, 0), "─", line, cBg)
	}
	var b strings.Builder
	b.WriteString(top)
	for _, l := range block(body, inner, h-2) {
		b.WriteString("\n " + l + " ")
	}
	b.WriteString("\n" + strings.Repeat(" ", w))
	return b.String()
}

// hairlineTo is n cells of stroke in colour c that fade into bg.
func hairlineTo(n int, stroke string, c, bg lipgloss.Color) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(fg(lipgloss.Color(mix(string(c), string(bg), float64(i)/float64(max(n, 1))))).Render(stroke))
	}
	return b.String()
}

// frame draws a frame of w×h cells with its title at the left of the top
// border, for what floats over the screen: a dialog, the palette.
func frame(title, body string, w, h int, border lipgloss.Color) string {
	if w < 4 || h < 2 {
		return ""
	}
	bs := fg(border)
	tl, tr, bl, br := corners()
	inner := w - 2
	top := bs.Render(tl + strings.Repeat("─", inner) + tr)
	if title != "" {
		title = clip(title, inner-4)
		top = bs.Render(tl+"─") + " " + title + " " + bs.Render(strings.Repeat("─", max(inner-width(title)-3, 0))+tr)
	}
	var b strings.Builder
	b.WriteString(top)
	side := bs.Render("│")
	for _, l := range block(body, inner, h-2) {
		b.WriteString("\n" + side + l + side)
	}
	b.WriteString("\n" + bs.Render(bl+strings.Repeat("─", inner)+br))
	return b.String()
}

// boxLeft is box; every frame has its title at the left now.
func boxLeft(title, body string, w, h int, border lipgloss.Color) string {
	return box(title, body, w, h, border)
}

// hjoin places blocks side by side; each block must already be rectangular.
func hjoin(parts ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// title renders a frame's title: what it holds in bold, which part of it,
// and how many as a small pill.
func title(name, scope string, count int, extra string) string {
	s := sWhite.Bold(true).Render(name)
	if scope != "" {
		s += sDim.Render(" · ") + sHint.Render(scope)
	}
	if count >= 0 {
		s += " " + pill(strconv.Itoa(count), cCrumbBg, cText)
	}
	if extra != "" {
		s += " " + sAmber.Render(extra)
	}
	return s
}

// overlay dims base and draws fg centred on top of it.
func overlay(base, front string, w, h int) string {
	bl := strings.Split(base, "\n")
	for len(bl) < h {
		bl = append(bl, "")
	}
	fl := strings.Split(front, "\n")
	fw := 0
	for _, l := range fl {
		fw = max(fw, width(l))
	}
	x := max((w-fw)/2, 0)
	y := max((h-len(fl))/2, 0)
	if lipgloss.ColorProfile() == termenv.Ascii {
		for i, l := range bl {
			bl[i] = sDim.Faint(true).Render(fit(ansi.Strip(l), w))
		}
		return splice(bl, fl, x, y)
	}
	// The screen sinks into the dark, its colours kept, and the dialog
	// casts a shadow on it.
	p := newPatch(bl[:h], w)
	for r := range p.rows {
		for i := range p.rows[r] {
			c := &p.rows[r][i]
			c.fg = mix(orText(c.fg), string(cBg), 0.62)
			c.bg = mix(orBase(c.bg), "#000000", 0.3)
			c.bold = false
		}
	}
	for j := y + 1; j <= y+len(fl); j++ {
		p.shade(x+fw, j, shadowDepth())
		p.shade(x+fw+1, j, shadowDepth())
	}
	for i := x + 2; i <= x+fw+1; i++ {
		p.shade(i, y+len(fl), shadowDepth())
	}
	return splice(p.lines(), fl, x, y)
}

// splice writes the lines of front into base at (x, y) without dimming base.
func splice(base, front []string, x, y int) string {
	out := append([]string(nil), base...)
	for i, l := range front {
		row := y + i
		if row < 0 || row >= len(out) {
			continue
		}
		b := out[row]
		lw := width(l)
		left := ansi.Truncate(b, x, "")
		if pad := x - width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(b, x+lw, "")
		out[row] = left + l + right
	}
	return strings.Join(out, "\n")
}

// thousands formats 4812 as "4,812".
func thousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
