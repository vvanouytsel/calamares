package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// personWindow is how far back a person's page looks.
const personWindow = 14 * 24 * time.Hour

// The views of a person's page.
const (
	personActivity = iota
	personPulls
	personRuns
	personCommits
	personReviews
	personIssues
)

var personViews = []struct{ label, icon string }{
	{"Activity", "clock"}, {"Pull requests", "pr"}, {"Runs", "actions"}, {"Commits", "commit"}, {"Reviews", "eye"}, {"Issues", "issue"},
}

// personView is what one person did lately in the repos you can see: their
// pull requests, runs, commits, reviews and issues, newest first.
type personView struct {
	id    int
	login string
	view  int
	// mine keeps only what involves you: their pull requests that ask for
	// your review, and their reviews of yours.
	mine bool
	t    *Table
	p    gh.Person
	rows []personEntry
	loadState
}

// personEntry is one line of a person's page.
type personEntry struct {
	key  string
	at   time.Time
	kind int // the view it belongs to
	verb string
	k    Kind // how it went, which colours the verb
	// merged colours the verb violet, as GitHub shows a merged pull request.
	merged bool
	repo   string
	ref    string // #57, CI #128, 3 commits
	text   string // the title, the branch or the commit message
	url    string

	pr      *gh.PersonPR
	run     *repoRun
	issue   *gh.PersonIssue
	commits []gh.PersonCommit
}

type personMsg struct {
	id  int
	p   gh.Person
	err error
}

func (m personMsg) viewID() int { return m.id }

func newPersonView(a *App, login string) *personView {
	v := &personView{id: a.newID(), login: login}
	v.t = newTable(
		Column{Title: "AGE", Right: true, Min: 3, Help: "how long ago it happened"},
		Column{Title: "WHAT", Min: 12, Help: "what they did: opened, merged or closed a pull request or issue, started a run (and how it went), pushed commits, approved or asked for changes"},
		Column{Title: "REPO", Max: 24, Flex: true, MidCut: true, Help: "the repository"},
		Column{Title: "TITLE", Max: 70, Flex: true, Below: true, Help: "the title of the pull request or issue, the title of the run, or the first line of the commit"},
	)
	v.t.Empty = "Loading what " + login + " did…"
	if at, ok := a.client.Recall(v.snapshot(), &v.p); ok {
		v.at = at
	}
	a.rememberPerson(login)
	return v
}

func (v *personView) snapshot() string { return "person:" + strings.ToLower(v.login) }

func (v *personView) ID() int             { return v.id }
func (v *personView) Crumb() string       { return v.login }
func (v *personView) Init(a *App) tea.Cmd { return v.Refresh(a, false) }

func (v *personView) Updated(a *App) time.Time { return v.at }
func (v *personView) Loading(a *App) bool      { return v.busy }

// Refresh reads the person again when asked, or when what we hold is older
// than staleAfter.
func (v *personView) Refresh(a *App, force bool) tea.Cmd {
	if !force && time.Since(v.at) < staleAfter || !v.begin() {
		return nil
	}
	id, login, key := v.id, v.login, v.snapshot()
	return func() tea.Msg {
		p, err := a.client.PersonActivity(context.Background(), login, time.Now().Add(-personWindow))
		if err == nil {
			a.client.Remember(key, p)
		}
		return personMsg{id, p, err}
	}
}

func (v *personView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case personMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read what "+v.login+" did: "+msg.err.Error())
		}
		v.p = msg.p
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

// name is how the page calls the person: their name when GitHub has one.
func (v *personView) name() string {
	if v.p.Name != "" {
		return v.p.Name
	}
	return v.login
}

