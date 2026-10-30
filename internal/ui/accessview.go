package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// accessView is the Access screen (:access): what your token can do, per
// repo, from the roles the sweep read and the refusals GitHub gave this session.
type accessView struct {
	id int
	t  *Table
}

func newAccessView(a *App) *accessView {
	v := &accessView{id: a.newID()}
	v.t = newTable(
		Column{Title: "REPO", Max: 34, Flex: true, MidCut: true},
		Column{Title: "ROLE", Min: 8},
		Column{Title: "BLOCKED", Max: 90, Flex: true},
	)
	v.t.Empty = "No repositories loaded yet."
	return v
}

func (v *accessView) ID() int                            { return v.id }
func (v *accessView) Init(a *App) tea.Cmd                { return nil }
func (v *accessView) Crumb() string                      { return "Access" }
func (v *accessView) Refresh(a *App, force bool) tea.Cmd { return nil }

func (v *accessView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

func (v *accessView) Keys(a *App) []Binding {
	var bs []Binding
	if urls := a.client.SSOURLs(); len(urls) > 0 {
		bs = append(bs, Binding{Keys: []string{"enter"}, Desc: "authorize SSO", Help: "open the page that authorizes the token for SSO",
			Do: do(func() tea.Cmd { return a.openURL(urls[0]) })})
	}
	return grouped("ACCESS", append(bs, v.t.moveKeys())...)
}

// cantList names what a role rules out, in the words of the keys.
func cantList(role string) []string {
	var out []string
	switch strings.ToUpper(role) {
	case "READ":
		out = []string{"merge", "rerun", "cancel", "label", "close others' issues"}
	case "TRIAGE":
		out = []string{"merge", "rerun", "cancel"}
	}
	return out
}

func (v *accessView) sync(a *App) {
	denied := a.client.AllDenied()
	seen := map[string]bool{}
	var rows []Row
	add := func(repo, role string) {
		seen[repo] = true
		can := cantList(role)
		text := "–"
		if len(can) > 0 {
			text = strings.Join(can, ", ")
		}
		styled := sDim.Render(text)
		if d := denied[repo]; len(d) > 0 {
			var caps []string
			for c, dn := range d {
				caps = append(caps, c.String()+": "+dn.Reason)
			}
			sort.Strings(caps)
			text = "refused this session: " + strings.Join(caps, "; ")
			if len(can) > 0 {
				text = strings.Join(can, ", ") + " · " + text
			}
			styled = sText.Render(strings.Join(can, ", ")+" ") + sAmber.Render(strings.TrimPrefix(strings.TrimPrefix(text, strings.Join(can, ", ")), " · "))
			if len(can) == 0 {
				styled = sAmber.Render(text)
			}
		}
		r := strings.ToLower(role)
		if r == "" {
			r = "?"
		}
		rows = append(rows, Row{Key: repo, Cells: []Cell{{Text: a.short(repo)}, {Text: r}, {Text: text, Styled: styled}}})
	}
	for _, r := range a.repos {
		add(r.FullName, r.Permission)
	}
	for repo := range denied {
		if repo != "" && !seen[repo] {
			add(repo, "")
		}
	}
	// What is held back comes first, then by name.
	sort.SliceStable(rows, func(i, j int) bool {
		li, lj := rows[i].Cells[2].Text != "–", rows[j].Cells[2].Text != "–"
		if li != lj {
			return li
		}
		return rows[i].Cells[0].Text < rows[j].Cells[0].Text
	})
	v.t.SetRows(rows)
}

func (v *accessView) tokenLine(a *App) string {
	kind := a.client.Kind().String()
	switch a.client.TokenSource {
	case "gh auth":
		kind += " from gh auth login"
	case "":
	default:
		kind += " from " + a.client.TokenSource
	}
	scopes, known := a.client.Scopes()
	switch {
	case known && len(scopes) > 0:
		kind += " · scopes " + strings.Join(scopes, ", ")
	case known:
		kind += " · no scopes"
	case a.client.Kind() == gh.TokenFineGrained:
		kind += " · fine-grained: permissions are set per repo"
	}
	return kind
}

func (v *accessView) Render(a *App, w, h int) string {
	ro := "off"
	if a.opts.ReadOnly {
		ro = "on"
	}
	var chips []string
	if n := len(a.client.AllDenied()); n > 0 {
		chips = append(chips, pill(fmt.Sprintf("%s%d refused", icon("lock"), n), cAmber, cOnAccent))
	}
	head := []string{
		screenTitle("lock", "Access", "what your token can do", -1, chips, w),
		" " + sDim.Render("Token  ") + sText.Render(v.tokenLine(a)),
		" " + sDim.Render("Host   ") + sText.Render(a.client.Host+" · read-only mode "+ro),
		"",
	}
	lines := append([]string(nil), head...)
	list := strings.Split(v.t.Render(w, max(h-len(head)-3, 3)), "\n")
	lines = append(lines, list...)
	if urls := a.client.SSOURLs(); len(urls) > 0 {
		lines = append(lines, "", " "+sDim.Render("SSO  ")+sAmber.Render("this token needs authorizing for SSO")+sDim.Render(" · enter opens the page"))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
