package config

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, yaml string) *Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CALAMARES_CONFIG", p)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIconsIsOneSetting(t *testing.T) {
	for yaml, want := range map[string]string{
		"":                          "nerd",
		"icons: plain\n":            "plain",
		"icons: ascii\n":            "ascii",
		"ascii: true\n":             "ascii",
		"ascii: true\nicons: plain": "ascii",
		"icons: whatever\n":         "nerd",
	} {
		if got := load(t, yaml).Icons; got != want {
			t.Errorf("%q: icons %q, want %q", yaml, got, want)
		}
	}
}

func TestAchievementsAreOnUnlessSwitchedOff(t *testing.T) {
	for yaml, want := range map[string]bool{
		"":                    true,
		"achievements: on\n":  true,
		"achievements: off\n": false,
		"achievements: OFF\n": false,
	} {
		if got := load(t, yaml).AchievementsOn(); got != want {
			t.Errorf("%q: achievements on %v, want %v", yaml, got, want)
		}
	}
}

func TestDirIsTheFolderOfTheConfigFileAndEmptyWithoutOne(t *testing.T) {
	c := load(t, "")
	if want := filepath.Dir(os.Getenv("CALAMARES_CONFIG")); c.Dir() != want {
		t.Errorf("Dir() = %q, want %q", c.Dir(), want)
	}
	if d := Default().Dir(); d != "" {
		t.Errorf("settings never loaded from a file have no folder, got %q", d)
	}
}

func TestTheOldTypicalDurationsSettingReadsAsTheMedian(t *testing.T) {
	if got := load(t, "typicalDurations: true\n").CompareDurations; got != CompareMedian {
		t.Errorf("typicalDurations: true reads as %q, want median", got)
	}
	if got := load(t, "compareDurations: previous\n").CompareDurations; got != ComparePrevious {
		t.Errorf("compareDurations: previous reads as %q", got)
	}
	if load(t, "compareDurations: sometimes\n").Comparing() {
		t.Error("a mode it doesn't know is off")
	}
}

func TestARepoWatchMatchesItsWorkflowsAndBranches(t *testing.T) {
	ci, nightly := ".github/workflows/ci.yml", ".github/workflows/nightly.yml"
	cases := []struct {
		name         string
		w            RepoWatch
		file, branch string
		want         bool
	}{
		{"every workflow on the default branch", RepoWatch{}, ci, "main", true},
		{"another branch is left out by default", RepoWatch{}, ci, "feat/login", false},
		{"a workflow not ticked", RepoWatch{Workflows: []string{ci}}, nightly, "main", false},
		{"a ticked workflow at a ref", RepoWatch{Workflows: []string{ci}}, ci + "@refs/heads/main", "main", true},
		{"every branch", RepoWatch{AllBranches: true}, ci, "feat/login", true},
		{"a branch pattern", RepoWatch{Branches: []string{"release/*"}}, ci, "release/2.1", true},
		{"a pattern leaves out the default branch", RepoWatch{Branches: []string{"release/*"}}, ci, "main", false},
	}
	for _, c := range cases {
		if got := c.w.Matches(c.file, c.branch, "main"); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWatchedReposAreSavedAndReadBack(t *testing.T) {
	c := load(t, "theme: ember\n")
	c.SetRepoWatch(RepoWatch{Repo: "acme/webshop", Workflows: []string{".github/workflows/ci.yml"}})
	c.SetRepoWatch(RepoWatch{Repo: "acme/infra", AllBranches: true})
	c.SetRepoWatch(RepoWatch{Repo: "ACME/webshop", Branches: []string{"release/*"}})
	c.StopRepoWatch("acme/infra")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	w, ok := again.RepoWatch("acme/webshop")
	if !ok || len(again.WatchedRepos) != 1 || len(w.Branches) != 1 || len(w.Workflows) != 0 {
		t.Errorf("one watch of acme/webshop on release/* should be saved, got %+v", again.WatchedRepos)
	}
	if again.Theme != "ember" {
		t.Errorf("saving should keep the rest of the file, theme = %q", again.Theme)
	}
}
