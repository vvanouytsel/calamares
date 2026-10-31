package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Issue is an issue with what the Issues place and the issue screen show.
type Issue struct {
	ID          string
	Repo        string
	Number      int
	Title       string
	URL         string
	State       string // OPEN or CLOSED
	StateReason string // COMPLETED, NOT_PLANNED, REOPENED or ""
	Author      string
	Body        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Labels      []Label
	Assignees   []string
	Milestone   string
	Comments    int
	LastComment *IssueComment
	Linked      []LinkedPR
	// What you may do with it, from GraphQL's viewerCan… fields.
	CanUpdate, CanClose, CanReopen, CanLabel, CanAssign bool
	ViewerPermission                                    string
	// Why says how it concerns you: "assigned", "asked", "author" or "mention".
	Why string
}

// IssueComment is a comment on an issue.
type IssueComment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
	URL       string
}

// LinkedPR is a pull request that closes an issue when it is merged.
type LinkedPR struct {
	Repo   string
	Number int
	Title  string
	State  string // OPEN, CLOSED or MERGED
	Draft  bool
	Checks string // SUCCESS, FAILURE, PENDING… or ""
}

// IssuesOfMine are the open issues that concern you, in the three views.
type IssuesOfMine struct {
	Assigned, Created, Mentioned []Issue
	// More says, for each list in the order above, that GitHub found more
	// than were read.
	More [3]bool `json:",omitempty"`
}

const issueFields = `fragment issue on Issue {
  id number title url state stateReason createdAt updatedAt body
  author { login }
  repository { nameWithOwner viewerPermission }
  labels(first: 10) { nodes { name color } }
  assignees(first: 5) { nodes { login } }
  milestone { title }
  comments { totalCount }
  lastComment: comments(last: 1) { nodes { author { login } createdAt body url } }
  closedByPullRequestsReferences(first: 3) { nodes { number title state isDraft repository { nameWithOwner }
    commits(last: 1) { nodes { commit { statusCheckRollup { state } } } } } }
  viewerCanUpdate viewerCanClose viewerCanReopen viewerCanLabel viewerCanAssign
}`

// myIssueSearches are the searches for the issues that concern you, in the
// order of IssuesOfMine.
var myIssueSearches = []mineSearch{
	{"assigned", "is:issue is:open assignee:@me archived:false sort:updated-desc"},
	{"created", "is:issue is:open author:@me archived:false sort:updated-desc"},
	{"mentioned", "is:issue is:open mentions:@me archived:false sort:updated-desc"},
}

var myIssuesQuery = mineQuery("MyIssues", myIssueSearches, "issue", issueFields, "  viewer { login }\n")

