package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
)

// find returns the screen cell (x, y) where text first appears.
func find(t *testing.T, m *Model, text string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(screen(m), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return len([]rune(line[:i])), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", text, screen(m))
	return 0, 0
}

func click(m *Model, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return cmd
}

func mouseModel() *Model {
	cfg := config.Default()
	cfg.Connections = []config.Connection{{Name: "alpha"}, {Name: "beta"}}
	m := New(cfg, nil, "alpha")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestCtrlKOutsideEditor(t *testing.T) {
	for _, f := range []focus{focusResults, focusConns} {
		m := mouseModel()
		m.focus = f
		m.Update(key('k', tea.ModCtrl))
		if !strings.Contains(screen(m), "^K-") {
			t.Fatalf("focus %v: no ^K- indicator", f)
		}
		_, cmd := m.Update(key('x'))
		if cmd == nil || !m.quitting {
			t.Fatalf("focus %v: ^K X did not quit", f)
		}
	}
	m := mouseModel()
	m.focus = focusResults
	m.Update(key('k', tea.ModCtrl))
	m.Update(key('b'))
	if !m.statusErr || !strings.Contains(m.status, "works in the SQL editor") || m.quitting {
		t.Fatalf("^K B in results: %q", m.status)
	}
	m.Update(key('k', tea.ModCtrl))
	m.Update(key('o')) // new tab from the results pane
	if len(m.tabs) != 2 {
		t.Fatal("^K O from results pane")
	}
}

func TestClickFocusesPanes(t *testing.T) {
	m := mouseModel()
	m.res.set(&db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"10"}, {"20"}, {"30"}}}, nil, nil, "")

	// results: click on the row showing "20"
	x, y := find(t, m, "│ 20")
	click(m, x+2, y)
	if m.focus != focusResults || m.res.row != 1 {
		t.Fatalf("results click: focus=%v row=%d", m.focus, m.res.row)
	}

	// editor: click on the 'w' of "two"
	m.ed.SetText("one\ntwo three")
	x, y = find(t, m, "two three")
	click(m, x+2, y)
	if m.focus != focusEditor || m.ed.CursorRow() != 1 || m.ed.CursorCol() != 2 {
		t.Fatalf("editor click: focus=%v cursor=%d,%d", m.focus, m.ed.CursorRow(), m.ed.CursorCol())
	}
	click(m, x+40, y) // past the end of the line
	if m.ed.CursorCol() != len("two three") {
		t.Fatalf("click past end: col %d", m.ed.CursorCol())
	}

	// connections: single click selects, double click connects
	x, y = find(t, m, "beta")
	click(m, x, y)
	if m.focus != focusConns || m.connSel != 1 || m.connecting {
		t.Fatalf("conn click: focus=%v sel=%d", m.focus, m.connSel)
	}
	if cmd := click(m, x, y); cmd == nil || !m.connecting || m.connectingTo != "beta" {
		t.Fatal("double click did not connect")
	}
}

func TestClickTabsAndClose(t *testing.T) {
	m := mouseModel()
	m.Update(key('k', tea.ModCtrl)) // editor ^K O
	m.Update(key('o'))
	if len(m.tabs) != 2 {
		t.Fatal("no second tab")
	}
	x, y := find(t, m, " 1 not connected ")
	click(m, x+2, y)
	if m.tabIndex(m.tab) != 0 {
		t.Fatal("tab bar click did not switch")
	}

	// [x] on the SQL panel closes the tab
	x, y = find(t, m, "[x]")
	click(m, x+1, y)
	if len(m.tabs) != 1 {
		t.Fatalf("[x] did not close the tab: %d tabs", len(m.tabs))
	}
	// on the last tab it asks before quitting
	x, y = find(t, m, "[x]")
	click(m, x, y)
	if m.mode != modePrompt || !strings.Contains(m.prompt.label, "quit sqlcli?") || m.quitting {
		t.Fatalf("last-tab [x]: mode=%v label=%q", m.mode, m.prompt.label)
	}
	typeText(m, "n")
	_, cmd := m.Update(key(tea.KeyEnter))
	if m.quitting || cmd != nil {
		t.Fatal("answering n must not quit")
	}
	click(m, x, y)
	typeText(m, "y")
	if _, cmd = m.Update(key(tea.KeyEnter)); cmd == nil || !m.quitting {
		t.Fatal("answering y should quit")
	}
}

func TestClickIgnoredInOverlays(t *testing.T) {
	m := mouseModel()
	m.mode = modeHelp
	click(m, 1, 3)
	if m.focus == focusConns {
		t.Fatal("click went through the help screen")
	}
}

func TestHelpScrolls(t *testing.T) {
	m := mouseModel() // 30 rows: the help doesn't fit
	m.Update(key(tea.KeyF1))
	if !strings.Contains(screen(m), "help 1-28/") || strings.Contains(screen(m), "MOUSE") {
		t.Fatalf("help should start at the top:\n%s", screen(m))
	}
	m.Update(key(tea.KeyEnd))
	if m.mode != modeHelp || !strings.Contains(screen(m), "MOUSE") {
		t.Fatal("End did not scroll to the bottom")
	}
	m.Update(key('q'))
	if m.mode != modeNormal {
		t.Fatal("other key should close help")
	}
}

func TestDoubleClickOpensRecordView(t *testing.T) {
	m := editModel("select * from t", []string{"ID", "NAME"}, [][]string{{"1", "Anna"}, {"2", "Bob"}, {"3", "Cyril"}})
	m.focus = focusEditor
	y, x := -1, 0
	for i, l := range strings.Split(screen(m), "\n") {
		if j := strings.Index(l, "Bob"); j >= 0 {
			y, x = i, len([]rune(l[:j]))
		}
	}
	if y < 0 {
		t.Fatalf("row not on screen:\n%s", screen(m))
	}
	click(m, x, y) // a single click selects the row
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.mode != modeNormal || m.focus != focusResults || m.res.row != 1 {
		t.Fatalf("single click: mode %v focus %v row %d", m.mode, m.focus, m.res.row)
	}
	click(m, x, y) // the second click: record view of that row
	if m.mode != modeRecord || m.res.row != 1 || !strings.Contains(screen(m), "Record 2 of 3") {
		t.Fatalf("double click: mode %v row %d\n%s", m.mode, m.res.row, screen(m))
	}
	// clicks on two different rows are not a double click
	press(m, key(tea.KeyEscape))
	click(m, x, y)
	click(m, x, y+1)
	if m.mode != modeNormal || m.res.row != 2 {
		t.Fatalf("two rows: mode %v row %d", m.mode, m.res.row)
	}
	// a slow second click isn't either
	m.lastClickAt = m.lastClickAt.Add(-time.Second)
	click(m, x, y+1)
	if m.mode != modeNormal {
		t.Fatal("slow clicks should not open the record view")
	}
}
