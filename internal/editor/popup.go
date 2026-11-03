package editor

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// PopupItem is one entry of the completion list.
type PopupItem struct {
	Text string // shown and inserted
	Kind string // shown dimmed on the right: column, table, keyword ...
}

const popupRows = 8 // visible entries

type popup struct {
	open  bool
	items []PopupItem
	sel   int
	top   int
	col   int // buffer column where the completed word starts
}

var (
	popupStyle    = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("252"))
	popupSelStyle = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")).Bold(true)
)

// OpenPopup shows the completion list for the word starting at column col
// of the cursor line. An empty list closes it.
func (e *Editor) OpenPopup(items []PopupItem, col int) {
	if len(items) == 0 {
		e.ClosePopup()
		return
	}
	keep := e.pop.open && e.pop.col == col && e.pop.sel < len(e.pop.items)
	sel := 0
	if keep { // stay on the same entry while the list is refined
		prev := e.pop.items[e.pop.sel]
		for i, it := range items {
			if it == prev {
				sel = i
			}
		}
	}
	e.pop = popup{open: true, items: items, col: col, sel: sel}
	e.popupScroll()
}

func (e *Editor) ClosePopup()     { e.pop = popup{} }
func (e *Editor) PopupOpen() bool { return e.pop.open }

// PopupMove moves the selection by d entries, stopping at the ends.
func (e *Editor) PopupMove(d int) {
	if !e.pop.open {
		return
	}
	e.pop.sel = min(max(e.pop.sel+d, 0), len(e.pop.items)-1)
	e.popupScroll()
}

func (e *Editor) popupScroll() {
	if e.pop.sel < e.pop.top {
		e.pop.top = e.pop.sel
	}
	if e.pop.sel >= e.pop.top+popupRows {
		e.pop.top = e.pop.sel - popupRows + 1
	}
}

// PopupSelected returns the selected entry.
func (e *Editor) PopupSelected() (PopupItem, bool) {
	if !e.pop.open {
		return PopupItem{}, false
	}
	return e.pop.items[e.pop.sel], true
}

// ReplaceWord replaces the text between column start and the cursor on the
// cursor line with text (one undo step) and leaves the cursor after it.
func (e *Editor) ReplaceWord(start int, text string) {
	e.pushUndo()
	line := e.lines[e.cur.row]
	start = min(max(start, 0), e.cur.col)
	nl := append(append(append([]rune{}, line[:start]...), []rune(text)...), line[e.cur.col:]...)
	e.lines[e.cur.row] = nl
	e.cur.col = start + len([]rune(text))
	e.wantX = e.cur.col
	e.scroll()
}

// popupCells lays the popup out on the screen: for each screen row it
// covers, the starting x (in text-area columns), its text and whether it
// is the selected entry.
func (e *Editor) popupCells(textW int) map[int]popupLine {
	if !e.pop.open || !e.Focused {
		return nil
	}
	n := min(popupRows, len(e.pop.items)-e.pop.top)
	nameW, kindW := 0, 0
	for _, it := range e.pop.items {
		nameW = max(nameW, len([]rune(it.Text)))
		kindW = max(kindW, len([]rune(it.Kind)))
	}
	nameW = min(nameW, 40)
	w := min(nameW+kindW+3, textW)
	x := min(max(e.pop.col-e.left, 0), max(textW-w, 0))

	cy := e.cur.row - e.top
	y := cy + 1
	if y+n > e.height && cy-n >= 0 { // no room below: open upwards
		y = cy - n
	}
	out := map[int]popupLine{}
	for i := 0; i < n && y+i < e.height; i++ {
		idx := e.pop.top + i
		it := e.pop.items[idx]
		name := []rune(it.Text)
		if len(name) > nameW {
			name = append(name[:nameW-1], '…')
		}
		s := []rune(fmt.Sprintf(" %-*s %*s ", nameW, string(name), kindW, it.Kind))
		if len(s) > w {
			s = s[:w]
		}
		out[y+i] = popupLine{x: x, text: s, sel: idx == e.pop.sel}
	}
	return out
}

type popupLine struct {
	x    int
	text []rune
	sel  bool
}