// entries are everything the person did in the window, newest first, each
// with the view it belongs to.
func (v *personView) entries(a *App, now time.Time) []personEntry {
	since := now.Add(-personWindow)
	var out []personEntry
	add := func(e personEntry) {
		if !e.at.Before(since) {
			out = append(out, e)
		}
	}
	forYou := map[string]bool{}
	for _, p := range v.p.ForYou {
		forYou[fmt.Sprintf("%s#%d", p.Repo, p.Number)] = true
	}
	for i := range v.p.Opened {
		p := &v.p.Opened[i]
		key := fmt.Sprintf("%s#%d", p.Repo, p.Number)
		if v.mine && !forYou[key] {
			continue
		}
		ref := fmt.Sprintf("#%d", p.Number)
		add(personEntry{key: "opened " + key, at: p.CreatedAt, kind: personPulls, verb: "opened", k: KOK, repo: p.Repo, ref: ref, text: p.Title, url: p.URL, pr: p})
		switch {
		case p.State == "MERGED":
			add(personEntry{key: "merged " + key, at: p.MergedAt, kind: personPulls, verb: "merged", k: KOK, merged: true, repo: p.Repo, ref: ref, text: p.Title, url: p.URL, pr: p})
		case p.State == "CLOSED":
			add(personEntry{key: "closed " + key, at: p.ClosedAt, kind: personPulls, verb: "closed", k: KFail, repo: p.Repo, ref: ref, text: p.Title, url: p.URL, pr: p})
		}
	}
	for i := range v.p.Reviewed {
		p := &v.p.Reviewed[i]
		if p.Reviewed.IsZero() || v.mine && !strings.EqualFold(p.Author, a.me) {
			continue
		}
		verb, k := "commented on", KSkip
		switch p.Review {
		case "APPROVED":
			verb, k = "approved", KOK
		case "CHANGES_REQUESTED":
			verb, k = "asked changes", KWait
		}
		add(personEntry{key: fmt.Sprintf("review %s#%d", p.Repo, p.Number), at: p.Reviewed, kind: personReviews, verb: verb, k: k,
			repo: p.Repo, ref: fmt.Sprintf("#%d", p.Number), text: p.Title, url: p.URL, pr: p})
	}
	if !v.mine {
		for i := range v.p.Issues {
			is := &v.p.Issues[i]
			key := fmt.Sprintf("%s#%d", is.Repo, is.Number)
			add(personEntry{key: "issue " + key, at: is.CreatedAt, kind: personIssues, verb: "opened", k: KOK, repo: is.Repo,
				ref: fmt.Sprintf("#%d", is.Number), text: is.Title, url: is.URL, issue: is})
		}
		for _, x := range recentRuns(a, a.ciRepoNames()) {
			x := x
			if !strings.EqualFold(x.run.Actor.Login, v.login) && !strings.EqualFold(x.run.TriggeringActor.Login, v.login) {
				continue
			}
			k := StatusKind(x.run.Status, x.run.Conclusion)
			add(personEntry{key: fmt.Sprintf("run %d", x.run.ID), at: x.run.CreatedAt, kind: personRuns, verb: runWord(k), k: k, repo: x.repo,
				ref: fmt.Sprintf("%s #%d", x.run.Name, x.run.RunNumber), text: x.run.DisplayTitle, url: x.run.HTMLURL, run: &x})
		}
		out = append(out, v.commitEntries()...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	return out
}

// commitEntries puts the commits pushed to one repo on one day on a line:
// "pushed 3 commits".
func (v *personView) commitEntries() []personEntry {
	var out []personEntry
	at := map[string]int{}
	for _, c := range v.p.Commits {
		day := c.At.Local().Format("2006-01-02")
		key := "commits " + c.Repo + " " + day
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, personEntry{key: key, at: c.At, kind: personCommits, verb: "pushed", k: KOK, repo: c.Repo, text: c.Message, url: c.URL})
		}
		out[i].commits = append(out[i].commits, c)
		n := len(out[i].commits)
		out[i].ref = fmt.Sprintf("%d %s", n, plural("commit", n))
	}
	return out
}

// whatCell says what they did: "opened #57", "pushed 2 commits", and for a
// run the run first, "CI #128 failed". The verb or the status word is in
// its colour.
func (e personEntry) whatCell() Cell {
	verb := fg(e.color()).Render(e.verb)
	if e.run != nil {
		return Cell{Text: e.ref + " " + e.verb, Styled: sGold.Render(e.ref) + " " + verb}
	}
	return Cell{Text: e.verb + " " + e.ref, Styled: verb + " " + sGold.Render(e.ref)}
}

// color is the colour of the entry's verb.
func (e personEntry) color() lipgloss.Color {
	if e.merged {
		return cViolet
	}
	return e.k.Color()
}

// runWord is how a run went, said after it: "CI #128 failed".
func runWord(k Kind) string {
	switch k {
	case KOK:
		return "passed"
	case KFail:
		return "failed"
	case KRun, KQueue:
		return "running"
	case KWait:
		return "waiting"
	}
	return "cancelled"
}

