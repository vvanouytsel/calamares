package gh

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PullRequest is an open pull request with the state of its checks and reviews.
type PullRequest struct {
	Number         int
	Title          string
	Repo           string
	Branch         string
	Base           string
	URL            string
	Author         string
	Draft          bool
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or ""
	UpdatedAt      time.Time
	Checks         CheckSummary
	// Why says how it concerns you, for your own list: WhyReview, WhyYours or WhyInvolved.
	Why string
}

// Reasons a pull request is in your list, most urgent first.
const (
	WhyReview   = "review requested"
	WhyYours    = "yours"
	WhyInvolved = "involved"
)

// CheckSummary counts the check runs and statuses on a PR's latest commit.
type CheckSummary struct {
	State                          string // SUCCESS, FAILURE, ERROR, PENDING, EXPECTED or "" (no checks)
	Total, Passed, Failed, Pending int
}

const prFields = `fragment pr on PullRequest {
  number title url isDraft updatedAt headRefName baseRefName reviewDecision
  author { login }
  repository { nameWithOwner }
  commits(last: 1) { nodes { commit { statusCheckRollup {
    state
    contexts(first: 100) { totalCount nodes {
      __typename
      ... on CheckRun { status conclusion }
      ... on StatusContext { state }
    } }
  } } } }
}`

type prNode struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	ReviewDecision string    `json:"reviewDecision"`
	Author         *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Rollup *struct {
					State    string `json:"state"`
					Contexts struct {
						TotalCount int `json:"totalCount"`
						Nodes      []struct {
							Type       string `json:"__typename"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							State      string `json:"state"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n prNode) pullRequest() PullRequest {
	pr := PullRequest{Number: n.Number, Title: n.Title, URL: n.URL, Draft: n.IsDraft, UpdatedAt: n.UpdatedAt,
		Branch: n.HeadRefName, Base: n.BaseRefName, Repo: n.Repository.NameWithOwner, ReviewDecision: n.ReviewDecision}
	if n.Author != nil {
		pr.Author = n.Author.Login
	}
	if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.Rollup != nil {
		r := n.Commits.Nodes[0].Commit.Rollup
		pr.Checks.State = r.State
		pr.Checks.Total = r.Contexts.TotalCount
		for _, cx := range r.Contexts.Nodes {
			switch checkKind(cx.Type, cx.Status, cx.Conclusion, cx.State) {
			case "pending":
				pr.Checks.Pending++
			case "passed":
				pr.Checks.Passed++
			default:
				pr.Checks.Failed++
			}
		}
	}
	return pr
}

// checkKind sorts a check run or commit status into passed, failed or pending.
func checkKind(typ, status, conclusion, state string) string {
	switch {
	case typ == "CheckRun" && status != "COMPLETED":
		return "pending"
	case typ == "CheckRun" && (conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED"):
		return "passed"
	case typ == "CheckRun":
		return "failed"
	case state == "SUCCESS":
		return "passed"
	case state == "PENDING" || state == "EXPECTED":
		return "pending"
	}
	return "failed"
}

type prSearch struct {
	Nodes []prNode `json:"nodes"`
}

// myPRSearches are the searches for the pull requests that concern you, in
// the order a pull request found by more than one is told by.
var myPRSearches = []mineSearch{
	{"review", "is:pr is:open review-requested:@me archived:false sort:updated-desc"},
	{"authored", "is:pr is:open author:@me archived:false sort:updated-desc"},
	{"involved", "is:pr is:open involves:@me archived:false sort:updated-desc"},
}

var myPRsQuery = mineQuery("MyPullRequests", myPRSearches, "pr", prFields, "")

// MyPullRequests returns the open pull requests that concern you: those
// waiting for your review, then yours, then others you're involved in.
// One GraphQL request, and one more per 30 for a search with more than 30,
// up to 100 each; more says a search found more than that.
func (c *Client) MyPullRequests(ctx context.Context) (prs []PullRequest, more bool, err error) {
	var data struct {
		Review   searchPage[prNode] `json:"review"`
		Authored searchPage[prNode] `json:"authored"`
		Involved searchPage[prNode] `json:"involved"`
	}
	if err := allowPartial(c.GraphQL(ctx, myPRsQuery, nil, &data)); err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	var out []PullRequest
	for i, group := range []struct {
		page searchPage[prNode]
		why  string
	}{{data.Review, WhyReview}, {data.Authored, WhyYours}, {data.Involved, WhyInvolved}} {
		nodes, more1 := readRest(ctx, c, myPRSearches[i], "pr", prFields, group.page)
		more = more || more1
		for _, n := range nodes {
			if n.Number == 0 || seen[n.URL] {
				continue
			}
			seen[n.URL] = true
			pr := n.pullRequest()
			pr.Why = group.why
			out = append(out, pr)
		}
	}
	return out, more, nil
}

const repoPRsQuery = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: OPEN, first: 50, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { ...pr } }
  }
}
` + prFields

// RepoPullRequests returns the open pull requests of repo, most recently updated first.
func (c *Client) RepoPullRequests(ctx context.Context, repo string) ([]PullRequest, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Repository struct {
			PullRequests prSearch `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := allowPartial(c.GraphQL(ctx, repoPRsQuery, map[string]any{"owner": owner, "name": name}, &data)); err != nil {
		return nil, err
	}
	var out []PullRequest
	for _, n := range data.Repository.PullRequests.Nodes {
		out = append(out, n.pullRequest())
	}
	return out, nil
}

