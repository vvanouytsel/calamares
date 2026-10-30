package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func branchesApp(t *testing.T, role string) (*App, *branchesView, *recorder) {
	withGitHub(t, map[string]string{"/graphql": `{"data":{"repository":{"defaultBranchRef":{"name":"main"},"refs":{"nodes":[
	 {"name":"main","compare":{"aheadBy":0,"behindBy":0},"associatedPullRequests":{"nodes":[]},"target":{"committedDate":"2026-10-14T00:00:00Z","messageHeadline":"chore"}},
	 {"name":"delivery","compare":{"aheadBy":4,"behindBy":2},"associatedPullRequests":{"nodes":[{"number":55,"state":"MERGED"}]},"target":{"committedDate":"2026-10-10T00:00:00Z","messageHeadline":"feat"}},
	 {"name":"old-spike","compare":{"aheadBy":40,"behindBy":1},"associatedPullRequests":{"nodes":[]},"target":{"committedDate":"2025-01-10T00:00:00Z","messageHeadline":"wip"}},
	 {"name":"release","branchProtectionRule":{"id":"R"},"compare":{"aheadBy":0,"behindBy":0},"associatedPullRequests":{"nodes":[]},"target":{"committedDate":"2026-10-01T00:00:00Z","messageHeadline":"r"}}]}}}}`,
		"/repos/o/r/git/refs/heads/delivery": "{}"})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := accessApp(t, role, "gho_x")
	a.repos[0].DefaultBranch = "main"
	v := newBranchesView(a, "o/r")
	drive(a, a.push(v), 5*time.Second)
	drive(a, v.Refresh(a, true), 5*time.Second)
	_ = a.View()
	return a, v, rec
}

func TestBranchesShowDistanceChipsAndWhatCanGo(t *testing.T) {
	a, _, _ := branchesApp(t, "WRITE")
	out := screen(a)
	for _, want := range []string{"Branches", "main", "↑2 ↓4", "merged #55", "1 merged, safe to delete", "1 stale", "delivery", "old-spike"} {
		if !strings.Contains(out, want) {
			t.Errorf("the branches lack %q:\n%s", want, out)
		}
	}
}

func TestAMergedBranchIsDeletedAfterAQuestionButTheDefaultOneIsNot(t *testing.T) {
	a, v, rec := branchesApp(t, "WRITE")
	cmd, _ := a.press(v.Keys(a), "X") // the cursor is on the default branch
	if f, ok := cmd().(flashMsg); !ok || !strings.Contains(f.text, "default branch") {
		t.Errorf("the default branch can't be deleted: %+v", f)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyDown}) // delivery
	pressKeys(a, runes("X"))
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("X should ask first, modal %T", a.modal)
	}
	pressKeys(a, runes("y"))
	time.Sleep(100 * time.Millisecond)
	for _, m := range rec.posted {
		_ = m
	}
	if rec.body("/repos/o/r/git/refs/heads/delivery") == "" && !rec.sawDelete("/repos/o/r/git/refs/heads/delivery") {
		t.Errorf("the branch was not deleted")
	}
}

func TestDeleteMergedBranchesIsInTheMenu(t *testing.T) {
	a, v, _ := branchesApp(t, "WRITE")
	found := false
	for _, b := range v.Keys(a) {
		if strings.HasPrefix(b.Help, "Delete merged branches (1)") && len(b.Keys) == 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("the menu should offer to delete the merged branches")
	}
}

func TestACompareScreenListsCommitsAndFiles(t *testing.T) {
	withGitHub(t, map[string]string{"/repos/o/r/compare/main...feat": `{"ahead_by":1,"behind_by":0,"commits":[{"sha":"abcdef123","commit":{"message":"feat: x\n\nmore","author":{"name":"Ann","date":"2026-10-10T00:00:00Z"}},"author":{"login":"ann"}}],
	  "files":[{"filename":"a.go","status":"modified","additions":2,"deletions":1,"patch":"@@ -1 +1,2 @@\n-old\n+new\n+newer"}]}`})
	a := accessApp(t, "WRITE", "gho_x")
	v := newCompareView(a, "o/r", "main", "feat")
	drive(a, a.push(v), 5*time.Second)
	_ = a.View()
	out := screen(a)
	if !strings.Contains(out, "Changes 1") || !strings.Contains(out, "Commits 1") || !strings.Contains(out, "a.go") {
		t.Errorf("compare:\n%s", out)
	}
	_ = gh.Comparison{}
}
