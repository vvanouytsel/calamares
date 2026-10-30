package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// maxDeploymentReads caps the environments whose last deployment is read:
// two requests each.
const maxDeploymentReads = 15

// envDeploy is an environment with its last deployment and the run that made it.
type envDeploy struct {
	env    gh.Environment
	deploy *gh.Deployment
	run    *gh.Run
}

type environmentsMsg struct {
	id   int
	list []envDeploy
	err  error
}

func (m environmentsMsg) viewID() int { return m.id }

func readEnvironments(a *App, repo string) ([]envDeploy, error) {
	ctx := context.Background()
	envs, err := a.client.Environments(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]envDeploy, len(envs))
	for i, e := range envs {
		out[i].env = e
		if i >= maxDeploymentReads {
			continue
		}
		d, ok, err := a.client.LastDeployment(ctx, repo, e.Name)
		if err != nil || !ok {
			continue
		}
		out[i].deploy = &d
		if id := d.RunID(); id != 0 {
			if r, err := a.client.GetRun(ctx, repo, id); err == nil {
				out[i].run = &r
			}
		}
	}
	return out, nil
}

// environmentsView is the Environments section of a repo: each environment
// with its protection rules and what was last deployed to it.
type environmentsView struct {
	id   int
	repo string
	t    *Table
	list []envDeploy
	loadState
}

func newEnvironmentsView(a *App, repo string) *environmentsView {
	v := &environmentsView{id: a.newID(), repo: repo}
	v.t = newTable(
		Column{Title: "ENVIRONMENT", Max: 30, Flex: true},
		Column{Title: "PROTECTION", Max: 40, Flex: true, Drop: 2, Help: "what must happen before a job may deploy to it: reviewers, a wait timer or branch rules"},
		Column{Title: "LAST DEPLOYMENT", Help: "how the last deployment to it went"},
		Column{Title: "BRANCH OR TAG", Max: 26, Flex: true, Drop: 1, Help: "the branch, tag or commit that was deployed"},
		Column{Title: "WHO", Max: 18, Flex: true, Drop: 3},
		Column{Title: "WHEN", Right: true, Min: 3},
	)
	v.t.Empty = "Loading environments…"
	return v
}

func (v *environmentsView) ID() int                  { return v.id }
func (v *environmentsView) Crumb() string            { return "environments" }
func (v *environmentsView) Init(a *App) tea.Cmd      { return nil }
func (v *environmentsView) Updated(a *App) time.Time { return v.at }
func (v *environmentsView) Loading(a *App) bool      { return v.busy }

func (v *environmentsView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(time.Minute) || !v.begin() {
		return nil
	}
	id, repo := v.id, v.repo
	return func() tea.Msg {
		list, err := readEnvironments(a, repo)
		return environmentsMsg{id, list, err}
	}
}

func (v *environmentsView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case environmentsMsg:
		v.end(msg.err)
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the environments of "+v.repo+": "+msg.err.Error())
		}
		v.list = msg.list
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

// protection sums up an environment's rules: who must approve, how long it
// waits, which branches may deploy.
func protection(e gh.Environment) []string {
	var out []string
	for _, r := range e.ProtectionRules {
		switch r.Type {
		case "required_reviewers":
			out = append(out, "approval by "+strings.Join(r.ReviewerNames(), ", "))
		case "wait_timer":
			out = append(out, fmt.Sprintf("waits %dm", r.WaitTimer))
		}
	}
	if p := e.BranchPolicy; p != nil {
		switch {
		case p.ProtectedBranches:
			out = append(out, "protected branches only")
		case p.CustomBranchPolicies:
			out = append(out, "chosen branches only")
		}
	}
	return out
}

func deployKind(d *gh.Deployment) (Kind, string) {
	if d == nil || d.Status == nil {
		return KNone, "–"
	}
	switch d.Status.State {
	case "success":
		return KOK, "deployed"
	case "failure", "error":
		return KFail, d.Status.State
	case "in_progress", "queued", "pending":
		return KRun, strings.ReplaceAll(d.Status.State, "_", " ")
	case "inactive":
		return KSkip, "replaced"
	}
	return KNone, d.Status.State
}

