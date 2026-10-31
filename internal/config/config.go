// Package config loads and saves the user's Calamares settings.
package config

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is persisted as YAML in the OS config directory.
type Config struct {
	// Host is the GitHub host, "github.com" unless you use GitHub Enterprise Server.
	Host string `yaml:"host"`
	// Favourites are "owner/repo" names pinned to the top of the repo list.
	Favourites []string `yaml:"favourites"`
	// RefreshSeconds is how often views with running work refresh (min 2).
	RefreshSeconds int `yaml:"refreshSeconds"`
	// ASCII is the old way to say icons: ascii; it is still read, and means that.
	ASCII bool `yaml:"ascii,omitempty"`
	// Icons is "nerd" for the icons of a Nerd Font (the default when the
	// config file doesn't say), "plain" for ordinary symbols, or "ascii" for
	// plain characters only, for terminals without good Unicode.
	Icons string `yaml:"icons,omitempty"`
	// Theme is the colour theme: cockpit (the default), midnight, ember, aurora,
	// neon, slate or paper. The theme picker (:theme, or T) saves your choice here.
	Theme string `yaml:"theme,omitempty"`
	// Animations is "full" (the default), "subtle" (only the spinners and the
	// pulse of what is running) or "off".
	Animations string `yaml:"animations,omitempty"`
	// RunFilter hides the runs that don't match it on every screen, written
	// like a search, e.g. "-branch:dependabot/*,renovate/* -actor:*[bot]".
	RunFilter string `yaml:"runFilter,omitempty"`
	// CompareDurations compares each job of a run with how long it took
	// before: "median" of earlier successful runs, "previous" for the previous
	// successful run, or "" for not at all. The t key in a run cycles them.
	CompareDurations string `yaml:"compareDurations,omitempty"`
	// TypicalDurations is the setting compareDurations replaced; true reads as "median".
	TypicalDurations bool `yaml:"typicalDurations,omitempty"`
	// LastDispatch remembers the inputs last used per "owner/repo/workflow-id".
	LastDispatch map[string]map[string]string `yaml:"lastDispatch,omitempty"`
	// WatchedRepos are the repos whose new runs are watched by themselves.
	WatchedRepos []RepoWatch `yaml:"watchedRepos,omitempty"`
	// Achievements is "off" to hide the Achievements place, the level in the
	// header and the cards for what you unlock; anything else shows them.
	Achievements string `yaml:"achievements,omitempty"`

	path string
}

// AchievementsOn says the achievements are shown.
func (c *Config) AchievementsOn() bool { return !strings.EqualFold(c.Achievements, "off") }

// Dir is the folder the config file lives in, where other state is kept
// next to it; "" for settings that were never loaded from a file.
func (c *Config) Dir() string {
	if c.path == "" {
		return ""
	}
	return filepath.Dir(c.path)
}

// Default returns the built-in settings.
func Default() *Config {
	return &Config{Host: "github.com", RefreshSeconds: 3}
}

// Path returns where the config file lives on this OS:
// %AppData%\calamares on Windows, ~/Library/Application Support/calamares on
// macOS and $XDG_CONFIG_HOME (or ~/.config)/calamares on Linux.
func Path() (string, error) {
	if p := os.Getenv("CALAMARES_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "calamares", "config.yaml"), nil
}

// Load reads the config file, falling back to defaults when it doesn't exist.
func Load() (*Config, error) {
	c := Default()
	p, err := Path()
	if err != nil {
		return c, err
	}
	c.path = p
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		c.Icons = "nerd"
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return c, err
	}
	c.path = p
	if c.Host == "" {
		c.Host = "github.com"
	}
	if c.RefreshSeconds < 2 {
		c.RefreshSeconds = 2
	}
	c.NormalizeIcons()
	c.normalizeCompare()
	return c, nil
}

// The ways a run's durations can be compared, as CompareDurations says.
const (
	CompareOff      = ""
	CompareMedian   = "median"
	ComparePrevious = "previous"
)

// normalizeCompare reads the old typicalDurations: true as the median, and
// anything it doesn't know as off.
func (c *Config) normalizeCompare() {
	if c.CompareDurations == CompareOff && c.TypicalDurations {
		c.CompareDurations = CompareMedian
	}
	c.TypicalDurations = false
	switch c.CompareDurations {
	case CompareMedian, ComparePrevious:
	default:
		c.CompareDurations = CompareOff
	}
}

// Comparing says a run's durations are compared with earlier runs.
func (c *Config) Comparing() bool { return c.CompareDurations != CompareOff }

