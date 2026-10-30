package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Column describes one table column.
type Column struct {
	Title  string
	Max    int  // preferred maximum; exceeded when the table has room to spare. 0 = no limit
	Min    int  // reserved width, so values that tick (ages, durations) don't shift the columns after them
	Flex   bool // shrinks first when space is short
	Right  bool
	MidCut bool // truncate in the middle, keeping the end, which is often what tells values apart
	Below  bool // in two-line mode, shown on its own line under the rest of the row
	Hidden bool
	// HideEmpty hides the column while none of the rows has a value in it.
	HideEmpty bool
	// Drop lets the column go when the table is too narrow even after
	// shrinking; the highest Drop goes first. 0 never drops.
	Drop int
	// Help says what the column holds, for the screen's explanation (i).
	Help string
}

// Cell holds the plain text (used for width, filtering and the selected row)
// and an optional styled rendering.
type Cell struct {
	Text   string
	Styled string
}

// Row is one table line. Kind decides the colour of the whole row.
type Row struct {
	Key   string
	Cells []Cell
	Kind  Kind
	Color *Kind // overrides Kind for colouring only
}

// Table is a filterable, scrollable k9s-style table.
type Table struct {
	Cols   []Column
	rows   []Row
	view   []int
	cursor int
	offset int
	filter string
	me     string
	Empty  string
	blur   bool // the table's pane isn't focused: show the cursor quietly

	// TwoLine puts the Below column on a second line under each row.
	TwoLine bool
	// Detail returns lines describing a row in full, shown under the table
	// for the row under the cursor.
	Detail func(Row) []string
	// HideDetail keeps Detail from showing, when a panel beside the table says it.
	HideDetail bool
	// Card, when set and Cards is on, draws a row as two lines of w cells
	// instead of as columns: what it is, then where it comes from.
	Card  func(r Row, w int) (first, second string)
	Cards bool

	marks map[string]bool // rows ticked for an action on several at once
	// Tickable says rows can be ticked: they get a checkbox while some are.
	Tickable bool
	// A range being ticked with shift: where it started, and the rows that
	// were ticked before it, which it grows on and shrinks back to.
	ranging   bool
	rangeFrom int
	rangeBase map[string]bool

	kinds   map[string]Kind  // each row's status as last set, to see what changed
	flashes map[string]flash // rows that just passed or failed

	bodyRows int   // rows that fit in the last render
	rowLines int   // screen lines per row in the last render
	floor    []int // widest content seen per column since the last reset
}

func newTable(cols ...Column) *Table {
	return &Table{Cols: cols, Empty: "Loading…"}
}

// SetRows replaces the rows, keeping the cursor on the same key when possible.
func (t *Table) SetRows(rows []Row) {
	key := ""
	if r := t.Selected(); r != nil {
		key = r.Key
	}
	t.noteKinds(rows)
	t.rows = rows
	t.apply()
	if key != "" {
		for i, idx := range t.view {
			if t.rows[idx].Key == key {
				t.cursor = i
				break
			}
		}
	}
	t.clamp()
}

// SetFilter filters rows by a query (see query); field names match column
// titles by prefix.
func (t *Table) SetFilter(q, me string) {
	t.endRange()
	t.filter = q
	t.me = me
	t.apply()
	t.cursor = 0
	t.offset = 0
	t.ResetWidths()
}

// ResetWidths lets columns shrink back to their content. Until then a column
// never gets narrower, so rows coming and going don't shift the columns.
func (t *Table) ResetWidths() { t.floor = nil }

func (t *Table) Filter() string { return t.filter }

// FilteredKeys returns the keys of the rows the filter keeps, in order.
func (t *Table) FilteredKeys() []string {
	out := make([]string, 0, len(t.view))
	for _, i := range t.view {
		out = append(out, t.rows[i].Key)
	}
	return out
}

// VisibleKeys returns the keys of the rows shown in the last render.
func (t *Table) VisibleKeys() []string {
	var out []string
	for vi := t.offset; vi < len(t.view) && vi < t.offset+t.bodyRows; vi++ {
		out = append(out, t.rows[t.view[vi]].Key)
	}
	return out
}

// Col returns the column with this title, or nil.
func (t *Table) Col(title string) *Column {
	for i := range t.Cols {
		if t.Cols[i].Title == title {
			return &t.Cols[i]
		}
	}
	return nil
}

