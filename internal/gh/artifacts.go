package gh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Artifact is a file a workflow run uploaded.
type Artifact struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size_in_bytes"`
	Expired   bool      `json:"expired"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// ListArtifacts lists the artifacts of a run (cached by ETag: asking again
// while nothing changed costs nothing).
func (c *Client) ListArtifacts(ctx context.Context, repo string, runID int64) ([]Artifact, error) {
	var resp struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	err := c.Get(ctx, fmt.Sprintf("repos/%s/actions/runs/%d/artifacts?per_page=100", repo, runID), &resp)
	return resp.Artifacts, err
}

// DownloadArtifact writes an artifact (a zip file) to w, telling progress as
// it goes. GitHub answers with a redirect to a download address that is valid
// for a minute; the redirect is followed, and Go drops the credentials on
// the other host.
func (c *Client) DownloadArtifact(ctx context.Context, repo string, id int64, w io.Writer, progress func(done, total int64)) error {
	return c.download(ctx, fmt.Sprintf("repos/%s/actions/artifacts/%d/zip", repo, id), "application/vnd.github+json", w, progress)
}

// download writes the body of a GET to w, following redirects.
func (c *Client) download(ctx context.Context, path, accept string, w io.Writer, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return err
	}
	c.decorate(req, accept)
	resp, tries, err := c.sendRetrying(req, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return c.fail(resp, body, tries)
	}
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
