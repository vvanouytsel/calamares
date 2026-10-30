package gh

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Commit is a commit on a branch of a repository.
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	URL     string    `json:"url"`
}

// Short is the abbreviated SHA.
func (c Commit) Short() string { return c.SHA[:min(7, len(c.SHA))] }

// Subject is the first line of the message.
func (c Commit) Subject() string {
	subject, _, _ := strings.Cut(c.Message, "\n")
	return subject
}

type commitsResp []struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
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

func (r commitsResp) commits() []Commit {
	out := make([]Commit, len(r))
	for i, x := range r {
		author := x.Commit.Author.Name
		if x.Author != nil && x.Author.Login != "" {
			author = x.Author.Login
		}
		out[i] = Commit{SHA: x.SHA, Message: x.Commit.Message, Author: author, Date: x.Commit.Author.Date, URL: x.HTMLURL}
	}
	return out
}

// CommitsPageSize is how many commits ListCommits returns per page.
const CommitsPageSize = 30

func commitsPath(repo, branch string, page int) string {
	q := url.Values{}
	q.Set("per_page", fmt.Sprint(CommitsPageSize))
	if page > 1 {
		q.Set("page", fmt.Sprint(page))
	}
	if branch != "" {
		q.Set("sha", branch)
	}
	return "repos/" + repo + "/commits?" + q.Encode()
}

// ListCommits returns one page of the commits of branch, newest first. An
// empty branch means the default branch.
func (c *Client) ListCommits(ctx context.Context, repo, branch string, page int) ([]Commit, error) {
	var resp commitsResp
	err := c.Get(ctx, commitsPath(repo, branch, page), &resp)
	return resp.commits(), err
}

// CachedCommits returns the last known first page of ListCommits, if any.
func (c *Client) CachedCommits(repo, branch string) ([]Commit, bool) {
	var resp commitsResp
	ok := c.Cached(commitsPath(repo, branch, 1), &resp)
	return resp.commits(), ok
}

// CachedCommitsAt reports when the cached first page was last confirmed.
func (c *Client) CachedCommitsAt(repo, branch string) (time.Time, bool) {
	return c.cachedAt(commitsPath(repo, branch, 1))
}

// CommitFiles returns the files a commit changes, with their patches.
func (c *Client) CommitFiles(ctx context.Context, repo, sha string) ([]FileDiff, error) {
	var resp struct {
		Files []fileJSON `json:"files"`
	}
	err := c.Get(ctx, "repos/"+repo+"/commits/"+sha, &resp)
	out := make([]FileDiff, len(resp.Files))
	for i, f := range resp.Files {
		out[i] = FileDiff{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch}
	}
	return out, err
}
