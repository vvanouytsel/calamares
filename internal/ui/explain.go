package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// explanation says what one thing on a screen means: a chip, a column or a
// part of a panel.
type explanation struct{ term, text string }

// explained is what a screen says about itself: what it is, and what each
// thing on it means.
type explained struct {
	title string
	items []explanation
}

// explainer is a screen that can say what it shows; i opens it.
type explainer interface {
	Explain(a *App) explained
}

// columnsExplained are the columns of t that say what they hold, as shown.
func columnsExplained(t *Table) []explanation {
	var out []explanation
	for _, c := range t.Cols {
		if c.Help != "" && !c.Hidden && c.Title != "" {
			out = append(out, explanation{c.Title, c.Help})
		}
	}
	return out
}

// explainKey opens what the screen on display says about itself.
func (a *App) explainKey(v View) Binding {
	e, ok := v.(explainer)
	return Binding{Keys: []string{"i"}, Desc: "explain", Help: "explain what this screen shows: its chips, columns and panel", Off: !ok,
		Do: do(func() tea.Cmd {
			if !ok {
				return nil
			}
			a.earnSeen("curious", screenName(v))
			return openModal(&explainModal{about: e.Explain(a)})
		})}
}

// explainModal lists what a screen's chips, columns and panel mean.
type explainModal struct {
	about  explained
	offset int
}

func (m *explainModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "i", "esc", "q", "enter":
		return true, nil
	case "down", "j":
		m.offset++
	case "up", "k":
		m.offset = max(m.offset-1, 0)
	}
	return false, nil
}

func (m *explainModal) Render(a *App, w, h int) string {
	boxW := min(w-4, 100)
	termW := 0
	for _, it := range m.about.items {
		termW = max(termW, width(it.term))
	}
	termW = min(termW, 24)
	textW := max(boxW-termW-10, 20)
	var lines []string
	for _, it := range m.about.items {
		text := strings.Split(ansi.Wordwrap(it.text, textW, ""), "\n")
		lines = append(lines, sWhite.Bold(true).Render(fit(it.term, termW))+"  "+sText.Render(text[0]))
		for _, more := range text[1:] {
			lines = append(lines, strings.Repeat(" ", termW+2)+sText.Render(more))
		}
	}
	room := max(h-8, 3)
	m.offset = min(m.offset, max(len(lines)-room, 0))
	shown := lines[m.offset:min(m.offset+room, len(lines))]
	hint := "i or esc closes"
	if len(lines) > room {
		hint = "↑↓ scroll · " + hint
	}
	shown = append(shown, "", sDim.Render(hint))
	return modalBox(sCyan.Bold(true).Render(m.about.title), shown, cCyan, 60, boxW)
}

func (v *reposView) Explain(a *App) explained {
	items := []explanation{
		{"N failing on " + commonBranch(v.listed), "repos whose default branch has a workflow whose latest run failed"},
		{"N awaiting approval", "repos with a run that waits for your approval"},
	}
	items = append(items, columnsExplained(v.t)...)
	return explained{"What Repos shows", append(items,
		explanation{"panel: Default branch", "each workflow on the default branch, by name: a running one with how long it has run, the others with how their latest run went"},
		explanation{"panel: Last commits", "how the checks of each recent commit on the default branch went, oldest at the left, and the share that passed"},
		explanation{"panel: Other branches, Recent runs", "the runs on other branches that have not finished, and the last ones that did"},
		explanation{"panel: Code owners", "the rules of the repo's CODEOWNERS file: who owns which paths, read once a day"},
		explanation{"panel: Shared workflows", "the reusable workflows the repo calls, at which version, and whether a newer one is in use elsewhere"},
	)}
}

func (v *activityView) Explain(a *App) explained {
	title := "What " + tabNames[v.place] + " › " + v.viewLabel() + " shows"
	var items []explanation
	switch v.place {
	case tabActions:
		items = []explanation{
			{"Failing", "the latest run of each workflow on the default branches that failed; g groups them by why"},
			{"Running", "runs that have started and not finished, also while a later job waits for a runner"},
			{"Waiting", "runs that wait for a runner, or for your approval to deploy"},
			{"Last 7 days", "every run of the last 7 days"},
			{"…", "a count not known yet: what it counts is still being read"},
		}
	default:
		items = []explanation{
			{"Overview", "what fails and what needs an update, across all the views of Platform, and the runner usage of the week"},
			{"Shared workflows", "the reusable workflows your repos call, and how each version does"},
			{"Shared actions", "the actions your repos use, yours only until e shows everyone's"},
			{"Warnings", "the warnings of the latest runs on the default branches, deprecations first"},
			{"Scheduled", "the workflows that run on a schedule, and how their last scheduled run went"},
			{"Runners", "the self-hosted runners, what they run, and the jobs waiting for one"},
			{"Caches", "how full the Actions cache of each repo is, and how much of it only pull requests can use"},
			{"Time", "where the runner time of the last 7 days went"},
			{"…", "a count not known yet: open the view to read it"},
		}
	}
	items = append(items, explanation{"p", "which repos the counts cover: your most recently pushed ones and the pinned ones, all repos with workflows, or the ones that call yours"})
	if s := v.sub(); s != nil {
		if e, ok := s.(interface{ explain(*App) []explanation }); ok {
			items = append(items, e.explain(a)...)
		}
		return explained{title, append(items, columnsExplained(s.table())...)}
	}
	items = append(items,
		explanation{"N running", "runs of these repos that have not finished"},
		explanation{"N running long", "runs still going after more than twice their usual time and at least 10 minutes more, likely a job that hangs: their time is red, and c cancels one"},
		explanation{"N awaiting approval", "runs that wait for your approval to deploy; a opens them"},
		explanation{"N failed today", "runs that failed in the last 24 hours"},
		explanation{"N% passing", "the share of the finished runs of the last 7 days that passed"},
		explanation{"median", "the median time of the runs of the last 7 days that passed"},
		explanation{"complete for the last …", "each repo is read for its latest 10 runs, so a busy one reaches less far back: the counts hold every run since then"},
	)
	return explained{title, append(items, columnsExplained(v.t)...)}
}

