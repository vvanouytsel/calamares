package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// bigLogo is the logo for the help screen, one braille character per 2×4 dots.
var bigLogo = []string{
	"..##...............##.",
	"..####...........####.",
	"..#####....#....#####.",
	"..###################.",
	"..###################.",
	"..###################.",
	"..###################.",
	"..###################.",
	"..###################.",
	"..#####..#####..#####.",
	"..####...#####...####.",
	".#####....###....#####",
	".######..#####..######",
	".#####################",
	".#####################",
	".#####################",
	"..###################.",
	"...#################..",
	"......#############...",
	"......##.###.##..##...",
	"......##.###.##..###..",
	"......###.##.###..##..",
	".......##.##..##..###.",
	"......###.##..###..##.",
	"......##..##..###..##.",
	"......##..##..###..##.",
	"..........##..##......",
	"......................",
}

// miniLogo is the logo for the top bar, one character per 2×4 dots: a cat
// head with pointed ears and eyes cut out, over four legs.
var miniLogo = []string{
	"#..........#",
	"###......###",
	"############",
	"##..####..##",
	"##..####..##",
	"############",
	"##.##..##.##",
	"#..#....#..#",
}

// brailleArt draws rows of dots ('#' is a dot) as braille characters, one
// line per four rows, fading from the light gold of the first line to the
// amber of the last.
func brailleArt(rows []string) []string {
	bit := [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	lines := (len(rows) + 3) / 4
	var out []string
	for line := 0; line < lines; line++ {
		var b strings.Builder
		for col := 0; col < len(rows[0]); col += 2 {
			cell := rune(0x2800)
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					if y := line*4 + dy; y < len(rows) && col+dx < len(rows[y]) && rows[y][col+dx] == '#' {
						cell |= bit[dy][dx]
					}
				}
			}
			b.WriteRune(cell)
		}
		out = append(out, lipgloss.NewStyle().Foreground(fade(line, lines)).Render(b.String()))
	}
	return out
}

// fade is the colour of line i of n: light gold at the top, amber at the bottom.
func fade(i, n int) lipgloss.Color {
	from, to := [3]int{0xf6, 0xc2, 0x5b}, [3]int{0xe0, 0x8a, 0x2c}
	var c [3]int
	for k := range c {
		c[k] = from[k]
		if n > 1 {
			c[k] += (to[k] - from[k]) * i / (n - 1)
		}
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]))
}

// logoTop is the mini logo drawn as two top bar lines.
var logoTop = brailleArt(miniLogo)
