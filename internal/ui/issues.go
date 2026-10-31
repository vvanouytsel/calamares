package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// issuesView lists issues: the open ones that concern you (the Issues
// place, in three views), or the open ones of one repo (a section of the
// repo screen).
type issuesView struct {
	id   int
	repo string // "" for your own list
	t    *Table
	view int // your own list: 0 assigned, 1 created, 2 mentioned
	// A repo's list is kept here; your own lives in the store.
	items   []gh.Issue
	enabled bool
	loadState
}

var issueViews = []string{"Assigned", "Created", "Mentioned"}

// issuesLiveEvery is how often the list refreshes while live.
const issuesLiveEvery = time.Minute

type repoIssuesMsg struct {
	id      int
	issues  []gh.Issue
	enabled bool
	err     error
}

func (m repoIssuesMsg) viewID() int { return m.id }

func newIssuesView(a *App, repo string) *issuesView {
	v := &issuesView{id: a.newID(), repo: repo, enabled: true}
	var cols []Column
	if repo == "" {
		cols = append(cols, Column{Title: "ROLE", Help: "why it concerns you: someone asked you something (asked), it is assigned to you, you wrote it (author), or you were mentioned"}, Column{Title: "REPO", Max: 24, Flex: true, MidCut: true, Help: "the repository"})
	}
	cols = append(cols, Column{Title: "#", Right: true, Help: "its number"}, Column{Title: "TITLE", Max: 60, Flex: true, Below: true})
	if repo != "" {
		cols = append(cols, Column{Title: "AUTHOR", Max: 20, Flex: true, Drop: 2, Help: "who opened it"}, Column{Title: "LABELS", Max: 30, Flex: true, Drop: 1, Help: "its labels"})
	}
	cols = append(cols, Column{Title: "AGE", Right: true, Min: 3, Help: "how long ago it last changed"})
	v.t = newTable(cols...)
	v.t.Empty = "Loading issues…"
	if repo != "" {
		var is []gh.Issue
		if at, ok := a.client.Recall(v.snapshot(), &is); ok {
			v.items, v.at = is, at
		}
	}
	return v
}

func (v *issuesView) snapshot() string { return "issues:" + v.repo }

func (v *issuesView) ID() int { return v.id }
func (v *issuesView) Crumb() string {
	if v.repo != "" {
		return "issues"
	}
	return "Issues"
}
func (v *issuesView) Init(a *App) tea.Cmd { return nil }

// issueKey tells issues apart.
func issueKey(i gh.Issue) string { return fmt.Sprintf("%s#%d", i.Repo, i.Number) }

func (v *issuesView) list(a *App) []gh.Issue {
	if v.repo != "" {
		return v.items
	}
	switch v.view {
	case 1:
		return a.st.issues.Created
	case 2:
		return a.st.issues.Mentioned
	}
	return a.st.issues.Assigned
}

func (v *issuesView) Updated(a *App) time.Time {
	if v.repo == "" {
		return a.st.issuesState.at
	}
	return v.at
}

func (v *issuesView) Loading(a *App) bool {
	if v.repo == "" {
		return a.st.issuesState.busy
	}
	return v.busy
}

func (v *issuesView) Refresh(a *App, force bool) tea.Cmd {
	state := &v.loadState
	if v.repo == "" {
		state = &a.st.issuesState
	}
	if !force && !state.waited(issuesLiveEvery) {
		return nil
	}
	if v.repo == "" {
		return a.st.loadIssues(a)
	}
	if !v.begin() {
		return nil
	}
	id, repo, key := v.id, v.repo, v.snapshot()
	return func() tea.Msg {
		is, enabled, err := a.client.RepoIssues(context.Background(), repo)
		if err == nil {
			a.client.Remember(key, is)
		}
		return repoIssuesMsg{id, is, enabled, err}
	}
}

