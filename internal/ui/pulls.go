package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// pullsView lists pull requests: the open ones that concern you (the Pull
// requests tab), or the open ones of one repo (a tab of the repo screen).
type pullsView struct {
	id   int
	repo string // "" for your own list
	t    *Table
	// A repo's list is kept here; your own list lives in the store.
	prs []gh.PullRequest
	loadState
}

// pullsLiveEvery is how often a list of pull requests refreshes while live.
const pullsLiveEvery = time.Minute

type repoPullsMsg struct {
	id  int
	prs []gh.PullRequest
	err error
}

func (m repoPullsMsg) viewID() int { return m.id }

func newPullsView(a *App, repo string) *pullsView {
	v := &pullsView{id: a.newID(), repo: repo}
	var cols []Column
	if repo == "" {
		cols = append(cols, Column{Title: "ROLE", Help: "why it is on your list: your review is asked (reviewer), you wrote it (author), or you take part in it (involved)"}, Column{Title: "REPO", Max: 30, Flex: true, MidCut: true, Help: "the repository"})
	}
	cols = append(cols,
		Column{Title: "#", Right: true, Help: "its number"},
		Column{Title: "TITLE", Max: 60, Flex: true, Below: true},
		Column{Title: "AUTHOR", Max: 20, Flex: true, Drop: 2, Help: "who opened it"},
	)
	if repo != "" {
		cols = append(cols, Column{Title: "BRANCH", Max: 30, Flex: true, Drop: 1, Help: "the branch it would merge"})
	}
	cols = append(cols,
		Column{Title: "CHECKS", Help: "how its checks did: how many passed of all, or how many fail"},
		Column{Title: "REVIEW", Help: "where the review stands: needs review, approved, changes requested or draft"},
		Column{Title: "UPDATED", Right: true, Min: 3, Help: "how long ago it last changed"},
	)
	v.t = newTable(cols...)
	v.t.Tickable = true
	v.t.Empty = "Loading pull requests…"
	if repo != "" {
		var prs []gh.PullRequest
		if at, ok := a.client.Recall(v.snapshot(), &prs); ok {
			v.prs, v.at = prs, at
		}
	}
	return v
}

func (v *pullsView) snapshot() string { return repoPullsSnapshot(v.repo) }

// repoPullsSnapshot is the name a repo's list of open pull requests is kept under.
func repoPullsSnapshot(repo string) string { return "pulls:" + repo }

func (v *pullsView) ID() int { return v.id }
func (v *pullsView) Crumb() string {
	if v.repo != "" {
		return "pull requests"
	}
	return "Pull requests"
}
func (v *pullsView) Init(a *App) tea.Cmd { return nil }

func (v *pullsView) list(a *App) []gh.PullRequest {
	if v.repo == "" {
		return a.st.prs
	}
	return v.prs
}

func (v *pullsView) Updated(a *App) time.Time {
	if v.repo == "" {
		return a.st.prsState.at
	}
	return v.at
}

func (v *pullsView) Loading(a *App) bool {
	if v.repo == "" {
		return a.st.prsState.busy
	}
	return v.busy
}

// Refresh reads the list again: when asked, or every minute while live.
func (v *pullsView) Refresh(a *App, force bool) tea.Cmd {
	state := &v.loadState
	if v.repo == "" {
		state = &a.st.prsState
	}
	if !force && !state.waited(pullsLiveEvery) {
		return nil
	}
	if v.repo == "" {
		return a.st.loadPRs(a)
	}
	if !v.begin() {
		return nil
	}
	id, repo, key := v.id, v.repo, v.snapshot()
	return func() tea.Msg {
		prs, err := a.client.RepoPullRequests(context.Background(), repo)
		if err == nil {
			a.client.Remember(key, prs)
		}
		return repoPullsMsg{id, prs, err}
	}
}

func (v *pullsView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case repoPullsMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the pull requests of "+v.repo+": "+msg.err.Error())
		}
		v.prs = msg.prs
		return nil
	case tea.KeyMsg:
		return v.key(a, msg.String())
	}
	return nil
}

func (v *pullsView) selected(a *App) (gh.PullRequest, bool) {
	r := v.t.Selected()
	if r == nil {
		return gh.PullRequest{}, false
	}
	for _, pr := range v.list(a) {
		if prKey(pr) == r.Key {
			return pr, true
		}
	}
	return gh.PullRequest{}, false
}

func prKey(pr gh.PullRequest) string { return fmt.Sprintf("%s#%d", pr.Repo, pr.Number) }

