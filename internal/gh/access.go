package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// TokenKind is what kind of token Calamares uses, told from its prefix.
type TokenKind int

const (
	TokenUnknown TokenKind = iota
	TokenClassic
	TokenOAuth
	TokenFineGrained
	TokenApp
)

func (k TokenKind) String() string {
	switch k {
	case TokenClassic:
		return "classic token"
	case TokenOAuth:
		return "OAuth token"
	case TokenFineGrained:
		return "fine-grained token"
	case TokenApp:
		return "GitHub App token"
	}
	return "token"
}

func tokenKind(token string) TokenKind {
	switch {
	case strings.HasPrefix(token, "github_pat_"):
		return TokenFineGrained
	case strings.HasPrefix(token, "ghp_"):
		return TokenClassic
	case strings.HasPrefix(token, "gho_"):
		return TokenOAuth
	case strings.HasPrefix(token, "ghs_"), strings.HasPrefix(token, "ghu_"):
		return TokenApp
	}
	return TokenUnknown
}

// Capability is something the user may want to do in a repo.
type Capability int

const (
	CapRead        Capability = iota // read the repo's issues, PRs, runs
	CapComment                       // comment, submit a review
	CapCreateIssue                   // open an issue
	CapTriage                        // labels, assignees, reviewers, close and reopen
	CapWrite                         // merge, rerun, cancel, dispatch, delete branches, releases
	CapSecurity                      // read Dependabot alerts
	CapAdmin                         // list secrets, variables and runners
)

func (c Capability) String() string {
	return [...]string{"read", "comment", "create issues", "triage", "write", "security alerts", "admin"}[c]
}

// Denial is why a capability is not available.
type Denial struct {
	Reason string // "needs write access to acme/webshop (you have read)"
	Fix    string // "ask an admin of acme/webshop"
	URL    string // a page that fixes it (the SSO authorization URL), or ""
}

// AccessError is an API error that says the token may not do this.
type AccessError struct {
	Repo   string
	Need   string // from X-Accepted-GitHub-Permissions, e.g. "pull_requests=write"
	SSOURL string // from X-GitHub-SSO
	Err    error
}

func (e *AccessError) Error() string { return e.Err.Error() }
func (e *AccessError) Unwrap() error { return e.Err }

// IsAccessDenied reports whether err says the token may not do this.
func IsAccessDenied(err error) bool {
	var ae *AccessError
	return errors.As(err, &ae)
}

// AsDenial turns an access error into the text a hint shows.
func AsDenial(err error) (Denial, bool) {
	var ae *AccessError
	if !errors.As(err, &ae) {
		return Denial{}, false
	}
	return ae.denial(), true
}

func (e *AccessError) denial() Denial {
	where := e.Repo
	if where == "" {
		where = "this"
	}
	switch {
	case e.SSOURL != "":
		return Denial{Reason: "the organisation needs this token authorized for SSO", Fix: "see :access", URL: e.SSOURL}
	case e.Need != "":
		return Denial{Reason: "GitHub refused: the token needs " + e.Need, Fix: "add it to the token"}
	}
	return Denial{Reason: "GitHub refused: no access to " + where, Fix: "ask an admin of " + where}
}

type capKey struct{}
type capVal struct {
	repo string
	cap  Capability
}

// WithCapability says which capability the requests made with ctx exercise,
// so a refusal is filed under it.
func WithCapability(ctx context.Context, repo string, c Capability) context.Context {
	return context.WithValue(ctx, capKey{}, capVal{repo, c})
}

// accessFromResponse builds an AccessError from a 403 that isn't a rate limit.
func (c *Client) accessFromResponse(resp *http.Response, err error) *AccessError {
	ae := &AccessError{Err: err, Need: strings.TrimSpace(resp.Header.Get("X-Accepted-GitHub-Permissions"))}
	if sso := resp.Header.Get("X-GitHub-SSO"); sso != "" {
		if _, after, ok := strings.Cut(sso, "url="); ok {
			ae.SSOURL = strings.TrimSpace(after)
		}
		if ae.SSOURL == "" {
			ae.SSOURL = "https://" + c.Host
		}
	}
	if resp.Request != nil {
		if cv, ok := resp.Request.Context().Value(capKey{}).(capVal); ok {
			ae.Repo = cv.repo
			c.learn(cv.repo, cv.cap, ae.denial())
		}
	}
	return ae
}

