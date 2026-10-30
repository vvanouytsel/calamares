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

// repoChoices lists the repos a form can pick, the default first.
func (a *App) repoChoices(first string) []string {
	out := []string{}
	if first == "" {
		// The repo you pushed to last, where you can write.
		var best time.Time
		for _, r := range a.repos {
			if !r.Archived && !strings.EqualFold(r.Permission, "READ") && r.PushedAt.After(best) {
				first, best = r.FullName, r.PushedAt
			}
		}
	}
	if first != "" {
		out = append(out, first)
	}
	var rest []string
	for _, r := range a.repos {
		if !r.Archived && r.FullName != first {
			rest = append(rest, r.FullName)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// currentRepo is the repo the screen on display is about, or the one of the
// checkout Calamares started in.
func (a *App) currentRepo() string {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if rs, ok := a.stack[i].(RepoScoped); ok && rs.RepoName() != "" {
			return rs.RepoName()
		}
	}
	if a.opts.StartRepo != "" {
		return a.opts.StartRepo
	}
	return ""
}

// newIssue opens the form for a new issue in repo, or in the repo you are
// in or the first of your repos.
func (a *App) newIssue(repo string) tea.Cmd {
	if repo == "" {
		repo = a.currentRepo()
	}
	choices := a.repoChoices(repo)
	if len(choices) == 0 {
		return a.flashCmd(nHint, "No repository to open an issue in yet.")
	}
	repo = choices[0]
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ts, _ := a.client.IssueTemplates(ctx, repo)
		return modalMsg{a.issueForm(choices, ts)}
	}
}

func (a *App) issueForm(repos []string, templates []gh.IssueTemplate) *formModal {
	repoF := &formField{kind: fPick, label: "Repository", options: repos, required: true}
	tmplF := newChoiceFormField("Template", []string{"none"}, 0)
	titleF := newTextFormField("Title", "", true)
	bodyF := newAreaFormField("Body", "", 6)
	labelsF := &formField{kind: fMulti, label: "Labels"}
	assignF := &formField{kind: fMulti, label: "Assignees"}
	m := &formModal{title: "New issue", fields: []*formField{repoF, tmplF, titleF, bodyF, labelsF, assignF}, submit: "Create issue"}
	var tmpls []gh.IssueTemplate
	var applied *gh.IssueTemplate
	setTemplates := func(ts []gh.IssueTemplate) {
		tmpls = ts
		opts := []string{"none"}
		for _, t := range ts {
			opts = append(opts, t.Name)
		}
		tmplF.options, tmplF.choice = opts, 0
		applied = nil
	}
	setTemplates(templates)
	labelsF.candidates = func() ([]multiItem, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ls, err := a.client.RepoLabels(ctx, repoF.value())
		var items []multiItem
		for _, l := range ls {
			items = append(items, multiItem{value: l.Name, label: l.Name, color: l.Color})
		}
		return items, err
	}
	assignF.candidates = func() ([]multiItem, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		us, err := a.assignable(ctx, repoF.value())
		var items []multiItem
		for _, u := range us {
			items = append(items, multiItem{value: u, label: u})
		}
		return items, err
	}
	m.onChange = func(a *App, m *formModal, i int) tea.Cmd {
		switch m.fields[i] {
		case repoF:
			labelsF.picked, assignF.picked = nil, nil
			repo := repoF.value()
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				ts, _ := a.client.IssueTemplates(ctx, repo)
				return formAsyncMsg{func() {
					if repoF.value() == repo {
						setTemplates(ts)
					}
				}}
			}
		case tmplF:
			if tmplF.choice == 0 || tmplF.choice > len(tmpls) {
				return nil
			}
			t := tmpls[tmplF.choice-1]
			// Fill what you haven't changed since the last template.
			if applied == nil || titleF.value() == strings.TrimSpace(applied.Title) {
				titleF.text.SetValue(t.Title)
			}
			if applied == nil || bodyF.value() == strings.TrimSpace(applied.Body) {
				bodyF.area.SetValue(t.Body)
			}
			if len(t.Labels) > 0 {
				labelsF.picked = append([]string(nil), t.Labels...)
			}
			if len(t.Assignees) > 0 {
				assignF.picked = append([]string(nil), t.Assignees...)
			}
			applied = &t
		}
		return nil
	}
	m.onSubmit = func(a *App, m *formModal) tea.Cmd {
		repo, ttl, body := repoF.value(), titleF.value(), bodyF.value()
		labels, assignees := append([]string(nil), labelsF.picked...), append([]string(nil), assignF.picked...)
		number := 0
		return a.actThen(repo, "", func(ctx context.Context) error {
			n, err := a.client.CreateIssue(ctx, repo, ttl, body, labels, assignees)
			number = n
			return err
		}, func() tea.Cmd {
			a.setFlash(nDone, fmt.Sprintf("Created %s#%d", a.short(repo), number))
			return a.push(newIssueView(a, repo, number))
		})
	}
	m.focusField(2)
	return m
}
