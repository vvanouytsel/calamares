package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBreak   = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlSummary = regexp.MustCompile(`(?is)<summary>(.*?)</summary>`)
	htmlImage   = regexp.MustCompile(`(?i)<img[^>]*?alt="([^"]*)"[^>]*>`)
	htmlTag     = regexp.MustCompile(`(?i)</?(details|blockquote|p|div|span|sub|sup|b|i|em|strong|a|img|picture|source|kbd|summary|h[1-6]|table|thead|tbody|tr|td|th|ul|ol|li|hr|code|pre)(\s[^>]*)?/?>`)

	mdHeading   = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	mdRule      = regexp.MustCompile(`^\s{0,3}([-*_])(\s*[-*_]){2,}\s*$`)
	mdBullet    = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	mdTask      = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	mdQuote     = regexp.MustCompile(`^\s{0,3}>\s?(.*)$`)
	mdFence     = regexp.MustCompile("^\\s{0,3}(```+|~~~+)\\s*([A-Za-z0-9_+-]*)")
	mdTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)
	mdImage     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	mdBold      = regexp.MustCompile(`\*\*(\S(?:.*?\S)?)\*\*|__(\S(?:.*?\S)?)__`)
	mdItalicAst = regexp.MustCompile(`\*(\S(?:[^*]*?\S)?)\*`)
	mdItalicUnd = regexp.MustCompile(`(^|[^\w])_(\S(?:[^_]*?\S)?)_([^\w]|$)`)
	mdStrike    = regexp.MustCompile(`~~(\S(?:.*?\S)?)~~`)
	mdMention   = regexp.MustCompile(`(^|[^\w/])@([A-Za-z0-9][A-Za-z0-9-]*(?:/[A-Za-z0-9_.-]+)?)`)
	mdIssueRef  = regexp.MustCompile(`(^|[^\w&/])(#\d+)\b`)
	mdURL       = regexp.MustCompile(`https?://[^\s<>)\]]+`)
)

var (
	sMdCode  = lipgloss.NewStyle().Foreground(cText).Background(lipgloss.Color("#16263d"))
	sMdBlock = lipgloss.NewStyle().Foreground(cRow)
	sMdLink  = lipgloss.NewStyle().Foreground(cCyan).Underline(true)
)

// stripControl removes control characters other than newline and tab, so text
// from GitHub cannot move the cursor or restyle the screen.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, s)
}