// ciRepoNames are the repos whose runs Calamares reads.
func (a *App) ciRepoNames() []string {
	var out []string
	for _, r := range a.ciRepos() {
		out = append(out, r.FullName)
	}
	return out
}

// shown are the entries of the view on display; the Commits view has a line
// per commit.
func (v *personView) shown(all []personEntry) []personEntry {
	if v.view == personActivity {
		return all
	}
	var out []personEntry
	for _, e := range all {
		if e.kind != v.view {
			continue
		}
		if v.view == personCommits {
			for _, c := range e.commits {
				c := c
				out = append(out, personEntry{key: "commit " + c.SHA, at: c.At, kind: personCommits, verb: "pushed", k: KOK, repo: c.Repo,
					ref: c.SHA[:min(7, len(c.SHA))], text: c.Message, url: c.URL, commits: []gh.PersonCommit{c}})
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

func (v *personView) sync(a *App) {
	now := time.Now()
	v.rows = v.shown(v.entries(a, now))
	var rows []Row
	for _, e := range v.rows {
		rows = append(rows, Row{Key: e.key, Kind: e.k, Cells: []Cell{
			steelCell(age(e.at, now), false),
			e.whatCell(),
			iconText("repo", a.short(e.repo), false),
			titleCell(e.text, false),
		}})
	}
	v.t.Empty = "Nothing in the last 14 days, in the repos you can see."
	if v.mine {
		v.t.Empty = "Nothing between you and " + v.login + " in the last 14 days."
	}
	switch {
	case v.busy && len(rows) == 0:
		v.t.Empty = "Loading what " + v.login + " did…"
	case v.err != nil && len(rows) == 0:
		v.t.Empty = "Could not read what " + v.login + " did: " + v.err.Error()
	}
	v.t.SetRows(rows)
}

func (v *personView) selected() (personEntry, bool) {
	r := v.t.Selected()
	if r == nil {
		return personEntry{}, false
	}
	for _, e := range v.rows {
		if e.key == r.Key {
			return e, true
		}
	}
	return personEntry{}, false
}

// MenuTitle names the person for the action menu.
func (v *personView) MenuTitle(a *App) string { return v.login }

func (v *personView) Keys(a *App) []Binding {
	var bs []Binding
	if e, ok := v.selected(); ok {
		if open := v.openKey(a, e); open.Do != nil {
			bs = append(bs, open)
		}
		if e.run != nil {
			x := *e.run
			bs = append(bs, Binding{Keys: []string{"l"}, Desc: "logs", Help: "the log of the run", Do: do(func() tea.Cmd { return a.push(newLogView(a, x.repo, x.run, nil, 0)) })})
		}
		repo, url := e.repo, e.url
		bs = append(bs,
			Binding{Keys: []string{"r"}, Desc: "repo", Help: "open the repo", Do: do(func() tea.Cmd { return a.openRepo(repo, "", 0) })},
			Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open it on GitHub", Do: do(func() tea.Cmd { return a.openURL(url) })},
			Binding{Keys: []string{"y"}, Desc: "copy link", Help: "copy the link", Do: do(func() tea.Cmd { return a.copyText(url, "link") })},
		)
	}
	mine := "only involving you"
	if v.mine {
		mine = "show everything"
	}
	login := v.login
	bs = append(bs,
		Binding{Keys: []string{"u"}, Desc: mine, Help: "only what involves you: their pull requests that ask for your review, and their reviews of yours / everything",
			Do: do(func() tea.Cmd { v.mine = !v.mine; return nil })},
		viewKeys("next view", "next view", "A PERSON", func(d int) tea.Cmd {
			v.view = (v.view + d + len(personViews)) % len(personViews)
			v.t.Top()
			return nil
		}),
		Binding{Help: "open " + login + "'s profile on GitHub", Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(login)) })},
		filterHint, v.t.moveKeys(),
	)
	return grouped("A PERSON", bs...)
}

// openKey opens the entry: a pull request, run or issue on its screen, a
// commit in the browser.
func (v *personView) openKey(a *App, e personEntry) Binding {
	b := Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open"}
	switch {
	case e.pr != nil:
		repo, n := e.pr.Repo, e.pr.Number
		b.Do = do(func() tea.Cmd { return a.push(newPRView(a, repo, n)) })
	case e.run != nil:
		x := *e.run
		b.Desc, b.Do = "open run", do(func() tea.Cmd { return a.push(newRunView(a, x.repo, x.run)) })
	case e.issue != nil:
		repo, n := e.issue.Repo, e.issue.Number
		b.Do = do(func() tea.Cmd { return a.push(newIssueView(a, repo, n)) })
	case len(e.commits) > 0:
		url := e.url
		b.Desc, b.Help, b.Do = "open in browser", "open the commit on GitHub", do(func() tea.Cmd { return a.openURL(url) })
	}
	return b
}

func (v *personView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *personView) FilterValue() string          { return v.t.Filter() }
func (v *personView) FilterInfo(a *App) filterInfo { return v.t.Info() }

func (v *personView) Render(a *App, w, h int) string {
	now := time.Now()
	all := v.entries(a, now)
	counts := make([]int, len(personViews))
	counts[personActivity] = len(all)
	failed, merged := 0, 0
	for _, e := range all {
		n := 1
		if e.kind == personCommits {
			n = len(e.commits)
		}
		counts[e.kind] += n
		switch {
		case e.run != nil && e.k == KFail:
			failed++
		case e.verb == "merged":
			merged++
		}
	}
	var chips []string
	if n := len(v.p.ForYou); n > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d awaiting your review", n)))
	}
	if failed > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d failed %s", failed, plural("run", failed))))
	}
	if merged > 0 {
		chips = append(chips, statusChip(KOK, 0, fmt.Sprintf("%d merged", merged)))
	}
	items := make([]stripItem, len(personViews))
	for i, pv := range personViews {
		items[i] = stripItem{Label: pv.label, Icon: pv.icon, Count: fmt.Sprint(counts[i])}
		if v.busy && v.at.IsZero() && i != personRuns {
			items[i].Count = notReadYet
		}
	}
	scope := "@" + v.login + " · last 14 days, in repos you can see"
	if v.view == personCommits {
		scope = "@" + v.login + " · last 14 days, on default branches of repos you can see"
	}
	if v.mine {
		scope += " · involving you"
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("user", v.name(), scope, v.t.Len(), chips, w), viewStrip(items, v.view, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, all, pw, ph) })
}

