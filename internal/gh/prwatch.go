package gh

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// PullRequestPulse is what a watched pull request is checked for: its
// state, checks, reviews, comments and the commits on it. It is a fraction
// of PullRequestDetails, so it can be read every half a minute.
type PullRequestPulse struct {
	PullRequest
	State     string // OPEN, CLOSED or MERGED
	Mergeable string // MERGEABLE, CONFLICTING or UNKNOWN
	HeadSHA   string
	MergedBy  string
	ClosedAt  time.Time
	// Commits counts the commits on it; PushedBy is who made the last one.
	Commits  int
	PushedBy string
	// Comments counts the comments in its conversation; LastCommentBy wrote the newest.
	Comments      int
	LastCommentBy string
	Reviews       []Review // everyone's latest review
}

// Ended says the pull request was merged or closed.
func (p PullRequestPulse) Ended() bool { return p.State == "MERGED" || p.State == "CLOSED" }

const prPulseQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      ...pr
      state mergeable headRefOid closedAt
      mergedBy { login }
      comments(last: 1) { totalCount nodes { author { login } } }
      latestReviews(first: 20) { nodes { author { login } state submittedAt } }
      pushed: commits(last: 1) { totalCount nodes { commit { author { user { login } name } } } }
    }
  }
}
` + prFields

type prPulseNode struct {
	prNode
	State      string    `json:"state"`
	Mergeable  string    `json:"mergeable"`
	HeadRefOid string    `json:"headRefOid"`
	ClosedAt   time.Time `json:"closedAt"`
	MergedBy   *struct {
		Login string `json:"login"`
	} `json:"mergedBy"`
	Comments struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	LatestReviews struct {
		Nodes []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	Pushed struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Commit struct {
				Author struct {
					User *struct {
						Login string `json:"login"`
					} `json:"user"`
					Name string `json:"name"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"pushed"`
}

// PullRequestPulse reads what a watched pull request is checked for.
func (c *Client) PullRequestPulse(ctx context.Context, repo string, number int) (PullRequestPulse, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return PullRequestPulse{}, fmt.Errorf("not a repository: %q", repo)
	}
	var data struct {
		Repository *struct {
			PullRequest *prPulseNode `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := allowPartial(c.GraphQL(ctx, prPulseQuery, vars, &data)); err != nil {
		return PullRequestPulse{}, err
	}
	if data.Repository == nil || data.Repository.PullRequest == nil {
		return PullRequestPulse{}, &APIError{Status: 404, Message: fmt.Sprintf("no pull request #%d in %s", number, repo)}
	}
	n := data.Repository.PullRequest
	p := PullRequestPulse{PullRequest: n.pullRequest(), State: n.State, Mergeable: n.Mergeable, HeadSHA: n.HeadRefOid, ClosedAt: n.ClosedAt,
		Commits: n.Pushed.TotalCount, Comments: n.Comments.TotalCount}
	if p.Repo == "" {
		p.Repo = repo
	}
	if n.MergedBy != nil {
		p.MergedBy = n.MergedBy.Login
	}
	if len(n.Comments.Nodes) > 0 && n.Comments.Nodes[0].Author != nil {
		p.LastCommentBy = n.Comments.Nodes[0].Author.Login
	}
	if len(n.Pushed.Nodes) > 0 {
		a := n.Pushed.Nodes[0].Commit.Author
		p.PushedBy = a.Name
		if a.User != nil {
			p.PushedBy = a.User.Login
		}
	}
	for _, r := range n.LatestReviews.Nodes {
		rv := Review{State: r.State, SubmittedAt: r.SubmittedAt}
		if r.Author != nil {
			rv.Author = r.Author.Login
		}
		p.Reviews = append(p.Reviews, rv)
	}
	return p, nil
}
