package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// wrap draws text in up to max lines of the panel's width.
func (p *panelText) wrap(text string, st lipgloss.Style, max int) {
	for i, l := range strings.Split(ansi.Wordwrap(text, p.inner(), ""), "\n") {
		if i == max {
			break
		}
		p.add(st.Render(l))
	}
}

// commitPanel describes the commit under the cursor and the runs that were
// started for it.
func (v *commitsView) panel(a *App, w, h int) []string {
	c, ok := v.selected()
	if !ok {
		return emptyPanel(w, h, "No commit selected.")
	}
	now := time.Now()
	p := newPanel(w)
	p.add(ico("commit") + sTan.Bold(true).Render(c.Short()) + "   " + fact("clock", age(c.Date, now)+" ago"))
	p.wrap(c.Subject(), sWhite.Bold(true), 3)
	p.add(fact("user", c.Author))
	if _, body, found := strings.Cut(c.Message, "\n"); found && strings.TrimSpace(body) != "" {
		p.add("")
		p.wrap(strings.TrimSpace(body), sText, 6)
	}
	var runs []gh.Run
	for _, r := range a.st.status[v.repo] {
		if strings.HasPrefix(r.HeadSHA, c.SHA) || strings.HasPrefix(c.SHA, r.HeadSHA) {
			runs = append(runs, r)
		}
	}
	if len(runs) > 0 {
		p.label("Runs for this commit")
		for _, r := range runs[:min(len(runs), 5)] {
			k := StatusKind(r.Status, r.Conclusion)
			p.row(glyph(k, a.frame)+" "+sText.Render(fmt.Sprintf("%s #%d", r.Name, r.RunNumber)), statusChip(k, a.frame, ""))
		}
	}
	return p.render(h)
}
