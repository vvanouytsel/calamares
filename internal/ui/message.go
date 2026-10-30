package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// messageModal shows a commit with its whole message, scrolling when it is long.
type messageModal struct {
	title  string
	header []string
	body   string
	offset int
}

func commitMessage(c gh.Commit) tea.Cmd {
	m := &messageModal{
		title:  sTan.Bold(true).Render(c.Short()),
		header: []string{sText.Render(c.Author) + sDim.Render("  ·  "+age(c.Date, time.Now())+" ago  ·  "+c.Date.Local().Format("2006-01-02 15:04"))},
		body:   strings.TrimSpace(c.Message),
	}
	return openModal(m)
}

func (m *messageModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.offset = max(m.offset-1, 0)
	case "down", "j":
		m.offset++
	case "pgup":
		m.offset = max(m.offset-10, 0)
	case "pgdown":
		m.offset += 10
	case "esc", "enter", "q", "m":
		return true, nil
	}
	return false, nil
}

func (m *messageModal) Render(a *App, w, h int) string {
	inner := min(max(w-12, 30), 100)
	body := strings.Split(ansi.Wordwrap(m.body, inner, ""), "\n")
	room := max(h-10, 3)
	m.offset = min(m.offset, max(len(body)-room, 0))
	shown := body[m.offset:min(m.offset+room, len(body))]
	lines := append([]string{}, m.header...)
	lines = append(lines, "")
	for _, l := range shown {
		lines = append(lines, sWhite.Render(l))
	}
	hint := "enter / esc closes"
	if len(body) > room {
		hint = "↑↓ scrolls · " + hint
	}
	lines = append(lines, "", sDim.Render(hint))
	return modalBox(m.title, lines, cCyan, inner+6, w-4)
}