func (v *environmentsView) rebuild(a *App) {
	now := time.Now()
	var rows []Row
	for _, e := range v.list {
		k, state := deployKind(e.deploy)
		prot := strings.Join(protection(e.env), " · ")
		if prot == "" {
			prot = "none"
		}
		ref, who, when := Cell{}, Cell{}, Cell{}
		if d := e.deploy; d != nil {
			ref = iconText("branch", d.Ref, false)
			who = iconText("user", d.Creator.Login, false)
			at := d.CreatedAt
			if d.Status != nil && !d.Status.CreatedAt.IsZero() {
				at = d.Status.CreatedAt
			}
			when = steelCell(age(at, now), false)
		}
		protCell := steelCell(prot, prot == "none")
		rows = append(rows, Row{Key: e.env.Name, Kind: k, Cells: []Cell{
			iconText("rocket", e.env.Name, false), protCell, statusCell(k, a.frame, state), ref, who, when}})
	}
	v.t.Empty = "This repo has no environments."
	if v.busy && len(rows) == 0 {
		v.t.Empty = "Loading environments…"
	}
	v.t.SetRows(rows)
}

func (v *environmentsView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *environmentsView) FilterValue() string          { return v.t.Filter() }
func (v *environmentsView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *environmentsView) sync(a *App)                  { v.rebuild(a) }

func (v *environmentsView) selected() (envDeploy, bool) {
	r := v.t.Selected()
	if r == nil {
		return envDeploy{}, false
	}
	for _, e := range v.list {
		if e.env.Name == r.Key {
			return e, true
		}
	}
	return envDeploy{}, false
}

func (v *environmentsView) Keys(a *App) []Binding {
	var bs []Binding
	if e, ok := v.selected(); ok {
		if e.run != nil {
			run := *e.run
			bs = append(bs, Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "run", Help: "the run that made the last deployment",
				Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, run)) })})
		}
		bs = append(bs, Binding{Keys: []string{"o"}, Desc: "browser", Help: "open the environment in the browser",
			Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(v.repo + "/deployments/" + e.env.Name)) })})
	}
	return grouped("ENVIRONMENTS", append(bs, filterHint, v.t.moveKeys())...)
}

