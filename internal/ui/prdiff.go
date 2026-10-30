package ui

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// changes is the second tab of a pull request: the changed files on the left
// and the diff of the selected file on the right, with the comments made on
// its lines.
type changes struct {
	files     []gh.FileDiff
	loaded    bool
	busy      bool
	err       error
	list      scrollList
	diff      scrollList
	focusDiff bool
	built     diffBuild
	listBuilt int
	listVer   int
	listWidth int
	// readOnly hides commenting, for a diff that has no pull request behind it.
	readOnly bool
}

// changesHost is what a diff needs to know about the thing it belongs to.
type changesHost struct {
	discussion gh.Discussion
	version    int
	author     string
}

type diffBuild struct {
	path   string
	w, ver int
}

// diffRef says what a diff list item is: a line of code, where a comment on it goes, or a thread.
type diffRef struct {
	path   string
	line   int
	side   string // RIGHT or LEFT
	code   string // the text of the line, for a suggestion
	thread int64  // the first comment of a thread, when the item is one
	author string
	first  *thread // the first thread on a line of code, to reply to
}

// diffLine is one line of a patch.
type diffLine struct {
	kind         byte // ' ', '+', '-', '@' for a hunk header or '\\' for a note
	text         string
	oldNo, newNo int
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func parsePatch(patch string) []diffLine {
	var out []diffLine
	oldNo, newNo := 0, 0
	for _, l := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[2])
			out = append(out, diffLine{kind: '@', text: l})
			continue
		}
		if l == "" {
			l = " "
		}
		switch l[0] {
		case '+':
			out = append(out, diffLine{'+', l[1:], 0, newNo})
			newNo++
		case '-':
			out = append(out, diffLine{'-', l[1:], oldNo, 0})
			oldNo++
		case '\\':
			out = append(out, diffLine{kind: '\\', text: l})
		default:
			out = append(out, diffLine{' ', l[1:], oldNo, newNo})
			oldNo++
			newNo++
		}
	}
	return out
}

var (
	addBg  = lipgloss.Color("#10301f")
	delBg  = lipgloss.Color("#3a1522")
	hunkBg = lipgloss.Color("#16263d")
)

// diffLineStyle colours a line of a diff hunk by its first character.
func diffLineStyle(l string) lipgloss.Style {
	switch {
	case strings.HasPrefix(l, "+"):
		return sGreen
	case strings.HasPrefix(l, "-"):
		return sRed
	case strings.HasPrefix(l, "@@"):
		return sTan
	}
	return sDim
}

func (c *changes) setFiles(files []gh.FileDiff) {
	c.files, c.loaded = files, true
	c.listVer++
}

func (c *changes) selected() (gh.FileDiff, bool) {
	item, ok := c.list.selectedItem()
	if !ok {
		return gh.FileDiff{}, false
	}
	i, _ := item.data.(int)
	return c.files[i], true
}

func statusMark(status string) string {
	switch status {
	case "added":
		return sGreen.Render("A")
	case "removed":
		return sRed.Render("D")
	case "renamed":
		return sTan.Render("R")
	}
	return sAmber.Render("M")
}

func (c *changes) fileItems(w int, discussion gh.Discussion) []listItem {
	comments := map[string]int{}
	for _, t := range threadsOf(discussion.Inline) {
		comments[t.root.Path]++
	}
	var items []listItem
	lastDir := ""
	for i, f := range c.files {
		dir := path.Dir(f.Path)
		if dir == "." {
			dir = ""
		}
		indent := ""
		if dir != "" {
			indent = "  "
			if dir != lastDir {
				items = append(items, listItem{lines: []string{sDim.Render(midCut(dir+"/", w))}})
			}
		}
		lastDir = dir
		tail := " " + sGreen.Render(fmt.Sprintf("+%d", f.Additions)) + " " + sRed.Render(fmt.Sprintf("−%d", f.Deletions))
		if n := comments[f.Path]; n > 0 {
			tail += " " + sAmber.Render(fmt.Sprintf("●%d", n))
		}
		name := midCut(path.Base(f.Path), max(w-width(tail)-width(indent)-2, 8))
		items = append(items, listItem{id: f.Path, selectable: true, data: i,
			lines: []string{indent + statusMark(f.Status) + " " + sText.Render(name) + tail}})
	}
	return items
}

