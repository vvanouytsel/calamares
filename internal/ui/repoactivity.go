package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// activityClockSkew is how far GitHub's clock may be ahead of ours: a pull
// request or release made this long before the last read still counts as new.
const activityClockSkew = 2 * time.Minute

// maxRepoNews is how many changes a watched repo remembers.
const maxRepoNews = 30

const snapshotRepoActivity = "repoactivity"

// repoNews is one change on a watched repo besides its runs: new commits on
// its default branch, a pull request opened or merged, a release.
type repoNews struct {
	prNews
	Commits []gh.ActivityCommit `json:",omitempty"` // the new commits, newest first
	Pull    *gh.ActivityPull    `json:",omitempty"`
	Release *gh.ActivityRelease `json:",omitempty"`
}

// savedActivity is what a repo watch knows of its repo, kept between sessions
// so what happened while Calamares was closed is told at the next start.
type savedActivity struct {
	Activity gh.RepoActivity
	News     []repoNews
}

type repoActivityMsg struct {
	repo string
	act  gh.RepoActivity
	err  error
}

func (a *App) saveRepoActivity() {
	out := map[string]savedActivity{}
	for key, st := range a.repoWatches {
		if st.actKnown {
			out[key] = savedActivity{st.act, st.news}
		}
	}
	a.client.Remember(snapshotRepoActivity, out)
}

func (a *App) loadRepoActivity() {
	var saved map[string]savedActivity
	if _, ok := a.client.Recall(snapshotRepoActivity, &saved); !ok {
		return
	}
	for key, s := range saved {
		if _, watched := a.cfg.RepoWatch(key); !watched {
			continue
		}
		st := a.repoWatch(key)
		st.act, st.actKnown, st.news = s.Activity, true, s.News
	}
}

// pollActivity reads the commits, pull requests and releases of the watched
// repos that tell about any: one GraphQL query each.
func (a *App) pollActivity() tea.Cmd {
	var cmds []tea.Cmd
	for _, w := range a.cfg.WatchedRepos {
		st := a.repoWatch(w.Repo)
		if len(w.Activity) == 0 || st.actPolling {
			continue
		}
		st.actPolling = true
		repo := w.Repo
		cmds = append(cmds, func() tea.Msg {
			act, err := a.client.RepoActivity(gh.Background(context.Background()), repo)
			return repoActivityMsg{repo, act, err}
		})
	}
	return tea.Batch(cmds...)
}

// handleRepoActivity tells what is new on a watched repo since the read
// before. The first read only notes how it is.
func (a *App) handleRepoActivity(m repoActivityMsg) tea.Cmd {
	st := a.repoWatch(m.repo)
	st.actPolling, st.actFailed = false, m.err
	w, ok := a.cfg.RepoWatch(m.repo)
	if !ok || m.err != nil {
		return nil
	}
	known := st.actKnown
	news := activityNews(st.act, m.act, w, a.me, time.Now())
	st.act, st.actKnown = m.act, true
	if !known || len(news) == 0 {
		a.saveRepoActivity()
		return nil
	}
	st.news = append(st.news, news...)
	if len(st.news) > maxRepoNews {
		st.news = st.news[len(st.news)-maxRepoNews:]
	}
	a.saveRepoActivity()
	var cmds []tea.Cmd
	for _, n := range news[max(len(news)-maxToasts, 0):] {
		cmds = append(cmds, a.tellRepoNews(m.repo, n))
	}
	return tea.Batch(cmds...)
}

// tellRepoNews shows a card for one change, and sends a desktop notification
// for a merge or a release while the terminal is out of focus: commits and
// opened pull requests come too often on a busy repo.
func (a *App) tellRepoNews(repo string, n repoNews) tea.Cmd {
	kind := nInfo
	if n.Kind == KOK {
		kind = nDone
	}
	a.addToast(&toast{kind: kind, repo: repo, right: "now", act: &n})
	if a.focused() || (n.Word != "merged" && n.Word != "released" && n.Word != "pre-released") {
		return nil
	}
	name := repo[strings.LastIndex(repo, "/")+1:]
	title := n.Text + " · " + name
	body, url := "", ""
	switch {
	case n.Pull != nil:
		body, url = n.Pull.Title, n.Pull.URL
	case n.Release != nil:
		body, url = n.Release.Name, n.Release.URL
	}
	return func() tea.Msg {
		_ = sendNotification(title, body, url)
		return nil
	}
}

