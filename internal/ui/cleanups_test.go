package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

func TestTheReposTableSaysHowMainIsDoingWithAlertsAndALockForALimitedRole(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	a.repos = []gh.Repo{
		{Name: "webshop", FullName: "acme/webshop", HasActions: true, Permission: "WRITE", DefaultBranch: "main", PushedAt: time.Now().Add(-2 * time.Minute), Health: []string{"SUCCESS"}},
		{Name: "payments", FullName: "acme/payments", HasActions: true, Permission: "READ", PushedAt: time.Now().Add(-3 * time.Hour), Health: []string{"SUCCESS"}},
	}
	a.st.status["acme/webshop"] = []gh.Run{mainRun(1, 10, "failure", time.Now()), mainRun(2, 20, "success", time.Now())}
	a.alerts["acme/webshop"] = &alertState{at: time.Now(), alerts: gh.Alerts{Total: 4, Severe: 1}}
	a.setTab(tabRepos)
	out := screen(a)
	for _, want := range []string{"DEFAULT BRANCH", "RECENT COMMITS", "ALERTS", "PUSHED", "1 failing", "1/1", " 4 ", "payments", "passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the repos table lacks %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"ISSUES", "FAILING", "PASS ", "STATE"} {
		if strings.Contains(out, gone) {
			t.Errorf("the %s column should be gone:\n%s", gone, out)
		}
	}
	if !strings.Contains(out, "payments "+strings.TrimSpace(icon("lock"))) && !strings.Contains(out, "payments ⚿") {
		t.Errorf("a repo you can only read should carry a lock:\n%s", out)
	}
}

func TestSecurityAlertsOfTheListedReposAreReadAndNeverAnError(t *testing.T) {
	withGitHub(t, map[string]string{"/graphql": `{"data":{"r0":{"vulnerabilityAlerts":{"totalCount":3,"nodes":[{"securityVulnerability":{"severity":"HIGH"}},{"securityVulnerability":{"severity":"LOW"}},{"securityVulnerability":{"severity":"LOW"}}]}}}}`})
	a := accessApp(t, "WRITE", "gho_x")
	a.setTab(tabRepos)
	a.syncTop()
	_, cmd := a.Update(tickMsg(time.Now()))
	drive(a, cmd, 2*time.Second)
	if chip := screenOf(a.alertChip("o/r")); !strings.Contains(chip, "3 Dependabot alerts") || !strings.Contains(chip, "1 critical or high") {
		t.Errorf("chip %q", chip)
	}
	// Read once: the next tick asks for nothing.
	if cmd := a.wantAlerts(); cmd != nil {
		t.Errorf("the alerts should be cached for half an hour")
	}
	if a.unreadErrors != 0 {
		t.Errorf("alerts must never be an error")
	}
}

func TestAReadOnlyRoleShowsNoAlertsAndAskedNothing(t *testing.T) {
	fake := &refusingGitHub{status: 200, body: "{}"}
	useTransport(t, fake)
	a := accessApp(t, "READ", "gho_x")
	a.setTab(tabRepos)
	a.syncTop()
	if cmd := a.wantAlerts(); cmd != nil {
		t.Errorf("read access can't see alerts: nothing should be asked")
	}
	if a.alertChip("o/r") != "" {
		t.Errorf("no chip for what you can't see")
	}
	if !strings.Contains(screenOf(a.alertLine("o/r")), "alerts") {
		t.Errorf("the repo screen should say alerts are locked, got %q", a.alertLine("o/r"))
	}
	if fake.count() != 0 {
		t.Errorf("%d requests", fake.count())
	}
	_ = http.StatusOK
}
