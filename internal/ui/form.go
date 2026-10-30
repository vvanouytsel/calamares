package ui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	fText   fieldKind = iota // one line
	fArea                    // several lines
	fChoice                  // one of a few, ←→ or space
	fBool                    // space
	fMulti                   // several of many, enter opens a picker
	fPick                    // one of many, enter opens a searchable picker
)

// formField is one field of a form.
type formField struct {
	kind     fieldKind
	label    string
	required bool
	rows     int // the least rows a text area shows
	hint     string

	text    textinput.Model
	area    textarea.Model
	options []string // fChoice
	choice  int
	on      bool     // fBool
	picked  []string // fMulti
	// candidates lists what a fMulti field can pick from; it runs when the
	// picker opens.
	candidates func() ([]multiItem, error)
}

// formModal is a form in a dialog: text, multi-line text, choices, switches
// and multi-select fields, and a button that submits. New issue, new pull
// request and new release use it.
type formModal struct {
	title  string
	lines  []string // shown above the fields
	fields []*formField
	focus  int
	err    string
	submit string // the label of the button
	// onSubmit runs when the form is valid and the button is pressed.
	onSubmit func(a *App, m *formModal) tea.Cmd
	// onChange runs after a choice field changed, to refresh what depends on it.
	onChange func(a *App, m *formModal, i int) tea.Cmd
	footer   func() []string
	// keys are extra keys of the form, such as ctrl+g to generate release notes.
	keys map[string]func(a *App, m *formModal) tea.Cmd
}

// formAsyncMsg runs apply on the UI goroutine, for something that was read
// while a form was open.
type formAsyncMsg struct{ apply func() }

func newTextFormField(label, value string, required bool) *formField {
	t := textinput.New()
	t.Prompt = ""
	t.SetValue(value)
	t.Width = 54
	t.CharLimit = 300
	t.TextStyle = sWhite
	t.Cursor.Style = sCyan
	return &formField{kind: fText, label: label, text: t, required: required}
}

func newAreaFormField(label, value string, rows int) *formField {
	in := textarea.New()
	in.Prompt = ""
	in.ShowLineNumbers = false
	in.CharLimit = 65000
	in.SetWidth(58)
	in.SetHeight(rows)
	in.FocusedStyle.CursorLine = sWhite
	in.FocusedStyle.Text = sWhite
	in.BlurredStyle = in.FocusedStyle
	in.SetValue(value)
	return &formField{kind: fArea, label: label, area: in, rows: rows}
}

func newChoiceFormField(label string, options []string, choice int) *formField {
	return &formField{kind: fChoice, label: label, options: options, choice: choice}
}

func (f *formField) value() string {
	switch f.kind {
	case fText:
		return strings.TrimSpace(f.text.Value())
	case fArea:
		return strings.TrimSpace(f.area.Value())
	case fChoice, fPick:
		if f.choice < len(f.options) {
			return f.options[f.choice]
		}
	case fBool:
		if f.on {
			return "true"
		}
		return "false"
	case fMulti:
		return strings.Join(f.picked, ",")
	}
	return ""
}

func (f *formField) empty() bool {
	if f.kind == fMulti {
		return len(f.picked) == 0
	}
	return f.value() == ""
}

func (m *formModal) focusField(i int) {
	n := len(m.fields) + 1
	i = (i%n + n) % n
	for j, f := range m.fields {
		switch f.kind {
		case fText:
			if j == i {
				f.text.Focus()
				f.text.CursorEnd()
			} else {
				f.text.Blur()
			}
		case fArea:
			if j == i {
				f.area.Focus()
			} else {
				f.area.Blur()
			}
		}
	}
	m.focus = i
}

func (m *formModal) onButton() bool { return m.focus == len(m.fields) }

// field finds a field by its label.
func (m *formModal) field(label string) *formField {
	for _, f := range m.fields {
		if f.label == label {
			return f
		}
	}
	return nil
}

func (m *formModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	k := msg.String()
	if h, ok := m.keys[k]; ok {
		return false, h(a, m)
	}
	switch k {
	case "esc":
		return true, nil
	case "tab", "down":
		if k == "down" && !m.onButton() && m.fields[m.focus].kind == fArea {
			break
		}
		m.focusField(m.focus + 1)
		return false, nil
	case "shift+tab", "up":
		if k == "up" && !m.onButton() && m.fields[m.focus].kind == fArea {
			break
		}
		m.focusField(m.focus - 1)
		return false, nil
	case "ctrl+s":
		return m.trySubmit(a)
	}
	if m.onButton() {
		if k == "enter" {
			return m.trySubmit(a)
		}
		return false, nil
	}
	f := m.fields[m.focus]
	switch f.kind {
	case fText:
		if k == "enter" {
			m.focusField(m.focus + 1)
			return false, nil
		}
		var cmd tea.Cmd
		f.text, cmd = f.text.Update(msg)
		m.err = ""
		return false, cmd
	case fArea:
		var cmd tea.Cmd
		f.area, cmd = f.area.Update(msg)
		m.err = ""
		return false, cmd
	case fChoice:
		if len(f.options) == 0 {
			return false, nil
		}
		d := 0
		switch k {
		case "right", " ":
			d = 1
		case "left":
			d = -1
		case "enter":
			m.focusField(m.focus + 1)
			return false, nil
		}
		if d != 0 {
			f.choice = (f.choice + d + len(f.options)) % len(f.options)
			if m.onChange != nil {
				return false, m.onChange(a, m, m.focus)
			}
		}
	case fBool:
		if k == " " {
			f.on = !f.on
		} else if k == "enter" {
			m.focusField(m.focus + 1)
		}
	case fMulti:
		if k == "enter" || k == " " {
			return false, m.pick(a, f)
		}
	case fPick:
		if k == "enter" || k == " " {
			return false, m.pickOne(a, f)
		}
	}
	return false, nil
}

