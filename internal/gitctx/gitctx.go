// Package gitctx detects the GitHub repository and branch of the current folder.
package gitctx

import (
	"os/exec"
	"regexp"
	"strings"
)

// Context is the repository Calamares was started in.
type Context struct {
	Repo   string // "owner/name"
	Branch string
}

var remoteRe = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?|ssh://(?:[^@/]+@)?|[^@/]+@)([^/:]+)(?::\d+)?[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// ParseRemote extracts host and "owner/name" from a git remote URL.
func ParseRemote(url string) (host, repo string, ok bool) {
	m := remoteRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), m[2] + "/" + m[3], true
}

// Detect looks at the git checkout in the working directory. It prefers the
// "upstream" remote over "origin" only when origin isn't on host.
func Detect(host string) (Context, bool) {
	var ctx Context
	for _, remote := range []string{"origin", "upstream"} {
		out, err := exec.Command("git", "remote", "get-url", remote).Output()
		if err != nil {
			continue
		}
		h, repo, ok := ParseRemote(string(out))
		if ok && (h == host || (host == "github.com" && h == "ssh.github.com")) {
			ctx.Repo = repo
			break
		}
	}
	if ctx.Repo == "" {
		return ctx, false
	}
	if out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "HEAD" {
			ctx.Branch = b
		}
	}
	return ctx, true
}