func (v *pullsView) key(a *App, k string) tea.Cmd {
	cmd, _ := a.press(v.Keys(a), k)
	return cmd
}

// ClearMarks unticks the ticked pull requests (esc does it before it goes back).
func (v *pullsView) ClearMarks() bool { return v.t.ClearMarks() }

// marked lists the ticked pull requests.
func (v *pullsView) marked(a *App) []gh.PullRequest {
	var out []gh.PullRequest
	for _, pr := range v.list(a) {
		if v.t.Marked(prKey(pr)) {
			out = append(out, pr)
		}
	}
	return out
}

// tickKeys tick pull requests, so a, C and M act on all of them.
func (v *pullsView) tickKeys() []Binding {
	return []Binding{
		{Keys: []string{" "}, Label: "space", Desc: "select", Help: "select the pull request: then a, C and M act on all selected ones",
			Do: do(func() tea.Cmd { v.t.ToggleMark(); return nil })},
		v.t.rangeKeys(),
		{Keys: []string{"*"}, Help: "select every pull request shown (again: unselect them)", Do: do(func() tea.Cmd { v.t.MarkAll(); return nil })},
	}
}

// bulkKeys approve, request changes on or merge every selected pull request.
func (v *pullsView) bulkKeys(a *App, prs []gh.PullRequest) []Binding {
	n := len(prs)
	about := []string{sWhite.Render(fmt.Sprintf("%d pull requests", n))}
	for i, pr := range prs {
		if i == 6 {
			about = append(about, sDim.Render(fmt.Sprintf("… and %d more", n-i)))
			break
		}
		about = append(about, sDim.Render(fmt.Sprintf("%s #%d · ", a.short(pr.Repo), pr.Number))+sText.Render(pr.Title))
	}
	return []Binding{
		{Keys: []string{"a"}, Desc: fmt.Sprintf("approve (%d)", n), Help: "approve every selected pull request", Mutates: true, Need: gh.CapComment,
			Do: do(func() tea.Cmd {
				return confirm(fmt.Sprintf("Approve %d pull requests?", n), append(about, "", "An approving review is submitted on each."), "Approve", false, func() tea.Cmd {
					return v.bulk(a, prs, "Approved", func(ctx context.Context, pr gh.PullRequest) error {
						return a.client.ReviewPullRequest(ctx, pr.Repo, pr.Number, "APPROVE", "")
					}, func(n int) {
						a.earnReview("APPROVE", n)
						a.earnBest("bulk-approver", n)
					})
				})
			})},
		{Keys: []string{"C"}, Desc: fmt.Sprintf("request changes (%d)", n), Help: "request changes on every selected pull request", Mutates: true, Need: gh.CapComment,
			Do: do(func() tea.Cmd {
				return askEditor("Request changes", about, "what needs to change, the same for each", "", "submits a review on each", true, func(text string) tea.Cmd {
					return v.bulk(a, prs, "Requested changes on", func(ctx context.Context, pr gh.PullRequest) error {
						return a.client.ReviewPullRequest(ctx, pr.Repo, pr.Number, "REQUEST_CHANGES", text)
					}, func(n int) { a.earnReview("REQUEST_CHANGES", n) })
				})
			})},
		{Keys: []string{"M"}, Desc: fmt.Sprintf("merge (%d)", n), Help: "merge every selected pull request, the same way", Mutates: true, Need: gh.CapWrite,
			Do: do(func() tea.Cmd { return v.bulkMerge(a, prs, about) })},
	}
}

// bulkMerge asks how to merge, then to confirm, then merges each in turn;
// GitHub refuses the ones its repo doesn't allow that way or can't merge.
func (v *pullsView) bulkMerge(a *App, prs []gh.PullRequest, about []string) tea.Cmd {
	var items []pickItem
	for _, m := range []string{"squash", "merge", "rebase"} {
		items = append(items, pickItem{value: m, label: m, note: sDim.Render(mergeMethodNote[m])})
	}
	return openModal(newPicker("merge-many", fmt.Sprintf("Merge %d pull requests", len(prs)), "", items, func(method string) tea.Cmd {
		lines := append(slices.Clone(about), "", "Each is merged into its base with "+method+"; one GitHub refuses is left as it is.")
		return confirm(fmt.Sprintf("Merge %d pull requests?", len(prs)), lines, "Merge", true, func() tea.Cmd {
			return v.bulk(a, prs, "Merged", func(ctx context.Context, pr gh.PullRequest) error {
				return a.client.MergePullRequest(ctx, pr.Repo, pr.Number, method)
			}, func(n int) {
				for range n {
					a.earnMerge(time.Now(), nil)
				}
			})
		})
	}))
}

