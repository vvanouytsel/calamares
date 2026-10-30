package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// filterInfo describes a filtered list: how many rows show out of how many,
// how many runs the saved filter hides from it, and the field names a query
// can use.
type filterInfo struct {
	shown, total int
	available    int  // rows that exist beyond the loaded ones, e.g. older runs; 0 if unknown
	hidden       int  // runs the saved filter keeps out of the list
	runs         bool // the list shows runs, so the saved filter applies
	fields       []string
	// values lists what a field can be set to, from the rows we hold; nil
	// when the screen can't say.
	values func(field string) []filterOption
}

// clearer is implemented by lists with filters besides the text query.
type clearer interface {
	ClearFilters(a *App) tea.Cmd
}

// Info returns the table's counts and the field names of its columns.
func (t *Table) Info() filterInfo {
	info := filterInfo{shown: t.Len(), total: t.Total()}
	for _, c := range t.Cols {
		if f := fieldName(c.Title); f != "" {
			info.fields = append(info.fields, f)
		}
	}
	info.values = t.columnValues
	return info
}

// fieldName turns a column title like "LAST 10 (old → new)" into
// the word a query uses for it.
func fieldName(title string) string {
	word, _, _ := strings.Cut(strings.ToLower(title), " ")
	return strings.TrimFunc(word, func(r rune) bool { return !unicode.IsLetter(r) })
}

// unknownFields returns the field names in q that match none of fields.
func unknownFields(q query, fields []string) []string {
	var out []string
	for _, t := range q {
		if t.field == "" {
			continue
		}
		known := false
		for _, f := range fields {
			if strings.HasPrefix(f, t.field) {
				known = true
				break
			}
		}
		if !known {
			out = append(out, t.field)
		}
	}
	return out
}

// filterLine says what a list is filtered by, how much it shows, and the
// keys that change it.
func (a *App) filterLine(q string, info filterInfo) string {
	var parts []string
	if q != "" {
		parts = append(parts, sCyan.Bold(true).Render("filter: ")+sWhite.Render(q))
	}
	if info.runs && a.cfg.RunFilter != "" {
		switch {
		case a.filterOff:
			parts = append(parts, sDim.Render("saved filter off"))
		case info.hidden > 0:
			parts = append(parts, sCyan.Render(fmt.Sprintf("saved filter hides %s", thousands(info.hidden))))
		default:
			parts = append(parts, sCyan.Render("saved filter on"))
		}
	}
	count := fmt.Sprintf("showing %s of %s", thousands(info.shown), thousands(info.total))
	switch {
	case info.shown == info.total && info.available > info.total:
		count = fmt.Sprintf("latest %s of %s · more load as you scroll", thousands(info.total), thousands(info.available))
	case info.shown == info.total:
		count = ""
	}
	if count != "" {
		parts = append(parts, sText.Render(count))
	}
	keys := keycap("/") + sHint.Render(" filter")
	if q != "" {
		keys = keycap("/") + sHint.Render(" edit  ") + keycap("x") + sHint.Render(" clear")
	}
	if info.runs && a.cfg.RunFilter != "" {
		keys += "  " + keycap("F") + sHint.Render(" saved filter")
	}
	if len(parts) == 0 {
		return keys
	}
	return strings.Join(parts, sDim.Render("  ·  ")) + "   " + keys
}

// queryHelp is the line under the filter box: the syntax, or what's wrong.
func queryHelp(q string, fields []string, me string) string {
	if bad := unknownFields(parseQuery(q, me), fields); len(bad) > 0 {
		return sRed.Render(fmt.Sprintf("No column called “%s” here. Try: %s", bad[0], strings.Join(fields, ", ")))
	}
	help := sDim.Render("words match any column · ")
	if slices.Contains(fields, "status") {
		help += sText.Render("status:failed") + sDim.Render(" or ") + sText.Render("status:passed") + sDim.Render(" · ")
	}
	return help + sText.Render("branch:main,dev") + sDim.Render(" either · ") +
		sText.Render("-actor:*[bot]") + sDim.Render(" not · ") + sText.Render("*") + sDim.Render(" wildcard · columns: ") +
		sText.Render(strings.Join(fields, " "))
}

// savedFilterModal edits the saved filter: what it is, how many runs it
// would hide, and whether it is on.
type savedFilterModal struct {
	input textinput.Model
	on    bool
}

