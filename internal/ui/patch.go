package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// A patch is a piece of the screen taken apart into cells, so that what
// floats over the screen (a card, its rounded edges, its shadow) can be
// drawn with what lies under it: a half block takes the colour below it, a
// shadow darkens the cells it falls on and keeps their text.
type patch struct {
	w    int
	rows [][]pcell
}

// pcell is one cell of the screen: what it shows and how.
type pcell struct {
	ch     string // "" for the second half of a wide character
	fg, bg string // "#rrggbb"; "" is the screen's own
	bold   bool
	faint  bool
	italic bool
	under  bool
}

// newPatch takes lines of the screen apart, each into w cells.
func newPatch(lines []string, w int) *patch {
	cv := &patch{w: w}
	for _, l := range lines {
		cv.rows = append(cv.rows, parseCells(l, w))
	}
	return cv
}

// at is the cell at (x, y), or nil off the patch.
func (cv *patch) at(x, y int) *pcell {
	if y < 0 || y >= len(cv.rows) || x < 0 || x >= cv.w {
		return nil
	}
	return &cv.rows[y][x]
}

// lines puts the patch back together as lines of the screen.
func (cv *patch) lines() []string {
	out := make([]string, len(cv.rows))
	for i, r := range cv.rows {
		out[i] = renderCells(r)
	}
	return out
}

// text writes s (which may hold ANSI codes) from (x, y). Where s has no
// background of its own the cell keeps the one it had.
func (cv *patch) text(x, y int, s string) {
	for i, c := range parseCells(s, width(s)) {
		if d := cv.at(x+i, y); d != nil {
			if c.bg == "" {
				c.bg = d.bg
			}
			*d = c
		}
	}
}

// glyph puts ch in colour fg on the cell at (x, y), on the background the
// cell already has.
func (cv *patch) glyph(x, y int, ch string, fgc lipgloss.Color) {
	if d := cv.at(x, y); d != nil {
		*d = pcell{ch: ch, fg: string(fgc), bg: d.bg}
	}
}

// shade darkens the cell at (x, y) by t, text and all, as a shadow does.
func (cv *patch) shade(x, y int, t float64) {
	if d := cv.at(x, y); d != nil {
		d.fg = mix(orText(d.fg), "#000000", t)
		d.bg = mix(orBase(d.bg), "#000000", t)
	}
}

// fade brings the text of the cells from x to x+n on row y towards c by t.
func (cv *patch) fade(x, y, n int, c lipgloss.Color, t float64) {
	for i := range n {
		if d := cv.at(x+i, y); d != nil {
			d.fg = mix(orText(d.fg), string(c), t)
		}
	}
}

func orText(c string) string {
	if c == "" {
		return string(cText)
	}
	return c
}

func orBase(c string) string {
	if c == "" {
		return string(cBg)
	}
	return c
}

// parseCells takes a line apart into w cells; what is missing is blank.
func parseCells(s string, w int) []pcell {
	out := make([]pcell, 0, w)
	var st pcell
	var state byte
	for len(s) > 0 && len(out) < w {
		seq, n, read, next := ansi.DecodeSequence(s, state, nil)
		state = next
		s = s[read:]
		switch {
		case strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m"):
			st = applySGR(st, seq[2:len(seq)-1])
		case n > 0:
			c := st
			c.ch = seq
			out = append(out, c)
			for i := 1; i < n && len(out) < w; i++ {
				c.ch = ""
				out = append(out, c)
			}
		}
	}
	for len(out) < w {
		out = append(out, pcell{ch: " "})
	}
	return out
}

// applySGR changes the style st by the parameters of an SGR sequence.
func applySGR(st pcell, params string) pcell {
	ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	if len(ps) == 0 {
		ps = []string{"0"}
	}
	num := func(i int) int {
		if i >= len(ps) {
			return 0
		}
		n, _ := strconv.Atoi(ps[i])
		return n
	}
	for i := 0; i < len(ps); i++ {
		switch p := num(i); {
		case p == 0:
			st = pcell{}
		case p == 1:
			st.bold = true
		case p == 2:
			st.faint = true
		case p == 3:
			st.italic = true
		case p == 4:
			st.under = true
		case p == 22:
			st.bold, st.faint = false, false
		case p == 23:
			st.italic = false
		case p == 24:
			st.under = false
		case p == 39:
			st.fg = ""
		case p == 49:
			st.bg = ""
		case p >= 30 && p <= 37:
			st.fg = ansi16(p - 30)
		case p >= 90 && p <= 97:
			st.fg = ansi16(p - 90 + 8)
		case p >= 40 && p <= 47:
			st.bg = ansi16(p - 40)
		case p >= 100 && p <= 107:
			st.bg = ansi16(p - 100 + 8)
		case p == 38 || p == 48:
			var c string
			switch num(i + 1) {
			case 2:
				c = fmt.Sprintf("#%02x%02x%02x", num(i+2)&255, num(i+3)&255, num(i+4)&255)
				i += 4
			case 5:
				c = ansi256(num(i + 2))
				i += 2
			}
			if p == 38 {
				st.fg = c
			} else {
				st.bg = c
			}
		}
	}
	return st
}

// ansi16 is one of the sixteen basic colours as xterm draws it.
func ansi16(n int) string {
	return [...]string{"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
		"#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff"}[n&15]
}

// ansi256 is one of the 256 colours as xterm draws it.
func ansi256(n int) string {
	switch {
	case n < 16:
		return ansi16(n)
	case n < 232:
		n -= 16
		v := func(i int) int {
			if i == 0 {
				return 0
			}
			return 55 + i*40
		}
		return fmt.Sprintf("#%02x%02x%02x", v(n/36), v(n/6%6), v(n%6))
	}
	g := 8 + (n-232)*10
	return fmt.Sprintf("#%02x%02x%02x", g, g, g)
}

// renderCells puts cells back together, one style for each run of cells
// that look the same.
func renderCells(cs []pcell) string {
	var b strings.Builder
	var run strings.Builder
	var cur pcell
	flush := func() {
		if run.Len() == 0 {
			return
		}
		st := lipgloss.NewStyle().Bold(cur.bold).Faint(cur.faint).Italic(cur.italic).Underline(cur.under)
		if cur.fg != "" {
			st = st.Foreground(lipgloss.Color(cur.fg))
		}
		if cur.bg != "" {
			st = st.Background(lipgloss.Color(cur.bg))
		}
		b.WriteString(st.Render(run.String()))
		run.Reset()
	}
	for _, c := range cs {
		if c.ch == "" {
			continue
		}
		key := c
		key.ch = ""
		if key != cur {
			flush()
			cur = key
		}
		run.WriteString(c.ch)
	}
	flush()
	return b.String()
}

// nearColor says whether two colours are the same but for rounding.
func nearColor(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	d := func(x, y int) bool { return x-y <= 2 && y-x <= 2 }
	return d(ar, br) && d(ag, bg) && d(ab, bb)
}
