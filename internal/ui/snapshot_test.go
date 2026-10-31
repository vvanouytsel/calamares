package ui

// Snapshot tests drive the real app against the GitHub API and write each
// frame as HTML, so the UI can be reviewed without an interactive terminal.
//
//	CALAMARES_SNAPSHOT=1 CALAMARES_SNAPSHOT_REPO=owner/repo CALAMARES_SNAPSHOT_DIR=.snapshots go test ./internal/ui -run TestSnapshots -v

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// drive runs cmd and everything it triggers until the app is idle.
// Clock ticks are dropped so the loop terminates. Other timers (debounces,
// glow bursts, refreshes after an action, looking for a dispatched run) are
// fed back to the app, but once only timers are pending drive waits at most
// timerGrace for one to fire: the short ones still play out, while the slow
// ones, which reschedule themselves or fire seconds later, end the drive
// instead of holding it until its timeout.
func drive(a *App, cmd tea.Cmd, timeout time.Duration) {
	type result struct {
		msg   tea.Msg
		timer bool
	}
	ch := make(chan result, 4096)
	busy, timers := 0, 0
	var start func(tea.Cmd)
	start = func(c tea.Cmd) {
		if c == nil {
			return
		}
		timer := isTimer(c)
		if timer {
			timers++
		} else {
			busy++
		}
		go func() { ch <- result{c(), timer} }()
	}
	start(cmd)
	deadline := time.After(timeout)
	for busy+timers > 0 {
		var quiet <-chan time.Time
		if busy == 0 {
			quiet = time.After(timerGrace)
		}
		select {
		case r := <-ch:
			if r.timer {
				timers--
			} else {
				busy--
			}
			switch mm := r.msg.(type) {
			case nil, tickMsg, animMsg:
			case tea.BatchMsg:
				for _, c := range mm {
					start(c)
				}
			default:
				_, c := a.Update(r.msg)
				start(c)
			}
		case <-quiet:
			return
		case <-deadline:
			return
		}
	}
}

// timerGrace is how long drive waits on timers alone: longer than the
// longest debounce (the palette search, 400 ms).
const timerGrace = 500 * time.Millisecond

// timerCmds are the functions whose commands only wait: tea.Tick, tea.Every
// and the blink of a text cursor.
var timerCmds = []string{
	"github.com/charmbracelet/bubbletea.Tick.",
	"github.com/charmbracelet/bubbletea.Every.",
	"github.com/charmbracelet/bubbles/cursor.(*Model).BlinkCmd.",
}

// isTimer tells a command that only waits from one that does work, such as
// a request.
func isTimer(c tea.Cmd) bool {
	name := runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
	for _, p := range timerCmds {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func press(a *App, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, c := a.Update(msg)
		drive(a, c, 30*time.Second)
	}
}

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// ansiToHTML converts lipgloss output to coloured HTML spans.
func ansiToHTML(s string) string {
	var b strings.Builder
	fgc, bgc, bold, faint, ul := "", "", false, false, false
	open := false
	flush := func() {
		if open {
			b.WriteString("</span>")
		}
		var st []string
		if fgc != "" {
			st = append(st, "color:"+fgc)
		}
		if bgc != "" {
			st = append(st, "background:"+bgc)
		}
		if bold {
			st = append(st, "font-weight:700")
		}
		if faint {
			st = append(st, "opacity:.55")
		}
		if ul {
			st = append(st, "text-decoration:underline")
		}
		b.WriteString(`<span style="` + strings.Join(st, ";") + `">`)
		open = true
	}
	last := 0
	for _, m := range sgr.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:m[0]]))
		last = m[1]
		codes := strings.Split(s[m[2]:m[3]], ";")
		for i := 0; i < len(codes); i++ {
			switch codes[i] {
			case "", "0":
				fgc, bgc, bold, faint, ul = "", "", false, false, false
			case "1":
				bold = true
			case "2":
				faint = true
			case "4":
				ul = true
			case "22":
				bold, faint = false, false
			case "24":
				ul = false
			case "39":
				fgc = ""
			case "49":
				bgc = ""
			case "38", "48":
				if i+4 < len(codes) && codes[i+1] == "2" {
					r, _ := strconv.Atoi(codes[i+2])
					g, _ := strconv.Atoi(codes[i+3])
					bl, _ := strconv.Atoi(codes[i+4])
					c := fmt.Sprintf("#%02x%02x%02x", r, g, bl)
					if codes[i] == "38" {
						fgc = c
					} else {
						bgc = c
					}
					i += 4
				}
			}
		}
		flush()
	}
	b.WriteString(html.EscapeString(s[last:]))
	if open {
		b.WriteString("</span>")
	}
	return b.String()
}

