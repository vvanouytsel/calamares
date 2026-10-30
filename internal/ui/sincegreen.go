package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// sinceGreen is what changed between a failed run and the last run of its
// workflow on the same branch that passed.
type sinceGreen struct {
	green      *gh.Run // nil when no earlier run passed
	commits    []gh.PRCommit
	ahead      int
	sameCommit bool
	failedImg  string // the runner image of the failed job
	greenImg   string // of the same job in the green run
	job        string // the job the images are of
}

type sinceGreenMsg struct {
	id   int
	data sinceGreen
	err  error
}

func (m sinceGreenMsg) viewID() int { return m.id }

// lastGreen is the newest run of the workflow on the branch of run that
// passed and started before it.
func lastGreen(ctx context.Context, a *App, repo string, run gh.Run) (*gh.Run, error) {
	runs, _, err := a.client.ListRuns(ctx, repo, gh.RunFilter{WorkflowID: run.WorkflowID, Branch: run.HeadBranch, Status: "success", PerPage: 10})
	if err != nil {
		return nil, err
	}
	for i, r := range runs {
		if r.ID != run.ID && r.CreatedAt.Before(run.CreatedAt) {
			return &runs[i], nil
		}
	}
	return nil, nil
}

// jobImage is the runner image a job of a run ran on, read from its log.
func jobImage(ctx context.Context, a *App, repo string, job gh.Job) string {
	raw, err := a.client.JobLog(ctx, repo, job.ID)
	if err != nil {
		return ""
	}
	return gh.RunnerImage(gh.ParseLog(raw))
}

func readSinceGreen(a *App, repo string, run gh.Run) (sinceGreen, error) {
	ctx := context.Background()
	var d sinceGreen
	green, err := lastGreen(ctx, a, repo, run)
	if err != nil || green == nil {
		return d, err
	}
	d.green = green
	if green.HeadSHA == run.HeadSHA {
		d.sameCommit = true
	} else if cmp, err := a.client.Compare(ctx, repo, green.HeadSHA, run.HeadSHA); err == nil {
		d.commits, d.ahead = cmp.Commits, cmp.Ahead
	}
	failedJobs, err := a.client.ListJobs(ctx, repo, run.ID)
	if err != nil {
		return d, nil
	}
	job, ok := failedJob(failedJobs)
	if !ok || job.StartedAt.IsZero() {
		return d, nil
	}
	greenJobs, err := a.client.ListJobs(ctx, repo, green.ID)
	if err != nil {
		return d, nil
	}
	for _, j := range greenJobs {
		if j.Name == job.Name {
			d.job = job.Name
			d.failedImg, d.greenImg = jobImage(ctx, a, repo, job), jobImage(ctx, a, repo, j)
			break
		}
	}
	return d, nil
}

// calledChange is a reusable workflow as two runs called it.
type calledChange struct {
	path             string // without the version
	before, after    string // the version asked for
	shaBefore, shaAt string // the commit it resolved to
}

// calledChanges pairs the reusable workflows of the green and the failed run.
func calledChanges(green, failed gh.Run) []calledChange {
	byPath := map[string]*calledChange{}
	var order []string
	add := func(rw gh.ReferencedWorkflow, after bool) {
		repo, file, version := rw.Split()
		key := strings.ToLower(repo + "/" + file)
		c, ok := byPath[key]
		if !ok {
			c = &calledChange{path: repo + "/" + file}
			byPath[key] = c
			order = append(order, key)
		}
		if after {
			c.after, c.shaAt = version, rw.SHA
		} else {
			c.before, c.shaBefore = version, rw.SHA
		}
	}
	for _, rw := range green.ReferencedWorkflows {
		add(rw, false)
	}
	for _, rw := range failed.ReferencedWorkflows {
		add(rw, true)
	}
	out := make([]calledChange, 0, len(order))
	for _, k := range order {
		out = append(out, *byPath[k])
	}
	return out
}

// changed says the workflow is another version, or the same tag at another commit.
func (c calledChange) changed() bool { return c.before != c.after || c.shaBefore != c.shaAt }

// sinceGreenView shows what changed since the run's workflow last passed on
// its branch: the commits, the reusable workflows it called and the runner image.
type sinceGreenView struct {
	id     int
	repo   string
	run    gh.Run
	data   sinceGreen
	loaded bool
	err    error
	offset int
}

func newSinceGreenView(a *App, repo string, run gh.Run) *sinceGreenView {
	return &sinceGreenView{id: a.newID(), repo: repo, run: run}
}

func (v *sinceGreenView) ID() int          { return v.id }
func (v *sinceGreenView) RepoName() string { return v.repo }
func (v *sinceGreenView) Crumb() string    { return "since it passed" }
func (v *sinceGreenView) Init(a *App) tea.Cmd {
	a.earnOnce("time-traveller")
	id, repo, run := v.id, v.repo, v.run
	return func() tea.Msg {
		d, err := readSinceGreen(a, repo, run)
		return sinceGreenMsg{id, d, err}
	}
}
func (v *sinceGreenView) Refresh(a *App, force bool) tea.Cmd {
	if !force {
		return nil
	}
	return v.Init(a)
}

func (v *sinceGreenView) Update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case sinceGreenMsg:
		v.data, v.err, v.loaded = m.data, m.err, true
	case tea.KeyMsg:
		cmd, _ := a.press(v.Keys(a), m.String())
		return cmd
	}
	return nil
}

