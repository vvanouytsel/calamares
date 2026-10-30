package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// incidentApp has GitHub Actions degraded since 40 minutes ago, a Webhooks
// incident two days ago and a Copilot one nobody here cares about; billing
// failed during the open incident.
func incidentApp(t *testing.T) *App {
	now := time.Now()
	a := sharedApp(t)
	resolved := now.Add(-47 * time.Hour)
	a.st.ghStatus = statusData{initial: true, open: map[string]bool{"i1": true}, state: loadState{at: now.Add(-time.Minute)}, status: gh.Status{
		Components: []gh.StatusComponent{{Name: "Actions", Status: "degraded_performance"}, {Name: "Git Operations", Status: "operational"}, {Name: "Webhooks", Status: "operational"}},
		Incidents: []gh.Incident{
			{ID: "i1", Name: "Actions jobs delayed or not starting", Status: "investigating", Impact: "minor", CreatedAt: now.Add(-40 * time.Minute),
				Components: []string{"Actions", "Git Operations"}, Updates: []gh.IncidentUpdate{{Status: "investigating", Body: "We are seeing delays in job pickup.", At: now.Add(-30 * time.Minute)}}},
			{ID: "i0", Name: "Webhooks delivered late", Status: "resolved", CreatedAt: now.Add(-48 * time.Hour), ResolvedAt: &resolved, Components: []string{"Webhooks"}},
			{ID: "i9", Name: "Copilot slow", Status: "investigating", CreatedAt: now.Add(-10 * time.Minute), Components: []string{"Copilot"}},
		},
		Maintenances: []gh.Maintenance{{ID: "m1", Name: "Packages maintenance", Status: "scheduled", From: now.Add(72 * time.Hour), Until: now.Add(74 * time.Hour), Components: []string{"Packages"}}},
	}}
	failed := callingRun(3, "failure", now.Add(-20*time.Minute), buildV2)
	failed.HeadBranch = "main"
	a.st.status["acme/billing"] = []gh.Run{failed}
	for i := range a.repos {
		a.repos[i].DefaultBranch = "main"
	}
	return a
}

func TestTheHeaderShowsAnOpenIncidentAndTheBarItsKey(t *testing.T) {
	a := incidentApp(t)
	out := ansi.Strip(a.View())
	wantAll(t, out, "GitHub Actions degraded · 40m", "ctrl+g", "incident")
	if strings.Contains(out, "Copilot") {
		t.Error("an incident about Copilot should stay quiet")
	}
}

func TestNoPillWhileAllIsWell(t *testing.T) {
	a := sharedApp(t)
	if a.statusPill() != "" {
		t.Error("no incident, no pill")
	}
}

func TestTheStatusScreenListsIncidentsAndWhatTheyDidToYourRuns(t *testing.T) {
	a := incidentApp(t)
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	v, ok := a.top().(*statusView)
	if !ok {
		t.Fatalf("ctrl+g opened %T", a.top())
	}
	v.sync(a)
	v.t.SetCursor(1)
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "GitHub status", "1 open", "1 maintenance", "Open incidents", "Upcoming", "Last 7 days",
		"Actions jobs delayed", "Packages maintenance", "Webhooks delivered late", "Actions ● degraded",
		"Timeline", "We are seeing delays", "Affected runs", "1 run failed", "GitHub is investigating this incident")
	if strings.Contains(out, "Copilot") {
		t.Error("the Copilot incident is listed")
	}
}

func TestTheStatusScreenSaysWhenNoMaintenanceIsPlannedAndNothingIsOpen(t *testing.T) {
	a := incidentApp(t)
	a.st.ghStatus.status.Incidents = a.st.ghStatus.status.Incidents[1:2]
	a.st.ghStatus.status.Maintenances = nil
	v := newStatusView(a)
	v.sync(a)
	out := ansi.Strip(v.Render(a, 170, 40))
	wantAll(t, out, "Open incidents", "None.", "Upcoming maintenance", "None planned for the parts above.", "Last 7 days", "Webhooks delivered late")
	if head := lineWith(out, "INCIDENT"); strings.Index(head, "INCIDENT")-strings.Index(head, "STATE") > 24 {
		t.Errorf("the notes should not widen the STATE column: %q", head)
	}
	if r := v.t.Selected(); r == nil || r.Key != "i\x00i0" {
		t.Errorf("the cursor should skip the headings and notes to the incident, is on %v", r)
	}
}

func TestAnEndedIncidentSaysSoAndWhatFailedDuringIt(t *testing.T) {
	a := incidentApp(t)
	s := a.st.ghStatus.status
	ended := time.Now()
	s.Incidents[0].Status, s.Incidents[0].ResolvedAt = "resolved", &ended
	a.Update(statusMsg{status: s, ok: true})
	if !strings.Contains(a.flash, "GitHub Actions is operational again") {
		t.Errorf("flash = %q", a.flash)
	}
	if a.statusPill() != "" {
		t.Error("the pill should be gone once it is resolved")
	}
}

func TestANewIncidentIsNews(t *testing.T) {
	a := incidentApp(t)
	s := a.st.ghStatus.status
	s.Incidents = append(s.Incidents, gh.Incident{ID: "i2", Name: "API errors", Status: "investigating", CreatedAt: time.Now(), Components: []string{"API Requests"}})
	a.Update(statusMsg{status: s, ok: true})
	if !strings.Contains(a.flash, "GitHub reports trouble: API errors") {
		t.Errorf("flash = %q", a.flash)
	}
}

func TestFailingMarksCausesThatStartedDuringAnIncident(t *testing.T) {
	a := incidentApp(t)
	_, out := sharedTab(a, viewFailing)
	wantAll(t, out, "1 since GitHub's incident")
}

func TestAnIncidentThatNamesNoPartCountsByItsTitle(t *testing.T) {
	for name, want := range map[string][2]bool{
		"Disruption with some GitHub services":                    {true, true},
		"[Retroactive] Actions workflow run logs delayed":         {true, true},
		"Copilot Code Review is unable to complete reviews":       {false, false},
		"Disruption with GitHub Billing":                          {false, false},
		"CCA Automations are delayed when starting an Automation": {false, false},
		"Degradation in repos contents API":                       {true, true},
	} {
		in := gh.Incident{Name: name}
		if got := [2]bool{relevant(nil, name), affectsRuns(in)}; got != want {
			t.Errorf("%q: relevant, affects runs = %v, want %v", name, got, want)
		}
	}
}