// viewLabel is the name of the view on display.
func (v *activityView) viewLabel() string {
	if listsRuns(v.filter) {
		return actFilters[v.filter-viewRunning].label
	}
	return viewNames[v.filter]
}

func (v *failingView) explain(a *App) []explanation {
	return []explanation{
		{"N failing", "the failed runs listed: the latest run of each workflow on the default branch, where it failed"},
		{"N same cause", "how many repos share the cause at the top"},
		{"N via your workflows", "causes whose runs all called one of your reusable workflows: likely yours to fix"},
		{"since GitHub's incident", "causes that started while GitHub reported an incident with Actions"},
		{"? no error line", "the step failed without logging a line that reads like an error; l opens its log"},
		{"g", "list every failed run on a line of its own instead of grouped by cause"},
	}
}

func (v *sharedView) explain(a *App) []explanation {
	return []explanation{
		{"N failing versions", "versions that fail more often than the version before them, or fail in more than 1 of 4 runs"},
		{"N outdated repositories", "repositories on a version older than one other repositories already use"},
		{"a line per version", "the name of a workflow or action is on the line of its first version; the lines under it are its other versions in use"},
		{"b", "keep only the outdated versions"},
	}
}

func (v *warningsView) explain(a *App) []explanation {
	return []explanation{
		{"N warnings in N repos", "the different warnings listed, and how many repos gave them"},
		{"N from your actions", "warnings that name one of your own actions: yours to fix"},
	}
}

func (v *scheduledView) explain(a *App) []explanation {
	return []explanation{
		{"N failing", "scheduled workflows whose latest scheduled run failed"},
		{"N disabled", "scheduled workflows GitHub disabled because the repo saw no activity for 60 days; E enables one again"},
	}
}

func (v *runnersView) explain(a *App) []explanation {
	return []explanation{
		{"N busy", "runners that run a job now"},
		{"N jobs waiting", "jobs that wait for a runner"},
		{"N without a runner", "labels jobs wait for that no runner we know of has"},
		{"N offline", "runners that are registered but not connected"},
		{"ended runners", "runners that are gone, such as pods that ran one job, folded into one line of the last 24 hours"},
	}
}

func (v *timeView) explain(a *App) []explanation {
	return []explanation{
		{"N h this week", "the runner time of every job of the last 7 days"},
		{"▲ / ▼ on last week", "how that compares with the 7 days before"},
		{"g", "the time by repo instead of by workflow"},
	}
}

func (v *pullsView) Explain(a *App) explained {
	items := []explanation{
		{"N to review", "pull requests that wait for your review; d sets one aside until it changes"},
		{"N failing", "pull requests with a failing check"},
		{"N ready to merge", "approved, checks green and no conflicts"},
		{"N draft", "pull requests still in draft"},
	}
	items = append(items, columnsExplained(v.t)...)
	return explained{"What Pull requests shows", append(items,
		explanation{"panel: Next step", "what the pull request needs now, and the keys that do it"},
		explanation{"panel: Checks", "a bar of its checks, green passed, red failed, amber running"},
	)}
}

func (v *prView) Explain(a *App) explained {
	return explained{"What a pull request shows", []explanation{
		{"head", "its state, the branch it merges into which, who opened it, how long ago, and the lines it adds and removes with a bar of their share"},
		{"Conversation", "the description, reviews and comments, oldest first; e unfolds the folded parts of the selected one"},
		{"Changes", "the files it changes and the diff of the selected one; c comments on a line"},
		{"Checks", "each check of its last commit; enter opens its workflow run"},
		{"Runs", "every workflow run of its commits, newest commit first"},
		{"Commits", "its commits"},
		{"panel: To merge", "what stands between it and a merge: no conflicts, checks, a review, up to date with its base, not a draft; the bar and n of m count the ones met"},
		{"panel: a key at the right", "the key that deals with that step; a lock when your role can't"},
		{"panel: Checks", "a required check nothing reports is named, as when a job was renamed"},
	}}
}