// bulk does fn to each pull request and says how many it did; the ticks go.
func (v *pullsView) bulk(a *App, prs []gh.PullRequest, done string, fn func(ctx context.Context, pr gh.PullRequest) error, earned func(n int)) tea.Cmd {
	if a.opts.ReadOnly {
		return a.flashCmd(nHint, readOnlyText)
	}
	v.t.ClearMarks()
	id := v.id
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var failed []string
		for _, pr := range prs {
			if err := fn(ctx, pr); err != nil {
				failed = append(failed, fmt.Sprintf("%s #%d: %v", a.short(pr.Repo), pr.Number, err))
			}
		}
		text := fmt.Sprintf("%s %d of %d pull requests", done, len(prs)-len(failed), len(prs))
		if len(failed) > 0 {
			return actionMsg{viewID: id, err: fmt.Errorf("%s · %s", text, strings.Join(failed, " · "))}
		}
		ok := len(prs) - len(failed)
		return actionMsg{viewID: id, text: text, then: func() tea.Cmd { earned(ok); return nil }}
	}
}

func (v *pullsView) Keys(a *App) []Binding {
	var pre []Binding
	if prs := v.marked(a); len(prs) > 0 {
		pre = v.bulkKeys(a, prs)
	}
	pr, ok := v.selected(a)
	if !ok {
		return grouped("PULL REQUESTS", v.newKeys(a, append(append(pre, v.tickKeys()...), filterHint, a.twoLineKey(), v.t.moveKeys())...)...)
	}
	bs := append(pre, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Do: do(func() tea.Cmd { return a.push(newPRView(a, pr.Repo, pr.Number)) })})
	bs = append(bs, a.prKeys(pr.Repo, pr.Number, pr.Title)...)
	bs = append(bs, a.prAsideBinding(pr))
	bs = append(bs, Binding{Keys: []string{"o"}, Desc: "browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(pr.URL) })})
	bs = append(bs, v.tickKeys()...)
	bs = append(bs,
		filterHint,
		a.twoLineKey(),
		Binding{Keys: []string{"y"}, Help: "copy the URL", Do: do(func() tea.Cmd { return a.copyText(pr.URL, "pull request URL") })},
		v.t.moveKeys(),
	)
	return grouped("PULL REQUESTS", v.newKeys(a, bs...)...)
}

func (v *pullsView) rebuild(a *App) {
	now := time.Now()
	var rows []Row
	for _, pr := range v.list(a) {
		checks := checksCell(pr.Checks)
		review := reviewCell(pr)
		cells := []Cell{}
		if v.repo == "" {
			cells = append(cells, whyCell(pr.Why, a.prAside(pr)), iconText("repo", a.short(pr.Repo), false))
		}
		num := fmt.Sprintf("#%d", pr.Number)
		cells = append(cells,
			Cell{Text: num, Styled: sGold.Render(num)},
			Cell{Text: icon("pr") + pr.Title, Styled: prIcon("OPEN", pr.Draft) + sWhite.Render(pr.Title)},
			iconCell("user", pr.Author, sText, cTeal),
		)
		if v.repo != "" {
			cells = append(cells, iconText("branch", pr.Branch, false))
		}
		cells = append(cells, checks, review, steelCell(age(pr.UpdatedAt, now), false))
		rows = append(rows, Row{Key: prKey(pr), Kind: KOK, Cells: cells})
	}
	v.t.Empty = "Nothing is waiting for your review."
	if v.repo != "" {
		v.t.Empty = "No open pull requests in this repo."
	}
	switch {
	case v.Loading(a) && len(rows) == 0:
		v.t.Empty = "Loading pull requests…"
	case v.loadErr(a) != nil && len(rows) == 0:
		v.t.Empty = "Could not load pull requests: " + v.loadErr(a).Error()
	}
	v.t.SetRows(rows)
}

func (v *pullsView) loadErr(a *App) error {
	if v.repo == "" {
		return a.st.prsState.err
	}
	return v.err
}

// whyCell says what you are to the pull request: its reviewer, its author, or
// involved in it.
func whyCell(why string, aside bool) Cell {
	switch {
	case why == gh.WhyReview && aside:
		return iconCell("eye", "reviewer", sDim, cDim)
	case why == gh.WhyReview:
		return iconCell("eye", "reviewer", sWait, cWait)
	case why == gh.WhyYours:
		return iconCell("pencil", "author", sWhite, cSteel)
	}
	return iconCell("at", "involved", sDim, cDim)
}

// tintCell is a cell in the colour of a status, crisp: no block behind it.
func tintCell(k Kind, text string) Cell {
	return Cell{Text: text, Styled: fg(k.Color()).Render(text)}
}

// checksCell summarises a PR's checks as a chip: "✓ 6/6", "✗ 1/7", "◐ 2/7".
func checksCell(c gh.CheckSummary) Cell {
	switch {
	case c.Total == 0:
		return Cell{Text: "– none", Styled: sDim.Render("– none")}
	case c.Failed > 0:
		return tintCell(KFail, fmt.Sprintf("✗ %d of %d failing", c.Failed, c.Total))
	case c.Pending > 0:
		return tintCell(KRun, fmt.Sprintf("◐ %d running", c.Pending))
	}
	return tintCell(KOK, fmt.Sprintf("✓ %d/%d", c.Passed, c.Total))
}

func reviewCell(pr gh.PullRequest) Cell {
	switch {
	case pr.Draft:
		return Cell{Text: "draft", Styled: sDim.Render("draft")}
	case pr.ReviewDecision == "APPROVED":
		return tintCell(KOK, "✓ approved")
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return tintCell(KFail, "✗ changes requested")
	case pr.ReviewDecision == "REVIEW_REQUIRED":
		return tintCell(KWait, "◆ needs review")
	}
	return Cell{Text: "–", Styled: sDim.Render("–")}
}

func (v *pullsView) SetFilter(a *App, q string) { v.t.SetFilter(q, a.me) }
func (v *pullsView) FilterValue() string        { return v.t.Filter() }
func (v *pullsView) FilterInfo(a *App) filterInfo {
	return v.t.Info()
}

func (v *pullsView) sync(a *App) { v.rebuild(a) }

func (v *pullsView) Render(a *App, w, h int) string {
	v.t.TwoLine = a.twoLine
	head := " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))
	if n := len(v.t.Marks()); n > 0 {
		head = tickedLine(n, w, "a", "approve", "C", "request changes", "M", "merge")
	}
	if v.repo != "" {
		return head + "\n" + v.t.Render(w-2, h-3)
	}
	st := v.stats(a)
	var chips []string
	if st.review > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d to review", st.review)))
	}
	if st.failing > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failing", st.failing)))
	}
	if st.ready > 0 {
		chips = append(chips, statusChip(KOK, 0, fmt.Sprintf("%d ready to merge", st.ready)))
	}
	if st.drafts > 0 {
		chips = append(chips, sDim.Render(fmt.Sprintf("%d draft", st.drafts)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("pr", "Pull requests", "open, involving you", v.t.Len(), chips, w), head},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

// prKeys are the keys that act on a pull request, shared by every screen
// that shows one. Each asks before it changes anything.
func (a *App) prKeys(repo string, number int, title string) []Binding {
	label := fmt.Sprintf("%s #%d", a.short(repo), number)
	about := []string{sWhite.Render(label) + sDim.Render(" · "+title)}
	if n := len(a.pendingFor(repo, number)); n > 0 {
		about = append(about, sAmber.Render(fmt.Sprintf("with %d pending comments", n)))
	}
	return grouped("A PULL REQUEST",
		Binding{Keys: []string{"a"}, Desc: "approve", Mutates: true, Need: gh.CapComment, Repo: repo, Do: do(func() tea.Cmd {
			return confirm("Approve pull request?", append(about, "", "Your approving review will be submitted."), "Approve", false, func() tea.Cmd {
				return a.review(repo, number, "APPROVE", "", "Approved "+label)
			})
		})},
		Binding{Keys: []string{"C"}, Help: "request changes", Mutates: true, Need: gh.CapComment, Repo: repo, Do: do(func() tea.Cmd {
			return askEditor("Request changes", about, "what needs to change", "", "submits the review", true, func(text string) tea.Cmd {
				return a.review(repo, number, "REQUEST_CHANGES", text, "Requested changes on "+label)
			})
		})},
		Binding{Keys: []string{"m"}, Desc: "comment", Mutates: true, Need: gh.CapComment, Repo: repo, Do: do(func() tea.Cmd {
			return askEditor("Comment", about, "your comment", "", "posts it", true, func(text string) tea.Cmd {
				if len(a.pendingFor(repo, number)) > 0 {
					return a.review(repo, number, "COMMENT", text, "Reviewed "+label)
				}
				return a.actThen(repo, "Commented on "+label, func(ctx context.Context) error {
					return a.client.CommentOnPullRequest(ctx, repo, number, text)
				}, func() tea.Cmd { a.earnReview("COMMENT", 1); return nil })
			})
		})},
		Binding{Keys: []string{"M"}, Desc: "merge", Help: "merge: pick how, then confirm", Mutates: true, Need: gh.CapWrite, Repo: repo,
			Do: do(func() tea.Cmd { return a.mergePR(repo, number, title) })},
		a.watchPRKey(repo, number),
	)
}

// mergePR asks how to merge (as far as the repo allows it), then to confirm.
func (a *App) mergePR(repo string, number int, title string) tea.Cmd {
	label := fmt.Sprintf("%s #%d", a.short(repo), number)
	return func() tea.Msg {
		d, err := a.client.PullRequestDetails(context.Background(), repo, number)
		if err != nil {
			return flashMsg{nError, "Could not check the pull request: " + err.Error()}
		}
		if len(d.MergeMethods) == 0 {
			return flashMsg{nHint, "This repo allows no way to merge from here."}
		}
		items := make([]pickItem, len(d.MergeMethods))
		for i, m := range d.MergeMethods {
			items[i] = pickItem{value: m, label: m, note: sDim.Render(mergeMethodNote[m])}
		}
		return modalMsg{newPicker("merge", "Merge "+label, "", items, func(method string) tea.Cmd {
			lines := []string{sWhite.Render(label) + sDim.Render(" · "+title), sDim.Render(d.Branch + " → " + d.Base)}
			if warn := mergeWarning(d); warn != "" {
				lines = append(lines, "", sAmber.Render(warn))
			}
			lines = append(lines, "", fmt.Sprintf("It will be merged into %s with %s.", d.Base, method))
			return confirm("Merge pull request?", lines, "Merge", true, func() tea.Cmd {
				return a.actThen(repo, "Merged "+label, func(ctx context.Context) error {
					return a.client.MergePullRequest(ctx, repo, number, method)
				}, func() tea.Cmd { a.earnMerge(time.Now(), &d); return nil })
			})
		})}
	}
}

var mergeMethodNote = map[string]string{
	"squash": "one commit on the base branch",
	"merge":  "a merge commit, keeping every commit",
	"rebase": "every commit replayed on the base branch",
}

// mergeWarning explains what GitHub says may stop the merge.
func mergeWarning(d gh.PullRequestDetail) string {
	switch {
	case d.Mergeable == "CONFLICTING" || d.MergeState == "DIRTY":
		return "It has merge conflicts; GitHub will refuse."
	case d.MergeState == "BLOCKED":
		return "Required reviews or checks aren't satisfied; GitHub may refuse."
	case d.MergeState == "BEHIND":
		return "It is behind its base branch; the repo may require updating it first."
	case d.MergeState == "UNSTABLE":
		return "Some checks are failing."
	case d.Draft:
		return "It is a draft; GitHub will refuse."
	}
	return ""
}

// reviewState describes a review as words in its colour.
func reviewState(state string) string {
	switch state {
	case "APPROVED":
		return sGreen.Render("✓ approved")
	case "CHANGES_REQUESTED":
		return sRed.Render("✗ changes requested")
	case "COMMENTED":
		return sText.Render("commented")
	case "DISMISSED":
		return sDim.Render("dismissed")
	}
	return sDim.Render(strings.ToLower(state))
}

// review submits one review with the line comments you have added to it
// (there may be none), then forgets them.
func (a *App) review(repo string, number int, event, text, done string) tea.Cmd {
	pend := a.pendingFor(repo, number)
	if n := len(pend); n > 0 {
		done += fmt.Sprintf(" with %d comments", n)
	}
	return a.actThen(repo, done, func(ctx context.Context) error {
		return a.client.ReviewWithComments(ctx, repo, number, event, text, pend)
	}, func() tea.Cmd {
		if len(pend) > 0 {
			a.setPending(repo, number, nil)
		}
		a.earnReview(event, 1)
		return nil
	})
}

// newKeys adds the key that opens a pull request for your branch, on your own list started in a checkout.
func (v *pullsView) newKeys(a *App, bs ...Binding) []Binding {
	if v.repo == "" && a.opts.FromCwd && a.opts.StartRepo != "" && a.opts.Branch != "" {
		bs = append(bs, a.newPRKey(a.opts.StartRepo))
	}
	return bs
}
