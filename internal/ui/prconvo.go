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

// conversation is the first tab of a pull request: the description, the
// reviews and the comments, oldest first.
type conversation struct {
	disc   gh.Discussion
	loaded bool
	busy   bool
	err    error
	ver    int
	list   scrollList
	built  convBuild
	// unfolded are the items whose <details> blocks are open.
	unfolded map[string]bool
}

type convBuild struct{ w, ver int }

// itemRef says what a list item is, for replying to it and opening it.
type itemRef struct {
	author string
	body   string
	url    string
	thread int64 // the first comment of the thread, to reply to
}

// maxTextWidth keeps lines readable on wide terminals.
const maxTextWidth = 110

func (c *conversation) count() string {
	if !c.loaded {
		return ""
	}
	n := len(c.disc.Comments) + len(threadsOf(c.disc.Inline))
	return fmt.Sprintf(" %d", n)
}

// thread is a comment on a line of the diff with the replies to it.
type thread struct {
	root    gh.Comment
	replies []gh.Comment
}

func (t thread) comments() []gh.Comment { return append([]gh.Comment{t.root}, t.replies...) }

// threadsOf groups the comments on the diff into threads, oldest first.
func threadsOf(inline []gh.Comment) []thread {
	byRoot := map[int64]*thread{}
	var order []int64
	for _, c := range inline {
		if c.InReplyTo == 0 {
			byRoot[c.ID] = &thread{root: c}
			order = append(order, c.ID)
		}
	}
	for _, c := range inline {
		if t, ok := byRoot[c.InReplyTo]; ok && c.InReplyTo != 0 {
			t.replies = append(t.replies, c)
		}
	}
	out := make([]thread, 0, len(order))
	for _, id := range order {
		t := *byRoot[id]
		sort.SliceStable(t.replies, func(i, j int) bool { return t.replies[i].CreatedAt.Before(t.replies[j].CreatedAt) })
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].root.CreatedAt.Before(out[j].root.CreatedAt) })
	return out
}

// personColor gives everyone in a conversation a colour of their own, the
// same each time, from the hues that don't mean a status.
func personColor(name string) lipgloss.Color {
	hues := []lipgloss.Color{cTeal, cPink, cViolet, cGold,
		lipgloss.Color(mix(string(cTeal), string(cViolet), 0.5)), lipgloss.Color(mix(string(cPink), string(cGold), 0.5))}
	h := 0
	for _, r := range strings.ToLower(name) {
		h = h*31 + int(r)
	}
	return hues[(h%len(hues)+len(hues))%len(hues)]
}

// personBar is the bar at the left of what someone wrote, in their colour.
func personBar(name string) string {
	g := "┃"
	if ascii {
		g = "|"
	}
	return fg(lipgloss.Color(mix(string(personColor(name)), string(cBg), 0.35))).Render(g) + " "
}

// person is someone's name with a dot, in their colour.
func person(name string) string {
	c := personColor(name)
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render("● " + name)
}

// authorChip marks the author of the pull request.
func authorChip() string {
	return lipgloss.NewStyle().Foreground(cGold).Background(lipgloss.Color(mix(string(cBg), string(cGold), 0.16))).Render(" author ")
}

func commentHeader(author string, at time.Time, prAuthor string, now time.Time, prefix string) string {
	h := personBar(author) + prefix + person(author)
	if strings.EqualFold(author, prAuthor) {
		h += " " + authorChip()
	}
	return h + "  " + icoIn("clock", cSteel) + sDim.Render(age(at, now)+" ago")
}

func indentLines(lines []string, indent string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = indent + l
	}
	return out
}

// commentBlock draws one comment: who and when, then its text.
func commentBlock(c gh.Comment, w int, prAuthor string, now time.Time, prefix string) []string {
	return foldedCommentBlock(c, w, prAuthor, now, prefix, false)
}

// foldedCommentBlock is commentBlock; with fold, a <details> block shows only its summary.
func foldedCommentBlock(c gh.Comment, w int, prAuthor string, now time.Time, prefix string, fold bool) []string {
	lines := []string{commentHeader(c.Author, c.CreatedAt, prAuthor, now, prefix)}
	if strings.TrimSpace(c.Body) == "" {
		return append(lines, personBar(c.Author)+sDim.Render("  (empty)"))
	}
	return append(lines, indentLines(renderMarkdownFolded(c.Body, w-4, fold), personBar(c.Author)+"  ")...)
}

