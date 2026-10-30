package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// multiItem is one choice of a multi-select picker.
type multiItem struct {
	value string
	label string
	color string // hex without #, shown as a dot, or ""
	note  string
}

// multiPicker lets you tick several items: space ticks, enter applies, ticked
// items come first, typing narrows. It says what will change before it does.
type multiPicker struct {
	title    string
	input    textinput.Model
	items    []multiItem
	initial  map[string]bool
	ticked   map[string]bool
	shown    []int
	sel      int
	offset   int
	onApply  func(add, remove []string) tea.Cmd
	onCancel func() tea.Cmd // when set, esc runs it instead of just closing
	applyTxt string
}

func newMultiPicker(title string, items []multiItem, current []string, onApply func(add, remove []string) tea.Cmd) *multiPicker {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to narrow"
	in.TextStyle = sWhite
	in.Cursor.Style = sCyan
	in.Focus()
	m := &multiPicker{title: title, input: in, initial: map[string]bool{}, ticked: map[string]bool{}, onApply: onApply}
	have := map[string]bool{}
	for _, it := range items {
		have[it.value] = true
	}
	for _, c := range current {
		m.initial[c], m.ticked[c] = true, true
		if !have[c] { // ticked now, though not in the list of candidates
			items = append(items, multiItem{value: c, label: c})
		}
	}
	m.items = items
	m.filter()
	return m
}

func (m *multiPicker) filter() {
	q := strings.TrimSpace(m.input.Value())
	score := map[int]int{}
	m.shown = m.shown[:0]
	for i, it := range m.items {
		if q == "" {
			m.shown = append(m.shown, i)
		} else if s, _, ok := fuzzy(q, it.label); ok {
			m.shown = append(m.shown, i)
			score[i] = s
		}
	}
	sort.SliceStable(m.shown, func(a, b int) bool {
		ta, tb := m.initial[m.items[m.shown[a]].value], m.initial[m.items[m.shown[b]].value]
		if ta != tb {
			return ta
		}
		return q != "" && score[m.shown[a]] > score[m.shown[b]]
	})
	m.sel = min(m.sel, max(len(m.shown)-1, 0))
}

// changes returns what ticking and unticking amounts to.
func (m *multiPicker) changes() (add, remove []string) {
	for _, it := range m.items {
		switch {
		case m.ticked[it.value] && !m.initial[it.value]:
			add = append(add, it.value)
		case !m.ticked[it.value] && m.initial[it.value]:
			remove = append(remove, it.value)
		}
	}
	return add, remove
}

func (m *multiPicker) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.onCancel != nil {
			return true, m.onCancel()
		}
		return true, nil
	case "enter":
		add, remove := m.changes()
		if len(add)+len(remove) == 0 {
			return true, a.flashCmd(nInfo, "Nothing changed.")
		}
		return true, m.onApply(add, remove)
	case " ", "ctrl+t":
		if m.sel < len(m.shown) {
			v := m.items[m.shown[m.sel]].value
			m.ticked[v] = !m.ticked[v]
		}
	case "up", "ctrl+p", "shift+tab":
		m.sel = max(m.sel-1, 0)
	case "down", "ctrl+n", "tab":
		m.sel = min(m.sel+1, max(len(m.shown)-1, 0))
	default:
		before := m.input.Value()
		m.input, _ = m.input.Update(msg)
		if m.input.Value() != before {
			m.sel = 0
			m.filter()
		}
	}
	return false, nil
}

func (m *multiPicker) Render(a *App, w, h int) string {
	rows := max(min(h-14, 12, len(m.items)), 3)
	listW := 46
	for _, it := range m.items {
		listW = max(listW, width(it.label)+width(it.note)+10)
	}
	listW = min(listW, w-12)
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+rows {
		m.offset = m.sel - rows + 1
	}
	lines := []string{sCyan.Render("› ") + m.input.View(), ""}
	for i := m.offset; i < len(m.shown) && i < m.offset+rows; i++ {
		it := m.items[m.shown[i]]
		box := "[ ] "
		if m.ticked[it.value] {
			box = "[x] "
		}
		dot := ""
		if it.color != "" {
			dot = lipgloss.NewStyle().Foreground(lipgloss.Color("#" + it.color)).Render("● ")
		}
		text := box + dot + it.label
		if it.note != "" {
			text += "  " + it.note
		}
		if i == m.sel {
			lines = append(lines, sSel.Bold(true).Render(fit("▸ "+ansiStrip(text), listW)))
			continue
		}
		style := sText
		if m.ticked[it.value] {
			style = sWhite
		}
		lines = append(lines, "  "+style.Render(box)+dot+style.Render(it.label)+"  "+sDim.Render(ansiStrip(it.note)))
	}
	if len(m.shown) == 0 {
		lines = append(lines, sDim.Render("Nothing matches."))
	}
	add, remove := m.changes()
	var change []string
	for _, x := range add {
		change = append(change, sGreen.Render("+ "+x))
	}
	for _, x := range remove {
		change = append(change, sRed.Render("− "+x))
	}
	summary := sDim.Render("nothing changed yet")
	if len(change) > 0 {
		summary = strings.Join(change, "  ")
	}
	lines = append(lines, "", summary, "", sDim.Render("space ticks · enter applies · esc cancels"))
	return modalBox(title(m.title, "", -1, ""), lines, cCyan, 50, w-4)
}
