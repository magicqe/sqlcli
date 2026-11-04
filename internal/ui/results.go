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
	textTop        int // first visible text line
	textLeft       int // horizontal scroll of the text view

	widths []int
	row    int // selected row
	top    int // first visible row
	colOff int // first visible column
	w, h   int
}

func (r *resultView) set(res *db.Result, messages, output []string, err string) {
	r.res, r.messages, r.output, r.err = res, messages, output, err
	r.row, r.top, r.colOff = 0, 0, 0
	r.textTop, r.textLeft = 0, 0
	r.rangeOn = false
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
		r.colOff = max(r.colOff-1, 0)
	case "right", "ctrl+f", "l":
		r.colOff = min(r.colOff+1, max(len(r.widths)-1, 0))
	}
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
	selDimStyle = lipgloss.NewStyle().Background(lipgloss.Color("237")) // selected row, pane not focused
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
	// visible columns
	var cols []int
	used := rnW + 1
	for i := r.colOff; i < len(r.widths); i++ {
		if used >= r.w && len(cols) > 0 {
			break
		}
		cols = append(cols, i)
		used += r.widths[i] + 3
	}

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
			if row[c] == db.Null {
				line.WriteString(nullStyle.Render(pad(v, r.widths[c])))
			} else {
				line.WriteString(pad(v, r.widths[c]))
			}
			line.WriteString(" ")
		}
		s := ansi.Truncate(line.String(), r.w, "")
		if r.rangeOn && i >= min(r.rangeA, r.rangeB) && i <= max(r.rangeA, r.rangeB) && i != r.row {
			s = selDimStyle.Render(ansi.Strip(s) + strings.Repeat(" ", max(r.w-ansi.StringWidth(s), 0)))
		}
		if i == r.row {
			st := selDimStyle
			if focused {
				st = selStyle
			}
			s = st.Render(ansi.Strip(s) + strings.Repeat(" ", max(r.w-ansi.StringWidth(s), 0)))
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
	s := fmt.Sprintf("row %d/%d%s  col %d/%d", min(r.row+1, len(r.res.Rows)), len(r.res.Rows), more, r.colOff+1, len(r.res.Columns))
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
