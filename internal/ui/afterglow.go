package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// A list of what runs now is often short. The room under it tells what
// just happened instead of staying dark: the runs that finished last, and
// how busy the last 24 hours were, hour by hour.

// afterglowMin is the room a short list must leave under it for the two
// sections to show.
const afterglowMin = 12

// sectionHead is the title of a section of a list: the title in white, a
// hairline that fades out and what it holds in grey at the right.
func sectionHead(title, about string, w int) string {
	left := " " + sWhite.Bold(true).Render(title) + " "
	right := ""
	if about != "" {
		right = " " + sDim.Render(about) + " "
	}
	return left + hairlineOn(max(w-width(left)-width(right), 0), string(cBg)) + right
}

// hairlineOn is a hairline of n cells that fades into bg.
func hairlineOn(n int, bg string) string {
	c := mix(string(cFrame), string(cText), 0.25)
	var b strings.Builder
	for i := range n {
		b.WriteString(fg(lipgloss.Color(mix(c, bg, float64(i)/float64(max(n, 1))))).Render("─"))
	}
	return b.String()
}

// afterglow is what goes under a short list of running runs, h lines of w
// cells: the runs that finished last and the runs of the last 24 hours.
func (v *activityView) afterglow(a *App, all []repoRun, now time.Time, w, h int) []string {
	var done []repoRun
	for _, x := range all {
		if x.run.Status == "completed" && v.mineOK(a, x) && now.Sub(x.run.UpdatedAt) < 24*time.Hour {
			done = append(done, x)
		}
	}
	slices.SortFunc(done, func(p, q repoRun) int { return q.run.UpdatedAt.Compare(p.run.UpdatedAt) })
	var out []string
	if len(done) > 0 {
		out = append(out, "", sectionHead("Just finished", "the runs that ended last, newest first", w))
		for _, x := range done[:min(len(done), max(h-9, 2), 5)] {
			out = append(out, finishedLine(a, x, now, w))
		}
	}
	if day := v.today(a, all, now, w); len(day) > 0 && len(out)+len(day) <= h {
		out = append(out, day...)
	}
	return out
}

// finishedLine is a run that finished, as a quiet line: how it went in its
// colour, where, which run, what for and how long it took.
func finishedLine(a *App, x repoRun, now time.Time, w int) string {
	r := x.run
	k := StatusKind(r.Status, r.Conclusion)
	quiet := k.RowColor() == cDim
	st := sText
	if quiet {
		st = sDim
	}
	status := fit(fg(k.Color()).Render(k.Glyph(a.frame)+" "+k.Label()), 12)
	where := fit(icoIn("repo", cGold)+st.Render(a.short(x.repo)), 18) + fit(st.Render(r.Name), 14) + fit(sGold.Render(fmt.Sprintf("#%d", r.RunNumber)), 7)
	took := fg(cSteel).Render(dur(r.Duration(now)))
	rest := max(w-2-width(status)-width(where)-width(took)-1, 6)
	return " " + status + where + fit(st.Render(r.DisplayTitle), rest) + " " + took
}

// today draws the runs of the last 24 hours as bars three lines high, one
// an hour, the hours with a failure in red and the hour we are in lit.
func (v *activityView) today(a *App, all []repoRun, now time.Time, w int) []string {
	var count, failed [24]int
	total, fails := 0, 0
	for _, x := range all {
		ago := int(now.Sub(x.run.CreatedAt) / time.Hour)
		if ago < 0 || ago >= 24 || !v.mineOK(a, x) {
			continue
		}
		count[23-ago]++
		total++
		if StatusKind(x.run.Status, x.run.Conclusion) == KFail {
			failed[23-ago]++
			fails++
		}
	}
	if total == 0 || ascii {
		return nil
	}
	step := 2
	if w >= 24*3+6 {
		step = 3
	}
	most := slices.Max(count[:])
	eighths := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	rows := make([]strings.Builder, 3)
	for i, n := range count {
		h := 0
		if n > 0 {
			h = max(n*24/most, 2)
		}
		c := lipgloss.Color(mix(string(cCyan), string(cBg), 0.3))
		switch {
		case failed[i] > 0:
			c = cRed
		case i == 23:
			c = breathe(cCyan)
		}
		for r := range 3 {
			level := min(max(h-(2-r)*8, 0), 8)
			g := eighths[level]
			if n == 0 && r == 2 {
				g, c = "▁", cFrame
			}
			rows[r].WriteString(fg(c).Render(strings.Repeat(g, step-1)) + " ")
		}
	}
	about := fmt.Sprintf("%d %s in the last 24 hours", total, plural("run", total))
	if fails > 0 {
		about += fmt.Sprintf(", %d failed", fails)
	}
	span := 24 * step
	axis := sDim.Render(lineLR("24h ago", "now ", span))
	if mid := "12h"; span > 30 {
		axis = sDim.Render(fit("24h ago", span/2-1) + fit(mid, span-span/2-3) + "now")
	}
	return []string{"", sectionHead("Today", about, w), " " + rows[0].String(), " " + rows[1].String(), " " + rows[2].String(), " " + axis}
}