func (v *environmentsView) Render(a *App, w, h int) string {
	failed, unprotected := 0, 0
	for _, e := range v.list {
		if k, _ := deployKind(e.deploy); k == KFail {
			failed++
		}
		if len(protection(e.env)) == 0 {
			unprotected++
		}
	}
	var chips []string
	if failed > 0 {
		chips = append(chips, statusChip(KFail, 0, fmt.Sprintf("%d %s failed", failed, plural("deployment", failed))))
	}
	if unprotected > 0 {
		chips = append(chips, sDim.Render(fmt.Sprintf("%d without protection", unprotected)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("rocket", "Environments", "", v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

func (v *environmentsView) panel(a *App, w, h int) []string {
	e, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "No environment selected.")
	}
	now := time.Now()
	p := newPanel(w)
	p.add(icoIn("rocket", cCyan) + sWhite.Bold(true).Render(e.env.Name))
	p.label("Protection rules")
	rules := protection(e.env)
	if len(rules) == 0 {
		p.add(sDim.Render("None: any workflow on any branch deploys at once."))
	}
	for _, r := range rules {
		p.add(sText.Render(r))
	}
	p.label("Last deployment")
	d := e.deploy
	if d == nil {
		p.add(sDim.Render("Nothing was deployed here yet."))
		return p.render(h)
	}
	k, state := deployKind(d)
	p.add(statusChip(k, a.frame, state) + sDim.Render(" · ") + sText.Render(age(d.CreatedAt, now)+" ago"))
	p.add(fact("branch", d.Ref) + sDim.Render(" @ ") + fg(cRef).Render(shortRef(d.SHA)))
	p.add(fact("user", d.Creator.Login))
	if e.run != nil {
		p.add(fact("workflow", fmt.Sprintf("%s #%d", e.run.Name, e.run.RunNumber)) + sDim.Render("  ") + keycap("enter") + sText.Render(" open"))
	}
	return p.render(h)
}

// settingRow is one secret or variable and where it is defined.
type settingRow struct {
	name     string
	variable bool
	from     string // "repo", "org" or the environment's name
	env      bool
	updated  time.Time
}

type settingsMsg struct {
	id   int
	s    gh.Settings
	envs []string
	err  error
}

func (m settingsMsg) viewID() int { return m.id }

// secretsView is the Secrets section of a repo: the names of the secrets and
// variables its workflows can read, and where each is defined.
type secretsView struct {
	id     int
	repo   string
	t      *Table
	rows   []settingRow
	locked *gh.Denial
	loadState
}

func newSecretsView(a *App, repo string) *secretsView {
	v := &secretsView{id: a.newID(), repo: repo}
	v.t = newTable(
		Column{Title: "NAME", Max: 44, Flex: true},
		Column{Title: "KIND", Help: "a secret, whose value nobody can read back, or a variable"},
		Column{Title: "FROM", Max: 30, Flex: true, Help: "where it is defined: the repo, the organization or an environment"},
		Column{Title: "ALSO IN", Max: 30, Flex: true, Drop: 1, Help: "other places that define the same name; for a job, an environment wins over the repo, and the repo over the organization"},
		Column{Title: "UPDATED", Right: true, Min: 3},
	)
	v.t.Empty = "Loading secrets and variables…"
	return v
}

func (v *secretsView) ID() int                  { return v.id }
func (v *secretsView) Crumb() string            { return "secrets" }
func (v *secretsView) Init(a *App) tea.Cmd      { return nil }
func (v *secretsView) Updated(a *App) time.Time { return v.at }
func (v *secretsView) Loading(a *App) bool      { return v.busy }

func (v *secretsView) Refresh(a *App, force bool) tea.Cmd {
	if !force && !v.waited(time.Minute) {
		return nil
	}
	if ok, d := a.can(v.repo, gh.CapAdmin); !ok {
		v.locked = &d
		return nil
	}
	if !v.begin() {
		return nil
	}
	id, repo := v.id, v.repo
	return func() tea.Msg {
		ctx := context.Background()
		envs, err := a.client.Environments(ctx, repo)
		if err != nil {
			return settingsMsg{id: id, err: err}
		}
		var names []string
		for _, e := range envs {
			names = append(names, e.Name)
		}
		s, err := a.client.RepoSettings(ctx, repo, names)
		return settingsMsg{id, s, names, err}
	}
}

func (v *secretsView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case settingsMsg:
		v.end(msg.err)
		v.locked = nil
		if gh.IsAccessDenied(msg.err) {
			v.locked = &gh.Denial{Reason: "only admins of " + v.repo + " may list its secrets and variables", Fix: "ask an admin of " + v.repo}
			return nil
		}
		if msg.err != nil {
			return a.flashCmd(nError, "Could not read the secrets of "+v.repo+": "+msg.err.Error())
		}
		v.rows = settingRows(msg.s, msg.envs)
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), msg.String())
		return cmd
	}
	return nil
}