type issueNode struct {
	ID          string    `json:"id"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	State       string    `json:"state"`
	StateReason string    `json:"stateReason"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Body        string    `json:"body"`
	Author      *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner    string `json:"nameWithOwner"`
		ViewerPermission string `json:"viewerPermission"`
	} `json:"repository"`
	Labels struct {
		Nodes []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	Assignees struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
	} `json:"assignees"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
	LastComment struct {
		Nodes []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			CreatedAt time.Time `json:"createdAt"`
			Body      string    `json:"body"`
			URL       string    `json:"url"`
		} `json:"nodes"`
	} `json:"lastComment"`
	Closing struct {
		Nodes []struct {
			Number     int    `json:"number"`
			Title      string `json:"title"`
			State      string `json:"state"`
			IsDraft    bool   `json:"isDraft"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
			Commits struct {
				Nodes []struct {
					Commit struct {
						Rollup *struct {
							State string `json:"state"`
						} `json:"statusCheckRollup"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
		} `json:"nodes"`
	} `json:"closedByPullRequestsReferences"`
	CanUpdate bool `json:"viewerCanUpdate"`
	CanClose  bool `json:"viewerCanClose"`
	CanReopen bool `json:"viewerCanReopen"`
	CanLabel  bool `json:"viewerCanLabel"`
	CanAssign bool `json:"viewerCanAssign"`
}

func (n issueNode) issue() Issue {
	i := Issue{ID: n.ID, Repo: n.Repository.NameWithOwner, Number: n.Number, Title: n.Title, URL: n.URL, State: n.State, StateReason: n.StateReason,
		Body: n.Body, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Comments: n.Comments.TotalCount, ViewerPermission: n.Repository.ViewerPermission,
		CanUpdate: n.CanUpdate, CanClose: n.CanClose, CanReopen: n.CanReopen, CanLabel: n.CanLabel, CanAssign: n.CanAssign}
	if n.Author != nil {
		i.Author = n.Author.Login
	}
	for _, l := range n.Labels.Nodes {
		i.Labels = append(i.Labels, Label{l.Name, l.Color})
	}
	for _, u := range n.Assignees.Nodes {
		i.Assignees = append(i.Assignees, u.Login)
	}
	if n.Milestone != nil {
		i.Milestone = n.Milestone.Title
	}
	if len(n.LastComment.Nodes) > 0 {
		c := n.LastComment.Nodes[0]
		lc := &IssueComment{Body: c.Body, CreatedAt: c.CreatedAt, URL: c.URL}
		if c.Author != nil {
			lc.Author = c.Author.Login
		}
		i.LastComment = lc
	}
	for _, p := range n.Closing.Nodes {
		lp := LinkedPR{Repo: p.Repository.NameWithOwner, Number: p.Number, Title: p.Title, State: p.State, Draft: p.IsDraft}
		if len(p.Commits.Nodes) > 0 && p.Commits.Nodes[0].Commit.Rollup != nil {
			lp.Checks = p.Commits.Nodes[0].Commit.Rollup.State
		}
		i.Linked = append(i.Linked, lp)
	}
	return i
}

// MyIssues returns the open issues assigned to you, created by you and
// mentioning you, each with how it concerns you. One GraphQL request, and
// one more per 30 for a search with more than 30, up to 100 each.
func (c *Client) MyIssues(ctx context.Context) (IssuesOfMine, error) {
	var data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Assigned  searchPage[issueNode] `json:"assigned"`
		Created   searchPage[issueNode] `json:"created"`
		Mentioned searchPage[issueNode] `json:"mentioned"`
	}
	if err := allowPartial(c.GraphQL(ctx, myIssuesQuery, nil, &data)); err != nil {
		return IssuesOfMine{}, err
	}
	me := data.Viewer.Login
	role := map[string]string{}
	pick := func(nodes []issueNode, why string, out *[]Issue) {
		for _, n := range nodes {
			if n.Number == 0 {
				continue
			}
			i := n.issue()
			key := fmt.Sprintf("%s#%d", i.Repo, i.Number)
			if _, ok := role[key]; !ok {
				role[key] = why
			}
			*out = append(*out, i)
		}
	}
	var res IssuesOfMine
	lists := []*[]Issue{&res.Assigned, &res.Created, &res.Mentioned}
	for i, page := range []searchPage[issueNode]{data.Assigned, data.Created, data.Mentioned} {
		var nodes []issueNode
		nodes, res.More[i] = readRest(ctx, c, myIssueSearches[i], "issue", issueFields, page)
		pick(nodes, []string{"assigned", "author", "mention"}[i], lists[i])
	}
	for _, list := range []*[]Issue{&res.Assigned, &res.Created, &res.Mentioned} {
		for k := range *list {
			i := &(*list)[k]
			i.Why = role[fmt.Sprintf("%s#%d", i.Repo, i.Number)]
			if i.Asked(me) {
				i.Why = "asked"
			}
		}
	}
	return res, nil
}

// Asked reports whether someone else commented last and is asking you
// something: they mention you, or end with a question.
func (i Issue) Asked(me string) bool {
	c := i.LastComment
	if c == nil || strings.EqualFold(c.Author, me) {
		return false
	}
	body := strings.TrimSpace(c.Body)
	assigned := false
	for _, a := range i.Assignees {
		if strings.EqualFold(a, me) {
			assigned = true
		}
	}
	return strings.Contains(strings.ToLower(body), "@"+strings.ToLower(me)) || (assigned && strings.HasSuffix(body, "?"))
}

// WaitsForYou reports whether the issue wants something from you: it is
// yours and someone else spoke last, or you were asked.
func (i Issue) WaitsForYou(me string) bool {
	if i.Why == "asked" {
		return true
	}
	return i.Why == "assigned" && i.LastComment != nil && !strings.EqualFold(i.LastComment.Author, me)
}

const repoIssuesQuery = `query RepoIssues($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    hasIssuesEnabled viewerPermission
    issues(states: OPEN, first: 50, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { ...issue } }
  }
}
` + issueFields

// RepoIssues returns the open issues of repo, most recently active first;
// enabled is false when the repo has issues turned off.
func (c *Client) RepoIssues(ctx context.Context, repo string) (issues []Issue, enabled bool, err error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			HasIssues bool `json:"hasIssuesEnabled"`
			Issues    struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
	}
	if err := allowPartial(c.GraphQL(ctx, repoIssuesQuery, map[string]any{"owner": owner, "name": name}, &data)); err != nil {
		return nil, false, err
	}
	for _, n := range data.Repository.Issues.Nodes {
		issues = append(issues, n.issue())
	}
	return issues, data.Repository.HasIssues, nil
}

// IssueDetail is an issue with its comments.
type IssueDetail struct {
	Issue
	CommentList []IssueComment
}

const issueQuery = `query IssueOne($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { issue(number: $number) { ...issue } }
}
` + issueFields

// GetIssue reads an issue (one GraphQL request, with what you may do with it
// and the pull requests that close it) and its comments (REST, cached by ETag).
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (IssueDetail, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			Issue *issueNode `json:"issue"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := allowPartial(c.GraphQL(ctx, issueQuery, vars, &data)); err != nil {
		return IssueDetail{}, err
	}
	if data.Repository.Issue == nil {
		return IssueDetail{}, fmt.Errorf("issue %s#%d not found", repo, number)
	}
	d := IssueDetail{Issue: data.Repository.Issue.issue()}
	var cs []struct {
		ID        int64     `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
		HTMLURL   string    `json:"html_url"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", repo, number), &cs); err != nil {
		return d, err
	}
	for _, x := range cs {
		d.CommentList = append(d.CommentList, IssueComment{ID: x.ID, Author: x.User.Login, Body: x.Body, CreatedAt: x.CreatedAt, URL: x.HTMLURL})
	}
	if n := len(d.CommentList); n > 0 {
		d.LastComment = &d.CommentList[n-1]
	}
	return d, nil
}

// CloseIssue closes an issue; reason is "completed" or "not_planned".
func (c *Client) CloseIssue(ctx context.Context, repo string, number int, reason string) error {
	return c.Do(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, number), map[string]string{"state": "closed", "state_reason": reason}, nil)
}

