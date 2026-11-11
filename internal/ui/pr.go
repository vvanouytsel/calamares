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

// The tabs of a pull request.
type prTab int

const (
	prTabConversation prTab = iota
	prTabChanges
	prTabChecks
	prTabRuns
	prTabCommits
)

const prTabCount = 5

// prView is one pull request in five tabs: the conversation (description,
// reviews and comments), the changes (files and their diffs, with comments on
// lines), the checks of its latest commit, a list you can open: a GitHub
// Actions check leads to its workflow run, every workflow run of its commits,
// and the commits.
type prView struct {
	id     int
	repo   string
	number int
	pr     gh.PullRequestDetail
	loaded bool
	loadState
	checks *Table

	tab  prTab
	conv conversation
	diff changes

	pendN         int    // line comments waiting for your review
	behind        int    // commits the base has that the branch lacks, read when it is behind
	behindFor     string // the head the count was read for
	commits       *Table
	commitList    []gh.PRCommit
	commitsLoaded bool
	commitsBusy   bool

	runs       *Table
	runList    []gh.Run
	runsLoaded bool
	runsBusy   bool

	required    []string // the checks the base branch requires
	requiredFor string   // the base they were read for
}

// prLiveEvery is how often an open pull request refreshes while live.
const prLiveEvery = 30 * time.Second

type prDetailMsg struct {
	id  int
	pr  gh.PullRequestDetail
	err error
}

type prDiscussionMsg struct {
	id  int
	d   gh.Discussion
	err error
}

type prFilesMsg struct {
	id    int
	files []gh.FileDiff
	err   error
}

type prBehindMsg struct {
	id  int
	sha string
	n   int
	err error
}

type prCommitsMsg struct {
	id      int
	commits []gh.PRCommit
	err     error
}

func (m prBehindMsg) viewID() int     { return m.id }
func (m prCommitsMsg) viewID() int    { return m.id }
func (m prDetailMsg) viewID() int     { return m.id }
func (m prDiscussionMsg) viewID() int { return m.id }
func (m prFilesMsg) viewID() int      { return m.id }

func newPRView(a *App, repo string, number int) *prView {
	v := &prView{id: a.newID(), repo: repo, number: number}
	v.checks = newTable(
		Column{Title: "CHECK", Max: 60, Flex: true, MidCut: true},
		Column{Title: "STATUS"},
		Column{Title: "WORKFLOW RUN", Max: 30, Flex: true, Drop: 1},
	)
	v.checks.Empty = "No checks on the latest commit."
	v.commits = newTable(
		Column{Title: "SHA", Min: 7},
		Column{Title: "COMMIT", Max: 90, Flex: true},
		Column{Title: "WHO", Max: 20},
		Column{Title: "AGE", Right: true, Min: 3},
	)
	v.commits.Empty = "Loading the commits…"
	v.runs = newPRRunsTable()
	return v
}

func (v *prView) ID() int          { return v.id }
func (v *prView) RepoName() string { return v.repo }
func (v *prView) Crumb() string    { return fmt.Sprintf("PR #%d", v.number) }
func (v *prView) Init(a *App) tea.Cmd {
	return tea.Batch(v.load(a), v.loadDiscussion(a))
}

func (v *prView) Updated(a *App) time.Time { return v.at }
func (v *prView) Loading(a *App) bool {
	return v.busy || v.conv.busy || v.diff.busy
}

func (v *prView) load(a *App) tea.Cmd {
	if !v.begin() {
		return nil
	}
	id, repo, number := v.id, v.repo, v.number
	return func() tea.Msg {
		pr, err := a.client.PullRequestDetails(context.Background(), repo, number)
		return prDetailMsg{id, pr, err}
	}
}

func (v *prView) loadDiscussion(a *App) tea.Cmd {
	if v.conv.busy {
		return nil
	}
	v.conv.busy = true
	id, repo, number := v.id, v.repo, v.number
	return func() tea.Msg {
		d, err := a.client.PullRequestDiscussion(context.Background(), repo, number)
		return prDiscussionMsg{id, d, err}
	}
}

