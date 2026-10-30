package gh

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAJobQueuedFromCreatedUntilItStarted(t *testing.T) {
	created := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	started := Job{Status: "completed", CreatedAt: created, StartedAt: created.Add(14 * time.Minute)}
	if got := started.Queued(created.Add(time.Hour)); got != 14*time.Minute {
		t.Errorf("a job that started after 14m queued %s", got)
	}
	waiting := Job{Status: "queued", CreatedAt: created}
	if got := waiting.Queued(created.Add(5 * time.Minute)); got != 5*time.Minute {
		t.Errorf("a job still waiting for a runner has queued %s so far, want 5m", got)
	}
	if got := (Job{Status: "completed", Conclusion: "skipped", CreatedAt: created}).Queued(created.Add(time.Hour)); got != 0 {
		t.Errorf("a job that never started did not queue: %s", got)
	}
	if got := (Job{Status: "completed", StartedAt: created}).Queued(created); got != 0 {
		t.Errorf("without a creation time the queue time is unknown: %s", got)
	}
}

func TestParseCronsReadsEveryScheduleLine(t *testing.T) {
	src := `on:
  schedule:
    - cron: '0 3 * * *'
    - cron: "*/15 * * * 1-5" # weekdays
  workflow_dispatch:
`
	got := ParseCrons(src)
	if strings.Join(got, "|") != "0 3 * * *|*/15 * * * 1-5" {
		t.Errorf("crons = %q", got)
	}
	if got := ParseCrons("on: push\n"); len(got) != 0 {
		t.Errorf("a workflow without a schedule has no crons: %q", got)
	}
}

func TestRequiredChecksComeFromRulesetsAndBranchProtection(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/webshop/rules/branches/main":
			w.Write([]byte(`[{"type":"pull_request"},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"build / test"},{"context":"lint"}]}}]`))
		case "/repos/acme/webshop/branches/main":
			w.Write([]byte(`{"name":"main","protection":{"required_status_checks":{"contexts":["lint","deploy-preview"]}}}`))
		default:
			http.NotFound(w, r)
		}
	})
	got, err := c.RequiredChecks(context.Background(), "acme/webshop", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "build / test|lint|deploy-preview" {
		t.Errorf("required = %q, want each name once, rulesets first", got)
	}
}

func TestABranchWithoutRulesIsNotAnError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	got, err := c.RequiredChecks(context.Background(), "acme/webshop", "main")
	if err != nil || len(got) != 0 {
		t.Errorf("got %q, %v; want nothing required and no error", got, err)
	}
}

func TestMissingChecksAreTheRequiredOnesNothingReported(t *testing.T) {
	got := MissingChecks([]string{"build / test", "Lint", "deploy"}, []string{"build / unit-test", "lint"})
	if strings.Join(got, "|") != "build / test|deploy" {
		t.Errorf("missing = %q", got)
	}
}

func TestADeploymentKnowsTheRunThatMadeIt(t *testing.T) {
	d := Deployment{Status: &DeploymentStatus{LogURL: "https://github.com/acme/webshop/actions/runs/4242/job/9"}}
	if got := d.RunID(); got != 4242 {
		t.Errorf("run = %d, want 4242", got)
	}
	if got := (Deployment{Status: &DeploymentStatus{TargetURL: "https://deploy.example.com/7"}}).RunID(); got != 0 {
		t.Errorf("a deployment made outside Actions has no run, got %d", got)
	}
}

func TestLastDeploymentCarriesItsLatestStatus(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/webshop/deployments":
			if r.URL.Query().Get("environment") != "production" {
				t.Errorf("asked for environment %q", r.URL.Query().Get("environment"))
			}
			w.Write([]byte(`[{"id":5,"sha":"abc1234","ref":"main","environment":"production","creator":{"login":"sam"}}]`))
		case "/repos/acme/webshop/deployments/5/statuses":
			w.Write([]byte(`[{"state":"success","log_url":"https://github.com/acme/webshop/actions/runs/88"}]`))
		default:
			http.NotFound(w, r)
		}
	})
	d, ok, err := c.LastDeployment(context.Background(), "acme/webshop", "production")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if d.Status == nil || d.Status.State != "success" || d.RunID() != 88 || d.Creator.Login != "sam" {
		t.Errorf("deployment = %+v", d)
	}
}

func TestSecretsOfAPrivateRepoHiddenBehindA404AreAnAccessError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	_, err := c.RepoSettings(context.Background(), "acme/webshop", nil)
	if !IsAccessDenied(err) {
		t.Errorf("err = %v, want an access error", err)
	}
}

