package gh

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CacheLimit is how much the Actions caches of a repo may hold by default;
// above it GitHub evicts the caches used least recently.
const CacheLimit = 10 << 30

// Cache is one entry of a repo's Actions cache.
type Cache struct {
	ID           int64     `json:"id"`
	Ref          string    `json:"ref"` // refs/heads/main, refs/pull/61/merge
	Key          string    `json:"key"`
	Size         int64     `json:"size_in_bytes"`
	LastAccessed time.Time `json:"last_accessed_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// PullNumber is the pull request a cache was made for, or 0 when it was made
// on a branch or a tag. Only that pull request's runs can use it.
func (c Cache) PullNumber() int {
	rest, ok := strings.CutPrefix(c.Ref, "refs/pull/")
	if !ok {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(rest, "%d/", &n); err != nil {
		return 0
	}
	return n
}

// Caches is what a repo's Actions cache holds: its size and count, and its
// largest entries.
type Caches struct {
	Size    int64 // of every active cache
	Count   int
	Largest []Cache // the 100 largest, largest first
}

// RepoCaches reads the use of a repo's Actions cache and its 100 largest
// entries: two requests, cached by ETag.
func (c *Client) RepoCaches(ctx context.Context, repo string) (Caches, error) {
	var usage struct {
		Size  int64 `json:"active_caches_size_in_bytes"`
		Count int   `json:"active_caches_count"`
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/actions/cache/usage", repo), &usage); err != nil {
		return Caches{}, err
	}
	out := Caches{Size: usage.Size, Count: usage.Count}
	if usage.Count == 0 {
		return out, nil
	}
	var list struct {
		Caches []Cache `json:"actions_caches"`
	}
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/actions/caches?per_page=100&sort=size_in_bytes&direction=desc", repo), &list); err != nil {
		return out, err
	}
	out.Largest = list.Caches
	return out, nil
}

// DeleteCache deletes one entry of a repo's Actions cache.
func (c *Client) DeleteCache(ctx context.Context, repo string, id int64) error {
	return c.Do(ctx, http.MethodDelete, fmt.Sprintf("repos/%s/actions/caches/%d", repo, id), nil, nil)
}
