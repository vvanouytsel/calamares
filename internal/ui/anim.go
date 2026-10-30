package ui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Animation is motion that says something: what is running breathes, a
// dialog opens, a row whose run just finished glows once. Everything that
// moves sets animated while it is drawn, so the screen is redrawn faster
// only while something is in flight, and sits still otherwise.

// The levels of the animations setting.
const (
	animOff    = 0 // nothing moves but the spinners
	animSubtle = 1 // the spinners and the pulse of what is running
	animFull   = 2
)

// smooth is set while something is drawn that needs quick redraws (a pulse,
// a shimmer, an opening dialog), not only the slow turn of a spinner.
var smooth bool

// animLevel is how much moves; see Config.Animations.
var animLevel = animFull

// parseAnim reads the animations setting; anything unknown means full.
func parseAnim(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none", "false", "no":
		return animOff
	case "subtle", "low":
		return animSubtle
	}
	return animFull
}

func animName(level int) string {
	switch level {
	case animOff:
		return "off"
	case animSubtle:
		return "subtle"
	}
	return "full"
}

// nowMs is the clock the animations run on.
func nowMs() int64 { return time.Now().UnixMilli() }

// ease slows t (0..1) towards its end.
func ease(t float64) float64 {
	t = math.Min(math.Max(t, 0), 1)
	return 1 - math.Pow(1-t, 3)
}

// pulse breathes between 0 and 1 over period.
func pulse(period time.Duration) float64 {
	animated, smooth = true, true
	phase := float64(nowMs()%period.Milliseconds()) / float64(period.Milliseconds())
	return (1 - math.Cos(phase*2*math.Pi)) / 2
}

// breathe is the background of a running chip: its tint, brightening and
// dimming slowly.
func breathe(base lipgloss.Color) lipgloss.Color {
	if animLevel < animSubtle {
		return base
	}
	return lipgloss.Color(mix(string(base), string(cAmber), 0.22*pulse(1800*time.Millisecond)))
}

// age of a moment in milliseconds.
func sinceMs(t int64) int64 { return nowMs() - t }

// progress is how far an animation of length ms that began at start has come
// (0..1), noting that something is moving while it has not finished.
func progress01(start int64, ms int64) float64 {
	p := float64(sinceMs(start)) / float64(ms)
	if p < 1 {
		animated, smooth = true, true
	}
	return math.Min(math.Max(p, 0), 1)
}

// shimmerBar is a row of w cells of a skeleton: a lighter band sweeps
// across it.
func shimmerBar(w int, offset float64) string {
	if animLevel < animFull {
		return fg(cMeterOff).Render(strings.Repeat("▬", w))
	}
	animated, smooth = true, true
	pos := math.Mod(float64(nowMs())/900+offset, 1.4)*float64(w+12) - 6
	var b strings.Builder
	for i := 0; i < w; i++ {
		d := math.Abs(float64(i) - pos)
		t := math.Max(0, 1-d/6)
		b.WriteString(fg(lipgloss.Color(mix(string(cMeterOff), string(cGrey), 0.55*t*t))).Render("▬"))
	}
	return b.String()
}

// skeleton is the rows shown in place of "Loading…": grey bars with a light
// sweeping over them.
func skeleton(w, h int) []string {
	widths := []float64{0.8, 0.6, 0.7, 0.5, 0.65}
	var out []string
	for i := 0; i < min(h, len(widths)); i++ {
		out = append(out, " "+shimmerBar(max(int(float64(w-2)*widths[i]), 4), float64(i)*0.12))
	}
	return out
}

// logoBorn is when Calamares started, for the name to light up letter by
// letter as it opens.
var logoBorn = nowMs()

// logoShimmer draws the name in the header in a gradient from the accent to
// violet, and lights it with a sweep of white for a moment every 20 seconds.
// As Calamares starts, the letters light up one after the other, each with
// a flash of white before it settles in its colour.
func logoShimmer(name string) string {
	rs := []rune(name)
	pos := -100.0
	since := sinceMs(logoBorn)
	if animLevel >= animFull {
		const cycle, sweep = 20000, 1400
		if t := nowMs() % cycle; t < sweep && since > 2000 {
			animated, smooth = true, true
			pos = float64(t)/sweep*float64(len(rs)+8) - 4
		}
	}
	var b strings.Builder
	for i, r := range rs {
		base := mix(string(cCyan), string(cViolet), float64(i)/float64(max(len(rs)-1, 1)))
		k := math.Max(0, 1-math.Abs(float64(i)-pos)/3)
		c := mix(base, string(cWhite), k)
		if lit := since - 300 - int64(i)*70; animLevel >= animFull && lit < 500 {
			animated, smooth = true, true
			switch {
			case lit < 0:
				c = string(cFrame)
			default:
				c = mix(string(cWhite), base, float64(lit)/500)
			}
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c)).Render(string(r)))
	}
	return b.String()
}

// openEase shows a dialog growing out of its middle: the first frames cut
// the box down to a few lines, keeping its top and bottom edge.
func openEase(box string, start int64) string {
	if animLevel < animFull {
		return box
	}
	p := progress01(start, 180)
	if p >= 1 {
		return box
	}
	lines := strings.Split(box, "\n")
	n := len(lines)
	if n < 6 {
		return box
	}
	k := max(int(ease(p)*float64(n)), 4)
	if k >= n {
		return box
	}
	mid := lines[1 : n-1]
	keep := k - 2
	from := (len(mid) - keep) / 2
	out := []string{lines[0]}
	out = append(out, mid[from:from+keep]...)
	return strings.Join(append(out, lines[n-1]), "\n")
}

// flash is a row whose status just changed: it glows in the colour of the
// new status and settles.
type flash struct {
	at int64
	c  lipgloss.Color
}

const flashMs = 1600

// noteKinds remembers each row's status, and starts a flash on a row that
// has just passed or failed.
func (t *Table) noteKinds(rows []Row) {
	next := make(map[string]Kind, len(rows))
	for _, r := range rows {
		next[r.Key] = r.Kind
		if prev, ok := t.kinds[r.Key]; ok && prev != r.Kind && (r.Kind == KOK || r.Kind == KFail) && animLevel >= animFull {
			if t.flashes == nil {
				t.flashes = map[string]flash{}
			}
			t.flashes[r.Key] = flash{nowMs(), r.Kind.Color()}
		}
	}
	t.kinds = next
}

// glow lays a fading tint of a changed row's colour under its line.
func (t *Table) glow(r Row, line string) string {
	f, ok := t.flashes[r.Key]
	if !ok {
		return line
	}
	p := progress01(f.at, flashMs)
	if p >= 1 {
		delete(t.flashes, r.Key)
		return line
	}
	return withBg(line, lipgloss.Color(mix(string(cBg), string(f.c), 0.3*(1-ease(p)))))
}