func TestRepoSettingsListsOwnInheritedAndEnvironmentNames(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/webshop/actions/secrets":
			w.Write([]byte(`{"total_count":1,"secrets":[{"name":"NPM_TOKEN"}]}`))
		case "/repos/acme/webshop/actions/variables":
			w.Write([]byte(`{"total_count":1,"variables":[{"name":"REGION","value":"eu-west-1"}]}`))
		case "/repos/acme/webshop/actions/organization-secrets":
			w.Write([]byte(`{"total_count":1,"secrets":[{"name":"SONAR_TOKEN"}]}`))
		case "/repos/acme/webshop/environments/production/secrets":
			w.Write([]byte(`{"total_count":1,"secrets":[{"name":"DEPLOY_KEY"}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	s, err := c.RepoSettings(context.Background(), "acme/webshop", []string{"production"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Secrets) != 1 || s.Secrets[0].Name != "NPM_TOKEN" || len(s.Variables) != 1 || s.Variables[0].Name != "REGION" {
		t.Errorf("own: %+v %+v", s.Secrets, s.Variables)
	}
	if len(s.OrgSecrets) != 1 || len(s.OrgVariables) != 0 {
		t.Errorf("inherited: %+v %+v", s.OrgSecrets, s.OrgVariables)
	}
	if got := s.EnvSecrets["production"]; len(got) != 1 || got[0].Name != "DEPLOY_KEY" {
		t.Errorf("environment secrets: %+v", got)
	}
}

func TestRunnerImageIsReadFromTheLogHeader(t *testing.T) {
	raw := "2026-10-12T10:00:00.0000000Z Current runner version: '2.329.0'\n" +
		"2026-10-12T10:00:00.0000000Z ##[group]Runner Image\n" +
		"2026-10-12T10:00:00.0000000Z Image: ubuntu-24.04\n" +
		"2026-10-12T10:00:00.0000000Z Version: 20261012.1\n" +
		"2026-10-12T10:00:00.0000000Z ##[endgroup]\n" +
		"2026-10-12T10:00:01.0000000Z ##[group]Run make test\n" +
		"2026-10-12T10:00:01.0000000Z Version: 9.9\n"
	if got := RunnerImage(ParseLog(raw)); got != "ubuntu-24.04 20261012.1" {
		t.Errorf("image = %q", got)
	}
	if got := RunnerImage(ParseLog("2026-10-12T10:00:01.0000000Z ##[group]Run make test\n")); got != "" {
		t.Errorf("a self-hosted runner logs no image, got %q", got)
	}
}

func TestTheContainerHookMessageSaysNoMoreThanAnExitCode(t *testing.T) {
	for _, text := range []string{
		"Process completed with exit code 1.",
		"Executing the custom container implementation failed. Please contact your self hosted runner administrator.",
		"Error: Executing the custom container implementation failed. Please contact your self hosted runner administrator.",
		"Error: Error: failed to run script step: command terminated with non-zero exit code: error executing command [sh -e /__w/_temp/9f76df90.sh], exit code 1",
	} {
		if !IsGenericFailure(LogLine{Text: text}) {
			t.Errorf("%q is generic", text)
		}
	}
	if IsGenericFailure(LogLine{Text: "Error: toomanyrequests: rate limit reached"}) {
		t.Error("a real error is not generic")
	}
}

func TestRunAnnotationsReadsManyRunsInOneRequest(t *testing.T) {
	requests := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"data":{
			"r0":{"checkSuite":{"checkRuns":{"nodes":[{"name":"build","annotations":{"nodes":[{"annotationLevel":"WARNING","message":"Node.js 20 actions are deprecated.","path":".github"}]}}]}}},
			"r1":{"checkSuite":{"checkRuns":{"nodes":[{"name":"test","annotations":{"nodes":[]}}]}}},
			"r2":null}}`))
	})
	got, err := c.RunAnnotations(context.Background(), []string{"WFR_a", "WFR_b", "WFR_c"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Errorf("%d requests for 3 runs, want 1", requests)
	}
	if len(got["WFR_a"]) != 1 || got["WFR_a"][0].Job != "build" || got["WFR_a"][0].Level != "WARNING" {
		t.Errorf("run a: %+v", got["WFR_a"])
	}
	if list, ok := got["WFR_b"]; !ok || len(list) != 0 {
		t.Errorf("a run without annotations is read, with none: %+v %v", list, ok)
	}
	if _, ok := got["WFR_c"]; ok {
		t.Error("a run GitHub can't find is left out")
	}
}
