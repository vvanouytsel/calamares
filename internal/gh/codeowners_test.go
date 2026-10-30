package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestParseCodeOwnersSkipsCommentsAndRulesWithoutOwner(t *testing.T) {
	src := `
# Everything
*       @acme/storefront @sam   # default
/docs/  
*.go    sam@acme.example
`
	rules := ParseCodeOwners(src)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2: %+v", len(rules), rules)
	}
	if rules[0].Pattern != "*" || !slices.Equal(rules[0].Owners, []string{"@acme/storefront", "@sam"}) {
		t.Errorf("first rule = %+v", rules[0])
	}
	if rules[1].Pattern != "*.go" || !slices.Equal(rules[1].Owners, []string{"sam@acme.example"}) {
		t.Errorf("second rule = %+v", rules[1])
	}
}

func TestDefaultOwnersAreThoseOfTheLastCatchAllRule(t *testing.T) {
	c := CodeOwners{Rules: []CodeOwnerRule{
		{"*", []string{"@old"}},
		{"/api/", []string{"@billing"}},
		{"*", []string{"@acme/storefront", "@sam"}},
	}}
	if got := c.Default(); !slices.Equal(got, []string{"@acme/storefront", "@sam"}) {
		t.Errorf("Default = %v", got)
	}
}

func TestDefaultOwnersWithoutCatchAllAreThoseInMostRules(t *testing.T) {
	c := CodeOwners{Rules: []CodeOwnerRule{
		{"/api/", []string{"@billing"}},
		{"/web/", []string{"@sam", "@billing"}},
		{"/ops/", []string{"@sam"}},
		{"/db/", []string{"@billing"}},
	}}
	if got := c.Default(); !slices.Equal(got, []string{"@billing", "@sam"}) {
		t.Errorf("Default = %v, want billing first", got)
	}
}

func TestCodeOwnersPrefersGitHubFolderAndMarksReposWithoutFile(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"r0": map[string]any{"nameWithOwner": "acme/webshop",
				"f0": map[string]any{"text": "* @acme/storefront\n"},
				"f1": map[string]any{"text": "* @other\n"}},
			"r1": map[string]any{"nameWithOwner": "acme/docs",
				"f2": map[string]any{"text": "* @sam\n"}},
			"r2": map[string]any{"nameWithOwner": "acme/infra"},
		}})
	})
	got, err := c.CodeOwners(context.Background(), []string{"acme/webshop", "acme/docs", "acme/infra"})
	if err != nil {
		t.Fatal(err)
	}
	if got["acme/webshop"].File != ".github/CODEOWNERS" || got["acme/webshop"].Default()[0] != "@acme/storefront" {
		t.Errorf("webshop = %+v", got["acme/webshop"])
	}
	if got["acme/docs"].File != "docs/CODEOWNERS" {
		t.Errorf("docs = %+v", got["acme/docs"])
	}
	if infra, ok := got["acme/infra"]; !ok || infra.File != "" {
		t.Errorf("infra = %+v, %v; want present without file", infra, ok)
	}
}
