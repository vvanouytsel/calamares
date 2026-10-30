package ui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// Binding is one key of a screen: what it is called, what it does and when
// it applies. A screen lists its bindings once, in Keys; pressing a key, the
// key bar, the help screen and the read-only check all work from that list.
//
// A screen leaves a binding out of its list while the key means nothing
// there (the cursor is on no row, the other pane has the focus). The first
// binding for a key wins, so a screen puts its own before the shared ones.
type Binding struct {
	Keys  []string // as Bubble Tea names them: "enter", "ctrl+r", "R"
	Label string   // how the key is shown; Keys[0] when empty
	Desc  string   // the few words in the key bar; "" keeps it out of the bar
	Help  string   // the line on the help screen; Desc when empty
	Group string   // the heading it is listed under on the help screen
	// Off marks a key that doesn't apply right now, such as rerun while the
	// run is still going: the key bar leaves it out, and pressing it says why.
	Off bool
	// Mutates marks a key that changes something on GitHub; read-only mode
	// refuses it.
	Mutates bool
	// Need is the capability the key exercises in Repo ("" for none, or for
	// notifications): a key the token or role doesn't allow is locked.
	Need gh.Capability
	Repo string
	// Locked is a reason the screen already knows (from what GitHub says
	// you may do with this very object) why the key can't be used.
	Locked string
	Do     func(key string) tea.Cmd
}

// Hint is a key shown in the key bar.
type Hint struct{ Key, Desc string }

const readOnlyText = "read-only mode: actions are disabled"

// The headings of the help screen.
const (
	groupMove       = "MOVE"
	groupEverywhere = "EVERYWHERE"
)

func (b Binding) label() string {
	if b.Label != "" || len(b.Keys) == 0 {
		return b.Label
	}
	return b.Keys[0]
}

func (b Binding) help() string {
	if b.Help != "" {
		return b.Help
	}
	return b.Desc
}

// do wraps an action that doesn't care which of the binding's keys was pressed.
func do(fn func() tea.Cmd) func(string) tea.Cmd {
	return func(string) tea.Cmd { return fn() }
}

// press runs the first binding for key. It reports whether there was one.
func (a *App) press(bs []Binding, key string) (tea.Cmd, bool) {
	for _, b := range bs {
		if !slices.Contains(b.Keys, key) {
			continue
		}
		return a.invoke(b, key), true
	}
	return nil, false
}

// invoke runs binding b as pressing key would, read-only mode and locks
// included; the action menu runs bindings through it too.
func (a *App) invoke(b Binding, key string) tea.Cmd {
	switch {
	case b.Mutates && a.opts.ReadOnly:
		return a.flashCmd(nHint, readOnlyText)
	case b.Do == nil:
		return nil
	}
	if d, locked := a.lockOf(b); locked {
		text := lockMark() + d.Reason
		if d.Fix != "" {
			text += " · " + d.Fix
		}
		return a.flashCmd(nHint, text)
	}
	// The capability stays until the next key, as a question asked first
	// (a confirmation, a picker) starts the action later.
	a.actNeed, a.actRepo = b.Need, b.Repo
	a.earnSeen("key-master", key)
	return b.Do(key)
}

// hints are the bindings the key bar shows: the ones that apply now, in
// order, without the keys read-only mode refuses.
func (a *App) hints(bs []Binding) []Hint {
	var out []Hint
	seen := map[string]bool{}
	for _, b := range bs {
		l := b.label()
		if seen[l] {
			continue
		}
		seen[l] = true
		if b.Desc == "" || b.Off || (b.Mutates && a.opts.ReadOnly) || len(b.Keys) == 0 {
			continue
		}
		if _, locked := a.lockOf(b); locked {
			continue
		}
		out = append(out, Hint{l, b.Desc})
	}
	return out
}

// grouped lists bs under heading g on the help screen, except the bindings
// that name a heading of their own.
func grouped(g string, bs ...Binding) []Binding {
	for i := range bs {
		if bs[i].Group == "" {
			bs[i].Group = g
		}
	}
	return bs
}

// only keeps the bindings for keys; without drops them.
func only(bs []Binding, keys ...string) []Binding {
	return slices.DeleteFunc(slices.Clone(bs), func(b Binding) bool { return !overlaps(b.Keys, keys) })
}

