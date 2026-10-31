package ui

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// releasesView lists the releases of a repo: tag, name, state, who and when,
// with the notes and the files of the one under the cursor in a panel.
type releasesView struct {
	id   int
	repo string
	t    *Table
	rels []gh.Release
	// since counts the commits of the default branch after the latest release.
	since    int
	sinceTag string
	loadState
}

type releasesMsg struct {
	id    int
	rels  []gh.Release
	since int
	tag   string
	err   error
}

func (m releasesMsg) viewID() int { return m.id }

func newReleasesView(a *App, repo string) *releasesView {
	v := &releasesView{id: a.newID(), repo: repo, since: -1}
	v.t = newTable(
		Column{Title: "TAG", Max: 24, Flex: true, MidCut: true},
		Column{Title: "NAME", Max: 50, Flex: true, Drop: 1},
		Column{Title: "STATE", Help: "published, draft or pre-release"},
		Column{Title: "WHO", Max: 18, Flex: true, Drop: 2},
		Column{Title: "PUBLISHED", Right: true, Min: 3},
		Column{Title: "FILES", Right: true, Help: "the files attached to it; A downloads one"},
	)
	v.t.Empty = "Loading releases…"
	var rs []gh.Release
	if at, ok := a.client.Recall(v.snapshot(), &rs); ok {
		v.rels, v.at = rs, at
	}
	return v
}

func (v *releasesView) snapshot() string { return "releases:" + v.repo }

func (v *releasesView) ID() int                  { return v.id }
func (v *releasesView) Crumb() string            { return "releases" }
func (v *releasesView) Init(a *App) tea.Cmd      { return nil }
func (v *releasesView) Updated(a *App) time.Time { return v.at }
func (v *releasesView) Loading(a *App) bool      { return v.busy }

func (v *releasesView) defaultBranch(a *App) string {
	if r, ok := a.repoByName(v.repo); ok && r.DefaultBranch != "" {
		return r.DefaultBranch
	}
	return "main"
}

// latest is the newest published release.
func (v *releasesView) latest() (gh.Release, bool) {
	for _, r := range v.rels {
		if !r.Draft && !r.Prerelease {
			return r, true
		}
	}
	return gh.Release{}, false
}

func (v *releasesView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(time.Minute) {
		return nil
	}
	if !v.begin() {
		return nil
	}
	id, repo, key, def := v.id, v.repo, v.snapshot(), v.defaultBranch(a)
	return func() tea.Msg {
		ctx := context.Background()
		rs, err := a.client.ListReleases(ctx, repo)
		if err != nil {
			return releasesMsg{id: id, err: err}
		}
		a.client.Remember(key, rs)
		msg := releasesMsg{id: id, rels: rs, since: -1}
		for _, r := range rs {
			if !r.Draft && !r.Prerelease {
				if n, err := a.client.CommitsSince(ctx, repo, r.Tag, def); err == nil {
					msg.since, msg.tag = n, r.Tag
				}
				break
			}
		}
		return msg
	}
}

func (v *releasesView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case releasesMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the releases of "+v.repo+": "+msg.err.Error())
		}
		v.rels, v.since, v.sinceTag = msg.rels, msg.since, msg.tag
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *releasesView) selected() (gh.Release, bool) {
	r := v.t.Selected()
	if r == nil {
		return gh.Release{}, false
	}
	for _, x := range v.rels {
		if strconv.FormatInt(x.ID, 10) == r.Key {
			return x, true
		}
	}
	return gh.Release{}, false
}

// MenuTitle names the selected release for the action menu.
func (v *releasesView) MenuTitle(a *App) string {
	if r, ok := v.selected(); ok {
		return r.Tag
	}
	return ""
}

func (v *releasesView) Keys(a *App) []Binding {
	var bs []Binding
	if r, ok := v.selected(); ok {
		bs = append(bs,
			Binding{Keys: []string{"P"}, Desc: "publish", Help: "publish the draft release", Off: !r.Draft, Mutates: true, Need: gh.CapWrite, Repo: v.repo,
				Do: do(func() tea.Cmd {
					return confirm("Publish the release?", []string{sWhite.Render(r.Tag) + sDim.Render(" · "+r.Name), "", "Everyone who can see the repo will see it."}, "Publish", false, func() tea.Cmd {
						return a.act(v.repo, "Published "+r.Tag, func(ctx context.Context) error { return a.client.PublishRelease(ctx, v.repo, r.ID) })
					})
				})},
			Binding{Keys: []string{"X"}, Desc: "delete draft", Help: "delete the draft release", Off: !r.Draft, Mutates: true, Need: gh.CapWrite, Repo: v.repo,
				Do: do(func() tea.Cmd {
					return confirm("Delete the draft?", []string{sWhite.Render(r.Tag) + sDim.Render(" · "+r.Name), "", "The draft is gone for good."}, "Delete", true, func() tea.Cmd {
						return a.act(v.repo, "Deleted the draft "+r.Tag, func(ctx context.Context) error { return a.client.DeleteRelease(ctx, v.repo, r.ID) })
					})
				})},
			Binding{Help: "download an asset…", Off: len(r.Assets) == 0, Need: gh.CapRead, Repo: v.repo,
				Do: do(func() tea.Cmd { return v.downloadAsset(a, r) })},
			Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(r.URL) })},
			Binding{Keys: []string{"y"}, Desc: "copy link", Help: "copy the link", Do: do(func() tea.Cmd { return a.copyText(r.URL, "release link") })},
		)
	}
	bs = append(bs, Binding{Keys: []string{"+"}, Desc: "new release", Help: "write a new release", Mutates: true, Need: gh.CapWrite, Repo: v.repo, Do: do(func() tea.Cmd { return a.newRelease(v) })})
	bs = append(bs, filterHint, v.t.moveKeys())
	return grouped("RELEASES", bs...)
}

