package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
	"github.com/vvanouytsel/calamares/internal/gitctx"
)

// inCheckout reports whether Calamares was started in a checkout of repo.
func (a *App) inCheckout(repo string) bool {
	return a.opts.FromCwd && strings.EqualFold(a.opts.StartRepo, repo)
}

// readableBranch makes a title of a branch name: "feat/delivery-estimates"
// becomes "Delivery estimates".
func readableBranch(b string) string {
	if i := strings.LastIndex(b, "/"); i >= 0 {
		b = b[i+1:]
	}
	b = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(b))
	if b == "" {
		return ""
	}
	return strings.ToUpper(b[:1]) + b[1:]
}

// branchHint is the line on the repo screen that offers a pull request for
// your branch, or "" when there is nothing to offer.
func (v *repoView) branchHint(a *App) string {
	if !a.inCheckout(v.repo) || a.opts.Branch == "" {
		return ""
	}
	if r, ok := a.repoByName(v.repo); ok && r.DefaultBranch == a.opts.Branch {
		return ""
	}
	if v.pulls == nil || v.pulls.at.IsZero() {
		return ""
	}
	for _, pr := range v.pulls.prs {
		if pr.Branch == a.opts.Branch {
			return ""
		}
	}
	return fact("pr", "Your branch "+a.opts.Branch+" has no pull request") + sDim.Render(" · ") + keycap("+") + sDim.Render(" create")
}

// newPRKey is the key that opens a pull request for your branch.
func (a *App) newPRKey(repo string) Binding {
	here := a.inCheckout(repo) && a.opts.Branch != ""
	return Binding{Keys: []string{"+"}, Desc: "new pull request", Help: "open a pull request for the branch you are on", Mutates: true, Need: gh.CapComment, Repo: repo,
		Locked: lockIf(here, "start Calamares inside a checkout of "+repo+", on a branch"),
		Do:     do(func() tea.Cmd { return a.newPR(repo, a.opts.Branch, true) })}
}

// newPR opens the form for a pull request of head into the repo's default
// branch. With local set, head is the branch of the checkout Calamares is in:
// unpushed commits are offered to be pushed first.
func (a *App) newPR(repo, head string, local bool) tea.Cmd {
	base := ""
	if r, ok := a.repoByName(repo); ok {
		base = r.DefaultBranch
	}
	if head == "" {
		return a.flashCmd(nHint, "Which branch? Start Calamares in a checkout on your branch.")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if base == "" {
			if r, err := a.client.GetRepo(ctx, repo); err == nil {
				base = r.DefaultBranch
			}
		}
		if base == "" {
			base = "main"
		}
		branches, _ := a.client.ListBranches(ctx, repo)
		if len(branches) == 0 {
			branches = []string{base}
		}
		info := prDraftInfo{repo: repo, head: head, base: base, branches: branches}
		if local {
			info.fill()
		}
		info.template = a.client.PullRequestTemplate(ctx, repo, base)
		return modalMsg{a.prForm(info)}
	}
}

// prDraftInfo is what the form is prefilled from.
type prDraftInfo struct {
	repo, head, base string
	branches         []string
	local            bool
	unpushed         int
	subjects         []string
	template         string
}

// fill reads the checkout: what is not pushed yet, and the commit subjects.
func (i *prDraftInfo) fill() {
	i.local = true
	ref := "origin/" + i.base
	if n, err := gitctx.Ahead("", ref); err == nil {
		i.unpushed = n
	}
	i.subjects, _ = gitctx.Subjects("", ref)
	if gitctx.Upstream("") == "" {
		i.unpushed = len(i.subjects)
	}
}

func (i prDraftInfo) title() string {
	if len(i.subjects) == 1 {
		return i.subjects[0]
	}
	return readableBranch(i.head)
}

func (i prDraftInfo) body() string {
	if strings.TrimSpace(i.template) != "" {
		return strings.TrimSpace(i.template)
	}
	var b strings.Builder
	for _, s := range i.subjects {
		b.WriteString("- " + s + "\n")
	}
	return strings.TrimSpace(b.String())
}

func (a *App) prForm(info prDraftInfo) *formModal {
	baseF := &formField{kind: fPick, label: "Base", options: info.branches, required: true}
	for k, b := range info.branches {
		if b == info.base {
			baseF.choice = k
		}
	}
	titleF := newTextFormField("Title", info.title(), true)
	bodyF := newAreaFormField("Description", info.body(), 6)
	draftF := &formField{kind: fBool, label: "Draft"}
	reviewersF := &formField{kind: fMulti, label: "Reviewers"}
	labelsF := &formField{kind: fMulti, label: "Labels"}
	fields := []*formField{baseF, titleF, bodyF, draftF, reviewersF, labelsF}
	var pushF *formField
	if info.local && info.unpushed > 0 {
		pushF = &formField{kind: fBool, label: fmt.Sprintf("↑ %d commits not pushed", info.unpushed), on: true, hint: "push first"}
		fields = append([]*formField{pushF}, fields...)
	}
	reviewersF.candidates = func() ([]multiItem, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		us, err := a.assignable(ctx, info.repo)
		var items []multiItem
		for _, u := range us {
			if !strings.EqualFold(u, a.me) {
				items = append(items, multiItem{value: u, label: u})
			}
		}
		return items, err
	}
	labelsF.candidates = func() ([]multiItem, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ls, err := a.client.RepoLabels(ctx, info.repo)
		var items []multiItem
		for _, l := range ls {
			items = append(items, multiItem{value: l.Name, label: l.Name, color: l.Color})
		}
		return items, err
	}
	m := &formModal{title: "New pull request", fields: fields, submit: "Create pull request",
		lines: []string{fact("branch", info.head) + sDim.Render(" → ") + sText.Render(a.short(info.repo))}}
	m.onSubmit = func(a *App, m *formModal) tea.Cmd {
		ttl, body, base, draft := titleF.value(), bodyF.value(), baseF.value(), draftF.on
		reviewers, labels := append([]string(nil), reviewersF.picked...), append([]string(nil), labelsF.picked...)
		push := pushF != nil && pushF.on
		number := 0
		return a.actThen(info.repo, "", func(ctx context.Context) error {
			if push {
				if _, err := gitctx.Push("", info.head); err != nil {
					return err
				}
			}
			n, err := a.client.CreatePullRequest(ctx, info.repo, ttl, info.head, base, body, draft)
			if err != nil {
				return err
			}
			number = n
			if len(reviewers) > 0 {
				if err := a.client.RequestReviewers(ctx, info.repo, n, reviewers); err != nil {
					return err
				}
			}
			if len(labels) > 0 {
				return a.client.SetLabels(ctx, info.repo, n, labels)
			}
			return nil
		}, func() tea.Cmd {
			a.setFlash(nDone, fmt.Sprintf("Created %s#%d · watching the runs it starts", a.short(info.repo), number))
			a.tab = tabPulls
			a.stack = []View{a.roots[tabPulls]}
			return tea.Batch(a.push(newPRView(a, info.repo, number)), a.awaitBranchRun(info.repo, info.head))
		})
	}
	m.focusField(len(fields) - 5)
	return m
}
