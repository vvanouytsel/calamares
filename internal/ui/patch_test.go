package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestAPatchPutsTheScreenBackAsItWas(t *testing.T) {
	inColour(t)
	line := sGold.Bold(true).Render("acme") + " " + withBg(sText.Render("webshop"), cPanel) + " ✓"
	p := newPatch([]string{line}, 20)
	if got := ansi.Strip(p.lines()[0]); got != fit("acme webshop ✓", 20) {
		t.Errorf("the text should survive: %q", got)
	}
	c := p.at(0, 0)
	if !c.bold || !nearColor(c.fg, string(cGold)) {
		t.Errorf("the first cell should be gold and bold: %+v", *c)
	}
	if b := p.at(6, 0).bg; !nearColor(b, string(cPanel)) {
		t.Errorf("webshop should keep its background, got %q", b)
	}
}

func TestATopEdgeTakesTheColourBeneathIt(t *testing.T) {
	inColour(t)
	under := lipgloss.Color("#123456")
	p := newPatch([]string{withBg(strings.Repeat(" ", 10), under)}, 10)
	edge(p, 2, 0, 6, cGreen, true)
	c := p.at(4, 0)
	if !nearColor(c.bg, string(under)) || !nearColor(c.fg, string(cGreen)) {
		t.Errorf("a half block should be the card's colour on the colour beneath: %+v", *c)
	}
}