// ReopenIssue opens a closed issue again.
func (c *Client) ReopenIssue(ctx context.Context, repo string, number int) error {
	return c.Do(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/issues/%d", repo, number), map[string]string{"state": "open", "state_reason": "reopened"}, nil)
}

// CreateIssue opens an issue and returns its number.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string, labels, assignees []string) (int, error) {
	in := map[string]any{"title": title}
	if body != "" {
		in["body"] = body
	}
	if len(labels) > 0 {
		in["labels"] = labels
	}
	if len(assignees) > 0 {
		in["assignees"] = assignees
	}
	var out struct {
		Number int `json:"number"`
	}
	err := c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues", repo), in, &out)
	return out.Number, err
}

// IssueTemplate is an issue template of a repo.
type IssueTemplate struct {
	Name      string
	About     string
	Title     string
	Labels    []string
	Assignees []string
	Body      string
}

// IssueTemplates reads the templates in .github/ISSUE_TEMPLATE. Markdown
// templates fill the title, labels and body from their front matter; the
// fields of a YAML issue form become headings of a plain body. A repo
// without templates gives an empty list.
func (c *Client) IssueTemplates(ctx context.Context, repo string) ([]IssueTemplate, error) {
	var dir []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := c.Get(ctx, "repos/"+repo+"/contents/.github/ISSUE_TEMPLATE", &dir); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []IssueTemplate
	for _, f := range dir {
		ext := strings.ToLower(path.Ext(f.Name))
		if f.Type != "file" || (ext != ".md" && ext != ".yml" && ext != ".yaml") || strings.EqualFold(f.Name, "config.yml") {
			continue
		}
		src, err := c.FileContent(ctx, repo, ".github/ISSUE_TEMPLATE/"+url.PathEscape(f.Name), "")
		if err != nil {
			continue
		}
		if ext == ".md" {
			out = append(out, parseMarkdownTemplate(f.Name, src))
		} else {
			out = append(out, parseFormTemplate(f.Name, src))
		}
	}
	return out, nil
}

func splitFrontMatter(src string) (front map[string]string, body string) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	front = map[string]string{}
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return front, src
	}
	head, body, found := strings.Cut(rest, "\n---")
	if !found {
		return front, src
	}
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			front[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return front, strings.TrimPrefix(body, "\n")
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.Trim(s, "[]"), ",") {
		if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseMarkdownTemplate(file, src string) IssueTemplate {
	front, body := splitFrontMatter(src)
	t := IssueTemplate{Name: front["name"], About: front["about"], Title: front["title"], Labels: splitList(front["labels"]), Assignees: splitList(front["assignees"]), Body: strings.TrimSpace(body)}
	if t.Name == "" {
		t.Name = strings.TrimSuffix(file, path.Ext(file))
	}
	return t
}

func parseFormTemplate(file, src string) IssueTemplate {
	t := IssueTemplate{Name: strings.TrimSuffix(file, path.Ext(file))}
	var body strings.Builder
	inBody := false
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name:"):
			t.Name = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "name:")), `"'`)
		case strings.HasPrefix(line, "description:"):
			t.About = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "description:")), `"'`)
		case strings.HasPrefix(line, "title:"):
			t.Title = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "title:")), `"'`)
		case strings.HasPrefix(line, "labels:"):
			t.Labels = splitList(strings.TrimPrefix(line, "labels:"))
		case strings.HasPrefix(line, "body:"):
			inBody = true
		case inBody && strings.HasPrefix(trim, "label:"):
			body.WriteString("### " + strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "label:")), `"'`) + "\n\n\n")
		}
	}
	t.Body = strings.TrimSpace(body.String())
	return t
}