// panel is the entry under the cursor, then the review requests between
// you, where the person was busiest and their activity per day.
func (v *personView) panel(a *App, all []personEntry, w, h int) []string {
	now := time.Now()
	if e, ok := v.selected(); ok && e.run != nil {
		return a.runPanel(*e.run, w, h)
	}
	p := newPanel(w)
	if e, ok := v.selected(); ok {
		v.entryFacts(a, p, e, now)
	} else {
		p.add(ico("user") + sWhite.Bold(true).Render(v.name()))
	}
	if len(v.p.ForYou)+len(v.p.ForThem) > 0 {
		p.label("Review requests")
		for _, pr := range v.p.ForYou {
			p.row(prIcon(pr.State, pr.Draft)+sGold.Render(fmt.Sprintf("#%d ", pr.Number))+sText.Render(pr.Title), sWait.Render("for you"))
		}
		for _, pr := range v.p.ForThem {
			p.row(prIcon(pr.State, pr.Draft)+sGold.Render(fmt.Sprintf("#%d ", pr.Number))+sText.Render(pr.Title), sDim.Render("for "+v.login))
		}
	}
	if repos := busiest(all); len(repos) > 0 {
		p.label("Repositories")
		for _, r := range repos[:min(len(repos), 4)] {
			p.row(fact("repo", a.short(r.repo)), sText.Render(r.words()))
		}
	}
	p.label("Activity per day")
	days := make([]time.Duration, 14)
	peak := time.Duration(0)
	for _, e := range all {
		d := int(now.Sub(e.at).Hours() / 24)
		if d >= 0 && d < len(days) {
			days[len(days)-1-d]++
			peak = max(peak, days[len(days)-1-d])
		}
	}
	// Two cells a day, so the axis under it has room for its ends.
	wide := make([]time.Duration, 0, 2*len(days))
	for _, d := range days {
		wide = append(wide, d, d)
	}
	p.add(daySpark(wide, peak))
	p.add(sDim.Render("14 days ago") + strings.Repeat(" ", 2*len(days)-len("14 days ago")-len("today")) + sDim.Render("today"))
	return p.render(h)
}