// loadBehind counts how far the branch is behind its base, once per head.
func (v *prView) loadBehind(a *App) tea.Cmd {
	pr := v.pr
	if pr.MergeState != "BEHIND" || v.behindFor == pr.HeadSHA {
		return nil
	}
	v.behindFor = pr.HeadSHA
	id, repo := v.id, v.repo
	return func() tea.Msg {
		ctx := gh.Background(context.Background())
		n, err := a.client.BehindBy(ctx, repo, pr.Base, pr.Branch)
		return prBehindMsg{id, pr.HeadSHA, n, err}
	}
}

type prRequiredMsg struct {
	id    int
	base  string
	names []string
	err   error
}

func (m prRequiredMsg) viewID() int { return m.id }

// loadRequired reads, once per base, which checks a merge into it requires.
func (v *prView) loadRequired(a *App) tea.Cmd {
	base := v.pr.Base
	if base == "" || v.requiredFor == base || v.pr.State != "OPEN" {
		return nil
	}
	v.requiredFor = base
	id, repo := v.id, v.repo
	return func() tea.Msg {
		names, err := a.client.RequiredChecks(gh.Background(context.Background()), repo, base)
		return prRequiredMsg{id, base, names, err}
	}
}

// missingChecks are the required checks no check of the pull request
// answers to; once nothing runs any more, none of them will report.
func (v *prView) missingChecks() []string {
	var reported []string
	for _, c := range v.pr.Checks {
		reported = append(reported, c.Name)
		if c.Workflow != "" {
			reported = append(reported, c.Workflow+" / "+c.Name)
		}
	}
	return gh.MissingChecks(v.required, reported)
}

func (v *prView) loadCommits(a *App) tea.Cmd {
	if v.commitsBusy {
		return nil
	}
	v.commitsBusy = true
	id, repo, number := v.id, v.repo, v.number
	return func() tea.Msg {
		c, err := a.client.PullRequestCommits(context.Background(), repo, number)
		return prCommitsMsg{id, c, err}
	}
}

func (v *prView) loadFiles(a *App) tea.Cmd {
	if v.diff.busy {
		return nil
	}
	v.diff.busy = true
	id, repo, number := v.id, v.repo, v.number
	return func() tea.Msg {
		files, err := a.client.PullRequestFiles(context.Background(), repo, number)
		return prFilesMsg{id, files, err}
	}
}

// Refresh reads the pull request again: when asked, or every 30 seconds while live.
func (v *prView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(prLiveEvery) {
		return nil
	}
	cmds := []tea.Cmd{v.load(a)}
	if v.conv.loaded || v.tab == prTabConversation {
		cmds = append(cmds, v.loadDiscussion(a))
	}
	if v.diff.loaded || v.tab == prTabChanges {
		cmds = append(cmds, v.loadFiles(a))
	}
	if v.runsLoaded || v.tab == prTabRuns {
		cmds = append(cmds, v.loadRuns(a))
	}
	return tea.Batch(cmds...)
}

func (v *prView) setTab(a *App, t prTab) tea.Cmd {
	v.tab = t
	switch {
	case t == prTabConversation && !v.conv.loaded:
		return v.loadDiscussion(a)
	case t == prTabChanges && !v.diff.loaded:
		return tea.Batch(v.loadFiles(a), v.loadDiscussion(a))
	case t == prTabCommits && !v.commitsLoaded:
		return v.loadCommits(a)
	case t == prTabRuns && !v.runsLoaded:
		cmds := []tea.Cmd{v.loadRuns(a)}
		if !v.commitsLoaded {
			cmds = append(cmds, v.loadCommits(a))
		}
		return tea.Batch(cmds...)
	}
	return nil
}

func (v *prView) cycleTab(a *App, d int) tea.Cmd {
	return v.setTab(a, prTab((int(v.tab)+d+prTabCount)%prTabCount))
}

