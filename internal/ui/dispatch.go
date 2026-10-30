package ui

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

type dispatchField struct {
	in     gh.DispatchInput
	text   textinput.Model
	choice int
	on     bool
}

type dispatchModal struct {
	repo   string
	wf     gh.Workflow
	fields []*dispatchField // fields[0] is the ref
	focus  int
	err    string
}

func (a *App) startDispatch(repo string, wf gh.Workflow) tea.Cmd {
	if wf.Dynamic() {
		return a.flashCmd(nHint, "This workflow is managed by GitHub and can't be started manually.")
	}
	// Read shared state here, on the UI goroutine, not inside the command.
	last := map[string]string{}
	for k, v := range a.cfg.LastDispatch[fmt.Sprintf("%s/%d", repo, wf.ID)] {
		last[k] = v
	}
	branch := ""
	if r, ok := a.repoByName(repo); ok {
		branch = r.DefaultBranch
	}
	return func() tea.Msg {
		ctx := context.Background()
		if branch == "" {
			if r, err := a.client.GetRepo(ctx, repo); err == nil {
				branch = r.DefaultBranch
			}
		}
		src, err := a.client.FileContent(ctx, repo, wf.Path, branch)
		if err != nil {
			return flashMsg{nError, "Could not read " + wf.Path + ": " + err.Error()}
		}
		ok, inputs, err := gh.ParseDispatchInputs(src)
		if err != nil {
			return flashMsg{nError, "Could not parse " + wf.Path + ": " + err.Error()}
		}
		if !ok {
			return flashMsg{nHint, wf.Name + " has no workflow_dispatch trigger, so it can't be started manually."}
		}
		return modalMsg{newDispatchModal(repo, wf, branch, inputs, last)}
	}
}

func newTextField(value string) textinput.Model {
	t := textinput.New()
	t.Prompt = ""
	t.SetValue(value)
	t.Width = 30
	t.CharLimit = 500
	t.TextStyle = sWhite
	t.Cursor.Style = sCyan
	return t
}

func newDispatchModal(repo string, wf gh.Workflow, branch string, inputs []gh.DispatchInput, last map[string]string) *dispatchModal {
	m := &dispatchModal{repo: repo, wf: wf}
	ref := branch
	if v, ok := last["@ref"]; ok && v != "" {
		ref = v
	}
	m.fields = append(m.fields, &dispatchField{in: gh.DispatchInput{Name: "Use workflow from", Type: "ref", Required: true, Description: "branch or tag"}, text: newTextField(ref)})
	for _, in := range inputs {
		val := in.Default
		if v, ok := last[in.Name]; ok {
			val = v
		}
		f := &dispatchField{in: in}
		switch in.Type {
		case "choice":
			for i, o := range in.Options {
				if o == val {
					f.choice = i
				}
			}
		case "boolean":
			f.on = val == "true"
		default:
			f.text = newTextField(val)
		}
		m.fields = append(m.fields, f)
	}
	m.focusField(0)
	return m
}

func (f *dispatchField) isText() bool { return f.in.Type != "choice" && f.in.Type != "boolean" }

func (f *dispatchField) value() string {
	switch f.in.Type {
	case "choice":
		if f.choice < len(f.in.Options) {
			return f.in.Options[f.choice]
		}
		return ""
	case "boolean":
		if f.on {
			return "true"
		}
		return "false"
	}
	return strings.TrimSpace(f.text.Value())
}

func (m *dispatchModal) focusField(i int) {
	for j, f := range m.fields {
		if f.isText() {
			if j == i {
				f.text.Focus()
				f.text.CursorEnd()
			} else {
				f.text.Blur()
			}
		}
	}
	m.focus = i
}

func (m *dispatchModal) ghCommand() string {
	var b strings.Builder
	fmt.Fprintf(&b, "gh workflow run %s -R %s --ref %s", path.Base(m.wf.Path), m.repo, shellQuote(m.fields[0].value()))
	for _, f := range m.fields[1:] {
		fmt.Fprintf(&b, " -f %s=%s", f.in.Name, shellQuote(f.value()))
	}
	return b.String()
}

// shellQuote quotes a value for the user's shell: PowerShell on Windows,
// a POSIX shell elsewhere.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\|&;<>(){}*?!#~,@%") {
		return s
	}
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// onButton reports whether focus is on the "Run workflow" button after the fields.
func (m *dispatchModal) onButton() bool { return m.focus == len(m.fields) }