func (v *sinceGreenView) Keys(a *App) []Binding {
	bs := []Binding{moveBinding("", func(k string) tea.Cmd {
		switch k {
		case "up", "k":
			v.offset = max(v.offset-1, 0)
		case "down", "j":
			v.offset++
		case "pgup":
			v.offset = max(v.offset-10, 0)
		case "pgdown":
			v.offset += 10
		case "home":
			v.offset = 0
		}
		return nil
	})}
	if g := v.data.green; g != nil {
		green := *g
		bs = append([]Binding{
			{Keys: []string{"g"}, Desc: "green run", Help: "open the run that passed", Do: do(func() tea.Cmd { return a.push(newRunView(a, v.repo, green)) })},
			{Keys: []string{"d"}, Desc: "diff", Help: "the changes between the two commits", Off: v.data.sameCommit,
				Do: do(func() tea.Cmd { return a.push(newCompareView(a, v.repo, green.HeadSHA, v.run.HeadSHA)) })},
		}, bs...)
	}
	return grouped("CHANGES SINCE LAST SUCCESS", bs...)
}

func (v *sinceGreenView) Render(a *App, w, h int) string {
	lines := v.lines(a, w)
	v.offset = min(v.offset, max(len(lines)-h+2, 0))
	end := min(v.offset+h-2, len(lines))
	return box(title("Since it last passed", fmt.Sprintf("%s #%d", v.run.Name, v.run.RunNumber), -1, ""), strings.Join(lines[v.offset:end], "\n"), w, h, cFrame)
}

func (v *sinceGreenView) lines(a *App, w int) []string {
	p := newPanel(w - 2)
	now := time.Now()
	switch {
	case !v.loaded:
		p.add(sDim.Render("Looking for the last run that passed…"))
		return p.all()
	case v.err != nil:
		p.add(sRed.Render("Could not read the runs: " + v.err.Error()))
		return p.all()
	case v.data.green == nil:
		p.add(sText.Render(fmt.Sprintf("No earlier run of %s on %s passed among the latest ten that did.", v.run.Name, v.run.HeadBranch)))
		return p.all()
	}
	g := *v.data.green
	p.add(statusChip(KOK, 0, fmt.Sprintf("#%d passed", g.RunNumber)) + sDim.Render(" · ") + sText.Render(age(g.CreatedAt, now)+" ago") + sDim.Render(" → ") +
		statusChip(KFail, 0, fmt.Sprintf("#%d failed", v.run.RunNumber)) + sDim.Render(" · ") + sText.Render(age(v.run.CreatedAt, now)+" ago"))

	p.label("Commits")
	switch {
	case v.data.sameCommit:
		p.callout(cWait, "The same commit passed before: the code did not change, something around it did.")
	case len(v.data.commits) == 0:
		p.add(sDim.Render("Could not compare the two commits."))
	default:
		p.add(sText.Render(fmt.Sprintf("%d %s since %s", v.data.ahead, plural("commit", v.data.ahead), shortRef(g.HeadSHA))))
		for i := len(v.data.commits) - 1; i >= 0 && len(v.data.commits)-i <= 12; i-- {
			c := v.data.commits[i]
			p.row(ico("commit")+fg(cRef).Render(shortRef(c.SHA))+" "+sText.Render(c.Message), fact("user", c.Author))
		}
	}

	p.label("Reusable workflows")
	changes := calledChanges(g, v.run)
	if len(changes) == 0 {
		p.add(sDim.Render("Neither run called one."))
	}
	for _, c := range changes {
		name := sText.Render(pathBase(c.path)) + sDim.Render("  "+c.path)
		switch {
		case !c.changed():
			p.row(name, sDim.Render(versionAt(c.after, c.shaAt)+" · same"))
		case c.before == c.after:
			p.row(name, fg(cWait).Render(c.after+" moved: "+shortRef(c.shaBefore)+" → "+shortRef(c.shaAt)))
		case c.before == "":
			p.row(name, fg(cWait).Render("new: "+versionAt(c.after, c.shaAt)))
		case c.after == "":
			p.row(name, sDim.Render("no longer called"))
		default:
			p.row(name, fg(cWait).Render(versionAt(c.before, c.shaBefore)+" → "+versionAt(c.after, c.shaAt)))
		}
	}

	p.label("Runner image")
	switch {
	case v.data.job == "":
		p.add(sDim.Render("Not known: the failed job has no counterpart in the run that passed, or never started."))
	case v.data.failedImg == "" && v.data.greenImg == "":
		p.add(sDim.Render("Self-hosted, or the logs are gone: no image in the log of " + v.data.job + "."))
	case v.data.failedImg == v.data.greenImg:
		p.row(fact("server", v.data.failedImg), sDim.Render("same"))
	default:
		p.row(fact("server", v.data.job), fg(cWait).Render(orNone(v.data.greenImg)+" → "+orNone(v.data.failedImg)))
	}
	p.label("Not compared")
	p.add(sDim.Render("The inputs of a run: GitHub's API does not return them."))
	return p.all()
}

func versionAt(version, sha string) string {
	if sha == "" || version == sha {
		return shortRef(version)
	}
	return version + " @" + shortRef(sha)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
