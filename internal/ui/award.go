package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// An award is the card for something you unlocked: an achievement at a
// tier, or a new level. Cards wait their turn: one shows at a time, above
// the cards of runs, and the next one shows when it is gone.
type award struct {
	x     *achievement
	tier  tier
	level int       // a new level, for the card that says so
	at    time.Time // when it came on screen; zero while it waits
}

const (
	awardLife = 8 * time.Second
	maxAwards = 6
)

// addAward queues a card and keeps its words for the Messages screen.
func (a *App) addAward(w *award) {
	if len(a.awards) >= maxAwards {
		return
	}
	if len(a.awards) == 0 {
		w.at = time.Now()
	}
	a.awards = append(a.awards, w)
	a.note(nDone, w.message())
}

// expireAwards takes the card on display away when its time is up and
// shows the next one.
func (a *App) expireAwards() {
	if len(a.awards) == 0 || time.Since(a.awards[0].at) <= awardLife {
		return
	}
	a.awards = a.awards[1:]
	if len(a.awards) > 0 {
		a.awards[0].at = time.Now()
	}
}

// dismissAward takes the card on display away.
func (a *App) dismissAward() {
	a.awards = a.awards[1:]
	if len(a.awards) > 0 {
		a.awards[0].at = time.Now()
	}
}

func (w *award) message() string {
	if w.level > 0 {
		return fmt.Sprintf("Level up: level %d · %s", w.level, levelTitle(w.level))
	}
	return fmt.Sprintf("Achievement unlocked: %s · %s", w.x.name, w.tier)
}

// color is the colour of the card: its tier's, or the accent for a level.
func (w *award) color() lipgloss.Color {
	if w.level > 0 {
		return cCyan
	}
	return tierColor(w.tier)
}

// awardSlab is the card for what you unlocked, as a slab: the medal (or
// the trophy for a new level) at the left, what it is for and what it is
// worth beside it, in the colour of its tier, with confetti as it arrives.
// more counts the cards behind it.
func (a *App) awardSlab(w *award, cw, more int) *slab {
	c, _ := arriving(w.at, w.color())
	fill := raisedBg() // steady, as the art is drawn on it
	var art []string
	if w.level > 0 {
		art = trophy(fill)
	} else {
		m, glyph, gc := a.medalLook(w.x)
		art = medal(10, m, w.tier >= tierGold, glyph, gc, fill)
	}
	artW := width(art[0])
	tw := cw - artW - 5
	var text []string
	peek := icoIn("trophy", w.color())
	if w.level > 0 {
		text = append([]string{lineLR(fg(cCyan).Bold(true).Render("Level up"), sDim.Render("now"), tw)}, a.levelUpText(w, tw)...)
		peek += sText.Render(fmt.Sprintf("Level %d", w.level)) + sDim.Render("  "+levelTitle(w.level))
	} else {
		text = append([]string{lineLR(sGold.Bold(true).Render("Achievement unlocked"), fg(tierColor(w.tier)).Bold(true).Render(w.tier.String()), tw)}, a.unlockText(w, tw)...)
		peek += sText.Render(w.x.name) + " " + fg(tierColor(w.tier)).Render(w.tier.String()) + sDim.Render(fmt.Sprintf("  +%d XP", tierXP[w.tier]))
	}
	for len(text) < len(art) {
		text = append(text, "")
	}
	keys := roundKey("ctrl+x", fill) + " " + sHint.Render("dismiss")
	if len(tabNames) > tabAchievements {
		keys = roundKey(fmt.Sprint(tabAchievements+1), fill) + " " + sHint.Render("achievements") + "   " + keys
	}
	if more > 0 {
		keys = lineLR(keys, sDim.Render(fmt.Sprintf("%d more ▴", more)), tw)
	}
	text = append(text, "", keys)
	return &slab{at: w.at, life: awardLife, color: c, fill: fill, body: slabBody(c, fill, art, artW, text, cw), peek: peek, confetti: true}
}

// unlockText is what the card says about an achievement: its name, what
// it is for and what it is worth, with what the next tier needs.
func (a *App) unlockText(w *award, room int) []string {
	x := w.x
	lines := []string{sWhite.Bold(true).Render(x.name)}
	lines = append(lines, wrapCapped(sText.Render(x.text), room, 2)...)
	worth := fg(cCyan).Render(fmt.Sprintf("+%d XP", tierXP[w.tier]))
	if need, t := x.next(a.ach.Counts[x.id]); need > 0 {
		worth += sDim.Render(fmt.Sprintf(" · %s at %d", t, need))
	}
	lines = append(lines, worth)
	if x.quip != "" && len(lines) < 5 {
		lines = append(lines, sDim.Italic(true).Render(clip("“"+x.quip+"”", room)))
	}
	return lines
}

// levelUpText is what the card says about a new level.
func (a *App) levelUpText(w *award, room int) []string {
	lines := []string{sWhite.Bold(true).Render(fmt.Sprintf("Level %d", w.level)) + sGold.Render(" · "+levelTitle(w.level))}
	if title, at := nextTitle(w.level); title != "" {
		lines = append(lines, sDim.Render(fmt.Sprintf("Next title at level %d: ", at))+sText.Render(title))
	}
	xp := a.ach.xp()
	lines = append(lines, xpBar(xp, min(room, 30)))
	return lines
}

// xpBar is how far xp is from one level to the next, as a bar and in numbers.
func xpBar(xp, w int) string {
	l := levelOf(xp)
	if l >= maxLevel {
		return sGold.Render(strings.Repeat("━", w)) + sDim.Render(fmt.Sprintf(" %d XP · top level", xp))
	}
	from, to := levelNeeds(l), levelNeeds(l+1)
	lit := min(w*(xp-from)/(to-from), w)
	return sGold.Render(strings.Repeat("━", lit)) + fg(cFrame).Render(strings.Repeat("━", w-lit)) +
		sDim.Render(fmt.Sprintf(" %d/%d XP", xp-from, to-from))
}

// confettiColors are the colours confetti falls in.
var confettiColors = []lipgloss.Color{"#e5b94a", "#9fd36b", "#f08cc0", "#56d4c4", "#58a6ff", "#ff9a3c"}
