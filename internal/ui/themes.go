package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// A theme is a small set of colours; everything else (the tints of status
// chips, the backgrounds of diffs, the dim frames) is mixed from them, so a
// theme is a dozen lines and the screens never name a colour of their own.

// Theme is one look: core colours as "#rrggbb".
type Theme struct {
	Name, Desc string
	Light      bool

	Bg       string // the base: every cell of the screen is painted with it
	Band     string // the header band
	Panel    string // a detail panel and the cards over the screen
	Sel      string // the row under the cursor
	Frame    string // a frame at rest and the hairlines
	Accent   string // focus, links, where you are
	AccentBg string // the view on display in a strip
	Text     string // data in a row
	Dim      string // labels and hints
	Grey     string
	White    string // what you read: titles
	Green    string // passed
	Red      string // failed
	Amber    string // running
	Wait     string // waits for a person
	Gold     string // the brand
	OnAccent string // text on an accent or a status colour
	// The colours of icons by what they stand for: branches, commits and
	// versions (Ref), people, and comments; Violet is merged and done.
	Ref, Violet, Teal, Pink string
}

// fields lists the colours of t, for mixing two themes.
func (t *Theme) fields() []*string {
	return []*string{&t.Bg, &t.Band, &t.Panel, &t.Sel, &t.Frame, &t.Accent, &t.AccentBg, &t.Text, &t.Dim, &t.Grey,
		&t.White, &t.Green, &t.Red, &t.Amber, &t.Wait, &t.Gold, &t.OnAccent, &t.Ref, &t.Violet, &t.Teal, &t.Pink}
}

var themes = []Theme{
	{Name: "cockpit", Desc: "the original: deep navy and a blue accent",
		Bg: "#0b1119", Band: "#101826", Panel: "#0f1722", Sel: "#16243d", Frame: "#2a3345", Accent: "#58a6ff", AccentBg: "#1d4177",
		Text: "#c9d1d9", Dim: "#657187", Grey: "#a3aebf", White: "#f0f3f6", Green: "#3fd68b", Red: "#ff5d6c", Amber: "#f5b942",
		Wait: "#ff9a3c", Gold: "#f0b64a", OnAccent: "#06101f",
		Ref: "#9fd36b", Violet: "#b392f0", Teal: "#56d4c4", Pink: "#f08cc0"},
	{Name: "abyss", Desc: "the deep sea: bioluminescent cyan on squid ink",
		Bg: "#050b12", Band: "#081621", Panel: "#08121d", Sel: "#0c2a33", Frame: "#173047", Accent: "#2fe3d3", AccentBg: "#0b4a4d",
		Text: "#c3d2dd", Dim: "#4d6276", Grey: "#93a9bb", White: "#ecf7fb", Green: "#4be3a0", Red: "#ff5f80", Amber: "#ffc457",
		Wait: "#ff9a4d", Gold: "#f5c04f", OnAccent: "#02110f",
		Ref: "#a8e36b", Violet: "#a78bfa", Teal: "#6cc8f0", Pink: "#f78fc8"},
	{Name: "midnight", Desc: "indigo night with a violet accent",
		Bg: "#0d0b1e", Band: "#14112c", Panel: "#120f27", Sel: "#241d4a", Frame: "#2c2656", Accent: "#9d8cff", AccentBg: "#3b2f86",
		Text: "#d6d3f0", Dim: "#6f6a9a", Grey: "#a9a5d0", White: "#f4f2ff", Green: "#5ee6a8", Red: "#ff6b8b", Amber: "#ffc857",
		Wait: "#ff9a6b", Gold: "#c4b5ff", OnAccent: "#0d0b1e",
		Ref: "#c3e27a", Violet: "#d9a6ff", Teal: "#6ee7d8", Pink: "#ff8fd0"},
	{Name: "ember", Desc: "warm charcoal with orange embers",
		Bg: "#14100d", Band: "#1c1612", Panel: "#18130f", Sel: "#33241a", Frame: "#3a2d22", Accent: "#ff9a3c", AccentBg: "#7a3d0c",
		Text: "#e6d9cc", Dim: "#8a7765", Grey: "#b9a894", White: "#fff3e6", Green: "#8fd16a", Red: "#ff5d4d", Amber: "#ffcb47",
		Wait: "#ff7a3c", Gold: "#ffb454", OnAccent: "#14100d",
		Ref: "#8cc8e8", Violet: "#d0a2e8", Teal: "#8fd6b9", Pink: "#ff9eb5"},
	{Name: "aurora", Desc: "deep teal with northern-lights green",
		Bg: "#07151a", Band: "#0b1f26", Panel: "#0a1b21", Sel: "#0f3640", Frame: "#1b3a42", Accent: "#2de2c4", AccentBg: "#0b5c52",
		Text: "#cfe8e6", Dim: "#5f8a8c", Grey: "#9cc2c2", White: "#effffd", Green: "#6df29a", Red: "#ff6f7d", Amber: "#ffd166",
		Wait: "#ff9f5a", Gold: "#2de2c4", OnAccent: "#07151a",
		Ref: "#c5e478", Violet: "#b9a3ff", Teal: "#7fd4ff", Pink: "#ff9ecf"},
	{Name: "neon", Desc: "synthwave: magenta and cyan on purple-black",
		Bg: "#100716", Band: "#1a0b26", Panel: "#150a1f", Sel: "#3a1450", Frame: "#3a1d4f", Accent: "#ff4fd8", AccentBg: "#7a1a6b",
		Text: "#eadcf5", Dim: "#8a6aa3", Grey: "#bba2d1", White: "#fff0ff", Green: "#38ffb0", Red: "#ff4d6d", Amber: "#ffe14d",
		Wait: "#ff9a3c", Gold: "#00e5ff", OnAccent: "#100716",
		Ref: "#c6f26b", Violet: "#b98bff", Teal: "#00e5ff", Pink: "#ff8ad8"},
	{Name: "slate", Desc: "a softer dark: slate grey, easy on the eyes",
		Bg: "#2b313c", Band: "#343b48", Panel: "#313845", Sel: "#3d4b63", Frame: "#4a5366", Accent: "#6cb6ff", AccentBg: "#2f5a8f",
		Text: "#d3dae3", Dim: "#8a94a6", Grey: "#b4bdcb", White: "#f3f6fa", Green: "#57d394", Red: "#ff7b84", Amber: "#f2c45a",
		Wait: "#ffa057", Gold: "#f0b64a", OnAccent: "#1b2028",
		Ref: "#b5d97a", Violet: "#c2a6f5", Teal: "#6fd6c8", Pink: "#f59bc9"},
	{Name: "paper", Desc: "a light theme, for bright rooms", Light: true,
		Bg: "#faf7f0", Band: "#efe9db", Panel: "#f3eee2", Sel: "#dfe9fb", Frame: "#d8d2c2", Accent: "#1f6feb", AccentBg: "#cfe0ff",
		Text: "#2d3340", Dim: "#8b8f99", Grey: "#5b6270", White: "#11151c", Green: "#1a7f4b", Red: "#cf222e", Amber: "#9a6700",
		Wait: "#b35900", Gold: "#b35900", OnAccent: "#ffffff",
		Ref: "#5f8a1c", Violet: "#8250df", Teal: "#0f7b70", Pink: "#bf3989"},
}

