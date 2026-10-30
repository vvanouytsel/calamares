package ui

import (
	"strings"
)

// viewNamer is a screen with views you switch with tab; ViewName is the one
// on display, as the trail in the header names it.
type viewNamer interface {
	ViewName() string
}

// placeless is a screen that belongs to no place, such as help or the GitHub
// status: while it shows, no place is lit.
type placeless interface {
	placeless()
}

func (v *statusView) placeless()  {}
func (v *helpView) placeless()    {}
func (v *noticesView) placeless() {}
func (v *accessView) placeless()  {}

func (v *activityView) ViewName() string { return v.viewLabel() }

func (v *issuesView) ViewName() string {
	if v.repo != "" {
		return ""
	}
	return issueViews[v.view]
}

// repoSections are the names of the sections of the repo screen, in order.
var repoSections = []string{"Actions", "Pull requests", "Issues", "Commits", "Branches", "Releases", "Environments", "Secrets"}

func (v *repoView) ViewName() string { return repoSections[v.section] }

// prTabs are the names of the tabs of a pull request, in order.
var prTabs = [prTabCount]string{"Conversation", "Changes", "Checks", "Runs", "Commits"}

func (v *prView) ViewName() string { return prTabs[v.tab] }

// commitTabs are the names of the tabs of a commit, in order.
var commitTabs = [commitTabCount]string{"Runs", "Changes"}

func (v *commitView) ViewName() string { return commitTabs[v.tab] }

// crumbOf is what the trail says of a screen: its name and, for one with
// views, the view on display.
func crumbOf(v View, name string) []string {
	out := []string{}
	if name != "" {
		out = append(out, name)
	}
	if n, ok := v.(viewNamer); ok && n.ViewName() != "" {
		out = append(out, n.ViewName())
	}
	return out
}

// trail is where you are after the place: each screen opened from it, and
// the view on display of each that has views.
func (a *App) trail() []string {
	segs := crumbOf(a.stack[0], "")
	for _, v := range a.stack[1:] {
		if l, ok := v.(located); ok && !a.mentions(segs, l.where().repo) {
			segs = append(segs, a.short(l.where().repo))
		}
		segs = append(segs, crumbOf(v, v.Crumb())...)
	}
	return segs
}

// shortTrail keeps the first segment and the last two of a long trail, with
// … for what is left out between them.
func shortTrail(segs []string) []string {
	if len(segs) <= 3 {
		return segs
	}
	return append([]string{segs[0], "…"}, segs[len(segs)-2:]...)
}

// backTo says where esc takes you: back to the screen under this one, or it
// clears the filter first.
func (a *App) backTo() string {
	if f, ok := a.top().(Filterer); ok && f.FilterValue() != "" {
		return "clear filter"
	}
	if len(a.stack) < 2 {
		return "back"
	}
	prev := a.stack[len(a.stack)-2]
	name := prev.Crumb()
	if len(a.stack) == 2 {
		name = tabNames[a.tab]
	}
	return "back to " + midCut(strings.Join(crumbOf(prev, name), " › "), 32)
}
