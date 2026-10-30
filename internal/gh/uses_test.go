package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestParseUsesFindsWorkflowsAndActionsButNotLocalOnes(t *testing.T) {
	src := `
jobs:
  build:
    uses: acme/java-workflows/.github/workflows/service.yml@v9
  lint:
    steps:
      - uses: other-org/checkout@v4
      - name: Lint
        uses: "acme/commit-lint-action@v2" # pinned
      - uses: ./.github/actions/local
      - uses: docker://alpine:3
      - uses: acme/dotnet-actions/setup@v1
`
	var got []string
	for _, u := range ParseUses("acme/webshop", ".github/workflows/build.yml", src) {
		got = append(got, fmt.Sprintf("%s@%s workflow=%v repo=%s file=%s", u.Target, u.Version, u.Workflow(), u.TargetRepo(), u.TargetFile()))
	}
	want := []string{
		"acme/java-workflows/.github/workflows/service.yml@v9 workflow=true repo=acme/java-workflows file=.github/workflows/service.yml",
		"other-org/checkout@v4 workflow=false repo=other-org/checkout file=",
		"acme/commit-lint-action@v2 workflow=false repo=acme/commit-lint-action file=",
		"acme/dotnet-actions/setup@v1 workflow=false repo=acme/dotnet-actions file=setup",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestUsesReadsReposInBatchesOfTwentyFive(t *testing.T) {
	var batches []int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		batches = append(batches, len(body.Variables)/2)
		data := map[string]any{}
		for i := 0; i < len(body.Variables)/2; i++ {
			repo := body.Variables[fmt.Sprintf("o%d", i)] + "/" + body.Variables[fmt.Sprintf("n%d", i)]
			data[fmt.Sprintf("r%d", i)] = map[string]any{
				"nameWithOwner": repo,
				"workflows": map[string]any{"entries": []any{
					map[string]any{"path": ".github/workflows/ci.yml", "object": map[string]any{"text": "steps:\n  - uses: acme/act@v1\n"}},
				}},
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	var repos []string
	for i := range 120 {
		repos = append(repos, fmt.Sprintf("acme/r%d", i))
	}
	uses, err := c.Uses(context.Background(), repos)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(batches) != "[25 25 25 25 20]" {
		t.Errorf("batches = %v, want [25 25 25 25 20]", batches)
	}
	if len(uses) != 120 || uses[119].Repo != "acme/r119" || uses[119].File != ".github/workflows/ci.yml" || uses[119].Target != "acme/act" {
		t.Errorf("got %d uses, last %+v", len(uses), uses[len(uses)-1])
	}
}

func TestUsesSplitsABatchGitHubGivesUpOn(t *testing.T) {
	var batches []int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		n := len(body.Variables) / 2
		batches = append(batches, n)
		if n > 7 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		data := map[string]any{}
		for i := range n {
			data[fmt.Sprintf("r%d", i)] = map[string]any{"nameWithOwner": body.Variables[fmt.Sprintf("n%d", i)],
				"action": map[string]any{"text": "runs:\n  steps:\n    - uses: acme/act@v1\n"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	var repos []string
	for i := range 25 {
		repos = append(repos, fmt.Sprintf("acme/r%d", i))
	}
	uses, err := c.Uses(context.Background(), repos)
	if err != nil {
		t.Fatal(err)
	}
	if len(uses) != 25 {
		t.Errorf("got %d uses after splitting %v, want one per repo", len(uses), batches)
	}
}

func TestParseUsesNamesTheJobThatCallsAWorkflow(t *testing.T) {
	src := `
jobs:
  build:
    uses: acme/workflows/.github/workflows/build.yml@v3
  deploy:
    name: Deploy it
    uses: acme/workflows/.github/workflows/deploy.yml@v1
  matrix:
    name: ${{ matrix.os }}
    uses: acme/workflows/.github/workflows/test.yml@v2
  lint:
    steps:
      - uses: acme/lint-action@v1
`
	got := map[string]string{}
	for _, u := range ParseUses("acme/webshop", ".github/workflows/ci.yml", src) {
		got[u.TargetFile()+"@"+u.Version] = u.Job
	}
	want := map[string]string{
		".github/workflows/build.yml@v3": "build", ".github/workflows/deploy.yml@v1": "Deploy it",
		".github/workflows/test.yml@v2": "matrix", "@v1": "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: job %q, want %q", k, got[k], v)
		}
	}
}

func TestRunsPathFiltersOnTheDayRunsWereCreated(t *testing.T) {
	p := runsPath("acme/webshop", RunFilter{Status: "completed", Created: ">=2026-10-04", PerPage: 100})
	if !strings.Contains(p, "created=%3E%3D2026-10-04") || !strings.Contains(p, "status=completed") {
		t.Errorf("path %s lacks the filters", p)
	}
}
