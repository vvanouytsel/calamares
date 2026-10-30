package gh

import (
	"context"
	"net/http"
	"testing"
)

func TestTokenKindByPrefix(t *testing.T) {
	for tok, want := range map[string]TokenKind{
		"ghp_x": TokenClassic, "gho_x": TokenOAuth, "github_pat_x": TokenFineGrained,
		"ghs_x": TokenApp, "ghu_x": TokenApp, "abc": TokenUnknown,
	} {
		if got := tokenKind(tok); got != want {
			t.Errorf("%s: %v, want %v", tok, got, want)
		}
	}
}

func TestScopesAreReadFromAnyResponse(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "repo, workflow, read:org")
		_, _ = w.Write([]byte(`{"login":"sam"}`))
	})
	if _, known := c.Scopes(); known {
		t.Fatal("scopes known before any request")
	}
	if _, err := c.CurrentUser(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, known := c.Scopes()
	if !known || len(s) != 3 || s[0] != "repo" {
		t.Errorf("scopes %v %v", s, known)
	}
}

func TestRoleTable(t *testing.T) {
	c := NewWithToken("github.com", "gho_x", "test")
	cases := []struct {
		role string
		cap  Capability
		ok   bool
	}{
		{"READ", CapComment, true}, {"READ", CapTriage, false}, {"READ", CapWrite, false},
		{"TRIAGE", CapTriage, true}, {"TRIAGE", CapWrite, false}, {"WRITE", CapWrite, true},
		{"ADMIN", CapWrite, true}, {"", CapWrite, true}, {"READ", CapRead, true},
	}
	for _, tc := range cases {
		if ok, d := c.Can("a/b", tc.cap, tc.role); ok != tc.ok {
			t.Errorf("%s %v: %v (%s)", tc.role, tc.cap, ok, d.Reason)
		}
	}
	if _, d := c.Can("a/b", CapWrite, "READ"); d.Reason != "needs write access to a/b (you have read)" {
		t.Errorf("reason %q", d.Reason)
	}
}

func Test403BecomesAnAccessErrorAndIsLearned(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Accepted-GitHub-Permissions", "actions=write")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
	})
	ctx := WithCapability(context.Background(), "a/b", CapWrite)
	err := c.Do(ctx, "POST", "repos/a/b/actions/runs/1/rerun", nil, nil)
	if !IsAccessDenied(err) {
		t.Fatalf("not an access error: %v", err)
	}
	d, ok := AsDenial(err)
	if !ok || d.Reason == "" {
		t.Errorf("denial %v", d)
	}
	if ok, d := c.Can("a/b", CapWrite, "WRITE"); ok || d.Reason == "" {
		t.Error("refusal not remembered")
	}
	if c.Stats().Limited != 0 {
		t.Error("counted as rate limit")
	}
}

func TestSSOHeaderKeepsTheURL(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-GitHub-SSO", "required; url=https://github.com/orgs/acme/sso?x=1")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource protected by organization SAML enforcement"}`))
	})
	err := c.Get(WithCapability(context.Background(), "acme/x", CapRead), "repos/acme/x", nil)
	d, _ := AsDenial(err)
	if d.URL != "https://github.com/orgs/acme/sso?x=1" || len(c.SSOURLs()) != 1 {
		t.Errorf("sso %+v %v", d, c.SSOURLs())
	}
}

func TestRateLimit403IsStillARateLimit(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	err := c.Get(context.Background(), "x", nil)
	if IsAccessDenied(err) || c.Stats().Limited != 1 {
		t.Errorf("rate limit became access error: %v", err)
	}
}

func TestGraphQLForbiddenBecomesAccessError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"type":"FORBIDDEN","message":"Resource not accessible by integration"}]}`))
	})
	var out struct{}
	err := c.GraphQL(WithCapability(context.Background(), "a/b", CapWrite), "query{x}", nil, &out)
	if !IsAccessDenied(err) {
		t.Fatalf("got %v", err)
	}
	if _, ok := c.Denied("a/b", CapWrite); !ok {
		t.Error("not learned")
	}
}