func (v *issuesView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case repoIssuesMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the issues of "+v.repo+": "+msg.err.Error())
		}
		v.items, v.enabled = msg.issues, msg.enabled
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *issuesView) selected(a *App) (gh.Issue, bool) {
	r := v.t.Selected()
	if r == nil {
		return gh.Issue{}, false
	}
	for _, i := range v.list(a) {
		if issueKey(i) == r.Key {
			return i, true
		}
	}
	return gh.Issue{}, false
}

// MenuTitle names the selected issue for the action menu.
func (v *issuesView) MenuTitle(a *App) string {
	if i, ok := v.selected(a); ok {
		return fmt.Sprintf("%s #%d", a.short(i.Repo), i.Number)
	}
	return ""
}

func (v *issuesView) Keys(a *App) []Binding {
	var bs []Binding
	if i, ok := v.selected(a); ok {
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Do: do(func() tea.Cmd { return a.push(newIssueView(a, i.Repo, i.Number)) })})
		bs = append(bs, a.issueKeys(i)...)
		bs = append(bs, a.issueAsideBinding(i))
	}
	newIn := v.repo
	if i, ok := v.selected(a); ok && newIn == "" {
		newIn = i.Repo
	}
	bs = append(bs, a.newIssueKey(newIn))
	if v.repo == "" {
		bs = append(bs, viewKeys("next view", "next view", "ISSUES", func(d int) tea.Cmd {
			v.view = (v.view + d + len(issueViews)) % len(issueViews)
			return nil
		}))
	}
	bs = append(bs, filterHint, a.twoLineKey(), v.t.moveKeys())
	return grouped("ISSUES", bs...)
}

// newIssueKey is the key that opens a new issue, in repo or in the one you pick.
func (a *App) newIssueKey(repo string) Binding {
	return Binding{Keys: []string{"+"}, Desc: "new issue", Help: "write a new issue", Mutates: true, Need: gh.CapCreateIssue, Repo: repo,
		Do: do(func() tea.Cmd { return a.newIssue(repo) })}
}

// issueKeys are the keys that act on an issue, shared by the lists and the
// issue screen. Each says why it is locked when GitHub says you may not.
func (a *App) issueKeys(i gh.Issue) []Binding {
	repo, n := i.Repo, i.Number
	label := fmt.Sprintf("%s #%d", a.short(repo), n)
	about := []string{sWhite.Render(label) + sDim.Render(" · "+i.Title)}
	closeDesc, closeLocked := "close", lockIf(i.CanClose, "you can't close this issue")
	if i.State == "CLOSED" {
		closeDesc, closeLocked = "reopen", lockIf(i.CanReopen, "you can't reopen this issue")
	}
	var current []string
	for _, l := range i.Labels {
		current = append(current, l.Name)
	}
	bs := []Binding{
		{Keys: []string{"W"}, Desc: "comment", Help: "write a comment", Mutates: true, Need: gh.CapComment, Repo: repo, Do: do(func() tea.Cmd {
			return askEditor("Comment", about, "your comment", "", "posts it", true, func(text string) tea.Cmd {
				return a.act(repo, "Commented on "+label, func(ctx context.Context) error {
					return a.client.CommentOnPullRequest(ctx, repo, n, text)
				})
			})
		})},
		{Keys: []string{"X"}, Desc: closeDesc, Help: closeDesc + " the issue", Mutates: true, Need: gh.CapTriage, Repo: repo, Locked: closeLocked,
			Do: do(func() tea.Cmd { return a.closeIssue(i) })},
		{Keys: []string{"T"}, Desc: "labels", Help: "add or remove labels", Mutates: true, Need: gh.CapTriage, Repo: repo,
			Locked: lockIf(i.CanLabel || i.CanUpdate, "you can't label this issue"),
			Do:     do(func() tea.Cmd { return a.pickLabelsOf(repo, n, current) })},
		{Keys: []string{"@"}, Desc: "assignees", Help: "add or remove assignees", Mutates: true, Need: gh.CapTriage, Repo: repo,
			Locked: lockIf(i.CanAssign, "you can't assign people here"),
			Do:     do(func() tea.Cmd { return a.pickAssigneesOf(repo, n, append([]string(nil), i.Assignees...)) })},
	}
	if len(i.Linked) > 0 {
		p := i.Linked[0]
		bs = append(bs, Binding{Keys: []string{"#"}, Desc: fmt.Sprintf("pull request #%d", p.Number), Help: fmt.Sprintf("open the linked pull request #%d", p.Number),
			Do: do(func() tea.Cmd { return a.push(newPRView(a, p.Repo, p.Number)) })})
	}
	bs = append(bs,
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(i.URL) })},
		Binding{Keys: []string{"y"}, Help: "copy the URL", Do: do(func() tea.Cmd { return a.copyText(i.URL, "issue URL") })},
	)
	return grouped("AN ISSUE", bs...)
}

