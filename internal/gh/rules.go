package gh

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

type branchRule struct {
	Type       string `json:"type"`
	Parameters struct {
		RequiredStatusChecks []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

type branchProtection struct {
	Protection *struct {
		RequiredStatusChecks *struct {
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		} `json:"required_status_checks"`
	} `json:"protection"`
}

// RequiredChecks names the checks a pull request into branch must pass:
// those of the rulesets that apply to it and those of its branch protection.
// Both are readable with read access to the repo.
func (c *Client) RequiredChecks(ctx context.Context, repo, branch string) ([]string, error) {
	var rules []branchRule
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/rules/branches/%s", repo, url.PathEscape(branch)), &rules); err != nil && !IsNotFound(err) {
		return nil, err
	}
	var out []string
	add := func(name string) {
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, r := range rules {
		if r.Type != "required_status_checks" {
			continue
		}
		for _, check := range r.Parameters.RequiredStatusChecks {
			add(check.Context)
		}
	}
	var b branchProtection
	if err := c.Get(ctx, fmt.Sprintf("repos/%s/branches/%s", repo, url.PathEscape(branch)), &b); err != nil && !IsNotFound(err) {
		return nil, err
	}
	if b.Protection != nil && b.Protection.RequiredStatusChecks != nil {
		for _, name := range b.Protection.RequiredStatusChecks.Contexts {
			add(name)
		}
		for _, check := range b.Protection.RequiredStatusChecks.Checks {
			add(check.Context)
		}
	}
	return out, nil
}

// MissingChecks lists the required checks no reported check answers to,
// in the order they are required. Names compare without regard to case.
func MissingChecks(required, reported []string) []string {
	var out []string
	for _, r := range required {
		found := false
		for _, name := range reported {
			if strings.EqualFold(r, name) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, r)
		}
	}
	return out
}
