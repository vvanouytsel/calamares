package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// downloadsDir is where files you save go: the Downloads folder, or the
// temp folder when there is none; never silently the working directory.
func downloadsDir() string {
	dir := os.TempDir()
	if home, err := os.UserHomeDir(); err == nil {
		if st, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && st.IsDir() {
			dir = filepath.Join(home, "Downloads")
		}
	}
	return dir
}

// bytesText says a size: "4.2 MB".
func bytesText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// expiryText says when an artifact goes away, or that it already has.
func expiryText(x gh.Artifact, now time.Time) string {
	switch {
	case x.Expired:
		return "expired"
	case x.ExpiresAt.IsZero():
		return ""
	case x.ExpiresAt.Before(now):
		return "expired"
	}
	d := x.ExpiresAt.Sub(now)
	if d >= 48*time.Hour {
		return fmt.Sprintf("expires in %dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("expires in %dh", int(d.Hours()))
}

type artifactsMsg struct {
	id   int
	key  string
	arts []gh.Artifact
	err  error
}

func (m artifactsMsg) viewID() int { return m.id }

// loadArtifacts reads the artifacts of the run on display once it has
// completed (a run that is still going may get more), once per attempt.
func (v *runView) loadArtifacts(a *App) tea.Cmd {
	run := v.run
	if run.Status != "completed" || v.artsBusy {
		return nil
	}
	key := fmt.Sprintf("%d/%d", run.ID, run.RunAttempt)
	if v.artsFor == key {
		return nil
	}
	v.artsBusy = true
	id, repo := v.id, v.repo
	return func() tea.Msg {
		arts, err := a.client.ListArtifacts(context.Background(), repo, run.ID)
		return artifactsMsg{id, key, arts, err}
	}
}

// artifactLines draws the ARTIFACTS block under the jobs.
func (v *runView) artifactLines(w int) []string {
	if len(v.arts) == 0 {
		return nil
	}
	now := time.Now()
	lines := []string{sDim.Render(fmt.Sprintf(" ARTIFACTS %d", len(v.arts)))}
	for _, x := range v.arts {
		right := bytesText(x.Size)
		if e := expiryText(x, now); e != "" {
			right += " · " + e
		}
		name := fit(x.Name, max(w-width(right)-3, 6))
		if x.Expired {
			lines = append(lines, clip(" "+sDim.Render(name+" "+right), w))
			continue
		}
		lines = append(lines, clip(" "+sText.Render(name)+" "+sDim.Render(right), w))
	}
	return lines
}

// downloadArtifactsKey is the binding that downloads a run's artifacts.
func (a *App) downloadArtifactsKey(repo string, run gh.Run, held []gh.Artifact, keys []string) Binding {
	help := "download artifacts…"
	if n := len(held); n > 0 {
		help = fmt.Sprintf("download artifacts (%d)…", n)
	}
	return Binding{Keys: keys, Desc: "artifacts", Help: help, Off: run.Active(), Need: gh.CapRead, Repo: repo,
		Do: do(func() tea.Cmd { return a.downloadArtifacts(repo, run, held) })}
}

// downloadArtifacts picks an artifact of the run and downloads it.
func (a *App) downloadArtifacts(repo string, run gh.Run, held []gh.Artifact) tea.Cmd {
	if run.Active() {
		return a.flashCmd(nHint, "The run is still going: its artifacts show when it has finished.")
	}
	if len(held) == 0 {
		return func() tea.Msg {
			arts, err := a.client.ListArtifacts(context.Background(), repo, run.ID)
			if err != nil {
				return flashMsg{nError, "Could not list the artifacts: " + err.Error()}
			}
			if len(arts) == 0 {
				return flashMsg{nHint, "This run has no artifacts."}
			}
			return a.pickArtifact(repo, run, arts)()
		}
	}
	return a.pickArtifact(repo, run, held)
}

func (a *App) pickArtifact(repo string, run gh.Run, arts []gh.Artifact) tea.Cmd {
	choose := func(x gh.Artifact) tea.Cmd {
		now := time.Now()
		if x.Expired || (!x.ExpiresAt.IsZero() && x.ExpiresAt.Before(now)) {
			return a.flashCmd(nHint, fmt.Sprintf("%s has expired: GitHub no longer keeps it.", x.Name))
		}
		return confirm("Download artifact?", []string{
			sWhite.Render(x.Name) + sDim.Render(" · "+bytesText(x.Size)),
			sDim.Render(fmt.Sprintf("%s #%d", run.Name, run.RunNumber)),
			"",
			"It will be saved in " + downloadsDir() + ".",
		}, "Download", false, func() tea.Cmd { return a.startDownload(repo, x) })
	}
	if len(arts) == 1 {
		return choose(arts[0])
	}
	byID := map[string]gh.Artifact{}
	var items []pickItem
	now := time.Now()
	for _, x := range arts {
		id := strconv.FormatInt(x.ID, 10)
		byID[id] = x
		note := bytesText(x.Size)
		if e := expiryText(x, now); e != "" {
			note += " · " + e
		}
		items = append(items, pickItem{value: id, label: x.Name, note: sDim.Render(note)})
	}
	return openModal(newPicker("artifact", "Download which artifact?", "", items, func(id string) tea.Cmd { return choose(byID[id]) }))
}

// downloadMsg is progress of a download, or its end.
type downloadMsg struct {
	ch       chan downloadMsg
	name     string
	done     int64
	total    int64
	finished bool
	path     string
	err      error
}

// startDownload saves an artifact to the Downloads folder and shows how far it is in the key bar.
func (a *App) startDownload(repo string, x gh.Artifact) tea.Cmd {
	return a.downloadFile(unsafeName.ReplaceAllString(x.Name, "_")+".zip", x.Size, func(ctx context.Context, w io.Writer, progress func(done, total int64)) error {
		return a.client.DownloadArtifact(ctx, repo, x.ID, w, progress)
	})
}

// downloadFile saves what fetch writes to name in the Downloads folder.
func (a *App) downloadFile(name string, size int64, fetch func(ctx context.Context, w io.Writer, progress func(done, total int64)) error) tea.Cmd {
	path := filepath.Join(downloadsDir(), name)
	ch := make(chan downloadMsg, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		f, err := os.Create(path)
		if err != nil {
			ch <- downloadMsg{ch: ch, name: name, finished: true, err: err}
			return
		}
		err = fetch(ctx, f, func(done, total int64) {
			select {
			case ch <- downloadMsg{ch: ch, name: name, done: done, total: total}:
			default: // the screen is behind: it only needs the latest
			}
		})
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(path)
		}
		ch <- downloadMsg{ch: ch, name: name, finished: true, path: path, err: err, total: size}
	}()
	a.flash, a.flashKind, a.flashAt = "Downloading "+name+" …", nInfo, time.Now()
	return waitDownload(ch)
}

func waitDownload(ch chan downloadMsg) tea.Cmd { return func() tea.Msg { return <-ch } }

// handleDownload shows progress in the key bar, and the end as a chip.
func (a *App) handleDownload(m downloadMsg) tea.Cmd {
	switch {
	case m.err != nil:
		a.setFlash(nError, "Could not download "+m.name+": "+m.err.Error())
		return nil
	case m.finished:
		where := filepath.Dir(m.path)
		if filepath.Base(where) == "Downloads" {
			where = "Downloads"
		}
		a.setFlash(nDone, "Saved "+m.name+" to "+where)
		return nil
	}
	text := "Downloading " + m.name + " · " + bytesText(m.done)
	if m.total > 0 {
		text += " of " + bytesText(m.total)
	}
	a.flash, a.flashKind, a.flashAt = text, nInfo, time.Now()
	return waitDownload(m.ch)
}
