package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// Modal is a dialog drawn over the dimmed screen; it gets every key.
type Modal interface {
	Update(a *App, msg tea.KeyMsg) (done bool, cmd tea.Cmd)
	Render(a *App, w, h int) string
}

type modalMsg struct{ m Modal }

func openModal(m Modal) tea.Cmd { return func() tea.Msg { return modalMsg{m} } }

func button(label string, on, danger bool) string {
	switch {
	case danger:
		return pill(label, cFailFrame, cWhite)
	case on:
		return pill(label, cCyan, cOnAccent)
	}
	return pill(label, cCrumbBg, cCrumbFg)
}

func modalBox(t string, lines []string, border lipgloss.Color, minW, maxW int) string {
	w := minW
	for _, l := range lines {
		w = max(w, width(l)+6)
	}
	w = min(w, maxW)
	body := make([]string, 0, len(lines)+2)
	body = append(body, "")
	for _, l := range lines {
		body = append(body, "  "+l)
	}
	body = append(body, "")
	// A dialog is a raised slab: its frame holds the surface of what floats.
	out := strings.Split(frame(t, strings.Join(body, "\n"), w, len(body)+2, border), "\n")
	for i, l := range out {
		out[i] = withBg(l, raisedBg())
	}
	return strings.Join(out, "\n")
}

// confirmModal asks before a destructive action. The safe choice is focused,
// so only a deliberate "y" goes ahead.
type confirmModal struct {
	title  string
	lines  []string
	yes    string
	no     string
	onYes  func() tea.Cmd
	danger bool
}

func confirm(title string, lines []string, yes string, danger bool, onYes func() tea.Cmd) tea.Cmd {
	return openModal(&confirmModal{title: title, lines: lines, yes: yes, no: "Cancel", onYes: onYes, danger: danger})
}

func (m *confirmModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return true, m.onYes()
	case "n", "N", "esc", "enter", "q":
		return true, nil
	}
	return false, nil
}

func (m *confirmModal) Render(a *App, w, h int) string {
	col := cCyan
	tc := sCyan
	if m.danger {
		col, tc = cRed, sRed
	}
	lines := append([]string{}, m.lines...)
	lines = append(lines, "", button("[y] "+m.yes, false, m.danger)+"  "+button("[n] "+m.no, true, false)+"  "+sDim.Render("enter / esc = no"))
	return modalBox(tc.Bold(true).Render(m.title), lines, col, 50, w-4)
}

// editorModal asks for a message of several lines: comments, replies and reviews.
// ctrl+s sends it, enter starts a new line.
type editorModal struct {
	title    string
	lines    []string
	input    textarea.Model
	required bool
	send     string
	onSubmit func(text string) tea.Cmd
	// A second way to send (ctrl+n), and a suggestion ctrl+g inserts.
	altSend    string
	onAlt      func(text string) tea.Cmd
	suggestion string
	pv         int // the preview: 0 follows the width, 1 on, 2 off
}

func askEditor(title string, lines []string, placeholder, initial, send string, required bool, onSubmit func(string) tea.Cmd) tea.Cmd {
	return openEditor(&editorModal{title: title, lines: lines, required: required, send: send, onSubmit: onSubmit}, placeholder, initial)
}

// openEditor builds the text area of m and opens it.
func openEditor(m *editorModal, placeholder string, initial ...string) tea.Cmd {
	in := textarea.New()
	in.Prompt = ""
	in.ShowLineNumbers = false
	in.Placeholder = placeholder
	in.CharLimit = 65000
	in.SetHeight(8)
	in.SetWidth(70)
	in.FocusedStyle.CursorLine = lipgloss.NewStyle()
	in.FocusedStyle.Text = sWhite
	in.FocusedStyle.Placeholder = sDim
	in.BlurredStyle = in.FocusedStyle
	if len(initial) > 0 {
		in.SetValue(initial[0])
	}
	in.Focus()
	m.input = in
	return openModal(m)
}

func (m *editorModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "ctrl+s", "alt+enter":
		text := strings.TrimSpace(m.input.Value())
		if m.required && text == "" {
			return false, nil
		}
		return true, m.onSubmit(text)
	case "ctrl+p":
		if m.pv == 1 || (m.pv == 0 && a.w >= 140) {
			m.pv = 2
		} else {
			m.pv = 1
		}
		return false, nil
	case "ctrl+n":
		text := strings.TrimSpace(m.input.Value())
		if m.onAlt == nil || (m.required && text == "") {
			return false, nil
		}
		return true, m.onAlt(text)
	case "ctrl+g":
		if m.suggestion != "" {
			m.input.InsertString("```suggestion" + string(rune(10)) + m.suggestion + string(rune(10)) + "```")
		}
		return false, nil
	}
	m.input, _ = m.input.Update(msg)
	return false, nil
}

// dialogWidth is how wide a big dialog (an editor, a form) is on a terminal
// of w columns: most of it, but never wider than max, and never narrower
// than will do.
func dialogWidth(w, max int) int {
	return min(max, w-4, max0i(w*8/10, 60))
}

