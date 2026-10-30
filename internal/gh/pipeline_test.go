package gh

import (
	"context"
	"encoding/base64"
	"net/http"
	"reflect"
	"testing"
)

func TestParsePipelineReadsJobsWithTheirDependencies(t *testing.T) {
	jobs, err := ParsePipeline(`
name: CI
jobs:
  build:
    runs-on: x
  test:
    name: Unit tests
    needs: build
  deploy:
    needs: [build, test]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlannedJob{
		{ID: "build", Name: "build", Line: 4, EndLine: 5},
		{ID: "test", Name: "Unit tests", Needs: []string{"build"}, Line: 6, EndLine: 8},
		{ID: "deploy", Name: "deploy", Needs: []string{"build", "test"}, Line: 9, EndLine: 10},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Errorf("got %+v, want %+v", jobs, want)
	}
}

func TestResolvePipelineReplacesACallingJobByTheJobsOfTheCalledWorkflow(t *testing.T) {
	files := map[string]string{
		"/repos/o/r/contents/.github/workflows/release.yml":  "jobs:\n  prepare:\n    runs-on: x\n  release:\n    name: Release\n    needs: prepare\n    uses: o/shared/.github/workflows/rel.yml@v1\n  notify:\n    needs: release\n",
		"/repos/o/shared/contents/.github/workflows/rel.yml": "jobs:\n  version:\n    name: Generate version\n  image:\n    name: Build image\n    needs: version\n",
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte(body)) + `"}`))
	})
	jobs, err := c.ResolvePipeline(context.Background(), "o/r", "abc", ".github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	type shape struct {
		ID, Name   string
		Needs      []string
		Repo, Ref  string
		Line, Last int
	}
	var got []shape
	for _, j := range jobs {
		got = append(got, shape{j.ID, j.Name, j.Needs, j.File.Repo, j.File.Ref, j.Line, j.EndLine})
	}
	want := []shape{
		{"prepare", "prepare", nil, "o/r", "abc", 2, 3},
		{"release/version", "Release / Generate version", []string{"prepare"}, "o/shared", "v1", 2, 3},
		{"release/image", "Release / Build image", []string{"release/version"}, "o/shared", "v1", 4, 6},
		{"notify", "notify", []string{"release/image"}, "o/r", "abc", 8, 9},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestALocalCallInsideACalledWorkflowIsReadFromThatWorkflowsRepo(t *testing.T) {
	files := map[string]string{
		"/repos/o/r/contents/ci.yml":                       "jobs:\n  go:\n    uses: o/shared/.github/workflows/a.yml@v2\n",
		"/repos/o/shared/contents/.github/workflows/a.yml": "jobs:\n  inner:\n    uses: ./.github/workflows/b.yml\n",
		"/repos/o/shared/contents/.github/workflows/b.yml": "jobs:\n  leaf:\n    runs-on: x\n",
	}
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok || (r.URL.Path != "/repos/o/r/contents/ci.yml" && r.URL.Query().Get("ref") != "v2") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte(body)) + `"}`))
	})
	jobs, err := c.ResolvePipeline(context.Background(), "o/r", "abc", "ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "go/inner/leaf" || jobs[0].File.Repo != "o/shared" || jobs[0].File.Path != ".github/workflows/b.yml" {
		t.Errorf("got %+v, want go/inner/leaf from o/shared's b.yml", jobs)
	}
}
