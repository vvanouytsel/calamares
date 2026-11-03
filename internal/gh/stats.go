package gh

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Authored is something someone did at a moment: a commit or a merged pull
// request. Login is empty for a commit GitHub can't tie to an account; Name
// is then the name in the commit.
type Authored struct {
	Login string    `json:"l,omitempty"`
	Name  string    `json:"n,omitempty"`
	At    time.Time `json:"a"`
}

// Who is the login, or the name for an author without an account.
func (x Authored) Who() string {
	if x.Login != "" {
		return x.Login
	}
	return x.Name
}

// RepoStats is what happened on a repo since a moment: the commits on its
// default branch and the pull requests merged, newest first, and how many of
// each it has ever had.
type RepoStats struct {
	Repo    string     `json:"r"`
	Commits []Authored `json:"c,omitempty"`
	// CommitsMore says there were more commits since than were read.
	CommitsMore bool       `json:"m,omitempty"`
	Merged      []Authored `json:"p,omitempty"`
	AllCommits  int        `json:"ac"`
	AllMerged   int        `json:"ap"`
}

// statsBatch is how many repos one request reads; each asks for up to
// 200 nodes.
const statsBatch = 10

// statsPages is how many pages of 100 commits are read of a busy repo.
const statsPages = 10

const statsFragment = `fragment stats on Repository {
  nameWithOwner
  defaultBranchRef { target { ... on Commit {
    recent: history(since: $since, first: 100) { pageInfo { hasNextPage endCursor } nodes { ...authored } }
    all: history(first: 1) { totalCount }
  } } }
  pullRequests(states: MERGED, first: 100, orderBy: {field: UPDATED_AT, direction: DESC}) {
    totalCount nodes { mergedAt author { login } }
  }
}
fragment authored on Commit { committedDate author { name user { login } } }`

