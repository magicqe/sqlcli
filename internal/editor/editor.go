// Package editor implements a small multi-line text editor with JOE
// (Joe's Own Editor) keybindings, rendered as a plain string for Bubble Tea.
package editor

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// Action is a request the editor cannot fulfil itself and passes to its host.
type Action int

const (
	ActionNone           Action = iota
	ActionExit                  // ^K X  / ^K Q
	ActionFindPrompt            // ^K F
	ActionGotoPrompt            // ^K L
	ActionSavePrompt            // ^K D
	ActionInsertFile            // ^K R
	ActionWriteBlock            // ^K W
	ActionHelp                  // ^K H
	ActionNewWindow             // ^K O  (new tab)
	ActionNextWindow            // ^K N
	ActionPrevWindow            // ^K P
	ActionCloseWindow           // ^K Q  (close tab)
	ActionSessions              // ^K E  (session list)
	ActionLoadFile              // ^K I  (replace the buffer with a file)
	ActionCopyOtherBlock        // ^K C with no block here: copy the block of another editor
	ActionMoveOtherBlock        // ^K M with no block here: move the block of another editor
)

type pos struct{ row, col int }

func (a pos) less(b pos) bool { return a.row < b.row || (a.row == b.row && a.col < b.col) }

type snapshot struct {
	lines [][]rune
	cur   pos
}

type Editor struct {
	lines  [][]rune
	cur    pos
	wantX  int // preferred column for vertical motion
	top    int
	left   int
	width  int
	height int

	markB, markK       pos
	hasMarkB, hasMarkK bool

	prefixK bool // ^K was pressed, waiting for the second key

	undo, redo    []snapshot
	lastWasInsert bool

	lastSearch string
	TabWidth   int
	AutoIndent bool
	Highlight  bool // SQL syntax colouring
	Focused    bool
	Status     string // short message about the last editor action

	styles Styles
	pop    popup // code-completion list

	blockStamp int64 // when the block was last marked (blockClock), to find the newest block
}

// blockClock orders block marking across editors (JOE has one block for
// all windows: the most recently marked one).
var blockClock int64

func (e *Editor) stampBlock() {
	blockClock++
	e.blockStamp = blockClock
}

// BlockStamp tells how recently the block was marked (larger is newer).
func (e *Editor) BlockStamp() int64 { return e.blockStamp }

type Styles struct {
	Text, LineNo, LineNoCur, Block, Cursor lipgloss.Style

	// syntax colours, indexed by tokKind
	Syntax [tkBind + 1]lipgloss.Style
}

func DefaultStyles() Styles {
	return Styles{
		Text:      lipgloss.NewStyle(),
		LineNo:    lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		LineNoCur: lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		Block:     lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255")),
		Cursor:    lipgloss.NewStyle().Reverse(true),
		Syntax: [tkBind + 1]lipgloss.Style{
			tkPlain:   lipgloss.NewStyle(),
			tkKeyword: lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true),
			tkType:    lipgloss.NewStyle().Foreground(lipgloss.Color("80")),
			tkFunc:    lipgloss.NewStyle().Foreground(lipgloss.Color("179")),
			tkString:  lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
			tkNumber:  lipgloss.NewStyle().Foreground(lipgloss.Color("176")),
			tkComment: lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
			tkBind:    lipgloss.NewStyle().Foreground(lipgloss.Color("215")),
		},
	}
}

func New() *Editor {
	return &Editor{lines: [][]rune{{}}, TabWidth: 4, AutoIndent: true, styles: DefaultStyles()}
}

func (e *Editor) SetSize(w, h int) { e.width, e.height = max(w, 1), max(h, 1); e.scroll() }

// Lines returns the buffer as strings.
func (e *Editor) Lines() []string {
	out := make([]string, len(e.lines))
	for i, l := range e.lines {
		out[i] = string(l)
	}
	return out
}

