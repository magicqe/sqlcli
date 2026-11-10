package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/db"
)

const maxColWidth = 50

// resultView shows a query result as a scrollable grid, or text: messages,
// errors and DBMS_OUTPUT.
type resultView struct {
	res      *db.Result
	messages []string
	err      string
	output   []string // DBMS_OUTPUT lines
	compile  []string // compilation errors of created PL/SQL objects

	textMode bool // show the text view (always when there is no grid)

	// rows (grid) or lines (text) selected by dragging the mouse
	rangeOn        bool
	rangeA, rangeB int

	// block of cells selected with Shift + cursor keys: from the anchor
	// (blockRow, blockCol) to the cell cursor (row, selCol)
	blockOn            bool
	blockRow, blockCol int
	blockAllCols       bool // whole rows: started with Shift-↑↓, until Shift-←→ narrows it
	textTop            int  // first visible text line
	textLeft           int  // horizontal scroll of the text view

	widths []int
	row    int // selected row
	top    int // first visible row
	colOff int // first visible column
	selCol int // column of the cell cursor

	edits []rowChange // changes made in the database (undone in the grid on rollback)
	w, h  int
}

func (r *resultView) set(res *db.Result, messages, output []string, err string) {
	r.res, r.messages, r.output, r.err = res, messages, output, err
	r.row, r.top, r.colOff, r.selCol = 0, 0, 0, 0
	r.edits = nil
	r.textTop, r.textLeft = 0, 0
	r.rangeOn, r.blockOn, r.blockAllCols = false, false, false
	r.compile = nil
	r.widths = nil
	r.textMode = !r.hasGrid()
	if !r.hasGrid() {
		return
	}
	r.widths = make([]int, len(res.Columns))
	for i, c := range res.Columns {
		r.widths[i] = ansi.StringWidth(c)
	}
	for n, row := range res.Rows {
		if n > 500 {
			break
		}
		for i, v := range row {
			r.widths[i] = max(r.widths[i], min(ansi.StringWidth(cell(v)), maxColWidth))
		}
	}
}

func cell(v string) string {
	if v == db.Null {
		return db.NullDisplay
	}
	if strings.ContainsAny(v, "\r\n\t") {
		v = strings.NewReplacer("\r\n", "⏎", "\n", "⏎", "\r", "⏎", "\t", " ").Replace(v)
	}
	return v
}

func (r *resultView) dataRows() int { return max(r.h-2, 1) } // minus header + separator

// hasGrid reports whether there is a query result to show as a grid.
func (r *resultView) hasGrid() bool { return r.err == "" && r.res != nil && r.res.IsQuery }

// canToggle reports whether 'o' can switch between grid and DBMS_OUTPUT.
func (r *resultView) canToggle() bool { return r.hasGrid() && len(r.output) > 0 }

// showText reports whether the text view is shown instead of the grid.
func (r *resultView) showText() bool { return r.textMode || !r.hasGrid() }

func (r *resultView) toggleText() {
	if r.canToggle() {
		r.textMode = !r.textMode
	}
}

func (r *resultView) handleKey(key string) {
	if r.showText() {
		r.handleTextKey(key)
		return
	}
	if !r.hasGrid() {
		return
	}
	n := len(r.res.Rows)
	switch key {
	case "up", "ctrl+p", "k":
		r.row--
	case "down", "ctrl+n", "j":
		r.row++
	case "pgup", "ctrl+u":
		r.row -= r.dataRows()
	case "pgdown", "ctrl+v":
		r.row += r.dataRows()
	case "home", "ctrl+a", "g":
		r.row = 0
	case "end", "ctrl+e", "G":
		r.row = n - 1
	case "left", "ctrl+b", "h":
		r.selCol = max(r.selCol-1, 0)
	case "right", "ctrl+f", "l":
		r.selCol = min(r.selCol+1, max(len(r.widths)-1, 0))
	}
	r.keepColVisible()
	r.clampRow()
}

// moveBy moves the grid selection by n rows (negative: up).
func (r *resultView) moveBy(n int) {
	if r.showText() {
		return
	}
	r.row += n
	r.clampRow()
}