// entryFacts describes a pull request, issue or commit entry.
func (v *personView) entryFacts(a *App, p *panelText, e personEntry, now time.Time) {
	switch {
	case e.pr != nil:
		pr := e.pr
		p.add(prIcon(pr.State, pr.Draft) + a.repoLabel(pr.Repo) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", pr.Number)))
		p.add(sWhite.Bold(true).Render(pr.Title))
		p.field("State", prStateWords(pr))
		if pr.Branch != "" {
			p.field("Branch", fact("branch", pr.Branch))
		}
		if pr.Author != "" && !strings.EqualFold(pr.Author, v.login) {
			p.field("Author", fact("user", pr.Author))
		}
		p.field("Opened", sText.Render(age(pr.CreatedAt, now)+" ago"))
		if !pr.Reviewed.IsZero() {
			p.field("Review", sText.Render(e.verb+" "+age(pr.Reviewed, now)+" ago"))
		}
	case e.issue != nil:
		is := e.issue
		p.add(icoIn("issue", cGreen) + a.repoLabel(is.Repo) + " " + sGold.Bold(true).Render(fmt.Sprintf("#%d", is.Number)))
		p.add(sWhite.Bold(true).Render(is.Title))
		state := sGreen.Render("open")
		if is.State == "CLOSED" {
			state = fg(cViolet).Render("closed")
		}
		p.field("State", state)
		p.field("Opened", sText.Render(age(is.CreatedAt, now)+" ago"))
	case len(e.commits) > 0:
		p.add(ico("commit") + a.repoLabel(e.repo))
		for _, c := range e.commits[:min(len(e.commits), 6)] {
			p.row(fg(cRef).Render(c.SHA[:min(7, len(c.SHA))])+" "+sText.Render(c.Message), sDim.Render(age(c.At, now)))
		}
		if more := len(e.commits) - 6; more > 0 {
			p.add(sDim.Render(fmt.Sprintf("+%d more", more)))
		}
	}
}

// prStateWords says how a pull request stands, in its colour.
func prStateWords(pr *gh.PersonPR) string {
	switch {
	case pr.State == "MERGED":
		return fg(cViolet).Render("merged")
	case pr.State == "CLOSED":
		return sRed.Render("closed")
	case pr.Draft:
		return sDim.Render("draft")
	}
	return sGreen.Render("open")
}

type personRepo struct {
	repo  string
	n     int
	kinds [personIssues + 1]int // per view: pull requests, runs, commits…
}

// words says what was done in the repo: "1 pull request · 2 commits".
func (r personRepo) words() string {
	nouns := map[int]string{personPulls: "pull request", personRuns: "run", personCommits: "commit", personReviews: "review", personIssues: "issue"}
	var parts []string
	for _, k := range []int{personPulls, personReviews, personRuns, personCommits, personIssues} {
		if n := r.kinds[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, plural(nouns[k], n)))
		}
	}
	return strings.Join(parts, " · ")
}

// busiest counts the entries per repo, most first.
func busiest(all []personEntry) []personRepo {
	at := map[string]int{}
	var out []personRepo
	for _, e := range all {
		i, ok := at[e.repo]
		if !ok {
			i = len(out)
			at[e.repo] = i
			out = append(out, personRepo{repo: e.repo})
		}
		n := max(len(e.commits), 1)
		out[i].n += n
		out[i].kinds[e.kind] += n
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].n > out[j].n })
	return out
}

// personKey opens the page of login, the person of what is under the cursor.
func (a *App) personKey(login string) Binding {
	return Binding{Keys: []string{"~"}, Desc: login, Help: "the page of " + login + ": what they did lately", Off: !gh.ValidLogin(login),
		Do: do(func() tea.Cmd { return a.openPerson(login) })}
}

// openPerson shows the page of login.
func (a *App) openPerson(login string) tea.Cmd {
	login = strings.TrimPrefix(strings.TrimSpace(login), "@")
	if !gh.ValidLogin(login) {
		return a.flashCmd(nHint, "“"+login+"” is not a GitHub login")
	}
	return a.push(newPersonView(a, login))
}

// rememberPerson keeps login first among the people you looked at last, for
// the go-to box.
func (a *App) rememberPerson(login string) {
	out := []string{login}
	for _, p := range a.people {
		if !strings.EqualFold(p, login) && len(out) < 8 {
			out = append(out, p)
		}
	}
	a.people = out
}