func (e *Editor) Text() string        { return strings.Join(e.Lines(), "\n") }
func (e *Editor) CursorRow() int      { return e.cur.row }
func (e *Editor) CursorCol() int      { return e.cur.col }
func (e *Editor) PrefixPending() bool { return e.prefixK }

func (e *Editor) SetText(s string) {
	e.pushUndo()
	e.lines = splitLines(s, e.TabWidth)
	e.cur = pos{}
	e.top, e.left = 0, 0
	e.hasMarkB, e.hasMarkK = false, false
}

func splitLines(s string, tab int) [][]rune {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", strings.Repeat(" ", max(tab, 1)))
	parts := strings.Split(s, "\n")
	out := make([][]rune, len(parts))
	for i, p := range parts {
		out[i] = []rune(p)
	}
	return out
}

// ---- block (JOE ^K B / ^K K) ----

// Block returns the ordered marked block, if both marks are set and differ.
func (e *Editor) block() (pos, pos, bool) {
	if !e.hasMarkB || !e.hasMarkK {
		return pos{}, pos{}, false
	}
	b, k := e.clamp(e.markB), e.clamp(e.markK)
	if !b.less(k) {
		return pos{}, pos{}, false
	}
	return b, k, true
}

func (e *Editor) HasBlock() bool { _, _, ok := e.block(); return ok }

func (e *Editor) BlockText() string {
	b, k, ok := e.block()
	if !ok {
		return ""
	}
	return e.textRange(b, k)
}

func (e *Editor) textRange(b, k pos) string {
	if b.row == k.row {
		return string(e.lines[b.row][b.col:k.col])
	}
	var sb strings.Builder
	sb.WriteString(string(e.lines[b.row][b.col:]))
	for r := b.row + 1; r < k.row; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.lines[r]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(e.lines[k.row][:k.col]))
	return sb.String()
}

func (e *Editor) deleteRange(b, k pos) {
	head := e.lines[b.row][:b.col]
	tail := e.lines[k.row][k.col:]
	nl := append(append([]rune{}, head...), tail...)
	e.lines = append(e.lines[:b.row], append([][]rune{nl}, e.lines[k.row+1:]...)...)
	e.cur = b
}

// insertAt inserts text at p and returns the position just after it.
func (e *Editor) insertAt(p pos, text string) pos {
	ins := splitLines(text, e.TabWidth)
	line := e.lines[p.row]
	head := append([]rune{}, line[:p.col]...)
	tail := append([]rune{}, line[p.col:]...)
	if len(ins) == 1 {
		e.lines[p.row] = append(append(head, ins[0]...), tail...)
		return pos{p.row, p.col + len(ins[0])}
	}
	newLines := make([][]rune, 0, len(ins))
	newLines = append(newLines, append(head, ins[0]...))
	newLines = append(newLines, ins[1:len(ins)-1]...)
	last := ins[len(ins)-1]
	end := pos{p.row + len(ins) - 1, len(last)}
	newLines = append(newLines, append(append([]rune{}, last...), tail...))
	e.lines = append(e.lines[:p.row], append(newLines, e.lines[p.row+1:]...)...)
	return end
}

// AppendStatement adds text at the end of the buffer as a statement of its
// own (after a blank line, unless the buffer is empty) in one undo step, and
// puts the cursor at its start.
func (e *Editor) AppendStatement(text string) {
	e.pushUndo()
	all := strings.TrimRight(e.Text(), " \t\n")
	if all == "" {
		e.lines = splitLines(text, e.TabWidth)
		e.cur = pos{}
	} else {
		e.lines = splitLines(all+"\n\n"+text, e.TabWidth)
		e.cur = pos{len(splitLines(all, e.TabWidth)) + 1, 0}
	}
	e.wantX = 0
	e.hasMarkB, e.hasMarkK = false, false
	e.scroll()
}

// InsertText inserts text (e.g. a paste or a history entry) at the cursor.
func (e *Editor) InsertText(s string) {
	e.pushUndo()
	e.cur = e.insertAt(e.cur, s)
	e.wantX = e.cur.col
	e.scroll()
}

