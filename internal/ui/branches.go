package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// branchesView lists the branches of a repo against the default one: how far
// ahead and behind, the pull request each has and how its checks are doing.
type branchesView struct {
	id       int
	repo     string
	t        *Table
	branches []gh.Branch
	loadState
}

// staleBranchDays is how long without a commit makes a branch stale.
const staleBranchDays = 90

type branchesMsg struct {
	id  int
	bs  []gh.Branch
	err error
}

func (m branchesMsg) viewID() int { return m.id }

func newBranchesView(a *App, repo string) *branchesView {
	v := &branchesView{id: a.newID(), repo: repo}
	v.t = newTable(
		Column{Title: "BRANCH", Max: 40, Flex: true, MidCut: true},
		Column{Title: "AHEAD/BEHIND", Help: "↑ the commits it has that the default branch lacks, ↓ the commits of the default branch it lacks"},
		Column{Title: "PR", Help: "its pull request"},
		Column{Title: "CI", Help: "how the checks of its last commit did"},
		Column{Title: "WHO", Max: 18, Flex: true, Drop: 2, Help: "who made its last commit"},
		Column{Title: "AGE", Right: true, Min: 3, Help: "how long ago its last commit was made"},
	)
	v.t.Empty = "Loading branches…"
	var bs []gh.Branch
	if at, ok := a.client.Recall(v.snapshot(), &bs); ok {
		v.branches, v.at = bs, at
	}
	return v
}

func (v *branchesView) snapshot() string { return "branches:" + v.repo }

func (v *branchesView) ID() int                  { return v.id }
func (v *branchesView) Crumb() string            { return "branches" }
func (v *branchesView) Init(a *App) tea.Cmd      { return nil }
func (v *branchesView) Updated(a *App) time.Time { return v.at }
func (v *branchesView) Loading(a *App) bool      { return v.busy }

func (v *branchesView) defaultBranch(a *App) string {
	if r, ok := a.repoByName(v.repo); ok && r.DefaultBranch != "" {
		return r.DefaultBranch
	}
	for _, b := range v.branches {
		if b.Default {
			return b.Name
		}
	}
	return "main"
}

func (v *branchesView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(time.Minute) {
		return nil
	}
	if !v.begin() {
		return nil
	}
	id, repo, key, def := v.id, v.repo, v.snapshot(), v.defaultBranch(a)
	return func() tea.Msg {
		bs, err := a.client.Branches(context.Background(), repo, def)
		if err == nil {
			a.client.Remember(key, bs)
		}
		return branchesMsg{id, bs, err}
	}
}

func (v *branchesView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case branchesMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the branches of "+v.repo+": "+msg.err.Error())
		}
		v.branches = msg.bs
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

func (v *branchesView) selected() (gh.Branch, bool) {
	r := v.t.Selected()
	if r == nil {
		return gh.Branch{}, false
	}
	for _, b := range v.branches {
		if b.Name == r.Key {
			return b, true
		}
	}
	return gh.Branch{}, false
}

// MenuTitle names the selected branch for the action menu.
func (v *branchesView) MenuTitle(a *App) string {
	if b, ok := v.selected(); ok {
		return b.Name
	}
	return ""
}

// branchCanGo says a branch has been merged and nothing keeps it: not the
// default branch, not protected, and its pull request is merged.
func branchCanGo(b gh.Branch) bool {
	return !b.Default && !b.Protected && b.PR != nil && b.PR.State == "MERGED"
}

func branchStale(b gh.Branch, now time.Time) bool {
	return !b.Default && !b.At.IsZero() && now.Sub(b.At) > staleBranchDays*24*time.Hour
}

func (v *branchesView) merged() []gh.Branch {
	var out []gh.Branch
	for _, b := range v.branches {
		if branchCanGo(b) {
			out = append(out, b)
		}
	}
	return out
}

