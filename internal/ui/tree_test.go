package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
)

func treeModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.Connections = []config.Connection{{Name: "ora"}, {Name: "rep"}}
	m := New(cfg, nil, "ora")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 30}) // the left panel gets its full width (30)
	m.conn = &db.Conn{Name: "ora"}
	m.trees = map[string]*objTree{"ora": {loading: true}}
	m.Update(treeObjectsMsg{conn: "ora", objs: []db.SchemaObject{
		{Type: "TABLE", Name: "EMP", Valid: true}, {Type: "TABLE", Name: "DEPT", Valid: true},
		{Type: "PACKAGE", Name: "TESTPKG", Valid: false}, {Type: "SEQUENCE", Name: "EMP_SEQ", Valid: true},
		{Type: "TABLE", Name: "Mixed", Valid: true},
	}})
	return m
}

func TestObjectTreeBrowse(t *testing.T) {
	m := treeModel(t)
	s := screen(m)
	for _, want := range []string{"Connections", "Objects · ora", "▸ Tables  3", "▸ Packages  1", "▸ Sequences  1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("screen lacks %q:\n%s", want, s)
		}
	}
	for _, l := range strings.Split(s, "\n") {
		if len([]rune(l)) > 200 {
			t.Fatalf("line too wide: %q", l)
		}
	}

	// Tab from the connections goes to the tree, Tab again to the editor
	m.focus = focusConns
	press(m, key(tea.KeyTab))
	if m.focus != focusTree {
		t.Fatalf("Tab: focus %v", m.focus)
	}
	press(m, key(tea.KeyEnter)) // open Tables
	if !strings.Contains(screen(m), "▾ Tables") || !strings.Contains(screen(m), "EMP") {
		t.Fatalf("open group:\n%s", screen(m))
	}
	press(m, key(tea.KeyDown)) // EMP (names come in the order loaded)
	tr := m.tree()
	if n := tr.selected(); n.name != "EMP" {
		t.Fatalf("selected %q", n.name)
	}
	cmd := m.updateTree("enter") // open EMP: loads its columns
	if cmd == nil || !tr.selected().open || !tr.selected().loading {
		t.Fatal("opening a table should load its columns")
	}
	m.Update(treeChildrenMsg{conn: "ora", objType: "TABLE", name: "EMP", children: []*treeNode{
		{label: "ID", extra: "NUMBER(5) NOT NULL", objType: "COLUMN"}, {label: "ENAME", extra: "VARCHAR2(10)", objType: "COLUMN"}}})
	if !strings.Contains(screen(m), "ID  NUMBER(5) NOT NULL") {
		t.Fatalf("columns:\n%s", screen(m))
	}
	press(m, key(tea.KeyDown)) // ID
	press(m, key('i'))         // insert the column name
	if m.ed.Text() != "ID" || m.focus != focusEditor {
		t.Fatalf("insert: %q focus %v", m.ed.Text(), m.focus)
	}
	m.focus = focusTree
	press(m, key(tea.KeyLeft)) // from the column to its table
	if tr.selected().name != "EMP" {
		t.Fatalf("left: %q", tr.selected().label)
	}
	press(m, key(tea.KeyLeft)) // close it
	if tr.selected().open {
		t.Fatal("left should close the table")
	}

	// invalid objects are marked; mixed-case names are quoted when inserted
	press(m, key(tea.KeyEnd)) // the last group: Sequences
	press(m, key(tea.KeyUp))  // Packages
	press(m, key(tea.KeyEnter))
	if !strings.Contains(screen(m), "TESTPKG ✗") {
		t.Fatalf("invalid mark:\n%s", screen(m))
	}
	if quoteIdent("Mixed") != `"Mixed"` || quoteIdent("EMP_1") != "EMP_1" {
		t.Fatal("quoteIdent")
	}

	// F2 hides the panel and moves focus away from the tree
	m.focus = focusTree
	press(m, key(tea.KeyF2))
	if m.showConns || m.focus == focusTree {
		t.Fatalf("F2: show=%v focus=%v", m.showConns, m.focus)
	}
}

func TestObjectTreeClick(t *testing.T) {
	m := treeModel(t)
	x, y := find(t, m, "▸ Packages")
	click(m, x+2, y)
	if m.focus != focusTree || m.tree().selected().label != "Packages" {
		t.Fatalf("click: focus %v sel %q", m.focus, m.tree().selected().label)
	}
	click(m, x+2, y) // double click opens it
	if !m.tree().selected().open {
		t.Fatal("double click should open the group")
	}
	// clicks in the connections box still select connections
	x, y = find(t, m, "rep")
	click(m, x, y)
	if m.focus != focusConns || m.connSel != 1 {
		t.Fatalf("connection click: focus %v sel %d", m.focus, m.connSel)
	}
}

func TestPanelKeyHints(t *testing.T) {
	m := treeModel(t)
	bottom := func() string {
		lines := strings.Split(screen(m), "\n")
		return lines[len(lines)-1]
	}
	h := m.connBoxHeight()
	m.focus = focusConns
	if m.connBoxHeight() != h || strings.Contains(screen(m), "a add") && !strings.Contains(bottom(), "a add") {
		t.Fatal("the connections box should not grow or show hints when focused")
	}
	m.res.set(&db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"1"}}}, nil, nil, "") // a grid
	for f, want := range map[focus]string{
		focusConns:   "Enter connect  a add  e edit  v view  d delete",
		focusTree:    "Enter open  ← close  i insert name  d desc  s select  ^D ddl",
		focusResults: "Enter record  u edit  i insert  d delete  e export",
		focusEditor:  "F1 help  F2 conns  F3 results",
	} {
		m.focus = f
		if !strings.Contains(bottom(), want) {
			t.Errorf("focus %v: bottom line %q lacks %q", f, bottom(), want)
		}
	}
	// the connections box shows only connections
	m.focus = focusConns
	lines := strings.Split(screen(m), "\n")
	for _, l := range lines[:h] {
		if strings.Contains(l, "Enter connect") {
			t.Fatal("key hints still inside the connections box")
		}
	}
}