func (m *dispatchModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	n := len(m.fields) + 1 // fields + the Run button
	switch msg.String() {
	case "esc":
		return true, nil
	case "tab", "down":
		m.focusField((m.focus + 1) % n)
		return false, nil
	case "shift+tab", "up":
		m.focusField((m.focus - 1 + n) % n)
		return false, nil
	case "ctrl+y":
		return false, a.copyText(m.ghCommand(), "gh command")
	case "ctrl+s":
		return m.submit(a)
	case "enter":
		// Enter only starts the workflow from the Run button; elsewhere it moves on.
		if m.onButton() {
			return m.submit(a)
		}
		m.focusField(m.focus + 1)
		return false, nil
	}
	if m.onButton() {
		return false, nil
	}
	f := m.fields[m.focus]
	switch msg.String() {
	case "left", "right":
		if f.in.Type == "choice" && len(f.in.Options) > 0 {
			d := 1
			if msg.String() == "left" {
				d = -1
			}
			f.choice = (f.choice + d + len(f.in.Options)) % len(f.in.Options)
			return false, nil
		}
	case " ":
		if f.in.Type == "boolean" {
			f.on = !f.on
			return false, nil
		}
		if f.in.Type == "choice" {
			f.choice = (f.choice + 1) % max(len(f.in.Options), 1)
			return false, nil
		}
	}
	if f.isText() {
		var cmd tea.Cmd
		f.text, cmd = f.text.Update(msg)
		m.err = ""
		return false, cmd
	}
	return false, nil
}

func (m *dispatchModal) submit(a *App) (bool, tea.Cmd) {
	inputs := map[string]string{}
	remember := map[string]string{"@ref": m.fields[0].value()}
	for _, f := range m.fields {
		if f.in.Required && f.value() == "" {
			m.err = f.in.Name + " is required"
			return false, nil
		}
	}
	for _, f := range m.fields[1:] {
		inputs[f.in.Name] = f.value()
		remember[f.in.Name] = f.value()
	}
	if a.cfg.LastDispatch == nil {
		a.cfg.LastDispatch = map[string]map[string]string{}
	}
	a.cfg.LastDispatch[fmt.Sprintf("%s/%d", m.repo, m.wf.ID)] = remember
	saveErr := a.cfg.Save()

	ref := m.fields[0].value()
	repo, wf := m.repo, m.wf
	cmds := []tea.Cmd{a.actThen(repo, fmt.Sprintf("Started %s on %s — you'll hear when it starts and finishes", wf.Name, ref), func(ctx context.Context) error {
		return a.client.Dispatch(ctx, repo, wf.ID, ref, inputs)
	}, func() tea.Cmd { return a.awaitDispatched(repo, wf.ID) })}
	if rv, ok := a.top().(*repoView); ok && rv.repo == repo {
		cmds = append(cmds, rv.selectWorkflow(a, wf.ID))
	} else {
		cmds = append(cmds, a.openRepo(repo, "", wf.ID))
	}
	if saveErr != nil {
		cmds = append(cmds, a.flashCmd(nError, "Could not remember these inputs: "+saveErr.Error()))
	}
	return true, tea.Batch(cmds...)
}

func (m *dispatchModal) Render(a *App, w, h int) string {
	labelW := 18
	for _, f := range m.fields {
		labelW = max(labelW, width(f.in.Name)+3)
	}
	var lines []string
	lines = append(lines, sDim.Render(m.wf.Path), "")
	for i, f := range m.fields {
		label := f.in.Name
		if f.in.Required && i > 0 {
			label += sRed.Render(" *")
		}
		ls := sText
		if i == m.focus {
			ls = sCyan.Bold(true)
			label = ls.Render(f.in.Name)
			if f.in.Required && i > 0 {
				label += sRed.Render(" *")
			}
		} else {
			label = ls.Render(f.in.Name) + map[bool]string{true: sRed.Render(" *"), false: ""}[f.in.Required && i > 0]
		}
		var val string
		switch f.in.Type {
		case "choice":
			var opts []string
			for oi, o := range f.in.Options {
				if oi == f.choice {
					opts = append(opts, sCyan.Render("(•) "+o))
				} else {
					opts = append(opts, sDim.Render("( ) "+o))
				}
			}
			val = strings.Join(opts, "  ")
		case "boolean":
			if f.on {
				val = sCyan.Render("[x]")
			} else {
				val = sDim.Render("[ ]")
			}
		default:
			box := f.text.View()
			if i == m.focus {
				val = sDim.Render("[") + fit(box, 32) + sDim.Render("]")
			} else {
				val = sDim.Render("[") + fit(sWhite.Render(f.text.Value()), 32) + sDim.Render("]")
			}
		}
		desc := ""
		if f.in.Description != "" {
			desc = "  " + sDim.Render(clip(f.in.Description, 40))
		}
		lines = append(lines, fit(label, labelW)+val+desc)
	}
	lines = append(lines, "")
	if m.err != "" {
		lines = append(lines, sRed.Render("✗ "+m.err), "")
	}
	lines = append(lines,
		button("▶ Run workflow", m.onButton(), false)+"  "+button("[esc] Cancel", false, false),
		sDim.Render("enter/tab next field · ←→ choose · space toggle · enter on ▶ (or ctrl+s) runs · ctrl+y copy gh command"),
		"",
		sDim.Render("equivalent: ")+sBlue.Render(clip(m.ghCommand(), w-24)),
	)
	return modalBox(title("Run workflow", m.wf.Name, -1, ""), lines, cCyan, 70, w-4)
}
