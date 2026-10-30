package gh

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RepoActivity is what a watched repo is checked for besides its runs: the
// latest commits on its default branch, its recently updated pull requests
// and its latest releases.
type RepoActivity struct {
	ReadAt   time.Time
	Branch   string // the default branch
	Head     string // the SHA at its tip
	Commits  []ActivityCommit
	Pulls    []ActivityPull
	Releases []ActivityRelease
}

// ActivityCommit is a commit on the default branch, newest first.
type ActivityCommit struct {
	SHA, Title, Author, URL string
	At                      time.Time
}

// ActivityPull is a pull request, most recently updated first.
type ActivityPull struct {
	Number               int
	Title, URL, State    string // OPEN, CLOSED or MERGED
	Branch, Base, Author string
	MergedBy             string
	Draft                bool
	CreatedAt, MergedAt  time.Time
}

// ActivityRelease is a published release, newest first.
type ActivityRelease struct {
	Tag, Name, URL, Author string
	Prerelease             bool
	At                     time.Time
}

const activityQuery = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name target { ... on Commit { oid
      history(first: 10) { nodes { oid messageHeadline committedDate url author { name user { login } } } }
    } } }
    pullRequests(first: 10, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes {
      number title url state isDraft headRefName baseRefName createdAt mergedAt
      author { login } mergedBy { login }
    } }
    releases(first: 5, orderBy: {field: CREATED_AT, direction: DESC}) { nodes {
      tagName name url isDraft isPrerelease publishedAt author { login }
    } }
  }
}`

type login struct {
	Login string `json:"login"`
}

func (l *login) name() string {
	if l == nil {
		return ""
	}
	return l.Login
}

// RepoActivity reads what a watched repo is checked for besides its runs:
// one GraphQL query.
func (c *Client) RepoActivity(ctx context.Context, repo string) (RepoActivity, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return RepoActivity{}, fmt.Errorf("not a repository: %q", repo)
	}
	var data struct {
		Repository *struct {
			DefaultBranchRef *struct {
				Name   string `json:"name"`
				Target struct {
					Oid     string `json:"oid"`
					History struct {
						Nodes []struct {
							Oid             string    `json:"oid"`
							MessageHeadline string    `json:"messageHeadline"`
							CommittedDate   time.Time `json:"committedDate"`
							URL             string    `json:"url"`
							Author          struct {
								Name string `json:"name"`
								User *login `json:"user"`
							} `json:"author"`
						} `json:"nodes"`
					} `json:"history"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
			PullRequests struct {
				Nodes []struct {
					Number      int       `json:"number"`
					Title       string    `json:"title"`
					URL         string    `json:"url"`
					State       string    `json:"state"`
					IsDraft     bool      `json:"isDraft"`
					HeadRefName string    `json:"headRefName"`
					BaseRefName string    `json:"baseRefName"`
					CreatedAt   time.Time `json:"createdAt"`
					MergedAt    time.Time `json:"mergedAt"`
					Author      *login    `json:"author"`
					MergedBy    *login    `json:"mergedBy"`
				} `json:"nodes"`
			} `json:"pullRequests"`
			Releases struct {
				Nodes []struct {
					TagName      string    `json:"tagName"`
					Name         string    `json:"name"`
					URL          string    `json:"url"`
					IsDraft      bool      `json:"isDraft"`
					IsPrerelease bool      `json:"isPrerelease"`
					PublishedAt  time.Time `json:"publishedAt"`
					Author       *login    `json:"author"`
				} `json:"nodes"`
			} `json:"releases"`
		} `json:"repository"`
	}
	if err := allowPartial(c.GraphQL(ctx, activityQuery, map[string]any{"owner": owner, "name": name}, &data)); err != nil {
		return RepoActivity{}, err
	}
	r := data.Repository
	if r == nil {
		return RepoActivity{}, &APIError{Status: 404, Message: "no repository " + repo}
	}
	out := RepoActivity{ReadAt: time.Now()}
	if b := r.DefaultBranchRef; b != nil {
		out.Branch, out.Head = b.Name, b.Target.Oid
		for _, n := range b.Target.History.Nodes {
			who := n.Author.Name
			if l := n.Author.User.name(); l != "" {
				who = l
			}
			out.Commits = append(out.Commits, ActivityCommit{SHA: n.Oid, Title: n.MessageHeadline, Author: who, URL: n.URL, At: n.CommittedDate})
		}
	}
	for _, n := range r.PullRequests.Nodes {
		out.Pulls = append(out.Pulls, ActivityPull{Number: n.Number, Title: n.Title, URL: n.URL, State: n.State, Branch: n.HeadRefName, Base: n.BaseRefName,
			Author: n.Author.name(), MergedBy: n.MergedBy.name(), Draft: n.IsDraft, CreatedAt: n.CreatedAt, MergedAt: n.MergedAt})
	}
	for _, n := range r.Releases.Nodes {
		if n.IsDraft {
			continue
		}
		out.Releases = append(out.Releases, ActivityRelease{Tag: n.TagName, Name: n.Name, URL: n.URL, Author: n.Author.name(), Prerelease: n.IsPrerelease, At: n.PublishedAt})
	}
	return out, nil
}