type statsRepo struct {
	NameWithOwner    string `json:"nameWithOwner"`
	DefaultBranchRef *struct {
		Target struct {
			Recent commitPage `json:"recent"`
			All    struct {
				TotalCount int `json:"totalCount"`
			} `json:"all"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
	PullRequests struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			MergedAt time.Time `json:"mergedAt"`
			Author   *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"pullRequests"`
}

type commitPage struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []struct {
		CommittedDate time.Time `json:"committedDate"`
		Author        *struct {
			Name string `json:"name"`
			User *struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"author"`
	} `json:"nodes"`
}

func (p commitPage) authored() []Authored {
	out := make([]Authored, 0, len(p.Nodes))
	for _, n := range p.Nodes {
		x := Authored{At: n.CommittedDate}
		if n.Author != nil {
			x.Name = n.Author.Name
			if n.Author.User != nil {
				x.Login = n.Author.User.Login
			}
		}
		out = append(out, x)
	}
	return out
}

// RepoStats reads what happened on each repo since since. Of a busy repo it
// reads up to statsPages pages of commits; the pull requests are the 100
// updated last. Repos it may not read are left out.
func (c *Client) RepoStats(ctx context.Context, repos []string, since time.Time) ([]RepoStats, error) {
	var out []RepoStats
	for start := 0; start < len(repos); start += statsBatch {
		got, err := c.statsOf(ctx, repos[start:min(start+statsBatch, len(repos))], since)
		if err != nil {
			return out, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (c *Client) statsOf(ctx context.Context, repos []string, since time.Time) ([]RepoStats, error) {
	params, fields := []string{"$since: GitTimestamp!"}, []string{}
	vars := map[string]any{"since": since.UTC().Format(time.RFC3339)}
	for i, repo := range repos {
		owner, name, _ := strings.Cut(repo, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		fields = append(fields, fmt.Sprintf("r%d: repository(owner: $o%d, name: $n%d) { ...stats }", i, i, i))
		vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
	}
	query := "query RepoStats(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}\n" + statsFragment
	var data map[string]*statsRepo
	if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
		return nil, err
	}
	var out []RepoStats
	for i := range repos {
		r := data[fmt.Sprintf("r%d", i)]
		if r == nil {
			continue
		}
		act := RepoStats{Repo: r.NameWithOwner, AllMerged: r.PullRequests.TotalCount}
		for _, p := range r.PullRequests.Nodes {
			if p.MergedAt.Before(since) {
				continue
			}
			x := Authored{At: p.MergedAt}
			if p.Author != nil {
				x.Login = p.Author.Login
			}
			act.Merged = append(act.Merged, x)
		}
		if b := r.DefaultBranchRef; b != nil {
			act.AllCommits = b.Target.All.TotalCount
			act.Commits = b.Target.Recent.authored()
			more, after := b.Target.Recent.PageInfo.HasNextPage, b.Target.Recent.PageInfo.EndCursor
			for page := 1; more && page < statsPages; page++ {
				next, err := c.commitsAfter(ctx, r.NameWithOwner, since, after)
				if err != nil {
					break
				}
				act.Commits = append(act.Commits, next.authored()...)
				more, after = next.PageInfo.HasNextPage, next.PageInfo.EndCursor
			}
			act.CommitsMore = more
		}
		out = append(out, act)
	}
	return out, nil
}

const commitsAfterQuery = `query CommitsAfter($owner: String!, $name: String!, $since: GitTimestamp!, $after: String!) {
  repository(owner: $owner, name: $name) { defaultBranchRef { target { ... on Commit {
    history(since: $since, first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { ...authored } }
  } } } }
}
fragment authored on Commit { committedDate author { name user { login } } }`

func (c *Client) commitsAfter(ctx context.Context, repo string, since time.Time, after string) (commitPage, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			DefaultBranchRef struct {
				Target struct {
					History commitPage `json:"history"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "since": since.UTC().Format(time.RFC3339), "after": after}
	err := c.GraphQL(ctx, commitsAfterQuery, vars, &data)
	return data.Repository.DefaultBranchRef.Target.History, err
}

// Contributor is someone who committed to a repo's default branch, and how
// many commits they made there.
type Contributor struct {
	Login   string `json:"l"`
	Commits int    `json:"c"`
}

// Contributors reads how many commits each of the 100 people who committed
// most to repo's default branch made there, ever. GitHub works this out on
// request: until it has, ready is false and the call is worth making again
// a little later.
func (c *Client) Contributors(ctx context.Context, repo string) (out []Contributor, ready bool, err error) {
	body, err := c.getRaw(ctx, "repos/"+repo+"/stats/contributors", "application/vnd.github+json", false)
	if err != nil {
		return nil, false, err
	}
	var stats []struct {
		Total  int `json:"total"`
		Author *struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	// An empty repo is 204 with no body; an answer still being worked out is
	// 202 with an empty object.
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, true, nil
	}
	if decode(body, &stats) != nil {
		return nil, false, nil
	}
	for _, s := range stats {
		if s.Author != nil && s.Total > 0 {
			out = append(out, Contributor{Login: s.Author.Login, Commits: s.Total})
		}
	}
	return out, true, nil
}

// RunCount is how many runs repo has had since since (ever, when since is
// zero) and how many of them failed.
func (c *Client) RunCount(ctx context.Context, repo string, since time.Time) (runs, failed int, err error) {
	f := RunFilter{PerPage: 1}
	if !since.IsZero() {
		f.Created = ">=" + since.UTC().Format("2006-01-02")
	}
	if _, runs, err = c.ListRuns(ctx, repo, f); err != nil {
		return 0, 0, err
	}
	f.Status = "failure"
	_, failed, err = c.ListRuns(ctx, repo, f)
	return runs, failed, err
}

// ContributionDay is a day of your contribution calendar: the commits, pull
// requests, reviews and issues GitHub counts for you that day.
type ContributionDay struct {
	Date  string `json:"d"` // 2006-01-02
	Count int    `json:"c"`
}

// Contributions reads your contribution calendar from your first year with a
// contribution to today, oldest day first.
func (c *Client) Contributions(ctx context.Context) ([]ContributionDay, error) {
	var years struct {
		Viewer struct {
			ContributionsCollection struct {
				ContributionYears []int `json:"contributionYears"`
			} `json:"contributionsCollection"`
		} `json:"viewer"`
	}
	if err := c.GraphQL(ctx, `query { viewer { contributionsCollection { contributionYears } } }`, nil, &years); err != nil {
		return nil, err
	}
	var fields []string
	for _, y := range years.Viewer.ContributionsCollection.ContributionYears {
		fields = append(fields, fmt.Sprintf(`y%d: contributionsCollection(from: "%d-01-01T00:00:00Z", to: "%d-12-31T23:59:59Z") { contributionCalendar { weeks { contributionDays { date contributionCount } } } }`, y, y, y))
	}
	if len(fields) == 0 {
		return nil, nil
	}
	var data struct {
		Viewer map[string]struct {
			ContributionCalendar struct {
				Weeks []struct {
					ContributionDays []struct {
						Date  string `json:"date"`
						Count int    `json:"contributionCount"`
					} `json:"contributionDays"`
				} `json:"weeks"`
			} `json:"contributionCalendar"`
		} `json:"viewer"`
	}
	if err := c.GraphQL(ctx, "query { viewer {\n"+strings.Join(fields, "\n")+"\n} }", nil, &data); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []ContributionDay
	for _, y := range data.Viewer {
		for _, w := range y.ContributionCalendar.Weeks {
			for _, d := range w.ContributionDays {
				if !seen[d.Date] {
					seen[d.Date] = true
					out = append(out, ContributionDay{d.Date, d.Count})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}