// ---- undo ----

func (e *Editor) snap() snapshot {
	cp := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		cp[i] = append([]rune(nil), l...)
	}
	return snapshot{cp, e.cur}
}

func (e *Editor) pushUndo() {
	e.undo = append(e.undo, e.snap())
	if len(e.undo) > 500 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
	e.lastWasInsert = false
}

func (e *Editor) doUndo() {
	if len(e.undo) == 0 {
		e.Status = "Nothing to undo"
		return
	}
	e.redo = append(e.redo, e.snap())
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.lines, e.cur = s.lines, s.cur
	e.lastWasInsert = false
}

func (e *Editor) doRedo() {
	if len(e.redo) == 0 {
		e.Status = "Nothing to redo"
		return
	}
	e.undo = append(e.undo, e.snap())
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.lines, e.cur = s.lines, s.cur
	e.lastWasInsert = false
}

// ---- search ----

// Find searches forward from the cursor (wrapping) case-insensitively.
func (e *Editor) Find(s string) bool {
	if s == "" {
		s = e.lastSearch
	}
	if s == "" {
		return false
	}
	e.lastSearch = s
	needle := []rune(strings.ToLower(s))
	n := len(e.lines)
	for i := 0; i <= n; i++ {
		r := (e.cur.row + i) % n
		line := []rune(strings.ToLower(string(e.lines[r])))
		start := 0
		if i == 0 {
			start = e.cur.col + 1
		}
		if i == n { // wrapped back to the starting line: search its head
			line = line[:min(len(line), e.cur.col+len(needle))]
			start = 0
		}
		if idx := indexRunes(line, needle, start); idx >= 0 {
			e.cur = pos{r, idx}
			e.wantX = idx
			e.scroll()
			e.Status = ""
			return true
		}
	}
	e.Status = fmt.Sprintf("Not found: %s", s)
	return false
}

func indexRunes(hay, needle []rune, from int) int {
	for i := max(from, 0); i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == string(needle) {
			return i
		}
	}
	return -1
}

// ClickAt moves the cursor to a position in the rendered view: x and y are
// 0-based offsets into the editor area (line-number gutter included).
func (e *Editor) ClickAt(x, y int) {
	e.prefixK = false
	e.cur = e.posAt(x, y)
	e.wantX = e.cur.col
	e.lastWasInsert = false
	e.scroll()
}

// PosAt returns the buffer position (row, col) shown at view offset x, y.
func (e *Editor) PosAt(x, y int) (int, int) {
	p := e.posAt(x, y)
	return p.row, p.col
}

func (e *Editor) posAt(x, y int) pos {
	return e.clamp(pos{e.top + y, max(e.left+x-e.gutterWidth(), 0)})
}

// SetBlock marks the block between two buffer positions (in any order).
func (e *Editor) SetBlock(r1, c1, r2, c2 int) {
	a, b := e.clamp(pos{r1, c1}), e.clamp(pos{r2, c2})
	if b.less(a) {
		a, b = b, a
	}
	e.markB, e.markK, e.hasMarkB, e.hasMarkK = a, b, true, true
	e.stampBlock()
}

// InsertBlock inserts text at the cursor (one undo step) and marks it as
// the block, like JOE does after copying a block.
func (e *Editor) InsertBlock(text string) {
	e.pushUndo()
	start := e.cur
	end := e.insertAt(e.cur, text)
	e.markB, e.markK, e.hasMarkB, e.hasMarkK = start, end, true, true
	e.stampBlock()
	e.scroll()
}

// DeleteBlock removes the block's text (one undo step) and the marks.
func (e *Editor) DeleteBlock() {
	b, k, ok := e.block()
	if !ok {
		return
	}
	e.pushUndo()
	e.deleteRange(b, k)
	e.hasMarkB, e.hasMarkK = false, false
	e.scroll()
}

// ClearBlock removes the block marks.
func (e *Editor) ClearBlock() { e.hasMarkB, e.hasMarkK = false, false }

