package gitctx

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// git runs a git command in dir ("" is the working directory) and returns its
// trimmed output; on failure the error carries what git said.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git %s: %s", args[0], lastLine(text))
	}
	return text, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Dirty reports whether the checkout has uncommitted changes.
func Dirty(dir string) (bool, error) {
	out, err := git(dir, "status", "--porcelain")
	return out != "", err
}

// CheckoutPR fetches pull request number (forks too) into a local branch and
// switches to it. It returns the last line of what git said.
func CheckoutPR(dir string, number int, branch string) (string, error) {
	if _, err := git(dir, "fetch", "origin", fmt.Sprintf("pull/%d/head:%s", number, branch)); err != nil {
		// The branch may exist from an earlier checkout: update it.
		if _, err2 := git(dir, "fetch", "origin", fmt.Sprintf("+pull/%d/head:%s", number, branch)); err2 != nil {
			return "", err
		}
	}
	out, err := git(dir, "switch", branch)
	return lastLine(out), err
}

// Upstream is the branch the current branch tracks, or "" when it tracks none.
func Upstream(dir string) string {
	out, err := git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return ""
	}
	return out
}

// Ahead counts the commits the current branch has that its upstream lacks;
// without an upstream it counts the commits since base.
func Ahead(dir, base string) (int, error) {
	rng := "@{u}..HEAD"
	if Upstream(dir) == "" {
		rng = base + "..HEAD"
	}
	out, err := git(dir, "rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// Push pushes the current branch to origin and sets it to track it.
func Push(dir, branch string) (string, error) {
	out, err := git(dir, "push", "-u", "origin", branch)
	return lastLine(out), err
}

// Subjects lists the subject lines of the commits of the current branch
// since base, oldest first.
func Subjects(dir, base string) ([]string, error) {
	out, err := git(dir, "log", "--reverse", "--format=%s", base+"..HEAD")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
