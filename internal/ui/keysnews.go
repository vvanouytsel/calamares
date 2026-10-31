package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/config"
)

// keysMoved are the keys that moved when every key got one meaning: the
// ones people pressed most, with what they do now.
var keysMoved = [][3]string{
	{"a", "A", "approve"},
	{"c", "X", "cancel a run"},
	{"m", "W", "comment, reply"},
	{"s", "r", "go to the repo (s now sorts everywhere)"},
	{"=", "d", "diff with the last run that passed"},
	{"v V", "c", "workflow file"},
	{"d", "a", "set aside"},
	{"n P", "+", "new issue, pull request, release, run"},
	{"l", "T", "labels"},
	{"T", "ctrl+t", "next theme"},
}

// keysNews tells someone who knew the old keys, once, which ones moved.
func (a *App) keysNews() tea.Cmd {
	if a.cfg.KeysVersion >= config.CurrentKeys {
		return nil
	}
	a.cfg.KeysVersion = config.CurrentKeys
	_ = a.cfg.Save()
	var b strings.Builder
	b.WriteString("Every key now means one thing on every screen. Lowercase keys only look,\n")
	b.WriteString("go somewhere or copy; capitals change something on GitHub and ask first.\n\n")
	for _, k := range keysMoved {
		b.WriteString(keycap(k[0]) + sDim.Render(" → ") + keycap(k[1]) + "  " + sText.Render(k[2]) + "\n")
	}
	b.WriteString("\n" + keycap("?") + sDim.Render(" lists every key of the screen you're on, ") + keycap(".") + sDim.Render(" everything you can do."))
	return openModal(&messageModal{
		title:  sWhite.Bold(true).Render("Keys changed"),
		header: []string{sDim.Render("what moved, and where to")},
		body:   b.String(),
	})
}