func (v *releasesView) downloadAsset(a *App, r gh.Release) tea.Cmd {
	start := func(x gh.ReleaseAsset) tea.Cmd {
		return confirm("Download the asset?", []string{sWhite.Render(x.Name) + sDim.Render(" · "+bytesText(x.Size)), "", "It will be saved in " + downloadsDir() + "."}, "Download", false, func() tea.Cmd {
			return a.downloadFile(unsafeName.ReplaceAllString(x.Name, "_"), x.Size, func(ctx context.Context, w io.Writer, progress func(done, total int64)) error {
				return a.client.DownloadReleaseAsset(ctx, v.repo, x.ID, w, progress)
			})
		})
	}
	if len(r.Assets) == 1 {
		return start(r.Assets[0])
	}
	byID := map[string]gh.ReleaseAsset{}
	var items []pickItem
	for _, x := range r.Assets {
		id := strconv.FormatInt(x.ID, 10)
		byID[id] = x
		items = append(items, pickItem{value: id, label: x.Name, note: sDim.Render(bytesText(x.Size) + fmt.Sprintf(" · %d downloads", x.Downloads))})
	}
	return openModal(newPicker("asset", "Download which asset?", "", items, func(id string) tea.Cmd { return start(byID[id]) }))
}

func releaseState(r gh.Release) Cell {
	switch {
	case r.Draft:
		return Cell{Text: "draft", Styled: sDim.Render("draft")}
	case r.Prerelease:
		return tintCell(KRun, "◐ pre-release")
	}
	return Cell{Text: " ", Styled: " "}
}

func (v *releasesView) rebuild(a *App) {
	now := time.Now()
	latestID := int64(0)
	if l, ok := v.latest(); ok {
		latestID = l.ID
	}
	var rows []Row
	for _, r := range v.rels {
		state := releaseState(r)
		if r.ID == latestID {
			state = tintCell(KOK, "★ latest")
		}
		when := r.PublishedAt
		if when.IsZero() {
			when = r.CreatedAt
		}
		name := r.Name
		if name == "" {
			name = r.Tag
		}
		rows = append(rows, Row{Key: strconv.FormatInt(r.ID, 10), Kind: KOK, Cells: []Cell{
			{Text: icon("tag") + r.Tag, Styled: ico("tag") + sGold.Render(r.Tag)}, titleCell(name, r.Draft), state,
			iconText("user", r.Author.Login, false), steelCell(age(when, now), false), steelCell(fmt.Sprint(len(r.Assets)), false)}})
	}
	v.t.Empty = "No releases yet."
	if v.busy && len(rows) == 0 {
		v.t.Empty = "Loading releases…"
	}
	v.t.SetRows(rows)
}

func (v *releasesView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *releasesView) FilterValue() string          { return v.t.Filter() }
func (v *releasesView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *releasesView) sync(a *App)                  { v.rebuild(a) }