// SetCursor moves the cursor to a buffer position (0-based), clamped.
func (e *Editor) SetCursor(row, col int) {
	e.cur = e.clamp(pos{row, col})
	e.wantX = e.cur.col
	e.scroll()
}

func (e *Editor) GotoLine(n int) {
	e.cur = e.clamp(pos{n - 1, 0})
	e.wantX = 0
	e.scroll()
}

// ---- key handling ----

// HandleKey processes a key (as reported by tea.KeyPressMsg.String() and
// its Text) and returns an action the host should perform.
func (e *Editor) HandleKey(key, text string) Action {
	e.Status = ""
	if e.prefixK {
		e.prefixK = false
		return e.handleCtrlK(key)
	}

	insertRun := false
	defer func() {
		if !insertRun {
			e.lastWasInsert = false
		}
		e.scroll()
	}()

	switch key {
	// --- movement ---
	case "left", "ctrl+b":
		e.moveLeft()
	case "right", "ctrl+f":
		e.moveRight()
	case "up", "ctrl+p":
		e.moveVert(-1)
	case "down", "ctrl+n":
		e.moveVert(1)
	case "ctrl+a": // JOE: beginning of line
		e.cur.col, e.wantX = 0, 0
	case "home": // smart home: first non-blank, then column 0
		e.home()
	case "end", "ctrl+e":
		e.cur.col = len(e.lines[e.cur.row])
		e.wantX = e.cur.col
	case "pgup", "ctrl+u":
		e.moveVert(-max(e.height-1, 1))
	case "pgdown", "ctrl+v":
		e.moveVert(max(e.height-1, 1))
	case "ctrl+z", "ctrl+left", "alt+left":
		e.wordLeft()
	case "ctrl+x", "ctrl+right", "alt+right":
		e.wordRight()
	case "ctrl+home":
		e.cur, e.wantX = pos{}, 0
	case "ctrl+end":
		e.cur = pos{len(e.lines) - 1, len(e.lines[len(e.lines)-1])}
		e.wantX = e.cur.col

	// --- prefix ---
	case "ctrl+k":
		e.prefixK = true

	// --- deletion ---
	case "backspace", "ctrl+h":
		e.pushUndo()
		e.backspace()
	case "delete", "ctrl+d":
		e.pushUndo()
		e.deleteChar()
	case "ctrl+y": // delete line
		e.pushUndo()
		e.deleteLine()
	case "ctrl+w": // delete word to the right
		e.pushUndo()
		e.deleteWordRight()
	case "ctrl+o": // delete word to the left
		e.pushUndo()
		e.deleteWordLeft()
	case "ctrl+j": // delete to end of line
		e.pushUndo()
		e.lines[e.cur.row] = e.lines[e.cur.row][:e.cur.col]

	// --- undo/redo (^_ and ^^, various terminal spellings) ---
	case "ctrl+_", "ctrl+/", "ctrl+7", "ctrl+-":
		e.doUndo()
	case "ctrl+^", "ctrl+6", "ctrl+shift+6":
		e.doRedo()

	// --- search ---
	case "ctrl+l":
		e.Find("")

	// --- insertion ---
	case "enter":
		e.pushUndo()
		e.newline()
	case "tab":
		if !e.lastWasInsert {
			e.pushUndo()
		}
		insertRun = true
		e.lastWasInsert = true
		n := e.TabWidth - e.cur.col%e.TabWidth
		e.cur = e.insertAt(e.cur, strings.Repeat(" ", n))
		e.wantX = e.cur.col
	case "space":
		text = " "
		fallthrough
	default:
		if text != "" && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
			if !e.lastWasInsert || text == " " {
				e.pushUndo() // one undo step per word
			}
			insertRun = true
			e.lastWasInsert = true
			e.cur = e.insertAt(e.cur, text)
			e.wantX = e.cur.col
		}
	}
	return ActionNone
}

