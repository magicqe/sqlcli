package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/db"
)

func editModel(sql string, cols []string, rows [][]string) *Model {
	m := mouseModel()
	m.conn = &db.Conn{Name: "ora"}
	m.res.set(&db.Result{IsQuery: true, Columns: cols, Rows: rows, SQL: sql}, nil, nil, "")
	m.focus = focusResults
	return m
}

func TestCellCursor(t *testing.T) {
	cols := []string{"ROWID", "ID", "A_LONG_COLUMN_NAME_1", "A_LONG_COLUMN_NAME_2", "A_LONG_COLUMN_NAME_3", "LAST"}
	m := editModel("select rowid, t.* from t", cols, [][]string{{"AAA", "1", "x", "y", "z", "end"}})
	for i := 0; i < 5; i++ {
		press(m, key(tea.KeyRight))
	}
	if m.res.selCol != 5 || !strings.Contains(m.res.view(true), "LAST") {
		t.Fatalf("cursor on the last column: sel %d\n%s", m.res.selCol, m.res.view(true))
	}
	if !strings.Contains(m.res.summary(), "col 6/6") {
		t.Fatalf("summary: %q", m.res.summary())
	}
	press(m, key(tea.KeyRight)) // stays on the last column
	if m.res.selCol != 5 {
		t.Fatal("moved past the last column")
	}
	if !strings.Contains(m.res.view(true), "\x1b[1;7m") && !strings.Contains(m.res.view(true), "7;1m") && !strings.Contains(m.res.view(true), "\x1b[7") {
		t.Fatalf("no cell highlight:\n%q", m.res.view(true))
	}
	press(m, key(tea.KeyHome))
	for i := 0; i < 5; i++ {
		press(m, key(tea.KeyLeft))
	}
	if m.res.selCol != 0 || m.res.colOff != 0 {
		t.Fatalf("back to the first column: sel %d off %d", m.res.selCol, m.res.colOff)
	}
}

func TestEditRefusals(t *testing.T) {
	cases := []struct {
		sql  string
		cols []string
		sel  int
		want string
	}{
		{"select * from emp", []string{"ID", "NAME"}, 1, "select rowid, t.* from emp t"},
		{"select e.rowid, e.name from emp e join dept d on d.id = e.dept", []string{"ROWID", "NAME"}, 1, "one table"},
		{"select rowid, t.* from emp t", []string{"ROWID", "NAME"}, 0, "ROWID can't be edited"},
	}
	for _, c := range cases {
		m := editModel(c.sql, c.cols, [][]string{{"a", "b"}})
		m.res.selCol = c.sel
		press(m, key('u'))
		if !m.statusErr || !strings.Contains(m.status, c.want) || m.mode != modeNormal {
			t.Errorf("%q: status %q", c.sql, m.status)
		}
	}
	m := editModel("select rowid, t.* from emp t", []string{"ROWID", "NAME"}, [][]string{{"a", "b"}})
	m.conn.ReadOnly = true
	m.res.selCol = 1
	press(m, key('u'))
	if !strings.Contains(m.status, "read-only") {
		t.Fatalf("read-only: %q", m.status)
	}
	m = mouseModel()
	m.focus = focusResults
	press(m, key('u'))
	if !strings.Contains(m.status, "run a query first") {
		t.Fatalf("no result: %q", m.status)
	}
}

func TestRevertEdits(t *testing.T) {
	m := editModel("select rowid, t.* from emp t", []string{"ROWID", "NAME"}, [][]string{{"a", "old"}})
	m.res.setCell(0, 1, "new")
	m.res.setCell(0, 1, "newer")
	if n := m.res.revertEdits(); n != 2 || m.res.res.Rows[0][1] != "old" {
		t.Fatalf("revert: %d %q", n, m.res.res.Rows[0][1])
	}
}
