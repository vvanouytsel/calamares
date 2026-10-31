package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// pagerView shows a workflow file: the definition of one job, or the whole
// file with that job marked.
type pagerView struct {
	id       int
	repo     string
	path     string
	ref      string       // "" is the default branch
	wf       *gh.Workflow // set when it can be dispatched from here
	label    string       // the job on display, if any
	from, to int          // the job's lines, from 1; 0 when the whole file is shown
	jobOnly  bool
	lines    []string
	err      error
	offset   int
	lastH    int
}

type fileMsg struct {
	id   int
	text string
	err  error
}

func (m fileMsg) viewID() int { return m.id }

func newPagerView(a *App, repo string, wf gh.Workflow) *pagerView {
	ref := ""
	if r, ok := a.repoByName(repo); ok {
		ref = r.DefaultBranch
	}
	return &pagerView{id: a.newID(), repo: repo, path: wf.Path, ref: ref, wf: &wf}
}

// newJobSourceView shows how a job is defined, as its workflow file was when
// the run started; a toggles the whole file.
func newJobSourceView(a *App, job gh.PlannedJob, label string) *pagerView {
	v := &pagerView{id: a.newID(), repo: job.File.Repo, path: job.File.Path, ref: job.File.Ref,
		label: label, from: job.Line, to: job.EndLine, jobOnly: true}
	v.setText(job.File.Text)
	return v
}

// newWorkflowSourceView shows a whole workflow file at a ref.
func newWorkflowSourceView(a *App, repo, path, ref string) *pagerView {
	return &pagerView{id: a.newID(), repo: repo, path: path, ref: ref}
}

func (v *pagerView) setText(text string) {
	v.lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func (v *pagerView) ID() int                            { return v.id }
func (v *pagerView) RepoName() string                   { return v.repo }
func (v *pagerView) Crumb() string                      { return "yaml" }
func (v *pagerView) Refresh(a *App, force bool) tea.Cmd { return nil }
func (v *pagerView) Keys(a *App) []Binding {
	scroll := moveBinding("scroll", func(k string) tea.Cmd { v.scroll(k); return nil })
	scroll.Keys = append(append([]string{}, scroll.Keys...), " ")
	bs := []Binding{scroll}
	if v.from > 0 {
		bs = append(bs, Binding{Keys: []string{"v"}, Desc: map[bool]string{true: "whole file", false: "only this job"}[v.jobOnly],
			Help: "whole file / only this job", Do: do(func() tea.Cmd {
				v.jobOnly = !v.jobOnly
				v.offset = 0
				if !v.jobOnly {
					v.offset = max(v.from-4, 0)
				}
				v.clamp()
				return nil
			})})
	}
	if v.wf != nil {
		bs = append(bs, Binding{Keys: []string{"+"}, Desc: "run workflow", Help: "start a new run of the workflow", Mutates: true, Need: gh.CapWrite, Repo: v.repo,
			Do: do(func() tea.Cmd { return a.startDispatch(v.repo, *v.wf) })})
	}
	bs = append(bs,
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(v.webURL(a)) })},
		Binding{Keys: []string{"y"}, Desc: "copy yaml", Help: "copy the YAML on display", Do: do(func() tea.Cmd {
			if v.lines == nil {
				return nil
			}
			first, last := v.shown()
			return a.copyText(strings.Join(v.lines[first-1:last], "\n"), "YAML")
		})},
	)
	return grouped("YAML", bs...)
}

// scroll moves the text by a line, a page, or to an end.
func (v *pagerView) scroll(k string) {
	page := max(v.lastH-1, 1)
	_, last := v.shown()
	switch k {
	case "up", "k":
		v.offset--
	case "down", "j":
		v.offset++
	case "pgup", "ctrl+b":
		v.offset -= page
	case "pgdown", "ctrl+f", " ":
		v.offset += page
	case "home":
		v.offset = 0
	case "end":
		v.offset = last
	}
	v.clamp()
}

// clamp keeps the last line of the text at the bottom of the screen at most.
func (v *pagerView) clamp() {
	first, last := v.shown()
	v.offset = max(min(v.offset, last-first+1-v.lastH), 0)
}

func (v *pagerView) Init(a *App) tea.Cmd {
	if v.lines != nil {
		return nil
	}
	id, repo, path, ref := v.id, v.repo, v.path, v.ref
	return func() tea.Msg {
		text, err := a.client.FileContent(context.Background(), repo, path, ref)
		return fileMsg{id, text, err}
	}
}

// shown are the line numbers (from 1) on display: the job's, or all.
func (v *pagerView) shown() (first, last int) {
	if v.jobOnly && v.from > 0 {
		return v.from, min(v.to, len(v.lines))
	}
	return 1, len(v.lines)
}

func (v *pagerView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case fileMsg:
		v.err = msg.err
		v.setText(msg.text)
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *pagerView) webURL(a *App) string {
	ref := v.ref
	if ref == "" {
		ref = "HEAD"
	}
	u := a.client.WebURL(v.repo + "/blob/" + ref + "/" + v.path)
	if v.from > 0 {
		u += fmt.Sprintf("#L%d-L%d", v.from, v.to)
	}
	return u
}

var yamlKey = regexp.MustCompile(`^(\s*-?\s*)([A-Za-z0-9_.\-/${}\[\] ]+?)(:)(\s|$)(.*)$`)

func highlightYAML(l string) string {
	trim := strings.TrimSpace(l)
	if strings.HasPrefix(trim, "#") {
		return sDim.Render(l)
	}
	if m := yamlKey.FindStringSubmatch(l); m != nil {
		val := m[5]
		vs := sText
		if strings.HasPrefix(strings.TrimSpace(val), "#") {
			vs = sDim
		} else if strings.Contains(val, "${{") {
			vs = sTan
		}
		return sDim.Render(m[1]) + sCyan.Render(m[2]) + sDim.Render(m[3]) + m[4] + vs.Render(val)
	}
	if strings.HasPrefix(trim, "- ") {
		i := strings.Index(l, "- ")
		return l[:i] + sDim.Render("- ") + sText.Render(l[i+2:])
	}
	return sText.Render(l)
}

func (v *pagerView) Render(a *App, w, h int) string {
	v.lastH = h - 2
	var body []string
	switch {
	case v.err != nil:
		body = []string{" " + sRed.Render("Could not load file: "+v.err.Error())}
	case v.lines == nil:
		body = []string{" " + sDim.Render("Loading…")}
	default:
		first, last := v.shown()
		for n := first + v.offset; n <= last && len(body) < h-2; n++ {
			mark := " "
			if !v.jobOnly && n >= v.from && n <= v.to && v.from > 0 {
				mark = sGold.Render("▌")
			}
			body = append(body, mark+sDim.Render(fmt.Sprintf("%4d ", n))+highlightYAML(strings.ReplaceAll(v.lines[n-1], "\t", "  ")))
		}
	}
	where := v.repo + " › " + v.path
	if v.ref != "" {
		where += " @ " + shortRef(v.ref)
	}
	name := "YAML"
	if v.label != "" {
		name = v.label
	}
	extra := ""
	if v.from > 0 {
		extra = fmt.Sprintf("lines %d–%d", v.from, v.to)
	}
	return box(title(name, where, -1, extra), strings.Join(body, "\n"), w, h, cFrame)
}

// shortRef abbreviates a commit SHA; branch and tag names stay as they are.
func shortRef(ref string) string {
	if len(ref) == 40 && strings.Trim(ref, "0123456789abcdef") == "" {
		return ref[:7]
	}
	return ref
}
