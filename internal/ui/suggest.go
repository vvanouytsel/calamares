package ui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// filterOption is something the word being typed in the filter can become. A
// count of 0 means the value is one of a fixed set, so it isn't shown.
type filterOption struct {
	text  string
	count int
}

func fixedSuggestions(values ...string) []filterOption {
	out := make([]filterOption, len(values))
	for i, v := range values {
		out[i] = filterOption{text: v}
	}
	return out
}

// countSuggestions lists the distinct values, the most common first.
func countSuggestions(values []string) []filterOption {
	counts := map[string]int{}
	for _, v := range values {
		if v != "" && len(v) <= 48 {
			counts[v]++
		}
	}
	out := make([]filterOption, 0, len(counts))
	for v, n := range counts {
		out = append(out, filterOption{v, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].text < out[j].text
	})
	return out
}

// plainValue is a cell's text without the icon or glyph in front of it.
func plainValue(text string) string {
	text = strings.TrimLeftFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("@#/_.", r)
	})
	return strings.TrimSpace(text)
}

// columnValues lists the values of the column a field names.
func (t *Table) columnValues(field string) []filterOption {
	col := slices.IndexFunc(t.Cols, func(c Column) bool { return fieldName(c.Title) == field })
	if col < 0 {
		return nil
	}
	var values []string
	for _, r := range t.rows {
		if col < len(r.Cells) {
			values = append(values, plainValue(r.Cells[col].Text))
		}
	}
	return countSuggestions(values)
}

// completion is what the last word of a filter query could be replaced with.
type completion struct {
	start   int // where the word begins in the query
	options []filterOption
	texts   []string // the query text each option stands for, from start on
}

// wordStart is where the last word of q begins; spaces inside quotes don't end a word.
func wordStart(q string) int {
	start, quoted := 0, false
	for i, r := range q {
		switch {
		case r == '"':
			quoted = !quoted
		case unicode.IsSpace(r) && !quoted:
			start = i + 1
		}
	}
	return start
}

func quoteValue(v string) string {
	if strings.ContainsAny(v, " \t\"") {
		return `"` + strings.ReplaceAll(v, `"`, "") + `"`
	}
	return v
}

// complete works out what the word being typed can become: a field name
// while it has no colon, a value of that field after one. An empty word
// offers fields only when asked.
func complete(q string, info filterInfo, asked bool) completion {
	start := wordStart(q)
	word := q[start:]
	c := completion{start: start}
	negation := ""
	if strings.HasPrefix(word, "-") {
		negation, word = "-", word[1:]
	}
	field, typed, hasField := strings.Cut(word, ":")
	if !hasField {
		if word == "" && !asked {
			return c
		}
		for _, f := range info.fields {
			if strings.HasPrefix(f, strings.ToLower(word)) && f != strings.ToLower(word) {
				c.options = append(c.options, filterOption{text: f + ":"})
				c.texts = append(c.texts, negation+f+":")
			}
		}
		return c
	}
	field = strings.ToLower(field)
	column := slices.IndexFunc(info.fields, func(f string) bool { return strings.HasPrefix(f, field) })
	if info.values == nil || field == "" || column < 0 {
		return c
	}
	done := ""
	if i := strings.LastIndex(typed, ","); i >= 0 {
		done, typed = typed[:i+1], typed[i+1:]
	}
	typed = strings.ToLower(strings.TrimPrefix(typed, `"`))
	typed = strings.TrimSuffix(typed, `"`)
	var prefixed, inside []filterOption
	for _, s := range info.values(info.fields[column]) {
		text := strings.ToLower(s.text)
		switch {
		case text == typed:
		case strings.HasPrefix(text, typed):
			prefixed = append(prefixed, s)
		case strings.Contains(text, typed):
			inside = append(inside, s)
		}
	}
	c.options = append(prefixed, inside...)
	for _, s := range c.options {
		c.texts = append(c.texts, negation+word[:len(field)+1]+done+quoteValue(s.text))
	}
	return c
}

// suggestState is where the suggestions under the filter box stand: which one
// is chosen, whether tab was pressed to list the fields of an empty word, and
// while tab cycles through the options, the options it started from.
type suggestState struct {
	sel     int
	asked   bool
	cycling bool
	comp    completion
}

func (s *suggestState) current(q string, info filterInfo) completion {
	if s.cycling {
		return s.comp
	}
	return complete(q, info, s.asked)
}

// completeFilter handles tab, shift+tab and the arrows while the filter box
// is open. It reports whether it used the key.
func (a *App) completeFilter(key string) bool {
	f, ok := a.top().(Filterer)
	if !ok {
		return false
	}
	delta := map[string]int{"tab": 1, "right": 1, "shift+tab": -1, "left": -1}[key]
	if delta == 0 {
		return false
	}
	info := f.FilterInfo(a)
	comp := a.suggest.current(a.input.Value(), info)
	if len(comp.options) == 0 {
		if key == "tab" && !a.suggest.asked {
			a.suggest.asked = true
		}
		return key == "tab" || key == "shift+tab"
	}
	if key == "tab" && !a.suggest.cycling {
		delta = 0
	}
	a.suggest.sel = (a.suggest.sel + delta + len(comp.options)) % len(comp.options)
	if key == "tab" || key == "shift+tab" || a.suggest.cycling {
		q := a.input.Value()[:comp.start] + comp.texts[a.suggest.sel]
		a.input.SetValue(q)
		a.input.CursorEnd()
		a.suggest.cycling, a.suggest.comp = true, comp
		f.SetFilter(a, q)
	}
	return true
}

// strip draws the options on one line, scrolled so the chosen one shows.
func (s suggestState) strip(comp completion, avail int) string {
	sel := min(s.sel, len(comp.options)-1)
	labels := make([]string, len(comp.options))
	for i, o := range comp.options {
		labels[i] = " " + o.text + " "
		if o.count > 0 {
			labels[i] = fmt.Sprintf(" %s %d ", o.text, o.count)
		}
	}
	hint := "tab complete"
	if len(comp.options) > 1 {
		hint += " · ← → choose"
	}
	room := max(avail-len(hint)-6, 10)
	first := 0
	for first < sel && widthFrom(labels, first, sel) > room {
		first++
	}
	var out []string
	used := 0
	for i := first; i < len(labels); i++ {
		if used+width(labels[i]) > room && i > sel {
			out = append(out, sDim.Render(fmt.Sprintf("+%d more", len(labels)-i)))
			break
		}
		used += width(labels[i]) + 1
		if i == sel {
			out = append(out, sSel.Render(labels[i]))
		} else {
			out = append(out, sWhite.Render(labels[i]))
		}
	}
	return strings.Join(out, " ") + "  " + sDim.Render(hint)
}

// widthFrom is how wide labels from..to are side by side.
func widthFrom(labels []string, from, to int) int {
	total := 0
	for _, l := range labels[from : to+1] {
		total += width(l) + 1
	}
	return total
}
