package gh

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CurrentUser returns the authenticated user.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	err := c.Get(ctx, "user", &u)
	return u, err
}

// GetRepo returns a single repository ("owner/name").
func (c *Client) GetRepo(ctx context.Context, repo string) (Repo, error) {
	var r Repo
	err := c.Get(ctx, "repos/"+repo, &r)
	if r.Permission == "" && r.Perms != nil {
		p := r.Perms
		r.Permission = RoleOf(p.Admin, p.Maintain, p.Push, p.Triage, p.Pull)
	}
	return r, err
}

type workflowsResp struct {
	Workflows []Workflow `json:"workflows"`
}

func workflowsPath(repo string) string { return "repos/" + repo + "/actions/workflows?per_page=100" }

// ListWorkflows returns the workflows defined in repo.
func (c *Client) ListWorkflows(ctx context.Context, repo string) ([]Workflow, error) {
	var resp workflowsResp
	err := c.Get(ctx, workflowsPath(repo), &resp)
	return resp.Workflows, err
}

type branchesResp []struct {
	Name string `json:"name"`
}

func (b branchesResp) names() []string {
	out := make([]string, len(b))
	for i, x := range b {
		out[i] = x.Name
	}
	return out
}

func branchesPath(repo string) string { return "repos/" + repo + "/branches?per_page=100" }

// ListBranches returns the names of up to 100 branches of repo.
func (c *Client) ListBranches(ctx context.Context, repo string) ([]string, error) {
	var resp branchesResp
	err := c.Get(ctx, branchesPath(repo), &resp)
	return resp.names(), err
}

// CachedBranches returns the last known branch names of repo, if any.
func (c *Client) CachedBranches(repo string) ([]string, bool) {
	var resp branchesResp
	ok := c.Cached(branchesPath(repo), &resp)
	return resp.names(), ok
}

// CachedWorkflows returns the last known workflows of repo, if any.
func (c *Client) CachedWorkflows(repo string) ([]Workflow, bool) {
	var resp workflowsResp
	ok := c.Cached(workflowsPath(repo), &resp)
	return resp.Workflows, ok
}

// RunFilter narrows ListRuns.
type RunFilter struct {
	WorkflowID int64
	Branch     string
	Actor      string
	Status     string
	Event      string
	HeadSHA    string
	Created    string // such as ">=2026-10-04"
	PerPage    int
	Page       int
}

type runsResp struct {
	TotalCount   int   `json:"total_count"`
	WorkflowRuns []Run `json:"workflow_runs"`
}

func runsPath(repo string, f RunFilter) string {
	path := "repos/" + repo + "/actions/runs"
	if f.WorkflowID != 0 {
		path = fmt.Sprintf("repos/%s/actions/workflows/%d/runs", repo, f.WorkflowID)
	}
	q := url.Values{}
	if f.PerPage == 0 {
		f.PerPage = 30
	}
	q.Set("per_page", fmt.Sprint(f.PerPage))
	if f.Page > 1 {
		q.Set("page", fmt.Sprint(f.Page))
	}
	if f.Branch != "" {
		q.Set("branch", f.Branch)
	}
	if f.Actor != "" {
		q.Set("actor", f.Actor)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.Event != "" {
		q.Set("event", f.Event)
	}
	if f.HeadSHA != "" {
		q.Set("head_sha", f.HeadSHA)
	}
	if f.Created != "" {
		q.Set("created", f.Created)
	}
	return path + "?" + q.Encode()
}

// CachedRuns returns the last known result of ListRuns for f, if any.
func (c *Client) CachedRuns(repo string, f RunFilter) ([]Run, int, bool) {
	var resp runsResp
	ok := c.Cached(runsPath(repo, f), &resp)
	c.KeepStarted(resp.WorkflowRuns)
	return resp.WorkflowRuns, resp.TotalCount, ok
}

// CachedRunsAt reports when the cached result of ListRuns for f was last confirmed.
func (c *Client) CachedRunsAt(repo string, f RunFilter) (time.Time, bool) {
	return c.cachedAt(runsPath(repo, f))
}

// ListRuns returns the most recent runs of a repo (or one workflow) and the
// total number of runs that match.
func (c *Client) ListRuns(ctx context.Context, repo string, f RunFilter) ([]Run, int, error) {
	var resp runsResp
	err := c.Get(ctx, runsPath(repo, f), &resp)
	c.KeepStarted(resp.WorkflowRuns)
	return resp.WorkflowRuns, resp.TotalCount, err
}

// GetRun returns a single run.
func (c *Client) GetRun(ctx context.Context, repo string, runID int64) (Run, error) {
	var r Run
	err := c.Get(ctx, fmt.Sprintf("repos/%s/actions/runs/%d", repo, runID), &r)
	runs := []Run{r}
	c.KeepStarted(runs)
	return runs[0], err
}

type jobsResp struct {
	Jobs []Job `json:"jobs"`
}

func jobsPath(repo string, runID int64, page int) string {
	return fmt.Sprintf("repos/%s/actions/runs/%d/jobs?filter=latest&per_page=100&page=%d", repo, runID, page)
}

// CachedJobs returns the last known jobs of a run (first 100), if any.
func (c *Client) CachedJobs(repo string, runID int64) ([]Job, bool) {
	var resp jobsResp
	ok := c.Cached(jobsPath(repo, runID, 1), &resp)
	c.keepStartedJobs(resp.Jobs)
	return resp.Jobs, ok
}

// ListJobs returns the jobs (with steps) of the latest attempt of a run.
func (c *Client) ListJobs(ctx context.Context, repo string, runID int64) ([]Job, error) {
	return c.ListAttemptJobs(ctx, repo, runID, 0)
}

func attemptJobsPath(repo string, runID int64, attempt, page int) string {
	if attempt == 0 {
		return jobsPath(repo, runID, page)
	}
	return fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100&page=%d", repo, runID, attempt, page)
}

// GetRunAttempt returns a run as it was in one of its attempts.
func (c *Client) GetRunAttempt(ctx context.Context, repo string, runID int64, attempt int) (Run, error) {
	var r Run
	err := c.Get(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d", repo, runID, attempt), &r)
	return r, err
}

// ListAttemptJobs returns the jobs of one attempt of a run; attempt 0 is the latest.
func (c *Client) ListAttemptJobs(ctx context.Context, repo string, runID int64, attempt int) ([]Job, error) {
	var all []Job
	for page := 1; page <= 5; page++ {
		var resp jobsResp
		if err := c.Get(ctx, attemptJobsPath(repo, runID, attempt, page), &resp); err != nil {
			return all, err
		}
		all = append(all, resp.Jobs...)
		if len(resp.Jobs) < 100 {
			break
		}
	}
	if attempt == 0 {
		c.keepStartedJobs(all)
	}
	return all, nil
}

// JobLog returns the raw log text of a finished job.
func (c *Client) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	b, err := c.getRaw(ctx, fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID), "application/vnd.github+json", false)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Annotations returns the check-run annotations of a job (job IDs are check-run IDs).
func (c *Client) Annotations(ctx context.Context, repo string, jobID int64) ([]Annotation, error) {
	var a []Annotation
	err := c.Get(ctx, fmt.Sprintf("repos/%s/check-runs/%d/annotations?per_page=50", repo, jobID), &a)
	return a, err
}

type debugBody struct {
	EnableDebugLogging bool `json:"enable_debug_logging"`
}

// Rerun re-runs every job of a run.
func (c *Client) Rerun(ctx context.Context, repo string, runID int64, debug bool) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/runs/%d/rerun", repo, runID), debugBody{debug}, nil)
}

