package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const issue9 = `{"id":"I9","number":9,"title":"Prices differ in checkout","url":"https://github.com/acme/webshop/issues/9","state":"OPEN","updatedAt":"2026-10-14T08:00:00Z","body":"Totals differ.",
   "author":{"login":"robin"},"repository":{"nameWithOwner":"acme/webshop","viewerPermission":"WRITE"},
   "labels":{"nodes":[{"name":"bug","color":"d73a4a"}]},"assignees":{"nodes":[{"login":"sam"}]},"comments":{"totalCount":4},
   "lastComment":{"nodes":[{"author":{"login":"robin"},"body":"@sam can you check?","createdAt":"2026-10-14T08:00:00Z"}]},
   "closedByPullRequestsReferences":{"nodes":[{"number":61,"title":"fix: round prices","state":"OPEN","isDraft":false,"repository":{"nameWithOwner":"acme/webshop"},"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}}]},
   "viewerCanUpdate":true,"viewerCanClose":true,"viewerCanReopen":false,"viewerCanLabel":true,"viewerCanAssign":true}`

const myIssuesJSON = `{"data":{"viewer":{"login":"sam"},"repository":{"issue":` + issue9 + `},
 "assigned":{"nodes":[` + issue9 + `]},
 "created":{"nodes":[{"id":"I14","number":14,"title":"Dark mode","url":"u","state":"OPEN","updatedAt":"2026-10-10T08:00:00Z","author":{"login":"sam"},
   "repository":{"nameWithOwner":"acme/webshop","viewerPermission":"WRITE"},"comments":{"totalCount":0},"lastComment":{"nodes":[]},"viewerCanClose":false}]},
 "mentioned":{"nodes":[]}}}`

func issuesApp(t *testing.T, role string) (*App, *recorder) {
	withGitHub(t, map[string]string{
		"/graphql":                              myIssuesJSON,
		"/repos/acme/webshop/issues/9/comments": `[{"id":1,"user":{"login":"robin"},"body":"@sam can you check?","created_at":"2026-10-14T08:00:00Z","html_url":"u"}]`,
		"/repos/acme/webshop/issues/9":          `{}`,
		"/repos/acme/webshop/labels":            `[{"name":"bug","color":"d73a4a"}]`,
	})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := accessApp(t, role, "gho_x")
	a.me = "sam"
	a.repos[0].FullName = "acme/webshop"
	drive(a, a.setTab(tabIssues), 5*time.Second)
	_ = a.View()
	return a, rec
}

func TestIssuesPlaceListsYourIssuesWithRolesChipsAndAPanel(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	out := screen(a)
	for _, want := range []string{"Issues", "Assigned 1", "Created 1", "Mentioned 0", "asked", "#9", "Prices differ", "1 bug", "1 awaiting your reply", "1 have a PR",
		"Next step", "robin asked you something", "Linked pull requests", "#61", "checks failing"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Issues place lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(screen(a), "6") && !strings.Contains(ansiHeader(a), "Issues") {
		t.Errorf("the header should list the sixth place")
	}
	if b := ansiHeader(a); !strings.Contains(b, "6") {
		t.Errorf("the header lacks the digit: %s", b)
	}
}

func ansiHeader(a *App) string { return screenOf(a.renderTopBar()) }

func screenOf(s string) string { return strings.Join(strings.Fields(stripANSI(s)), " ") }

func stripANSI(s string) string { return ansiStrip(s) }

func TestViewsAreSwitchedWithTab(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab})
	if out := screen(a); !strings.Contains(out, "Dark mode") || strings.Contains(out, "Prices differ in checkout") {
		t.Errorf("the Created view should list only #14:\n%s", out)
	}
}

func TestOpeningAnIssueShowsItsConversation(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := a.top().(*issueView); !ok {
		t.Fatalf("enter should open the issue, top is %T", a.top())
	}
	out := screen(a)
	for _, want := range []string{"#9", "open", "robin", "can you check?"} {
		if !strings.Contains(out, want) {
			t.Errorf("the issue screen lacks %q:\n%s", want, out)
		}
	}
}

func TestClosingAnIssueAsNotPlannedSendsTheReason(t *testing.T) {
	a, rec := issuesApp(t, "WRITE")
	pressKeys(a, runes("X"))
	if _, ok := a.modal.(*pickerModal); !ok {
		t.Fatalf("X should ask how to close, modal is %T", a.modal)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := a.modal.(*editorModal); !ok {
		t.Fatalf("an optional comment should be asked, modal is %T", a.modal)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyCtrlS})
	time.Sleep(100 * time.Millisecond)
	if body := rec.body("/repos/acme/webshop/issues/9"); !strings.Contains(body, `"state_reason":"not_planned"`) {
		t.Errorf("close request: %q", body)
	}
}

func TestCloseIsLockedWhenGitHubSaysYouCannot(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	pressKeys(a, tea.KeyMsg{Type: tea.KeyTab}) // #14: viewerCanClose false
	cmd, _ := a.press(a.top().Keys(a), "X")
	f, ok := cmd().(flashMsg)
	if !ok || f.kind != nHint || !strings.Contains(f.text, "can't close this issue") {
		t.Errorf("X on an issue you can't close gave %+v", f)
	}
}

func TestANewIssueIsCreatedFromTheForm(t *testing.T) {
	a, rec := issuesApp(t, "WRITE")
	rec.next.(*countingGitHub).bodies["/repos/acme/webshop/issues"] = `{"number":23}`
	pressKeys(a, runes("n"))
	f, ok := a.modal.(*formModal)
	if !ok {
		t.Fatalf("n should open the form, modal is %T", a.modal)
	}
	pressKeys(a, runes("Broken"), tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(f.err, "required") && a.modal != nil {
		// a title is typed: the form was sent
	}
	time.Sleep(150 * time.Millisecond)
	if body := rec.body("/repos/acme/webshop/issues"); !strings.Contains(body, `"title":"Broken"`) {
		t.Errorf("create request: %q", body)
	}
}

func TestFormRefusesToSendWithoutATitle(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	pressKeys(a, runes("n"), tea.KeyMsg{Type: tea.KeyCtrlS})
	f, ok := a.modal.(*formModal)
	if !ok || !strings.Contains(f.err, "Title is required") {
		t.Errorf("the form should say a title is needed, modal %T", a.modal)
	}
}

func TestIssuesCanBeFoundInTheGoToBoxByNumber(t *testing.T) {
	a, _ := issuesApp(t, "WRITE")
	press(a, ":")
	a.input.SetValue("#9")
	a.pal.update(a, "#9")
	found := false
	for _, it := range a.pal.items {
		if it.kind == sugIssue && it.issue.Number == 9 {
			found = true
		}
	}
	if !found {
		t.Errorf("#9 should find the issue")
	}
}
