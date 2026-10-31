package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// prWatchEvery is how often a watched pull request is read: one small
// GraphQL query each.
const prWatchEvery = 30 * time.Second

// keepEndedPR is how long a merged or closed pull request stays on the
// Watching tab.
const keepEndedPR = 24 * time.Hour

// maxPRNews is how many changes a watched pull request remembers.
const maxPRNews = 30

const snapshotPRWatches = "prwatches"

// prWatch is a pull request you watch with w: it is read every half a
// minute, and what changed since the read before is told on a card.
type prWatch struct {
	Repo   string
	Number int
	Since  time.Time
	// Pulse is how it was at the last read; Known says there was one.
	Pulse gh.PullRequestPulse
	Known bool
	News  []prNews // what changed while it was watched, oldest first

	polling  bool
	failed   error
	notFound int // reads in a row that GitHub answered 404
}

// prNews is one change to a watched pull request.
type prNews struct {
	At   time.Time
	Kind Kind   // the colour it is told in
	Word string // the chip: approved, failed, merged…
	Text string // what happened, in a few words
}

type prPulseMsg struct {
	repo   string
	number int
	pulse  gh.PullRequestPulse
	err    error
}

func prWatchKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(repo), number)
}

func (w *prWatch) label() string { return fmt.Sprintf("%s #%d", w.Repo, w.Number) }

// savePRWatches keeps the pull request watches with the response cache, so
// they are there the next time Calamares starts.
func (a *App) savePRWatches() {
	out := make([]*prWatch, 0, len(a.prWatches))
	for _, w := range a.prWatches {
		out = append(out, w)
	}
	a.client.Remember(snapshotPRWatches, out)
}

// loadPRWatches brings back the pull request watches of the last session.
func (a *App) loadPRWatches() {
	var saved []*prWatch
	if _, ok := a.client.Recall(snapshotPRWatches, &saved); !ok {
		return
	}
	for _, w := range saved {
		if w.Known && w.Pulse.Ended() && time.Since(w.endedAt()) > keepEndedPR {
			continue
		}
		a.prWatches[prWatchKey(w.Repo, w.Number)] = w
	}
}

// endedAt is when it was merged or closed, as far as the watch knows.
func (w *prWatch) endedAt() time.Time {
	if !w.Pulse.ClosedAt.IsZero() {
		return w.Pulse.ClosedAt
	}
	if n := len(w.News); n > 0 {
		return w.News[n-1].At
	}
	return w.Since
}

// watchPRKey watches or stops watching a pull request.
func (a *App) watchPRKey(repo string, number int) Binding {
	desc := "watch"
	if a.prWatches[prWatchKey(repo, number)] != nil {
		desc = "stop watching"
	}
	return Binding{Keys: []string{"w"}, Desc: desc,
		Help: "watch: a card when it is reviewed, commented on or pushed to, its checks finish, or it is merged or closed",
		Do:   do(func() tea.Cmd { return a.watchPR(repo, number) })}
}

// watchPR toggles watching a pull request.
func (a *App) watchPR(repo string, number int) tea.Cmd {
	key := prWatchKey(repo, number)
	if w, ok := a.prWatches[key]; ok {
		delete(a.prWatches, key)
		a.savePRWatches()
		return a.flashCmd(nInfo, "Stopped watching "+w.label())
	}
	a.prWatches[key] = &prWatch{Repo: repo, Number: number, Since: time.Now()}
	a.savePRWatches()
	a.earnOnce("eagle-eye")
	a.setFlash(nInfo, fmt.Sprintf("Watching %s #%d · a card when it is reviewed, pushed to, merged or its checks finish", repo, number))
	return a.pollPRWatches()
}

// pollPRWatches reads every watched pull request that is still open and
// isn't being read already, and lets go of the ones that ended a day ago.
func (a *App) pollPRWatches() tea.Cmd {
	var cmds []tea.Cmd
	dropped := false
	for key, w := range a.prWatches {
		if w.Known && w.Pulse.Ended() {
			if time.Since(w.endedAt()) > keepEndedPR {
				delete(a.prWatches, key)
				dropped = true
			}
			continue
		}
		if w.polling {
			continue
		}
		w.polling = true
		repo, number := w.Repo, w.Number
		cmds = append(cmds, func() tea.Msg {
			p, err := a.client.PullRequestPulse(gh.Background(context.Background()), repo, number)
			return prPulseMsg{repo, number, p, err}
		})
	}
	if dropped {
		a.savePRWatches()
	}
	return tea.Batch(cmds...)
}