func (e *Editor) handleCtrlK(key string) Action {
	defer e.scroll()
	k := strings.TrimPrefix(key, "ctrl+") // ^K ^B behaves like ^K B
	switch strings.ToLower(k) {
	case "b":
		e.markB, e.hasMarkB = e.cur, true
		e.stampBlock()
		e.Status = "Block begin set"
	case "k":
		e.markK, e.hasMarkK = e.cur, true
		e.stampBlock()
		e.Status = "Block end set"
	case "c": // copy block to cursor
		b, kk, ok := e.block()
		if !ok {
			return ActionCopyOtherBlock // the host looks in other editors
		}
		txt := e.textRange(b, kk)
		e.pushUndo()
		end := e.insertAt(e.cur, txt)
		e.markB, e.markK = e.cur, end
	case "m": // move block to cursor
		b, kk, ok := e.block()
		if !ok {
			return ActionMoveOtherBlock // the host looks in other editors
		}
		if !e.cur.less(b) && e.cur.less(kk) {
			e.Status = "Cursor is inside block"
			return ActionNone
		}
		txt := e.textRange(b, kk)
		e.pushUndo()
		target := e.cur
		if !target.less(b) { // target after block: adjust for removed text
			target = adjustAfterDelete(target, b, kk)
		}
		e.deleteRange(b, kk)
		end := e.insertAt(target, txt)
		e.cur = target
		e.markB, e.markK = target, end
	case "y": // delete block
		b, kk, ok := e.block()
		if !ok {
			e.Status = "No block"
			return ActionNone
		}
		e.pushUndo()
		e.deleteRange(b, kk)
		e.hasMarkB, e.hasMarkK = false, false
	case "u": // top of file
		e.cur, e.wantX = pos{}, 0
	case "v": // end of file
		e.cur = pos{len(e.lines) - 1, len(e.lines[len(e.lines)-1])}
		e.wantX = e.cur.col
	case "l":
		return ActionGotoPrompt
	case "f":
		return ActionFindPrompt
	case "d":
		return ActionSavePrompt
	case "r":
		return ActionInsertFile
	case "w":
		if !e.HasBlock() {
			e.Status = "No block"
			return ActionNone
		}
		return ActionWriteBlock
	case "x":
		return ActionExit
	case "q":
		return ActionCloseWindow
	case "e":
		return ActionSessions
	case "i":
		return ActionLoadFile
	case "o":
		return ActionNewWindow
	case "n":
		return ActionNextWindow
	case "p":
		return ActionPrevWindow
	case "h":
		return ActionHelp
	case "a": // centre line (JOE: ^K A) -- here: clear block marks
		e.hasMarkB, e.hasMarkK = false, false
		e.Status = "Block cleared"
	case "esc", "g":
		// cancel
	default:
		e.Status = "Unknown ^K command: " + key
	}
	return ActionNone
}

func adjustAfterDelete(p, b, k pos) pos {
	if p.row == k.row {
		return pos{b.row, b.col + (p.col - k.col)}
	}
	return pos{p.row - (k.row - b.row), p.col}
}

func (e *Editor) clamp(p pos) pos {
	p.row = min(max(p.row, 0), len(e.lines)-1)
	p.col = min(max(p.col, 0), len(e.lines[p.row]))
	return p
}

func (e *Editor) moveLeft() {
	if e.cur.col > 0 {
		e.cur.col--
	} else if e.cur.row > 0 {
		e.cur.row--
		e.cur.col = len(e.lines[e.cur.row])
	}
	e.wantX = e.cur.col
}

func (e *Editor) moveRight() {
	if e.cur.col < len(e.lines[e.cur.row]) {
		e.cur.col++
	} else if e.cur.row < len(e.lines)-1 {
		e.cur.row++
		e.cur.col = 0
	}
	e.wantX = e.cur.col
}

func (e *Editor) moveVert(d int) {
	e.cur.row = min(max(e.cur.row+d, 0), len(e.lines)-1)
	e.cur.col = min(e.wantX, len(e.lines[e.cur.row]))
}

