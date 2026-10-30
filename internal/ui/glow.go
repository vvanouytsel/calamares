package ui

import (
	"hash/fnv"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Glows: moving around is always drawn in its final state at once; what
// moves is only light laid over it. The view you switch to lights up and a
// sheen sweeps across it, the cursor bar flashes as it lands on a row. While
// a glow plays the screen is redrawn at 120 frames a second (a burst),
// and slows down again as soon as it has settled.

// glowing is set while a glow is drawn, so the burst keeps going.
var glowing bool

// glowMarks remembers, per thing that can change (a strip, the header, a
// table), the value it showed and when it last changed.
var glowMarks = map[string]struct {
	v  int
	at int64
}{}

// changedAt notes v for key and returns when it last changed (0 when it never
// did since it was first seen, so nothing glows on the first draw).
func changedAt(key string, v int) int64 {
	c, seen := glowMarks[key]
	if !seen {
		glowMarks[key] = struct {
			v  int
			at int64
		}{v, 0}
		return 0
	}
	if c.v != v {
		c.v, c.at = v, nowMs()
		glowMarks[key] = c
	}
	return c.at
}

// changedAtS is changedAt for a value that is a string.
func changedAtS(key, s string) int64 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return changedAt(key, int(h.Sum32()))
}

// glowProgress is how far a glow of ms that began at start has come; it is 1
// when there is none, and notes that a glow is on display while it plays.
func glowProgress(start int64, ms int64) float64 {
	if start == 0 || animLevel < animFull {
		return 1
	}
	p := float64(sinceMs(start)) / float64(ms)
	if p >= 1 {
		return 1
	}
	glowing, animated, smooth = true, true, true
	return math.Max(p, 0)
}

// glowUnit is a piece of a lit segment and the style of its text.
type glowUnit struct {
	text string
	st   lipgloss.Style
}

// glowSeg draws units on a background that starts lit (towards hot) and
// settles on base, with a band of light sweeping once from left to right.
func glowSeg(units []glowUnit, base, hot lipgloss.Color, p float64) string {
	type cell struct {
		s  string
		st lipgloss.Style
	}
	var cells []cell
	for _, u := range units {
		rs := []rune(u.text)
		for i := 0; i < len(rs); i++ {
			s := string(rs[i])
			// A Nerd Font glyph is drawn with the space after it, so the
			// next cell's background can't paint over it.
			if rs[i] >= 0xe000 && rs[i] <= 0xf8ff && i+1 < len(rs) {
				s += string(rs[i+1])
				i++
			}
			cells = append(cells, cell{s, u.st})
		}
	}
	n := 0
	for _, c := range cells {
		n += utf8.RuneCountInString(c.s)
	}
	pos := ease(p)*float64(n+8) - 4
	var b strings.Builder
	x := 0
	for _, c := range cells {
		k := math.Max(0, 1-math.Abs(float64(x)-pos)/3.5)
		t := math.Min(0.4*(1-ease(p))+0.55*k*k, 0.85)
		bg := lipgloss.Color(mix(string(base), string(hot), t))
		b.WriteString(c.st.Background(bg).Render(c.s))
		x += utf8.RuneCountInString(c.s)
	}
	return b.String()
}

// glowBracket is a bracket of the view on display: white as it lands,
// settling on the accent.
func glowBracket(s string, p float64) string {
	c := cCyan
	if p < 1 {
		c = lipgloss.Color(mix(string(cWhite), string(cCyan), ease(p)))
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

// stripGlowMs and cursorGlowMs are how long the glows play.
const (
	stripGlowMs  = 520
	cursorGlowMs = 220
)

// burstMsg is a frame of a burst.
type burstMsg struct{}

const burstEvery = 8 * time.Millisecond // 120 frames a second

func burstTick() tea.Cmd {
	return tea.Tick(burstEvery, func(time.Time) tea.Msg { return burstMsg{} })
}

// kick starts a burst after a key, when none is running: the
// first frames of a glow then follow each other closely instead of waiting
// for the slow beat of the spinners.
func (a *App) kick() tea.Cmd {
	if animLevel < animFull {
		return nil
	}
	a.burstUntil = time.Now().Add(40 * time.Millisecond)
	if a.bursting {
		return nil
	}
	a.bursting = true
	return burstTick()
}

// onBurst keeps the burst going while a glow plays.
func (a *App) onBurst() tea.Cmd {
	if a.glowing || time.Now().Before(a.burstUntil) {
		return burstTick()
	}
	a.bursting = false
	return nil
}