// PullRequestDetail is everything the pull request screen shows.
type PullRequestDetail struct {
	PullRequest
	Body                         string
	State                        string // OPEN, CLOSED or MERGED
	Mergeable                    string // MERGEABLE, CONFLICTING or UNKNOWN
	MergeState                   string // CLEAN, BLOCKED, BEHIND, DIRTY, UNSTABLE, HAS_HOOKS, DRAFT or UNKNOWN
	Additions, Deletions, Files  int
	Reviews                      []Review
	Requested                    []string // reviewers still asked to review
	Checks                       []Check
	ChangedFiles                 []FileChange
	MergeMethods                 []string // what the repo allows: merge, squash, rebase
	HeadSHA                      string
	ViewerLogin                  string
	ViewerCanReview, ViewerOwner bool

	// ID is the GraphQL node id the mutations need.
	ID string
	// What you may do with it, from GraphQL's viewerCan… fields.
	CanUpdate, CanClose, CanReopen, CanLabel, CanAssign      bool
	CanEnableAutoMerge, CanDisableAutoMerge, CanUpdateBranch bool
	AutoMerge                                                *AutoMerge
	Labels                                                   []Label
	Assignees                                                []string
	Threads                                                  []ReviewThread
	Closes                                                   []IssueRef // issues it closes when merged
	ViewerPermission                                         string
	AutoMergeAllowed                                         bool
}

// AutoMerge is auto-merge switched on for a pull request.
type AutoMerge struct{ By, Method string }

// Label is a label with its colour as hex (no #).
type Label struct{ Name, Color string }

// IssueRef points at an issue.
type IssueRef struct {
	Repo   string
	Number int
	Title  string
}

// ReviewThread is a thread of review comments on the diff.
type ReviewThread struct {
	ID                       string
	Resolved                 bool
	CanResolve, CanUnresolve bool
	FirstCommentID           int64 // the REST id of the thread's first comment
}

// Review is someone's latest review.
type Review struct {
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	SubmittedAt time.Time
}

// Check is one check run or commit status on the latest commit.
type Check struct {
	Name     string
	Kind     string // passed, failed or pending
	Status   string // raw: a check run's conclusion or status, or a status's state
	URL      string
	RunID    int64 // the workflow run behind a GitHub Actions check, or 0
	Workflow string
}

// FileChange is one changed file.
type FileChange struct {
	Path                 string
	Additions, Deletions int
}

const prDetailQuery = `query($owner: String!, $name: String!, $number: Int!) {
  viewer { login }
  repository(owner: $owner, name: $name) {
    mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed viewerPermission autoMergeAllowed
    pullRequest(number: $number) {
      ...pr
      id viewerCanUpdate viewerCanClose viewerCanReopen viewerCanLabel viewerCanAssign
      viewerCanEnableAutoMerge viewerCanDisableAutoMerge viewerCanUpdateBranch
      autoMergeRequest { mergeMethod enabledBy { login } }
      labels(first: 20) { nodes { name color } }
      assignees(first: 10) { nodes { login } }
      reviewThreads(first: 100) { nodes { id isResolved viewerCanResolve viewerCanUnresolve comments(first: 1) { nodes { databaseId } } } }
      closingIssuesReferences(first: 5) { nodes { number title repository { nameWithOwner } } }
      body state mergeable mergeStateStatus additions deletions changedFiles headRefOid
      reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { name } ... on Bot { login } } } }
      latestReviews(first: 20) { nodes { author { login } state submittedAt } }
      files(first: 100) { nodes { path additions deletions } }
      latest: commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 100) { nodes {
        __typename
        ... on CheckRun { name status conclusion detailsUrl checkSuite { workflowRun { databaseId workflow { name } } } }
        ... on StatusContext { context state targetUrl }
      } } } } } }
    }
  }
}
` + prFields

