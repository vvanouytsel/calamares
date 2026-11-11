package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// fuzzy scores how well pattern matches s as a case-insensitive subsequence.
// Matches at the start of words and consecutive runs score higher.
func fuzzy(pattern, s string) (score int, idx []int, ok bool) {
	if pattern == "" {
		return 0, nil, true
	}
	p := []rune(strings.ToLower(pattern))
	r := []rune(s)
	lr := []rune(strings.ToLower(s))
	pi := 0
	prev := -2
	for i := 0; i < len(lr) && pi < len(p); i++ {
		if lr[i] != p[pi] {
			continue
		}
		score += 10
		if i == prev+1 {
			score += 15
		}
		if i == 0 || !unicode.IsLetter(r[i-1]) && !unicode.IsDigit(r[i-1]) {
			score += 8
		}
		if prev >= 0 {
			score -= min(i-prev-1, 10)
		}
		idx = append(idx, i)
		prev = i
		pi++
	}
	if pi < len(p) {
		return 0, nil, false
	}
	score -= len(r) / 6
	return score, idx, true
}

func highlight(s string, idx []int, base lipgloss.Style) string {
	if len(idx) == 0 {
		return base.Render(s)
	}
	set := map[int]bool{}
	for _, i := range idx {
		set[i] = true
	}
	hl := lipgloss.NewStyle().Foreground(cAmber).Bold(true).Underline(true)
	var b strings.Builder
	for i, r := range []rune(s) {
		if set[i] {
			b.WriteString(hl.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

type suggKind int

const (
	sugCommand suggKind = iota
	sugPR
	sugRepo
	sugWorkflow
	sugSearch
	sugPlace
	sugIssue
	sugPerson
)

type suggestion struct {
	kind  suggKind
	label string
	desc  string
	repo  string
	wf    gh.Workflow
	pr    gh.PullRequest
	issue gh.Issue
	cmd   string
	score int
	idx   []int
	fav   bool
}

var commands = []struct{ name, desc string }{
	{"pulls", "pull requests involving you · :pr new opens one for your branch"},
	{"actions", "what fails and runs across your repos"},
	{"platform", "shared workflows and actions, warnings, schedules, runners and runner time"},
	{"repositories", "your repositories and how their default branch is doing"},
	{"runs", "the current repo (or :runs owner/repo)"},
	{"filter", "save a filter for runs on every screen, e.g. :filter -branch:dependabot/* (empty clears it)"},
	{"issues", "issues involving you"},
	{"issue", "open a new issue: :issue new"},
	{"watching", "the runs you watch"},
	{"access", "what your token can do"},
	{"status", "GitHub status: incidents and what they did to your runs"},
	{"theme", "switch the color theme: :theme opens the picker, :theme neon sets one"},
	{"help", "all keys"},
	{"quit", "leave Calamares"},
}

type palette struct {
	items []suggestion
	sel   int
}

// openSearch opens the search box. With list set, on a screen with a list,
// it filters that list as you type; otherwise it's the go-to palette for
// repos, workflows, pull requests and commands.
func (a *App) openSearch(list bool) tea.Cmd {
	a.mode = modeSearch
	a.listSearch = false
	a.savedFilter = ""
	a.suggest = suggestState{}
	a.input.SetValue("")
	a.input.Placeholder = "repo, workflow, pull request, @person or :command"
	if f, ok := a.top().(Filterer); ok && list {
		a.listSearch = true
		a.savedFilter = f.FilterValue()
		a.input.Placeholder = "type to filter this list"
	}
	a.pal.sel = 0
	if !a.listSearch {
		a.pal.update(a, a.input.Value())
	}
	return a.input.Focus()
}

func (a *App) closeSearch(restore bool) {
	if f, ok := a.top().(Filterer); ok && a.listSearch && restore {
		f.SetFilter(a, a.savedFilter)
	}
	a.mode = modeNormal
	a.listSearch = false
	a.input.Blur()
}

func (p *palette) update(a *App, q string) {
	q = strings.TrimSpace(strings.TrimPrefix(q, ":"))
	first := ""
	if f := strings.Fields(q); len(f) > 0 {
		first = f[0]
	}
	var items []suggestion
	for i, name := range tabNames {
		if s, idx, ok := fuzzy(q, name); ok && (q == "" || s > 20) {
			items = append(items, suggestion{kind: sugPlace, label: name, desc: icon(tabIcons[i]) + string(rune('1'+i)), cmd: fmt.Sprint(i), score: s + 40, idx: idx})
		}
	}
	for _, c := range commands {
		if s, idx, ok := fuzzy(first, c.name); ok && (q == "" || s > 20) {
			items = append(items, suggestion{kind: sugCommand, label: ":" + c.name, desc: c.desc, cmd: c.name, score: s + 25, idx: shift(idx, 1)})
		}
	}
	items = append(items, a.peopleMatching(q)...)
	for _, pr := range a.st.prs {
		label := fmt.Sprintf("%s #%d %s", a.short(pr.Repo), pr.Number, pr.Title)
		if s, idx, ok := fuzzy(q, label); ok && q != "" {
			items = append(items, suggestion{kind: sugPR, label: label, repo: pr.Repo, pr: pr, score: s, idx: idx})
		}
	}
	for _, is := range a.st.allIssues() {
		label := fmt.Sprintf("%s #%d %s", a.short(is.Repo), is.Number, is.Title)
		hit := fmt.Sprintf("#%d", is.Number) == q || fmt.Sprintf("%s#%d", a.short(is.Repo), is.Number) == q
		if s, idx, ok := fuzzy(q, label); (ok || hit) && q != "" {
			if hit {
				s += 40
			}
			items = append(items, suggestion{kind: sugIssue, label: label, repo: is.Repo, issue: is, score: s - 3, idx: idx})
		}
	}
	for _, r := range a.repos {
		if s, idx, ok := fuzzyTail(q, r.FullName, len(r.FullName)-len(r.Name)); ok {
			fav := a.cfg.IsFavourite(r.FullName)
			if fav {
				s += 15
			}
			if r.HasActions {
				s += 8
			} else if q == "" {
				continue
			}
			desc := ""
			if run, ok := a.st.latest(r.FullName); ok {
				k := StatusKind(run.Status, run.Conclusion)
				desc = k.Glyph(0) + " " + run.Name + " " + age(run.CreatedAt, time.Now())
			}
			items = append(items, suggestion{kind: sugRepo, label: r.FullName, repo: r.FullName, desc: desc, score: s, idx: idx, fav: fav})
		}
	}
	for repo, wfs := range a.workflows {
		for _, w := range wfs {
			label := repo + " › " + w.Name
			if s, idx, ok := fuzzyTail(q, label, len([]rune(label))-len([]rune(w.Name))); ok && q != "" {
				items = append(items, suggestion{kind: sugWorkflow, label: label, repo: repo, wf: w, score: s - 5, idx: idx})
			}
		}
	}
	if q == "" {
		for i := range items {
			if items[i].kind == sugRepo && !items[i].fav {
				items[i].score = -i
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	if q != "" && len(items) > 0 && items[0].score > 0 {
		keep := items[:0]
		for i, it := range items {
			if i < 8 || it.score*3 >= items[0].score {
				keep = append(keep, it)
			}
		}
		items = keep
	}
	if len(items) > 12 {
		items = items[:12]
	}
	// Repositories found by searching all of GitHub, beyond your own list.
	if q != "" && strings.EqualFold(a.search.query, q) {
		known := map[string]bool{}
		for _, r := range a.repos {
			known[strings.ToLower(r.FullName)] = true
		}
		n := 0
		for _, r := range a.search.results {
			if known[strings.ToLower(r.FullName)] || n >= 5 {
				continue
			}
			_, idx, _ := fuzzyTail(q, r.FullName, len(r.FullName)-len(r.Name))
			desc := fmt.Sprintf("★ %s", thousands(r.Stars))
			if r.Private {
				desc += " · private"
			}
			items = append(items, suggestion{kind: sugSearch, label: r.FullName, repo: r.FullName, desc: desc, idx: idx, score: -1000})
			n++
		}
	}
	// Group by kind, groups ordered by their best match, so headers appear once.
	rank := map[suggKind]int{}
	for _, it := range items {
		if _, seen := rank[it.kind]; !seen {
			rank[it.kind] = len(rank)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return rank[items[i].kind] < rank[items[j].kind] })
	p.items = items
	if p.sel >= len(items) {
		p.sel = 0
	}
}

// fuzzyTail prefers matches inside the last part of s (the repo or workflow
// name, starting at rune offset tail) over matches spread across the owner.
func fuzzyTail(q, s string, tail int) (int, []int, bool) {
	if q == "" {
		return 0, nil, true
	}
	r := []rune(s)
	if tail > 0 && tail < len(r) {
		if sc, idx, ok := fuzzy(q, string(r[tail:])); ok {
			return sc + 10, shift(idx, tail), true
		}
	}
	return fuzzy(q, s)
}

func shift(idx []int, n int) []int {
	out := make([]int, len(idx))
	for i, v := range idx {
		out[i] = v + n
	}
	return out
}

var groupNames = map[suggKind]string{
	sugCommand: "GO TO", sugPR: "YOUR PULL REQUESTS", sugRepo: "YOUR REPOS",
	sugWorkflow: "WORKFLOWS", sugSearch: "ON GITHUB", sugPlace: "PLACES", sugIssue: "ISSUES", sugPerson: "PEOPLE",
}

func (p *palette) render(a *App, w int) string {
	if len(p.items) == 0 {
		msg := "No matches. Try a repo name, owner/name, a workflow, or :repos :pulls :actions"
		if a.search.busy {
			msg = "Searching GitHub…"
		}
		return frame("", " "+sDim.Render(msg), w, 3, cCyan)
	}
	var lines []string
	last := suggKind(-1)
	for i, it := range p.items {
		if it.kind != last {
			lines = append(lines, " "+sDim.Render(groupNames[it.kind]))
			last = it.kind
		}
		star := "  "
		if it.fav {
			star = sAmber.Render("★ ")
		}
		if i == p.sel {
			txt := " ▸ " + it.label
			if it.desc != "" {
				txt += "   " + it.desc
			}
			lines = append(lines, sSel.Bold(true).Render(fit(txt, w-2)))
			continue
		}
		line := "   " + star + highlight(it.label, it.idx, sRow)
		if it.desc != "" {
			line += "   " + sDim.Render(it.desc)
		}
		lines = append(lines, line)
	}
	if a.search.busy {
		lines = append(lines, " "+sDim.Render("searching GitHub…"))
	}
	return frame("", strings.Join(lines, "\n"), w, len(lines)+2, cCyan)
}

func (a *App) renderPrompt() string {
	if f, ok := a.top().(Filterer); ok && a.listSearch {
		info := f.FilterInfo(a)
		label, help := "Filter ›", queryHelp(a.input.Value(), info.fields, a.me)
		if comp := a.suggest.current(a.input.Value(), info); len(comp.options) > 0 {
			help = a.suggest.strip(comp, a.w-4)
		}
		count := sText.Render(fmt.Sprintf("showing %s of %s", thousands(info.shown), thousands(info.total)))
		if len(info.fields) == 0 {
			label, help = "Search log ›", sDim.Render("after enter: n / N jump to the next / previous match")
			count = sText.Render(fmt.Sprintf("%s matches", thousands(info.shown)))
		}
		keys := sDim.Render("   enter keep · esc undo")
		a.input.Width = max(a.w-width(count)-width(keys)-20, 10)
		inner := " " + sCyan.Bold(true).Render(label) + " " + a.input.View()
		pad := a.w - 2 - width(inner) - width(count) - width(keys) - 1
		line := inner + strings.Repeat(" ", max(pad, 1)) + count + keys
		return frame("", line+"\n "+help, a.w, 4, cCyan)
	}
	label := sCyan.Bold(true).Render("Go to ›")
	hint := sDim.Render("↑↓ choose · enter go · esc close")
	a.input.Width = max(a.w-width(hint)-14, 10)
	inner := " " + label + " " + a.input.View()
	pad := a.w - 2 - width(inner) - width(hint) - 1
	return frame("", inner+strings.Repeat(" ", max(pad, 1))+hint, a.w, 3, cCyan)
}

func (a *App) updateSearch(msg tea.KeyMsg) tea.Cmd {
	if a.listSearch {
		return a.updateListFilter(msg)
	}
	switch msg.String() {
	case "esc":
		a.closeSearch(true)
		return nil
	case "up", "ctrl+p", "shift+tab":
		if a.pal.sel > 0 {
			a.pal.sel--
		}
		return nil
	case "down", "ctrl+n", "tab":
		if a.pal.sel < len(a.pal.items)-1 {
			a.pal.sel++
		}
		return nil
	case "enter":
		line := a.input.Value()
		a.closeSearch(true)
		if strings.TrimSpace(strings.TrimPrefix(line, ":")) == "" {
			return nil
		}
		return a.execute(line)
	}
	var cmd tea.Cmd
	before := a.input.Value()
	a.input, cmd = a.input.Update(msg)
	if v := a.input.Value(); v != before {
		a.pal.sel = 0
		a.pal.update(a, v)
		return tea.Batch(cmd, a.scheduleSearch(v))
	}
	return cmd
}

// updateListFilter edits the filter of the list on screen as you type.
func (a *App) updateListFilter(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.closeSearch(true)
		return nil
	case "enter":
		a.closeSearch(false)
		return nil
	}
	if a.completeFilter(msg.String()) {
		return nil
	}
	var cmd tea.Cmd
	before := a.input.Value()
	a.input, cmd = a.input.Update(msg)
	a.suggest.cycling = false
	if v := a.input.Value(); v != before {
		a.suggest = suggestState{}
		if f, ok := a.top().(Filterer); ok {
			f.SetFilter(a, v)
		}
	}
	return cmd
}

// saveRunFilter stores q as the saved run filter and switches it on.
func (a *App) saveRunFilter(q string) tea.Cmd {
	a.cfg.RunFilter, a.filterOff = q, false
	if err := a.cfg.Save(); err != nil {
		return a.flashCmd(nError, "Could not save config: "+err.Error())
	}
	if q == "" {
		return a.flashCmd(nDone, "Saved filter cleared")
	}
	return a.flashCmd(nDone, "Saved filter: "+q+" · F switches it off")
}

// execute runs a typed command line, falling back to the selected suggestion.
func (a *App) execute(line string) tea.Cmd {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), ":"))
	if len(fields) > 0 && strings.HasPrefix(strings.TrimSpace(line), ":") || (len(fields) > 0 && a.pal.sel < len(a.pal.items) && a.pal.items[a.pal.sel].kind == sugCommand) {
		arg := ""
		if len(fields) > 1 {
			arg = fields[1]
		}
		switch fields[0] {
		case "q", "q!", "quit", "exit":
			return tea.Quit
		case "watching", "watches", "watch":
			return a.setTab(tabWatching)
		case "pulls", "prs", "pr":
			if arg == "new" || arg == "create" {
				repo := a.opts.StartRepo
				if repo == "" {
					repo = a.currentRepo()
				}
				if !a.inCheckout(repo) {
					return a.flashCmd(nHint, "A new pull request needs a checkout: start Calamares inside the repo, on your branch.")
				}
				return a.invoke(a.newPRKey(repo), "P")
			}
			return a.setTab(tabPulls)
		case "actions", "activity":
			return a.setTab(tabActions)
		case "platform", "shared":
			return a.setTab(tabPlatform)
		case "repositories", "repos", "repo":
			return a.setTab(tabRepos)
		case "issues":
			return a.setTab(tabIssues)
		case "issue":
			if arg == "new" || arg == "create" {
				return a.newIssue("")
			}
			return a.setTab(tabIssues)
		case "theme", "themes":
			if arg != "" {
				return a.setTheme(arg, true)
			}
			return a.openThemes()
		case "access", "token", "permissions":
			return a.push(newAccessView(a))
		case "status", "incidents", "githubstatus":
			return a.openStatus()
		case "help", "?":
			return a.toggleHelp()
		case "filter":
			return a.saveRunFilter(strings.Join(fields[1:], " "))
		case "runs", "run", "wf", "workflows":
			if repo := a.repoArg(arg); repo != "" {
				return a.openRepo(repo, "", 0)
			}
			return a.flashCmd(nHint, "Which repo? Open one first, or use :runs owner/repo")
		}
	}
	if a.pal.sel < len(a.pal.items) {
		it := a.pal.items[a.pal.sel]
		switch it.kind {
		case sugCommand:
			return a.execute(":" + it.cmd)
		case sugIssue:
			a.tab = tabIssues
			a.stack = []View{a.roots[tabIssues]}
			return a.push(newIssueView(a, it.issue.Repo, it.issue.Number))
		case sugPlace:
			n, _ := strconv.Atoi(it.cmd)
			return a.setTab(n)
		case sugRepo, sugSearch:
			return a.openRepo(it.repo, "", 0)
		case sugPerson:
			return a.openPerson(it.cmd)
		case sugWorkflow:
			return a.openRepo(it.repo, "", it.wf.ID)
		case sugPR:
			a.tab = tabPulls
			a.stack = []View{a.roots[tabPulls]}
			return a.push(newPRView(a, it.pr.Repo, it.pr.Number))
		}
	}
	if len(fields) == 1 && strings.Count(fields[0], "/") == 1 {
		return a.openRepo(fields[0], "", 0)
	}
	if len(fields) == 1 && strings.HasPrefix(fields[0], "@") {
		return a.openPerson(fields[0])
	}
	return a.flashCmd(nHint, "Nothing matches “"+strings.TrimSpace(line)+"”")
}

func (a *App) repoArg(arg string) string {
	if arg != "" {
		if strings.Contains(arg, "/") {
			return arg
		}
		best, bestScore := "", -1<<30
		for _, r := range a.repos {
			if s, _, ok := fuzzy(arg, r.FullName); ok && s > bestScore {
				best, bestScore = r.FullName, s
			}
		}
		return best
	}
	for i := len(a.stack) - 1; i >= 0; i-- {
		if rs, ok := a.stack[i].(RepoScoped); ok {
			return rs.RepoName()
		}
	}
	return ""
}

// repoSearch holds the GitHub-wide search behind the search box.
type repoSearch struct {
	seq     int
	query   string
	results []gh.Repo
	busy    bool
}

type searchTickMsg struct {
	seq   int
	query string
}

type searchResultMsg struct {
	query string
	repos []gh.Repo
}

// scheduleSearch searches GitHub once typing pauses (the search API allows
// 30 requests a minute, so every keystroke would be too many).
func (a *App) scheduleSearch(line string) tea.Cmd {
	q := strings.TrimSpace(strings.TrimPrefix(line, ":"))
	a.search.seq++
	if len(q) < 2 || strings.ContainsAny(q, " :") {
		a.search.busy = false
		return nil
	}
	seq := a.search.seq
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg { return searchTickMsg{seq, q} })
}

func (a *App) runSearch(m searchTickMsg) tea.Cmd {
	if m.seq != a.search.seq || a.mode != modeSearch {
		return nil
	}
	a.search.busy = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		repos, err := a.client.SearchRepos(ctx, m.query, 10)
		if err != nil {
			return searchResultMsg{query: m.query}
		}
		return searchResultMsg{m.query, repos}
	}
}

func (a *App) searchDone(m searchResultMsg) {
	cur := strings.TrimSpace(strings.TrimPrefix(a.input.Value(), ":"))
	if !strings.EqualFold(cur, m.query) {
		return
	}
	a.search.busy = false
	a.search.query, a.search.results = m.query, m.repos
	if a.mode == modeSearch {
		sel := a.pal.sel
		a.pal.update(a, a.input.Value())
		a.pal.sel = min(sel, max(len(a.pal.items)-1, 0))
	}
}

// peopleMatching suggests the pages of people for q: with "@" in front, the
// people you looked at last and the authors of your pull requests and
// issues, and the login as typed.
func (a *App) peopleMatching(q string) []suggestion {
	typed, ok := strings.CutPrefix(q, "@")
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var known []string
	add := func(login string) {
		if login != "" && !seen[strings.ToLower(login)] {
			seen[strings.ToLower(login)] = true
			known = append(known, login)
		}
	}
	for _, p := range a.people {
		add(p)
	}
	for _, pr := range a.st.prs {
		add(pr.Author)
	}
	for _, is := range a.st.allIssues() {
		add(is.Author)
	}
	var out []suggestion
	for i, login := range known {
		if s, idx, ok := fuzzy(typed, login); ok || typed == "" {
			score := s + 60
			if i < len(a.people) {
				score += 20 // looked at lately
			}
			out = append(out, suggestion{kind: sugPerson, label: "@" + login, desc: "their page", cmd: login, score: score, idx: shift(idx, 1)})
		}
	}
	if typed != "" && !seen[strings.ToLower(typed)] && gh.ValidLogin(typed) {
		out = append(out, suggestion{kind: sugPerson, label: "@" + typed, desc: "their page", cmd: typed, score: 50})
	}
	return out
}