// themeNamed finds a theme by name, or nil.
func themeNamed(name string) *Theme {
	for i := range themes {
		if strings.EqualFold(themes[i].Name, name) {
			return &themes[i]
		}
	}
	return nil
}

// themeNames lists the names, for messages.
func themeNames() []string {
	var out []string
	for _, t := range themes {
		out = append(out, t.Name)
	}
	return out
}

// rgb parses "#rrggbb".
func rgb(s string) (r, g, b int) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0
	}
	v, _ := strconv.ParseUint(s, 16, 32)
	return int(v >> 16 & 255), int(v >> 8 & 255), int(v & 255)
}

// mix blends a towards b by t (0 is a, 1 is b).
func mix(a, b string, t float64) string {
	ar, ag, ab := rgb(a)
	br, bg, bb := rgb(b)
	f := func(x, y int) int { return min(max(int(float64(x)+(float64(y-x))*t+0.5), 0), 255) }
	return fmt.Sprintf("#%02x%02x%02x", f(ar, br), f(ag, bg), f(ab, bb))
}

// mixTheme blends two themes: the colours of a at t=0, of b at t=1.
func mixTheme(a, b Theme, t float64) Theme {
	out := b
	fa, fb, fo := a.fields(), b.fields(), out.fields()
	for i := range fo {
		*fo[i] = mix(*fa[i], *fb[i], t)
	}
	return out
}

// baseOn says the screen is painted with the theme's base colour. It is off
// where colour is off (NO_COLOR) or the icons are plain ASCII.
var baseOn = true

// currentTheme is the theme on display.
var currentTheme = themes[0]

