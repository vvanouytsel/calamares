package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestEveryKeyHasOneMeaning reads every binding in the code: each
// single-character key must be in keyMap, and a binding that changes
// something on GitHub may not sit on a lowercase key.
func TestEveryKeyHasOneMeaning(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var keys []string
			mutates, isBinding := false, false
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				name, _ := kv.Key.(*ast.Ident)
				if name == nil {
					continue
				}
				switch name.Name {
				case "Keys":
					isBinding = true
					if list, ok := kv.Value.(*ast.CompositeLit); ok {
						for _, e := range list.Elts {
							if s, ok := e.(*ast.BasicLit); ok && s.Kind == token.STRING {
								k, _ := strconv.Unquote(s.Value)
								keys = append(keys, k)
							}
						}
					}
				case "Mutates":
					id, _ := kv.Value.(*ast.Ident)
					mutates = id != nil && id.Name == "true"
				}
			}
			if !isBinding {
				return true
			}
			at := fset.Position(lit.Pos())
			for _, k := range keys {
				if utf8.RuneCountInString(k) != 1 {
					continue // enter, ctrl+r, shift+e: named keys
				}
				if _, ok := keyMap[k]; !ok {
					t.Errorf("%s: key %q is not in keyMap; give it one meaning there first", at, k)
				}
				if r, _ := utf8.DecodeRuneInString(k); mutates && unicode.IsLower(r) {
					t.Errorf("%s: key %q changes GitHub but is lowercase; lowercase keys are always safe", at, k)
				}
			}
			return true
		})
	}
}

// byHelp runs the binding of bs whose help starts with help, as the . menu
// does for the actions without a key of their own.
func byHelp(t *testing.T, a *App, bs []Binding, help string) tea.Cmd {
	t.Helper()
	for _, b := range bs {
		if strings.HasPrefix(b.Help, help) {
			return a.invoke(b, "")
		}
	}
	t.Fatalf("no binding with help %q", help)
	return nil
}
