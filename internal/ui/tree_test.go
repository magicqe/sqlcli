package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
		focusTree:    "Enter open  ← close  / filter  x no filter  c collapse  i insert name  d desc  s select  ^D ddl",
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

func TestObjectTreeFilter(t *testing.T) {
	m := treeModel(t)
	m.focus = focusTree
	if !strings.Contains(ansi.Strip(m.bottomLine()), "/ filter") {
		t.Fatalf("hint: %q", m.bottomLine())
	}
	press(m, key('/'))
	if m.mode != modePrompt || !strings.Contains(m.prompt.label, "Filter objects") {
		t.Fatalf("filter question: mode %v %q", m.mode, m.prompt.label)
	}
	// the tree follows as you type
	typeText(m, "em")
	s := screen(m)
	for _, want := range []string{"▾ Tables  1/3", "EMP", "▾ Sequences  1/1", "EMP_SEQ", "/ em  2 matches"} {
		if !strings.Contains(s, want) {
			t.Fatalf("live filter: missing %q\n%s", want, s)
		}
	}
	for _, gone := range []string{"DEPT", "Packages", "Mixed"} {
		if strings.Contains(s, gone) {
			t.Fatalf("live filter: %q should be hidden\n%s", gone, s)
		}
	}
	if n := m.tree().selected(); n == nil || n.name != "EMP" {
		t.Fatalf("the first match should be selected: %+v", n)
	}
	press(m, key(tea.KeyEnter))
	if m.mode != modeNormal || m.focus != focusTree || m.tree().filter != "em" || !strings.Contains(screen(m), "/ em  2 matches") {
		t.Fatalf("after Enter: mode %v focus %v filter %q", m.mode, m.focus, m.tree().filter)
	}

	// wildcards, case-insensitive; no match
	press(m, key('f'))
	press(m, key('y', tea.ModCtrl))
	typeText(m, "t*pkg")
	if s := screen(m); !strings.Contains(s, "TESTPKG") || strings.Contains(s, "EMP") {
		t.Fatalf("wildcard:\n%s", s)
	}
	typeText(m, "zz")
	if s := screen(m); !strings.Contains(s, "no match") || !strings.Contains(s, "0 matches") {
		t.Fatalf("no match:\n%s", s)
	}
	// Esc goes back to the previous filter
	press(m, key(tea.KeyEscape))
	if m.tree().filter != "em" || !strings.Contains(screen(m), "EMP_SEQ") {
		t.Fatalf("Esc: filter %q", m.tree().filter)
	}
	// an empty filter shows everything again
	press(m, key('/'))
	press(m, key('y', tea.ModCtrl))
	press(m, key(tea.KeyEnter))
	if s := screen(m); m.tree().filter != "" || !strings.Contains(s, "Packages  1") || strings.Contains(s, "matches") {
		t.Fatalf("cleared:\n%s", s)
	}

	// the filter survives a reload of the tree
	m.tree().setFilter("dept")
	m.loadTree(true)
	m.Update(treeObjectsMsg{conn: "ora", objs: []db.SchemaObject{{Type: "TABLE", Name: "EMP", Valid: true}, {Type: "TABLE", Name: "DEPT", Valid: true}}})
	if s := screen(m); !strings.Contains(s, "DEPT") || strings.Contains(s, "EMP") || !strings.Contains(s, "/ dept  1 match") {
		t.Fatalf("after reload:\n%s", s)
	}
}

func TestObjectTreeClearFilterAndCollapse(t *testing.T) {
	m := treeModel(t)
	m.focus = focusTree
	tr := m.tree()
	tr.setFilter("emp_seq")
	if n := tr.selected(); n == nil || n.name != "EMP_SEQ" {
		t.Fatalf("selected %+v", n)
	}
	// x removes the filter and keeps the selected object
	press(m, key('x'))
	if tr.filter != "" || !strings.Contains(screen(m), "Packages  1") || m.status != "Object filter removed" {
		t.Fatalf("x: filter %q status %q\n%s", tr.filter, m.status, screen(m))
	}
	if n := tr.selected(); n == nil || n.name != "EMP_SEQ" {
		t.Fatalf("x should keep the selection: %+v", n)
	}
	press(m, key('x'))
	if !strings.Contains(m.status, "No object filter") {
		t.Fatalf("x without a filter: %q", m.status)
	}

	// c collapses everything and selects the group of the selection
	tr.groups[0].open = true // Tables
	tr.groups[1].open = true // Packages
	tr.selectNode(tr.groups[2].children[0]) // EMP_SEQ again (opening groups moved it)
	press(m, key('c'))
	nodes, _ := tr.visible()
	if len(nodes) != len(tr.groups) {
		t.Fatalf("c: %d visible nodes, want %d groups", len(nodes), len(tr.groups))
	}
	if n := tr.selected(); n == nil || n.label != "Sequences" {
		t.Fatalf("c should select the selection's group: %+v", n)
	}
	if s := ansi.Strip(m.bottomLine()); !strings.Contains(s, "x no filter") || !strings.Contains(s, "c collapse") {
		t.Fatalf("hint: %q", s)
	}
}
