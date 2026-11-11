package gh

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// PersonPR is a pull request in what a person did: one they opened, or one
// they reviewed (with their last review).
type PersonPR struct {
	Repo      string
	Number    int
	Title     string
	URL       string
	State     string // OPEN, MERGED or CLOSED
	Draft     bool
	Branch    string
	Author    string
	CreatedAt time.Time
	UpdatedAt time.Time
	MergedAt  time.Time
	ClosedAt  time.Time
	// Review is the person's last review of it, "" for a pull request they opened.
	Review   string // APPROVED, CHANGES_REQUESTED or COMMENTED
	Reviewed time.Time
}

// PersonIssue is an issue a person opened.
type PersonIssue struct {
	Repo      string
	Number    int
	Title     string
	URL       string
	State     string // OPEN or CLOSED
	CreatedAt time.Time
	ClosedAt  time.Time
}

// PersonCommit is a commit a person wrote, on a default branch.
type PersonCommit struct {
	Repo    string
	SHA     string
	Message string // its first line
	URL     string
	At      time.Time
}

// Person is what someone did lately in the repositories you can see.
type Person struct {
	Login string
	Name  string
	// Opened are the pull requests they opened, Reviewed the ones of others
	// they reviewed, both updated since the start of the window.
	Opened   []PersonPR
	Reviewed []PersonPR
	Issues   []PersonIssue
	Commits  []PersonCommit
	// ForYou are their open pull requests that ask for your review; ForThem
	// your open pull requests that ask for theirs.
	ForYou  []PersonPR
	ForThem []PersonPR
}

