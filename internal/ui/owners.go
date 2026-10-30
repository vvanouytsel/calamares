package ui

import (
	"fmt"
	"strings"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// ownerCell names who owns the repo as a whole: the first owner of its
// CODEOWNERS file and how many others share it.
func (a *App) ownerCell(r gh.Repo) Cell {
	owners, read := a.st.owners[r.FullName]
	switch {
	case r.Archived:
		return Cell{Text: "–", Styled: sDim.Render("–")}
	case !read:
		return Cell{}
	case len(owners.Default()) == 0:
		return a.unownedCell(r)
	}
	names := owners.Default()
	text := names[0]
	if len(names) > 1 {
		text += fmt.Sprintf(" +%d", len(names)-1)
	}
	return iconText("user", text, false)
}

func (a *App) unownedCell(r gh.Repo) Cell {
	const text = "no owners"
	if canPush(r) {
		return iconCell("user", text, sDim, cRed)
	}
	return Cell{Text: text, Styled: sDim.Render(text)}
}

func canPush(r gh.Repo) bool {
	switch strings.ToUpper(r.Permission) {
	case "WRITE", "MAINTAIN", "ADMIN":
		return true
	}
	return false
}

const ownerPanelRules = 6

// ownersSection lists the rules of the repo's CODEOWNERS file in the panel.
func (a *App) ownersSection(p *panelText, repo gh.Repo) {
	owners, read := a.st.owners[repo.FullName]
	if !read || repo.Archived {
		return
	}
	p.label("Code owners")
	if owners.File == "" {
		p.add(sDim.Render("no CODEOWNERS file"))
		return
	}
	for _, rule := range owners.Rules[:min(len(owners.Rules), ownerPanelRules)] {
		p.row(ico("user")+sText.Render(strings.Join(rule.Owners, " ")), sDim.Render(rule.Pattern))
	}
	if more := len(owners.Rules) - ownerPanelRules; more > 0 {
		p.add(sDim.Render(fmt.Sprintf("and %d more rules", more)))
	}
	p.add(sDim.Render(owners.File))
}