// clampRow keeps the selection on an existing row and in view.
func (r *resultView) clampRow() {
	r.row = min(max(r.row, 0), max(len(r.res.Rows)-1, 0))
	if r.row < r.top {
		r.top = r.row
	}
	if r.row >= r.top+r.dataRows() {
		r.top = r.row - r.dataRows() + 1
	}
}

// selectedRowText returns the selected row as tab-separated "COL: value" lines.
func (r *resultView) selectedRow() []string {
	if r.showText() || len(r.res.Rows) == 0 {
		return nil
	}
	return r.res.Rows[r.row]
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	sepStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	selStyle    = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")).Bold(true)
	selDimStyle = lipgloss.NewStyle().Background(lipgloss.Color("237"))                                  // selected row, pane not focused
	cellStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)                                           // the cell cursor
	blockStyle  = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")) // selected cells (Shift + cursor keys)
	nullStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Italic(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
)

// textLines builds the text view: messages, the error, then DBMS_OUTPUT.
func (r *resultView) textLines() []string {
	var lines []string
	for _, m := range r.messages {
		lines = append(lines, okStyle.Render(m))
	}
	if r.err != "" {
		for _, l := range strings.Split(r.err, "\n") {
			lines = append(lines, errStyle.Render(l))
		}
	}
	if len(r.compile) > 0 {
		lines = append(lines, errStyle.Render("── Compilation errors ──"))
		for _, l := range r.compile {
			lines = append(lines, errStyle.Render(l))
		}
	}
	if len(r.output) > 0 {
		lines = append(lines, sepStyle.Render(fmt.Sprintf("── Output (%d line(s)) ──", len(r.output))))
		for _, l := range r.output {
			lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, sepStyle.Render("No results yet. F5 runs the statement under the cursor (or the marked block)."))
	}
	return lines
}

func (r *resultView) handleTextKey(key string) {
	n := len(r.textLines())
	switch key {
	case "up", "ctrl+p", "k":
		r.textTop--
	case "down", "ctrl+n", "j":
		r.textTop++
	case "pgup", "ctrl+u":
		r.textTop -= r.h
	case "pgdown", "ctrl+v":
		r.textTop += r.h
	case "home", "ctrl+a", "g":
		r.textTop = 0
	case "end", "ctrl+e", "G":
		r.textTop = n
	case "left", "ctrl+b", "h":
		r.textLeft = max(r.textLeft-8, 0)
	case "right", "ctrl+f", "l":
		r.textLeft += 8
	}
	r.textTop = min(max(r.textTop, 0), max(n-r.h, 0))
}

