package gh

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Comment is a comment on a pull request: one in its conversation, or one on
// a line of the diff.
type Comment struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
	URL       string
	// The fields below are set on comments on the diff only.
	Path      string
	Line      int    // the line the comment is on, or 0 when the code has changed since
	Side      string // RIGHT for the new file, LEFT for the old one
	InReplyTo int64
	DiffHunk  string
	ReviewID  int64
}

// Outdated reports whether the line a diff comment was made on has changed since.
func (c Comment) Outdated() bool { return c.Path != "" && c.Line == 0 }

// ReviewNote is a submitted review with the summary its author wrote.
type ReviewNote struct {
	ID          int64
	Author      string
	State       string // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	Body        string
	SubmittedAt time.Time
	URL         string
}

// Discussion is everything people said on a pull request.
type Discussion struct {
	Comments []Comment    // the conversation
	Reviews  []ReviewNote // submitted reviews
	Inline   []Comment    // comments on the diff, replies included
}

// FileDiff is one changed file with its patch.
type FileDiff struct {
	Path                 string
	PreviousPath         string
	Status               string // added, removed, modified, renamed, copied, changed
	Additions, Deletions int
	Patch                string // empty for binary files and very large diffs
}

type userRef struct {
	Login string `json:"login"`
}

type commentJSON struct {
	ID        int64     `json:"id"`
	User      *userRef  `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"html_url"`
	Path      string    `json:"path"`
	Line      *int      `json:"line"`
	Side      string    `json:"side"`
	InReplyTo int64     `json:"in_reply_to_id"`
	DiffHunk  string    `json:"diff_hunk"`
	ReviewID  int64     `json:"pull_request_review_id"`
}

func (j commentJSON) comment() Comment {
	c := Comment{ID: j.ID, Body: j.Body, CreatedAt: j.CreatedAt, URL: j.URL, Path: j.Path, Side: j.Side,
		InReplyTo: j.InReplyTo, DiffHunk: j.DiffHunk, ReviewID: j.ReviewID}
	if j.User != nil {
		c.Author = j.User.Login
	}
	if j.Line != nil {
		c.Line = *j.Line
	}
	return c
}

const (
	pageSize = 100
	maxPages = 30
)

// getAll reads every page of a list endpoint.
func getAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; page <= maxPages; page++ {
		var batch []T
		if err := c.Get(ctx, fmt.Sprintf("%s%sper_page=%d&page=%d", path, sep, pageSize, page), &batch); err != nil {
			return all, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			break
		}
	}
	return all, nil
}

type reviewJSON struct {
	ID          int64     `json:"id"`
	User        *userRef  `json:"user"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	URL         string    `json:"html_url"`
}

// PullRequestDiscussion returns the conversation, the reviews and the diff comments of a pull request.
func (c *Client) PullRequestDiscussion(ctx context.Context, repo string, number int) (Discussion, error) {
	var d Discussion
	issue, err := getAll[commentJSON](ctx, c, fmt.Sprintf("repos/%s/issues/%d/comments", repo, number))
	if err != nil {
		return d, err
	}
	reviews, err := getAll[reviewJSON](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number))
	if err != nil {
		return d, err
	}
	inline, err := getAll[commentJSON](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number))
	if err != nil {
		return d, err
	}
	for _, j := range issue {
		d.Comments = append(d.Comments, j.comment())
	}
	for _, r := range reviews {
		note := ReviewNote{ID: r.ID, State: r.State, Body: r.Body, SubmittedAt: r.SubmittedAt, URL: r.URL}
		if r.User != nil {
			note.Author = r.User.Login
		}
		d.Reviews = append(d.Reviews, note)
	}
	for _, j := range inline {
		d.Inline = append(d.Inline, j.comment())
	}
	return d, nil
}

type fileJSON struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

// PullRequestFiles returns the files a pull request changes, with their patches.
func (c *Client) PullRequestFiles(ctx context.Context, repo string, number int) ([]FileDiff, error) {
	files, err := getAll[fileJSON](ctx, c, fmt.Sprintf("repos/%s/pulls/%d/files", repo, number))
	out := make([]FileDiff, len(files))
	for i, f := range files {
		out[i] = FileDiff{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch}
	}
	return out, err
}

// ReplyToReviewComment answers a comment on the diff, in the same thread.
func (c *Client) ReplyToReviewComment(ctx context.Context, repo string, number int, commentID int64, body string) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls/%d/comments/%d/replies", repo, number, commentID),
		map[string]string{"body": body}, nil)
}

// CommentOnLine starts a thread on a line of the diff of the commit sha. side is RIGHT for
// the new version of the file and LEFT for the old one.
func (c *Client) CommentOnLine(ctx context.Context, repo string, number int, sha, path string, line int, side, body string) error {
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number),
		map[string]any{"body": body, "commit_id": sha, "path": path, "line": line, "side": side}, nil)
}