// forbiddenToAccess turns GraphQL FORBIDDEN errors into an AccessError.
func (c *Client) forbiddenToAccess(ctx context.Context, msg string) *AccessError {
	ae := &AccessError{Err: errors.New("GitHub GraphQL: " + msg)}
	if cv, ok := ctx.Value(capKey{}).(capVal); ok {
		ae.Repo = cv.repo
		c.learn(cv.repo, cv.cap, ae.denial())
	}
	return ae
}

func (c *Client) learn(repo string, cap Capability, d Denial) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denied == nil {
		c.denied = map[string]map[Capability]Denial{}
	}
	if c.denied[repo] == nil {
		c.denied[repo] = map[Capability]Denial{}
	}
	c.denied[repo][cap] = d
	if d.URL != "" {
		c.ssoURLs = append(c.ssoURLs, d.URL)
	}
}

// Denied tells what was learned this session: GitHub refused cap in repo.
func (c *Client) Denied(repo string, cap Capability) (Denial, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.denied[repo][cap]
	return d, ok
}

// AllDenied lists the refusals learned this session, by repo.
func (c *Client) AllDenied() map[string]map[Capability]Denial {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]map[Capability]Denial{}
	for r, m := range c.denied {
		out[r] = map[Capability]Denial{}
		for k, v := range m {
			out[r][k] = v
		}
	}
	return out
}

// Kind is the kind of token in use.
func (c *Client) Kind() TokenKind { return tokenKind(c.token) }

// Scopes returns the scopes of a classic or OAuth token, as the last response
// listed them; known is false until one did (fine-grained tokens never do).
func (c *Client) Scopes() (scopes []string, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.scopes), c.scopesKnown
}

func (c *Client) trackScopes(resp *http.Response) {
	vals, ok := resp.Header["X-Oauth-Scopes"]
	if !ok {
		return
	}
	var scopes []string
	for _, s := range strings.Split(strings.Join(vals, ","), ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	c.mu.Lock()
	c.scopes, c.scopesKnown = scopes, true
	c.mu.Unlock()
}

// SSOURLs lists the SSO authorization pages GitHub pointed at.
func (c *Client) SSOURLs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.ssoURLs)
}

// roleRank orders the roles GitHub knows; 0 is unknown.
func roleRank(role string) int {
	switch strings.ToUpper(role) {
	case "READ", "PULL":
		return 1
	case "TRIAGE":
		return 2
	case "WRITE", "PUSH":
		return 3
	case "MAINTAIN":
		return 4
	case "ADMIN":
		return 5
	}
	return 0
}

// RoleOf names the highest role in the permissions REST reports for a repo.
func RoleOf(admin, maintain, push, triage, pull bool) string {
	switch {
	case admin:
		return "ADMIN"
	case maintain:
		return "MAINTAIN"
	case push:
		return "WRITE"
	case triage:
		return "TRIAGE"
	case pull:
		return "READ"
	}
	return ""
}

func needRole(c Capability) (int, string) {
	switch c {
	case CapTriage:
		return 2, "triage"
	case CapWrite, CapSecurity:
		return 3, "write"
	case CapAdmin:
		return 5, "admin"
	}
	return 1, "read"
}

// Can says whether the user may exercise c in repo. role is the user's role
// there (READ, TRIAGE, WRITE, MAINTAIN, ADMIN) or "" when it isn't known; an
// unknown answer is optimistic, and GitHub's refusal teaches the rest.
func (c *Client) Can(repo string, cap Capability, role string) (bool, Denial) {
	if d, ok := c.Denied(repo, cap); ok {
		return false, d
	}
	if cap != CapRead {
		if need, name := needRole(cap); roleRank(role) > 0 && roleRank(role) < need {
			return false, Denial{
				Reason: fmt.Sprintf("needs %s access to %s (you have %s)", name, repo, strings.ToLower(role)),
				Fix:    "ask an admin of " + repo,
			}
		}
	}
	kind := c.Kind()
	if scopes, known := c.Scopes(); known && (kind == TokenClassic || kind == TokenOAuth || kind == TokenUnknown) {
		has := func(names ...string) bool {
			for _, n := range names {
				if slices.Contains(scopes, n) {
					return true
				}
			}
			return false
		}
		switch cap {
		case CapSecurity:
			if !has("security_events", "repo") {
				return false, Denial{Reason: "the token lacks the security_events scope", Fix: "gh auth refresh -s security_events"}
			}
		case CapComment, CapCreateIssue, CapTriage, CapWrite, CapAdmin:
			if !has("repo", "public_repo") {
				return false, Denial{Reason: "the token lacks the repo scope", Fix: "gh auth refresh -s repo"}
			}
		}
	}
	return true, Denial{}
}
