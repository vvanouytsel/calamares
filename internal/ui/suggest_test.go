package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func suggestTable() *Table {
	t := newTable(Column{Title: "REPOSITORY"}, Column{Title: "STATE"}, Column{Title: "OWNER"})
	t.SetRows([]Row{
		{Key: "a", Cells: []Cell{{Text: "acme/api"}, {Text: "✗ failed"}, {Text: " @acme/billing"}}},
		{Key: "b", Cells: []Cell{{Text: "acme/web"}, {Text: "✓ passed"}, {Text: " @acme/billing"}}},
		{Key: "c", Cells: []Cell{{Text: "acme/docs"}, {Text: "✓ passed"}, {Text: " @sam"}}},
		{Key: "d", Cells: []Cell{{Text: "acme/ops"}, {Text: "✓ passed"}, {Text: "Platform team"}}},
	})
	return t
}

func texts(c completion) []string {
	var out []string
	for _, o := range c.options {
		out = append(out, o.text)
	}
	return out
}

func TestHalfTypedColumnNameCompletesToTheField(t *testing.T) {
	c := complete("ow", suggestTable().Info(), false)
	if got := strings.Join(c.texts, ","); got != "owner:" {
		t.Errorf("got %q", got)
	}
}

func TestValuesAfterTheColonAreThoseInTheListMostCommonFirstWithoutIcons(t *testing.T) {
	c := complete("repo:acme/a state:", suggestTable().Info(), false)
	if got := strings.Join(texts(c), ","); got != "passed,failed" {
		t.Errorf("got %q", got)
	}
	if c.options[0].count != 3 {
		t.Errorf("passed counted %d, want 3", c.options[0].count)
	}
	c = complete("owner:", suggestTable().Info(), false)
	if got := strings.Join(texts(c), ","); got != "@acme/billing,@sam,Platform team" {
		t.Errorf("got %q", got)
	}
}

func TestValueCompletionMatchesInsideTheValueAndQuotesSpaces(t *testing.T) {
	c := complete("-owner:plat", suggestTable().Info(), false)
	if len(c.texts) != 1 || c.texts[0] != `-owner:"Platform team"` {
		t.Errorf("got %v", c.texts)
	}
	c = complete("owner:@sam,@acme/b", suggestTable().Info(), false)
	if len(c.texts) != 1 || c.texts[0] != "owner:@sam,@acme/billing" {
		t.Errorf("got %v", c.texts)
	}
}

func TestPlainWordsAndUnknownFieldsOfferNothingUntilAsked(t *testing.T) {
	info := suggestTable().Info()
	if c := complete("billing", info, false); len(c.options) != 0 {
		t.Errorf("plain word offered %v", texts(c))
	}
	if c := complete("colour:", info, false); len(c.options) != 0 {
		t.Errorf("unknown field offered %v", texts(c))
	}
	if c := complete("", info, false); len(c.options) != 0 {
		t.Errorf("empty word offered %v", texts(c))
	}
	if c := complete("", info, true); strings.Join(texts(c), ",") != "repository:,state:,owner:" {
		t.Errorf("asked for fields, got %v", texts(c))
	}
}

func TestTabCompletesTheFieldThenCyclesThroughTheValues(t *testing.T) {
	a := sharedApp(t)
	v := newReposView(a)
	v.rebuild(a)
	a.push(v)
	a.openSearch(true)
	a.input.SetValue("pus")
	a.updateSearch(tea.KeyMsg{Type: tea.KeyTab})
	if got := a.input.Value(); got != "pushed:" {
		t.Fatalf("after tab: %q", got)
	}
	a.suggest = suggestState{}
	a.input.SetValue("repo:")
	a.updateSearch(tea.KeyMsg{Type: tea.KeyTab})
	first := a.input.Value()
	a.updateSearch(tea.KeyMsg{Type: tea.KeyTab})
	if second := a.input.Value(); second == first || !strings.HasPrefix(second, "repo:") {
		t.Errorf("tab twice gave %q then %q, want two different completions", first, second)
	}
}

func TestTheStripShowsTheChosenOptionAndTheKeys(t *testing.T) {
	c := complete("owner:", suggestTable().Info(), false)
	got := ansi.Strip(suggestState{sel: 1}.strip(c, 100))
	for _, want := range []string{"@acme/billing 2", "@sam 1", "tab complete", "← → choose"} {
		if !strings.Contains(got, want) {
			t.Errorf("strip lacks %q: %s", want, got)
		}
	}
}