// cleanHTML turns the HTML GitHub allows in markdown into plain text, leaving
// code blocks alone. With fold, a <details> block shows only its summary.
func cleanHTML(src string, fold bool) string {
	src = htmlComment.ReplaceAllString(src, "")
	if fold {
		src = foldDetails(src)
	}
	var out []string
	inFence := false
	for _, l := range strings.Split(src, "\n") {
		if mdFence.MatchString(l) {
			inFence = !inFence
		}
		if !inFence {
			l = htmlBreak.ReplaceAllString(l, "\n")
			l = htmlSummary.ReplaceAllString(l, "▸ **$1**")
			l = htmlImage.ReplaceAllString(l, "[image: $1]")
			l = htmlTag.ReplaceAllString(l, "")
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

var (
	htmlDetailsOpen  = regexp.MustCompile(`(?i)<details(\s[^>]*)?>`)
	htmlDetailsClose = regexp.MustCompile(`(?i)</details\s*>`)
)

// foldDetails keeps the summary of each outer <details> block and drops
// what it hides, as GitHub shows it until you open it.
func foldDetails(src string) string {
	var b strings.Builder
	for {
		open := htmlDetailsOpen.FindStringIndex(src)
		if open == nil {
			b.WriteString(src)
			return b.String()
		}
		b.WriteString(src[:open[0]])
		rest, depth, end := src[open[1]:], 1, -1
		for at := 0; depth > 0; {
			o := htmlDetailsOpen.FindStringIndex(rest[at:])
			c := htmlDetailsClose.FindStringIndex(rest[at:])
			if c == nil {
				break
			}
			if o != nil && o[0] < c[0] {
				depth, at = depth+1, at+o[1]
				continue
			}
			depth, at = depth-1, at+c[1]
			if depth == 0 {
				end = at
			}
		}
		inner := rest
		if end >= 0 {
			inner, src = rest[:end], rest[end:]
		} else {
			src = ""
		}
		summary := "Details"
		if m := htmlSummary.FindStringSubmatch(inner); m != nil {
			summary = strings.TrimSpace(htmlTag.ReplaceAllString(m[1], ""))
		}
		b.WriteString("\n\n▸ **" + summary + "** …\n\n")
	}
}

// renderMarkdown draws GitHub-flavoured markdown as styled terminal lines, each
// at most w cells wide.
func renderMarkdown(src string, w int) []string { return renderMarkdownFolded(src, w, false) }

// hasDetails says the markdown has a <details> block to fold.
func hasDetails(src string) bool { return htmlDetailsOpen.MatchString(src) }

// renderMarkdownFolded is renderMarkdown; with fold, a <details> block shows
// only its summary.
func renderMarkdownFolded(src string, w int, fold bool) []string {
	w = max(w, 8)
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	src = cleanHTML(stripControl(src), fold)

	var out []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, wrapStyled(inlineMarkdown(strings.Join(para, " ")), w, "", "")...)
			para = nil
		}
	}

	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimSpace(line)

		if m := mdFence.FindStringSubmatch(line); m != nil {
			flush()
			if m[2] != "" {
				out = append(out, sDim.Render("╭ "+m[2]))
			}
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), m[1][:3]); i++ {
				code := strings.ReplaceAll(strings.TrimRight(lines[i], " \t"), "\t", "    ")
				for _, part := range hardWrap(code, w-2) {
					out = append(out, sDim.Render("│ ")+sMdBlock.Render(part))
				}
			}
			continue
		}
		switch {
		case trim == "":
			flush()
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
		case mdRule.MatchString(line):
			flush()
			out = append(out, sDim.Render(strings.Repeat("─", min(w, 40))))
		case mdHeading.MatchString(line):
			flush()
			m := mdHeading.FindStringSubmatch(line)
			style := sCyan.Bold(true)
			if len(m[1]) > 1 {
				style = sWhite.Bold(true)
			}
			for _, l := range wrapStyled(style.Render(stripInline(m[2])), w, "", "") {
				out = append(out, l)
			}
		case mdQuote.MatchString(line):
			flush()
			var quoted []string
			for ; i < len(lines) && mdQuote.MatchString(lines[i]); i++ {
				quoted = append(quoted, mdQuote.FindStringSubmatch(lines[i])[1])
			}
			i--
			for _, l := range renderMarkdown(strings.Join(quoted, "\n"), w-2) {
				out = append(out, sDim.Render("▎ ")+l)
			}
		case mdBullet.MatchString(line):
			flush()
			m := mdBullet.FindStringSubmatch(line)
			indent := strings.Repeat(" ", min(len(strings.ReplaceAll(m[1], "\t", "    "))/2*2, w/2))
			marker, body := "•", m[3]
			if m[2][0] >= '0' && m[2][0] <= '9' {
				marker = m[2]
			}
			if t := mdTask.FindStringSubmatch(body); t != nil {
				marker, body = "☐", t[2]
				if t[1] != " " {
					marker = "☑"
				}
			}
			first := indent + sTan.Render(marker) + " "
			rest := strings.Repeat(" ", width(first))
			out = append(out, wrapStyled(inlineMarkdown(body), w, first, rest)...)
		case strings.Contains(trim, "|") && i+1 < len(lines) && mdTableSep.MatchString(lines[i+1]) && strings.Contains(lines[i+1], "-"):
			flush()
			var rows [][]string
			for ; i < len(lines) && strings.Contains(lines[i], "|"); i++ {
				if mdTableSep.MatchString(lines[i]) {
					continue
				}
				cells := strings.Split(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|")
				for c := range cells {
					cells[c] = strings.TrimSpace(cells[c])
				}
				rows = append(rows, cells)
			}
			i--
			out = append(out, renderTable(rows, w)...)
		default:
			para = append(para, trim)
		}
	}
	flush()
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func renderTable(rows [][]string, w int) []string {
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], width(stripInline(c)))
		}
	}
	for total := cols * 3; ; {
		total = cols*3 - 1
		for _, cw := range widths {
			total += cw
		}
		if total <= w {
			break
		}
		widest := 0
		for i, cw := range widths {
			if cw > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 4 {
			break
		}
		widths[widest]--
	}
	var out []string
	for n, r := range rows {
		var cells []string
		for i := 0; i < cols; i++ {
			text := ""
			if i < len(r) {
				text = stripInline(r[i])
			}
			style := sText
			if n == 0 {
				style = sBold
			}
			cells = append(cells, style.Render(fit(text, widths[i])))
		}
		out = append(out, clip(strings.Join(cells, sDim.Render(" │ ")), w))
		if n == 0 {
			var rule []string
			for _, cw := range widths {
				rule = append(rule, strings.Repeat("─", cw))
			}
			out = append(out, sDim.Render(clip(strings.Join(rule, "─┼─"), w)))
		}
	}
	return out
}