func (v *prView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case prDetailMsg:
		v.end(msg.err)
		if msg.err != nil {
			if v.loaded {
				return a.flashCmd(nError, "Could not refresh the pull request: "+msg.err.Error())
			}
			return nil
		}
		v.pr, v.loaded = msg.pr, true
		v.rebuild()
		v.conv.ver++
		cmd := tea.Batch(v.loadBehind(a), v.loadRequired(a))
		if v.tab == prTabRuns && !v.runsLoaded {
			cmd = tea.Batch(cmd, v.loadRuns(a))
		}
		return cmd
	case prRequiredMsg:
		if msg.err == nil && msg.base == v.requiredFor {
			v.required = msg.names
		}
		return nil
	case prBehindMsg:
		if msg.err == nil && msg.sha == v.behindFor {
			v.behind = msg.n
		}
		return nil
	case prCommitsMsg:
		v.commitsBusy = false
		if msg.err != nil {
			v.commits.Empty = "Could not load the commits: " + msg.err.Error()
			return nil
		}
		v.commitList, v.commitsLoaded = msg.commits, true
		v.rebuildCommits(a)
		v.rebuildRuns()
		return nil
	case prRunsMsg:
		v.runsBusy = false
		if msg.err != nil {
			v.runs.Empty = "Could not load the runs: " + msg.err.Error()
			return nil
		}
		v.runList, v.runsLoaded = v.ownRuns(msg.runs), true
		v.rebuildRuns()
		return nil
	case prDiscussionMsg:
		v.conv.busy, v.conv.err = false, msg.err
		if msg.err == nil {
			v.conv.disc, v.conv.loaded = msg.d, true
			v.conv.ver++
		}
		return nil
	case prFilesMsg:
		v.diff.busy, v.diff.err = false, msg.err
		if msg.err == nil {
			v.diff.setFiles(msg.files)
		}
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

func (v *prView) selectedCheck() (gh.Check, bool) {
	r := v.checks.Selected()
	if r == nil {
		return gh.Check{}, false
	}
	for _, c := range v.pr.Checks {
		if checkKey(c) == r.Key {
			return c, true
		}
	}
	return gh.Check{}, false
}

// checkKey tells checks apart; two workflows can have checks of the same name.
func checkKey(c gh.Check) string { return fmt.Sprintf("%s\x00%d\x00%s", c.Name, c.RunID, c.URL) }

func (v *prView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

func (v *prView) Keys(a *App) []Binding {
	var bs []Binding
	switch v.tab {
	case prTabConversation:
		bs = v.conv.keys(a, v)
	case prTabChanges:
		bs = v.diff.keys(a, v.pr.URL+"/files", func(replyOnly bool) tea.Cmd { return v.diff.comment(a, v, replyOnly) })
	case prTabChecks:
		bs = v.checksKeys(a)
	case prTabRuns:
		bs = v.runsKeys(a)
	case prTabCommits:
		bs = v.commitsKeys(a)
	}
	bs = append(bs,
		viewKeys("next view", "next view", "", func(d int) tea.Cmd { return v.cycleTab(a, d) }),
		Binding{Keys: []string{"W"}, Desc: "comment", Help: "write a comment on the pull request", Mutates: true, Need: gh.CapComment, Repo: v.repo, Do: do(func() tea.Cmd {
			if len(a.pendingFor(v.repo, v.number)) > 0 {
				return askEditor("Comment", []string{sWhite.Render(a.short(v.repo) + fmt.Sprintf(" #%d", v.number)), sAmber.Render(fmt.Sprintf("sent with your %d pending line comments", len(a.pendingFor(v.repo, v.number))))},
					"your comment", "", "submits the review", true, func(text string) tea.Cmd {
						return a.review(v.repo, v.number, "COMMENT", text, "Reviewed "+a.short(v.repo)+fmt.Sprintf(" #%d", v.number))
					})
			}
			return v.compose(a, "Comment", "your comment", "", func(ctx context.Context, text string) error {
				return a.client.CommentOnPullRequest(ctx, v.repo, v.number, text)
			}, "Commented on "+a.short(v.repo)+fmt.Sprintf(" #%d", v.number))
		})},
	)
	for _, b := range a.prKeys(v.repo, v.number, v.pr.Title) {
		if b.Keys[0] == "C" {
			b.Desc = "request changes"
		}
		bs = append(bs, b)
	}
	bs = append(bs, v.actionKeys(a)...)
	bs = append(bs,
		a.personKey(v.pr.Author),
		Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(v.pr.URL) })},
		Binding{Keys: []string{"y"}, Desc: "copy link", Help: "copy the link", Do: do(func() tea.Cmd { return a.copyText(v.pr.URL, "pull request URL") })},
	)
	for i := range bs {
		if bs[i].Need != gh.CapRead && bs[i].Repo == "" {
			bs[i].Repo = v.repo
		}
	}
	return grouped("A PULL REQUEST", bs...)
}