func (v *branchesView) Keys(a *App) []Binding {
	var bs []Binding
	if b, ok := v.selected(); ok {
		delReason := ""
		switch {
		case b.Default:
			delReason = "this is the default branch"
		case b.Protected:
			delReason = "this branch is protected"
		}
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "compare", Help: "compare with the default branch",
				Off: b.Default, Do: do(func() tea.Cmd { return a.push(newCompareView(a, v.repo, v.defaultBranch(a), b.Name)) })},
			Binding{Keys: []string{"+"}, Desc: "new pull request", Help: "open a pull request from this branch", Off: b.Default || (b.PR != nil && b.PR.State == "OPEN"),
				Mutates: true, Need: gh.CapComment, Repo: v.repo, Do: do(func() tea.Cmd { return a.newPR(v.repo, b.Name, a.inCheckout(v.repo) && a.opts.Branch == b.Name) })},
			Binding{Keys: []string{"X"}, Desc: "delete branch", Help: "delete the branch", Off: delReason != "", Mutates: true, Need: gh.CapWrite, Repo: v.repo,
				Do: do(func() tea.Cmd {
					if delReason != "" {
						return a.flashCmd(nHint, "Can't delete it: "+delReason+".")
					}
					return a.deleteBranches(v.repo, []gh.Branch{b})
				})},
			Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(v.repo + "/tree/" + b.Name)) })},
			Binding{Keys: []string{"y"}, Desc: "copy name", Help: "copy the branch name", Do: do(func() tea.Cmd { return a.copyText(b.Name, "branch name") })},
		)
	}
	if m := v.merged(); len(m) > 0 {
		bs = append(bs, Binding{Help: fmt.Sprintf("Delete merged branches (%d)…", len(m)), Mutates: true, Need: gh.CapWrite, Repo: v.repo,
			Do: do(func() tea.Cmd { return a.deleteBranches(v.repo, m) })})
	}
	bs = append(bs, filterHint, v.t.moveKeys())
	return grouped("BRANCHES", bs...)
}

