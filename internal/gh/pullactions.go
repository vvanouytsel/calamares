package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// touch notes that something changed, so no earlier answer is reused as fresh.
func (c *Client) touch() {
	c.mu.Lock()
	c.mutatedAt = time.Now()
	c.mu.Unlock()
}

// mutate runs a GraphQL mutation.
func (c *Client) mutate(ctx context.Context, query string, vars map[string]any) error {
	var out map[string]any
	err := c.GraphQL(ctx, query, vars, &out)
	if err == nil {
		c.touch()
	}
	return err
}

// SetDraft marks a pull request ready for review (draft false) or converts it to a draft.
func (c *Client) SetDraft(ctx context.Context, nodeID string, draft bool) error {
	if draft {
		return c.mutate(ctx, `mutation ConvertToDraft($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { clientMutationId } }`, map[string]any{"id": nodeID})
	}
	return c.mutate(ctx, `mutation ReadyForReview($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { clientMutationId } }`, map[string]any{"id": nodeID})
}

// SetPullRequestState closes (state "closed") or reopens ("open") a pull request.
func (c *Client) SetPullRequestState(ctx context.Context, repo string, number int, state string) error {
	return c.Do(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/pulls/%d", repo, number), map[string]string{"state": state}, nil)
}

// RequestReviewers asks users to review.
func (c *Client) RequestReviewers(ctx context.Context, repo string, number int, users []string) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", repo, number), map[string][]string{"reviewers": users}, nil)
}

// RemoveReviewers withdraws review requests.
func (c *Client) RemoveReviewers(ctx context.Context, repo string, number int, users []string) error {
	return c.Do(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", repo, number), map[string][]string{"reviewers": users}, nil)
}

// SetLabels replaces the labels of a pull request or issue.
func (c *Client) SetLabels(ctx context.Context, repo string, number int, labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	return c.Do(ctx, http.MethodPut, fmt.Sprintf("repos/%s/issues/%d/labels", repo, number), map[string][]string{"labels": labels}, nil)
}

// RepoLabels lists the labels of a repo.
func (c *Client) RepoLabels(ctx context.Context, repo string) ([]Label, error) {
	var raw []struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/labels?per_page=100", repo), &raw); err != nil {
		return nil, err
	}
	out := make([]Label, len(raw))
	for i, l := range raw {
		out[i] = Label{l.Name, l.Color}
	}
	return out, nil
}

// AddAssignees and RemoveAssignees change who an issue or pull request is assigned to.
func (c *Client) AddAssignees(ctx context.Context, repo string, number int, users []string) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/assignees", repo, number), map[string][]string{"assignees": users}, nil)
}

