package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// issueView is one issue: its description, then its comments, with the
// panel of the list beside it.
type issueView struct {
	id     int
	repo   string
	number int
	d      gh.IssueDetail
	loaded bool
	loadState
	list  scrollList
	ver   int
	built convBuild
}

// issueLiveEvery is how often an open issue refreshes while live.
const issueLiveEvery = 30 * time.Second

type issueDetailMsg struct {
	id  int
	d   gh.IssueDetail
	err error
}

func (m issueDetailMsg) viewID() int { return m.id }

func newIssueView(a *App, repo string, number int) *issueView {
	return &issueView{id: a.newID(), repo: repo, number: number}
}

func (v *issueView) ID() int                  { return v.id }
func (v *issueView) RepoName() string         { return v.repo }
func (v *issueView) Crumb() string            { return fmt.Sprintf("#%d", v.number) }
func (v *issueView) Init(a *App) tea.Cmd      { return v.load(a) }
func (v *issueView) Updated(a *App) time.Time { return v.at }
func (v *issueView) Loading(a *App) bool      { return v.busy }

func (v *issueView) load(a *App) tea.Cmd {
	if !v.begin() {
		return nil
	}
	id, repo, number := v.id, v.repo, v.number
	return func() tea.Msg {
		d, err := a.client.GetIssue(context.Background(), repo, number)
		return issueDetailMsg{id, d, err}
	}
}

func (v *issueView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(issueLiveEvery) {
		return nil
	}
	return v.load(a)
}

func (v *issueView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case issueDetailMsg:
		v.end(msg.err)
		if msg.err != nil {
			if v.loaded {
				return a.flashCmd(nError, "Could not refresh the issue: "+msg.err.Error())
			}
			return nil
		}
		v.d, v.loaded = msg.d, true
		v.ver++
		return nil
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

// issue is the issue as the screen knows it now.
func (v *issueView) issue() gh.Issue { return v.d.Issue }

func (v *issueView) Keys(a *App) []Binding {
	bs := []Binding{v.list.moveKeys("move")}
	if !v.loaded {
		return grouped("AN ISSUE", bs...)
	}
	item, ok := v.list.selectedItem()
	ref, _ := item.data.(itemRef)
	reply := Binding{Keys: []string{"W", "enter"}, Label: "W", Desc: "reply", Help: "reply to the selected comment, quoting it", Off: !ok, Mutates: true, Need: gh.CapComment, Repo: v.repo}
	if ok {
		label := fmt.Sprintf("%s #%d", a.short(v.repo), v.number)
		reply.Do = do(func() tea.Cmd {
			about := []string{sWhite.Render(label) + sDim.Render(" · "+v.d.Title)}
			return askEditor("Reply to "+ref.author, about, "your reply", quote(ref), "posts it", true, func(text string) tea.Cmd {
				return a.act(v.repo, "Commented on "+label, func(ctx context.Context) error {
					return a.client.CommentOnPullRequest(ctx, v.repo, v.number, text)
				})
			})
		})
	}
	bs = append(bs, reply, a.personKey(ref.author))
	bs = append(bs, a.issueKeys(v.issue())...)
	bs = append(bs, a.newIssueKey(v.repo))
	return grouped("AN ISSUE", bs...)
}

func (v *issueView) items(w int) []listItem {
	now := time.Now()
	d := v.d
	body := []string{sWhite.Bold(true).Render(d.Author) + sTan.Render(" · author") + sDim.Render(" · opened this issue "+age(d.CreatedAt, now)+" ago")}
	if strings.TrimSpace(d.Body) == "" {
		body = append(body, sDim.Render("  No description provided."))
	} else {
		body = append(body, indentLines(renderMarkdown(d.Body, w-2), "  ")...)
	}
	items := []listItem{{id: "body", lines: append(body, ""), selectable: true, data: itemRef{author: d.Author, body: d.Body, url: d.URL}}}
	for _, c := range d.CommentList {
		lines := append(commentBlock(gh.Comment{ID: c.ID, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt}, w, d.Author, now, ""), "")
		items = append(items, listItem{id: fmt.Sprintf("c%d", c.ID), lines: lines, selectable: true, data: itemRef{author: c.Author, body: c.Body, url: c.URL}})
	}
	return items
}

func (v *issueView) Render(a *App, w, h int) string {
	if !v.loaded {
		msg := "Loading issue…"
		if v.err != nil {
			msg = "Could not load the issue: " + v.err.Error()
		}
		return box(title("Issue", fmt.Sprintf("%s #%d", v.repo, v.number), -1, ""), " "+sDim.Render(msg), w, h, cFrame)
	}
	d, now := v.d, time.Now()
	stateC, label := cGreen, "open"
	if d.State == "CLOSED" {
		stateC, label = cViolet, "closed · completed"
		if d.StateReason == "NOT_PLANNED" {
			stateC, label = cGrey, "closed · not planned"
		}
	}
	state := lipgloss.NewStyle().Foreground(stateC).Background(lipgloss.Color(mix(string(cBg), string(stateC), 0.2))).Render(" " + icon("issue") + label + " ")
	var labels []string
	for _, l := range d.Labels {
		labels = append(labels, labelChip(l.Name, l.Color))
	}
	head := []string{
		clip(" "+icoIn("issue", stateC)+sGold.Bold(true).Render(fmt.Sprintf("#%d", d.Number))+" "+sWhite.Bold(true).Render(d.Title)+"   "+ico("repo")+a.repoLabel(v.repo)+a.roleChip(v.repo), w),
		clip(" "+state+"  "+fact("user", d.Author)+"   "+fact("clock", age(d.UpdatedAt, now))+"   "+fact("comment", fmt.Sprint(d.Comments))+"   "+strings.Join(labels, " "), w),
	}
	bodyH := h - len(head)
	bw, side := w, []string(nil)
	if w >= panelMinWidth {
		pw := min(max(w*3/10, 34), 46)
		bw, side = w-pw-1, issuePanel(a, v.enriched(a), pw, bodyH)
	}
	inner := min(bw-4, maxTextWidth)
	if v.built != (convBuild{inner, v.ver}) || len(v.list.items) == 0 {
		first := len(v.list.items) == 0
		v.list.setItems(v.items(inner - 2))
		if first {
			v.list.first()
		}
		v.built = convBuild{inner, v.ver}
	}
	body := box(title("Conversation", "W comments or replies", -1, ""), strings.Join(v.list.render(bw-2, bodyH-2), "\n"), bw, bodyH, cFrame)
	if side != nil {
		rows := strings.Split(body, "\n")
		lines := make([]string, bodyH)
		for i := range lines {
			l := ""
			if i < len(rows) {
				l = rows[i]
			}
			lines[i] = fit(l, bw) + " " + side[i]
		}
		body = strings.Join(lines, "\n")
	}
	return strings.Join(head, "\n") + "\n" + body
}

// enriched is the issue with how it concerns you, when the lists hold it.
func (v *issueView) enriched(a *App) gh.Issue {
	i := v.d.Issue
	for _, held := range a.st.allIssues() {
		if held.Repo == v.repo && held.Number == v.number {
			i.Why = held.Why
		}
	}
	return i
}