func max0i(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// dialogHeight is how many rows of text a big dialog can show when it also
// needs chrome rows besides.
func dialogHeight(h, chrome int) int {
	return min(max0i(h*9/10-chrome, 5), 34)
}

func (m *editorModal) Render(a *App, w, h int) string {
	preview := m.pv == 1 || (m.pv == 0 && w >= 140)
	side := preview && w >= 140
	boxW := dialogWidth(w, 130)
	if side {
		boxW = dialogWidth(w, 168)
	}
	textW := max0i(boxW-12, 20)
	if side {
		textW = (boxW - 15) / 2
	}
	m.input.SetWidth(textW)
	m.input.SetHeight(dialogHeight(h, len(m.lines)+9))
	lines := append([]string{}, m.lines...)
	lines = append(lines, "")
	text := strings.Split(m.input.View(), "\n")
	var shown []string
	switch {
	case side:
		md := renderMarkdown(m.input.Value(), textW)
		if strings.TrimSpace(m.input.Value()) == "" {
			md = []string{sDim.Render("The preview shows here as you write.")}
		}
		for i, l := range text {
			r := ""
			if i < len(md) {
				r = md[i]
			}
			shown = append(shown, sCyan.Render("│ ")+fit(l, textW)+sDim.Render(" ┃ ")+fit(r, textW))
		}
	case preview:
		md := renderMarkdown(m.input.Value(), textW)
		if strings.TrimSpace(m.input.Value()) == "" {
			md = []string{sDim.Render("Nothing to preview yet.")}
		}
		for i := range text {
			r := ""
			if i < len(md) {
				r = md[i]
			}
			shown = append(shown, sDim.Render("┃ ")+r)
		}
	default:
		for _, l := range text {
			shown = append(shown, sCyan.Render("│ ")+l)
		}
	}
	lines = append(lines, shown...)
	flip := "ctrl+p preview"
	if preview {
		flip = "ctrl+p back to writing"
	}
	hint := "ctrl+s " + m.send + " · enter new line · " + flip + " · esc cancels"
	if m.onAlt != nil {
		hint = "ctrl+s " + m.send + " · ctrl+n " + m.altSend + " · enter new line · " + flip + " · esc cancels"
	}
	if m.suggestion != "" {
		hint += " · ctrl+g inserts a suggestion"
	}
	if m.required && strings.TrimSpace(m.input.Value()) == "" {
		hint = "type a message · esc cancels"
	}
	lines = append(lines, "", sDim.Render(hint))
	return modalBox(sCyan.Bold(true).Render(m.title), lines, cCyan, boxW, boxW)
}

// approveModal reviews pending deployments of a run.
type approveModal struct {
	repo string
	run  gh.Run
	envs []gh.PendingDeployment
}

// approveRun loads the pending deployments of run and opens the review dialog.
func (a *App) approveRun(repo string, run gh.Run) tea.Cmd {
	if run.Status != "waiting" {
		return a.flashCmd(nHint, "This run isn't waiting for an approval.")
	}
	return func() tea.Msg {
		envs, err := a.client.PendingDeployments(context.Background(), repo, run.ID)
		if err != nil {
			return flashMsg{nError, "Could not load pending deployments: " + err.Error()}
		}
		if len(envs) == 0 {
			return flashMsg{nHint, "Nothing is waiting for approval on this run."}
		}
		return modalMsg{&approveModal{repo: repo, run: run, envs: envs}}
	}
}

func (m *approveModal) ids() []int64 {
	var ids []int64
	for _, e := range m.envs {
		if e.CurrentUserCanApprove {
			ids = append(ids, e.Environment.ID)
		}
	}
	return ids
}

func (m *approveModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	review := func(approve bool) tea.Cmd {
		ids := m.ids()
		if len(ids) == 0 {
			return a.flashCmd(nHint, "You're not a required reviewer for these environments.")
		}
		verb := "Rejected"
		if approve {
			verb = "Approved"
		}
		return a.act(m.repo, fmt.Sprintf("%s deployment of %s #%d", verb, m.run.Name, m.run.RunNumber), func(ctx context.Context) error {
			return a.client.ReviewDeployments(ctx, m.repo, m.run.ID, ids, approve, "Reviewed from Calamares")
		})
	}
	switch msg.String() {
	case "a":
		return true, review(true)
	case "x":
		// Rejecting stops a deployment for everyone: ask once more.
		return true, confirm("Reject deployment?", []string{
			sWhite.Render(fmt.Sprintf("%s #%d", m.run.Name, m.run.RunNumber)) + sDim.Render(" · "+m.run.DisplayTitle),
			"",
			"The run stops waiting and its deployment jobs will not run.",
		}, "Reject", true, func() tea.Cmd { return review(false) })
	case "esc", "n", "q", "enter":
		return true, nil
	}
	return false, nil
}

func (m *approveModal) Render(a *App, w, h int) string {
	lines := []string{
		sWhite.Render(fmt.Sprintf("%s #%d", m.run.Name, m.run.RunNumber)) + sDim.Render(" · "+m.run.DisplayTitle),
		sDim.Render(m.run.HeadBranch + " · by " + m.run.Actor.Login),
		"",
		"Waiting on:",
	}
	for _, e := range m.envs {
		who := sGreen.Render("you can approve")
		if !e.CurrentUserCanApprove {
			who = sDim.Render("not your review")
		}
		lines = append(lines, "  "+sWait.Render("◆ "+e.Environment.Name)+"  "+who)
	}
	lines = append(lines, "",
		button("[a] Approve", false, false)+"  "+button("[x] Reject", false, true)+"  "+button("[esc] Later", true, false))
	return modalBox(sWait.Bold(true).Render("Review deployment"), lines, cWait, 56, w-4)
}
