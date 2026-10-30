package gh

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Environment is a deployment environment of a repo with its protection rules.
type Environment struct {
	Name            string           `json:"name"`
	HTMLURL         string           `json:"html_url"`
	ProtectionRules []ProtectionRule `json:"protection_rules"`
	BranchPolicy    *struct {
		ProtectedBranches    bool `json:"protected_branches"`
		CustomBranchPolicies bool `json:"custom_branch_policies"`
	} `json:"deployment_branch_policy"`
}

// ProtectionRule is one rule of an environment: required reviewers, a wait
// timer or a branch policy.
type ProtectionRule struct {
	Type              string `json:"type"` // required_reviewers, wait_timer, branch_policy
	WaitTimer         int    `json:"wait_timer"`
	PreventSelfReview bool   `json:"prevent_self_review"`
	Reviewers         []struct {
		Type     string `json:"type"` // User or Team
		Reviewer struct {
			Login string `json:"login"`
			Name  string `json:"name"`
			Slug  string `json:"slug"`
		} `json:"reviewer"`
	} `json:"reviewers"`
}

// ReviewerNames names who may approve a deployment under the rule: logins of
// people, and teams as their slug.
func (r ProtectionRule) ReviewerNames() []string {
	var out []string
	for _, rv := range r.Reviewers {
		switch {
		case rv.Reviewer.Login != "":
			out = append(out, rv.Reviewer.Login)
		case rv.Reviewer.Slug != "":
			out = append(out, "@"+rv.Reviewer.Slug)
		default:
			out = append(out, rv.Reviewer.Name)
		}
	}
	return out
}

type environmentsResp struct {
	TotalCount   int           `json:"total_count"`
	Environments []Environment `json:"environments"`
}

// Environments lists the environments of repo.
func (c *Client) Environments(ctx context.Context, repo string) ([]Environment, error) {
	var resp environmentsResp
	err := c.Get(ctx, fmt.Sprintf("repos/%s/environments?per_page=100", repo), &resp)
	if IsNotFound(err) {
		return nil, nil
	}
	return resp.Environments, err
}

// Deployment is one deployment to an environment, with its latest status.
type Deployment struct {
	ID          int64     `json:"id"`
	SHA         string    `json:"sha"`
	Ref         string    `json:"ref"`
	Environment string    `json:"environment"`
	Creator     User      `json:"creator"`
	CreatedAt   time.Time `json:"created_at"`
	// Status is the latest status of the deployment, when it has one.
	Status *DeploymentStatus `json:"status,omitempty"`
}

// DeploymentStatus is a state a deployment reached, and where it was logged.
type DeploymentStatus struct {
	State     string    `json:"state"` // success, failure, error, in_progress, queued, pending, inactive
	LogURL    string    `json:"log_url"`
	TargetURL string    `json:"target_url"`
	CreatedAt time.Time `json:"created_at"`
}

var runInURL = regexp.MustCompile(`/actions/runs/(\d+)`)

// RunID is the workflow run that made the deployment, read from where its
// status was logged, or 0 when it wasn't a run of GitHub Actions.
func (d Deployment) RunID() int64 {
	if d.Status == nil {
		return 0
	}
	for _, u := range []string{d.Status.LogURL, d.Status.TargetURL} {
		if m := runInURL.FindStringSubmatch(u); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			return id
		}
	}
	return 0
}

// LastDeployment is the newest deployment to environment, with its latest
// status; ok is false when nothing was ever deployed there.
func (c *Client) LastDeployment(ctx context.Context, repo, environment string) (d Deployment, ok bool, err error) {
	var list []Deployment
	path := fmt.Sprintf("repos/%s/deployments?environment=%s&per_page=1", repo, url.QueryEscape(environment))
	if err := c.Get(ctx, path, &list); err != nil || len(list) == 0 {
		return Deployment{}, false, err
	}
	d = list[0]
	var statuses []DeploymentStatus
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/deployments/%d/statuses?per_page=1", repo, d.ID), &statuses); err != nil {
		return d, true, err
	}
	if len(statuses) > 0 {
		d.Status = &statuses[0]
	}
	return d, true, nil
}
