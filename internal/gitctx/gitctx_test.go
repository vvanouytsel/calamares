package gitctx

import "testing"

func TestParseRemote(t *testing.T) {
	cases := []struct{ url, host, repo string }{
		{"https://github.com/acme/Storefront.git", "github.com", "acme/Storefront"},
		{"https://github.com/acme/Storefront", "github.com", "acme/Storefront"},
		{"git@github.com:acme/payments-api.git", "github.com", "acme/payments-api"},
		{"ssh://git@github.com/acme/payments-api.git", "github.com", "acme/payments-api"},
		{"https://user@ghe.acme.io/platform/infra.git\n", "ghe.acme.io", "platform/infra"},
		{"ssh://git@ssh.github.com:443/acme/api.git", "ssh.github.com", "acme/api"},
	}
	for _, c := range cases {
		host, repo, ok := ParseRemote(c.url)
		if !ok || host != c.host || repo != c.repo {
			t.Errorf("ParseRemote(%q) = %q, %q, %v; want %q, %q", c.url, host, repo, ok, c.host, c.repo)
		}
	}
	if _, _, ok := ParseRemote("/local/path/repo"); ok {
		t.Error("local paths should not parse")
	}
}
