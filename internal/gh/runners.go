package gh

import (
	"context"
	"fmt"
)

// Runner is a self-hosted runner of an organization or a repo.
type Runner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Status string `json:"status"` // online or offline
	Busy   bool   `json:"busy"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// LabelNames are the runner's labels.
func (r Runner) LabelNames() []string {
	out := make([]string, len(r.Labels))
	for i, l := range r.Labels {
		out[i] = l.Name
	}
	return out
}

type runnersResp struct {
	TotalCount int      `json:"total_count"`
	Runners    []Runner `json:"runners"`
}

// OrgRunners lists the self-hosted runners of an organization; GitHub only
// answers organization owners, or tokens allowed to manage its runners.
func (c *Client) OrgRunners(ctx context.Context, org string) ([]Runner, error) {
	var resp runnersResp
	err := c.Get(WithCapability(ctx, org, CapAdmin), fmt.Sprintf("orgs/%s/actions/runners?per_page=100", org), &resp)
	return resp.Runners, err
}

// RepoRunners lists the self-hosted runners of a repo; GitHub only answers
// its admins.
func (c *Client) RepoRunners(ctx context.Context, repo string) ([]Runner, error) {
	var resp runnersResp
	err := c.Get(WithCapability(ctx, repo, CapAdmin), fmt.Sprintf("repos/%s/actions/runners?per_page=100", repo), &resp)
	return resp.Runners, err
}
