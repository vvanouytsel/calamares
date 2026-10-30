package gh

import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Text from GitHub is written by anyone who can open an issue, push a commit
// or run a workflow, and Calamares writes it to a terminal. An escape sequence
// in it could move the cursor, retitle the window or write to the clipboard,
// so all text is cleaned on its way in.

// decode is json.Unmarshal that also cleans every string it fills in.
func decode(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	cleanStrings(reflect.ValueOf(v))
	return nil
}

// cleanText takes escape sequences and control characters out of s; newlines
// and tabs stay.
func cleanText(s string) string {
	if !hasControl(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || printable(r) {
			return r
		}
		return -1
	}, ansi.Strip(s))
}

// cleanLogLine is cleanText for a line of a job log: its colours stay, as
// tools print them on purpose, but every other escape sequence goes.
func cleanLogLine(s string) string {
	if !hasControl(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n, colour := escapeLen(s[i:])
			if colour {
				b.WriteString(s[i : i+n])
			}
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\t' || (printable(r) && !(r == utf8.RuneError && size == 1)) {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// printable reports whether r is neither a C0 or C1 control character nor DEL.
func printable(r rune) bool { return r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) }

// hasControl is a quick test for text that may need cleaning: a control
// character, the lead byte of a C1 control (which also starts ° or £), or
// bytes that are not UTF-8.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if b := s[i]; (b < 0x20 && b != '\n' && b != '\t') || b == 0x7f || b == 0xc2 {
			return true
		}
	}
	return !utf8.ValidString(s)
}

// escapeLen measures the escape sequence at the start of s, and reports
// whether it only sets colours (SGR: ESC [ digits m).
func escapeLen(s string) (int, bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameters, intermediates, then a final byte
		for i := 2; i < len(s); i++ {
			c := s[i]
			switch {
			case c >= 0x40 && c <= 0x7e:
				return i + 1, c == 'm' && strings.Trim(s[2:i], "0123456789;:") == ""
			case c < 0x20 || c > 0x3f:
				return i, false
			}
		}
		return len(s), false
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: a string up to BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1, false
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2, false
			}
		}
		return len(s), false
	}
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	return min(i+1, len(s)), false
}

// cleanStrings cleans every string v holds: fields, elements and map values.
func cleanStrings(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			cleanStrings(v.Elem())
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		e := v.Elem()
		if e.Kind() == reflect.String {
			if v.CanSet() {
				v.Set(reflect.ValueOf(cleanText(e.String())))
			}
			return
		}
		cleanStrings(e)
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() {
				cleanStrings(f)
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return // bytes, such as json.RawMessage, are decoded later
		}
		for i := range v.Len() {
			cleanStrings(v.Index(i))
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			// a map's values can't be set in place: clean a copy and put it back
			val := reflect.New(iter.Value().Type()).Elem()
			val.Set(iter.Value())
			cleanStrings(val)
			v.SetMapIndex(iter.Key(), val)
		}
	case reflect.String:
		if v.CanSet() {
			if s := cleanText(v.String()); s != v.String() {
				v.SetString(s)
			}
		}
	}
}