// validLogin is what a GitHub login looks like; a bot ends in [bot].
var validLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})(\[bot\])?$`)

// ValidLogin reports whether login can be a GitHub login.
func ValidLogin(login string) bool { return validLogin.MatchString(login) }

// searchName is how a search names login: a bot as app/name.
func searchName(login string) string {
	if name, ok := strings.CutSuffix(login, "[bot]"); ok {
		return "app/" + name
	}
	return login
}

const personPRFields = `fragment personPR on PullRequest {
  number title url state isDraft headRefName createdAt updatedAt mergedAt closedAt
  author { login }
  repository { nameWithOwner }
}`

const personQuery = `query Person($login: String!, $opened: String!, $reviewed: String!, $issues: String!, $forYou: String!, $forThem: String!) {
  user(login: $login) { login name }
  opened: search(query: $opened, type: ISSUE, first: 50) { nodes { ...personPR } }
  reviewed: search(query: $reviewed, type: ISSUE, first: 30) { nodes { ...personPR
    ... on PullRequest { reviews(last: 1, author: $login) { nodes { state submittedAt } } } } }
  issues: search(query: $issues, type: ISSUE, first: 30) { nodes { ... on Issue {
    number title url state createdAt closedAt repository { nameWithOwner } } } }
  forYou: search(query: $forYou, type: ISSUE, first: 20) { nodes { ...personPR } }
  forThem: search(query: $forThem, type: ISSUE, first: 20) { nodes { ...personPR } }
}
` + personPRFields

type personPRNode struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	State       string    `json:"state"`
	IsDraft     bool      `json:"isDraft"`
	HeadRefName string    `json:"headRefName"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	MergedAt    time.Time `json:"mergedAt"`
	ClosedAt    time.Time `json:"closedAt"`
	Author      *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Reviews struct {
		Nodes []struct {
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"reviews"`
}

func (n personPRNode) pr() PersonPR {
	p := PersonPR{Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, URL: n.URL, State: n.State, Draft: n.IsDraft,
		Branch: n.HeadRefName, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, MergedAt: n.MergedAt, ClosedAt: n.ClosedAt}
	if n.Author != nil {
		p.Author = n.Author.Login
	}
	if r := n.Reviews.Nodes; len(r) > 0 {
		p.Review, p.Reviewed = r[len(r)-1].State, r[len(r)-1].SubmittedAt
	}
	return p
}

func prsOf(nodes []personPRNode) []PersonPR {
	var out []PersonPR
	for _, n := range nodes {
		if n.Number != 0 {
			out = append(out, n.pr())
		}
	}
	return out
}

// PersonActivity reads what login did since since: the pull requests they
// opened and reviewed, the issues they opened and the commits they wrote,
// in the repositories you can see. One GraphQL query and one commit search.
func (c *Client) PersonActivity(ctx context.Context, login string, since time.Time) (Person, error) {
	if !ValidLogin(login) {
		return Person{}, fmt.Errorf("%q is not a GitHub login", login)
	}
	who, day := searchName(login), since.UTC().Format("2006-01-02")
	vars := map[string]any{
		"login":    strings.TrimSuffix(login, "[bot]"),
		"opened":   fmt.Sprintf("is:pr author:%s updated:>=%s sort:updated-desc", who, day),
		"reviewed": fmt.Sprintf("is:pr reviewed-by:%s -author:%s updated:>=%s sort:updated-desc", who, who, day),
		"issues":   fmt.Sprintf("is:issue author:%s created:>=%s sort:created-desc", who, day),
		"forYou":   fmt.Sprintf("is:pr is:open author:%s review-requested:@me sort:updated-desc", who),
		"forThem":  fmt.Sprintf("is:pr is:open author:@me review-requested:%s sort:updated-desc", who),
	}
	var data struct {
		User *struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"user"`
		Opened   struct{ Nodes []personPRNode } `json:"opened"`
		Reviewed struct{ Nodes []personPRNode } `json:"reviewed"`
		Issues   struct {
			Nodes []struct {
				Number     int       `json:"number"`
				Title      string    `json:"title"`
				URL        string    `json:"url"`
				State      string    `json:"state"`
				CreatedAt  time.Time `json:"createdAt"`
				ClosedAt   time.Time `json:"closedAt"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"nodes"`
		} `json:"issues"`
		ForYou  struct{ Nodes []personPRNode } `json:"forYou"`
		ForThem struct{ Nodes []personPRNode } `json:"forThem"`
	}
	// A bot is no user, so user is null for one; the searches still answer.
	if err := allowPartial(c.GraphQL(ctx, personQuery, vars, &data)); err != nil {
		return Person{}, err
	}
	p := Person{Login: login, Opened: prsOf(data.Opened.Nodes), Reviewed: prsOf(data.Reviewed.Nodes),
		ForYou: prsOf(data.ForYou.Nodes), ForThem: prsOf(data.ForThem.Nodes)}
	if data.User != nil && !strings.HasSuffix(login, "[bot]") { // a bot's name without [bot] may be someone else
		p.Name = data.User.Name
	}
	for _, n := range data.Issues.Nodes {
		if n.Number != 0 {
			p.Issues = append(p.Issues, PersonIssue{Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, URL: n.URL,
				State: n.State, CreatedAt: n.CreatedAt, ClosedAt: n.ClosedAt})
		}
	}
	commits, err := c.personCommits(ctx, who, day)
	if err != nil {
		return p, err
	}
	p.Commits = commits
	return p, nil
}

// personCommits searches the commits who wrote since day. GitHub searches
// only the default branches.
func (c *Client) personCommits(ctx context.Context, who, day string) ([]PersonCommit, error) {
	q := url.QueryEscape(fmt.Sprintf("author:%s author-date:>=%s", who, day))
	var res struct {
		Items []struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
			Commit  struct {
				Message string `json:"message"`
				Author  struct {
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	if err := c.Get(ctx, "search/commits?q="+q+"&sort=author-date&order=desc&per_page=50", &res); err != nil {
		return nil, err
	}
	var out []PersonCommit
	for _, it := range res.Items {
		msg, _, _ := strings.Cut(it.Commit.Message, "\n")
		out = append(out, PersonCommit{Repo: it.Repository.FullName, SHA: it.SHA, Message: msg, URL: it.HTMLURL, At: it.Commit.Author.Date})
	}
	return out, nil
}
