package ui

import (
	"strings"
	"unicode"
)

// query is a parsed filter, shared by the list search and the saved filter.
// Terms are separated by spaces and must all match. A term is a value, or
// field:value to look only in fields whose name starts with field;
// field:a,b matches either value; a leading "-" excludes what the term
// matches. Values match as substrings, or as a whole-value pattern when they
// contain "*". Case is ignored, double quotes keep spaces in a value, and
// "@me" stands for your login.
type query []queryTerm

type queryTerm struct {
	field  string
	values []string
	negate bool
}

// synonyms map GitHub's words for a run's state to the ones on screen.
var synonyms = map[string]string{
	"success":     "passed",
	"succeeded":   "passed",
	"failure":     "failed",
	"in_progress": "running",
	"cancelled":   "canceled",
}

func parseQuery(q, me string) query {
	q = strings.ReplaceAll(strings.ToLower(q), "@me", strings.ToLower(me))
	var out query
	for _, tok := range splitQuery(q) {
		var t queryTerm
		if len(tok) > 1 && tok[0] == '-' {
			t.negate, tok = true, tok[1:]
		}
		if field, val, ok := strings.Cut(tok, ":"); ok && field != "" && val != "" {
			t.field = field
			for _, v := range strings.Split(val, ",") {
				if v != "" {
					t.values = append(t.values, v)
				}
			}
		} else {
			t.values = []string{tok}
		}
		for _, v := range t.values {
			if alt, ok := synonyms[v]; ok {
				t.values = append(t.values, alt)
			}
		}
		out = append(out, t)
	}
	return out
}

// splitQuery splits on spaces outside double quotes and drops the quotes.
func splitQuery(q string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range q {
		switch {
		case r == '"':
			quoted = !quoted
		case unicode.IsSpace(r) && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// match reports whether every term holds; values returns the texts of the
// fields a term's field name selects ("" selects all of them).
func (q query) match(values func(field string) []string) bool {
	for _, t := range q {
		if t.hits(values(t.field)) == t.negate {
			return false
		}
	}
	return true
}

func (t queryTerm) hits(texts []string) bool {
	for _, s := range texts {
		s = strings.ToLower(s)
		for _, v := range t.values {
			if strings.Contains(v, "*") && globMatch(v, s) || !strings.Contains(v, "*") && strings.Contains(s, v) {
				return true
			}
		}
	}
	return false
}

// globMatch matches s against pattern, where "*" stands for any run of
// characters, slashes included.
func globMatch(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case pi < len(p) && p[pi] == t[ti]:
			pi++
			ti++
		case star >= 0:
			mark++
			pi, ti = star+1, mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
