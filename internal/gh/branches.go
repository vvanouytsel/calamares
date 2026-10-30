package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Branch is a branch of a repo with how it stands against the default one.
type Branch struct {
	Name      string
	Default   bool
	Protected bool
	Ahead     int // commits it has that the default branch lacks
	Behind    int // commits the default branch has that it lacks
	PR        *BranchPR
	Checks    string // SUCCESS, FAILURE, PENDING… of its last commit, or ""
	Author    string
	Subject   string
	At        time.Time
}

// BranchPR is the pull request a branch has, if any.
type BranchPR struct {
	Number int
	State  string // OPEN, CLOSED or MERGED
	Draft  bool
}

const branchesQuery = `query Branches($owner: String!, $name: String!, $default: String!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
    refs(refPrefix: "refs/heads/", first: 50, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) { nodes {
      name
      branchProtectionRule { id }
      compare(headRef: $default) { aheadBy behindBy }
      associatedPullRequests(first: 1, states: [OPEN, MERGED], orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { number state isDraft } }
      target { ... on Commit { committedDate messageHeadline author { user { login } name } statusCheckRollup { state } } }
    } }
  }
}`

// Branches lists up to 50 branches of repo, newest commit first, with their
// distance from the default branch, their pull request and their checks.
func (c *Client) Branches(ctx context.Context, repo, defaultBranch string) ([]Branch, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			DefaultBranchRef *struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
			Refs struct {
				Nodes []struct {
					Name string `json:"name"`
					Rule *struct {
						ID string `json:"id"`
					} `json:"branchProtectionRule"`
					Compare *struct {
						AheadBy  int `json:"aheadBy"`
						BehindBy int `json:"behindBy"`
					} `json:"compare"`
					PRs struct {
						Nodes []struct {
							Number  int    `json:"number"`
							State   string `json:"state"`
							IsDraft bool   `json:"isDraft"`
						} `json:"nodes"`
					} `json:"associatedPullRequests"`
					Target struct {
						CommittedDate time.Time `json:"committedDate"`
						Headline      string    `json:"messageHeadline"`
						Author        *struct {
							User *struct {
								Login string `json:"login"`
							} `json:"user"`
							Name string `json:"name"`
						} `json:"author"`
						Rollup *struct {
							State string `json:"state"`
						} `json:"statusCheckRollup"`
					} `json:"target"`
				} `json:"nodes"`
			} `json:"refs"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "default": defaultBranch}
	if err := allowPartial(c.GraphQL(ctx, branchesQuery, vars, &data)); err != nil {
		return nil, err
	}
	if b := data.Repository.DefaultBranchRef; b != nil {
		defaultBranch = b.Name
	}
	var out []Branch
	for _, n := range data.Repository.Refs.Nodes {
		b := Branch{Name: n.Name, Default: n.Name == defaultBranch, Protected: n.Rule != nil, Subject: n.Target.Headline, At: n.Target.CommittedDate}
		if n.Compare != nil {
			// compare runs from this branch to the default one: what the
			// default has that this lacks is "ahead" there, and the other way round.
			b.Ahead, b.Behind = n.Compare.BehindBy, n.Compare.AheadBy
		}
		if len(n.PRs.Nodes) > 0 {
			p := n.PRs.Nodes[0]
			b.PR = &BranchPR{Number: p.Number, State: p.State, Draft: p.IsDraft}
		}
		if a := n.Target.Author; a != nil {
			b.Author = a.Name
			if a.User != nil {
				b.Author = a.User.Login
			}
		}
		if n.Target.Rollup != nil {
			b.Checks = n.Target.Rollup.State
		}
		out = append(out, b)
	}
	return out, nil
}

// DeleteBranch deletes a branch.
func (c *Client) DeleteBranch(ctx context.Context, repo, branch string) error {
	return c.Do(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/git/refs/heads/%s", repo, escapeRef(branch)), nil, nil)
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Comparison is what a branch has against another.
type Comparison struct {
	Ahead, Behind int
	Commits       []PRCommit
	Files         []FileDiff
}

// Compare compares head with base: the commits head has and the files it changes.
func (c *Client) Compare(ctx context.Context, repo, base, head string) (Comparison, error) {
	var resp struct {
		AheadBy  int `json:"ahead_by"`
		BehindBy int `json:"behind_by"`
		Commits  []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string    `json:"name"`
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"commits"`
		Files []fileJSON `json:"files"`
	}
	err := c.Get(ctx, fmt.Sprintf("repos/%s/compare/%s...%s", repo, url.PathEscape(base), url.PathEscape(head)), &resp)
	out := Comparison{Ahead: resp.AheadBy, Behind: resp.BehindBy}
	for _, k := range resp.Commits {
		subject, _, _ := strings.Cut(k.Commit.Message, "\n")
		who := k.Commit.Author.Name
		if k.Author != nil {
			who = k.Author.Login
		}
		out.Commits = append(out.Commits, PRCommit{SHA: k.SHA, Message: subject, Author: who, At: k.Commit.Author.Date})
	}
	for _, f := range resp.Files {
		out.Files = append(out.Files, FileDiff{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
	}
	return out, err
}