// applyTheme sets every colour and style the screens draw with.
func applyTheme(t Theme) {
	currentTheme = t
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	text, bg := t.Text, t.Bg

	cBg = c(bg)
	cFrame, cFrameDim = c(t.Frame), c(t.Frame)
	cCyan, cBlue = c(t.Accent), c(t.Accent)
	cRow, cText, cCrumbFg = c(text), c(text), c(text)
	cWait = c(t.Wait)
	cGold = c(t.Gold)
	cGreen, cRed, cAmber = c(t.Green), c(t.Red), c(t.Amber)
	cTan = c(mix(t.Amber, t.Wait, 0.35))
	cRef, cViolet, cTeal, cPink = c(t.Ref), c(t.Violet), c(t.Teal), c(t.Pink)
	cSteel = c(mix(t.Dim, t.Grey, 0.5))
	cDim, cGrey, cHint, cWhite = c(t.Dim), c(t.Grey), c(t.Grey), c(t.White)
	cBand, cPanel = c(t.Band), c(t.Panel)
	cAccentBg = c(t.AccentBg)
	cSelBg = c(t.Sel)
	cBlurSelBg = c(mix(bg, t.Sel, 0.55))
	cCrumbBg = c(mix(t.Band, text, 0.14))
	cOnAccent = c(t.OnAccent)
	cMatchBg = c(mix(bg, t.Amber, 0.38))
	cErrBg = c(mix(bg, t.Red, 0.1))
	cFailFrame = c(mix(t.Red, bg, 0.4))
	cMeterOff = c(mix(bg, t.Frame, 1))
	cGutter = c(mix(t.Dim, bg, 0.45))
	cCodeBg = c(mix(bg, t.Sel, 0.8))
	addBg, delBg, hunkBg = c(mix(bg, t.Green, 0.17)), c(mix(bg, t.Red, 0.18)), c(mix(bg, t.Sel, 0.9))

	strength := 0.2
	if t.Light {
		strength = 0.16
	}
	tint = map[Kind]lipgloss.Color{
		KOK: c(mix(bg, t.Green, strength)), KFail: c(mix(bg, t.Red, strength)),
		KRun: c(mix(bg, t.Amber, strength)), KWait: c(mix(bg, t.Wait, strength)),
	}

	sBlueDigit = fg(c(mix(t.Accent, t.White, 0.35)))
	sDim, sText, sWhite = fg(cDim), fg(cText), fg(cWhite)
	sBold = fg(cWhite).Bold(true)
	sCyan, sWait, sGold, sGrey, sTan = fg(cCyan), fg(cWait), fg(cGold), fg(cGrey), fg(cTan)
	sGreen, sRed, sAmber, sBlue = fg(cGreen), fg(cRed), fg(cAmber), fg(cBlue)
	sHint, sRow = fg(cHint), fg(cRow)
	sSel = lipgloss.NewStyle().Foreground(cWhite).Background(cSelBg)
	sBlurSel = lipgloss.NewStyle().Foreground(cText).Background(cBlurSelBg)
	sKeycap = lipgloss.NewStyle().Foreground(cWhite).Background(cCrumbBg).Bold(true)
	sHeader = lipgloss.NewStyle().Foreground(cGrey).Bold(true)
	sMdCode = lipgloss.NewStyle().Foreground(cText).Background(cCodeBg)
	sMdBlock = lipgloss.NewStyle().Foreground(cRow)
	sMdLink = lipgloss.NewStyle().Foreground(cCyan).Underline(true)
}

func init() { applyTheme(themes[0]) }

// colourOff says the user asked for no colour (https://no-color.org).
func colourOff() bool { return os.Getenv("NO_COLOR") != "" }

// paintBase lays the base colour under every cell of the screen: each line is
// padded to w and drawn on cBg, also behind the parts that have a style of
// their own, so no cell is left to the terminal's own background.
func paintBase(out string, w, h int) string {
	if !baseOn {
		return out
	}
	lines := strings.Split(out, "\n")
	blank := strings.Repeat(" ", w)
	for len(lines) < h {
		lines = append(lines, blank)
	}
	for i, l := range lines {
		lines[i] = withBg(fit(l, w), cBg)
	}
	return strings.Join(lines, "\n")
}

// themeFade is a change of theme in progress.
type themeFade struct {
	from, to Theme
	start    int64 // unix milliseconds
}

const fadeMillis = 220

// themeItems are the themes as the picker lists them.
func themeItems() []pickItem {
	var items []pickItem
	for _, t := range themes {
		sw := ""
		for _, col := range []string{t.Bg, t.Accent, t.Green, t.Red, t.Amber} {
			sw += lipgloss.NewStyle().Background(lipgloss.Color(col)).Render("  ")
		}
		items = append(items, pickItem{value: t.Name, label: t.Name, note: sw + " " + sDim.Render(t.Desc)})
	}
	return items
}
