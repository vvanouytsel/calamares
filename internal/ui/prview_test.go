package ui

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const reviewedPR = `{"data":{"viewer":{"login":"me"},"repository":{"squashMergeAllowed":true,"pullRequest":{
	"number":12,"title":"feat: deploy","url":"https://github.com/o/r/pull/12","state":"OPEN","headRefName":"feat","baseRefName":"main",
	"headRefOid":"abc123","body":"## Why\n\nWe **need** this, see ` + "`deploy.sh`" + `.\n\n- first\n- second",
	"author":{"login":"you"},"repository":{"nameWithOwner":"o/r"},"mergeStateStatus":"CLEAN","mergeable":"MERGEABLE","changedFiles":2,
	"latest":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
		{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","checkSuite":{"workflowRun":{"databaseId":99,"workflow":{"name":"CI"}}}}]}}}}]}}}}}`

const prIssueComments = `[{"id":1,"user":{"login":"alice"},"body":"Looks **great**, one question about the _timeout_.","created_at":"2026-10-12T08:00:00Z","html_url":"https://github.com/o/r/pull/12#issuecomment-1"}]`

const prReviews = `[{"id":5,"user":{"login":"bob"},"state":"APPROVED","body":"","submitted_at":"2026-10-12T09:00:00Z"}]`

const prLineComments = `[
 {"id":10,"user":{"login":"alice"},"body":"Why not **retry** here?","created_at":"2026-10-12T08:30:00Z","path":"src/deploy.sh","line":2,"side":"RIGHT","diff_hunk":"@@ -1,2 +1,3 @@\n keep\n+added","pull_request_review_id":7},
 {"id":11,"user":{"login":"you"},"body":"Good point","created_at":"2026-10-12T08:40:00Z","path":"src/deploy.sh","line":2,"side":"RIGHT","in_reply_to_id":10,"pull_request_review_id":8}]`

const prFiles = `[
 {"filename":"README.md","status":"modified","additions":1,"deletions":0,"patch":"@@ -1 +1,2 @@\n # Title\n+more"},
 {"filename":"src/deploy.sh","status":"modified","additions":1,"deletions":1,"patch":"@@ -1,3 +1,3 @@\n keep\n+added\n-removed\n tail"}]`

// recorder remembers the bodies of the requests that change something.
type recorder struct {
	next   http.RoundTripper
	mu     sync.Mutex
	posted map[string]string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		var body []byte
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		r.mu.Lock()
		r.posted[req.URL.Path] = string(body)
		r.mu.Unlock()
	}
	return r.next.RoundTrip(req)
}

func (r *recorder) body(path string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.posted[path]
}

func reviewedPRApp(t *testing.T) (*App, *prView, *recorder) {
	withGitHub(t, map[string]string{
		"/graphql":                                reviewedPR,
		"/repos/o/r/issues/12/comments":           prIssueComments,
		"/repos/o/r/pulls/12/reviews":             prReviews,
		"/repos/o/r/pulls/12/comments":            prLineComments,
		"/repos/o/r/pulls/12/files":               prFiles,
		"/repos/o/r/pulls/12/comments/10/replies": "{}",
	})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := apiApp(t)
	v := newPRView(a, "o/r", 12)
	drive(a, a.push(v), 5*time.Second)
	_ = a.View()
	return a, v, rec
}

func pressKeys(a *App, keys ...tea.KeyMsg) {
	for _, k := range keys {
		_, cmd := a.Update(k)
		drive(a, cmd, 5*time.Second)
		_ = a.View()
	}
}

func screen(a *App) string { return ansi.Strip(a.View()) }

func TestConversationTabRendersDescriptionAndCommentsAsMarkdown(t *testing.T) {
	a, _, _ := reviewedPRApp(t)
	out := screen(a)
	for _, want := range []string{"Conversation 2", "Changes 2", "Checks 1", "Why", "We need this", "deploy.sh", "• first",
		"alice", "Looks great, one question about the timeout.", "bob ✓ approved", "src/deploy.sh:2", "Why not retry here?", "Good point"} {
		if !strings.Contains(out, want) {
			t.Errorf("the conversation lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "**") || strings.Contains(out, "## ") {
		t.Errorf("markdown should be rendered, not shown raw:\n%s", out)
	}
}

func TestChangesTabListsFilesAndShowsTheDiffWithItsComments(t *testing.T) {
	a, _, _ := reviewedPRApp(t)
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab})
	out := screen(a)
	for _, want := range []string{"Files  2 ", "README.md", "deploy.sh", "+1", "−1", "●1", "# Title", "+ more"} {
		if !strings.Contains(out, want) {
			t.Errorf("the changes lack %q:\n%s", want, out)
		}
	}
	pressKeys(a, runes("j"))
	out = screen(a)
	for _, want := range []string{"src/deploy.sh", "added", "removed", "Why not retry here?", "↳ ● you", "Good point"} {
		if !strings.Contains(out, want) {
			t.Errorf("the diff of deploy.sh lacks %q:\n%s", want, out)
		}
	}
}