// closeIssue asks how to close an issue (or to reopen one), then does it.
func (a *App) closeIssue(i gh.Issue) tea.Cmd {
	repo, n := i.Repo, i.Number
	label := fmt.Sprintf("%s #%d", a.short(repo), n)
	about := []string{sWhite.Render(label) + sDim.Render(" · "+i.Title)}
	if i.State == "CLOSED" {
		return confirm("Reopen issue?", append(about, "", "It will be open again."), "Reopen", false, func() tea.Cmd {
			return a.act(repo, "Reopened "+label, func(ctx context.Context) error { return a.client.ReopenIssue(ctx, repo, n) })
		})
	}
	items := []pickItem{
		{value: "completed", label: "completed", note: sDim.Render("it was done or fixed")},
		{value: "not_planned", label: "not planned", note: sDim.Render("won't be done")},
	}
	return openModal(newPicker("close-issue", "Close "+label, "", items, func(reason string) tea.Cmd {
		return askEditor("Close "+label+" as "+strings.ReplaceAll(reason, "_", " "), about, "a comment, if you like (optional)", "", "closes it", false, func(text string) tea.Cmd {
			return a.act(repo, "Closed "+label+" as "+strings.ReplaceAll(reason, "_", " "), func(ctx context.Context) error {
				if text != "" {
					if err := a.client.CommentOnPullRequest(ctx, repo, n, text); err != nil {
						return err
					}
				}
				return a.client.CloseIssue(ctx, repo, n, reason)
			})
		})
	}))
}

// roleCell says how an issue concerns you; one that asked you something
// but was set aside is shown quietly.
func roleCell(why string, aside bool) Cell {
	switch {
	case why == "asked" && aside:
		return iconCell("at", "asked", sDim, cDim)
	case why == "asked":
		return iconCell("at", "asked", sWait, cWait)
	case why == "assigned":
		return iconCell("user", "assigned", sWhite, cTeal)
	case why == "author":
		return iconCell("pencil", "author", sText, cSteel)
	}
	return iconCell("at", "mention", sDim, cDim)
}

// labelsCell lists labels, each name in its own colour behind a dot.
func labelsCell(ls []gh.Label) Cell {
	var plain, styled []string
	for _, l := range ls {
		c := "#" + strings.TrimPrefix(l.Color, "#")
		plain = append(plain, "● "+l.Name)
		styled = append(styled, fg(lipgloss.Color(c)).Render("●")+" "+sText.Render(l.Name))
	}
	return Cell{Text: strings.Join(plain, " "), Styled: strings.Join(styled, " ")}
}

func labelsText(ls []gh.Label) string {
	var names []string
	for _, l := range ls {
		names = append(names, l.Name)
	}
	return strings.Join(names, " ")
}

