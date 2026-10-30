package gh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Release is a release of a repo.
type Release struct {
	ID          int64     `json:"id"`
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Target      string    `json:"target_commitish"`
	URL         string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []ReleaseAsset `json:"assets"`
}

// ReleaseAsset is a file attached to a release.
type ReleaseAsset struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Downloads int    `json:"download_count"`
}

// ListReleases lists the latest 30 releases of repo, drafts included when
// you may see them (cached by ETag).
func (c *Client) ListReleases(ctx context.Context, repo string) ([]Release, error) {
	var out []Release
	err := c.Get(ctx, fmt.Sprintf("repos/%s/releases?per_page=30", repo), &out)
	return out, err
}

// ReleaseSpec is what a new release is made from.
type ReleaseSpec struct {
	Tag, Target, Name, Body string
	Draft, Prerelease       bool
}

// CreateRelease creates a release (and its tag, if it does not exist).
func (c *Client) CreateRelease(ctx context.Context, repo string, s ReleaseSpec) (Release, error) {
	in := map[string]any{"tag_name": s.Tag, "name": s.Name, "body": s.Body, "draft": s.Draft, "prerelease": s.Prerelease}
	if s.Target != "" {
		in["target_commitish"] = s.Target
	}
	var out Release
	err := c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/releases", repo), in, &out)
	return out, err
}

// PublishRelease publishes a draft.
func (c *Client) PublishRelease(ctx context.Context, repo string, id int64) error {
	return c.Do(ctx, http.MethodPatch, fmt.Sprintf("repos/%s/releases/%d", repo, id), map[string]any{"draft": false}, nil)
}

// DeleteRelease deletes a release (its tag stays).
func (c *Client) DeleteRelease(ctx context.Context, repo string, id int64) error {
	return c.Do(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/releases/%d", repo, id), nil, nil)
}

// GenerateReleaseNotes asks GitHub to write the notes of a release, from the
// changes since previousTag ("" for all).
func (c *Client) GenerateReleaseNotes(ctx context.Context, repo, tag, target, previousTag string) (name, body string, err error) {
	in := map[string]string{"tag_name": tag}
	if target != "" {
		in["target_commitish"] = target
	}
	if previousTag != "" {
		in["previous_tag_name"] = previousTag
	}
	var out struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	err = c.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/releases/generate-notes", repo), in, &out)
	return out.Name, out.Body, err
}

// DownloadReleaseAsset writes a release asset to w.
func (c *Client) DownloadReleaseAsset(ctx context.Context, repo string, id int64, w io.Writer, progress func(done, total int64)) error {
	return c.download(ctx, fmt.Sprintf("repos/%s/releases/assets/%d", repo, id), "application/octet-stream", w, progress)
}

// Tags lists the tag names of repo, newest first as GitHub orders them.
func (c *Client) Tags(ctx context.Context, repo string) ([]string, error) {
	var raw []struct {
		Name string `json:"name"`
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/tags?per_page=100", repo), &raw); err != nil {
		return nil, err
	}
	out := make([]string, len(raw))
	for i, t := range raw {
		out[i] = t.Name
	}
	return out, nil
}

// CommitsSince counts the commits of head that base lacks.
func (c *Client) CommitsSince(ctx context.Context, repo, base, head string) (int, error) {
	var out struct {
		AheadBy int `json:"ahead_by"`
	}
	err := c.Get(ctx, fmt.Sprintf("repos/%s/compare/%s...%s?per_page=1", repo, url.PathEscape(base), url.PathEscape(head)), &out)
	return out.AheadBy, err
}

// NextVersions proposes the versions after tag: patch, minor and major.
// A tag that is not a semantic version gives nothing.
func NextVersions(tag string) (patch, minor, major string) {
	prefix := ""
	v := tag
	if strings.HasPrefix(v, "v") {
		prefix, v = "v", v[1:]
	}
	var a, b, c int
	if n, _ := fmt.Sscanf(v, "%d.%d.%d", &a, &b, &c); n != 3 {
		return "", "", ""
	}
	return fmt.Sprintf("%s%d.%d.%d", prefix, a, b, c+1), fmt.Sprintf("%s%d.%d.0", prefix, a, b+1), fmt.Sprintf("%s%d.0.0", prefix, a+1)
}
