package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// GitHub's status page is read at most every two minutes: it needs no
// token and costs nothing of the API budget.
const statusEvery = 2 * time.Minute

// statusParts are the parts of GitHub that Calamares shows or that runs
// depend on, by the status page's name, with a short name; an incident
// about anything else (Copilot, Pages, Codespaces) stays quiet.
var statusParts = []struct {
	name, short string
	runs        bool // a run can fail or wait when it is down
}{
	{"Actions", "Actions", true}, {"Git Operations", "Git", true}, {"API Requests", "API", true},
	{"Webhooks", "Webhooks", true}, {"Packages", "Packages", true}, {"Pull Requests", "PRs", false}, {"Issues", "Issues", false},
}

func statusPart(name string) (short string, runs, ok bool) {
	for _, p := range statusParts {
		if p.name == name {
			return p.short, p.runs, true
		}
	}
	return "", false, false
}

// statusData is what the store knows of GitHub's status page.
type statusData struct {
	status  gh.Status
	state   loadState
	none    bool            // the host has no status page
	open    map[string]bool // the incidents open at the last read, to tell when one ends
	next    time.Time       // when to read it again
	initial bool            // the first read is done, so new incidents are news
}

type statusMsg struct {
	status gh.Status
	ok     bool
	err    error
}

// pollStatus reads the status page when it is due.
func (a *App) pollStatus() tea.Cmd {
	d := &a.st.ghStatus
	if d.none || time.Now().Before(d.next) || !d.state.begin() {
		return nil
	}
	d.next = time.Now().Add(statusEvery)
	return func() tea.Msg {
		s, ok, err := a.client.GitHubStatus(context.Background())
		return statusMsg{s, ok, err}
	}
}

// gotStatus keeps what the status page says and tells when an incident
// starts or ends.
func (a *App) gotStatus(m statusMsg) tea.Cmd {
	d := &a.st.ghStatus
	d.state.end(m.err)
	if !m.ok {
		d.none = true
		return nil
	}
	if m.err != nil {
		return nil // the next poll tries again; the old status stays
	}
	d.status = m.status
	was := d.open
	d.open = map[string]bool{}
	var started, ended []gh.Incident
	for _, in := range relevantIncidents(m.status) {
		if in.Open() {
			d.open[in.ID] = true
			if !was[in.ID] && d.initial {
				started = append(started, in)
			}
		} else if was[in.ID] {
			ended = append(ended, in)
		}
	}
	first := !d.initial
	d.initial = true
	switch {
	case len(ended) > 0:
		a.setFlash(nDone, a.endedNotice(ended[0]))
	case len(started) > 0:
		a.setFlash(nError, "GitHub reports trouble: "+started[0].Name+" · ctrl+g")
	case first && len(d.open) > 0:
		a.setFlash(nInfo, "GitHub has an open incident · ctrl+g shows it")
	}
	return nil
}

// endedNotice says an incident is over and what failed during it.
func (a *App) endedNotice(in gh.Incident) string {
	s := incidentSubject(in) + " is operational again"
	if groups, runs := a.failedDuring(in); runs > 0 {
		s += fmt.Sprintf(" · %d %s (%d %s) failed during it · R on Failing reruns them", groups, plural("cause", groups), runs, plural("run", runs))
	}
	return s
}

// incidentSubject names what an incident is about: the first part of
// GitHub we show that it names, such as "GitHub Actions", or "GitHub".
func incidentSubject(in gh.Incident) string {
	for _, c := range in.Components {
		if _, _, ok := statusPart(c); ok {
			return "GitHub " + c
		}
	}
	return "GitHub"
}