func (v *issuesView) rebuild(a *App) {
	now := time.Now()
	var rows []Row
	for _, i := range v.list(a) {
		var cells []Cell
		if v.repo == "" {
			cells = append(cells, roleCell(i.Why, a.issueAside(i)), iconText("repo", a.short(i.Repo), false))
		}
		num := fmt.Sprintf("#%d", i.Number)
		cells = append(cells, Cell{Text: num, Styled: sGold.Render(num)}, Cell{Text: icon("issue") + i.Title, Styled: issueIcon(i) + sWhite.Render(i.Title)})
		if v.repo != "" {
			cells = append(cells, iconCell("user", i.Author, sText, cTeal), labelsCell(i.Labels))
		}
		cells = append(cells, steelCell(age(i.UpdatedAt, now), false))
		rows = append(rows, Row{Key: issueKey(i), Kind: KOK, Cells: cells})
	}
	v.t.Empty = "Nothing is waiting for your attention."
	if v.repo != "" {
		v.t.Empty = "No open issues in this repo."
		if !v.enabled {
			v.t.Empty = "This repo has issues turned off."
		}
	}
	loadErr := v.err
	if v.repo == "" {
		loadErr = a.st.issuesState.err
	}
	switch {
	case v.Loading(a) && len(rows) == 0:
		v.t.Empty = "Loading issues…"
	case loadErr != nil && len(rows) == 0:
		v.t.Empty = "Could not load issues: " + loadErr.Error()
	}
	v.t.SetRows(rows)
}

func (v *issuesView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *issuesView) FilterValue() string          { return v.t.Filter() }
func (v *issuesView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *issuesView) sync(a *App)                  { v.rebuild(a) }

// allIssues lists each issue of the three views once.
func (s *store) allIssues() []gh.Issue {
	var out []gh.Issue
	seen := map[string]bool{}
	for _, l := range [][]gh.Issue{s.issues.Assigned, s.issues.Created, s.issues.Mentioned} {
		for _, i := range l {
			if k := issueKey(i); !seen[k] {
				seen[k] = true
				out = append(out, i)
			}
		}
	}
	return out
}

