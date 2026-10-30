package gh

import "testing"

func TestParseDispatchInputs(t *testing.T) {
	src := `
name: Deploy
on:
  push:
    branches: [main]
  workflow_dispatch:
    inputs:
      environment:
        description: Where to deploy
        type: choice
        required: true
        default: staging
        options: [dev, staging, production]
      version:
        description: Version tag
        required: true
      dry_run:
        type: boolean
        default: true
jobs: {}
`
	ok, inputs, err := ParseDispatchInputs(src)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(inputs) != 3 {
		t.Fatalf("got %d inputs, want 3", len(inputs))
	}
	env := inputs[0]
	if env.Name != "environment" || env.Type != "choice" || !env.Required || env.Default != "staging" || len(env.Options) != 3 {
		t.Errorf("environment parsed wrong: %+v", env)
	}
	if inputs[1].Type != "string" || !inputs[1].Required {
		t.Errorf("version parsed wrong: %+v", inputs[1])
	}
	if inputs[2].Type != "boolean" || inputs[2].Default != "true" {
		t.Errorf("dry_run parsed wrong: %+v", inputs[2])
	}
}

func TestParseDispatchForms(t *testing.T) {
	cases := map[string]bool{
		"on: workflow_dispatch\n":                    true,
		"on: [push, workflow_dispatch]\n":            true,
		"on:\n  workflow_dispatch:\n":                true,
		"on:\n  push:\n    branches: [main]\n":       false,
		"on: push\n":                                 false,
		"name: x\njobs: {}\n":                        false,
		"on:\n  workflow_dispatch: {}\n  push: {}\n": true,
	}
	for src, want := range cases {
		ok, inputs, err := ParseDispatchInputs(src)
		if err != nil {
			t.Errorf("%q: %v", src, err)
		}
		if ok != want || len(inputs) != 0 {
			t.Errorf("%q: ok=%v inputs=%d, want ok=%v", src, ok, len(inputs), want)
		}
	}
}

func TestReferencedWorkflowSplitsIntoRepoFileAndVersion(t *testing.T) {
	for _, c := range []struct{ path, repo, file, version string }{
		{"acme/workflows/.github/workflows/build.yml@v3", "acme/workflows", ".github/workflows/build.yml", "v3"},
		{"acme/workflows/.github/workflows/build.yml@refs/heads/main", "acme/workflows", ".github/workflows/build.yml", "refs/heads/main"},
		{"acme/api/.github/workflows/lint.yml@5b2a5ad0c1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6", "acme/api", ".github/workflows/lint.yml", "5b2a5ad0c1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6"},
		{"acme/workflows/.github/workflows/build.yml", "acme/workflows", ".github/workflows/build.yml", ""},
	} {
		repo, file, version := ReferencedWorkflow{Path: c.path}.Split()
		if repo != c.repo || file != c.file || version != c.version {
			t.Errorf("%s: got %q %q %q, want %q %q %q", c.path, repo, file, version, c.repo, c.file, c.version)
		}
	}
}
