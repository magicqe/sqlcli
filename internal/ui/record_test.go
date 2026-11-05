package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/db"
)

func recordModel(n int) *Model {
	m := mouseModel()
	r := &db.Result{IsQuery: true, Columns: []string{"ID", "NAME", "NOTE"}}
	for i := 1; i <= n; i++ {
		note := db.Null
		if i == 2 {
			note = "line one\nline two"
		}
		r.Rows = append(r.Rows, []string{fmt.Sprint(i), fmt.Sprintf("name %d", i), note})
	}
	m.res.set(r, nil, nil, "")
	m.focus = focusResults
	return m
}

func TestRecordNavigation(t *testing.T) {
	m := recordModel(5)
	press(m, key(tea.KeyEnter))
	if m.mode != modeRecord || !strings.Contains(screen(m), "Record 1 of 5") || !strings.Contains(screen(m), "name 1") {
		t.Fatalf("record view:\n%s", screen(m))
	}
	if !strings.Contains(screen(m), "NOTE │ (null)") {
		t.Fatal("NULL not shown")
	}
	steps := []struct {
		k    tea.KeyPressMsg
		want int
	}{
		{key(tea.KeyRight), 2},
		{key('f', tea.ModCtrl), 3},
		{key(tea.KeyLeft), 2},
		{key('b', tea.ModCtrl), 1},
		{key(tea.KeyLeft), 1}, // stays on the first record
		{key(tea.KeyEnd), 5},
		{key(tea.KeyRight), 5}, // stays on the last record
		{key(tea.KeyHome), 1},
		{key('e', tea.ModCtrl), 5},
		{key('a', tea.ModCtrl), 1},
	}
	for i, s := range steps {
		press(m, s.k)
		if m.mode != modeRecord || m.res.row+1 != s.want {
			t.Fatalf("step %d (%s): mode=%v record=%d, want %d", i, s.k.String(), m.mode, m.res.row+1, s.want)
		}
		if !strings.Contains(screen(m), fmt.Sprintf("Record %d of 5", s.want)) || !strings.Contains(screen(m), fmt.Sprintf("name %d", s.want)) {
			t.Fatalf("step %d: screen does not show record %d", i, s.want)
		}
	}
	press(m, key(tea.KeyRight))
	if s := screen(m); !strings.Contains(s, "line one") || !strings.Contains(s, "line two") {
		t.Fatal("multi-line value not shown")
	}
	press(m, key(tea.KeyEscape))
	if m.mode != modeNormal || m.res.row != 1 {
		t.Fatalf("Esc should close and keep the selection: mode=%v row=%d", m.mode, m.res.row)
	}
}

func TestRecordArrowsMoveFields(t *testing.T) {
	m := recordModel(3)
	press(m, key(tea.KeyEnter))
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyDown)) // stays on the last field
	if m.mode != modeRecord || m.res.selCol != 2 || m.res.row != 0 {
		t.Fatalf("Down: field %d record %d", m.res.selCol, m.res.row+1)
	}
	press(m, key(tea.KeyRight)) // the next record keeps the field
	if m.res.row != 1 || m.res.selCol != 2 {
		t.Fatalf("Right: field %d record %d", m.res.selCol, m.res.row+1)
	}
	press(m, key(tea.KeyUp))
	press(m, key('p', tea.ModCtrl))
	press(m, key(tea.KeyUp)) // stays on the first field
	if m.res.selCol != 0 || m.res.row != 1 {
		t.Fatalf("Up: field %d record %d", m.res.selCol, m.res.row+1)
	}
	press(m, key('n', tea.ModCtrl))
	if m.res.selCol != 1 {
		t.Fatalf("Ctrl-N: field %d", m.res.selCol)
	}
	if !strings.Contains(screen(m), "←→ previous/next record  ↑↓ field") {
		t.Fatalf("hint:\n%s", screen(m))
	}
}

func TestRecordScrollsLongRecord(t *testing.T) {
	m := mouseModel()
	r := &db.Result{IsQuery: true}
	row := []string{}
	for i := 1; i <= 60; i++ {
		r.Columns = append(r.Columns, fmt.Sprintf("COL%02d", i))
		row = append(row, fmt.Sprint(i))
	}
	r.Rows = [][]string{row}
	m.res.set(r, nil, nil, "")
	m.focus = focusResults
	press(m, key(tea.KeyEnter))
	if !strings.Contains(screen(m), "lines 1-27/60") || strings.Contains(screen(m), "COL60") {
		t.Fatalf("long record:\n%s", screen(m))
	}
	press(m, key(tea.KeyPgDown, tea.ModShift))
	press(m, key(tea.KeyPgDown, tea.ModShift))
	if !strings.Contains(screen(m), "COL60") || m.mode != modeRecord {
		t.Fatal("Shift-PgDn did not scroll to the end")
	}
	press(m, key(tea.KeyUp, tea.ModShift))
	if !strings.Contains(screen(m), "lines 33-59/60") {
		t.Fatalf("Shift-Up did not scroll one line:\n%s", screen(m))
	}
	press(m, key(tea.KeyPgUp, tea.ModShift))
	press(m, key(tea.KeyPgUp, tea.ModShift))
	if !strings.Contains(screen(m), "COL01") {
		t.Fatal("Shift-PgUp did not scroll back")
	}
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if !strings.Contains(screen(m), "lines 4-30/60") {
		t.Fatal("wheel should scroll the record")
	}
}

func TestRecordPageKeys(t *testing.T) {
	m := recordModel(20)
	m.cfg.UI.RecordPage = 3
	press(m, key(tea.KeyEnter))
	if !strings.Contains(screen(m), "PgUp PgDn ±3 records") {
		t.Fatalf("hint should show the page size:\n%s", screen(m))
	}
	steps := []struct {
		k    tea.KeyPressMsg
		want int
	}{
		{key(tea.KeyPgDown), 4},
		{key(tea.KeyPgDown), 7},
		{key('v', tea.ModCtrl), 10},
		{key(tea.KeyPgUp), 7},
		{key('u', tea.ModCtrl), 4},
		{key(tea.KeyPgUp), 1},
		{key(tea.KeyPgUp), 1}, // clamps at the first record
		{key(tea.KeyEnd), 20},
		{key(tea.KeyPgDown), 20}, // clamps at the last record
		{key(tea.KeyPgUp), 17},
	}
	for i, s := range steps {
		press(m, s.k)
		if m.mode != modeRecord || m.res.row+1 != s.want {
			t.Fatalf("step %d (%s): record %d, want %d", i, s.k.String(), m.res.row+1, s.want)
		}
	}
	if !strings.Contains(screen(m), "Record 17 of 20") {
		t.Fatal("title not updated")
	}
}

func TestSelectedRowColour(t *testing.T) {
	m := recordModel(3)
	m.res.row = 1
	view := m.res.view(true)
	if !strings.Contains(view, "48;5;24") { // blue background on the selected row
		t.Fatalf("focused selection colour missing:\n%q", view)
	}
	if v := m.res.view(false); !strings.Contains(v, "48;5;237") || strings.Contains(v, "48;5;24") {
		t.Fatalf("unfocused selection should be dim grey:\n%q", v)
	}
	SetSelectionColor("#3a5f9e")
	defer SetSelectionColor("24")
	if v := m.res.view(true); !strings.Contains(v, "48;2;58;95;158") {
		t.Fatalf("custom colour not used:\n%q", v)
	}
}