// rowKeys are the places a comment can be attached to for a line of code.
func rowKeys(l diffLine) []string {
	switch l.kind {
	case '+':
		return []string{fmt.Sprintf("RIGHT:%d", l.newNo)}
	case '-':
		return []string{fmt.Sprintf("LEFT:%d", l.oldNo)}
	}
	return []string{fmt.Sprintf("RIGHT:%d", l.newNo), fmt.Sprintf("LEFT:%d", l.oldNo)}
}

func (c *changes) diffItems(f gh.FileDiff, discussion gh.Discussion, w int, prAuthor string) []listItem {
	if strings.TrimSpace(f.Patch) == "" {
		msg := "No diff to show: the file is binary, empty or too large. o opens the pull request in the browser."
		if f.Status == "renamed" {
			msg = "Renamed without changes."
		}
		return []listItem{{lines: []string{sDim.Render(msg)}}}
	}
	lines := parsePatch(f.Patch)
	top := 0
	for _, l := range lines {
		top = max(top, l.oldNo, l.newNo)
	}
	nw := max(len(strconv.Itoa(top)), 3)
	numbers := 2*nw + 2
	codeW := max(w-numbers-2, 10)

	threads := map[string][]thread{}
	matched := map[int64]bool{}
	for _, t := range threadsOf(discussion.Inline) {
		if t.root.Path == f.Path && !t.root.Outdated() {
			key := fmt.Sprintf("%s:%d", t.root.Side, t.root.Line)
			threads[key] = append(threads[key], t)
		}
	}
	now := time.Now()
	var items []listItem
	for _, l := range lines {
		if l.kind == '@' {
			items = append(items, listItem{lines: []string{lipgloss.NewStyle().Foreground(cTan).Background(hunkBg).Render(fit(l.text, w))}})
			continue
		}
		if l.kind == '\\' {
			items = append(items, listItem{lines: []string{strings.Repeat(" ", numbers) + sDim.Render(l.text)}})
			continue
		}
		ref := diffRef{path: f.Path, line: l.newNo, side: "RIGHT", code: l.text}
		if l.kind == '-' {
			ref.code = ""
			ref.line, ref.side = l.oldNo, "LEFT"
		}
		id := fmt.Sprintf("%s:%d:%c", ref.side, ref.line, l.kind)
		var after []thread
		for _, key := range rowKeys(l) {
			for _, t := range threads[key] {
				if !matched[t.root.ID] {
					matched[t.root.ID] = true
					after = append(after, t)
				}
			}
		}
		if len(after) > 0 {
			ref.first = &after[0]
		}
		plain, selected := codeRow(l, nw, codeW, false), codeRow(l, nw, codeW, true)
		items = append(items, listItem{id: id, lines: plain, selected: selected, selectable: true, data: ref})
		for _, t := range after {
			items = append(items, threadItem(t, numbers, w, prAuthor, now))
		}
	}

	var loose []listItem
	for _, t := range threadsOf(discussion.Inline) {
		if t.root.Path == f.Path && !matched[t.root.ID] {
			loose = append(loose, listItem{id: fmt.Sprintf("t%d", t.root.ID), selectable: true,
				lines: append(threadBlock(t, w, prAuthor, now, true), ""),
				data:  diffRef{path: f.Path, thread: t.root.ID, author: t.root.Author}})
		}
	}
	if len(loose) > 0 {
		items = append(items, listItem{lines: []string{"", sAmber.Render("Comments that are not on a line shown above")}})
		items = append(items, loose...)
	}
	return items
}

func threadItem(t thread, numbers, w int, prAuthor string, now time.Time) listItem {
	inner := threadBlock(t, w-numbers-2, prAuthor, now, false)
	lines := make([]string, len(inner))
	for i, l := range inner {
		lines[i] = strings.Repeat(" ", numbers+2) + l
	}
	return listItem{id: fmt.Sprintf("t%d", t.root.ID), lines: lines, selectable: true,
		data: diffRef{path: t.root.Path, line: t.root.Line, side: t.root.Side, thread: t.root.ID, author: t.root.Author}}
}

