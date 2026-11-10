package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/db"
)

// fakeClipboard replaces the desktop clipboard tool and records copies.
func fakeClipboard(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := systemCopy
	systemCopy = func(s string) { got = append(got, s) }
	t.Cleanup(func() { systemCopy = old })
	return &got
}

func drag(m *Model, x1, y1, x2, y2 int) {
	m.Update(tea.MouseClickMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: (x1 + x2) / 2, Y: y2, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x2, Y: y2, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x2, Y: y2, Button: tea.MouseLeft})
	drain(m, cmd)
}

func TestDragCopiesEditorText(t *testing.T) {
	clip := fakeClipboard(t)
	m := mouseModel()
	m.ed.SetText("select a, b\n  from t\n where c = 1")
	x1, y1 := find(t, m, "a, b")
	x2, y2 := find(t, m, "from t")
	drag(m, x1, y1, x2+4, y2) // from "a" to just before " t"
	if len(*clip) != 1 || (*clip)[0] != "a, b\n  from" {
		t.Fatalf("copied %q", *clip)
	}
	if m.status != "Copied 11 character(s) to the clipboard" || m.ed.BlockText() != "a, b\n  from" {
		t.Fatalf("status %q block %q", m.status, m.ed.BlockText())
	}
	// a plain click (no drag) clears the mouse selection and copies nothing
	click(m, x1, y1)
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	drain(m, cmd)
	if m.ed.HasBlock() || len(*clip) != 1 {
		t.Fatalf("plain click: block=%v copies=%d", m.ed.HasBlock(), len(*clip))
	}
	// a ^K B / ^K K block is not cleared by a click
	m.ed.SetCursor(0, 0)
	ctrlK(m, 'b')
	m.ed.SetCursor(0, 6)
	ctrlK(m, 'k')
	click(m, x2, y2)
	if m.ed.BlockText() != "select" {
		t.Fatal("click cleared a keyboard block")
	}
}

func TestDragCopiesResultRows(t *testing.T) {
	clip := fakeClipboard(t)
	m := mouseModel()
	r := &db.Result{IsQuery: true, Columns: []string{"ID", "NAME"}}
	for _, row := range [][]string{{"1", "a"}, {"2", db.Null}, {"3", "c d"}, {"4", "x"}} {
		r.Rows = append(r.Rows, row)
	}
	m.res.set(r, nil, nil, "")
	x, y1 := find(t, m, "│ a ")
	_, y3 := find(t, m, "│ c d")
	drag(m, x, y1, x, y3)
	if len(*clip) != 1 || (*clip)[0] != "1\ta\n2\t\n3\tc d" {
		t.Fatalf("copied %q", *clip)
	}
	if m.status != "Copied 3 row(s) to the clipboard" || m.res.row != 2 || !m.res.rangeOn {
		t.Fatalf("status %q row %d range %v", m.status, m.res.row, m.res.rangeOn)
	}
	if v := m.res.view(true); strings.Count(v, "48;5;237") < 2 { // rows 1-2 dim, row 3 selected
		t.Fatalf("range not highlighted:\n%q", v)
	}
	press(m, key(tea.KeyDown)) // a key ends the range
	if m.res.rangeOn {
		t.Fatal("key should clear the range")
	}
}

func TestDragCopiesOutputLines(t *testing.T) {
	clip := fakeClipboard(t)
	m := mouseModel()
	m.res.set(&db.Result{Message: "PL/SQL procedure successfully completed."}, []string{"PL/SQL procedure successfully completed.  (2ms)"}, []string{"first", "second", "third"}, "")
	x, y1 := find(t, m, "first")
	_, y2 := find(t, m, "second")
	drag(m, x, y1, x, y2)
	if len(*clip) != 1 || (*clip)[0] != "first\nsecond" {
		t.Fatalf("copied %q", *clip)
	}
	if m.status != "Copied 2 line(s) to the clipboard" {
		t.Fatalf("status %q", m.status)
	}
}