func (v *issuesView) Render(a *App, w, h int) string {
	v.t.TwoLine = a.twoLine
	if v.repo != "" {
		return " " + a.filterLine(v.t.Filter(), v.FilterInfo(a)) + "\n" + v.t.Render(w-2, h-3)
	}
	bugs, wait, withPR := 0, 0, 0
	for _, i := range a.st.allIssues() {
		for _, l := range i.Labels {
			if strings.Contains(strings.ToLower(l.Name), "bug") {
				bugs++
				break
			}
		}
		if a.issueWaits(i) {
			wait++
		}
		if len(i.Linked) > 0 {
			withPR++
		}
	}
	var chips []string
	if bugs > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d %s", bugs, plural("bug", bugs))))
	}
	if wait > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d awaiting your reply", wait)))
	}
	if withPR > 0 {
		chips = append(chips, icoIn("pr", cGreen)+sText.Render(fmt.Sprintf("%d have a PR", withPR)))
	}
	items := make([]stripItem, len(issueViews))
	for k, name := range issueViews {
		n := 0
		switch k {
		case 0:
			n = len(a.st.issues.Assigned)
		case 1:
			n = len(a.st.issues.Created)
		default:
			n = len(a.st.issues.Mentioned)
		}
		items[k] = stripItem{Label: name, Icon: []string{"user", "pencil", "at"}[k], Count: countMore(n, a.st.issues.More[k])}
		if w := a.issuesWaitingIn(k); w > 0 {
			items[k].St = &sWait
		}
	}
	return listWithPanel(a, w, h,
		[]string{screenTitleMore("issue", "Issues", "yours across your repos", v.t.Len(), a.st.issues.More[v.view], chips, w), viewStrip(items, v.view, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

// issueNextStep says in a few words what the issue wants from you: the
// words, and the colour of the callout they stand in.
func issueNextStep(a *App, i gh.Issue) (string, lipgloss.Color) {
	now := time.Now()
	for _, p := range i.Linked {
		if p.State == "OPEN" && !p.Draft && p.Checks == "SUCCESS" {
			return sGreen.Bold(true).Render(fmt.Sprintf("Linked PR #%d is ready to merge", p.Number)), cGreen
		}
	}
	when := ""
	if i.LastComment != nil {
		when = sDim.Render("  " + age(i.LastComment.CreatedAt, now))
	}
	switch {
	case a.issueAside(i) && i.WaitsForYou(a.me):
		return sDim.Render("Set aside until someone writes again"), cDim
	case i.Why == "asked" && i.LastComment != nil:
		return sWait.Bold(true).Render("◆ "+i.LastComment.Author+" asked you something") + when, cWait
	case i.WaitsForYou(a.me) && i.LastComment != nil:
		return sWait.Bold(true).Render("◆ "+i.LastComment.Author+" replied") + when, cWait
	case i.Comments == 0 && i.Why == "assigned":
		return sAmber.Render("No reply yet"), cAmber
	}
	return sText.Render("Nothing awaiting your reply"), cSteel
}

// issueIcon is the issue icon in the colour of its state: green open,
// violet closed as done, grey closed as not planned.
func issueIcon(i gh.Issue) string {
	c := cGreen
	if i.State == "CLOSED" {
		c = cViolet
		if i.StateReason == "NOT_PLANNED" {
			c = cGrey
		}
	}
	return icoIn("issue", c)
}

// issuePanel describes an issue: what it is, what it wants from you and
// what is linked to it.
func issuePanel(a *App, i gh.Issue, w, h int) []string {
	now := time.Now()
	p := newPanel(w)
	p.add(issueIcon(i) + a.repoLabel(i.Repo) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", i.Number)))
	for k, l := range strings.Split(ansi.Wordwrap(i.Title, max(w-3, 10), ""), "\n") {
		if k == 4 {
			break
		}
		p.add(sWhite.Bold(true).Render(l))
	}
	if len(i.Labels) > 0 {
		var chips []string
		for _, l := range i.Labels {
			chips = append(chips, labelChip(l.Name, l.Color))
		}
		p.chips(chips)
	}
	meta := fact("clock", age(i.UpdatedAt, now)) + "   " + fact("user", i.Author) + "   " + fact("comment", fmt.Sprint(i.Comments))
	p.add(meta)
	if i.Milestone != "" {
		p.add(fact("milestone", i.Milestone))
	}
	p.label("Next step")
	step, c := issueNextStep(a, i)
	lines := []string{step}
	keys := []string{keycap("W") + sDim.Render(" reply")}
	if i.WaitsForYou(a.me) {
		if a.issueAside(i) {
			keys = append(keys, keycap("a")+sDim.Render(" bring back"))
		} else {
			keys = append(keys, keycap("a")+sDim.Render(" set aside"))
		}
	}
	lines = append(lines, strings.Join(keys, "  "))
	p.callout(c, lines...)
	if len(i.Linked) > 0 {
		p.label("Linked pull requests")
		for _, l := range i.Linked {
			k, status := KOK, "ready"
			switch {
			case l.State == "MERGED":
				k, status = KOK, "merged"
			case l.Checks == "FAILURE" || l.Checks == "ERROR":
				k, status = KFail, "checks failing"
			case l.Checks == "PENDING":
				k, status = KRun, "checks running"
			case l.Draft:
				k, status = KSkip, "draft"
			}
			p.add(prIcon(l.State, l.Draft) + sGold.Render(fmt.Sprintf("#%d", l.Number)) + " " + sText.Render(l.Title))
			p.add("  " + statusChip(k, a.frame, status))
		}
	}
	if len(i.Assignees) > 0 {
		p.label("Assignees")
		var people []string
		for _, who := range i.Assignees {
			people = append(people, fact("user", who))
		}
		p.chips(people)
	}
	return p.render(h)
}

func (v *issuesView) panel(a *App, w, h int) []string {
	i, ok := v.selected(a)
	if !ok {
		return emptyPanel(w, h, "No issue selected.")
	}
	return issuePanel(a, i, w, h)
}