func (t *Table) apply() {
	t.view = t.view[:0]
	q := parseQuery(t.filter, t.me)
	for i, r := range t.rows {
		if q.match(t.cellValues(r)) {
			t.view = append(t.view, i)
		}
	}
}

func (t *Table) cellValues(r Row) func(field string) []string {
	return func(field string) []string {
		var out []string
		for i, c := range r.Cells {
			if field == "" || i < len(t.Cols) && strings.HasPrefix(strings.ToLower(t.Cols[i].Title), field) {
				out = append(out, c.Text)
			}
		}
		return out
	}
}

func (t *Table) Len() int        { return len(t.view) }
func (t *Table) Total() int      { return len(t.rows) }
func (t *Table) Cursor() int     { return t.cursor }
func (t *Table) SetCursor(i int) { t.cursor = i; t.clamp() }

// Selected returns the row under the cursor.
func (t *Table) Selected() *Row {
	if t.cursor < 0 || t.cursor >= len(t.view) {
		return nil
	}
	return &t.rows[t.view[t.cursor]]
}

// SelectKey puts the cursor on the row with key, and says whether one is listed.
func (t *Table) SelectKey(key string) bool {
	for i, ri := range t.view {
		if t.rows[ri].Key == key {
			t.cursor = i
			return true
		}
	}
	return false
}

// Move moves the cursor by d rows.
func (t *Table) Move(d int) { t.cursor += d; t.clamp() }
func (t *Table) Top()       { t.cursor = 0; t.clamp() }
func (t *Table) Bottom()    { t.cursor = len(t.view) - 1; t.clamp() }
func (t *Table) Page(d int) {
	t.Move(d * max(t.bodyRows, 1))
}