// RerunFailed re-runs only the failed jobs of a run (and their dependents).
func (c *Client) RerunFailed(ctx context.Context, repo string, runID int64, debug bool) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/runs/%d/rerun-failed-jobs", repo, runID), debugBody{debug}, nil)
}

// RerunJob re-runs a single job.
func (c *Client) RerunJob(ctx context.Context, repo string, jobID int64, debug bool) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/jobs/%d/rerun", repo, jobID), debugBody{debug}, nil)
}

// Cancel cancels a run.
func (c *Client) Cancel(ctx context.Context, repo string, runID int64) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/runs/%d/cancel", repo, runID), nil, nil)
}

// Dispatch triggers a workflow_dispatch event.
func (c *Client) Dispatch(ctx context.Context, repo string, workflowID int64, ref string, inputs map[string]string) error {
	body := map[string]any{"ref": ref}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/workflows/%d/dispatches", repo, workflowID), body, nil)
}

// SetWorkflowEnabled enables or disables a workflow.
func (c *Client) SetWorkflowEnabled(ctx context.Context, repo string, workflowID int64, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	return c.Do(ctx, http.MethodPut, fmt.Sprintf("repos/%s/actions/workflows/%d/%s", repo, workflowID, action), nil, nil)
}

// FileContent returns the decoded content of a file at ref.
func (c *Client) FileContent(ctx context.Context, repo, path, ref string) (string, error) {
	p := "repos/" + repo + "/contents/" + strings.TrimPrefix(path, "/")
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var resp struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.Get(ctx, p, &resp); err != nil {
		return "", err
	}
	if resp.Encoding != "base64" {
		return resp.Content, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(resp.Content, "\n", ""))
	return cleanText(string(b)), err
}

// PendingDeployments returns the environments a run is waiting on.
func (c *Client) PendingDeployments(ctx context.Context, repo string, runID int64) ([]PendingDeployment, error) {
	var p []PendingDeployment
	err := c.Do(ctx, http.MethodGet, fmt.Sprintf("repos/%s/actions/runs/%d/pending_deployments", repo, runID), nil, &p)
	return p, err
}

// ReviewDeployments approves or rejects pending deployments of a run.
func (c *Client) ReviewDeployments(ctx context.Context, repo string, runID int64, envIDs []int64, approve bool, comment string) error {
	state := "rejected"
	if approve {
		state = "approved"
	}
	body := map[string]any{"environment_ids": envIDs, "state": state, "comment": comment}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/actions/runs/%d/pending_deployments", repo, runID), body, nil)
}

// SearchRepos searches every repository on the host the user can see,
// matching on name first. Used by the command bar for repos outside the list.
func (c *Client) SearchRepos(ctx context.Context, query string, n int) ([]Repo, error) {
	q := strings.TrimSpace(query)
	if owner, name, ok := strings.Cut(q, "/"); ok && owner != "" {
		q = name + " in:name user:" + owner
		if name == "" {
			q = "user:" + owner
		}
	} else {
		q += " in:name"
	}
	var resp struct {
		Items []Repo `json:"items"`
	}
	err := c.Get(ctx, fmt.Sprintf("search/repositories?q=%s&per_page=%d", url.QueryEscape(q), n), &resp)
	return resp.Items, err
}
