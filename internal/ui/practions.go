package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
	"github.com/vvanouytsel/calamares/internal/gitctx"
)

// lockIf is the reason a key is locked when ok is false, from what GitHub
// says you may do with this very object.
func lockIf(ok bool, reason string) string {
	if ok {
		return ""
	}
	return reason
}

// actionKeys are the keys of the pull request screen beyond the ones every
// list shows: draft, close, reviewers, labels, assignees, update the branch,
// auto-merge, check out. Each says why it is locked when GitHub says you
// may not use it on this pull request.
func (v *prView) actionKeys(a *App) []Binding {
	pr := v.pr
	label := fmt.Sprintf("%s #%d", a.short(v.repo), v.number)
	about := []string{sWhite.Render(label) + sDim.Render(" · "+pr.Title)}
	open := pr.State == "OPEN"
	var bs []Binding

	// Ready for review / back to draft.
	draftDesc, draftHelp, draftTitle := "ready", "mark ready for review", "Mark ready for review?"
	draftNote := "Reviewers will be notified."
	if !pr.Draft {
		draftDesc, draftHelp, draftTitle, draftNote = "draft", "convert back to a draft", "Convert to a draft?", "It can't be merged until it is marked ready again."
	}
	bs = append(bs, Binding{Keys: []string{"D"}, Desc: draftDesc, Help: draftHelp, Off: !open, Mutates: true, Need: gh.CapComment, Repo: v.repo,
		Locked: lockIf(pr.CanUpdate, "you can't edit this pull request"),
		Do: do(func() tea.Cmd {
			return confirm(draftTitle, append(about, "", draftNote), "Yes", false, func() tea.Cmd {
				return a.actThen(v.repo, label+" is now "+map[bool]string{true: "a draft", false: "ready for review"}[!pr.Draft], func(ctx context.Context) error {
					return a.client.SetDraft(ctx, pr.ID, !pr.Draft)
				}, func() tea.Cmd {
					if pr.Draft {
						a.earnOnce("out-of-drafts")
					}
					return nil
				})
			})
		})})

	// Close / reopen.
	closeDesc, closeLocked := "close", lockIf(pr.CanClose, "you can't close this pull request")
	if pr.State == "CLOSED" {
		closeDesc, closeLocked = "reopen", lockIf(pr.CanReopen, "you can't reopen this pull request")
	}
	bs = append(bs, Binding{Keys: []string{"X"}, Desc: closeDesc, Help: closeDesc + " the pull request", Off: pr.State == "MERGED", Mutates: true, Need: gh.CapTriage, Repo: v.repo,
		Locked: closeLocked,
		Do: do(func() tea.Cmd {
			state, done := "closed", "Closed "+label
			if pr.State == "CLOSED" {
				state, done = "open", "Reopened "+label
			}
			return confirm(capFirst(closeDesc)+" pull request?", append(about, "", "It will be "+map[string]string{"closed": "closed without merging", "open": "open again"}[state]+"."),
				capFirst(closeDesc), state == "closed", func() tea.Cmd {
					return a.act(v.repo, done, func(ctx context.Context) error {
						return a.client.SetPullRequestState(ctx, v.repo, v.number, state)
					})
				})
		})})

	// Reviewers, labels, assignees.
	bs = append(bs, Binding{Keys: []string{"@"}, Desc: "reviewers", Help: "request or remove reviewers", Off: !open, Mutates: true, Need: gh.CapTriage, Repo: v.repo,
		Do: do(func() tea.Cmd { return v.pickReviewers(a) })})
	bs = append(bs, Binding{Keys: []string{"T"}, Desc: "labels", Help: "add or remove labels", Mutates: true, Need: gh.CapTriage, Repo: v.repo,
		Locked: lockIf(pr.CanLabel || pr.CanUpdate, "you can't label this pull request"),
		Do:     do(func() tea.Cmd { return v.pickLabels(a) })})
	bs = append(bs, Binding{Help: "Assignees…", Mutates: true, Need: gh.CapTriage, Repo: v.repo,
		Locked: lockIf(pr.CanAssign, "you can't assign people here"),
		Do:     do(func() tea.Cmd { return v.pickAssignees(a) })})

	// Update the branch.
	bs = append(bs, Binding{Keys: []string{"B"}, Desc: "update branch", Help: "bring the branch up to date with its base", Off: !open || pr.MergeState != "BEHIND", Mutates: true, Need: gh.CapWrite, Repo: v.repo,
		Locked: lockIf(pr.CanUpdateBranch || pr.MergeState != "BEHIND", "you can't update this branch"),
		Do:     do(func() tea.Cmd { return v.updateBranch(a) })})

	// Auto-merge.
	autoDesc, autoLocked := "auto-merge", ""
	switch {
	case pr.AutoMerge != nil:
		autoDesc, autoLocked = "auto-merge off", lockIf(pr.CanDisableAutoMerge, "you can't turn off auto-merge")
	case !pr.AutoMergeAllowed:
		autoLocked = "auto-merge isn't turned on for this repo"
	case !pr.CanEnableAutoMerge:
		autoLocked = "auto-merge can't be turned on: it can be merged right now, or isn't allowed for you"
	}
	bs = append(bs, Binding{Desc: autoDesc, Help: "auto-merge: merge by itself once it can be (M offers it too)", Off: !open, Mutates: true, Need: gh.CapWrite, Repo: v.repo,
		Locked: autoLocked, Do: do(func() tea.Cmd { return v.autoMerge(a) })})

	for _, c := range pr.Closes {
		c := c
		bs = append(bs, Binding{Help: fmt.Sprintf("Open issue #%d: %s", c.Number, c.Title),
			Do: do(func() tea.Cmd { return a.push(newIssueView(a, c.Repo, c.Number)) })})
	}

	// Check out locally: only from a checkout of this repo.
	here := a.opts.FromCwd && strings.EqualFold(a.opts.StartRepo, v.repo)
	bs = append(bs, Binding{Help: "Check out locally", Locked: lockIf(here, "start Calamares inside a checkout of "+v.repo),
		Do: do(func() tea.Cmd { return v.checkout(a) })})
	if n := len(a.pendingFor(v.repo, v.number)); n > 0 {
		bs = append(bs, Binding{Help: fmt.Sprintf("Discard %d pending comments", n), Mutates: false, Do: do(func() tea.Cmd {
			return confirm("Discard pending comments?", append(about, "", fmt.Sprintf("The %d comments you added to your review will be dropped.", n)), "Discard", true, func() tea.Cmd {
				a.setPending(v.repo, v.number, nil)
				v.conv.ver++
				v.diff.listVer++
				return a.flashCmd(nInfo, "Discarded the pending comments")
			})
		})})
	}
	return bs
}

