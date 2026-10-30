package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func ownersApp(t *testing.T) *App {
	a := sharedApp(t)
	a.st.owners = map[string]gh.CodeOwners{
		"acme/api":     {File: ".github/CODEOWNERS", Rules: []gh.CodeOwnerRule{{Pattern: "*", Owners: []string{"@acme/billing", "@sam"}}, {Pattern: "/docs/", Owners: []string{"@acme/writers"}}}},
		"acme/web":     {},
		"acme/billing": {File: "CODEOWNERS", Rules: []gh.CodeOwnerRule{{Pattern: "*", Owners: []string{"@sam"}}}},
	}
	return a
}

func ownerText(a *App, r gh.Repo) string { return ansi.Strip(a.ownerCell(r).Text) }

func TestOwnerCellNamesTheFirstOwnerAndCountsTheOthers(t *testing.T) {
	a := ownersApp(t)
	if got := ownerText(a, gh.Repo{FullName: "acme/api"}); !strings.HasSuffix(got, "@acme/billing +1") {
		t.Errorf("got %q", got)
	}
	if got := ownerText(a, gh.Repo{FullName: "acme/billing"}); !strings.HasSuffix(got, "@sam") || strings.Contains(got, "+") {
		t.Errorf("got %q", got)
	}
}

func TestOwnerCellSaysNoOwnersForARepoWithoutFile(t *testing.T) {
	a := ownersApp(t)
	if got := ownerText(a, gh.Repo{FullName: "acme/web", Permission: "WRITE"}); !strings.Contains(got, "no owners") {
		t.Errorf("got %q", got)
	}
}

func TestOwnerCellIsEmptyUntilTheFileIsReadAndDashForArchivedRepos(t *testing.T) {
	a := ownersApp(t)
	if got := ownerText(a, gh.Repo{FullName: "acme/unread"}); got != "" {
		t.Errorf("unread repo got %q", got)
	}
	if got := ownerText(a, gh.Repo{FullName: "acme/api", Archived: true}); got != "–" {
		t.Errorf("archived repo got %q", got)
	}
}

func TestOwnersFilterMatchesTheOwnerColumn(t *testing.T) {
	a := ownersApp(t)
	cell := a.ownerCell(gh.Repo{FullName: "acme/api"})
	tbl := newTable(Column{Title: "REPOSITORY"}, Column{Title: "OWNER"})
	tbl.SetRows([]Row{{Key: "acme/api", Cells: []Cell{{Text: "acme/api"}, cell}}, {Key: "acme/web", Cells: []Cell{{Text: "acme/web"}, {Text: "no owners"}}}})
	tbl.SetFilter("owner:billing", "")
	if tbl.Len() != 1 || tbl.Selected().Key != "acme/api" {
		t.Errorf("filter left %d rows", tbl.Len())
	}
}

func TestPanelListsTheRulesOfTheCodeownersFile(t *testing.T) {
	a := ownersApp(t)
	p := newPanel(60)
	a.ownersSection(p, gh.Repo{FullName: "acme/api"})
	got := ansi.Strip(strings.Join(p.render(20), "\n"))
	for _, want := range []string{"CODEOWNERS", "@acme/billing @sam", "@acme/writers", "/docs/", ".github/CODEOWNERS"} {
		if !strings.Contains(got, want) {
			t.Errorf("panel lacks %q:\n%s", want, got)
		}
	}
}