// settingRows lists every secret and variable once per place it is defined:
// the environments first, as they win over the repo, which wins over the
// organization.
func settingRows(s gh.Settings, envs []string) []settingRow {
	var out []settingRow
	add := func(list []gh.Setting, variable bool, from string, env bool) {
		for _, x := range list {
			out = append(out, settingRow{name: x.Name, variable: variable, from: from, env: env, updated: x.UpdatedAt})
		}
	}
	for _, e := range envs {
		add(s.EnvSecrets[e], false, e, true)
		add(s.EnvVariables[e], true, e, true)
	}
	add(s.Secrets, false, "repo", false)
	add(s.Variables, true, "repo", false)
	add(s.OrgSecrets, false, "org", false)
	add(s.OrgVariables, true, "org", false)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].variable != out[j].variable {
			return !out[i].variable
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// alsoIn are the other places a secret or variable of the same name is
// defined, of the same kind.
func (v *secretsView) alsoIn(r settingRow) []string {
	var out []string
	for _, o := range v.rows {
		if o.name == r.name && o.variable == r.variable && o.from != r.from {
			out = append(out, o.from)
		}
	}
	return out
}

func (v *secretsView) rebuild(a *App) {
	now := time.Now()
	var rows []Row
	for _, r := range v.rows {
		kind := "secret"
		if r.variable {
			kind = "variable"
		}
		from := iconText("repo", r.from, false)
		if r.env {
			from = iconText("rocket", r.from, false)
		}
		also := strings.Join(v.alsoIn(r), ", ")
		updated := Cell{}
		if !r.updated.IsZero() {
			updated = steelCell(age(r.updated, now), false)
		}
		rows = append(rows, Row{Key: kind + "\x00" + r.from + "\x00" + r.name, Kind: KOK, Cells: []Cell{
			iconText("key", r.name, false), steelCell(kind, false), from, {Text: also, Styled: sWait.Render(also)}, updated}})
	}
	v.t.Empty = "No secrets or variables."
	switch {
	case v.locked != nil:
		v.t.Empty = strings.TrimSpace(icon("lock")) + " " + v.locked.Reason + " · " + v.locked.Fix
	case v.busy && len(rows) == 0:
		v.t.Empty = "Loading secrets and variables…"
	}
	v.t.SetRows(rows)
}

func (v *secretsView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *secretsView) FilterValue() string          { return v.t.Filter() }
func (v *secretsView) FilterInfo(a *App) filterInfo { return v.t.Info() }
func (v *secretsView) sync(a *App)                  { v.rebuild(a) }

func (v *secretsView) Keys(a *App) []Binding {
	return grouped("SECRETS", Binding{Keys: []string{"o"}, Desc: "browser", Help: "open the repo's secrets in the browser",
		Do: do(func() tea.Cmd { return a.openURL(a.client.WebURL(v.repo + "/settings/secrets/actions")) })},
		filterHint, v.t.moveKeys())
}

func (v *secretsView) Render(a *App, w, h int) string {
	secrets, vars, shadowed := 0, 0, 0
	for _, r := range v.rows {
		if r.variable {
			vars++
		} else {
			secrets++
		}
		if len(v.alsoIn(r)) > 0 && r.from != "org" {
			shadowed++
		}
	}
	var chips []string
	if len(v.rows) > 0 {
		chips = append(chips, sText.Render(fmt.Sprintf("%d %s · %d %s", secrets, plural("secret", secrets), vars, plural("variable", vars))))
	}
	if shadowed > 0 {
		chips = append(chips, statusChip(KWait, 0, fmt.Sprintf("%d defined twice", shadowed)))
	}
	return listWithPanel(a, w, h,
		[]string{screenTitle("key", "Secrets and variables", "names only", v.t.Len(), chips, w), " " + a.filterLine(v.t.Filter(), v.FilterInfo(a))},
		func(lw, lh int) string { return v.t.Render(lw, lh) },
		func(pw, ph int) []string { return v.panel(a, pw, ph) })
}

func (v *secretsView) selected() (settingRow, bool) {
	r := v.t.Selected()
	if r == nil {
		return settingRow{}, false
	}
	for _, x := range v.rows {
		kind := "secret"
		if x.variable {
			kind = "variable"
		}
		if kind+"\x00"+x.from+"\x00"+x.name == r.Key {
			return x, true
		}
	}
	return settingRow{}, false
}

func (v *secretsView) panel(a *App, w, h int) []string {
	r, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "Nothing selected.")
	}
	p := newPanel(w)
	kind := "secret"
	if r.variable {
		kind = "variable"
	}
	p.add(icoIn("key", cGold) + sWhite.Bold(true).Render(r.name))
	p.add(sDim.Render(kind+" · from ") + sText.Render(r.from))
	if also := v.alsoIn(r); len(also) > 0 {
		p.label("Duplicate definitions")
		p.add(sText.Render("Also in " + strings.Join(also, ", ") + "."))
		p.callout(cWait, "A job in an environment reads the environment's value, otherwise the repo's, then the organization's.")
	}
	return p.render(h)
}