// handlePRPulse tells what changed on a watched pull request since it was
// read last. The first read only notes how it is.
func (a *App) handlePRPulse(m prPulseMsg) tea.Cmd {
	w, ok := a.prWatches[prWatchKey(m.repo, m.number)]
	if !ok {
		return nil
	}
	w.polling, w.failed = false, m.err
	if gh.IsNotFound(m.err) {
		w.notFound++
	} else {
		w.notFound = 0
	}
	if w.notFound >= goneAfter {
		delete(a.prWatches, prWatchKey(m.repo, m.number))
		a.savePRWatches()
		a.setFlash(nInfo, "Stopped watching "+w.label()+" · it is gone from GitHub")
		return nil
	}
	if m.err != nil {
		return nil
	}
	news := prChanges(w.Pulse, m.pulse, a.me, time.Now())
	if !w.Known {
		news = nil
	}
	w.Pulse, w.Known = m.pulse, true
	a.st.updatePR(m.repo, m.number, m.pulse)
	if len(news) == 0 {
		a.savePRWatches()
		return nil
	}
	w.News = append(w.News, news...)
	if len(w.News) > maxPRNews {
		w.News = w.News[len(w.News)-maxPRNews:]
	}
	a.savePRWatches()
	return a.tellPRNews(w, news)
}

// updatePR brings a pull request in your list up to date with what its
// watch read: its checks, review and title, or takes it out once it ended.
func (s *store) updatePR(repo string, number int, p gh.PullRequestPulse) {
	for i, pr := range s.prs {
		if pr.Number != number || !strings.EqualFold(pr.Repo, repo) {
			continue
		}
		if p.Ended() {
			s.prs = append(s.prs[:i:i], s.prs[i+1:]...)
			return
		}
		why := pr.Why
		s.prs[i] = p.PullRequest
		s.prs[i].Why, s.prs[i].Repo = why, pr.Repo
		return
	}
}

// tellPRNews shows a card with what changed, the weightiest first, and sends
// a desktop notification while the terminal is out of focus.
func (a *App) tellPRNews(w *prWatch, news []prNews) tea.Cmd {
	news = weightiestFirst(news)
	p := w.Pulse
	kind := nInfo
	switch news[0].Kind {
	case KFail:
		kind = nError
	case KOK:
		kind = nDone
	}
	a.addToast(&toast{kind: kind, repo: w.Repo, right: "now", pr: &prCard{number: w.Number, title: p.Title, url: p.URL,
		branch: p.Branch, author: p.Author, state: p.State, draft: p.Draft, news: news}})
	if a.focused() {
		return nil
	}
	name := w.Repo
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	title := fmt.Sprintf("#%d %s · %s", w.Number, news[0].Word, name)
	var lines []string
	for _, n := range news {
		lines = append(lines, n.Text)
	}
	body := p.Title + "\n" + strings.Join(lines, " · ")
	url := p.URL
	return func() tea.Msg {
		_ = sendNotification(title, body, url)
		return nil
	}
}

// weightiestFirst puts what ends or breaks the pull request first, then what
// moves it on, then the rest, keeping the order within each.
func weightiestFirst(news []prNews) []prNews {
	weight := func(n prNews) int {
		switch {
		case n.Word == "merged" || n.Word == "closed":
			return 0
		case n.Kind == KFail:
			return 1
		case n.Kind == KOK:
			return 2
		}
		return 3
	}
	out := append([]prNews(nil), news...)
	sort.SliceStable(out, func(i, j int) bool { return weight(out[i]) < weight(out[j]) })
	return out
}

