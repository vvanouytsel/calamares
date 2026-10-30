package gh

import (
	"compress/gzip"
	"encoding/gob"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type diskEntry struct {
	URL          string
	ETag         string
	LastModified string
	Body         []byte
	At           time.Time
	Format       int
}

// cacheFormat stamps the entries on disk. A body is kept with only the fields
// its type declared when it was read, and GitHub answers 304 while it is
// unchanged, so a field added to a cached type would stay empty until the
// data changes. Raise cacheFormat when a cached type gains a field.
const cacheFormat = 3

// maxDiskCache bounds the persisted cache; the newest entries win.
const maxDiskCache = 16 << 20

// persistable limits the disk cache to list endpoints that make start-up and
// navigation instant; logs and single objects are left out.
func persistable(url string) bool {
	return strings.HasPrefix(url, snapshotPrefix) ||
		strings.HasSuffix(strings.SplitN(url, "?", 2)[0], "/actions/workflows") ||
		strings.Contains(url, "/actions/runs?") ||
		(strings.Contains(url, "/actions/workflows/") && strings.Contains(url, "/runs?")) ||
		strings.Contains(url, "/jobs?")
}

// SaveCache writes the response cache to path so the next start shows data instantly.
func (c *Client) SaveCache(path string) error {
	c.mu.Lock()
	entries := make([]diskEntry, 0, len(c.cache))
	for u, e := range c.cache {
		if persistable(u) {
			entries = append(entries, diskEntry{URL: u, ETag: e.etag, LastModified: e.lastModified, Body: e.body, At: e.at, Format: cacheFormat})
		}
	}
	c.mu.Unlock()
	// newest first, then trim to the size budget
	slices.SortStableFunc(entries, func(x, y diskEntry) int { return y.At.Compare(x.At) })
	size := 0
	for i, e := range entries {
		size += len(e.Body)
		if size > maxDiskCache {
			entries = entries[:i]
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// A temp file of its own, so two saves at once (the periodic one and the
	// one on quit) can't write into the same file.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	zw := gzip.NewWriter(f)
	if err := gob.NewEncoder(zw).Encode(entries); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCache restores a cache written by SaveCache; entries older than a week,
// or saved in another format, are dropped.
func (c *Client) LoadCache(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	var entries []diskEntry
	if err := gob.NewDecoder(zr).Decode(&entries); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		if e.Format == cacheFormat && time.Since(e.At) < 7*24*time.Hour && (strings.HasPrefix(e.URL, c.apiBase+"/") || strings.HasPrefix(e.URL, snapshotPrefix)) {
			c.cache[e.URL] = cacheEntry{etag: e.ETag, lastModified: e.LastModified, body: e.Body, at: e.At}
		}
	}
	return nil
}

// CachePath is where the response cache lives for host.
func CachePath(host string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "calamares", strings.ReplaceAll(host, ":", "_")+".cache"), nil
}

// snapshotPrefix marks cache entries that hold a decoded result (such as a
// GraphQL answer) rather than the response to a GET.
const snapshotPrefix = "snapshot:"

// Remember keeps v under name in the response cache, so it is saved with it
// and can be shown instantly on the next start.
func (c *Client) Remember(name string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[snapshotPrefix+name] = cacheEntry{body: body, at: time.Now()}
}

// Recall decodes what Remember kept under name, and reports when it was kept.
func (c *Client) Recall(name string, v any) (time.Time, bool) {
	c.mu.Lock()
	e, ok := c.cache[snapshotPrefix+name]
	c.mu.Unlock()
	if !ok || decode(e.body, v) != nil {
		return time.Time{}, false
	}
	return e.at, true
}
