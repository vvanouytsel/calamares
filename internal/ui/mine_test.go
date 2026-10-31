package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestCountsSayWhenGitHubHoldsMoreThanWasRead(t *testing.T) {
	a := apiApp(t)
	a.st.prs = []gh.PullRequest{{Number: 1, Title: "fix: typo", Repo: "acme/webshop", Why: gh.WhyYours}}
	a.st.prsMore = true
	a.tab, a.stack = tabPulls, []View{a.roots[tabPulls]}
	if out := ansi.Strip(a.View()); !strings.Contains(out, "open, involving you · 1+") {
		t.Errorf("the pull requests title should count 1+:\n%s", out)
	}
	a.st.issues = gh.IssuesOfMine{Assigned: []gh.Issue{{Number: 9, Title: "Prices differ", Repo: "acme/webshop", Why: "assigned"}}, More: [3]bool{true}}
	a.tab, a.stack = tabIssues, []View{a.roots[tabIssues]}
	if out := ansi.Strip(a.View()); !strings.Contains(out, "yours across your repos · 1+") || !strings.Contains(out, "1+") {
		t.Errorf("the issues title and strip should count 1+:\n%s", out)
	}
}