// relevantIncidents are the incidents about a part of GitHub we show, or
// about no part in particular, open ones first, then the newest.
func relevantIncidents(s gh.Status) []gh.Incident {
	var out []gh.Incident
	for _, in := range s.Incidents {
		if relevant(in.Components, in.Name) {
			out = append(out, in)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Open() != out[j].Open() {
			return out[i].Open()
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// relevant says an incident or a maintenance is about a part of GitHub we
// show. Nearly half of GitHub's incidents name no part; those count unless
// their title names something we don't show, such as Copilot or billing.
func relevant(components []string, name string) bool {
	if len(components) == 0 {
		return !mentions(name, elsewhere)
	}
	for _, c := range components {
		if _, _, ok := statusPart(c); ok {
			return true
		}
	}
	return false
}

// elsewhere are words in an incident's title about parts of GitHub we don't
// show; runWords are words about what runs depend on.
var (
	elsewhere = []string{"copilot", "billing", "codespaces", "pages", "sponsors", "marketplace", "cca ", "npm", "discussions", "dependabot alerts"}
	runWords  = []string{"actions", "runner", "workflow", "git ", "git operations", "api", "webhook", "package", "some github services", "latency", "unavailable"}
)

func mentions(name string, words []string) bool {
	name = strings.ToLower(name) + " "
	for _, w := range words {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// affectsRuns says an incident may have made runs fail or wait.
func affectsRuns(in gh.Incident) bool {
	if len(in.Components) == 0 {
		return mentions(in.Name, runWords) && !mentions(in.Name, elsewhere)
	}
	for _, c := range in.Components {
		if _, runs, ok := statusPart(c); ok && runs {
			return true
		}
	}
	return false
}

// openIncidents are the relevant incidents GitHub hasn't resolved.
func (a *App) openIncidents() []gh.Incident {
	var out []gh.Incident
	for _, in := range relevantIncidents(a.st.ghStatus.status) {
		if in.Open() {
			out = append(out, in)
		}
	}
	return out
}

// partState is how a part of GitHub is doing, in a word and a colour.
func partState(status string) (string, lipgloss.Color) {
	switch status {
	case "operational":
		return "operational", cGreen
	case "degraded_performance":
		return "degraded", cAmber
	case "partial_outage":
		return "partly down", cAmber
	case "major_outage":
		return "down", cRed
	case "under_maintenance":
		return "maintenance", cBlue
	}
	return strings.ReplaceAll(status, "_", " "), cDim
}

// incidentColor is red for an outage or a major incident, amber otherwise.
func (a *App) incidentColor(in gh.Incident) lipgloss.Color {
	if in.Impact == "major" || in.Impact == "critical" {
		return cRed
	}
	for _, c := range a.st.ghStatus.status.Components {
		if c.Status == "major_outage" && contains(in.Components, c.Name) {
			return cRed
		}
	}
	return cAmber
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// statusPill is the header's word on an open incident: what has trouble,
// how bad and for how long; "" while all is well.
func (a *App) statusPill() string {
	open := a.openIncidents()
	if len(open) == 0 {
		return ""
	}
	in := open[0]
	what := incidentSubject(in)
	state := "has an incident"
	for _, c := range a.st.ghStatus.status.Components {
		if "GitHub "+c.Name == what && c.Status != "operational" {
			state, _ = partState(c.Status)
		}
	}
	text := fmt.Sprintf("%s %s %s · %s", icon("warning"), what, state, age(in.CreatedAt, time.Now()))
	if len(open) > 1 {
		text += fmt.Sprintf(" +%d", len(open)-1)
	}
	c := a.incidentColor(in)
	k := KRun
	if c == cRed {
		k = KFail
	}
	return roundPill(strings.TrimSpace(text), c, tint[k])
}

// incidentWindow is when an incident was going on: from its start to when
// it was resolved, or now.
func incidentWindow(in gh.Incident, now time.Time) (time.Time, time.Time) {
	end := in.Ended()
	if end.IsZero() {
		end = now
	}
	return in.CreatedAt, end
}

// incidentAt is the incident that may have made runs fail at t: one about
// a part runs depend on, going on at t, open or resolved within the last day.
func (a *App) incidentAt(t time.Time) (gh.Incident, bool) {
	now := time.Now()
	for _, in := range relevantIncidents(a.st.ghStatus.status) {
		if !affectsRuns(in) || (!in.Open() && now.Sub(in.Ended()) > 24*time.Hour) {
			continue
		}
		if from, to := incidentWindow(in, now); !t.Before(from) && !t.After(to) {
			return in, true
		}
	}
	return gh.Incident{}, false
}

// failedDuring counts the causes on the Failing view, and their runs, that
// started while in was going on.
func (a *App) failedDuring(in gh.Incident) (groups, runs int) {
	from, to := incidentWindow(in, time.Now())
	for _, g := range groupFailures(failingRuns(a, a.activityRepos()), a.st.causes) {
		if t := g.oldest(); !t.Before(from) && !t.After(to) {
			groups++
			runs += len(g.runs)
		}
	}
	return groups, runs
}

// runsDuring are the runs we hold that failed, or waited long for a runner,
// while in was going on.
func (a *App) runsDuring(in gh.Incident) (failed, slow []repoRun) {
	from, to := incidentWindow(in, time.Now())
	for _, x := range recentRuns(a, a.activityRepos()) {
		at := x.run.CreatedAt
		if at.Before(from) || at.After(to) {
			continue
		}
		switch k := StatusKind(x.run.Status, x.run.Conclusion); {
		case k == KFail:
			failed = append(failed, x)
		case !x.run.RunStartedAt.IsZero() && x.run.RunStartedAt.Sub(x.run.CreatedAt) > 10*time.Minute:
			slow = append(slow, x)
		}
	}
	return failed, slow
}

// activityRepos are the repos the Actions tab covers.
func (a *App) activityRepos() []string {
	for _, v := range a.roots {
		if act, ok := v.(*activityView); ok {
			return act.repos(a)
		}
	}
	return nil
}

// openStatus shows the GitHub status screen, or goes back from it.
func (a *App) openStatus() tea.Cmd {
	if _, ok := a.top().(*statusView); ok {
		return a.pop()
	}
	return a.push(newStatusView(a))
}