func TestCommentingOnALinePostsItToThatLineOfTheLatestCommit(t *testing.T) {
	a, _, rec := reviewedPRApp(t)
	rec.next.(*countingGitHub).bodies["/repos/o/r/pulls/12/comments"] = "{}"
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab}, runes("j"), runes("l"), runes("j"), runes("j"), runes("j"), runes("c"))
	if _, ok := a.modal.(*editorModal); !ok {
		t.Fatalf("c should open the editor, modal is %T", a.modal)
	}
	pressKeys(a, runes("needs a test"), tea.KeyMsg{Type: tea.KeyCtrlN})
	body := rec.body("/repos/o/r/pulls/12/comments")
	for _, want := range []string{`"body":"needs a test"`, `"commit_id":"abc123"`, `"path":"src/deploy.sh"`, `"side":"LEFT"`, `"line":2`} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment request lacks %s: %s", want, body)
		}
	}
}

func TestRepliesGoToTheThreadTheyAnswer(t *testing.T) {
	a, _, rec := reviewedPRApp(t)
	pressKeys(a, runes("j"), runes("j"), runes("r"))
	if _, ok := a.modal.(*editorModal); !ok {
		t.Fatalf("r should open the editor, modal is %T", a.modal)
	}
	pressKeys(a, runes("thanks"), tea.KeyMsg{Type: tea.KeyCtrlS})
	if body := rec.body("/repos/o/r/pulls/12/comments/10/replies"); !strings.Contains(body, `"body":"thanks"`) {
		t.Errorf("the reply should go to the thread of comment 10, posted %q", body)
	}
}

func TestRepliesToAConversationCommentQuoteIt(t *testing.T) {
	a, _, _ := reviewedPRApp(t)
	pressKeys(a, runes("j"), runes("r"))
	m, ok := a.modal.(*editorModal)
	if !ok || !strings.Contains(m.input.Value(), "> @alice wrote:") {
		t.Fatalf("the reply should start with a quote of alice's comment, modal %T", a.modal)
	}
}

func TestPatchesKeepTrackOfLineNumbers(t *testing.T) {
	lines := parsePatch("@@ -10,3 +20,3 @@ func\n keep\n-gone\n+new\n tail")
	want := [][2]int{{0, 0}, {10, 20}, {11, 0}, {0, 21}, {12, 22}}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d", len(lines), len(want))
	}
	for i, l := range lines {
		if l.oldNo != want[i][0] || l.newNo != want[i][1] {
			t.Errorf("line %d is %d/%d, want %d/%d", i, l.oldNo, l.newNo, want[i][0], want[i][1])
		}
	}
}

func TestMarkdownRendersStructureAndFitsTheWidth(t *testing.T) {
	src := "# Title\n\nSome **bold**, _italic_ and `code` with a [link](https://x.y) and @someone.\n\n> quoted\n\n1. one\n2. two\n- [x] done\n- [ ] todo\n\n```go\nfmt.Println(\"hi\")\n```\n\n<!-- hidden -->\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	lines := renderMarkdown(src, 30)
	out := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Title", "Some bold, italic and  code", "link", "@someone", "▎ quoted", "1. one", "☑ done", "☐ todo", "│ fmt.Println", "a │ b", "1 │ 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered markdown lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden") || strings.Contains(out, "**") {
		t.Errorf("comments and markers should not show:\n%s", out)
	}
	for _, l := range lines {
		if width(l) > 30 {
			t.Errorf("line is %d wide, want ≤ 30: %q", width(l), ansi.Strip(l))
		}
	}
}

func TestActionsScreenHasNoLast24HoursViews(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/o/r/actions/runs": oneRun})
	a := apiApp(t)
	drive(a, a.setTab(tabActions), 5*time.Second)
	out := screen(a)
	if strings.Contains(out, "24 hours") || strings.Contains(out, "24h") {
		t.Errorf("the Actions screen should not show 24 hour views:\n%s", out)
	}
}

func TestEveryPullRequestTabFitsTheTerminal(t *testing.T) {
	a, _, _ := reviewedPRApp(t)
	for _, size := range [][2]int{{140, 40}, {80, 24}, {60, 16}} {
		a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for range 3 {
			pressKeys(a, runes("j"), tea.KeyMsg{Type: tea.KeyTab})
			for i, l := range strings.Split(a.View(), "\n") {
				if w := width(l); w > size[0] {
					t.Errorf("at %dx%d line %d is %d wide: %q", size[0], size[1], i, w, ansi.Strip(l))
				}
			}
			if n := strings.Count(a.View(), "\n") + 1; n > size[1] {
				t.Errorf("at %dx%d the screen is %d lines", size[0], size[1], n)
			}
		}
	}
}

func TestMarkdownLeavesNoEscapeCharactersOfItsOwnMakingBroken(t *testing.T) {
	src := "[![Compatibility](https://x/y.svg)](https://docs) <blockquote>quoted</blockquote> \x1b[2Jboom\n\n```\nList<String>\n```"
	lines := renderMarkdown(src, 80)
	for _, l := range lines {
		if strings.Contains(ansi.Strip(l), "\x1b") || strings.Contains(ansi.Strip(l), "[4;") {
			t.Errorf("broken escape sequence in %q", l)
		}
	}
	out := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"‹image: Compatibility›", "quoted", "boom", "List<String>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<blockquote>") {
		t.Errorf("html tags should go:\n%s", out)
	}
}

// sawDelete reports whether a DELETE for path was sent.
func (r *recorder) sawDelete(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.posted[path]
	return ok
}