// prChanges is what changed on a pull request between two reads: its state,
// checks, commits, reviews, comments, draft and conflicts. What you did
// yourself is left out.
func prChanges(old, cur gh.PullRequestPulse, me string, now time.Time) []prNews {
	var out []prNews
	add := func(k Kind, word, text string) { out = append(out, prNews{At: now, Kind: k, Word: word, Text: text}) }
	who := func(login string) string {
		switch {
		case login == "":
			return "someone"
		case login == me:
			return "you"
		}
		return login
	}
	mine := func(login string) bool { return me != "" && login == me }

	switch {
	case cur.State == "MERGED" && old.State != "MERGED":
		add(KOK, "merged", "merged by "+who(cur.MergedBy))
	case cur.State == "CLOSED" && old.State != "CLOSED":
		add(KNone, "closed", "closed without merging")
	case cur.State == "OPEN" && old.State == "CLOSED":
		add(KRun, "reopened", "reopened")
	}
	if cur.Ended() {
		return out
	}

	if cur.HeadSHA != old.HeadSHA && old.HeadSHA != "" && !mine(cur.PushedBy) {
		if n := cur.Commits - old.Commits; n > 0 {
			add(KRun, "pushed", fmt.Sprintf("%s pushed %d new %s", who(cur.PushedBy), n, plural("commit", n)))
		} else {
			add(KRun, "pushed", who(cur.PushedBy)+" rewrote its commits")
		}
	}

	moved := cur.Checks.State != old.Checks.State || cur.HeadSHA != old.HeadSHA
	switch c := cur.Checks; {
	case !moved || c.Total == 0:
	case c.State == "SUCCESS":
		add(KOK, "passed", fmt.Sprintf("all %d %s passed", c.Total, plural("check", c.Total)))
	case c.State == "FAILURE" || c.State == "ERROR":
		add(KFail, "failed", fmt.Sprintf("%d of %d %s failed", max(c.Failed, 1), c.Total, plural("check", c.Total)))
	}

	before := map[string]gh.Review{}
	for _, r := range old.Reviews {
		before[r.Author] = r
	}
	for _, r := range cur.Reviews {
		if b, ok := before[r.Author]; (ok && b.State == r.State && !r.SubmittedAt.After(b.SubmittedAt)) || mine(r.Author) {
			continue
		}
		switch r.State {
		case "APPROVED":
			add(KOK, "approved", who(r.Author)+" approved it")
		case "CHANGES_REQUESTED":
			add(KFail, "changes requested", who(r.Author)+" asked for changes")
		case "COMMENTED":
			add(KNone, "reviewed", who(r.Author)+" left a review")
		}
	}

	if n := cur.Comments - old.Comments; n > 0 && !(n == 1 && mine(cur.LastCommentBy)) {
		if n == 1 {
			add(KNone, "commented", who(cur.LastCommentBy)+" commented")
		} else {
			add(KNone, "commented", fmt.Sprintf("%d new comments, the last by %s", n, who(cur.LastCommentBy)))
		}
	}

	switch {
	case old.Draft && !cur.Draft:
		add(KWait, "ready for review", "ready for review")
	case !old.Draft && cur.Draft:
		add(KNone, "back to draft", "back to draft")
	}
	switch {
	case cur.Mergeable == "CONFLICTING" && old.Mergeable != "CONFLICTING":
		add(KFail, "conflicting", "has conflicts with "+cur.Base)
	case old.Mergeable == "CONFLICTING" && cur.Mergeable == "MERGEABLE":
		add(KOK, "conflict resolved", "the conflicts are resolved")
	}
	return out
}

// sortedPRWatches lists open pull requests first, then the ones that ended,
// each most recently changed first.
func (a *App) sortedPRWatches() []*prWatch {
	out := make([]*prWatch, 0, len(a.prWatches))
	for _, w := range a.prWatches {
		out = append(out, w)
	}
	last := func(w *prWatch) time.Time {
		if n := len(w.News); n > 0 {
			return w.News[n-1].At
		}
		return w.Since
	}
	sort.Slice(out, func(i, j int) bool {
		if ei, ej := out[i].Pulse.Ended(), out[j].Pulse.Ended(); ei != ej {
			return ej
		}
		if li, lj := last(out[i]), last(out[j]); !li.Equal(lj) {
			return li.After(lj)
		}
		return out[i].label() < out[j].label()
	})
	return out
}

// prStateCell is how a watched pull request stands: open, draft, merged or
// closed, in GitHub's colours.
func prStateCell(w *prWatch) Cell {
	if !w.Known {
		return Cell{Text: icon("pr") + "reading", Styled: icoIn("pr", cGrey) + sDim.Render("reading")}
	}
	p := w.Pulse
	word := map[string]string{"MERGED": "merged", "CLOSED": "closed"}[p.State]
	switch {
	case word != "":
	case p.Draft:
		word = "draft"
	default:
		word = "open"
	}
	return Cell{Text: icon("pr") + word, Styled: prIcon(p.State, p.Draft) + fg(prStateColor(p.State, p.Draft)).Render(word)}
}