func TestSnapshots(t *testing.T) {
	repo := os.Getenv("CALAMARES_SNAPSHOT_REPO")
	if os.Getenv("CALAMARES_SNAPSHOT") == "" || repo == "" {
		t.Skip("set CALAMARES_SNAPSHOT=1 and CALAMARES_SNAPSHOT_REPO=owner/repo to render real screens")
	}
	dir := os.Getenv("CALAMARES_SNAPSHOT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	_ = os.MkdirAll(dir, 0o755)
	lipgloss.SetColorProfile(termenv.TrueColor)
	client, err := gh.New("github.com")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Favourites = []string{repo}
	a := New(client, cfg, Options{Version: "dev"})
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 46})
	drive(a, a.Init(), 60*time.Second)

	const style = `<!doctype html><meta charset="utf-8"><style>body{background:#05080f;color:#d8e4f6;font-family:sans-serif;padding:20px}
pre{background:#0a1020;font:13px/1.25 "Cascadia Mono",Consolas,monospace;padding:10px;display:inline-block;border:1px solid #16233b}</style>`
	var pages []string
	snap := func(name string) {
		page := fmt.Sprintf("<h3>%s</h3><pre>%s</pre>", name, ansiToHTML(a.View()))
		pages = append(pages, page)
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d.html", len(pages))), []byte(style+page), 0o644)
	}
	snap("repos (start screen)")
	press(a, "2")
	drive(a, nil, 30*time.Second)
	snap("actions")
	press(a, "tab")
	snap("actions: next view")
	press(a, "3")
	drive(a, nil, 10*time.Second)
	snap("pull requests")
	press(a, "1")
	drive(a, nil, 30*time.Second)
	snap("repos")
	press(a, "/", "s", "k")
	snap("filter /sk on the repos tab")
	press(a, "esc")
	drive(a, a.openRepo(repo, "", 0), 30*time.Second)
	snap("repo: workflows sidebar + all runs")
	press(a, "down")
	time.Sleep(300 * time.Millisecond)
	drive(a, nil, time.Second)
	snap("repo: first workflow selected")
	press(a, "right")
	rv := a.top().(*repoView)
	pane, _ := rv.pane(a)
	for i := 0; i < pane.t.Len(); i++ {
		if r := pane.t.Selected(); r != nil && r.Kind == KFail {
			break
		}
		press(a, "down")
	}
	snap("repo: runs pane focused, failed run selected")
	press(a, "enter")
	snap("run detail")
	press(a, "e")
	press(a, "l")
	snap("log viewer")
	press(a, "/", "F", "A", "I", "L", "enter")
	snap("log search /FAIL")
	press(a, "esc", "esc", "esc")
	press(a, "tab")
	drive(a, nil, 10*time.Second)
	snap("repo: pull requests")
	press(a, "?")
	snap("help")

	out := `<!doctype html><meta charset="utf-8"><style>body{background:#05080f;color:#d8e4f6;font-family:sans-serif;padding:20px}
pre{background:#0a1020;font:13px/1.25 "Cascadia Mono",Consolas,monospace;padding:10px;display:inline-block;border:1px solid #16233b}</style>` + strings.Join(pages, "\n")
	p := filepath.Join(dir, "snapshots.html")
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("wrote", p)
}