func (c *Client) RemoveAssignees(ctx context.Context, repo string, number int, users []string) error {
	return c.Do(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/issues/%d/assignees", repo, number), map[string][]string{"assignees": users}, nil)
}

// UpdateBranch brings the pull request's branch up to date with its base:
// method "merge" merges the base into it, "rebase" rebases it.
func (c *Client) UpdateBranch(ctx context.Context, repo string, number int, nodeID, headSHA, method string) error {
	if method == "rebase" {
		return c.mutate(ctx, `mutation UpdateBranch($id: ID!, $oid: GitObjectID) { updatePullRequestBranch(input: {pullRequestId: $id, expectedHeadOid: $oid, updateMethod: REBASE}) { clientMutationId } }`,
			map[string]any{"id": nodeID, "oid": headSHA})
	}
	return c.Do(ctx, http.MethodPut, fmt.Sprintf("repos/%s/pulls/%d/update-branch", repo, number), map[string]string{"expected_head_sha": headSHA}, nil)
}

// EnableAutoMerge merges the pull request by itself once it can be (method
// "merge", "squash" or "rebase"); DisableAutoMerge turns it off.
func (c *Client) EnableAutoMerge(ctx context.Context, nodeID, method string) error {
	return c.mutate(ctx, `mutation EnableAutoMerge($id: ID!, $m: PullRequestMergeMethod!) { enablePullRequestAutoMerge(input: {pullRequestId: $id, mergeMethod: $m}) { clientMutationId } }`,
		map[string]any{"id": nodeID, "m": strings.ToUpper(method)})
}

func (c *Client) DisableAutoMerge(ctx context.Context, nodeID string) error {
	return c.mutate(ctx, `mutation DisableAutoMerge($id: ID!) { disablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }`, map[string]any{"id": nodeID})
}

// ResolveThread resolves or unresolves a review thread.
func (c *Client) ResolveThread(ctx context.Context, threadID string, resolve bool) error {
	if resolve {
		return c.mutate(ctx, `mutation ResolveThread($id: ID!) { resolveReviewThread(input: {threadId: $id}) { clientMutationId } }`, map[string]any{"id": threadID})
	}
	return c.mutate(ctx, `mutation UnresolveThread($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { clientMutationId } }`, map[string]any{"id": threadID})
}

// BehindBy counts the commits of base that head lacks.
func (c *Client) BehindBy(ctx context.Context, repo, base, head string) (int, error) {
	var out struct {
		BehindBy int `json:"behind_by"`
	}
	err := c.Get(ctx, fmt.Sprintf("repos/%s/compare/%s...%s?per_page=1", repo, url.PathEscape(base), url.PathEscape(head)), &out)
	return out.BehindBy, err
}

// PRCommit is one commit of a pull request.
type PRCommit struct {
	SHA     string
	Message string // the subject line
	Author  string
	At      time.Time
}

// PullRequestCommits lists the commits of a pull request, oldest first.
func (c *Client) PullRequestCommits(ctx context.Context, repo string, number int) ([]PRCommit, error) {
	var raw []struct {
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
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/pulls/%d/commits?per_page=100", repo, number), &raw); err != nil {
		return nil, err
	}
	out := make([]PRCommit, len(raw))
	for i, r := range raw {
		subject, _, _ := strings.Cut(r.Commit.Message, "\n")
		who := r.Commit.Author.Name
		if r.Author != nil {
			who = r.Author.Login
		}
		out[i] = PRCommit{SHA: r.SHA, Message: subject, Author: who, At: r.Commit.Author.Date}
	}
	return out, nil
}

// AssignableUsers lists who can be assigned or asked to review in repo.
func (c *Client) AssignableUsers(ctx context.Context, repo string) ([]string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			Users struct {
				Nodes []struct {
					Login string `json:"login"`
				} `json:"nodes"`
			} `json:"assignableUsers"`
		} `json:"repository"`
	}
	q := `query AssignableUsers($owner: String!, $name: String!) { repository(owner: $owner, name: $name) { assignableUsers(first: 100) { nodes { login } } } }`
	if err := allowPartial(c.GraphQL(ctx, q, map[string]any{"owner": owner, "name": name}, &data)); err != nil {
		return nil, err
	}
	var out []string
	for _, u := range data.Repository.Users.Nodes {
		out = append(out, u.Login)
	}
	return out, nil
}

// DraftComment is a comment on a line of the diff, held for a review.
type DraftComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"` // LEFT or RIGHT
	Body string `json:"body"`
}

// ReviewWithComments submits one review with its line comments.
func (c *Client) ReviewWithComments(ctx context.Context, repo string, number int, event, body string, comments []DraftComment) error {
	in := map[string]any{"event": event}
	if body != "" {
		in["body"] = body
	}
	if len(comments) > 0 {
		in["comments"] = comments
	}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number), in, nil)
}

// CreatePullRequest opens a pull request of head into base and returns its number.
func (c *Client) CreatePullRequest(ctx context.Context, repo, title, head, base, body string, draft bool) (int, error) {
	in := map[string]any{"title": title, "head": head, "base": base, "draft": draft}
	if body != "" {
		in["body"] = body
	}
	var out struct {
		Number int `json:"number"`
	}
	err := c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls", repo), in, &out)
	return out.Number, err
}

// PullRequestTemplate reads the repo's pull request template from ref, or "".
func (c *Client) PullRequestTemplate(ctx context.Context, repo, ref string) string {
	for _, p := range []string{".github/pull_request_template.md", ".github/PULL_REQUEST_TEMPLATE.md", "docs/pull_request_template.md", "pull_request_template.md", "PULL_REQUEST_TEMPLATE.md"} {
		if s, err := c.FileContent(ctx, repo, p, ref); err == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