// home toggles between the first non-blank character and column 0.
func (e *Editor) home() {
	line := e.lines[e.cur.row]
	ind := 0
	for ind < len(line) && line[ind] == ' ' {
		ind++
	}
	if e.cur.col == ind {
		e.cur.col = 0
	} else {
		e.cur.col = ind
	}
	e.wantX = e.cur.col
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#'
}

func (e *Editor) wordRight() {
	line := e.lines[e.cur.row]
	if e.cur.col >= len(line) {
		e.moveRight()
		return
	}
	c := e.cur.col
	for c < len(line) && isWord(line[c]) {
		c++
	}
	for c < len(line) && !isWord(line[c]) {
		c++
	}
	e.cur.col, e.wantX = c, c
}

func (e *Editor) wordLeft() {
	if e.cur.col == 0 {
		e.moveLeft()
		return
	}
	line := e.lines[e.cur.row]
	c := e.cur.col
	for c > 0 && !isWord(line[c-1]) {
		c--
	}
	for c > 0 && isWord(line[c-1]) {
		c--
	}
	e.cur.col, e.wantX = c, c
}

func (e *Editor) backspace() {
	if e.cur.col > 0 {
		line := e.lines[e.cur.row]
		e.lines[e.cur.row] = append(line[:e.cur.col-1:e.cur.col-1], line[e.cur.col:]...)
		e.cur.col--
	} else if e.cur.row > 0 {
		prev := e.lines[e.cur.row-1]
		col := len(prev)
		e.lines[e.cur.row-1] = append(append([]rune{}, prev...), e.lines[e.cur.row]...)
		e.lines = append(e.lines[:e.cur.row], e.lines[e.cur.row+1:]...)
		e.cur = pos{e.cur.row - 1, col}
	}
	e.wantX = e.cur.col
}

func (e *Editor) deleteChar() {
	line := e.lines[e.cur.row]
	if e.cur.col < len(line) {
		e.lines[e.cur.row] = append(line[:e.cur.col:e.cur.col], line[e.cur.col+1:]...)
	} else if e.cur.row < len(e.lines)-1 {
		e.lines[e.cur.row] = append(append([]rune{}, line...), e.lines[e.cur.row+1]...)
		e.lines = append(e.lines[:e.cur.row+1], e.lines[e.cur.row+2:]...)
	}
}

func (e *Editor) deleteLine() {
	if len(e.lines) == 1 {
		e.lines[0] = []rune{}
	} else {
		e.lines = append(e.lines[:e.cur.row], e.lines[e.cur.row+1:]...)
	}
	e.cur = e.clamp(pos{e.cur.row, e.wantX})
}

func (e *Editor) deleteWordRight() {
	line := e.lines[e.cur.row]
	if e.cur.col >= len(line) {
		e.deleteChar()
		return
	}
	c := e.cur.col
	if isWord(line[c]) {
		for c < len(line) && isWord(line[c]) {
			c++
		}
	} else {
		for c < len(line) && !isWord(line[c]) {
			c++
		}
	}
	e.lines[e.cur.row] = append(line[:e.cur.col:e.cur.col], line[c:]...)
}

func (e *Editor) deleteWordLeft() {
	if e.cur.col == 0 {
		e.backspace()
		return
	}
	line := e.lines[e.cur.row]
	c := e.cur.col
	if isWord(line[c-1]) {
		for c > 0 && isWord(line[c-1]) {
			c--
		}
	} else {
		for c > 0 && !isWord(line[c-1]) {
			c--
		}
	}
	e.lines[e.cur.row] = append(line[:c:c], line[e.cur.col:]...)
	e.cur.col, e.wantX = c, c
}

func (e *Editor) newline() {
	indent := ""
	if e.AutoIndent {
		line := e.lines[e.cur.row]
		i := 0
		for i < len(line) && i < e.cur.col && line[i] == ' ' {
			i++
		}
		indent = strings.Repeat(" ", i)
	}
	e.cur = e.insertAt(e.cur, "\n"+indent)
	e.wantX = e.cur.col
}

// ---- rendering ----