type prDetailNode struct {
	prNode
	Body             string `json:"body"`
	State            string `json:"state"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	ChangedFiles     int    `json:"changedFiles"`
	HeadRefOid       string `json:"headRefOid"`
	ID               string `json:"id"`
	CanUpdate        bool   `json:"viewerCanUpdate"`
	CanClose         bool   `json:"viewerCanClose"`
	CanReopen        bool   `json:"viewerCanReopen"`
	CanLabel         bool   `json:"viewerCanLabel"`
	CanAssign        bool   `json:"viewerCanAssign"`
	CanEnableAuto    bool   `json:"viewerCanEnableAutoMerge"`
	CanDisableAuto   bool   `json:"viewerCanDisableAutoMerge"`
	CanUpdateBranch  bool   `json:"viewerCanUpdateBranch"`
	AutoMergeRequest *struct {
		MergeMethod string `json:"mergeMethod"`
		EnabledBy   *struct {
			Login string `json:"login"`
		} `json:"enabledBy"`
	} `json:"autoMergeRequest"`
	LabelNodes struct {
		Nodes []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	AssigneeNodes struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
	} `json:"assignees"`
	ThreadNodes struct {
		Nodes []struct {
			ID           string `json:"id"`
			IsResolved   bool   `json:"isResolved"`
			CanResolve   bool   `json:"viewerCanResolve"`
			CanUnresolve bool   `json:"viewerCanUnresolve"`
			Comments     struct {
				Nodes []struct {
					DatabaseID int64 `json:"databaseId"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	ClosingNodes struct {
		Nodes []struct {
			Number     int    `json:"number"`
			Title      string `json:"title"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"nodes"`
	} `json:"closingIssuesReferences"`
	ReviewRequests struct {
		Nodes []struct {
			Reviewer struct {
				Login string `json:"login"`
				Name  string `json:"name"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			Author *struct {
				Login string `json:"login"`
			} `json:"author"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	FileNodes struct {
		Nodes []FileChangeNode `json:"nodes"`
	} `json:"files"`
	Latest struct {
		Nodes []struct {
			Commit struct {
				Rollup *struct {
					Contexts struct {
						Nodes []struct {
							Type       string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							DetailsURL string `json:"detailsUrl"`
							CheckSuite *struct {
								WorkflowRun *struct {
									DatabaseID int64 `json:"databaseId"`
									Workflow   struct {
										Name string `json:"name"`
									} `json:"workflow"`
								} `json:"workflowRun"`
							} `json:"checkSuite"`
							Context   string `json:"context"`
							State     string `json:"state"`
							TargetURL string `json:"targetUrl"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"latest"`
}

// FileChangeNode is a changed file as GraphQL returns it.
type FileChangeNode struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// PullRequestDetails returns everything about pull request number in repo. One GraphQL request.
func (c *Client) PullRequestDetails(ctx context.Context, repo string, number int) (PullRequestDetail, error) {
	owner, name, _ := strings.Cut(repo, "/")
	var data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Repository struct {
			MergeCommitAllowed bool          `json:"mergeCommitAllowed"`
			SquashMergeAllowed bool          `json:"squashMergeAllowed"`
			RebaseMergeAllowed bool          `json:"rebaseMergeAllowed"`
			ViewerPermission   string        `json:"viewerPermission"`
			AutoMergeAllowed   bool          `json:"autoMergeAllowed"`
			PullRequest        *prDetailNode `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := allowPartial(c.GraphQL(ctx, prDetailQuery, vars, &data)); err != nil {
		return PullRequestDetail{}, err
	}
	n := data.Repository.PullRequest
	if n == nil {
		return PullRequestDetail{}, fmt.Errorf("pull request %s#%d not found", repo, number)
	}
	d := PullRequestDetail{PullRequest: n.pullRequest(), Body: n.Body, State: n.State, Mergeable: n.Mergeable,
		MergeState: n.MergeStateStatus, Additions: n.Additions, Deletions: n.Deletions, Files: n.ChangedFiles,
		HeadSHA: n.HeadRefOid, ViewerLogin: data.Viewer.Login}
	d.ViewerOwner = strings.EqualFold(d.Author, d.ViewerLogin)
	d.ID, d.CanUpdate, d.CanClose, d.CanReopen, d.CanLabel, d.CanAssign = n.ID, n.CanUpdate, n.CanClose, n.CanReopen, n.CanLabel, n.CanAssign
	d.CanEnableAutoMerge, d.CanDisableAutoMerge, d.CanUpdateBranch = n.CanEnableAuto, n.CanDisableAuto, n.CanUpdateBranch
	d.ViewerPermission, d.AutoMergeAllowed = data.Repository.ViewerPermission, data.Repository.AutoMergeAllowed
	if r := n.AutoMergeRequest; r != nil {
		d.AutoMerge = &AutoMerge{Method: strings.ToLower(r.MergeMethod)}
		if r.EnabledBy != nil {
			d.AutoMerge.By = r.EnabledBy.Login
		}
	}
	for _, l := range n.LabelNodes.Nodes {
		d.Labels = append(d.Labels, Label{l.Name, l.Color})
	}
	for _, u := range n.AssigneeNodes.Nodes {
		d.Assignees = append(d.Assignees, u.Login)
	}
	for _, t := range n.ThreadNodes.Nodes {
		th := ReviewThread{ID: t.ID, Resolved: t.IsResolved, CanResolve: t.CanResolve, CanUnresolve: t.CanUnresolve}
		if len(t.Comments.Nodes) > 0 {
			th.FirstCommentID = t.Comments.Nodes[0].DatabaseID
		}
		d.Threads = append(d.Threads, th)
	}
	for _, c := range n.ClosingNodes.Nodes {
		d.Closes = append(d.Closes, IssueRef{c.Repository.NameWithOwner, c.Number, c.Title})
	}
	for _, r := range n.ReviewRequests.Nodes {
		if who := r.Reviewer.Login + r.Reviewer.Name; who != "" {
			d.Requested = append(d.Requested, who)
		}
	}
	for _, r := range n.LatestReviews.Nodes {
		rv := Review{State: r.State, SubmittedAt: r.SubmittedAt}
		if r.Author != nil {
			rv.Author = r.Author.Login
		}
		d.Reviews = append(d.Reviews, rv)
	}
	for _, f := range n.FileNodes.Nodes {
		d.ChangedFiles = append(d.ChangedFiles, FileChange(f))
	}
	if len(n.Latest.Nodes) > 0 && n.Latest.Nodes[0].Commit.Rollup != nil {
		for _, cx := range n.Latest.Nodes[0].Commit.Rollup.Contexts.Nodes {
			ch := Check{Name: cx.Name, URL: cx.DetailsURL, Kind: checkKind(cx.Type, cx.Status, cx.Conclusion, cx.State)}
			ch.Status = strings.ToLower(cx.Conclusion)
			if cx.Status != "COMPLETED" {
				ch.Status = strings.ToLower(cx.Status)
			}
			if cx.Type == "StatusContext" {
				ch.Name, ch.URL, ch.Status = cx.Context, cx.TargetURL, strings.ToLower(cx.State)
			}
			if cx.CheckSuite != nil && cx.CheckSuite.WorkflowRun != nil {
				ch.RunID = cx.CheckSuite.WorkflowRun.DatabaseID
				ch.Workflow = cx.CheckSuite.WorkflowRun.Workflow.Name
			}
			d.Checks = append(d.Checks, ch)
		}
	}
	for _, m := range []struct {
		ok   bool
		name string
	}{{data.Repository.SquashMergeAllowed, "squash"}, {data.Repository.MergeCommitAllowed, "merge"}, {data.Repository.RebaseMergeAllowed, "rebase"}} {
		if m.ok {
			d.MergeMethods = append(d.MergeMethods, m.name)
		}
	}
	return d, nil
}

// ReviewPullRequest submits a review: event is APPROVE, REQUEST_CHANGES or COMMENT.
func (c *Client) ReviewPullRequest(ctx context.Context, repo string, number int, event, body string) error {
	in := map[string]string{"event": event}
	if body != "" {
		in["body"] = body
	}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number), in, nil)
}

// CommentOnPullRequest adds a comment to the pull request's conversation.
func (c *Client) CommentOnPullRequest(ctx context.Context, repo string, number int, body string) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/issues/%d/comments", repo, number), map[string]string{"body": body}, nil)
}

// MergePullRequest merges with method: merge, squash or rebase.
func (c *Client) MergePullRequest(ctx context.Context, repo string, number int, method string) error {
	return c.Do(ctx, http.MethodPut, fmt.Sprintf("repos/%s/pulls/%d/merge", repo, number), map[string]string{"merge_method": method}, nil)
}
