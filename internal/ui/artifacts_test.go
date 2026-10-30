package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

const artifactsJSON = `{"artifacts":[
 {"id":5,"name":"webshop-dist","size_in_bytes":4400000,"expired":false,"expires_at":"2099-01-01T00:00:00Z"},
 {"id":6,"name":"coverage","size_in_bytes":120000,"expired":true}]}`

func TestARunShowsItsArtifactsAndDownloadsOne(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	withGitHub(t, map[string]string{
		"/repos/o/r/actions/runs/7/artifacts": artifactsJSON,
		"/repos/o/r/actions/artifacts/5/zip":  "PK-zip-bytes",
		"/repos/o/r/actions/runs/7/jobs":      `{"jobs":[]}`,
		"/repos/o/r/actions/runs/7":           `{"id":7,"name":"CI","run_number":3,"status":"completed","conclusion":"success"}`,
	})
	a := accessApp(t, "WRITE", "gho_x")
	run := gh.Run{ID: 7, Name: "CI", RunNumber: 3, Status: "completed", Conclusion: "success"}
	v := newRunView(a, "o/r", run)
	drive(a, a.push(v), 5*time.Second)
	_ = a.View()
	out := screen(a)
	for _, want := range []string{"ARTIFACTS 2", "webshop-dist", "4.2 MB", "expires in", "coverage", "expired"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run lacks %q:\n%s", want, out)
		}
	}
	pressKeys(a, runes("A"))
	if _, ok := a.modal.(*pickerModal); !ok {
		t.Fatalf("A should pick an artifact, modal is %T", a.modal)
	}
	pressKeys(a, tea.KeyMsg{Type: tea.KeyEnter}, runes("y"))
	deadline := time.Now().Add(3 * time.Second)
	path := filepath.Join(home, "Downloads", "webshop-dist.zip")
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && string(b) == "PK-zip-bytes" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("the artifact was not saved to %s", path)
}

func TestAnExpiredArtifactCannotBeDownloaded(t *testing.T) {
	a := accessApp(t, "WRITE", "gho_x")
	cmd := a.pickArtifact("o/r", gh.Run{Name: "CI"}, []gh.Artifact{{ID: 6, Name: "coverage", Expired: true}})
	f, ok := cmd().(flashMsg)
	if !ok || f.kind != nHint || !strings.Contains(f.text, "has expired") {
		t.Errorf("got %+v", f)
	}
}

func TestSizesAndExpiryAreSaidPlainly(t *testing.T) {
	if bytesText(4400000) != "4.2 MB" || bytesText(2048) != "2 KB" || bytesText(12) != "12 B" {
		t.Errorf("%s %s %s", bytesText(4400000), bytesText(2048), bytesText(12))
	}
	now := time.Now()
	if got := expiryText(gh.Artifact{ExpiresAt: now.Add(12*24*time.Hour + time.Hour)}, now); got != "expires in 12d" {
		t.Errorf("got %q", got)
	}
}