func without(bs []Binding, keys ...string) []Binding {
	return slices.DeleteFunc(slices.Clone(bs), func(b Binding) bool { return overlaps(b.Keys, keys) })
}

func overlaps(a, b []string) bool {
	return slices.ContainsFunc(a, func(k string) bool { return slices.Contains(b, k) })
}

var moveKeys = []string{"up", "k", "down", "j", "pgup", "ctrl+b", "pgdown", "ctrl+f", "home", "g", "end", "G"}

// moveBinding is the keys that move through a list, handled by move.
func moveBinding(desc string, move func(key string) tea.Cmd) Binding {
	return Binding{Keys: moveKeys, Label: "↑↓", Desc: desc, Help: "move · g G top / bottom · pgup pgdn a page", Group: groupMove, Do: move}
}

// moveKeys moves the table's cursor.
func (t *Table) moveKeys() Binding {
	return moveBinding("", func(k string) tea.Cmd { t.handleNav(k); return nil })
}

// rangeKeys tick every row between where shift was first held and the cursor.
func (t *Table) rangeKeys() Binding {
	return Binding{Keys: []string{"shift+up", "shift+down", "K", "J", "shift+home", "shift+end"}, Label: "shift+↑↓", Desc: "select a range",
		Help: "select every row from here to where you move (J and K too; home, end: to the top or bottom)",
		Do:   func(k string) tea.Cmd { t.tickRangeKey(k); return nil }}
}

// globalKeys are the keys that work on every screen, here for screen v; they
// come before the screen's own.
func (a *App) globalKeys(v View) []Binding {
	filter := "go to a repo, PR, workflow or command"
	f, filters := v.(Filterer)
	if filters {
		filter = "filter this list (in a log: search)"
	}
	bs := []Binding{
		{Keys: []string{"/"}, Help: filter, Do: do(func() tea.Cmd { return a.openSearch(true) })},
		{Keys: []string{":", "ctrl+k"}, Label: ": ctrl+k", Help: "go to a repo, PR, workflow or command",
			Do: do(func() tea.Cmd { return a.openSearch(false) })},
		{Keys: tabDigits(), Label: "1–" + string(rune('0'+len(tabNames))), Help: strings.Join(tabNames, " · "),
			Do: func(k string) tea.Cmd { return a.setTab(int(k[0] - '1')) }},
		{Keys: []string{"<", ">"}, Help: "previous / next place", Do: func(k string) tea.Cmd {
			if k == "<" {
				return a.stepTab(-1)
			}
			return a.stepTab(1)
		}},
		{Keys: []string{"esc"}, Help: "back (clears the filter first)", Do: do(func() tea.Cmd {
			if m, ok := v.(interface{ ClearMarks() bool }); ok && m.ClearMarks() {
				return nil
			}
			if filters && f.FilterValue() != "" {
				f.SetFilter(a, "")
				return nil
			}
			return a.pop()
		})},
		{Keys: []string{"q"}, Help: "back · quit on a tab", Do: do(func() tea.Cmd {
			if len(a.stack) == 1 {
				return tea.Quit
			}
			return a.pop()
		})},
		{Keys: []string{"."}, Help: "actions: everything you can do with what is selected", Do: do(a.openActionMenu)},
		{Keys: []string{"?"}, Help: "this help", Do: do(a.toggleHelp)},
		a.explainKey(v),
		{Keys: []string{"!"}, Help: "messages: what Calamares told you", Do: do(a.openNotices)},
		{Keys: []string{"ctrl+g"}, Help: "GitHub status: incidents and what they did to your runs", Do: do(a.openStatus)},
		{Keys: []string{"ctrl+r"}, Help: "refresh this screen", Do: do(func() tea.Cmd {
			cmd := v.Refresh(a, true)
			if scope := a.refreshScope(); scope != "" {
				return tea.Batch(cmd, a.flashCmd(nInfo, "Refreshing only the "+scope))
			}
			return cmd
		})},
		{Keys: []string{"ctrl+o"}, Help: "open the run or pull request in the latest card", Do: do(a.openToastRun)},
		{Keys: []string{"ctrl+l"}, Help: "open the log of the failed run in the latest message", Do: do(a.openToastLog)},
		{Keys: []string{"ctrl+x"}, Help: "dismiss the latest message", Do: do(a.dismissToast)},
		{Keys: []string{"L"}, Help: "live updates on / off", Do: do(a.toggleLive)},
		{Keys: []string{"ctrl+s"}, Help: "freeze the screen while you select text with the mouse", Do: do(func() tea.Cmd { a.freeze(); return nil })},
		{Keys: []string{"F"}, Help: "the saved filter for runs", Do: do(a.openSavedFilter)},
		{Keys: []string{"alt+down", "alt+up"}, Label: "alt+↑↓", Help: "scroll the detail panel", Do: func(k string) tea.Cmd {
			if k == "alt+up" {
				scrollPanelBy(-3)
			} else {
				scrollPanelBy(3)
			}
			return nil
		}},
		{Keys: []string{"T"}, Help: "next color theme (:theme opens the picker)", Do: do(a.cycleTheme)},
	}
	if filters {
		bs = append(bs, Binding{Keys: []string{"x"}, Help: "clear the list's filters", Do: do(func() tea.Cmd {
			f.SetFilter(a, "")
			if c, ok := v.(clearer); ok {
				return c.ClearFilters(a)
			}
			return nil
		})})
	}
	return grouped(groupEverywhere, bs...)
}