// pickReviewers lets you tick the people asked to review.
func (v *prView) pickReviewers(a *App) tea.Cmd {
	repo, number, author := v.repo, v.number, v.pr.Author
	current := slices.Clone(v.pr.Requested)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		users, err := a.assignable(ctx, repo)
		if err != nil {
			return flashMsg{nError, "Could not list who can review: " + err.Error()}
		}
		var items []multiItem
		for _, u := range users {
			if !strings.EqualFold(u, author) {
				items = append(items, multiItem{value: u, label: u})
			}
		}
		return modalMsg{newMultiPicker("Reviewers of "+a.short(repo)+fmt.Sprintf(" #%d", number), items, current, func(add, remove []string) tea.Cmd {
			return a.act(repo, "Reviewers updated · "+changeSummary(add, remove), func(ctx context.Context) error {
				if len(add) > 0 {
					if err := a.client.RequestReviewers(ctx, repo, number, add); err != nil {
						return err
					}
				}
				if len(remove) > 0 {
					return a.client.RemoveReviewers(ctx, repo, number, remove)
				}
				return nil
			})
		})}
	}
}

func changeSummary(add, remove []string) string {
	var parts []string
	if len(add) > 0 {
		parts = append(parts, "+ "+strings.Join(add, ", "))
	}
	if len(remove) > 0 {
		parts = append(parts, "− "+strings.Join(remove, ", "))
	}
	return strings.Join(parts, "  ")
}