func (a *App) openSavedFilter() tea.Cmd {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "empty: show every run"
	in.TextStyle = sWhite
	in.Cursor.Style = sCyan
	in.SetValue(a.cfg.RunFilter)
	in.CursorEnd()
	in.Focus()
	a.modal = &savedFilterModal{input: in, on: !a.filterOff}
	return nil
}

func (m *savedFilterModal) Update(a *App, msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, nil
	case "tab":
		m.on = !m.on
		return false, nil
	case "enter":
		q := strings.TrimSpace(m.input.Value())
		if bad := unknownFields(parseQuery(q, a.me), runFields); len(bad) > 0 {
			return false, nil
		}
		cmd := a.saveRunFilter(q)
		a.filterOff = !m.on
		return true, cmd
	}
	m.input, _ = m.input.Update(msg)
	return false, nil
}

func (m *savedFilterModal) Render(a *App, w, h int) string {
	q := strings.TrimSpace(m.input.Value())
	total, hidden := a.hiddenBy(q)
	state := button("on", m.on, false) + " " + button("off", !m.on, false)
	effect := sDim.Render("Shows every run.")
	if q != "" {
		effect = sText.Render(fmt.Sprintf("Hides %s of the %s recent runs Calamares knows about.", thousands(hidden), thousands(total)))
	}
	if bad := unknownFields(parseQuery(q, a.me), runFields); len(bad) > 0 {
		effect = sRed.Render(fmt.Sprintf("No field called “%s”. Fields: %s", bad[0], strings.Join(runFields, ", ")))
	}
	lines := []string{
		sDim.Render("Hides runs on every screen, also next time you start Calamares."),
		"",
		sCyan.Render("› ") + m.input.View(),
		"",
		effect,
		"",
		sText.Render("Examples  ") + sWhite.Render("-branch:dependabot/*,renovate/*") + sDim.Render(" hide bot branches"),
		"          " + sWhite.Render("-actor:*[bot]") + sDim.Render(" hide runs started by bots"),
		"          " + sWhite.Render(`workflow:"Build and release"`) + sDim.Render(" only this workflow"),
		"",
		state + sDim.Render("  tab switches   ·   enter saves   ·   esc cancels"),
	}
	return modalBox(sCyan.Bold(true).Render("Saved filter"), lines, cCyan, 72, w-4)
}

// hiddenBy counts the recent runs, and how many of them filter q would hide.
func (a *App) hiddenBy(q string) (total, hidden int) {
	pq := parseQuery(q, a.me)
	for repo, runs := range a.st.status {
		for _, r := range runs {
			total++
			if q != "" && !pq.match(a.runValues(repo, r)) {
				hidden++
			}
		}
	}
	return total, hidden
}

// filtering reports whether the saved run filter is hiding runs.
func (a *App) filtering() bool { return a.cfg.RunFilter != "" && !a.filterOff }

// visibleRuns drops the runs the saved filter hides.
func (a *App) visibleRuns(repo string, runs []gh.Run) []gh.Run {
	if !a.filtering() {
		return runs
	}
	if key := a.cfg.RunFilter + "\x00" + a.me; a.runQueryOf != key {
		a.runQuery, a.runQueryOf = parseQuery(a.cfg.RunFilter, a.me), key
	}
	var out []gh.Run
	for _, r := range runs {
		if a.runQuery.match(a.runValues(repo, r)) {
			out = append(out, r)
		}
	}
	return out
}

var runFields = []string{"repo", "workflow", "branch", "actor", "event", "status", "title"}

// runValues gives a run's texts per field, for matching a query.
func (a *App) runValues(repo string, r gh.Run) func(field string) []string {
	return func(field string) []string {
		var out []string
		for _, f := range runFields {
			if !strings.HasPrefix(f, field) {
				continue
			}
			switch f {
			case "repo":
				out = append(out, repo, a.short(repo))
			case "workflow":
				out = append(out, r.Name)
			case "branch":
				out = append(out, r.HeadBranch)
			case "actor":
				out = append(out, r.Actor.Login)
			case "event":
				out = append(out, r.Event)
			case "status":
				out = append(out, StatusKind(r.Status, r.Conclusion).Label())
			case "title":
				out = append(out, r.DisplayTitle)
			}
		}
		return out
	}
}