// threadBlock draws a diff comment thread's replies; with its hunk the code it is about comes first.
func threadBlock(t thread, w int, prAuthor string, now time.Time, withHunk bool) []string {
	var lines []string
	if withHunk {
		where := t.root.Path
		if t.root.Line > 0 {
			where += fmt.Sprintf(":%d", t.root.Line)
		}
		head := ico("file") + sCyan.Render(where)
		if t.root.Outdated() {
			head += " " + statusChip(KRun, 0, "outdated")
		}
		lines = append(lines, head)
		for _, l := range hunkTail(t.root.DiffHunk, 4) {
			lines = append(lines, personBar(t.root.Author)+"  "+clip(diffLineStyle(l).Render(strings.ReplaceAll(l, "\t", "    ")), w-4))
		}
	}
	for i, c := range t.comments() {
		prefix := ""
		if i > 0 {
			prefix = sDim.Render("↳ ")
		}
		lines = append(lines, commentBlock(c, w, prAuthor, now, prefix)...)
	}
	return lines
}

func hunkTail(hunk string, n int) []string {
	lines := strings.Split(strings.TrimRight(hunk, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func (c *conversation) items(a *App, v *prView, w int) []listItem {
	now := time.Now()
	pr := v.pr
	var items []listItem

	var reviewers []string
	for _, r := range pr.Reviews {
		reviewers = append(reviewers, person(r.Author)+" "+reviewState(r.State))
	}
	for _, who := range pr.Requested {
		reviewers = append(reviewers, person(who)+" "+sWait.Render("◆ requested"))
	}
	if len(reviewers) == 0 {
		reviewers = []string{sDim.Render("none yet")}
	}
	items = append(items, listItem{lines: []string{ico("eye") + sDim.Render("Reviewers  ") + strings.Join(reviewers, "   "), ""}})

	body := []string{personBar(pr.Author) + person(pr.Author) + " " + authorChip() + sDim.Render("  opened this pull request")}
	if strings.TrimSpace(pr.Body) == "" {
		body = append(body, personBar(pr.Author)+sDim.Render("  No description provided."))
	} else {
		body = append(body, indentLines(renderMarkdownFolded(pr.Body, w-4, !c.unfolded["body"]), personBar(pr.Author)+"  ")...)
	}
	items = append(items, listItem{id: "body", lines: append(body, ""), selectable: true,
		data: itemRef{author: pr.Author, body: pr.Body, url: pr.URL}})

	type entry struct {
		at   time.Time
		item listItem
	}
	var entries []entry
	for _, cm := range c.disc.Comments {
		id := fmt.Sprintf("c%d", cm.ID)
		lines := append(foldedCommentBlock(cm, w, pr.Author, now, "", !c.unfolded[id]), "")
		entries = append(entries, entry{cm.CreatedAt, listItem{id: id, lines: lines, selectable: true,
			data: itemRef{author: cm.Author, body: cm.Body, url: cm.URL}}})
	}
	for _, r := range c.disc.Reviews {
		if r.State == "PENDING" || (r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "") {
			continue
		}
		lines := []string{personBar(r.Author) + person(r.Author) + " " + reviewState(r.State) + "  " + icoIn("clock", cSteel) + sDim.Render(age(r.SubmittedAt, now)+" ago")}
		id := fmt.Sprintf("r%d", r.ID)
		if strings.TrimSpace(r.Body) != "" {
			lines = append(lines, indentLines(renderMarkdownFolded(r.Body, w-4, !c.unfolded[id]), personBar(r.Author)+"  ")...)
		}
		entries = append(entries, entry{r.SubmittedAt, listItem{id: id, lines: append(lines, ""), selectable: true,
			data: itemRef{author: r.Author, body: r.Body, url: r.URL}}})
	}
	for _, t := range threadsOf(c.disc.Inline) {
		lines := append(threadBlock(t, w, pr.Author, now, true), "")
		for _, th := range pr.Threads {
			if th.FirstCommentID == t.root.ID {
				if th.Resolved {
					lines[0] += "  " + statusChip(KOK, 0, "resolved")
				} else {
					lines[0] += "  " + statusChip(KWait, 0, "unresolved")
				}
			}
		}
		entries = append(entries, entry{t.root.CreatedAt, listItem{id: fmt.Sprintf("t%d", t.root.ID), lines: lines, selectable: true,
			data: itemRef{author: t.root.Author, body: t.root.Body, url: t.root.URL, thread: t.root.ID}}})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	for _, e := range entries {
		items = append(items, e.item)
	}
	return items
}

func (c *conversation) render(a *App, v *prView, w, h int) string {
	inner := min(w-4, maxTextWidth)
	if c.built != (convBuild{inner, c.ver}) || len(c.list.items) == 0 {
		first := len(c.list.items) == 0
		c.list.setItems(c.items(a, v, inner-2))
		if first {
			c.list.first()
		}
		c.built = convBuild{inner, c.ver}
	}
	lines := c.list.render(w-2, h-2)
	if c.err != nil {
		lines = append([]string{" " + sRed.Render(fit("Could not load the comments: "+c.err.Error(), w-4))}, lines...)[:h-2]
	} else if !c.loaded {
		lines = append([]string{" " + sDim.Render("Loading comments…")}, lines...)[:h-2]
	}
	scope := ""
	if c.loaded {
		scope = "W comments or replies"
	}
	return box(title("Conversation", scope, -1, ""), strings.Join(lines, "\n"), w, h, cFrame)
}

// keys are the keys of the Conversation tab.
func (c *conversation) keys(a *App, v *prView) []Binding {
	bs := []Binding{c.list.moveKeys("move")}
	item, ok := c.list.selectedItem()
	ref, _ := item.data.(itemRef)
	reply := Binding{Keys: []string{"W", "enter"}, Label: "W", Desc: "reply", Help: "reply to the selected comment", Off: !ok, Mutates: true, Need: gh.CapComment, Repo: v.repo,
		Do: do(func() tea.Cmd { return v.reply(a, ref) })}
	if !ok {
		reply.Do = nil
	}
	bs = append(bs, reply)
	if ok {
		if b, found := v.toggleThread(a, ref); found {
			bs = append(bs, b)
		}
	}
	if ok && hasDetails(ref.body) {
		desc := "unfold details"
		if c.unfolded[item.id] {
			desc = "fold details"
		}
		bs = append(bs, Binding{Keys: []string{"v"}, Desc: desc, Help: "open or close the folded parts (<details>) of the selected comment",
			Do: do(func() tea.Cmd {
				if c.unfolded == nil {
					c.unfolded = map[string]bool{}
				}
				c.unfolded[item.id] = !c.unfolded[item.id]
				c.ver++
				return nil
			})})
	}
	if ok && ref.url != "" {
		bs = append(bs, Binding{Keys: []string{"o"}, Help: "open the comment in the browser", Do: do(func() tea.Cmd { return a.openURL(ref.url) })})
	}
	return grouped("CONVERSATION", bs...)
}

// reply answers a comment: in its thread for a comment on the diff, as a new
// comment quoting it otherwise.
func (v *prView) reply(a *App, ref itemRef) tea.Cmd {
	label := fmt.Sprintf("%s #%d", a.short(v.repo), v.number)
	if ref.thread != 0 {
		return v.compose(a, "Reply to "+ref.author, "your reply", "", func(ctx context.Context, text string) error {
			return a.client.ReplyToReviewComment(ctx, v.repo, v.number, ref.thread, text)
		}, "Replied on "+label)
	}
	return v.compose(a, "Reply to "+ref.author, "your reply", quote(ref), func(ctx context.Context, text string) error {
		return a.client.CommentOnPullRequest(ctx, v.repo, v.number, text)
	}, "Commented on "+label)
}

// quote starts a reply with the first lines of what it answers.
func quote(ref itemRef) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(ref.body, "\r", "")), "\n") {
		if len(lines) == 3 {
			lines = append(lines, "> …")
			break
		}
		lines = append(lines, "> "+l)
	}
	if len(lines) == 0 || ref.body == "" {
		return ""
	}
	return fmt.Sprintf("> @%s wrote:\n%s\n\n", ref.author, strings.Join(lines, "\n"))
}
