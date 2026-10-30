package ui

import (
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// menuTitler is implemented by screens that can say what is selected, for the
// title of the action menu; otherwise the crumb is used.
type menuTitler interface{ MenuTitle(a *App) string }

// menuItem is one line of the action menu: a binding of the screen, with
// the reason it can't be used now, if any.
type menuItem struct {
	b      Binding
	label  string
	key    string
	group  string
	reason string
}

// actionMenu lists everything you can do with what is selected: every
// binding of the screen with a description, including the ones that have no
// key of their own. Type to narrow it; enter runs the binding as its key would.
type actionMenu struct {
	title  string
	input  textinput.Model
	items  []menuItem
	shown  []int
	sel    int
	offset int
}

func (a *App) openActionMenu() tea.Cmd {
	a.earn("power-user", 1)
	v := a.top()
	global := map[string]bool{}
	for _, g := range a.globalKeys(v) {
		for _, k := range g.Keys {
			global[k] = true
		}
	}
	var items []menuItem
	seen := map[string]bool{}
	for _, b := range v.Keys(a) {
		text := b.help()
		if text == "" || b.Do == nil || b.Group == groupMove || b.Group == groupEverywhere || overlaps(b.Keys, moveKeys) {
			continue
		}
		if len(b.Keys) > 0 && global[b.Keys[0]] && b.Keys[0] != "enter" {
			continue
		}
		// The words in brackets are for the help screen.
		if i := strings.Index(text, " ("); i > 0 && !strings.HasSuffix(text, "…") {
			text = text[:i]
		}
		it := menuItem{b: b, label: text, group: b.Group}
		if len(b.Keys) > 0 {
			it.key = b.label()
			if seen[it.key] {
				continue
			}
			seen[it.key] = true
		}
		switch {
		case b.Mutates && a.opts.ReadOnly:
			it.reason = "read-only mode"
		case lockedFor(a, b) != "":
			it.reason = lockedFor(a, b)
		case b.Off:
			it.reason = "not now"
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return a.flashCmd(nHint, "Nothing to do with this here.")
	}
	// One heading per group, in the order the groups first appear.
	var order []string
	for _, it := range items {
		if !slices.Contains(order, it.group) {
			order = append(order, it.group)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return slices.Index(order, items[i].group) < slices.Index(order, items[j].group)
	})
	name := v.Crumb()
	if t, ok := v.(menuTitler); ok {
		if s := t.MenuTitle(a); s != "" {
			name = s
		}
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to search"
	in.TextStyle = sWhite
	in.Cursor.Style = sCyan
	in.Focus()
	m := &actionMenu{title: "Actions for " + name, input: in, items: items}
	m.filter()
	return openModal(m)
}

func (m *actionMenu) filter() {
	q := strings.TrimSpace(m.input.Value())
	m.shown = m.shown[:0]
	score := map[int]int{}
	for i, it := range m.items {
		if q == "" {
			m.shown = append(m.shown, i)
			continue
		}
		s, _, ok := fuzzy(q, it.label)
		if k, _, kok := fuzzy(q, it.key); kok && it.key != "" && strings.EqualFold(q, it.key) {
			s, ok = s+100, true
			_ = k
		}
		if ok {
			m.shown = append(m.shown, i)
			score[i] = s
		}
	}
	if q != "" {
		sort.SliceStable(m.shown, func(a, b int) bool { return score[m.shown[a]] > score[m.shown[b]] })
	}
	m.sel = min(m.sel, max(len(m.shown)-1, 0))
}

func (m *actionMenu) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "enter":
		if m.sel < len(m.shown) {
			it := m.items[m.shown[m.sel]]
			key := ""
			if len(it.b.Keys) > 0 {
				key = it.b.Keys[0]
			}
			return true, a.invoke(it.b, key)
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
	return false, nil
}

func (m *actionMenu) Render(a *App, w, h int) string {
	listW := 64
	for _, it := range m.items {
		listW = max(listW, width(it.label)+width(it.reason)+14)
	}
	listW = min(listW, 84, w-12)
	rows := max(min(h-8, 20), 3)
	type line struct {
		text string
		idx  int // index in m.shown, or -1 for a heading
	}
	var all []line
	last := "\x00"
	for i, si := range m.shown {
		it := m.items[si]
		if strings.TrimSpace(m.input.Value()) == "" && it.group != last && it.group != "" {
			all = append(all, line{sDim.Render(it.group), -1})
			last = it.group
		}
		all = append(all, line{"", i})
	}
	sel := 0
	for i, l := range all {
		if l.idx == m.sel {
			sel = i
		}
	}
	if sel < m.offset {
		m.offset = sel
	}
	if sel >= m.offset+rows {
		m.offset = sel - rows + 1
	}
	lines := []string{sCyan.Render("› ") + m.input.View(), ""}
	for i := m.offset; i < len(all) && i < m.offset+rows; i++ {
		l := all[i]
		if l.idx < 0 {
			lines = append(lines, l.text)
			continue
		}
		it := m.items[m.shown[l.idx]]
		right := it.key
		if it.reason != "" {
			right = it.reason
		}
		labelW := listW - width(right) - 6
		if l.idx == m.sel {
			lines = append(lines, sSel.Bold(true).Render(fit("▸ "+fit(it.label, labelW)+"  "+right, listW)))
			continue
		}
		left := "  " + fit(it.label, labelW)
		switch {
		case it.reason != "":
			lines = append(lines, sDim.Render(fit(left+"  "+right, listW)))
		default:
			lines = append(lines, sText.Render(left)+strings.Repeat(" ", max(listW-width(left)-width(right), 1))+sDim.Render(right))
		}
	}
	if len(m.shown) == 0 {
		lines = append(lines, sDim.Render("Nothing matches."))
	}
	lines = append(lines, "", sDim.Render("type to search · ↑↓ choose · enter runs it · esc closes"))
	return modalBox(title(m.title, "", -1, ""), lines, cCyan, 56, w-4)
}