// pickOne opens a searchable picker for a fPick field and comes back to the form.
func (m *formModal) pickOne(a *App, f *formField) tea.Cmd {
	var items []pickItem
	for _, o := range f.options {
		items = append(items, pickItem{value: o, label: o})
	}
	idx := m.focus
	p := newPicker("form-pick", f.label, f.value(), items, func(v string) tea.Cmd {
		for i, o := range f.options {
			if o == v {
				f.choice = i
			}
		}
		cmds := []tea.Cmd{openModal(m)}
		if m.onChange != nil {
			cmds = append(cmds, m.onChange(a, m, idx))
		}
		return tea.Batch(cmds...)
	})
	p.onCancel = func() tea.Cmd { return openModal(m) }
	return openModal(p)
}

// pick opens the picker of a multi-select field and comes back to the form.
func (m *formModal) pick(a *App, f *formField) tea.Cmd {
	return func() tea.Msg {
		items, err := f.candidates()
		if err != nil {
			return flashMsg{nError, "Could not list the choices: " + err.Error()}
		}
		p := newMultiPicker(f.label, items, f.picked, func(add, remove []string) tea.Cmd {
			var now []string
			for _, p := range f.picked {
				if !slices.Contains(remove, p) {
					now = append(now, p)
				}
			}
			f.picked = append(now, add...)
			return openModal(m)
		})
		p.onCancel = func() tea.Cmd { return openModal(m) }
		return modalMsg{p}
	}
}

func (m *formModal) trySubmit(a *App) (bool, tea.Cmd) {
	for _, f := range m.fields {
		if f.required && f.empty() {
			m.err = f.label + " is required"
			return false, nil
		}
	}
	return true, m.onSubmit(a, m)
}

func (m *formModal) Render(a *App, w, h int) string {
	boxW := dialogWidth(w, 120)
	labelW := 12
	for _, f := range m.fields {
		labelW = max(labelW, width(f.label)+3)
	}
	tw := min(max0i(boxW-labelW-14, 30), 110)
	ah := dialogHeight(h, len(m.lines)+len(m.fields)*2+10) / max0i(areas(m), 1)
	lines := append([]string{}, m.lines...)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	for i, f := range m.fields {
		label := sText.Render(f.label)
		if i == m.focus {
			label = sCyan.Bold(true).Render(f.label)
		}
		if f.required {
			label += sRed.Render(" *")
		}
		var val string
		switch f.kind {
		case fText:
			f.text.Width = tw - 2
			if i == m.focus {
				val = sDim.Render("[") + fit(f.text.View(), tw) + sDim.Render("]")
			} else {
				val = sDim.Render("[") + fit(sWhite.Render(f.text.Value()), tw) + sDim.Render("]")
			}
		case fArea:
			f.area.SetWidth(max0i(boxW-12, 30))
			f.area.SetHeight(max0i(f.rows, ah))
			rows := strings.Split(f.area.View(), "\n")
			for j := range rows {
				rows[j] = sCyan.Render("│ ") + rows[j]
			}
			lines = append(lines, fit(label, labelW))
			lines = append(lines, rows...)
			continue
		case fChoice:
			var opts []string
			for oi, o := range f.options {
				if oi == f.choice {
					opts = append(opts, sCyan.Render("(•) "+o))
				} else {
					opts = append(opts, sDim.Render("( ) "+o))
				}
			}
			val = strings.Join(opts, "  ")
			if len(f.options) > 4 {
				val = sCyan.Render("‹ "+f.options[f.choice]+" ›") + sDim.Render("  ←→")
			}
			if len(f.options) == 0 {
				val = sDim.Render("none")
			}
		case fBool:
			val = sDim.Render("[ ]")
			if f.on {
				val = sCyan.Render("[x]")
			}
		case fPick:
			val = sCyan.Render("‹ "+f.value()+" ›") + sDim.Render("  enter picks")
			if len(f.options) == 0 {
				val = sDim.Render("none")
			}
		case fMulti:
			val = sDim.Render("none  · enter picks")
			if len(f.picked) > 0 {
				val = sText.Render(strings.Join(f.picked, ", "))
			}
		}
		if f.hint != "" && i == m.focus {
			val += "  " + sDim.Render(f.hint)
		}
		lines = append(lines, fit(label, labelW)+val)
	}
	lines = append(lines, "")
	if m.footer != nil {
		lines = append(lines, m.footer()...)
	}
	if m.err != "" {
		lines = append(lines, sRed.Render("✗ "+m.err), "")
	}
	lines = append(lines,
		button("▶ "+m.submit, m.onButton(), false)+"  "+button("[esc] Cancel", false, false),
		sDim.Render("tab next field · ←→ choose · space ticks · enter opens a picker · ctrl+s sends"))
	return modalBox(title(m.title, "", -1, ""), lines, cCyan, boxW, boxW)
}

// areas counts the multi-line fields of the form.
func areas(m *formModal) int {
	n := 0
	for _, f := range m.fields {
		if f.kind == fArea {
			n++
		}
	}
	return n
}