// codeRow draws one line of a diff, wrapped to the width of the code.
func codeRow(l diffLine, nw, codeW int, selected bool) []string {
	bg := lipgloss.Color("")
	sign, fgc := " ", cText
	switch l.kind {
	case '+':
		bg, sign, fgc = addBg, "+", cGreen
	case '-':
		bg, sign, fgc = delBg, "−", cRed
	}
	if selected {
		bg = cSelBg
	}
	style := func(c lipgloss.Color) lipgloss.Style {
		st := lipgloss.NewStyle().Foreground(c)
		if bg != lipgloss.Color("") {
			st = st.Background(bg)
		}
		return st
	}
	number := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", nw)
		}
		return fmt.Sprintf("%*d", nw, n)
	}
	text := strings.ReplaceAll(stripControl(l.text), "\t", "    ")
	var out []string
	for i, part := range hardWrap(text, codeW) {
		prefix := number(l.oldNo) + " " + number(l.newNo) + " "
		mark := sign
		if i > 0 {
			prefix, mark = strings.Repeat(" ", 2*nw+2), " "
		}
		out = append(out, style(cDim).Render(prefix)+style(fgc).Render(mark+" "+fit(part, codeW)))
	}
	return out
}

// keys are the keys of a diff: the files on the left, the selected file's
// diff on the right. comment starts a comment on the selected line; it isn't
// used for a read-only diff.
func (c *changes) keys(a *App, browserURL string, comment func(replyOnly bool) tea.Cmd) []Binding {
	otherFile := Binding{Keys: []string{"}", "{"}, Label: "{ }", Desc: "other file", Help: "previous / next file",
		Do: func(k string) tea.Cmd { c.list.step(map[string]int{"}": 1, "{": -1}[k]); return nil }}
	browser := Binding{Keys: []string{"o"}, Help: "open in the browser", Do: do(func() tea.Cmd { return a.openURL(browserURL) })}
	if !c.focusDiff {
		otherFile.Desc = ""
		return grouped("CHANGES",
			c.list.moveKeys("file"),
			Binding{Keys: []string{"enter", "l", "right"}, Label: "enter", Desc: "show diff", Do: do(func() tea.Cmd { c.focusDiff = true; return nil })},
			otherFile, browser,
		)
	}
	bs := []Binding{c.diff.moveKeys("move")}
	if c.readOnly {
		bs = append(bs, Binding{Keys: []string{"enter"}})
	} else {
		bs = append(bs,
			Binding{Keys: []string{"c", "enter"}, Label: "c", Desc: "comment on line", Help: "comment on the selected line", Mutates: true, Need: gh.CapComment,
				Do: do(func() tea.Cmd { return comment(false) })},
			Binding{Keys: []string{"r"}, Desc: "reply", Help: "reply to the thread on the selected line", Mutates: true, Need: gh.CapComment,
				Do: do(func() tea.Cmd { return comment(true) })},
		)
	}
	bs = append(bs, otherFile,
		Binding{Keys: []string{"h", "left"}, Label: "←", Desc: "files", Do: do(func() tea.Cmd { c.focusDiff = false; return nil })},
		browser,
	)
	return grouped("CHANGES", bs...)
}