// commitsKeys are the keys of the Commits tab.
func (v *prView) commitsKeys(a *App) []Binding {
	bs := []Binding{v.commits.moveKeys()}
	if r := v.commits.Selected(); r != nil {
		for _, c := range v.commitList {
			if c.SHA == r.Key {
				commit := gh.Commit{SHA: c.SHA, Message: c.Message, Author: c.Author, Date: c.At}
				bs = append([]Binding{{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open commit", Do: do(func() tea.Cmd { return a.push(newCommitView(a, v.repo, commit)) })}}, bs...)
			}
		}
	}
	return grouped("COMMITS", bs...)
}

func (v *prView) rebuildCommits(a *App) {
	now := time.Now()
	var rows []Row
	for _, c := range v.commitList {
		rows = append(rows, Row{Key: c.SHA, Cells: []Cell{
			{Text: c.SHA[:min(7, len(c.SHA))], Styled: sTan.Render(c.SHA[:min(7, len(c.SHA))])},
			{Text: c.Message}, {Text: c.Author}, {Text: age(c.At, now)}}})
	}
	v.commits.SetRows(rows)
	v.commits.Empty = "No commits."
}

// compose opens the editor and, once the text is sent, runs send with it.
func (v *prView) compose(a *App, title, placeholder, initial string, send func(ctx context.Context, text string) error, done string) tea.Cmd {
	about := []string{sWhite.Render(fmt.Sprintf("%s #%d", a.short(v.repo), v.number)) + sDim.Render(" · "+v.pr.Title)}
	return askEditor(title, about, placeholder, initial, "posts it", true, func(text string) tea.Cmd {
		return a.act(v.repo, done, func(ctx context.Context) error { return send(ctx, text) })
	})
}

// checksKeys are the keys of the Checks tab: a GitHub Actions check opens
// its workflow run, any other check its page.
func (v *prView) checksKeys(a *App) []Binding {
	var bs []Binding
	check, ok := v.selectedCheck()
	failedRun := ok && check.RunID != 0 && check.Kind == "failed"
	switch {
	case ok && check.RunID != 0:
		run := gh.Run{ID: check.RunID, Name: check.Workflow, HeadBranch: v.pr.Branch}
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open run", Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, run)) })},
			Binding{Keys: []string{"l"}, Desc: "logs", Do: do(func() tea.Cmd { return a.push(newLogView(a, v.repo, run, nil, 0)) })},
		)
	case ok && check.URL != "":
		bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Help: "open the check's page", Do: do(func() tea.Cmd { return a.openURL(check.URL) })})
	}
	bs = append(bs,
		Binding{Keys: []string{"R"}, Desc: "rerun", Off: !failedRun, Mutates: true, Need: gh.CapWrite, Repo: v.repo, Do: do(func() tea.Cmd {
			if !failedRun {
				return a.flashCmd(nHint, "Select a failed GitHub Actions check to rerun its failed jobs.")
			}
			return a.rerunPicker(v.repo, gh.Run{ID: check.RunID, Name: check.Workflow, Status: "completed", Conclusion: "failure"}, nil, nil)
		})},
		v.checks.moveKeys(),
	)
	return grouped("CHECKS", bs...)
}

