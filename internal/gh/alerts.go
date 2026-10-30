package gh

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var errNoAlerts = errors.New("you can not see the security alerts of this repo")

// allowPartialAccess treats a partial answer as an answer; errors that say you may not see it stay errors.
func allowPartialAccess(err error) error { return allowPartial(err) }

// Alerts counts the open Dependabot alerts of a repo.
type Alerts struct {
	Total  int
	Severe int // critical or high
}

const alertsQuery = `query Alerts($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    vulnerabilityAlerts(states: OPEN, first: 100) { totalCount nodes { securityVulnerability { severity } } }
  }
}`

// SecurityAlerts counts the open Dependabot alerts of repo: one GraphQL
// read. A repo you may not see alerts of answers with an access error.
func (c *Client) SecurityAlerts(ctx context.Context, repo string) (Alerts, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			Alerts *struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					Vulnerability struct {
						Severity string `json:"severity"`
					} `json:"securityVulnerability"`
				} `json:"nodes"`
			} `json:"vulnerabilityAlerts"`
		} `json:"repository"`
	}
	err := c.GraphQL(WithCapability(ctx, repo, CapSecurity), alertsQuery, map[string]any{"owner": owner, "name": name}, &data)
	if err := allowPartialAccess(err); err != nil {
		return Alerts{}, err
	}
	a := data.Repository.Alerts
	if a == nil {
		// GraphQL answers null when you may not see the alerts.
		return Alerts{}, &AccessError{Repo: repo, Err: errNoAlerts}
	}
	out := Alerts{Total: a.TotalCount}
	for _, n := range a.Nodes {
		if s := n.Vulnerability.Severity; s == "CRITICAL" || s == "HIGH" {
			out.Severe++
		}
	}
	return out, nil
}

// alertsBatch is how many repos one request counts the alerts of.
const alertsBatch = 25

// SecurityAlertsOf counts the open Dependabot alerts of many repos, 25 to a
// GraphQL request. Repos whose alerts you may not see are in denied.
func (c *Client) SecurityAlertsOf(ctx context.Context, repos []string) (counts map[string]Alerts, denied map[string]bool, err error) {
	counts, denied = map[string]Alerts{}, map[string]bool{}
	for start := 0; start < len(repos); start += alertsBatch {
		batch := repos[start:min(start+alertsBatch, len(repos))]
		var params, fields []string
		vars := map[string]any{}
		for i, repo := range batch {
			owner, name, _ := strings.Cut(repo, "/")
			params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
			fields = append(fields, fmt.Sprintf("r%d: repository(owner: $o%d, name: $n%d) { vulnerabilityAlerts(states: OPEN, first: 50) { totalCount nodes { securityVulnerability { severity } } } }", i, i, i))
			vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
		}
		query := "query Alerts(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}"
		var data map[string]*struct {
			Alerts *struct {
				TotalCount int `json:"totalCount"`
				Nodes      []struct {
					Vulnerability struct {
						Severity string `json:"severity"`
					} `json:"securityVulnerability"`
				} `json:"nodes"`
			} `json:"vulnerabilityAlerts"`
		}
		if err := allowPartialAccess(c.GraphQL(ctx, query, vars, &data)); err != nil {
			return nil, nil, err
		}
		for i, repo := range batch {
			r := data[fmt.Sprintf("r%d", i)]
			if r == nil || r.Alerts == nil {
				denied[repo] = true
				continue
			}
			al := Alerts{Total: r.Alerts.TotalCount}
			for _, n := range r.Alerts.Nodes {
				if s := n.Vulnerability.Severity; s == "CRITICAL" || s == "HIGH" {
					al.Severe++
				}
			}
			counts[repo] = al
		}
	}
	return counts, denied, nil
}
