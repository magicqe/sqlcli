package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	if (*clip)[2] != want || m.mode != modeRecord || !strings.Contains(screen(m), "Copied record 1") {
		t.Fatalf("C: %q", (*clip)[2])
	}
}
