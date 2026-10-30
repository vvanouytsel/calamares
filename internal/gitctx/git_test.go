package gitctx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// checkout makes a clone of a bare remote with one commit on main.
func checkout(t *testing.T) (dir, remote string) {
	base := t.TempDir()
	remote = filepath.Join(base, "remote.git")
	dir = filepath.Join(base, "work")
	run(t, base, "init", "--bare", "-b", "main", remote)
	run(t, base, "clone", remote, dir)
	run(t, dir, "switch", "-c", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "first")
	run(t, dir, "push", "-u", "origin", "main")
	return dir, remote
}

func TestDirtyAndAheadAndPush(t *testing.T) {
	dir, _ := checkout(t)
	if d, err := Dirty(dir); err != nil || d {
		t.Fatalf("clean checkout: dirty=%v err=%v", d, err)
	}
	run(t, dir, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, _ := Dirty(dir); !d {
		t.Errorf("an untracked file should make it dirty")
	}
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "second")
	run(t, dir, "commit", "--allow-empty", "-m", "third")
	if Upstream(dir) != "" {
		t.Errorf("a new branch has no upstream")
	}
	if n, err := Ahead(dir, "main"); err != nil || n != 2 {
		t.Errorf("ahead of main = %d, %v, want 2", n, err)
	}
	subjects, _ := Subjects(dir, "main")
	if len(subjects) != 2 || subjects[0] != "second" {
		t.Errorf("subjects %v", subjects)
	}
	if _, err := Push(dir, "feature"); err != nil {
		t.Fatal(err)
	}
	if Upstream(dir) != "origin/feature" {
		t.Errorf("upstream %q", Upstream(dir))
	}
	if n, _ := Ahead(dir, "main"); n != 0 {
		t.Errorf("after pushing, ahead = %d", n)
	}
}

func TestCheckoutPRFetchesAndSwitches(t *testing.T) {
	dir, remote := checkout(t)
	// The remote has the pull request's head under refs/pull/7/head.
	run(t, dir, "switch", "-c", "elsewhere")
	run(t, dir, "commit", "--allow-empty", "-m", "pr work")
	run(t, dir, "push", "origin", "elsewhere:refs/pull/7/head")
	run(t, dir, "switch", "main")
	_ = remote
	if _, err := CheckoutPR(dir, 7, "pr-7"); err != nil {
		t.Fatal(err)
	}
	out, _ := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if out != "pr-7" {
		t.Errorf("on branch %q", out)
	}
}
