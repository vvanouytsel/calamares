package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// pickItem is one choice in a picker.
type pickItem struct {
	value string
	label string // what typing matches against
	note  string // styled detail shown after the label
}

// pickerModal is a dropdown: type to narrow the list, ↑↓ to choose, enter
// to apply. With free set, enter on a text that matches nothing uses the
// text itself.
type pickerModal struct {
	key     string
	title   string
	current string
	free    bool
	onPick  func(value string) tea.Cmd
	// onCancel, when set, runs on esc, to come back to what the picker was opened from.
	onCancel func() tea.Cmd
	// onMove runs when the selection changes, to preview it.
	onMove func(value string)
	input  textinput.Model
	items  []pickItem
	shown  []int
	sel    int
	offset int
}

// pickerItemsMsg adds items to the open picker with the same key, for
// choices that arrive after it opened.
type pickerItemsMsg struct {
	key   string
	items []pickItem
}

func newPicker(key, title, current string, items []pickItem, onPick func(string) tea.Cmd) *pickerModal {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to filter"
	in.TextStyle = sWhite
	in.Cursor.Style = sCyan
	in.Focus()
	m := &pickerModal{key: key, title: title, current: current, onPick: onPick, input: in}
	m.add(items)
	for i, idx := range m.shown {
		if m.item(idx).value == current {
			m.sel = i
		}
	}
	return m
}

// add appends the items whose value isn't listed yet, keeping the selection.
func (m *pickerModal) add(items []pickItem) {
	have := map[string]bool{}
	for _, it := range m.items {
		have[it.value] = true
	}
	for _, it := range items {
		if !have[it.value] {
			have[it.value] = true
			m.items = append(m.items, it)
		}
	}
	selected, ok := m.selected()
	m.filter()
	if ok {
		for i, idx := range m.shown {
			if m.item(idx).value == selected.value {
				m.sel = i
			}
		}
	}
}

func (m *pickerModal) filter() {
	q := strings.TrimSpace(m.input.Value())
	m.shown = m.shown[:0]
	score := map[int]int{}
	for i, it := range m.items {
		if q == "" {
			m.shown = append(m.shown, i)
		} else if s, _, ok := fuzzy(q, it.label); ok {
			m.shown = append(m.shown, i)
			score[i] = s
		}
	}
	if q != "" {
		sort.SliceStable(m.shown, func(a, b int) bool { return score[m.shown[a]] > score[m.shown[b]] })
	}
	// A typed value that no choice contains is offered first, so enter uses
	// what you typed rather than a loose match (the list may be incomplete).
	if m.free && q != "" && !m.anyContains(q) {
		m.shown = append([]int{typedChoice}, m.shown...)
	}
	m.sel = min(m.sel, max(len(m.shown)-1, 0))
}

// typedChoice stands in m.shown for the text typed into the filter.
const typedChoice = -1

func (m *pickerModal) anyContains(q string) bool {
	q = strings.ToLower(q)
	for _, it := range m.items {
		if strings.Contains(strings.ToLower(it.label), q) {
			return true
		}
	}
	return false
}

func (m *pickerModal) item(idx int) pickItem {
	if idx == typedChoice {
		q := strings.TrimSpace(m.input.Value())
		return pickItem{value: q, label: q, note: sDim.Render("use what you typed")}
	}
	return m.items[idx]
}

func (m *pickerModal) selected() (pickItem, bool) {
	if m.sel < 0 || m.sel >= len(m.shown) {
		return pickItem{}, false
	}
	return m.item(m.shown[m.sel]), true
}

func (m *pickerModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.onCancel != nil {
			return true, m.onCancel()
		}
		return true, nil
	case "enter":
		if it, ok := m.selected(); ok {
			return true, m.onPick(it.value)
		}
		return false, nil
	case "up", "ctrl+p", "shift+tab":
		m.sel = max(m.sel-1, 0)
	case "down", "ctrl+n", "tab":
		m.sel = min(m.sel+1, max(len(m.shown)-1, 0))
	case "pgup":
		m.sel = max(m.sel-10, 0)
	case "pgdown":
		m.sel = min(m.sel+10, max(len(m.shown)-1, 0))
	default:
		before := m.input.Value()
		m.input, _ = m.input.Update(msg)
		if m.input.Value() != before {
			m.sel = 0
			m.filter()
		}
	}
	if m.onMove != nil {
		if it, ok := m.selected(); ok {
			m.onMove(it.value)
		}
	}
	return false, nil
}

func (m *pickerModal) Render(a *App, w, h int) string {
	rows := max(min(h-12, 14, len(m.items)), 1)
	labelW, listW := 0, 44
	for _, it := range m.items {
		labelW = max(labelW, width(it.label))
	}
	for _, it := range m.items {
		listW = max(listW, labelW+width(it.note)+6)
	}
	listW = min(listW, w-12)
	labelW = min(labelW, listW/2)
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+rows {
		m.offset = m.sel - rows + 1
	}

	lines := []string{sCyan.Render("› ") + m.input.View(), ""}
	for i := m.offset; i < len(m.shown) && i < m.offset+rows; i++ {
		it := m.item(m.shown[i])
		mark := "  "
		if it.value == m.current {
			mark = sCyan.Render("● ")
		}
		if i == m.sel {
			lines = append(lines, sSel.Bold(true).Render(fit("▸ "+fit(it.label, labelW)+"  "+ansiStrip(it.note), listW)))
			continue
		}
		lines = append(lines, fit(mark+sText.Render(fit(it.label, labelW))+"  "+it.note, listW))
	}
	if len(m.shown) == 0 {
		lines = append(lines, sDim.Render("Nothing matches."))
	}
	for len(lines) < rows+2 {
		lines = append(lines, "")
	}
	lines = append(lines, "", sDim.Render("type to filter · ↑↓ choose · enter apply · esc cancel"))
	return modalBox(title(m.title, "", len(m.shown), ""), lines, cCyan, 50, w-4)
}