func TestRecordViewCopies(t *testing.T) {
	clip := fakeClipboard(t)
	m := editModel("select * from t", []string{"ID", "NOTE", "EMPTY"}, [][]string{{"7", "line 1\r\nline 2", db.Null}})
	press(m, key(tea.KeyEnter)) // record view
	press(m, key(tea.KeyDown))  // NOTE
	press(m, key('c'))
	if len(*clip) != 1 || (*clip)[0] != "line 1\r\nline 2" || m.mode != modeRecord {
		t.Fatalf("c: %q mode %v", *clip, m.mode)
	}
	if !strings.Contains(screen(m), "Copied NOTE to the clipboard (14 characters)") {
		t.Fatalf("status in the record view:\n%s", screen(m))
	}
	press(m, key(tea.KeyDown)) // EMPTY: NULL copies as nothing
	press(m, key('c'))
	if (*clip)[1] != "" {
		t.Fatalf("NULL: %q", (*clip)[1])
	}
	press(m, key('C'))
	want := "ID:    7\nNOTE:  line 1\n       line 2\nEMPTY: \n"
	if (*clip)[2] != want || m.mode != modeRecord || !strings.Contains(screen(m), "Copied row 1") {
		t.Fatalf("C: %q", (*clip)[2])
	}
}

func TestGridCopies(t *testing.T) {
	clip := fakeClipboard(t)
	m := editModel("select * from t", []string{"ID", "NAME"}, [][]string{{"1", "Anna"}, {"2", db.Null}})
	press(m, key(tea.KeyRight)) // NAME
	press(m, key('c'))
	press(m, key(tea.KeyDown))
	press(m, key('c')) // NULL
	press(m, key('C'))
	if len(*clip) != 3 || (*clip)[0] != "Anna" || (*clip)[1] != "" || (*clip)[2] != "ID:   2\nNAME: \n" {
		t.Fatalf("copies: %q", *clip)
	}
	if m.mode != modeNormal || m.focus != focusResults || !strings.Contains(m.status, "Copied row 2") {
		t.Fatalf("mode %v focus %v status %q", m.mode, m.focus, m.status)
	}
	m.res.set(nil, nil, nil, "")
	press(m, key('c'))
	if len(*clip) != 3 || !m.statusErr {
		t.Fatalf("no result: %q %q", *clip, m.status)
	}
}

func TestGridBlockSelection(t *testing.T) {
	clip := fakeClipboard(t)
	m := editModel("select * from t", []string{"ID", "NAME", "NOTE"}, [][]string{
		{"1", "Anna", "a\tb"}, {"2", db.Null, `say "hi"`}, {"3", "Cyril", "x"}})
	press(m, key(tea.KeyDown, tea.ModShift))  // rows 1-2
	press(m, key(tea.KeyRight, tea.ModShift)) // columns ID-NAME
	if r1, r2, c1, c2, ok := m.res.blockRange(); !ok || r1 != 0 || r2 != 1 || c1 != 0 || c2 != 1 {
		t.Fatalf("block %d-%d x %d-%d %v", r1, r2, c1, c2, ok)
	}
	if !strings.Contains(m.res.summary(), "2×2 selected") || !strings.Contains(m.res.view(true), "48;5;24") {
		t.Fatalf("summary %q / no highlight", m.res.summary())
	}
	if s := ansi.Strip(m.bottomLine()); !strings.Contains(s, "c copy cells") || !strings.Contains(s, "Esc unselect") {
		t.Fatalf("hints: %q", s)
	}
	press(m, key('c'))
	press(m, key('C')) // the selection stays for another copy
	if len(*clip) != 2 || (*clip)[0] != "1\tAnna\n2\t\n" || (*clip)[1] != "ID\tNAME\n1\tAnna\n2\t\n" {
		t.Fatalf("copies: %q", *clip)
	}
	// extend to the NOTE column: quoting of tabs and quotes
	press(m, key(tea.KeyRight, tea.ModShift))
	press(m, key('c'))
	if (*clip)[2] != "1\tAnna\t\"a\tb\"\n2\t\t\"say \"\"hi\"\"\"\n" || !strings.Contains(m.status, "2×3 cells") {
		t.Fatalf("quoted: %q %q", (*clip)[2], m.status)
	}
	// Esc clears the selection (focus stays); a plain arrow too
	press(m, key(tea.KeyEscape))
	if m.res.blockOn || m.focus != focusResults {
		t.Fatalf("Esc: block %v focus %v", m.res.blockOn, m.focus)
	}
	press(m, key(tea.KeyUp, tea.ModShift))
	press(m, key(tea.KeyDown))
	if m.res.blockOn {
		t.Fatal("a plain key should end the selection")
	}
	press(m, key('c')) // a single cell again
	if (*clip)[3] != "x" && (*clip)[3] != `say "hi"` {
		t.Fatalf("single cell: %q", (*clip)[3])
	}
}

