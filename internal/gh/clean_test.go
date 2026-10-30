package gh

import (
	"context"
	"net/http"
	"testing"
)

// hostile holds what someone could put in a title or a commit message: a
// clipboard write (OSC 52), a window title, a screen clear, a cursor move
// back over the line and an 8-bit CSI.
const hostile = `fix: \u001b]52;c;cm0gLXJmIH4=\u0007tidy\u001b]0;pwned\u001b\\ the \u001b[2J\u001b[Hbasket\rX\u009b31m`

func TestTextFromGitHubCannotSendEscapesToTheTerminal(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		_, _ = w.Write([]byte(`{"title":"` + hostile + `","body":"line one\r\nline two\tend",
			"labels":[{"name":"` + hostile + `"}],"extra":{"note":"` + hostile + `","list":["` + hostile + `"]}}`))
	})
	var v struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []*struct {
			Name string `json:"name"`
		} `json:"labels"`
		Extra map[string]any `json:"extra"`
	}
	for _, read := range []func() error{
		func() error { return c.Get(context.Background(), "repos/o/r/issues/1", &v) },
		func() error {
			if !c.Cached("repos/o/r/issues/1", &v) {
				t.Fatal("the response should be cached")
			}
			return nil
		},
	} {
		if err := read(); err != nil {
			t.Fatal(err)
		}
		const want = "fix: tidy the basketX31m"
		if v.Title != want || v.Labels[0].Name != want {
			t.Errorf("title %q, label %q, want %q", v.Title, v.Labels[0].Name, want)
		}
		if v.Extra["note"] != want || v.Extra["list"].([]any)[0] != want {
			t.Errorf("values without a type should be cleaned too: %#v", v.Extra)
		}
		if v.Body != "line one\nline two\tend" {
			t.Errorf("newlines and tabs should stay, got %q", v.Body)
		}
	}
}

func TestALogKeepsItsColoursButNoOtherEscapes(t *testing.T) {
	raw := "2026-10-12T10:00:00.0000000Z \x1b[1;31mFAIL\x1b[0m src/basket.test.ts\x1b]52;c;cm0gLXJmIH4=\x07\x1b[2K\r\tdone\x9b31m\n"
	lines := ParseLog(raw)
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
	if want := "\x1b[1;31mFAIL\x1b[0m src/basket.test.ts\tdone31m"; lines[0].Text != want {
		t.Errorf("got %q, want %q", lines[0].Text, want)
	}
}

func TestAWorkflowFileIsCleaned(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		// "name: CI\x1b]0;x\x07\r\non: push\n" in base64
		_, _ = w.Write([]byte(`{"encoding":"base64","content":"bmFtZTogQ0kbXTA7eAcNCm9uOiBwdXNoCg=="}`))
	})
	got, err := c.FileContent(context.Background(), "o/r", ".github/workflows/ci.yml", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "name: CI\non: push\n" {
		t.Errorf("got %q", got)
	}
}