func (e *Editor) gutterWidth() int { return len(fmt.Sprint(len(e.lines))) + 1 }

func (e *Editor) scroll() {
	e.cur = e.clamp(e.cur)
	if e.cur.row < e.top {
		e.top = e.cur.row
	}
	if e.cur.row >= e.top+e.height {
		e.top = e.cur.row - e.height + 1
	}
	textW := max(e.width-e.gutterWidth(), 1)
	if e.cur.col < e.left {
		e.left = e.cur.col
	}
	if e.cur.col >= e.left+textW {
		e.left = e.cur.col - textW + 1
	}
}

func (e *Editor) View() string {
	e.scroll()
	gw := e.gutterWidth()
	textW := max(e.width-gw, 1)
	b, k, hasBlock := e.block()
	inBlock := func(r, c int) bool {
		p := pos{r, c}
		return hasBlock && !p.less(b) && p.less(k)
	}

	// lexer state at the first visible line
	var lex lexState
	if e.Highlight {
		for r := 0; r < e.top && r < len(e.lines); r++ {
			lex = scanState(e.lines[r], lex)
		}
	}

	pops := e.popupCells(textW)

	var out strings.Builder
	for i := 0; i < e.height; i++ {
		r := e.top + i
		if i > 0 {
			out.WriteByte('\n')
		}
		if r >= len(e.lines) {
			out.WriteString(e.styles.LineNo.Render(fmt.Sprintf("%*s", gw, "~ ")))
			if p, ok := pops[i]; ok {
				out.WriteString(strings.Repeat(" ", p.x))
				st := popupStyle
				if p.sel {
					st = popupSelStyle
				}
				out.WriteString(st.Render(string(p.text)))
				out.WriteString(strings.Repeat(" ", max(textW-p.x-len(p.text), 0)))
			} else {
				out.WriteString(strings.Repeat(" ", textW))
			}
			continue
		}
		ln := e.styles.LineNo
		if r == e.cur.row {
			ln = e.styles.LineNoCur
		}
		out.WriteString(ln.Render(fmt.Sprintf("%*d ", gw-1, r+1)))

		line := e.lines[r]
		var kinds []tokKind
		if e.Highlight {
			kinds, lex = lexLine(line, lex)
		}
		// group consecutive cells with the same style to keep output small
		var seg []rune
		segStyle := -1 // 0 text, 1 block, 2 cursor, 3+tokKind syntax
		flush := func() {
			if len(seg) == 0 {
				return
			}
			s := string(seg)
			switch {
			case segStyle == 50:
				out.WriteString(popupStyle.Render(s))
			case segStyle == 51:
				out.WriteString(popupSelStyle.Render(s))
			case segStyle == 1:
				out.WriteString(e.styles.Block.Render(s))
			case segStyle == 2:
				out.WriteString(e.styles.Cursor.Render(s))
			case segStyle > 3:
				out.WriteString(e.styles.Syntax[segStyle-3].Render(s))
			default:
				out.WriteString(s)
			}
			seg = seg[:0]
		}
		pl, hasPop := pops[i]
		for x := 0; x < textW; x++ {
			c := e.left + x
			ch := ' '
			if c < len(line) {
				ch = line[c]
			}
			if hasPop && x >= pl.x && x < pl.x+len(pl.text) {
				st := 50
				if pl.sel {
					st = 51
				}
				if st != segStyle {
					flush()
					segStyle = st
				}
				seg = append(seg, pl.text[x-pl.x])
				continue
			}
			st := 0
			if e.Focused && r == e.cur.row && c == e.cur.col {
				st = 2
			} else if c < len(line) && inBlock(r, c) || (c == len(line) && inBlock(r, c) && r < k.row) {
				st = 1
			} else if c < len(kinds) && kinds[c] != tkPlain {
				st = 3 + int(kinds[c])
			}
			if st != segStyle {
				flush()
				segStyle = st
			}
			seg = append(seg, ch)
		}
		flush()
	}
	return out.String()
}