func (v *repoView) Explain(a *App) explained {
	if s, name, _ := v.extraSection(a); s != nil {
		if e, ok := s.(explainer); ok {
			return e.Explain(a)
		}
		return explained{"What " + name + " shows", nil}
	}
	sections := []explanation{{"strip", "the sections of the repo; a dim one holds nothing, and Issues is left out when the repo has issues turned off"}}
	switch v.section {
	case sectionPulls:
		return explained{"What a repo's pull requests show", append(sections, columnsExplained(v.pullsList(a).t)...)}
	case sectionIssues:
		return explained{"What a repo's issues show", append(sections, columnsExplained(v.issuesList(a).t)...)}
	case sectionCommits:
		return explained{"What a repo's commits show", append(sections, columnsExplained(v.commitsList(a).t)...)}
	}
	items := append(sections,
		explanation{"Workflows", "each workflow: the dot is how its latest run went, the bar and the number how often it passes"},
		explanation{"Health", "the latest runs of the repo: how many of the finished ones passed (canceled ones are not counted), a block per run, oldest at the left, and how long they take from start to end, waits for approval included"},
		explanation{"fails on", "the branches its failed runs ran on"},
	)
	if p, _ := v.pane(a); p != nil {
		items = append(items, columnsExplained(p.t)...)
	}
	return explained{"What a repo's Actions show", append(items,
		explanation{"preview", "the run under the cursor: how it went, where it comes from and its jobs"},
	)}
}

func (v *branchesView) Explain(a *App) explained {
	return explained{"What Branches shows", append([]explanation{
		{"N merged, safe to delete", "branches whose pull request was merged; X deletes one"},
		{"N stale", "branches without a commit for 90 days"},
	}, columnsExplained(v.t)...)}
}

func (v *releasesView) Explain(a *App) explained {
	return explained{"What Releases shows", columnsExplained(v.t)}
}

func (v *environmentsView) Explain(a *App) explained {
	return explained{"What Environments shows", columnsExplained(v.t)}
}

func (v *secretsView) Explain(a *App) explained {
	return explained{"What Secrets shows", append([]explanation{
		{"names only", "GitHub never shows the value of a secret; only admins may list them"},
	}, columnsExplained(v.t)...)}
}

func (v *commitsView) Explain(a *App) explained {
	return explained{"What Commits shows", columnsExplained(v.t)}
}

func (v *watchView) Explain(a *App) explained {
	return explained{"What Watching › " + watchViews[v.view] + " shows", append([]explanation{
		{"Runs", "the runs you watch with w: a card when one starts, passes or fails, and a desktop notification while the terminal is out of focus"},
		{"Pull requests", "the pull requests you watch with w: a card when one is reviewed, commented on or pushed to, its checks pass or fail, it gets a conflict, or it is merged or closed"},
		{"Repos", "the repos you watch with W: their new runs of the workflows and branches you picked show up under Runs by themselves, and only a failure sends a notification; with Activity, a card for new commits on the default branch, pull requests opened and merged, and releases, and a notification for a merge or a release"},
		{"N running, passed, failed", "how the watched runs are doing"},
	}, columnsExplained(v.list())...)}
}

func (v *issuesView) Explain(a *App) explained {
	return explained{"What Issues shows", append([]explanation{
		{"N bug", "issues with a label that says bug"},
		{"N wait for you", "issues where someone asked you something and you have not answered; d sets one aside until someone writes again"},
		{"N have a PR", "issues a pull request says it closes"},
	}, columnsExplained(v.t)...)}
}

func (v *statusView) Explain(a *App) explained {
	return explained{"What GitHub status shows", append([]explanation{
		{"parts", "how each part of GitHub that Calamares shows or runs on is doing, read from githubstatus.com every two minutes"},
		{"Open incidents", "incidents GitHub has not resolved yet"},
		{"Upcoming maintenance", "maintenance GitHub has planned for those parts"},
		{"Last 7 days", "the incidents of the last week"},
		{"panel: Affected runs", "the runs that failed while the incident went on, and the ones that waited over 10 minutes for a runner"},
	}, columnsExplained(v.t)...)}
}

func (v *runView) Explain(a *App) explained {
	return explained{"What a run shows", []explanation{
		{"head", "how the run went, its workflow and number, the commit it ran for, its branch and commit, who started it and how, the attempt, how long it took and how long ago it started"},
		{"Jobs", "its jobs and their steps; space folds them, and the cursor starts on the step that failed"},
		{"durations", "how long each job and step took; t compares them with the median of the last 8 successful runs or with the previous successful one"},
		{"Why it failed", "the failing step, its first telling error line and the annotations of the job"},
		{"queued", "how long the run and each job waited for a runner"},
		{"=", "what changed since the workflow last passed on the branch: commits, reusable workflows and the runner image"},
	}}
}