// pickLabels lets you tick the labels of the pull request.
func (v *prView) pickLabels(a *App) tea.Cmd {
	var current []string
	for _, l := range v.pr.Labels {
		current = append(current, l.Name)
	}
	return a.pickLabelsOf(v.repo, v.number, current)
}

// pickAssignees lets you tick who the pull request is assigned to.
func (v *prView) pickAssignees(a *App) tea.Cmd {
	return a.pickAssigneesOf(v.repo, v.number, slices.Clone(v.pr.Assignees))
}

// pickLabelsOf lets you tick the labels of a pull request or issue.
func (a *App) pickLabelsOf(repo string, number int, current []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		labels, err := a.client.RepoLabels(ctx, repo)
		if err != nil {
			return flashMsg{nError, "Could not list the labels: " + err.Error()}
		}
		var items []multiItem
		for _, l := range labels {
			items = append(items, multiItem{value: l.Name, label: l.Name, color: l.Color})
		}
		return modalMsg{newMultiPicker("Labels of "+a.short(repo)+fmt.Sprintf(" #%d", number), items, current, func(add, remove []string) tea.Cmd {
			final := slices.DeleteFunc(slices.Clone(current), func(s string) bool { return slices.Contains(remove, s) })
			final = append(final, add...)
			return a.act(repo, "Labels updated · "+changeSummary(add, remove), func(ctx context.Context) error {
				return a.client.SetLabels(ctx, repo, number, final)
			})
		})}
	}
}

// pickAssigneesOf lets you tick who a pull request or issue is assigned to.
func (a *App) pickAssigneesOf(repo string, number int, current []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		users, err := a.assignable(ctx, repo)
		if err != nil {
			return flashMsg{nError, "Could not list who can be assigned: " + err.Error()}
		}
		var items []multiItem
		for _, u := range users {
			items = append(items, multiItem{value: u, label: u})
		}
		return modalMsg{newMultiPicker("Assignees of "+a.short(repo)+fmt.Sprintf(" #%d", number), items, current, func(add, remove []string) tea.Cmd {
			return a.act(repo, "Assignees updated · "+changeSummary(add, remove), func(ctx context.Context) error {
				if len(add) > 0 {
					if err := a.client.AddAssignees(ctx, repo, number, add); err != nil {
						return err
					}
				}
				if len(remove) > 0 {
					return a.client.RemoveAssignees(ctx, repo, number, remove)
				}
				return nil
			})
		})}
	}
}

// assignable lists who can be assigned in repo, read once per session.
func (a *App) assignable(ctx context.Context, repo string) ([]string, error) {
	if u, ok := a.users[repo]; ok {
		return u, nil
	}
	var cached []string
	if _, ok := a.client.Recall("users:"+repo, &cached); ok && len(cached) > 0 {
		a.users[repo] = cached
		return cached, nil
	}
	u, err := a.client.AssignableUsers(ctx, repo)
	if err != nil {
		return nil, err
	}
	a.users[repo] = u
	a.client.Remember("users:"+repo, u)
	return u, nil
}

// updateBranch asks how to bring the branch up to date, then does it.
func (v *prView) updateBranch(a *App) tea.Cmd {
	pr, repo, number := v.pr, v.repo, v.number
	label := fmt.Sprintf("%s #%d", a.short(repo), number)
	items := []pickItem{{value: "merge", label: "merge " + pr.Base + " into it", note: sDim.Render("adds a merge commit")}}
	if slices.Contains(pr.MergeMethods, "rebase") {
		items = append(items, pickItem{value: "rebase", label: "rebase it on " + pr.Base, note: sDim.Render("rewrites its commits")})
	}
	return openModal(newPicker("update-branch", "Update the branch of "+label, "", items, func(method string) tea.Cmd {
		lines := []string{sWhite.Render(label) + sDim.Render(" · "+pr.Title), "", "Its branch " + pr.Branch + " will be brought up to date with " + pr.Base + " (" + method + ")."}
		return confirm("Update the branch?", lines, "Update", method == "rebase", func() tea.Cmd {
			return a.act(repo, "Updated the branch of "+label, func(ctx context.Context) error {
				return a.client.UpdateBranch(ctx, repo, number, pr.ID, pr.HeadSHA, method)
			})
		})
	}))
}