// stripInline removes inline markup, leaving the words.
func stripInline(s string) string {
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdBold.ReplaceAllString(s, "$1$2")
	s = strings.ReplaceAll(s, "`", "")
	return s
}

// inlineMarkdown styles code, emphasis, links, mentions and references.
func inlineMarkdown(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 {
		return styleInline(s)
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString(sMdCode.Render(" " + p + " "))
		} else {
			b.WriteString(styleInline(p))
		}
	}
	return b.String()
}

func styleInline(s string) string {
	s = mdImage.ReplaceAllStringFunc(s, func(m string) string {
		return "‹image: " + mdImage.FindStringSubmatch(m)[1] + "›"
	})
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		return sMdLink.Render(mdLink.FindStringSubmatch(m)[1])
	})
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		g := mdBold.FindStringSubmatch(m)
		return sWhite.Bold(true).Render(g[1] + g[2])
	})
	s = mdStrike.ReplaceAllStringFunc(s, func(m string) string {
		return sDim.Strikethrough(true).Render(mdStrike.FindStringSubmatch(m)[1])
	})
	s = mdItalicAst.ReplaceAllStringFunc(s, func(m string) string {
		return sText.Italic(true).Render(mdItalicAst.FindStringSubmatch(m)[1])
	})
	s = mdItalicUnd.ReplaceAllStringFunc(s, func(m string) string {
		g := mdItalicUnd.FindStringSubmatch(m)
		return g[1] + sText.Italic(true).Render(g[2]) + g[3]
	})
	s = mdMention.ReplaceAllString(s, "$1"+sTan.Render("@$2"))
	s = mdIssueRef.ReplaceAllString(s, "$1"+sCyan.Render("$2"))
	if !strings.Contains(s, "\x1b") {
		s = mdURL.ReplaceAllStringFunc(s, func(u string) string { return sMdLink.Render(u) })
	}
	return inBaseColour(s)
}

// inBaseColour paints s in the text colour, restoring it after each styled span.
func inBaseColour(s string) string {
	start, _, found := strings.Cut(sText.Render("\x00"), "\x00")
	if !found || start == "" {
		return s
	}
	return start + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+start) + "\x1b[0m"
}

// wrapStyled wraps styled text to w cells, putting first before the first line
// and rest before the others.
func wrapStyled(s string, w int, first, rest string) []string {
	room := max(w-width(first), 4)
	var out []string
	for i, l := range strings.Split(ansi.Wrap(s, room, ""), "\n") {
		prefix := rest
		if i == 0 {
			prefix = first
		}
		out = append(out, prefix+l)
	}
	return out
}

// hardWrap splits s into pieces of at most w cells.
func hardWrap(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	return strings.Split(ansi.Hardwrap(s, max(w, 1), true), "\n")
}