// NormalizeIcons makes Icons one of nerd, plain or ascii: an unset one is
// nerd, and the old ascii: true means ascii.
func (c *Config) NormalizeIcons() {
	switch strings.ToLower(c.Icons) {
	case "plain":
		c.Icons = "plain"
	case "ascii":
		c.Icons = "ascii"
	default:
		c.Icons = "nerd"
	}
	if c.ASCII {
		c.Icons = "ascii"
	}
	c.ASCII = c.Icons == "ascii"
}

// Save writes the settings the app itself changes (favourites, remembered
// dispatch inputs, the saved run filter, typical durations, watched repos) and keeps everything else as it is on disk, so one-off
// command-line flags like --ascii or --host never become permanent.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	disk := Default()
	if b, err := os.ReadFile(c.path); err == nil {
		if err := yaml.Unmarshal(b, disk); err != nil {
			return err
		}
	}
	disk.Favourites = c.Favourites
	disk.LastDispatch = c.LastDispatch
	disk.RunFilter = c.RunFilter
	disk.CompareDurations, disk.TypicalDurations = c.CompareDurations, false
	disk.Theme = c.Theme
	disk.WatchedRepos = c.WatchedRepos
	b, err := yaml.Marshal(disk)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o644)
}

// IsFavourite reports whether repo is pinned.
func (c *Config) IsFavourite(repo string) bool {
	return slices.ContainsFunc(c.Favourites, func(f string) bool { return strings.EqualFold(f, repo) })
}

// ToggleFavourite pins or unpins repo and reports the new state.
func (c *Config) ToggleFavourite(repo string) bool {
	if i := slices.IndexFunc(c.Favourites, func(f string) bool { return strings.EqualFold(f, repo) }); i >= 0 {
		c.Favourites = slices.Delete(c.Favourites, i, i+1)
		return false
	}
	c.Favourites = append(c.Favourites, repo)
	return true
}

// RepoWatch says which new runs of a repo are watched.
type RepoWatch struct {
	Repo string `yaml:"repo"`
	// Workflows are the paths of the workflow files watched, such as
	// .github/workflows/ci.yml; none means every workflow, also new ones.
	Workflows []string `yaml:"workflows,omitempty"`
	// Branches are the branches watched, as patterns such as release/*;
	// none means the default branch, and AllBranches every branch.
	Branches []string `yaml:"branches,omitempty"`
	// AllBranches watches the runs of every branch.
	AllBranches bool `yaml:"allBranches,omitempty"`
	// Activity is what else of the repo is told about besides its runs:
	// ActivityCommits, ActivityPulls and ActivityReleases. None means runs only.
	Activity []string `yaml:"activity,omitempty"`
}

// What a repo watch can tell about besides runs.
const (
	ActivityCommits  = "commits"  // new commits on the default branch
	ActivityPulls    = "pulls"    // pull requests opened and merged
	ActivityReleases = "releases" // releases published
)

// AllActivity is every kind of activity, in the order the picker lists them.
var AllActivity = []string{ActivityCommits, ActivityPulls, ActivityReleases}

// Tells reports whether the watch tells about kind, one of the Activity values.
func (w RepoWatch) Tells(kind string) bool { return slices.Contains(w.Activity, kind) }

// Matches reports whether a run of the workflow file on branch is
// watched; defaultBranch is the repo's default branch.
func (w RepoWatch) Matches(file, branch, defaultBranch string) bool {
	file, _, _ = strings.Cut(file, "@")
	if len(w.Workflows) > 0 && !slices.Contains(w.Workflows, file) {
		return false
	}
	switch {
	case w.AllBranches:
		return true
	case len(w.Branches) == 0:
		return branch == defaultBranch
	}
	return slices.ContainsFunc(w.Branches, func(p string) bool {
		ok, _ := path.Match(p, branch)
		return ok
	})
}

// RepoWatch returns how repo is watched, if it is.
func (c *Config) RepoWatch(repo string) (RepoWatch, bool) {
	if i := c.repoWatchIndex(repo); i >= 0 {
		return c.WatchedRepos[i], true
	}
	return RepoWatch{}, false
}

// SetRepoWatch starts watching w.Repo, or changes how it is watched.
func (c *Config) SetRepoWatch(w RepoWatch) {
	if i := c.repoWatchIndex(w.Repo); i >= 0 {
		c.WatchedRepos[i] = w
		return
	}
	c.WatchedRepos = append(c.WatchedRepos, w)
}

// StopRepoWatch stops watching repo.
func (c *Config) StopRepoWatch(repo string) {
	if i := c.repoWatchIndex(repo); i >= 0 {
		c.WatchedRepos = slices.Delete(c.WatchedRepos, i, i+1)
	}
}

func (c *Config) repoWatchIndex(repo string) int {
	return slices.IndexFunc(c.WatchedRepos, func(w RepoWatch) bool { return strings.EqualFold(w.Repo, repo) })
}
