package gh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

var errNoSecrets = errors.New("only admins of the repo can list its secrets")

// Setting is a secret or a variable of GitHub Actions, by name. Values are
// never read.
type Setting struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Settings are the secrets and variables a repo's workflows can read: its
// own, those the organization shares with it, and those of each environment.
type Settings struct {
	Secrets, Variables       []Setting
	OrgSecrets, OrgVariables []Setting
	// EnvSecrets and EnvVariables are per environment name.
	EnvSecrets, EnvVariables map[string][]Setting
}

type secretsResp struct {
	Secrets []Setting `json:"secrets"`
}

type variablesResp struct {
	Variables []Setting `json:"variables"`
}

func (c *Client) secretList(ctx context.Context, path string) ([]Setting, error) {
	var resp secretsResp
	err := c.Get(ctx, path, &resp)
	return resp.Secrets, err
}

func (c *Client) variableList(ctx context.Context, path string) ([]Setting, error) {
	var resp variablesResp
	err := c.Get(ctx, path, &resp)
	return resp.Variables, err
}

// RepoSettings lists the names of the secrets and variables of repo and of
// the given environments. GitHub answers only admins of the repo, or tokens
// allowed to read its secrets; a refusal comes back as an access error.
func (c *Client) RepoSettings(ctx context.Context, repo string, environments []string) (Settings, error) {
	ctx = WithCapability(ctx, repo, CapAdmin)
	base := "repos/" + repo + "/actions/"
	var s Settings
	var err error
	if s.Secrets, err = c.secretList(ctx, base+"secrets?per_page=100"); err != nil {
		if IsNotFound(err) {
			// GitHub hides the secrets of a private repo from non-admins behind a 404.
			return Settings{}, &AccessError{Repo: repo, Err: errNoSecrets}
		}
		return Settings{}, err
	}
	if s.Variables, err = c.variableList(ctx, base+"variables?per_page=30"); err != nil && !IsNotFound(err) {
		return Settings{}, err
	}
	// A repo of a user has no organization to inherit from: GitHub says 404.
	if s.OrgSecrets, err = c.secretList(ctx, base+"organization-secrets?per_page=100"); err != nil && !IsNotFound(err) {
		return Settings{}, err
	}
	if s.OrgVariables, err = c.variableList(ctx, base+"organization-variables?per_page=30"); err != nil && !IsNotFound(err) {
		return Settings{}, err
	}
	s.EnvSecrets, s.EnvVariables = map[string][]Setting{}, map[string][]Setting{}
	for _, env := range environments {
		at := fmt.Sprintf("repos/%s/environments/%s/", repo, url.PathEscape(env))
		if s.EnvSecrets[env], err = c.secretList(ctx, at+"secrets?per_page=100"); err != nil && !IsNotFound(err) {
			return Settings{}, err
		}
		if s.EnvVariables[env], err = c.variableList(ctx, at+"variables?per_page=30"); err != nil && !IsNotFound(err) {
			return Settings{}, err
		}
	}
	return s, nil
}