func (v *releasesView) Render(a *App, w, h int) string {
	var chips []string
	if v.since > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d commits since %s", v.since, v.sinceTag)))
	}
	drafts := 0
	for _, r := range v.rels {
		if r.Draft {
			drafts++
		}
	}
	if drafts > 0 {
		chips = append(chips, sDim.Render(fmt.Sprintf("%d draft", drafts)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("tag", "Releases", "", v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

func (v *releasesView) panel(a *App, w, h int) []string {
	r, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "No release selected.")
	}
	now := time.Now()
	p := newPanel(w)
	p.add(ico("tag") + sGold.Bold(true).Render(r.Tag))
	if r.Name != "" && r.Name != r.Tag {
		p.wrap(r.Name, sText, 2)
	}
	when := r.PublishedAt
	if when.IsZero() {
		when = r.CreatedAt
	}
	p.add(fact("user", r.Author.Login) + sDim.Render("  ·  ") + fact("clock", age(when, now)+" ago"))
	if r.Draft {
		p.add(sAmber.Render("a draft") + sDim.Render(" · ") + keycap("P") + sDim.Render(" publishes it"))
	}
	if strings.TrimSpace(r.Body) != "" {
		p.label("Release notes")
		for i, l := range renderMarkdown(r.Body, max(w-4, 10)) {
			if i == 9 {
				p.add(sDim.Render("…"))
				break
			}
			p.add(l)
		}
	}
	if len(r.Assets) > 0 {
		p.label(fmt.Sprintf("Assets (%d)", len(r.Assets)))
		for _, x := range r.Assets[:min(len(r.Assets), 6)] {
			p.row(sText.Render(fit(x.Name, max(w-22, 8))), sDim.Render(bytesText(x.Size)+fmt.Sprintf(" · %d↓", x.Downloads)))
		}
	}
	var runs []gh.Run
	for _, run := range a.st.status[v.repo] {
		if run.HeadBranch == r.Tag && len(runs) < 3 {
			runs = append(runs, run)
		}
	}
	if len(runs) > 0 {
		p.label("Checks on this tag")
		for _, run := range runs {
			k := StatusKind(run.Status, run.Conclusion)
			p.row(glyph(k, a.frame)+" "+sText.Render(fmt.Sprintf("%s #%d", run.Name, run.RunNumber)), sDim.Render(age(run.CreatedAt, now)))
		}
	}
	return p.render(h)
}

// releaseWorkflow names a workflow of the repo that looks like it publishes releases, or "".
func (a *App) releaseWorkflow(repo string) string {
	for _, w := range a.workflows[repo] {
		if strings.Contains(strings.ToLower(w.Name+" "+w.Path), "release") {
			return w.Name
		}
	}
	return ""
}

// newRelease opens the form for a new release: the next version, the
// target, a title and notes (ctrl+g has GitHub write them), a draft and a
// pre-release switch.
func (a *App) newRelease(v *releasesView) tea.Cmd {
	repo := v.repo
	latestTag := ""
	if l, ok := v.latest(); ok {
		latestTag = l.Tag
	}
	branches := []string{v.defaultBranch(a)}
	patch, minor, major := gh.NextVersions(latestTag)
	var bumps []string
	for _, b := range []struct{ name, tag string }{{"patch", patch}, {"minor", minor}, {"major", major}} {
		if b.tag != "" {
			bumps = append(bumps, b.name+" "+b.tag)
		}
	}
	bumps = append(bumps, "custom")
	// minor first, as the likely choice: patch, minor, major order with minor selected.
	bumpF := newChoiceFormField("Version", bumps, 0)
	if len(bumps) > 2 {
		bumpF.choice = 1
	}
	tagF := newTextFormField("Tag", "", true)
	targetF := &formField{kind: fPick, label: "Target", options: branches}
	nameF := newTextFormField("Title", "", true)
	notesF := newAreaFormField("Notes", "", 6)
	draftF := &formField{kind: fBool, label: "Draft", on: true}
	preF := &formField{kind: fBool, label: "Pre-release"}
	setTag := func() {
		if strings.HasPrefix(bumpF.value(), "custom") {
			return
		}
		_, tag, _ := strings.Cut(bumpF.value(), " ")
		if nameF.value() == "" || nameF.value() == tagF.value() {
			nameF.text.SetValue(tag)
		}
		tagF.text.SetValue(tag)
	}
	setTag()
	m := &formModal{title: "New release", submit: "Create release", fields: []*formField{bumpF, tagF, targetF, nameF, notesF, draftF, preF}}
	if latestTag != "" {
		m.lines = []string{sDim.Render("after " + latestTag)}
	}
	m.onChange = func(a *App, m *formModal, i int) tea.Cmd {
		if m.fields[i] == bumpF {
			setTag()
		}
		return nil
	}
	m.footer = func() []string {
		if wf := a.releaseWorkflow(repo); wf != "" {
			return []string{sDim.Render(fmt.Sprintf("%s publishes releases from CI: %s", a.short(repo), wf))}
		}
		return nil
	}
	m.keys = map[string]func(a *App, m *formModal) tea.Cmd{
		"ctrl+g": func(a *App, m *formModal) tea.Cmd {
			tag, target := tagF.value(), targetF.value()
			if tag == "" {
				m.err = "Tag is required to generate notes"
				return nil
			}
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				name, body, err := a.client.GenerateReleaseNotes(ctx, repo, tag, target, latestTag)
				return formAsyncMsg{func() {
					if err != nil {
						m.err = "Could not generate the notes: " + err.Error()
						return
					}
					notesF.area.SetValue(body)
					if nameF.value() == "" || nameF.value() == tag {
						nameF.text.SetValue(name)
					}
				}}
			}
		},
	}
	m.onSubmit = func(a *App, m *formModal) tea.Cmd {
		spec := gh.ReleaseSpec{Tag: tagF.value(), Target: targetF.value(), Name: nameF.value(), Body: notesF.value(), Draft: draftF.on, Prerelease: preF.on}
		what := "Published "
		if spec.Draft {
			what = "Created the draft "
		}
		return a.act(repo, what+spec.Tag, func(ctx context.Context) error {
			_, err := a.client.CreateRelease(ctx, repo, spec)
			return err
		})
	}
	m.lines = append(m.lines, sDim.Render("ctrl+g writes the notes from the changes since the last release"))
	m.focusField(1)
	return openModal(m)
}