func (v *prView) rebuild() {
	var rows []Row
	// Failing checks first, then running, then the rest.
	for _, kind := range []string{"failed", "pending", "passed"} {
		for _, c := range v.pr.Checks {
			if c.Kind != kind {
				continue
			}
			k := map[string]Kind{"failed": KFail, "pending": KRun, "passed": KOK}[kind]
			status := c.Status
			if status == "" {
				status = kind
			}
			label := k.Glyph(0) + " " + status
			run := ""
			if c.RunID != 0 {
				run = c.Workflow
			}
			rows = append(rows, Row{Key: checkKey(c), Kind: k, Cells: []Cell{
				{Text: c.Name},
				{Text: label, Styled: fg(k.Color()).Render(label)},
				{Text: run},
			}})
		}
	}
	v.checks.SetRows(rows)
}

// prHeaderLines is how many lines the title, the facts and the strip of views take above the view.
const prHeaderLines = 3

func (v *prView) Render(a *App, w, h int) string {
	if !v.loaded {
		msg := "Loading pull request…"
		if v.err != nil {
			msg = "Could not load the pull request: " + v.err.Error()
		}
		return box(title("Pull request", fmt.Sprintf("%s #%d", v.repo, v.number), -1, ""), " "+sDim.Render(msg), w, h, cFrame)
	}
	now := time.Now()
	pr := v.pr
	v.pendN = len(a.pendingFor(v.repo, v.number))
	head := []string{
		" " + prIcon(pr.State, pr.Draft) + sGold.Bold(true).Render(fmt.Sprintf("#%d", pr.Number)) + " " + sWhite.Bold(true).Render(pr.Title) +
			"   " + ico("repo") + a.repoLabel(v.repo) + a.roleChip(v.repo),
		" " + prStateChip(pr.State, pr.Draft) + "  " + fact("branch", pr.Branch) + sDim.Render(" → ") + sText.Render(pr.Base) +
			"   " + fact("user", pr.Author) + "   " + fact("clock", age(pr.UpdatedAt, now)) + "   " +
			diffStat(pr.Additions, pr.Deletions) + "   " + v.reviewState(),
		v.tabStrip(w),
	}
	for i := range head[:2] {
		head[i] = clip(head[i], w)
	}

	bodyH := h - prHeaderLines
	bw, side := w, []string(nil)
	if v.tab != prTabChanges && w >= panelMinWidth {
		pw := min(max(w*3/10, 34), 46)
		bw, side = w-pw-1, v.panel(a, pw, bodyH)
	}
	var body string
	switch v.tab {
	case prTabConversation:
		body = v.conv.render(a, v, bw, bodyH)
	case prTabChanges:
		body = v.diff.render(a, v, w, bodyH)
	case prTabRuns:
		v.runs.TwoLine = false
		body = box(title("Runs", "enter opens one", len(v.runList), ""), v.runs.Render(bw-2, bodyH-2), bw, bodyH, cFrame)
	case prTabCommits:
		v.commits.TwoLine = false
		body = box(title("Commits", "enter opens one", len(v.commitList), ""), v.commits.Render(bw-2, bodyH-2), bw, bodyH, cFrame)
	default:
		v.checks.TwoLine = false
		scope := "enter opens the run"
		if side == nil {
			scope = ansi.Strip(v.mergeLine())
		}
		body = box(title("Checks", scope, len(pr.Checks), ""), v.checks.Render(bw-2, bodyH-2), bw, bodyH, cFrame)
	}
	if side != nil {
		rows := strings.Split(body, "\n")
		lines := make([]string, bodyH)
		for i := range lines {
			l := ""
			if i < len(rows) {
				l = rows[i]
			}
			lines[i] = fit(l, bw) + " " + side[i]
		}
		body = strings.Join(lines, "\n")
	}
	return strings.Join(head, "\n") + "\n" + body
}

