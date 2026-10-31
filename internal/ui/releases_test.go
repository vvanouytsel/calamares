package ui

import (
	"github.com/vvanouytsel/calamares/internal/gh"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const releasesJSON = `[
 {"id":3,"tag_name":"v2.16.0-rc1","name":"2.16.0 RC","draft":false,"prerelease":true,"published_at":"2026-10-13T00:00:00Z","author":{"login":"robin"},"assets":[]},
 {"id":2,"tag_name":"v2.15.0","name":"2.15.0","body":"## What's new\n\n- delivery estimates\n- faster basket","draft":false,"prerelease":false,"published_at":"2026-10-01T00:00:00Z","author":{"login":"robin"},
  "assets":[{"id":11,"name":"webshop_linux_amd64.tar.gz","size":4400000,"download_count":120},{"id":12,"name":"checksums.txt","size":900,"download_count":30}]},
 {"id":1,"tag_name":"v2.14.0","name":"2.14.0","draft":true,"created_at":"2026-09-20T00:00:00Z","author":{"login":"alex"},"assets":[]}]`

func releasesApp(t *testing.T) (*App, *releasesView, *recorder) {
	withGitHub(t, map[string]string{
		"/repos/o/r/releases":                releasesJSON,
		"/repos/o/r/compare/v2.15.0...main":  `{"ahead_by":14}`,
		"/repos/o/r/releases/generate-notes": `{"name":"v2.16.0","body":"## Changes\n- one"}`,
		"/repos/o/r/releases/1":              `{}`,
		"/repos/o/r/actions/workflows":       `{"workflows":[{"id":1,"name":"Release","path":".github/workflows/release.yml","state":"active"}]}`,
	})
	rec := &recorder{next: http.DefaultTransport, posted: map[string]string{}}
	http.DefaultTransport = rec
	a := accessApp(t, "WRITE", "gho_x")
	a.repos[0].DefaultBranch = "main"
	v := newReleasesView(a, "o/r")
	drive(a, a.push(v), 5*time.Second)
	drive(a, v.Refresh(a, true), 5*time.Second)
	_ = a.View()
	return a, v, rec
}

func TestReleasesShowStateNotesAndAssets(t *testing.T) {
	a, _, _ := releasesApp(t)
	out := screen(a)
	for _, want := range []string{"Releases", "pre-release", "draft", "14 commits since v2.15.0", "1 draft"} {
		if !strings.Contains(out, want) {
			t.Errorf("the releases lack %q:\n%s", want, out)
		}
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyDown})
	out = screen(a)
	for _, want := range []string{"latest", "Release notes", "delivery estimates", "Assets (2)", "webshop_linux", "4.2 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("the panel of v2.15.0 lacks %q:\n%s", want, out)
		}
	}
}

func TestADraftIsDeletedAfterAQuestion(t *testing.T) {
	a, _, rec := releasesApp(t)
	pressKeys(a, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyDown}, runes("X"), runes("y"))
	if !rec.sawDelete("/repos/o/r/releases/1") {
		t.Errorf("the draft was not deleted")
	}
}

func TestANewReleaseProposesTheNextVersionAndCanGenerateNotes(t *testing.T) {
	a, _, rec := releasesApp(t)
	rec.next.(*countingGitHub).bodies["/repos/o/r/releases"] = `{"id":9}`
	a.workflows["o/r"] = []gh.Workflow{{Name: "Release", Path: ".github/workflows/release.yml", State: "active"}}
	pressKeys(a, runes("+"))
	f, ok := a.modal.(*formModal)
	if !ok {
		t.Fatalf("+ should open the form, modal is %T", a.modal)
	}
	if f.field("Tag").value() != "v2.16.0" {
		t.Errorf("the likely next version is the minor one, got %q", f.field("Tag").value())
	}
	if !strings.Contains(screen(a), "publishes releases from CI: Release") {
		t.Errorf("the form should say the repo has a release workflow:\n%s", screen(a))
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyCtrlG})
	if !strings.Contains(f.field("Notes").value(), "## Changes") {
		t.Errorf("ctrl+g should fill in the notes GitHub wrote, got %q", f.field("Notes").value())
	}
}
