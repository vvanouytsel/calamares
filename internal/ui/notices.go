package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// notice is something Calamares said in the key bar: what an action did, or
// what went wrong. The key bar shows one for a few seconds; the Messages
// screen keeps them all for the session.
type notice struct {
	at   time.Time
	kind noticeKind
	text string
}

// noticeKind is what sort of thing a message is: it picks the chip, its icon
// and colour, and how long the message stays in the key bar.
type noticeKind int

const (
	nInfo    noticeKind = iota // something changed, or good to know
	nDone                      // an action worked
	nHint                      // your key did nothing, and why
	nError                     // something went wrong
	nStarted                   // a run you watch has started
)

func (k noticeKind) word() string {
	return [...]string{"info", "done", "hint", "error", "started"}[k]
}

func (k noticeKind) color() lipgloss.Color {
	return [...]lipgloss.Color{cBlue, cGreen, cAmber, cRed, cAmber}[k]
}

// glyph is the icon in the chip: a Nerd Font glyph, an ordinary symbol, or a
// letter where the terminal can't draw symbols.
func (k noticeKind) glyph() string {
	switch {
	case ascii:
		return [...]string{"i", "v", "!", "x", ">"}[k]
	case nerd:
		return [...]string{"", "", "", "", ""}[k]
	}
	return [...]string{"i", "✓", "!", "✗", "▶"}[k]
}

// chip is the kind as a small label on its colour: " ✓ done ".
func (k noticeKind) chip() string {
	return pill(k.glyph()+" "+k.word(), k.color(), cOnAccent)
}

// lifetime is how long the key bar shows a message of this kind.
func (k noticeKind) lifetime() time.Duration {
	if k == nError {
		return 10 * time.Second
	}
	return 6 * time.Second
}

// maxNotices is how many messages are kept; the oldest go first.
const maxNotices = 200

// note keeps a message for the Messages screen.
func (a *App) note(kind noticeKind, text string) {
	if n := len(a.notices); n > 0 && a.notices[n-1].text == text && time.Since(a.notices[n-1].at) < 2*time.Second {
		return
	}
	a.notices = append(a.notices, notice{time.Now(), kind, text})
	if len(a.notices) > maxNotices {
		a.notices = a.notices[len(a.notices)-maxNotices:]
	}
	if _, reading := a.top().(*noticesView); kind == nError && !reading {
		a.unreadErrors++
	}
}

// countedWindow is how long after a request that failed another one that
// fails the same way still counts in the same message.
const countedWindow = 2 * time.Minute

// countedNote is a message that counts the requests that failed one way.
type countedNote struct {
	n  int
	at time.Time
}

// noteCounted tells that a request failed in a way many requests fail at
// once (GitHub did not answer, or failed on its side): the first time as a
// message of its own, then by counting up in that message, so one outage is
// one error and not one per repo. It returns the message.
func (a *App) noteCounted(c *countedNote, prefix, how string) string {
	now := time.Now()
	if now.Sub(c.at) >= countedWindow {
		c.n = 0
	}
	c.n++
	c.at = now
	text := fmt.Sprintf("%s: %d %s %s · ctrl+r tries again", prefix, c.n, plural("request", c.n), how)
	if c.n > 1 {
		for i := len(a.notices) - 1; i >= 0; i-- {
			if strings.HasPrefix(a.notices[i].text, prefix) {
				a.notices[i].at, a.notices[i].text = now, text
				return text
			}
		}
	}
	a.note(nError, text)
	return text
}

// openNotices shows the Messages screen, or closes it when it is open.
func (a *App) openNotices() tea.Cmd {
	if _, ok := a.top().(*noticesView); ok {
		return a.pop()
	}
	a.unreadErrors = 0
	return a.push(newNoticesView(a))
}

// noticesView is the Messages screen: everything the key bar said this
// session, newest first, so an error can still be read after it is gone.
type noticesView struct {
	id int
	t  *Table
}

func newNoticesView(a *App) *noticesView {
	v := &noticesView{id: a.newID()}
	v.t = newTable(
		Column{Title: "TIME"},
		Column{Title: "KIND"},
		Column{Title: "MESSAGE", Flex: true},
		Column{Title: "AGO", Right: true},
	)
	v.t.Empty = "Nothing yet. What Calamares says in the bottom right corner is kept here."
	return v
}

func (v *noticesView) ID() int                            { return v.id }
func (v *noticesView) Init(a *App) tea.Cmd                { return nil }
func (v *noticesView) Crumb() string                      { return "messages" }
func (v *noticesView) Refresh(a *App, force bool) tea.Cmd { return nil }

func (v *noticesView) SetFilter(a *App, q string)   { v.t.SetFilter(q, a.me) }
func (v *noticesView) FilterValue() string          { return v.t.Filter() }
func (v *noticesView) FilterInfo(a *App) filterInfo { return v.t.Info() }

func (v *noticesView) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		cmd, _ := a.press(v.Keys(a), k.String())
		return cmd
	}
	return nil
}

// selected is the message under the cursor.
func (v *noticesView) selected(a *App) (notice, bool) {
	r := v.t.Selected()
	if r == nil {
		return notice{}, false
	}
	for i, n := range a.notices {
		if fmt.Sprint(i) == r.Key {
			return n, true
		}
	}
	return notice{}, false
}

func (v *noticesView) Keys(a *App) []Binding {
	var bs []Binding
	if n, ok := v.selected(a); ok {
		bs = append(bs,
			Binding{Keys: []string{"enter", "right"}, Label: "enter", Desc: "read in full", Do: do(func() tea.Cmd {
				return openModal(&messageModal{
					title:  n.kind.chip(),
					header: []string{sDim.Render(n.at.Local().Format("15:04:05") + "  ·  " + age(n.at, time.Now()) + " ago")},
					body:   n.text,
				})
			})},
			Binding{Keys: []string{"y"}, Desc: "copy", Help: "copy the message", Do: do(func() tea.Cmd { return a.copyText(n.text, "message") })},
		)
	}
	return grouped("MESSAGES", append(bs, filterHint, v.t.moveKeys())...)
}

// sync lists the messages, newest first.
func (v *noticesView) sync(a *App) {
	a.unreadErrors = 0
	rows := make([]Row, 0, len(a.notices))
	for i := len(a.notices) - 1; i >= 0; i-- {
		n := a.notices[i]
		row := KOK
		if n.kind == nError {
			row = KFail
		}
		label := " " + n.kind.glyph() + " " + n.kind.word() + " "
		ago := age(n.at, time.Now()) + " ago"
		rows = append(rows, Row{Key: fmt.Sprint(i), Kind: row, Cells: []Cell{
			{Text: n.at.Local().Format("15:04:05")},
			{Text: label, Styled: n.kind.chip()},
			{Text: n.text},
			{Text: ago, Styled: sDim.Render(ago)},
		}})
	}
	v.t.SetRows(rows)
}

func (v *noticesView) Render(a *App, w, h int) string {
	body := " " + a.filterLine(v.t.Filter(), v.FilterInfo(a)) + "\n" + v.t.Render(w-2, h-3)
	return box(title("Messages", "what Calamares told you this session", v.t.Len(), ""), body, w, h, cFrame)
}
