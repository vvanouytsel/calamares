package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// alertsCacheFor is how long a count of security alerts is kept.
const alertsCacheFor = 30 * time.Minute

// alertState is what is known of the Dependabot alerts of one repo.
type alertState struct {
	at     time.Time
	alerts gh.Alerts
	denied bool // you can't see them: never an error
	busy   bool
}

type alertsMsg struct {
	repo   string
	alerts gh.Alerts
	err    error
}

type manyAlertsMsg struct {
	repos  []string
	counts map[string]gh.Alerts
	denied map[string]bool
	err    error
}

// wantAlerts reads the alerts of the repo under the cursor, once, cached for
// half an hour; on the Repos screen those of every listed repo not known yet,
// in batches. It runs on the clock's tick, so it only asks when you stay.
func (a *App) wantAlerts() tea.Cmd {
	repo := ""
	switch v := a.top().(type) {
	case *reposView:
		if cmd := a.wantListedAlerts(v); cmd != nil {
			return cmd
		}
		repo = v.selectedRepo()
	case *repoView:
		repo = v.repo
	}
	if repo == "" || a.client.Rate().Saving(time.Now()) {
		return nil
	}
	st := a.alerts[repo]
	if st == nil {
		st = &alertState{}
		a.alerts[repo] = st
	}
	if st.busy || (!st.at.IsZero() && time.Since(st.at) < alertsCacheFor) {
		return nil
	}
	if ok, _ := a.can(repo, gh.CapSecurity); !ok {
		st.denied, st.at = true, time.Now()
		return nil
	}
	st.busy = true
	return func() tea.Msg {
		al, err := a.client.SecurityAlerts(gh.Background(context.Background()), repo)
		return alertsMsg{repo, al, err}
	}
}

func (a *App) gotAlerts(m alertsMsg) {
	st := a.alerts[m.repo]
	if st == nil {
		st = &alertState{}
		a.alerts[m.repo] = st
	}
	st.busy, st.at = false, time.Now()
	switch {
	case gh.IsAccessDenied(m.err):
		st.denied = true
	case m.err == nil:
		st.denied, st.alerts = false, m.alerts
	}
}

// alertChip says how many Dependabot alerts a repo has: red when one is
// critical or high, amber otherwise. Nothing while unknown or when there are none.
func (a *App) alertChip(repo string) string {
	st := a.alerts[repo]
	if st == nil || st.at.IsZero() || st.denied || st.alerts.Total == 0 {
		return ""
	}
	text := fmt.Sprintf("⛨ %d Dependabot alerts", st.alerts.Total)
	if st.alerts.Total == 1 {
		text = "⛨ 1 Dependabot alert"
	}
	if st.alerts.Severe > 0 {
		return sRed.Bold(true).Render(text) + sDim.Render(fmt.Sprintf(" · %d critical or high", st.alerts.Severe))
	}
	return sAmber.Render(text)
}

// alertLine is the line of the repo screen's panel about alerts: the count,
// or a dim lock when you can't see them.
func (a *App) alertLine(repo string) string {
	st := a.alerts[repo]
	switch {
	case st == nil || st.at.IsZero():
		return ""
	case st.denied:
		return sDim.Render("⛨ alerts " + strings.TrimSpace(icon("lock")))
	case st.alerts.Total == 0:
		return sDim.Render("⛨ no open alerts")
	}
	return a.alertChip(repo)
}

// alertsKey opens the repo's Dependabot alerts in the browser.
func (a *App) alertsKey(repo string) Binding {
	return Binding{Help: "Dependabot alerts in the browser",
		Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(repo + "/security/dependabot")) })}
}

// wantListedAlerts reads, in one go, the alerts of the listed repos whose
// count is unknown or older than half an hour.
func (a *App) wantListedAlerts(v *reposView) tea.Cmd {
	if a.alertsBusy || a.client.Rate().Saving(time.Now()) {
		return nil
	}
	var repos []string
	for _, r := range v.listed {
		st := a.alerts[r.FullName]
		if st != nil && (st.busy || (!st.at.IsZero() && time.Since(st.at) < alertsCacheFor)) {
			continue
		}
		if ok, _ := a.can(r.FullName, gh.CapSecurity); !ok {
			a.alerts[r.FullName] = &alertState{denied: true, at: time.Now()}
			continue
		}
		repos = append(repos, r.FullName)
	}
	if len(repos) == 0 {
		return nil
	}
	a.alertsBusy = true
	return func() tea.Msg {
		counts, denied, err := a.client.SecurityAlertsOf(gh.Background(context.Background()), repos)
		return manyAlertsMsg{repos, counts, denied, err}
	}
}

func (a *App) gotManyAlerts(m manyAlertsMsg) {
	a.alertsBusy = false
	now := time.Now()
	for _, repo := range m.repos {
		st := &alertState{at: now}
		switch {
		case m.err != nil:
			st.at = time.Time{}
			if a.alerts[repo] != nil {
				continue
			}
		case m.denied[repo]:
			st.denied = true
		default:
			st.alerts = m.counts[repo]
		}
		a.alerts[repo] = st
	}
}

// alertCell is the column of a repo's open Dependabot alerts: red with a
// critical or high one, a lock when you may not see them.
func (a *App) alertCell(repo string) Cell {
	st := a.alerts[repo]
	switch {
	case st == nil || st.at.IsZero():
		return Cell{Text: "", Styled: ""}
	case st.denied:
		lock := strings.TrimSpace(icon("lock"))
		return Cell{Text: lock, Styled: sDim.Render(lock)}
	case st.alerts.Total == 0:
		return Cell{Text: "–", Styled: sDim.Render("–")}
	}
	text := fmt.Sprint(st.alerts.Total)
	if st.alerts.Severe > 0 {
		return Cell{Text: text, Styled: sRed.Bold(true).Render(text)}
	}
	return Cell{Text: text, Styled: sAmber.Render(text)}
}
