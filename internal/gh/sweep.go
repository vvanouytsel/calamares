package gh

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// repoFields is what the sweep reads of a repository: whether it has
// workflow files and the combined check state of the last 10 commits on its
// default branch.
const repoFields = `fragment repoFields on Repository {
      databaseId name nameWithOwner url isArchived isFork isPrivate pushedAt viewerPermission hasIssuesEnabled
      owner { login }
      defaultBranchRef { name target { ... on Commit {
        history(first: 10) { nodes { statusCheckRollup { state } } }
      } } }
      workflows: object(expression: "HEAD:.github/workflows") { ... on Tree { entries { name } } }
}`

// sweepQuery reads a page of your repositories, most recently pushed first,
// with repoFields. Resolving those check states is
// slow on GitHub's side: a page of 25 takes a few seconds, while 50 or more
// can hit GitHub's 10-second limit. A page costs 1 GraphQL point.
const sweepQuery = `query($after: String) {
  viewer { repositories(first: 25, after: $after,
      affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      orderBy: {field: PUSHED_AT, direction: DESC}) {
    pageInfo { hasNextPage endCursor }
    nodes { ...repoFields }
  } }
}
` + repoFields

type sweepPage struct {
	Viewer struct {
		Repositories struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []sweepRepo `json:"nodes"`
		} `json:"repositories"`
	} `json:"viewer"`
}

type sweepRepo struct {
	DatabaseID       int64     `json:"databaseId"`
	Name             string    `json:"name"`
	NameWithOwner    string    `json:"nameWithOwner"`
	URL              string    `json:"url"`
	IsArchived       bool      `json:"isArchived"`
	IsFork           bool      `json:"isFork"`
	IsPrivate        bool      `json:"isPrivate"`
	PushedAt         time.Time `json:"pushedAt"`
	ViewerPermission string    `json:"viewerPermission"`
	HasIssuesEnabled bool      `json:"hasIssuesEnabled"`
	Owner            struct {
		Login string `json:"login"`
	} `json:"owner"`
	DefaultBranchRef *struct {
		Name   string `json:"name"`
		Target struct {
			History struct {
				Nodes []struct {
					Rollup *struct {
						State string `json:"state"`
					} `json:"statusCheckRollup"`
				} `json:"nodes"`
			} `json:"history"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
	Workflows *struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	} `json:"workflows"`
}

// SweepPage reads one page of your repositories, most recently pushed
// first, and returns the cursor of the next page ("" after the last one).
func (c *Client) SweepPage(ctx context.Context, after string) ([]Repo, string, error) {
	vars := map[string]any{"after": nil}
	if after != "" {
		vars["after"] = after
	}
	var page sweepPage
	if err := allowPartial(c.GraphQL(ctx, sweepQuery, vars, &page)); err != nil {
		return nil, "", err
	}
	var out []Repo
	for _, n := range page.Viewer.Repositories.Nodes {
		if n.NameWithOwner != "" { // a repo GitHub withheld, with an error, in a partial answer
			out = append(out, n.repo())
		}
	}
	next := ""
	if info := page.Viewer.Repositories.PageInfo; info.HasNextPage {
		next = info.EndCursor
	}
	return out, next, nil
}

func (n sweepRepo) repo() Repo {
	r := Repo{ID: n.DatabaseID, Name: n.Name, FullName: n.NameWithOwner, Owner: User{Login: n.Owner.Login},
		Private: n.IsPrivate, Archived: n.IsArchived, Fork: n.IsFork, HTMLURL: n.URL, PushedAt: n.PushedAt, Permission: n.ViewerPermission,
		IssuesOff: !n.HasIssuesEnabled}
	if b := n.DefaultBranchRef; b != nil {
		r.DefaultBranch = b.Name
		for _, c := range b.Target.History.Nodes {
			state := ""
			if c.Rollup != nil {
				state = c.Rollup.State
			}
			r.Health = append(r.Health, state)
		}
	}
	if n.Workflows != nil {
		for _, e := range n.Workflows.Entries {
			if strings.HasSuffix(e.Name, ".yml") || strings.HasSuffix(e.Name, ".yaml") {
				r.HasActions = true
				break
			}
		}
	}
	return r
}

// pushesQuery reads a page of your repositories with only when each was last
// pushed to. Without the check states it is quick for GitHub to answer, so a
// page holds 100 repos and costs 1 GraphQL point.
const pushesQuery = `query($after: String) {
  viewer { repositories(first: 100, after: $after,
      affiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
      orderBy: {field: PUSHED_AT, direction: DESC}) {
    pageInfo { hasNextPage endCursor }
    nodes { nameWithOwner pushedAt }
  } }
}`

// Push is when a repository was last pushed to, on any branch.
type Push struct {
	Repo string
	At   time.Time
}

// SweepPushes reads one page of when your repositories were last pushed to,
// most recently pushed first, and returns the cursor of the next page (""
// after the last one).
func (c *Client) SweepPushes(ctx context.Context, after string) ([]Push, string, error) {
	vars := map[string]any{"after": nil}
	if after != "" {
		vars["after"] = after
	}
	var page struct {
		Viewer struct {
			Repositories struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					NameWithOwner string    `json:"nameWithOwner"`
					PushedAt      time.Time `json:"pushedAt"`
				} `json:"nodes"`
			} `json:"repositories"`
		} `json:"viewer"`
	}
	if err := allowPartial(c.GraphQL(ctx, pushesQuery, vars, &page)); err != nil {
		return nil, "", err
	}
	var out []Push
	for _, n := range page.Viewer.Repositories.Nodes {
		if n.NameWithOwner != "" {
			out = append(out, Push{n.NameWithOwner, n.PushedAt})
		}
	}
	next := ""
	if info := page.Viewer.Repositories.PageInfo; info.HasNextPage {
		next = info.EndCursor
	}
	return out, next, nil
}

// maxRepoBatch is how many repositories one SweepRepos request reads.
const maxRepoBatch = 25

// SweepRepos reads the same details as the sweep for the named repositories
// ("owner/name") only, so refreshing a few of them doesn't read them all.
// Repositories GitHub can't find or withholds are left out of the answer.
func (c *Client) SweepRepos(ctx context.Context, names []string) ([]Repo, error) {
	var out []Repo
	for start := 0; start < len(names); start += maxRepoBatch {
		batch := names[start:min(start+maxRepoBatch, len(names))]
		var decl, body strings.Builder
		vars := map[string]any{}
		for i, full := range batch {
			owner, name, ok := strings.Cut(full, "/")
			if !ok {
				continue
			}
			fmt.Fprintf(&decl, "$o%d: String!, $n%d: String!, ", i, i)
			fmt.Fprintf(&body, "r%d: repository(owner: $o%d, name: $n%d) { ...repoFields } ", i, i, i)
			vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
		}
		if len(vars) == 0 {
			continue
		}
		query := "query(" + strings.TrimSuffix(decl.String(), ", ") + ") { " + body.String() + "}\n" + repoFields
		var resp map[string]*sweepRepo
		if err := allowPartial(c.GraphQL(ctx, query, vars, &resp)); err != nil {
			return out, err
		}
		for i := range batch {
			if n := resp[fmt.Sprintf("r%d", i)]; n != nil && n.NameWithOwner != "" {
				out = append(out, n.repo())
			}
		}
	}
	return out, nil
}