// tabStrip draws the strip of views.
func (v *prView) tabStrip(w int) string {
	pr := v.pr
	failed := 0
	for _, c := range pr.Checks {
		if c.Kind == "failed" {
			failed++
		}
	}
	checks := stripItem{Label: "Checks", Icon: "checks", Count: fmt.Sprint(len(pr.Checks))}
	if failed > 0 {
		checks.Count, checks.St = fmt.Sprintf("%d ✗", failed), &sRed
	}
	changes := stripItem{Label: "Changes", Icon: "code", Count: fmt.Sprint(pr.Files)}
	if n := v.pendN; n > 0 {
		changes.Count, changes.St = fmt.Sprintf("%d · %d pending", pr.Files, n), &sAmber
	}
	runs := stripItem{Label: "Runs", Icon: "actions"}
	if v.runsLoaded {
		runs.Count = fmt.Sprint(len(v.runList))
	}
	commits := stripItem{Label: "Commits", Icon: "commit"}
	if v.commitsLoaded {
		commits.Count = fmt.Sprint(len(v.commitList))
	}
	return viewStrip([]stripItem{
		{Label: "Conversation", Icon: "comments", Count: strings.TrimSpace(v.conv.count())},
		changes,
		checks,
		runs,
		commits,
	}, int(v.tab), w)
}

// reviewState is where the review stands, in a few words.
func (v *prView) reviewState() string {
	switch v.pr.ReviewDecision {
	case "APPROVED":
		return statusChip(KOK, 0, "approved")
	case "CHANGES_REQUESTED":
		return statusChip(KFail, 0, "changes requested")
	case "REVIEW_REQUIRED":
		return statusChip(KWait, 0, "needs review")
	}
	return sDim.Render("no review required")
}

// prIcon is the pull request icon in the colour of its state, as on GitHub:
// green open, violet merged, red closed, grey draft.
func prIcon(state string, draft bool) string {
	return icoIn("pr", prStateColor(state, draft))
}

func prStateColor(state string, draft bool) lipgloss.Color {
	switch {
	case state == "MERGED":
		return cViolet
	case state == "CLOSED":
		return cRed
	case draft:
		return cGrey
	}
	return cGreen
}

// prStateChip is the state of a pull request as a chip in its colour.
func prStateChip(state string, draft bool) string {
	label := map[string]string{"OPEN": "open", "CLOSED": "closed", "MERGED": "merged"}[state]
	if draft {
		label = "draft"
	}
	if label == "" {
		label = strings.ToLower(state)
	}
	c := prStateColor(state, draft)
	return lipgloss.NewStyle().Foreground(c).Background(lipgloss.Color(mix(string(cBg), string(c), 0.2))).Render(" " + icon("pr") + label + " ")
}

// diffStat is "+86 −12" with a bar of five blocks split between them.
func diffStat(add, del int) string {
	out := sGreen.Render(fmt.Sprintf("+%d", add)) + " " + sRed.Render(fmt.Sprintf("−%d", del))
	if add+del == 0 || ascii {
		return out
	}
	g := int(float64(add)/float64(add+del)*5 + 0.5)
	if add > 0 && g == 0 {
		g = 1
	}
	if del > 0 && g == 5 {
		g = 4
	}
	return out + " " + sGreen.Render(strings.Repeat("■", g)) + sRed.Render(strings.Repeat("■", 5-g))
}

// mergeLine says whether the pull request can be merged, in GitHub's terms.
func (v *prView) mergeLine() string {
	review, merge := v.mergeParts()
	return review + sDim.Render(" · ") + merge
}

// mergeParts are the review state and the merge state of the pull request.
func (v *prView) mergeParts() (review, merge string) {
	pr := v.pr
	review = map[string]string{
		"APPROVED":          sGreen.Render("✓ approved"),
		"CHANGES_REQUESTED": sRed.Render("✗ changes requested"),
		"REVIEW_REQUIRED":   sWait.Render("◆ needs review"),
	}[pr.ReviewDecision]
	if review == "" {
		review = sDim.Render("no review required")
	}
	merge = map[string]string{
		"CLEAN":    sGreen.Render("ready to merge"),
		"BLOCKED":  sAmber.Render("blocked by required reviews or checks"),
		"BEHIND":   sAmber.Render("behind its base branch"),
		"DIRTY":    sRed.Render("has merge conflicts"),
		"UNSTABLE": sAmber.Render("mergeable, but some checks fail"),
		"DRAFT":    sDim.Render("draft"),
	}[pr.MergeState]
	if merge == "" {
		merge = sDim.Render("merge state unknown")
	}
	return review, merge
}