// autoMerge turns auto-merge on (asking how to merge) or off.
func (v *prView) autoMerge(a *App) tea.Cmd {
	pr, repo := v.pr, v.repo
	label := fmt.Sprintf("%s #%d", a.short(repo), v.number)
	if pr.AutoMerge != nil {
		return confirm("Turn off auto-merge?", []string{sWhite.Render(label) + sDim.Render(" · "+pr.Title), "", "It will no longer merge by itself."}, "Turn off", false, func() tea.Cmd {
			return a.act(repo, "Auto-merge is off for "+label, func(ctx context.Context) error {
				return a.client.DisableAutoMerge(ctx, pr.ID)
			})
		})
	}
	if len(pr.MergeMethods) == 0 {
		return a.flashCmd(nHint, "This repo allows no way to merge from here.")
	}
	items := make([]pickItem, len(pr.MergeMethods))
	for i, m := range pr.MergeMethods {
		items[i] = pickItem{value: m, label: m, note: sDim.Render(mergeMethodNote[m])}
	}
	return openModal(newPicker("auto-merge", "Auto-merge "+label, "", items, func(method string) tea.Cmd {
		lines := []string{sWhite.Render(label) + sDim.Render(" · "+pr.Title), "", "It will be merged into " + pr.Base + " with " + method + " as soon as its checks and reviews allow."}
		return confirm("Turn on auto-merge?", lines, "Turn on", false, func() tea.Cmd {
			return a.act(repo, "Auto-merge is on for "+label+" · "+method, func(ctx context.Context) error {
				return a.client.EnableAutoMerge(ctx, pr.ID, method)
			})
		})
	}))
}

// checkout fetches the pull request's branch and switches to it.
func (v *prView) checkout(a *App) tea.Cmd {
	number, branch := v.number, v.pr.Branch
	local := "pr-" + fmt.Sprint(number)
	if dirty, err := gitctx.Dirty(""); err != nil {
		return a.flashCmd(nError, "Could not look at the checkout: "+err.Error())
	} else if dirty {
		return a.flashCmd(nHint, "You have uncommitted changes here: commit or stash them first.")
	}
	lines := []string{sWhite.Render(fmt.Sprintf("#%d", number)) + sDim.Render(" · "+v.pr.Title), "", "Its branch " + branch + " will be fetched as " + local + " and checked out in this folder."}
	return confirm("Check out the pull request?", lines, "Check out", false, func() tea.Cmd {
		return func() tea.Msg {
			out, err := gitctx.CheckoutPR("", number, local)
			if err != nil {
				return flashMsg{nError, err.Error()}
			}
			return flashMsg{nDone, "Checked out " + local + " · " + out}
		}
	})
}

// toggleThread resolves or unresolves the review thread of ref.
func (v *prView) toggleThread(a *App, ref itemRef) (Binding, bool) {
	for _, t := range v.pr.Threads {
		if t.FirstCommentID != ref.thread || ref.thread == 0 {
			continue
		}
		desc, locked, resolve := "resolve", lockIf(t.CanResolve, "you can't resolve this thread"), true
		if t.Resolved {
			desc, locked, resolve = "unresolve", lockIf(t.CanUnresolve, "you can't unresolve this thread"), false
		}
		id := t.ID
		return Binding{Keys: []string{"V"}, Desc: desc, Help: desc + " the thread", Mutates: true, Need: gh.CapComment, Repo: v.repo, Locked: locked,
			Do: do(func() tea.Cmd {
				return a.act(v.repo, capFirst(desc)+"d the thread", func(ctx context.Context) error {
					return a.client.ResolveThread(ctx, id, resolve)
				})
			})}, true
	}
	return Binding{}, false
}
