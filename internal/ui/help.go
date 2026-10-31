package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpView lists the keys of the screen it was opened on, then the keys that
// work everywhere. Both lists come from the bindings themselves.
type helpView struct {
	id     int
	under  View // the screen the help is about
	offset int
	rows   int // lines of help, and how many of them fit, in the last render
	fit    int
}

func newHelpView(a *App, under View) *helpView { return &helpView{id: a.newID(), under: under} }

func (v *helpView) ID() int                            { return v.id }
func (v *helpView) Init(a *App) tea.Cmd                { return nil }
func (v *helpView) Crumb() string                      { return "help" }
func (v *helpView) Refresh(a *App, force bool) tea.Cmd { return nil }

func (v *helpView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

func (v *helpView) Keys(a *App) []Binding {
	if v.rows <= v.fit {
		return nil
	}
	return []Binding{moveBinding("scroll", func(k string) tea.Cmd {
		switch k {
		case "up", "k":
			v.offset--
		case "down", "j":
			v.offset++
		case "pgup", "ctrl+b":
			v.offset -= v.fit
		case "pgdown", "ctrl+f":
			v.offset += v.fit
		case "home":
			v.offset = 0
		case "end":
			v.offset = v.rows
		}
		return nil
	})}
}

type helpGroup struct {
	title string
	keys  [][3]string // key, what it does, "off" when it doesn't apply right now
}

// helpGroups sorts bindings under their headings, in the order the headings
// first appear. A key listed twice keeps its first meaning, as when pressed.
func helpGroups(a *App, bs []Binding) []helpGroup {
	var out []helpGroup
	at := map[string]int{}
	seen := map[string]bool{}
	for _, b := range bs {
		label, text := b.label(), b.help()
		if text == "" || len(b.Keys) == 0 || seen[label] {
			continue
		}
		seen[label] = true
		group := b.Group
		if b.Mutates {
			group = groupChangesGitHub // capitals that ask first, apart from the keys that only look
		}
		i, ok := at[group]
		if !ok {
			i = len(out)
			at[group] = i
			out = append(out, helpGroup{title: group})
		}
		off := ""
		switch {
		case b.Mutates && a.opts.ReadOnly:
			off = "read-only"
		case lockedFor(a, b) != "":
			off = lockedFor(a, b)
		case b.Off:
			off = "not now"
		}
		out[i].keys = append(out[i].keys, [3]string{label, text, off})
	}
	return out
}

func (v *helpView) Render(a *App, w, h int) string {
	groups := helpGroups(a, append(v.under.Keys(a), a.globalKeys(v.under)...))
	lines := []string{" " + sWhite.Bold(true).Render("Keys of ") + sCyan.Bold(true).Render(v.under.Crumb()) +
		sDim.Render("   every screen has its own: press ? there"), ""}
	lines = append(lines, helpColumns(groups, w-4)...)
	notes := []string{
		" " + sWhite.Render("Where things are") + sDim.Render("  Repos: default branch health → a repo (Actions, Pull requests, Commits) → a run → its log · Pull requests: to review, yours, involved · Actions: what fails and runs in your active repos · Watching: runs you want to hear about · Platform: shared workflows and actions, warnings, schedules, runners and runner time · Issues: those involving you · Achievements: your medals and level"),
		" " + sWhite.Render("Explain") + sDim.Render("  i on a screen says what each of its chips, columns and panel parts means"),
		" " + sWhite.Render("Keys") + sDim.Render("  a key means the same on every screen · lowercase keys only look, go somewhere or copy · capitals change something on GitHub and ask first · . lists everything you can do"),
		" " + sWhite.Render("Refreshing") + sDim.Render("  Actions keeps itself up to date; another screen checks GitHub when you open it with data older than 5 minutes, ctrl+r refreshes it, L keeps it live while you're here"),
		" " + sWhite.Render("Filtering") + sDim.Render("  words match any column · ") + sText.Render("status:failed") + sDim.Render(" or ") + sText.Render("status:passed") + sDim.Render(" · ") + sText.Render("branch:main,dev") + sDim.Render(" either · ") + sText.Render("-actor:*[bot]") + sDim.Render(" not · ") + sText.Render(`workflow:"Build and release"`) + sDim.Render(" spaces · ") + sText.Render("actor:@me") + sDim.Render(" you"),
		" " + sWhite.Render("Status") + "  " + sGreen.Render("✓ passed  ") + sRed.Render("✗ failed  ") + sAmber.Render("◐ running  ") + fg(cGrey).Render("○ queued  ") + sWait.Render("◆ waiting for approval  ") + sDim.Render("⊘ canceled  – skipped"),
	}
	places := make([]string, len(tabNames))
	for i, n := range tabNames {
		places[i] = fmt.Sprintf("%d %s", i+1, n)
	}
	placesLine := []string{" " + sWhite.Render("Places") + "  " + sText.Render(strings.Join(places, " · ")) + sDim.Render("   < > previous / next"), ""}
	lines = append(append(lines[:2:2], placesLine...), lines[2:]...)
	for _, n := range notes {
		lines = append(lines, strings.Split(lipgloss.NewStyle().Width(w-4).Render(n), "\n")...)
	}
	if header := logoHeader(a); h-2 >= len(lines)+len(header) {
		lines = append(header, lines...)
	}
	v.rows, v.fit = len(lines), max(h-2, 1)
	v.offset = max(min(v.offset, v.rows-v.fit), 0)
	ttl := title("Keys", "? or esc closes", -1, "")
	if v.rows > v.fit {
		ttl = title("Keys", "? or esc closes · ↑↓ scrolls", -1, "")
		lines = lines[v.offset:min(v.offset+v.fit, len(lines))]
	}
	return box(ttl, strings.Join(lines, "\n"), w, h, cFrame)
}

// helpColumns lays the headings out in columns: a short heading goes under
// the one before it while the column stays no taller than the longest
// heading, and columns that don't fit next to each other go below.
func helpColumns(groups []helpGroup, w int) []string {
	type column struct {
		lines []string
		width int
	}
	tallest := 0
	for _, g := range groups {
		tallest = max(tallest, len(g.keys)+1)
	}
	var cols []column
	for _, g := range groups {
		keyW := 0
		for _, k := range g.keys {
			keyW = max(keyW, width(k[0]))
		}
		block := []string{sWhite.Bold(true).Render(g.title)}
		for _, k := range g.keys {
			line := sTan.Render(fit(k[0], keyW)) + "  " + sText.Render(k[1])
			if k[2] != "" {
				line = sDim.Render(fit(k[0], keyW) + "  " + k[1] + " · " + k[2])
			}
			block = append(block, line)
		}
		if n := len(cols); n > 0 && len(cols[n-1].lines)+1+len(block) <= tallest {
			cols[n-1].lines = append(append(cols[n-1].lines, ""), block...)
		} else {
			cols = append(cols, column{lines: block})
		}
		c := &cols[len(cols)-1]
		for _, l := range block {
			c.width = max(c.width, width(l)+4)
		}
	}
	var lines []string
	for start := 0; start < len(cols); {
		end, used := start, 0
		for end < len(cols) && (end == start || used+cols[end].width <= w) {
			used += cols[end].width
			end++
		}
		rows := 0
		for _, c := range cols[start:end] {
			rows = max(rows, len(c.lines))
		}
		for r := 0; r < rows; r++ {
			var b strings.Builder
			b.WriteString(" ")
			for _, c := range cols[start:end] {
				cell := ""
				if r < len(c.lines) {
					cell = c.lines[r]
				}
				b.WriteString(fit(cell, c.width))
			}
			lines = append(lines, b.String())
		}
		lines = append(lines, "")
		start = end
	}
	return lines
}

// logoHeader puts the name next to the logo.
func logoHeader(a *App) []string {
	logo := brailleArt(bigLogo)
	text := []string{
		sGold.Bold(true).Render("Calamares"),
		sDim.Render("a command center for GitHub"),
		"",
		sHint.Render("what you get when you cook an octocat"),
	}
	if a.opts.Version != "" {
		text = append(text, "", sDim.Render(a.opts.Version))
	}
	first := (len(logo) - len(text)) / 2
	out := make([]string, 0, len(logo)+1)
	for i, l := range logo {
		line := " " + l
		if j := i - first; j >= 0 && j < len(text) {
			line += "   " + text[j]
		}
		out = append(out, line)
	}
	return append(out, "")
}

// lockedFor is the reason a binding is locked for this token, with the lock
// icon, or "".
func lockedFor(a *App, b Binding) string {
	if d, locked := a.lockOf(b); locked {
		return strings.TrimSpace(lockMark()) + " " + d.Reason
	}
	return ""
}