// activityNews is what is new on a repo between two reads, of the kinds w
// tells about. What you did yourself is left out.
func activityNews(old, cur gh.RepoActivity, w config.RepoWatch, me string, now time.Time) []repoNews {
	var out []repoNews
	add := func(k Kind, word, text string) *repoNews {
		out = append(out, repoNews{prNews: prNews{At: now, Kind: k, Word: word, Text: text}})
		return &out[len(out)-1]
	}
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
	since := old.ReadAt.Add(-activityClockSkew)

	if w.Tells(config.ActivityCommits) && old.Head != "" && cur.Head != old.Head && cur.Branch == old.Branch {
		var fresh []gh.ActivityCommit
		found := false
		for _, c := range cur.Commits {
			if c.SHA == old.Head {
				found = true
				break
			}
			fresh = append(fresh, c)
		}
		others := false
		for _, c := range fresh {
			others = others || !mine(c.Author)
		}
		switch {
		case !found && len(cur.Commits) < 10:
			add(KWait, "rewritten", "the history of "+cur.Branch+" was rewritten").Commits = fresh
		case !others:
		case !found:
			add(KRun, "pushed", fmt.Sprintf("%d+ new commits on %s", len(fresh), cur.Branch)).Commits = fresh
		default:
			add(KRun, "pushed", fmt.Sprintf("%d new %s on %s", len(fresh), plural("commit", len(fresh)), cur.Branch)).Commits = fresh
		}
	}

	if w.Tells(config.ActivityPulls) {
		before := map[int]string{}
		for _, p := range old.Pulls {
			before[p.Number] = p.State
		}
		for i := len(cur.Pulls) - 1; i >= 0; i-- { // oldest change first
			p := cur.Pulls[i]
			prev, seen := before[p.Number]
			switch {
			case p.State == "MERGED" && ((seen && prev != "MERGED") || (!seen && p.MergedAt.After(since))):
				if !mine(p.MergedBy) {
					add(KOK, "merged", fmt.Sprintf("#%d merged by %s", p.Number, who(p.MergedBy))).Pull = &p
				}
			case !seen && p.State == "OPEN" && p.CreatedAt.After(since) && !mine(p.Author):
				if p.Draft {
					add(KNone, "opened", fmt.Sprintf("#%d opened as a draft by %s", p.Number, who(p.Author))).Pull = &p
				} else {
					add(KWait, "opened", fmt.Sprintf("#%d opened by %s", p.Number, who(p.Author))).Pull = &p
				}
			}
		}
	}

	if w.Tells(config.ActivityReleases) {
		seen := map[string]bool{}
		for _, r := range old.Releases {
			seen[r.Tag] = true
		}
		for i := len(cur.Releases) - 1; i >= 0; i-- {
			r := cur.Releases[i]
			if seen[r.Tag] || !r.At.After(since) || mine(r.Author) {
				continue
			}
			if r.Prerelease {
				add(KOK, "pre-released", r.Tag+" pre-released by "+who(r.Author)).Release = &r
			} else {
				add(KOK, "released", r.Tag+" released by "+who(r.Author)).Release = &r
			}
		}
	}
	return out
}

// describeActivity says in a few words what else a repo watch tells about.
func describeActivity(w config.RepoWatch) string {
	var kinds []string
	for _, k := range config.AllActivity {
		if w.Tells(k) {
			kinds = append(kinds, activityNames[k])
		}
	}
	switch len(kinds) {
	case 0:
		return "runs only"
	case 1:
		return kinds[0]
	}
	return strings.Join(kinds[:len(kinds)-1], ", ") + " and " + kinds[len(kinds)-1]
}

// activityNames name the kinds of activity in a list.
var activityNames = map[string]string{config.ActivityCommits: "commits", config.ActivityPulls: "pull requests", config.ActivityReleases: "releases"}

// actCard is what a card about a repo's activity says: its title, then
// its details, each line at most w cells.
func (a *App) actCard(n *repoNews, w int) (string, []string) {
	switch {
	case n.Pull != nil:
		p := n.Pull
		title := sGold.Bold(true).Render(fmt.Sprintf("#%d", p.Number)) + " " + sWhite.Bold(true).Render(p.Title)
		return title, append(wrapCapped(joinFacts(fact("branch", p.Branch+" → "+p.Base), fact("user", p.Author)), w, 2), sText.Render(n.Text))
	case n.Release != nil:
		r := n.Release
		title := fg(cRef).Bold(true).Render(r.Tag)
		if r.Name != "" && r.Name != r.Tag {
			title += sDim.Render(" · ") + sWhite.Bold(true).Render(r.Name)
		}
		return title, []string{sText.Render(n.Text)}
	}
	var out []string
	for _, c := range n.Commits[:min(len(n.Commits), 2)] {
		sha := fg(cRef).Render(c.SHA[:min(7, len(c.SHA))])
		by := "  " + fact("user", c.Author)
		out = append(out, sha+" "+fit(sText.Render(c.Title), max(w-8-width(by), 8))+by)
	}
	if more := len(n.Commits) - 2; more > 0 {
		out = append(out, sDim.Render(fmt.Sprintf("and %d more", more)))
	}
	return sWhite.Bold(true).Render(n.Text), out
}

// openActivity opens what a card about a repo's activity is about: the
// pull request, the newest commit, or the repo's releases.
func (a *App) openActivity(repo string, n *repoNews) tea.Cmd {
	switch {
	case n.Pull != nil:
		return a.push(newPRView(a, repo, n.Pull.Number))
	case n.Release != nil:
		rv := newRepoView(a, repo, "")
		return tea.Batch(a.push(rv), rv.setSection(a, sectionReleases))
	case len(n.Commits) > 0:
		c := n.Commits[0]
		return a.push(newCommitView(a, repo, gh.Commit{SHA: c.SHA, Message: c.Title, Author: c.Author, Date: c.At, URL: c.URL}))
	}
	return a.push(newRepoView(a, repo, ""))
}
