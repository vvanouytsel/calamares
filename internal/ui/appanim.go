package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// stepFade moves a change of theme one step on, before the screen is drawn.
func (a *App) stepFade() {
	if a.fade == nil {
		return
	}
	p := progress01(a.fade.start, fadeMillis)
	if p >= 1 {
		applyTheme(a.fade.to)
		a.fade = nil
		return
	}
	applyTheme(mixTheme(a.fade.from, a.fade.to, ease(p)))
}

// setTheme switches to the theme called name, fading over a fifth of a
// second. With save it is remembered in the config.
func (a *App) setTheme(name string, save bool) tea.Cmd {
	t := themeNamed(name)
	if t == nil {
		return a.flashCmd(nHint, "No theme called "+name+". The themes are "+strings.Join(themeNames(), ", ")+".")
	}
	from, prev := currentTheme, a.themeName
	a.themeName = t.Name
	if animLevel >= animFull && prev != t.Name {
		a.fade = &themeFade{from: from, to: *t, start: nowMs()}
	} else {
		a.fade = nil
		applyTheme(*t)
	}
	if save {
		a.earnSeen("fashionista", t.Name)
		a.cfg.Theme = t.Name
		if err := a.cfg.Save(); err != nil {
			return a.flashCmd(nError, "Could not save the theme: "+err.Error())
		}
		return a.flashCmd(nDone, "Theme: "+t.Name)
	}
	return nil
}

// openThemes is the theme picker; it previews each theme as you move, enter
// keeps it and esc comes back to the one you had.
func (a *App) openThemes() tea.Cmd {
	was := a.themeName
	p := newPicker("theme", "Theme", was, themeItems(), func(v string) tea.Cmd { return a.setTheme(v, true) })
	p.onMove = func(v string) { a.setTheme(v, false) }
	p.onCancel = func() tea.Cmd { a.setTheme(was, false); return nil }
	return openModal(p)
}

// cycleTheme moves to the next theme.
func (a *App) cycleTheme() tea.Cmd {
	for i, t := range themes {
		if t.Name == a.themeName {
			return a.setTheme(themes[(i+1)%len(themes)].Name, true)
		}
	}
	return a.setTheme(themes[0].Name, true)
}

// modalBox draws the dialog on display, on the raised surface, opening with
// a short ease the first time it is drawn.
func (a *App) modalBox() string {
	if !a.modalOpen {
		a.modalOpen, a.modalAt = true, nowMs()
	}
	box := openEase(a.modal.Render(a, a.w, a.h), a.modalAt)
	lines := strings.Split(box, "\n")
	for i, l := range lines {
		lines[i] = withBg(l, cPanel)
	}
	return strings.Join(lines, "\n")
}

// noteModal notes that no dialog is open, so the next one eases open again.
func (a *App) noteModal() {
	if a.modal == nil {
		a.modalOpen = false
	}
}
