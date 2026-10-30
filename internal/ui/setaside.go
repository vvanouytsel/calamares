package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// Setting aside: an issue that asked you something, or a pull request that
// waits for your review, counts on its screen until you act on it. Some you
// can't or won't act on (a mention in someone else's repo), so d sets one
// aside: it stops counting until something new happens on it (a new comment
// on an issue, any change to a pull request). The list is kept with the
// response cache, so it survives a restart.

const snapshotAside = "set-aside"

func (a *App) loadAside() {
	a.aside = map[string]time.Time{}
	a.client.Recall(snapshotAside, &a.aside)
	if a.aside == nil {
		a.aside = map[string]time.Time{}
	}
}

// isAside says whether key was set aside after its last activity.
func (a *App) isAside(key string, last time.Time) bool {
	t, ok := a.aside[key]
	return ok && !last.After(t)
}

func (a *App) toggleAside(key, label string, aside bool) tea.Cmd {
	if a.aside == nil {
		a.aside = map[string]time.Time{}
	}
	msg := "Set aside " + label + ": it counts again when something new happens"
	if aside {
		delete(a.aside, key)
		msg = "Brought back " + label
	} else {
		a.aside[key] = time.Now()
		a.earn("not-now", 1)
	}
	a.client.Remember(snapshotAside, a.aside)
	a.syncTop()
	return a.flashCmd(nDone, msg)
}

func issueAsideKey(i gh.Issue) string    { return "issue:" + issueKey(i) }
func prAsideKey(p gh.PullRequest) string { return "pr:" + prKey(p) }

// issueLast is the last thing that happened on an issue that matters for
// whether it waits for you: its last comment.
func issueLast(i gh.Issue) time.Time {
	if i.LastComment != nil {
		return i.LastComment.CreatedAt
	}
	return i.UpdatedAt
}

// issueWaits says whether an issue waits for you and was not set aside.
func (a *App) issueWaits(i gh.Issue) bool {
	return i.WaitsForYou(a.me) && !a.issueAside(i)
}

func (a *App) issueAside(i gh.Issue) bool { return a.isAside(issueAsideKey(i), issueLast(i)) }

// prWaits says whether a pull request waits for your review and was not set aside.
func (a *App) prWaits(p gh.PullRequest) bool {
	return p.Why == gh.WhyReview && !a.prAside(p)
}

func (a *App) prAside(p gh.PullRequest) bool { return a.isAside(prAsideKey(p), p.UpdatedAt) }

// issueAsideBinding is d on an issue: set it aside while it waits for you, bring
// it back when it was set aside.
func (a *App) issueAsideBinding(i gh.Issue) Binding {
	label := fmt.Sprintf("%s #%d", a.short(i.Repo), i.Number)
	aside := a.issueAside(i)
	desc, help := "done for now", "set aside: stop counting it until someone writes again"
	if aside {
		desc, help = "bring back", "count it again as waiting for you"
	}
	return Binding{Keys: []string{"d"}, Desc: desc, Help: help, Off: !aside && !i.WaitsForYou(a.me),
		Do: do(func() tea.Cmd { return a.toggleAside(issueAsideKey(i), label, aside) })}
}

// prAsideBinding is d on a pull request that waits for your review.
func (a *App) prAsideBinding(p gh.PullRequest) Binding {
	label := fmt.Sprintf("%s #%d", a.short(p.Repo), p.Number)
	aside := a.prAside(p)
	desc, help := "done for now", "set aside: stop counting it until the pull request changes"
	if aside {
		desc, help = "bring back", "count it again as waiting for your review"
	}
	return Binding{Keys: []string{"d"}, Desc: desc, Help: help, Off: !aside && p.Why != gh.WhyReview,
		Do: do(func() tea.Cmd { return a.toggleAside(prAsideKey(p), label, aside) })}
}

// issuesWaitingIn counts the issues of one of your three lists that wait for you.
func (a *App) issuesWaitingIn(view int) int {
	list := a.st.issues.Assigned
	switch view {
	case 1:
		list = a.st.issues.Created
	case 2:
		list = a.st.issues.Mentioned
	}
	n := 0
	for _, i := range list {
		if a.issueWaits(i) {
			n++
		}
	}
	return n
}