// prWatchRows are the rows of the Pull requests view of Watching.
func (a *App) prWatchRows(now time.Time) []Row {
	var rows []Row
	for _, w := range a.sortedPRWatches() {
		quiet := w.Known && w.Pulse.Ended()
		title := w.Pulse.Title
		if title == "" {
			title = "…"
		}
		latest := steelCell("nothing yet", true)
		if n := len(w.News); n > 0 {
			x := w.News[n-1]
			t := x.Text + " · " + age(x.At, now)
			latest = Cell{Text: t, Styled: fg(x.Kind.Color()).Render(x.Text) + fg(cSteel).Render(" · "+age(x.At, now))}
		}
		checks, review := Cell{Text: "–", Styled: sDim.Render("–")}, Cell{Text: "–", Styled: sDim.Render("–")}
		if w.Known && !quiet {
			checks, review = checksCell(w.Pulse.Checks), reviewCell(w.Pulse.PullRequest)
		}
		row := Row{Key: prWatchKey(w.Repo, w.Number), Kind: KOK, Cells: []Cell{
			prStateCell(w),
			numberCell(w.Number, quiet),
			titleCell(title, quiet),
			iconText("repo", a.short(w.Repo), quiet),
			checks,
			review,
			latest,
		}}
		if quiet {
			row.Kind = KNone
		}
		rows = append(rows, row)
	}
	return rows
}

// prWatchPanel tells about the watched pull request under the cursor: what
// it is, how its checks and reviews stand, and what changed while you
// watched it.
func (a *App) prWatchPanel(key string, w, h int) []string {
	pw := a.prWatches[key]
	if pw == nil {
		return emptyPanel(w, h, "No pull request watched yet. Press w on one.")
	}
	p := pw.Pulse
	pn := newPanel(w)
	pn.add(prIcon(p.State, p.Draft) + sGold.Bold(true).Render(fmt.Sprintf("#%d", pw.Number)) + " " + sWhite.Bold(true).Render(p.Title))
	pn.add(strings.Join([]string{fact("repo", a.short(pw.Repo)), fact("branch", p.Branch), fact("user", p.Author)}, sDim.Render("  ·  ")))
	if pw.failed != nil {
		pn.callout(cRed, sText.Render("Could not read it: "+pw.failed.Error()))
	}
	if !pw.Known {
		pn.add(sDim.Render("Reading it…"))
		return pn.render(h)
	}
	now := time.Now()
	pn.label("Status")
	pn.field("State", prStateCell(pw).Styled)
	if !p.Ended() {
		pn.field("Checks", checksCell(p.Checks).Styled)
		pn.field("Review", reviewCell(p.PullRequest).Styled)
		if p.Mergeable == "CONFLICTING" {
			pn.field("Merge", sRed.Render("conflicts with "+p.Base))
		}
	}
	for i, r := range p.Reviews {
		k := map[string]Kind{"APPROVED": KOK, "CHANGES_REQUESTED": KFail}[r.State]
		label := ""
		if i == 0 {
			label = "Reviewers"
		}
		pn.field(label, fg(k.Color()).Render(k.Glyph(a.frame))+" "+person(r.Author)+" "+sDim.Render(strings.ToLower(strings.ReplaceAll(r.State, "_", " "))))
	}
	pn.label("Changes")
	pn.add(sDim.Render("seen since you started watching it, " + age(pw.Since, now) + " ago"))
	if len(pw.News) == 0 {
		pn.add(sDim.Render("None yet."))
	}
	for i := len(pw.News) - 1; i >= 0; i-- {
		x := pw.News[i]
		pn.row(fg(x.Kind.Color()).Render(x.Kind.Glyph(a.frame))+" "+sText.Render(x.Text), fg(cSteel).Render(age(x.At, now)))
	}
	return pn.render(h)
}

// prWatchKeys are the keys of the Pull requests view of Watching.
func (v *watchView) prWatchKeys(a *App) []Binding {
	var bs []Binding
	if r := v.prs.Selected(); r != nil {
		if w := a.prWatches[r.Key]; w != nil {
			repo, number, url := w.Repo, w.Number, w.Pulse.URL
			if url == "" {
				url = a.client.WebURL(fmt.Sprintf("%s/pull/%d", repo, number))
			}
			bs = append(bs,
				Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "open", Do: do(func() tea.Cmd { return a.push(newPRView(a, repo, number)) })},
				Binding{Keys: []string{"w"}, Desc: "stop watching", Do: do(func() tea.Cmd { return a.watchPR(repo, number) })},
				Binding{Keys: []string{"o"}, Desc: "open in browser", Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(url) })},
				Binding{Keys: []string{"y"}, Desc: "copy link", Help: "copy the link", Do: do(func() tea.Cmd { return a.copyText(url, "pull request URL") })},
			)
		}
	}
	return grouped("WATCHING", append(bs, v.viewKey(), filterHint, v.prs.moveKeys())...)
}