// deleteBranches asks, listing what goes, then deletes the branches.
func (a *App) deleteBranches(repo string, bs []gh.Branch) tea.Cmd {
	var names []string
	for _, b := range bs {
		names = append(names, b.Name)
	}
	lines := []string{sWhite.Render(a.short(repo)) + sDim.Render(fmt.Sprintf(" · %d branches", len(bs))), ""}
	for i, n := range names {
		if i == 8 {
			lines = append(lines, sDim.Render(fmt.Sprintf("… and %d more", len(names)-8)))
			break
		}
		lines = append(lines, "  "+sText.Render(n))
	}
	lines = append(lines, "", "Their commits stay in the pull requests that merged them.")
	title := "Delete the branch?"
	if len(bs) > 1 {
		title = fmt.Sprintf("Delete %d branches?", len(bs))
	}
	return confirm(title, lines, "Delete", true, func() tea.Cmd {
		return a.act(repo, fmt.Sprintf("Deleted %s", strings.Join(names, ", ")), func(ctx context.Context) error {
			for _, n := range names {
				if err := a.client.DeleteBranch(ctx, repo, n); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func prChip(p *gh.BranchPR) Cell {
	switch {
	case p == nil:
		return Cell{Text: "–", Styled: sDim.Render("–")}
	case p.State == "MERGED":
		return iconCell("pr", fmt.Sprintf("merged #%d", p.Number), sText, cViolet)
	case p.State == "CLOSED":
		return iconCell("pr", fmt.Sprintf("closed #%d", p.Number), sDim, cRed)
	case p.Draft:
		return iconCell("pr", fmt.Sprintf("draft #%d", p.Number), sDim, cGrey)
	}
	return iconCell("pr", fmt.Sprintf("open #%d", p.Number), sText, cGreen)
}

func ciChip(state string) Cell {
	switch state {
	case "SUCCESS":
		return statusCell(KOK, 0, "")
	case "FAILURE", "ERROR":
		return statusCell(KFail, 0, "")
	case "PENDING", "EXPECTED":
		return statusCell(KRun, 0, "")
	}
	return Cell{Text: "–", Styled: sDim.Render("–")}
}

func (v *branchesView) rebuild(a *App) {
	now := time.Now()
	var rows []Row
	for _, b := range v.branches {
		name := icon("branch") + b.Name
		styled := ico("branch") + sText.Render(b.Name)
		switch {
		case b.Default:
			name, styled = icon("star")+b.Name, ico("star")+sWhite.Bold(true).Render(b.Name)
		case b.Protected:
			name = icon("lock") + b.Name
			styled = icoIn("lock", cSteel) + sText.Render(b.Name)
		}
		ab := fmt.Sprintf("↑%d ↓%d", b.Ahead, b.Behind)
		abCell := Cell{Text: ab, Styled: sGreen.Render(fmt.Sprintf("↑%d", b.Ahead)) + " " + sAmber.Render(fmt.Sprintf("↓%d", b.Behind))}
		if b.Default {
			abCell = Cell{Text: " ", Styled: " "}
		}
		rows = append(rows, Row{Key: b.Name, Kind: KOK, Cells: []Cell{
			{Text: name, Styled: styled}, abCell, prChip(b.PR), ciChip(b.Checks), iconText("user", b.Author, false), steelCell(age(b.At, now), false)}})
	}
	v.t.Empty = "No branches."
	if v.busy && len(rows) == 0 {
		v.t.Empty = "Loading branches…"
	}
	v.t.SetRows(rows)
}

func (v *branchesView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *branchesView) FilterValue() string          { return v.t.Filter() }
func (v *branchesView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *branchesView) sync(a *App)                  { v.rebuild(a) }

func (v *branchesView) Render(a *App, w, h int) string {
	now := time.Now()
	stale := 0
	for _, b := range v.branches {
		if branchStale(b, now) {
			stale++
		}
	}
	var chips []string
	if n := len(v.merged()); n > 0 {
		chips = append(chips, statusChip(KOK, 0, fmt.Sprintf("%d merged, safe to delete", n)))
	}
	if stale > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d stale", stale)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("branch", "Branches", "against "+v.defaultBranch(a), v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

func (v *branchesView) panel(a *App, w, h int) []string {
	b, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "No branch selected.")
	}
	now := time.Now()
	p := newPanel(w)
	p.add(ico("branch") + sWhite.Bold(true).Render(b.Name))
	if b.Default {
		p.add(sDim.Render("the default branch"))
	}
	if b.Protected {
		p.add(sDim.Render(icon("lock") + "protected"))
	}
	p.label("Last commit")
	p.wrap(b.Subject, sText, 3)
	p.add(sDim.Render(b.Author + " · " + age(b.At, now) + " ago"))
	if c := ciChip(b.Checks); b.Checks != "" {
		p.add(c.Styled + sDim.Render(" its checks"))
	}
	if !b.Default {
		p.label("Compared with " + v.defaultBranch(a))
		p.add(sGreen.Render(fmt.Sprintf("↑ %d ahead", b.Ahead)) + sDim.Render("  ·  ") + sAmber.Render(fmt.Sprintf("↓ %d behind", b.Behind)))
		switch {
		case branchCanGo(b):
			p.add(sGreen.Render("merged: safe to delete") + sDim.Render(" · ") + keycap("X") + sDim.Render(" deletes it"))
		case branchStale(b, now):
			p.add(sAmber.Render(fmt.Sprintf("no commit for %d days", int(now.Sub(b.At).Hours()/24))))
		}
	}
	p.label("Pull request")
	switch {
	case b.PR == nil && !b.Default:
		p.add(sDim.Render("none yet · ") + keycap("+") + sDim.Render(" opens one"))
	case b.PR != nil:
		p.add(prChip(b.PR).Styled)
	default:
		p.add(sDim.Render("–"))
	}
	var runs []gh.Run
	for _, r := range a.st.status[v.repo] {
		if r.HeadBranch == b.Name && len(runs) < 4 {
			runs = append(runs, r)
		}
	}
	if len(runs) > 0 {
		p.label("Runs on this branch")
		for _, r := range runs {
			k := StatusKind(r.Status, r.Conclusion)
			p.row(glyph(k, a.frame)+" "+sText.Render(fmt.Sprintf("%s #%d", r.Name, r.RunNumber)), sDim.Render(age(r.CreatedAt, now)))
		}
	}
	return p.render(h)
}