func (r *resultView) view(focused bool) string {
	if r.showText() {
		lines := r.textLines()
		r.textTop = min(r.textTop, max(len(lines)-r.h, 0))
		top := r.textTop
		lines = lines[top:min(len(lines), top+r.h)]
		for i, l := range lines {
			lines[i] = ansi.Cut(l, r.textLeft, r.textLeft+r.w)
			if n := top + i; r.rangeOn && n >= min(r.rangeA, r.rangeB) && n <= max(r.rangeA, r.rangeB) {
				lines[i] = selStyle.Render(ansi.Strip(lines[i]) + strings.Repeat(" ", max(r.w-ansi.StringWidth(lines[i]), 0)))
			}
		}
		return strings.Join(lines, "\n")
	}

	res := r.res
	rnW := len(fmt.Sprint(len(res.Rows)))
	r.keepColVisible()
	cols := r.visibleCols()

	pad := func(s string, w int) string {
		s = ansi.Truncate(s, w, "…")
		return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
	}

	var b strings.Builder
	// header
	hdr := strings.Repeat(" ", rnW) + " "
	sep := strings.Repeat("─", rnW) + "─"
	for _, c := range cols {
		hdr += "│ " + pad(res.Columns[c], r.widths[c]) + " "
		sep += "┼─" + strings.Repeat("─", r.widths[c]) + "─"
	}
	b.WriteString(headerStyle.Render(ansi.Truncate(hdr, r.w, "")))
	b.WriteByte('\n')
	b.WriteString(sepStyle.Render(ansi.Truncate(sep, r.w, "")))

	for i := r.top; i < len(res.Rows) && i < r.top+r.dataRows(); i++ {
		b.WriteByte('\n')
		row := res.Rows[i]
		var line strings.Builder
		line.WriteString(sepStyle.Render(fmt.Sprintf("%*d ", rnW, i+1)))
		for _, c := range cols {
			line.WriteString(sepStyle.Render("│ "))
			v := cell(row[c])
			switch {
			case r.blockOn && i == r.row && c == r.selCol && focused:
				line.WriteString(cellStyle.Render(pad(v, r.widths[c])))
			case r.inBlock(i, c):
				line.WriteString(blockStyle.Render(pad(v, r.widths[c])))
			case row[c] == db.Null:
				line.WriteString(nullStyle.Render(pad(v, r.widths[c])))
			default:
				line.WriteString(pad(v, r.widths[c]))
			}
			line.WriteString(" ")
		}
		s := ansi.Truncate(line.String(), r.w, "")
		if r.blockOn { // the block shows the selection; no row highlight
			b.WriteString(s)
			continue
		}
		if r.rangeOn && i >= min(r.rangeA, r.rangeB) && i <= max(r.rangeA, r.rangeB) && i != r.row {
			s = selDimStyle.Render(ansi.Strip(s) + strings.Repeat(" ", max(r.w-ansi.StringWidth(s), 0)))
		}
		if i == r.row {
			st := selDimStyle
			if focused {
				st = selStyle
			}
			plain := []rune(ansi.Strip(s) + strings.Repeat(" ", max(r.w-ansi.StringWidth(s), 0)))
			// the cell cursor: the selected column of the selected row
			x0 := rnW + 1
			cellX, cellW := -1, 0
			for _, c := range cols {
				if c == r.selCol {
					cellX, cellW = x0+2, r.widths[c]
				}
				x0 += r.widths[c] + 3
			}
			if focused && cellX >= 0 && cellX < len(plain) {
				end := min(cellX+cellW, len(plain))
				s = st.Render(string(plain[:cellX])) + cellStyle.Render(string(plain[cellX:end])) + st.Render(string(plain[end:]))
			} else {
				s = st.Render(string(plain))
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

// appendRows adds rows fetched later to the current result.
func (r *resultView) appendRows(rows [][]string) {
	if r.res == nil {
		return
	}
	from := len(r.res.Rows)
	r.res.Rows = append(r.res.Rows, rows...)
	for n := from; n < len(r.res.Rows) && n <= 500; n++ { // widths come from the first rows
		for i, v := range r.res.Rows[n] {
			r.widths[i] = max(r.widths[i], min(ansi.StringWidth(cell(v)), maxColWidth))
		}
	}
}

// clickRow selects the grid row shown at content line y (0 = header).
func (r *resultView) clickRow(y int) {
	if r.showText() || y < 2 {
		return
	}
	if i := r.top + y - 2; i < len(r.res.Rows) {
		r.row = i
	}
}

// nearEnd reports that the selection is within a page of the last row.
func (r *resultView) nearEnd() bool {
	return !r.showText() && r.row >= len(r.res.Rows)-1-r.dataRows()
}

func (r *resultView) summary() string {
	if r.showText() {
		s := ""
		if n := len(r.textLines()); n > r.h {
			s = fmt.Sprintf("lines %d-%d/%d", r.textTop+1, min(r.textTop+r.h, n), n)
		}
		if r.canToggle() {
			s += "  o: grid"
		}
		return strings.TrimSpace(s)
	}
	if !r.hasGrid() {
		return ""
	}
	more := ""
	if r.res.More {
		more = "+"
	}
	s := fmt.Sprintf("row %d/%d%s  col %d/%d", min(r.row+1, len(r.res.Rows)), len(r.res.Rows), more, r.selCol+1, len(r.res.Columns))
	if r1, r2, c1, c2, ok := r.blockRange(); ok {
		if r.blockAllCols {
			s += fmt.Sprintf("  %d row(s) selected", r2-r1+1)
		} else {
			s += fmt.Sprintf("  %d×%d selected", r2-r1+1, c2-c1+1)
		}
	}
	if len(r.output) > 0 {
		s += "  o: DBMS_OUTPUT"
	}
	return s
}

// SetSelectionColor sets the background of the selected result row: a
// 256-colour number ("24") or "#rrggbb". The text turns white and bold.
func SetSelectionColor(c string) {
	if strings.TrimSpace(c) == "" {
		return
	}
	selStyle = lipgloss.NewStyle().Background(lipgloss.Color(strings.TrimSpace(c))).Foreground(lipgloss.Color("255")).Bold(true)
}

// indexAt returns the row (grid) or line (text) shown at content line y,
// clamped to what exists; ok is false when there is nothing there.
func (r *resultView) indexAt(y int) (int, bool) {
	if r.showText() {
		n := len(r.textLines())
		if n == 0 {
			return 0, false
		}
		return min(max(r.textTop+y, 0), n-1), true
	}
	if r.res == nil || len(r.res.Rows) == 0 {
		return 0, false
	}
	return min(max(r.top+y-2, r.top), len(r.res.Rows)-1), true
}

// rangeText returns the selected rows as tab-separated values (NULL as
// empty), or the selected text lines, and how many rows/lines that is.
func (r *resultView) rangeText() (string, int) {
	a, b := min(r.rangeA, r.rangeB), max(r.rangeA, r.rangeB)
	var out []string
	if r.showText() {
		lines := r.textLines()
		for i := a; i <= b && i < len(lines); i++ {
			out = append(out, ansi.Strip(lines[i]))
		}
	} else {
		for i := a; i <= b && i < len(r.res.Rows); i++ {
			cells := make([]string, len(r.res.Rows[i]))
			for j, v := range r.res.Rows[i] {
				if v != db.Null {
					cells[j] = v
				}
			}
			out = append(out, strings.Join(cells, "\t"))
		}
	}
	return strings.Join(out, "\n"), len(out)
}

// rowChange remembers a change so a rollback can undo it in the grid.
type rowChange struct {
	kind     byte // 'u' cell edited, 'i' row inserted, 'd' row deleted
	row, col int
	old      string   // 'u': the old value
	data     []string // 'd': the deleted row
}

// visibleCols returns the columns shown, from colOff while they fit.
func (r *resultView) visibleCols() []int {
	rnW := len(fmt.Sprint(len(r.res.Rows)))
	var cols []int
	used := rnW + 1
	for i := r.colOff; i < len(r.widths); i++ {
		if used >= r.w && len(cols) > 0 {
			break
		}
		cols = append(cols, i)
		used += r.widths[i] + 3
	}
	return cols
}

// keepColVisible scrolls sideways so the cell cursor's column is shown
// completely.
func (r *resultView) keepColVisible() {
	if r.res == nil || len(r.widths) == 0 {
		return
	}
	r.selCol = min(max(r.selCol, 0), len(r.widths)-1)
	if r.selCol < r.colOff {
		r.colOff = r.selCol
	}
	rnW := len(fmt.Sprint(len(r.res.Rows)))
	for r.colOff < r.selCol {
		used := rnW + 1
		for i := r.colOff; i <= r.selCol; i++ {
			used += r.widths[i] + 3
		}
		if used <= r.w {
			break
		}
		r.colOff++
	}
}

// setCell changes a shown value after an UPDATE, remembering the old one.
func (r *resultView) setCell(row, col int, value string) {
	r.edits = append(r.edits, rowChange{kind: 'u', row: row, col: col, old: r.res.Rows[row][col]})
	r.res.Rows[row][col] = value
	if row <= 500 {
		r.widths[col] = max(r.widths[col], min(ansi.StringWidth(cell(value)), maxColWidth))
	}
}

// insertRow adds a row (just inserted in the database) at index at.
func (r *resultView) insertRow(at int, data []string) {
	at = min(max(at, 0), len(r.res.Rows))
	r.res.Rows = append(r.res.Rows[:at], append([][]string{data}, r.res.Rows[at:]...)...)
	r.edits = append(r.edits, rowChange{kind: 'i', row: at})
	r.row = at
	r.clampRow()
}

// deleteRow removes a row (just deleted in the database).
func (r *resultView) deleteRow(at int) {
	r.edits = append(r.edits, rowChange{kind: 'd', row: at, data: r.res.Rows[at]})
	r.res.Rows = append(r.res.Rows[:at], r.res.Rows[at+1:]...)
	r.clampRow()
}

// revertEdits undoes the changes in the grid (after a rollback): old values
// come back, inserted rows go, deleted rows return.
func (r *resultView) revertEdits() int {
	n := len(r.edits)
	for i := n - 1; i >= 0; i-- {
		e := r.edits[i]
		if r.res == nil {
			break
		}
		switch e.kind {
		case 'u':
			if e.row < len(r.res.Rows) {
				r.res.Rows[e.row][e.col] = e.old
			}
		case 'i':
			if e.row < len(r.res.Rows) {
				r.res.Rows = append(r.res.Rows[:e.row], r.res.Rows[e.row+1:]...)
			}
		case 'd':
			at := min(e.row, len(r.res.Rows))
			r.res.Rows = append(r.res.Rows[:at], append([][]string{e.data}, r.res.Rows[at:]...)...)
		}
	}
	r.edits = nil
	if r.res != nil {
		r.clampRow()
	}
	return n
}

// ---- block selection (Shift + cursor keys) ----

// extendBlock moves the cell cursor with a Shift + cursor key, starting a
// block at the current cell first. It reports whether key was such a key.
func (r *resultView) extendBlock(key string) bool {
	base, ok := strings.CutPrefix(key, "shift+")
	switch base {
	case "up", "down", "left", "right", "pgup", "pgdown", "home", "end":
	default:
		ok = false
	}
	if !ok || r.showText() || !r.hasGrid() || len(r.res.Rows) == 0 {
		return false
	}
	vertical := base != "left" && base != "right"
	if !r.blockOn {
		r.blockOn, r.blockRow, r.blockCol = true, r.row, r.selCol
		r.blockAllCols = vertical // Shift-↑↓ select whole rows
	} else if !vertical {
		r.blockAllCols = false // Shift-←→ narrow to the columns from the start column
	}
	r.handleKey(base)
	return true
}

// blockRange returns the selected block (rows r1..r2, columns c1..c2).
func (r *resultView) blockRange() (r1, r2, c1, c2 int, ok bool) {
	if !r.blockOn || r.showText() || !r.hasGrid() {
		return 0, 0, 0, 0, false
	}
	if r.blockAllCols {
		return min(r.blockRow, r.row), max(r.blockRow, r.row), 0, len(r.res.Columns) - 1, true
	}
	return min(r.blockRow, r.row), max(r.blockRow, r.row), min(r.blockCol, r.selCol), max(r.blockCol, r.selCol), true
}

func (r *resultView) inBlock(row, col int) bool {
	r1, r2, c1, c2, ok := r.blockRange()
	return ok && row >= r1 && row <= r2 && col >= c1 && col <= c2
}

// blockText is the block as tab-separated text (pastes into spreadsheets):
// one line per row, NULL as empty, values with tabs, line breaks or quotes
// in double quotes. header adds a first line with the column names.
func (r *resultView) blockText(header bool) string {
	r1, r2, c1, c2, ok := r.blockRange()
	if !ok {
		return ""
	}
	field := func(v string) string {
		if v == db.Null {
			return ""
		}
		if strings.ContainsAny(v, "\t\r\n\"") {
			return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
		}
		return v
	}
	var b strings.Builder
	line := func(vals []string) {
		for i, v := range vals {
			if i > 0 {
				b.WriteByte('\t')
			}
			b.WriteString(field(v))
		}
		b.WriteByte('\n')
	}
	if header {
		line(r.res.Columns[c1 : c2+1])
	}
	for i := r1; i <= r2 && i < len(r.res.Rows); i++ {
		line(r.res.Rows[i][c1 : c2+1])
	}
	return b.String()
}
