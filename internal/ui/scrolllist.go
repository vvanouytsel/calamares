package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// listItem is a block of lines in a scrollList.
type listItem struct {
	id         string
	lines      []string
	selected   []string // drawn instead of lines while the item is selected
	selectable bool
	data       any
}

// scrollList is a column of items of different heights with one selected
// item. Items are drawn with a two-cell gutter that marks the selection.
type scrollList struct {
	items  []listItem
	cursor int
	offset int
	h      int
	follow bool
	tops   []int
}

// setItems replaces the items, keeping the selection on the item with the same id.
func (l *scrollList) setItems(items []listItem) {
	id := ""
	if l.cursor < len(l.items) {
		id = l.items[l.cursor].id
	}
	l.items = items
	l.tops = nil
	for i, it := range items {
		if id != "" && it.id == id {
			l.cursor = i
			return
		}
	}
	l.cursor = min(l.cursor, max(len(items)-1, 0))
	l.settle(1)
}

// settle moves the cursor onto a selectable item, looking in direction dir first.
func (l *scrollList) settle(dir int) {
	if len(l.items) == 0 {
		return
	}
	for _, d := range []int{dir, -dir} {
		for i := l.cursor; i >= 0 && i < len(l.items); i += d {
			if l.items[i].selectable {
				l.cursor = i
				return
			}
		}
	}
}

func (l *scrollList) selectedItem() (listItem, bool) {
	if l.cursor < 0 || l.cursor >= len(l.items) || !l.items[l.cursor].selectable {
		return listItem{}, false
	}
	return l.items[l.cursor], true
}

func (l *scrollList) layout() {
	if len(l.tops) == len(l.items) {
		return
	}
	l.tops = make([]int, len(l.items)+1)
	for i, it := range l.items {
		l.tops[i+1] = l.tops[i] + len(it.lines)
	}
	l.tops = l.tops[:len(l.items)]
}

func (l *scrollList) totalLines() int {
	if len(l.items) == 0 {
		return 0
	}
	l.layout()
	last := len(l.items) - 1
	return l.tops[last] + len(l.items[last].lines)
}

func (l *scrollList) top(i int) int    { l.layout(); return l.tops[i] }
func (l *scrollList) bottom(i int) int { return l.top(i) + len(l.items[i].lines) }

// step selects the next selectable item in direction dir; inside an item taller
// than the view it scrolls first.
func (l *scrollList) step(dir int) {
	if len(l.items) == 0 {
		return
	}
	if l.h > 0 && l.cursor < len(l.items) {
		switch {
		case dir > 0 && l.bottom(l.cursor) > l.offset+l.h:
			l.offset++
			return
		case dir < 0 && l.top(l.cursor) < l.offset:
			l.offset--
			return
		}
	}
	for i := l.cursor + dir; i >= 0 && i < len(l.items); i += dir {
		if l.items[i].selectable {
			l.cursor, l.follow = i, true
			return
		}
	}
}

func (l *scrollList) scrollPage(dir int) {
	page := max(l.h-1, 1)
	l.offset = max(min(l.offset+dir*page, l.totalLines()-l.h), 0)
	for i := range l.items {
		if l.items[i].selectable && l.bottom(i) > l.offset && l.top(i) >= l.offset {
			l.cursor = i
			return
		}
	}
	l.follow = false
}

func (l *scrollList) first() {
	l.cursor, l.offset, l.follow = 0, 0, true
	l.settle(1)
}

func (l *scrollList) last() {
	l.cursor, l.follow = max(len(l.items)-1, 0), true
	l.settle(-1)
}

// handleNav applies the movement keys; it reports whether the key was used.
func (l *scrollList) handleNav(key string) bool {
	switch key {
	case "up", "k":
		l.step(-1)
	case "down", "j":
		l.step(1)
	case "pgup", "ctrl+b":
		l.scrollPage(-1)
	case "pgdown", "ctrl+f", " ":
		l.scrollPage(1)
	case "home", "g":
		l.first()
	case "end", "G":
		l.last()
	default:
		return false
	}
	return true
}

// render draws h lines, each w cells wide; item lines must fit w-2.
func (l *scrollList) render(w, h int) []string {
	l.h = h
	if len(l.items) > 0 {
		l.cursor = min(max(l.cursor, 0), len(l.items)-1)
		if l.follow {
			top, bottom := l.top(l.cursor), l.bottom(l.cursor)
			if top < l.offset || bottom-top > h {
				l.offset = top
			} else if bottom > l.offset+h {
				l.offset = bottom - h
			}
			l.follow = false
		}
	}
	l.offset = max(min(l.offset, l.totalLines()-h), 0)

	out := make([]string, 0, h)
	for i, it := range l.items {
		if l.bottom(i) <= l.offset {
			continue
		}
		lines, mark := it.lines, "  "
		if i == l.cursor && it.selectable {
			mark = sCyan.Render("▌") + " "
			if it.selected != nil {
				lines = it.selected
			}
		}
		for n, line := range lines {
			if l.top(i)+n < l.offset {
				continue
			}
			if len(out) == h {
				return out
			}
			out = append(out, fit(mark+line, w))
		}
	}
	for len(out) < h {
		out = append(out, strings.Repeat(" ", w))
	}
	return out
}

// moveKeys moves through the list; space pages down too.
func (l *scrollList) moveKeys(desc string) Binding {
	b := moveBinding(desc, func(k string) tea.Cmd { l.handleNav(k); return nil })
	b.Keys = append(append([]string{}, b.Keys...), " ")
	return b
}