func TestGridShiftUpDownSelectsRows(t *testing.T) {
	clip := fakeClipboard(t)
	m := editModel("select * from t", []string{"ID", "NAME", "NOTE"}, [][]string{
		{"1", "Anna", "a"}, {"2", "Bob", "b"}, {"3", "Cyril", "c"}})
	press(m, key(tea.KeyRight)) // cursor on NAME: whole rows are selected anyway
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyDown, tea.ModShift))
	press(m, key(tea.KeyUp, tea.ModShift))
	press(m, key(tea.KeyUp, tea.ModShift)) // rows 1-2
	if r1, r2, c1, c2, ok := m.res.blockRange(); !ok || r1 != 0 || r2 != 1 || c1 != 0 || c2 != 2 {
		t.Fatalf("rows: %d-%d x %d-%d", r1, r2, c1, c2)
	}
	if !strings.Contains(m.res.summary(), "2 row(s) selected") {
		t.Fatalf("summary %q", m.res.summary())
	}
	press(m, key('c'))
	press(m, key('C'))
	if (*clip)[0] != "1\tAnna\ta\n2\tBob\tb\n" || (*clip)[1] != "ID\tNAME\tNOTE\n1\tAnna\ta\n2\tBob\tb\n" || !strings.Contains(m.status, "2 row(s) with column names") {
		t.Fatalf("copies %q status %q", *clip, m.status)
	}
	// Shift-→ narrows to the columns from the start column (NAME) to the cursor
	press(m, key(tea.KeyRight, tea.ModShift))
	press(m, key('c'))
	if (*clip)[2] != "Anna\ta\nBob\tb\n" || !strings.Contains(m.res.summary(), "2×2 selected") {
		t.Fatalf("narrowed: %q %q", (*clip)[2], m.res.summary())
	}
	// in the view, every column of the selected rows is highlighted
	press(m, key(tea.KeyEscape))
	press(m, key(tea.KeyDown, tea.ModShift))
	if v := m.res.view(true); strings.Count(v, "48;5;24") < 3 {
		t.Fatalf("whole rows should be highlighted:\n%q", v)
	}
}

func TestGridSelectMode(t *testing.T) {
	clip := fakeClipboard(t)
	m := editModel("select * from t", []string{"ID", "NAME"}, [][]string{{"1", "a"}, {"2", "b"}, {"3", "c"}})
	press(m, key('v')) // selection mode: plain arrows extend, whole rows
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyDown))
	if r1, r2, c1, c2, ok := m.res.blockRange(); !ok || r1 != 0 || r2 != 2 || c1 != 0 || c2 != 1 {
		t.Fatalf("v + Down Down: %d-%d x %d-%d %v", r1, r2, c1, c2, ok)
	}
	if s := ansi.Strip(m.bottomLine()); !strings.Contains(s, "arrows extend") {
		t.Fatalf("hints: %q", s)
	}
	press(m, key('C'))
	if (*clip)[0] != "ID\tNAME\n1\ta\n2\tb\n3\tc\n" || !m.res.blockOn {
		t.Fatalf("copy: %q", *clip)
	}
	press(m, key('v')) // ends the mode and the selection
	press(m, key(tea.KeyUp))
	if m.res.blockOn || m.res.row != 1 {
		t.Fatalf("after v: block %v row %d", m.res.blockOn, m.res.row)
	}
	press(m, key(tea.KeySpace)) // Space works too; Esc ends it
	press(m, key(tea.KeyUp))
	press(m, key(tea.KeyEscape))
	if m.res.blockOn || m.res.blockSticky || m.focus != focusResults {
		t.Fatal("Esc should end the selection mode")
	}
}
