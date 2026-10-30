package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// topBarHeight is the number of lines the header takes: two lines on a band
// that fades from the accent (the logo and the name with what is going on at
// the right, the logo and the places) and a line of the page under them,
// into which the place on display rises as a tab.
const topBarHeight = 3

// logoWidth is the cells the pixel-art logo takes.
const logoWidth = 6

// wordmark is the name in gold, spaced out.
func wordmark() string {
	if ascii {
		return logoShimmer("CALAMARES")
	}
	return logoShimmer("C A L A M A R E S")
}

// logoLine is line i of the pixel-art logo; plain terminals get blanks.
func logoLine(i int) string {
	if ascii {
		return strings.Repeat(" ", logoWidth)
	}
	return logoTop[i]
}

// renderTopBar draws the header: the logo, the name and what else is going
// on at the right on the first line, the logo, the places and where you are
// on the second, both on a band of their own, and a rule under them.
func (a *App) renderTopBar() string {
	name := " " + logoLine(0) + "  " + wordmark()

	// The data's age and the live state always show; the rest is left out
	// from the end when there is no room.
	must := capsule(joinDot(a.freshness(), a.liveState()), lipgloss.Color(mix(string(cBand), string(cText), 0.08)))
	var extra []string
	if pill := a.statusPill(); pill != "" {
		extra = append(extra, pill)
	}
	if a.cfg.AchievementsOn() {
		extra = append(extra, roundPill(icon("trophy")+a.levelText(), cGold, lipgloss.Color(mix(string(cBg), string(cGold), 0.18))))
	}
	if n := a.unreadErrors; n == 1 {
		extra = append(extra, pill("! 1 error", cRed, cOnAccent))
	} else if n > 1 {
		extra = append(extra, pill(fmt.Sprintf("! %d errors", n), cRed, cOnAccent))
	}
	if n := a.runningWatches(); n > 0 {
		extra = append(extra, icoIn("watching", heartbeat())+sAmber.Render(fmt.Sprintf("watching %d", n)))
	}
	extra = append(extra, a.budget())
	if a.opts.ReadOnly {
		extra = append(extra, pill("read-only", cAmber, cOnAccent))
	}
	if a.cfg.RunFilter != "" && !a.filterOff {
		extra = append(extra, sText.Render("saved filter on"))
	}
	if a.client.Host != "github.com" {
		extra = append(extra, sDim.Render(a.client.Host))
	}
	if a.opts.Version != "" {
		extra = append(extra, sDim.Render(a.opts.Version))
	}
	r := must
	for _, part := range extra {
		if next := joinDot(part, r); width(name)+width(next)+3 <= a.w {
			r = next
		}
	}
	r += " "
	first := name + strings.Repeat(" ", max(a.w-width(name)-width(r), 0)) + r

	var second string
	for _, short := range []struct {
		tabs  bool
		crumb int
	}{{false, 0}, {false, 24}, {false, 12}, {true, 0}, {true, 24}, {true, 12}} {
		second = a.topLeft(short.tabs, short.crumb)
		if width(second) <= a.w-1 {
			break
		}
	}
	return bandLine(clip(first, a.w), a.w) + "\n" + bandLine(clip(second, a.w), a.w) + "\n" + strings.Repeat(" ", a.w)
}

// tabIcon is the icon of a place in the theme's accent; Watching beats
// while a run you watch is running.
func (a *App) tabIcon(i int) string {
	c := cCyan
	if tabIcons[i] == "watching" && a.runningWatches() > 0 {
		c = heartbeat()
	}
	return icoIn(tabIcons[i], c)
}

// icoOn is an icon in colour c on background bg, followed by a space on it.
func icoOn(name string, c, bg lipgloss.Color) string {
	g := icon(name)
	if g == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(c).Background(bg).Render(g)
}

// heartbeat is the colour of something live: amber, beating brighter.
func heartbeat() lipgloss.Color {
	if animLevel < animSubtle {
		return cAmber
	}
	p := pulse(1400 * time.Millisecond)
	return lipgloss.Color(mix(string(cAmber), string(cWhite), 0.45*p*p))
}

// joinDot joins the non-empty parts with a dim " · ".
func joinDot(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sDim.Render(" · "))
}

// topLeft draws the logo, the places and where you are. With shortTabs only
// the current place shows its name; crumbMax > 0 shortens breadcrumbs.
func (a *App) topLeft(shortTabs bool, crumbMax int) string {
	var b strings.Builder
	write := func(s string) { b.WriteString(s) }
	write(" " + logoLine(1) + "  ")
	_, away := a.top().(placeless)
	for i, name := range tabNames {
		digit := fmt.Sprint(i + 1)
		ic := a.tabIcon(i)
		switch {
		case i == a.tab && away:
			write(sDim.Render("[") + sText.Render(" "+digit+" ") + ic + sText.Render(name+" ") + sDim.Render("]") + "  ")
		case i == a.tab:
			// The place on display is a tab of the page: it rises out of the
			// line under the header into the band.
			p := glowProgress(changedAt("place", a.tab), stripGlowMs)
			open, shut := "", ""
			if !pillsOn() {
				open, shut = activeEnds(p)
			}
			seg := sCyan.Background(cBg).Render(" "+digit+" ") + icoOn(tabIcons[i], cCyan, cBg) + sWhite.Background(cBg).Bold(true).Render(name+"  ")
			if p < 1 {
				seg = glowSeg([]glowUnit{{" " + digit + " ", sCyan}, {icon(tabIcons[i]), sCyan}, {name + "  ", sWhite.Bold(true)}}, cBg, cCyan, p)
			}
			write(open + seg + shut + " ")
		case shortTabs:
			write(sDim.Render(" "+digit+" ") + ic + " ")
		default:
			write(sDim.Render(" "+digit+" ") + ic + sHint.Render(name+" ") + "  ")
		}
	}
	full := a.trail()
	segs := full
	if crumbMax > 0 {
		segs = shortTrail(full)
	}
	for i, c := range segs {
		write(sDim.Render("› "))
		if crumbMax > 0 {
			c = midCut(c, crumbMax)
		}
		if i < len(segs)-1 {
			write(sText.Render(c) + " ")
			continue
		}
		write(currentCrumb(c, changedAtS("trail", strings.Join(full, "\x00"))) + " ")
	}
	return b.String()
}

// currentCrumb is the last segment of the trail, where you are: white and
// bold, lit for a moment when it changes, as a place is.
func currentCrumb(c string, changed int64) string {
	st := sWhite.Bold(true)
	if p := glowProgress(changed, stripGlowMs); p < 1 {
		st = st.Foreground(lipgloss.Color(mix(string(cCyan), string(cWhite), ease(p))))
	}
	return st.Underline(true).Render(c)
}

// bandLine draws s on the header band, which is lit by the accent at its
// left and fades to the band's own colour at its right.
func bandLine(s string, w int) string {
	return fadeAcross(onBand(s, w), cBand, lipgloss.Color(mix(string(cBand), string(cCyan), 0.22)), cBand)
}

// capsule puts s on bg between round ends.
func capsule(s string, bg lipgloss.Color) string {
	if s == "" {
		return ""
	}
	if !pillsOn() {
		return withBg(" "+s+" ", bg)
	}
	return fg(bg).Render(pillLeft) + withBg(s, bg) + fg(bg).Render(pillRight)
}