func (t *Table) clamp() {
	if t.cursor >= len(t.view) {
		t.cursor = len(t.view) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// handleNav applies common movement keys; it reports whether the key was used.
func (t *Table) handleNav(key string) bool {
	t.endRange()
	switch key {
	case "up", "k":
		t.Move(-1)
	case "down", "j":
		t.Move(1)
	case "pgup", "ctrl+b":
		t.Page(-1)
	case "pgdown", "ctrl+f":
		t.Page(1)
	case "home", "g":
		t.Top()
	case "end", "G":
		t.Bottom()
	default:
		return false
	}
	return true
}

// Render draws the header, the visible rows and, when there is room, the
// detail of the selected row into w×h cells.
func (t *Table) Render(w, h int) string {
	t.bodyRows, t.rowLines = 0, 1
	if h <= 0 {
		return ""
	}
	if t.Card != nil && t.Cards {
		return t.renderCards(w, h)
	}
	cols := t.lineCols()
	ticking := t.Tickable && len(t.marks) > 0
	lw := w
	if ticking {
		lw = w - checkboxWidth
	}
	widths := t.widths(lw, cols)
	for t.squeezed(widths, cols) {
		drop := -1
		for n, i := range cols {
			if d := t.Cols[i].Drop; d > 0 && (drop < 0 || d > t.Cols[cols[drop]].Drop) {
				drop = n
			}
		}
		if drop < 0 {
			break
		}
		cols = slices.Delete(slices.Clone(cols), drop, drop+1)
		widths = t.widths(lw, cols)
	}
	var hdr strings.Builder
	if ticking {
		hdr.WriteString(strings.Repeat(" ", checkboxWidth))
	}
	for _, i := range cols {
		hdr.WriteString(" " + alignCell(t.Cols[i].Title, widths[i], t.Cols[i].Right) + " ")
	}
	lines := []string{sHeader.Render(fit(hdr.String(), w))}

	if len(t.view) == 0 {
		msg := t.Empty
		if len(t.rows) > 0 {
			msg = "No rows match the filter."
		}
		if msg == "Loading…" && animLevel >= animFull {
			return strings.Join(append(lines, skeleton(w, h-1)...), "\n")
		}
		return strings.Join(append(lines, emptyLines(msg)...), "\n")
	}

	below, indent := t.belowCol(), 0
	if below >= 0 {
		t.rowLines = 2
		if len(cols) > 0 {
			indent = widths[cols[0]] + 3
		}
	}
	body := h - 1
	detail := t.detailLines()
	if len(detail) > 0 && body-len(detail)-1 >= 3*t.rowLines {
		body -= len(detail) + 1
	} else {
		detail = nil
	}
	t.bodyRows = body / t.rowLines
	t.scroll()

	for vi := t.offset; vi < len(t.view) && vi < t.offset+t.bodyRows; vi++ {
		r := t.rows[t.view[vi]]
		selected := vi == t.cursor
		line := t.renderLine(r, cols, widths, w, selected, ticking)
		if !selected {
			line = t.glow(r, line)
		}
		lines = append(lines, line)
		if below >= 0 {
			lines = append(lines, t.renderBelow(r, below, indent, w, selected))
		}
	}
	if len(detail) > 0 {
		for len(lines) < 1+body {
			lines = append(lines, "")
		}
		lines = append(lines, sDim.Render(strings.Repeat("─", w)))
		for _, l := range detail {
			lines = append(lines, clip(l, w))
		}
	}
	return strings.Join(lines, "\n")
}

func (t *Table) scroll() {
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.bodyRows {
		t.offset = t.cursor - t.bodyRows + 1
	}
	if t.offset > max(len(t.view)-t.bodyRows, 0) {
		t.offset = max(len(t.view)-t.bodyRows, 0)
	}
	t.offset = max(t.offset, 0)
}

// squeezeLimit is how narrow a column may get before a column with Drop
// makes way for it.
const squeezeLimit = 16

// squeezed reports whether a column got narrower than squeezeLimit (or than
// its widest value, if that's shorter) to make the table fit.
func (t *Table) squeezed(widths, cols []int) bool {
	for _, i := range cols {
		want := t.floor[i]
		if m := t.Cols[i].Max; m > 0 {
			want = min(want, m)
		}
		if widths[i] < min(want, squeezeLimit) {
			return true
		}
	}
	return false
}

// lineCols lists the columns drawn on a row's first line, in order.
func (t *Table) lineCols() []int {
	below := t.belowCol()
	var out []int
	for i, c := range t.Cols {
		if !c.Hidden && i != below && !(c.HideEmpty && t.empty(i)) {
			out = append(out, i)
		}
	}
	return out
}

func (t *Table) empty(col int) bool {
	for _, idx := range t.view {
		if r := t.rows[idx]; col < len(r.Cells) && r.Cells[col].Text != "" {
			return false
		}
	}
	return true
}

// belowCol is the column moved to a second line, or -1 when rows are one line.
func (t *Table) belowCol() int {
	if !t.TwoLine {
		return -1
	}
	for i, c := range t.Cols {
		if c.Below && !c.Hidden {
			return i
		}
	}
	return -1
}

func (t *Table) detailLines() []string {
	r := t.Selected()
	if t.Detail == nil || r == nil || t.HideDetail {
		return nil
	}
	return t.Detail(*r)
}

func (t *Table) cell(r Row, i int) Cell {
	if i < len(r.Cells) {
		return r.Cells[i]
	}
	return Cell{}
}

func (t *Table) renderLine(r Row, cols, widths []int, w int, selected, ticking bool) string {
	var line strings.Builder
	marked := ticking && t.marks[r.Key]
	if ticking {
		line.WriteString(" " + checkbox(marked))
	}
	rs := fg(r.color().RowColor())
	for _, i := range cols {
		c := t.cell(r, i)
		if c.Styled != "" {
			// A cell too wide is cut with its colours kept.
			line.WriteString(" " + t.Cols[i].render(c.Styled, widths[i]) + " ")
		} else {
			line.WriteString(" " + rs.Render(t.Cols[i].render(c.Text, widths[i])) + " ")
		}
	}
	if selected {
		return t.landed(fit(line.String(), w))
	}
	if marked {
		return withBg(fit(line.String(), w), markedBg())
	}
	return fit(line.String(), w)
}

// checkboxWidth is the room a checkbox takes at the left of a row while
// rows are ticked.
const checkboxWidth = 2

// checkbox is ticked in the accent, or an empty box.
func checkbox(ticked bool) string {
	on, off := "■", "□"
	switch {
	case ascii:
		on, off = "x", "."
	case nerd:
		on, off = "󰄵", "󰄱"
	}
	if ticked {
		return fg(cCyan).Bold(true).Render(on)
	}
	return sDim.Render(off)
}

// markedBg lays a band of the accent under a ticked row.
func markedBg() lipgloss.Color { return lipgloss.Color(mix(string(cBg), string(cCyan), 0.16)) }

// landed draws the row under the cursor; just after the cursor moved onto it
// the accent bar flashes white and the row is a little lighter, settling
// within a fifth of a second. The row itself is in its place at once.
func (t *Table) landed(line string) string {
	p := glowProgress(changedAt(fmt.Sprintf("table:%p", t), t.cursor), cursorGlowMs)
	if p >= 1 || t.blur {
		return selectedLine(line, t.blur)
	}
	e := ease(p)
	if strings.HasPrefix(line, " ") {
		bar := "▌"
		if ascii {
			bar = ">"
		}
		line = fg(lipgloss.Color(mix(string(cWhite), string(cCyan), e))).Render(bar) + line[1:]
	}
	return withSelectionBg(line, lipgloss.Color(mix(string(cSelBg), string(cCyan), 0.3*(1-e))), true)
}

// selectedLine draws line as the row under the cursor: on the selection
// background, with an accent bar in its left margin while its pane has the focus.
func selectedLine(line string, blur bool) string {
	if !blur && strings.HasPrefix(line, " ") {
		line = accentBar() + line[1:]
	}
	return withSelection(line, blur)
}

func (t *Table) renderBelow(r Row, col, indent, w int, selected bool) string {
	text := strings.Repeat(" ", indent) + t.Cols[col].render(t.cell(r, col).Text, max(w-indent-1, 0))
	st := sText
	if r.color().RowColor() == cDim {
		st = sDim
	}
	line := st.Render(fit(text, w))
	if selected {
		return withSelection(line, t.blur)
	}
	return line
}

func (r Row) color() Kind {
	if r.Color != nil {
		return *r.Color
	}
	return r.Kind
}

// render fits plain text s into w cells the way the column wants it.
func (c Column) render(s string, w int) string {
	if c.MidCut && width(s) > w {
		return midCut(s, w)
	}
	return alignCell(s, w, c.Right)
}

// midCut shortens s to w cells by replacing its middle with "…", keeping
// twice as much of the end as of the start.
func midCut(s string, w int) string {
	if width(s) <= w {
		return s
	}
	if w < 5 {
		return fit(s, w)
	}
	head := (w - 1) / 3
	tail := w - 1 - head
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, width(s)-tail, "")
}

func alignCell(s string, w int, right bool) string {
	if right {
		sw := width(s)
		if sw < w {
			return strings.Repeat(" ", w-sw) + s
		}
	}
	return fit(s, w)
}

// widths sizes the columns in cols to fit w cells. Each column wants its
// widest value (never less than it wanted before ResetWidths); Max caps that
// only while the table is short of room.
func (t *Table) widths(w int, cols []int) []int {
	want := make([]int, len(t.Cols))
	for _, i := range cols {
		want[i] = max(width(t.Cols[i].Title), t.Cols[i].Min)
	}
	for _, idx := range t.view {
		r := t.rows[idx]
		for _, i := range cols {
			if i < len(r.Cells) {
				want[i] = max(want[i], width(r.Cells[i].Text))
			}
		}
	}
	if len(t.floor) != len(t.Cols) {
		t.floor = make([]int, len(t.Cols))
	}
	for _, i := range cols {
		want[i] = max(want[i], t.floor[i])
		t.floor[i] = want[i]
	}

	ws := make([]int, len(t.Cols))
	total := 0
	for _, i := range cols {
		ws[i] = want[i]
		if c := t.Cols[i]; c.Max > 0 && ws[i] > c.Max {
			ws[i] = c.Max
		}
		total += ws[i] + 2
	}
	if total < w {
		growCapped(ws, want, cols, w-total)
		return ws
	}
	shrinkWidest(t.Cols, ws, cols, total-w)
	return ws
}

// shrinkWidest takes over cells away one at a time from the widest column,
// flexible columns first (down to 12 cells), then any column (down to 8), so
// long columns end up sharing the space instead of one losing it all.
func shrinkWidest(c []Column, ws, cols []int, over int) {
	for ; over > 0; over-- {
		best := -1
		for _, i := range cols {
			if c[i].Flex && ws[i] > 12 && (best < 0 || ws[i] > ws[best]) {
				best = i
			}
		}
		if best < 0 {
			for _, i := range cols {
				if ws[i] > 8 && (best < 0 || ws[i] > ws[best]) {
					best = i
				}
			}
		}
		if best < 0 {
			return
		}
		ws[best]--
	}
}

// growCapped hands spare cells to the columns Max held back, smallest
// shortfall first, so as many values as possible show in full.
func growCapped(ws, want, cols []int, spare int) {
	short := slices.Clone(cols)
	slices.SortStableFunc(short, func(a, b int) int { return (want[a] - ws[a]) - (want[b] - ws[b]) })
	for _, i := range short {
		add := min(want[i]-ws[i], spare)
		if add <= 0 {
			continue
		}
		ws[i] += add
		spare -= add
	}
}

// renderCards draws every row as a card of two lines with a thin rule
// between the cards, without a header.
func (t *Table) renderCards(w, h int) string {
	if len(t.view) == 0 {
		msg := t.Empty
		if len(t.rows) > 0 {
			msg = "No rows match the filter."
		}
		if msg == "Loading…" && animLevel >= animFull {
			return strings.Join(skeleton(w, h), "\n")
		}
		return strings.Join(emptyLines(msg), "\n")
	}
	t.rowLines = 3
	t.bodyRows = max((h+1)/3, 1)
	t.scroll()
	var lines []string
	rule := fg(cFrame).Render(" " + strings.Repeat("─", max(w-2, 0)))
	for vi := t.offset; vi < len(t.view) && vi < t.offset+t.bodyRows; vi++ {
		if vi > t.offset {
			lines = append(lines, rule)
		}
		first, second := t.Card(t.rows[t.view[vi]], w)
		first, second = fit(first, w), fit(second, w)
		if vi == t.cursor {
			first, second = selectedLine(first, t.blur), selectedLine(second, t.blur)
		}
		lines = append(lines, first, second)
	}
	return strings.Join(lines, "\n")
}

// emptyLines draws the message of an empty list; a message may take several
// lines, and one that starts with the lock icon is an access problem.
func emptyLines(msg string) []string {
	var out []string
	for i, l := range strings.Split(msg, "\n") {
		style := sDim
		if i == 0 && strings.HasPrefix(l, lockMark()) {
			style = sAmber
		}
		out = append(out, " "+style.Render(l))
	}
	return out
}

// ToggleMark ticks or unticks the row under the cursor.
func (t *Table) ToggleMark() {
	t.endRange()
	r := t.Selected()
	if r == nil {
		return
	}
	if t.marks == nil {
		t.marks = map[string]bool{}
	}
	if t.marks[r.Key] {
		delete(t.marks, r.Key)
	} else {
		t.marks[r.Key] = true
	}
}

// MarkAll ticks every row shown (after the filter); when they are all ticked
// already, it unticks them.
func (t *Table) MarkAll() {
	t.endRange()
	if t.marks == nil {
		t.marks = map[string]bool{}
	}
	all := len(t.view) > 0
	for _, idx := range t.view {
		if !t.marks[t.rows[idx].Key] {
			all = false
		}
	}
	for _, idx := range t.view {
		if all {
			delete(t.marks, t.rows[idx].Key)
		} else {
			t.marks[t.rows[idx].Key] = true
		}
	}
}

// ClearMarks unticks everything and reports whether anything was ticked.
func (t *Table) ClearMarks() bool {
	t.endRange()
	had := len(t.marks) > 0
	t.marks = nil
	return had
}

// Marked says whether the row with this key is ticked.
func (t *Table) Marked(key string) bool { return t.marks[key] }

// Marks lists the keys of the ticked rows, in the order of the rows.
func (t *Table) Marks() []string {
	var out []string
	for _, r := range t.rows {
		if t.marks[r.Key] {
			out = append(out, r.Key)
		}
	}
	return out
}

// TickTo moves the cursor to row i and ticks every row from where the range
// started, which is the cursor when no range is being ticked yet. Rows ticked
// before the range stay ticked; rows it passed and left again are unticked.
func (t *Table) TickTo(i int) {
	if len(t.view) == 0 {
		return
	}
	if !t.ranging {
		t.ranging, t.rangeFrom, t.rangeBase = true, t.cursor, map[string]bool{}
		for k := range t.marks {
			t.rangeBase[k] = true
		}
	}
	t.cursor = i
	t.clamp()
	t.marks = map[string]bool{}
	for k := range t.rangeBase {
		t.marks[k] = true
	}
	for j := min(t.rangeFrom, t.cursor); j <= max(t.rangeFrom, t.cursor) && j < len(t.view); j++ {
		t.marks[t.rows[t.view[j]].Key] = true
	}
}

func (t *Table) endRange() { t.ranging, t.rangeBase = false, nil }

// tickRangeKey applies shift+up or K, shift+down or J, shift+home or shift+end.
func (t *Table) tickRangeKey(key string) {
	switch key {
	case "shift+up", "K":
		t.TickTo(t.cursor - 1)
	case "shift+down", "J":
		t.TickTo(t.cursor + 1)
	case "shift+home":
		t.TickTo(0)
	case "shift+end":
		t.TickTo(len(t.view) - 1)
	}
}