func (a *App) toggleHelp() tea.Cmd {
	if _, ok := a.top().(*helpView); ok {
		return a.pop()
	}
	return a.push(newHelpView(a, a.top()))
}

func (a *App) toggleLive() tea.Cmd {
	a.live = !a.live
	if a.live {
		a.setFlash(nInfo, "Live updates on: this screen keeps itself up to date · L stops")
		return a.top().Refresh(a, true)
	}
	a.setFlash(nInfo, "Live updates off · ctrl+r refreshes")
	return nil
}

// viewKeys is the key that moves along a screen's strip of views: tab and ]
// go to the next one, shift+tab and [ to the previous one.
func viewKeys(desc, help, group string, step func(d int) tea.Cmd) Binding {
	return Binding{Keys: []string{"tab", "]", "shift+tab", "["}, Label: "tab", Desc: desc,
		Help: help + " (] too; shift+tab and [: previous)", Group: group,
		Do: func(k string) tea.Cmd {
			if k == "shift+tab" || k == "[" {
				return step(-1)
			}
			return step(1)
		}}
}

// lockMark starts every message about something the token may not do.
func lockMark() string {
	if s := icon("lock"); s != "" {
		return s
	}
	return "locked: "
}

// can says whether the token may exercise c in repo, from your role there
// (when the sweep or a read told it) and what GitHub has refused before.
func (a *App) can(repo string, c gh.Capability) (bool, gh.Denial) {
	role := ""
	if r, ok := a.repoByName(repo); ok {
		role = r.Permission
	}
	return a.client.Can(repo, c, role)
}

// lockOf says whether binding b is locked for this token, and why.
func (a *App) lockOf(b Binding) (gh.Denial, bool) {
	if b.Locked != "" {
		return gh.Denial{Reason: b.Locked}, true
	}
	if b.Need == gh.CapRead {
		return gh.Denial{}, false
	}
	ok, d := a.can(b.Repo, b.Need)
	return d, !ok
}

// roleChip is the small note on a repo's screens that your access there is
// limited: a lock and your role. Nothing shows with write access or more.
func (a *App) roleChip(repo string) string {
	r, ok := a.repoByName(repo)
	if !ok || r.Permission == "" {
		return ""
	}
	switch strings.ToUpper(r.Permission) {
	case "READ", "TRIAGE":
		return "  " + sDim.Render(icon("lock")+strings.ToLower(r.Permission))
	}
	return ""
}

// prioritise moves the bindings for keys to the front, in that order, and
// keeps the rest as they were: the key bar shows a screen's first keys, so a
// screen puts the ones you reach for most there.
func prioritise(bs []Binding, keys ...string) []Binding {
	var front, rest []Binding
	taken := make([]bool, len(bs))
	for _, k := range keys {
		for i, b := range bs {
			if !taken[i] && len(b.Keys) > 0 && b.Keys[0] == k {
				front = append(front, b)
				taken[i] = true
			}
		}
	}
	for i, b := range bs {
		if !taken[i] {
			rest = append(rest, b)
		}
	}
	return append(front, rest...)
}