// comment starts a thread on the selected line, or replies to the thread there.
func (c *changes) comment(a *App, v *prView, replyOnly bool) tea.Cmd {
	if !c.focusDiff {
		return nil
	}
	item, ok := c.diff.selectedItem()
	ref, isRef := item.data.(diffRef)
	if !ok || !isRef {
		return nil
	}
	label := fmt.Sprintf("%s #%d", a.short(v.repo), v.number)
	reply := ref.thread
	if reply == 0 && ref.first != nil {
		reply = ref.first.root.ID
	}
	if reply != 0 {
		author := ref.author
		if author == "" {
			author = ref.first.root.Author
		}
		return v.compose(a, "Reply to "+author, "your reply", "", func(ctx context.Context, text string) error {
			return a.client.ReplyToReviewComment(ctx, v.repo, v.number, reply, text)
		}, "Replied on "+label)
	}
	if replyOnly {
		return a.flashCmd(nHint, "No comment on this line to reply to: c starts one.")
	}
	if ref.line == 0 || v.pr.HeadSHA == "" {
		return a.flashCmd(nHint, "Select a line of code to comment on.")
	}
	where := fmt.Sprintf("%s:%d", ref.path, ref.line)
	about := []string{sWhite.Render(label) + sDim.Render(" · "+v.pr.Title), sCyan.Render(where)}
	sent := func(text string) tea.Cmd {
		return a.act(v.repo, "Commented on "+where, func(ctx context.Context) error {
			return a.client.CommentOnLine(ctx, v.repo, v.number, v.pr.HeadSHA, ref.path, ref.line, ref.side, text)
		})
	}
	added := func(text string) tea.Cmd {
		a.addPending(v.repo, v.number, gh.DraftComment{Path: ref.path, Line: ref.line, Side: ref.side, Body: text})
		v.diff.listVer++
		n := len(a.pendingFor(v.repo, v.number))
		return a.flashCmd(nDone, fmt.Sprintf("Added to your review · %d pending · a, C or m sends them together", n))
	}
	return openEditor(&editorModal{title: sCyan.Bold(true).Render("Comment on " + where), lines: about, required: true,
		send: "add to review", onSubmit: added, altSend: "comment now", onAlt: sent, suggestion: ref.code}, "your comment")
}

func (c *changes) render(a *App, v *prView, w, h int) string {
	disc := v.conv.disc
	if pend := a.pendingAsComments(v.repo, v.number); len(pend) > 0 {
		disc.Inline = append(slices.Clone(disc.Inline), pend...)
	}
	return c.renderHost(a, changesHost{disc, v.conv.ver, v.pr.Author}, w, h)
}

func (c *changes) renderHost(a *App, host changesHost, w, h int) string {
	if !c.loaded {
		msg := "Loading the changes…"
		if c.err != nil {
			msg = "Could not load the changes: " + c.err.Error()
		}
		return box(title("Changes", "", -1, ""), " "+sDim.Render(msg), w, h, cFrame)
	}
	lw := min(max(w/3, 28), 48)
	if w < 80 {
		lw = max(w/3, 20)
	}
	c.listWidth = lw
	dw := w - lw
	discussion := host.discussion

	if c.listBuilt != c.listVer || len(c.list.items) == 0 {
		first := len(c.list.items) == 0
		c.list.setItems(c.fileItems(lw-6, discussion))
		if first {
			c.list.first()
		}
		c.listBuilt = c.listVer
	}
	file, ok := c.selected()
	if !ok {
		return box(title("Changes", "", -1, ""), " "+sDim.Render("No changed files."), w, h, cFrame)
	}
	listLines := c.list.render(lw-2, h-2)

	want := diffBuild{file.Path, dw - 4, host.version + c.listVer}
	if c.built != want {
		if c.built.path != file.Path {
			c.diff = scrollList{}
		}
		c.diff.setItems(c.diffItems(file, discussion, dw-4, host.author))
		if c.built.path != file.Path {
			c.diff.first()
		}
		c.built = want
	}
	diffLines := c.diff.render(dw-2, h-2)

	listBorder, diffBorder := cFrame, cFrameDim
	if c.focusDiff {
		listBorder, diffBorder = cFrameDim, cFrame
	}
	filesBox := box(title("Files", "", len(c.files), ""), strings.Join(listLines, "\n"), lw, h, listBorder)
	name := file.Path
	if file.PreviousPath != "" {
		name = file.PreviousPath + " → " + file.Path
	}
	diffBox := box(sWhite.Bold(true).Render(midCut(name, dw-8)), strings.Join(diffLines, "\n"), dw, h, diffBorder)
	return hjoin(filesBox, diffBox)
}
