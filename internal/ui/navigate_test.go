package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestF12AndCtrlArrows(t *testing.T) {
	m := treeModel(t)
	m.focus = focusEditor
	press(m, key(tea.KeyF12))
	if m.focus != focusTree {
		t.Fatalf("F12: %v", m.focus)
	}
	moves := []struct {
		from focus
		dir  rune
		to   focus
	}{
		{focusEditor, tea.KeyDown, focusResults},
		{focusResults, tea.KeyUp, focusEditor},
		{focusEditor, tea.KeyLeft, focusConns},
		{focusConns, tea.KeyRight, focusEditor},
		{focusResults, tea.KeyLeft, focusTree},
		{focusTree, tea.KeyRight, focusResults},
		{focusConns, tea.KeyDown, focusTree},
		{focusTree, tea.KeyUp, focusConns},
		{focusConns, tea.KeyUp, focusConns}, // nothing above: stays
	}
	for _, mv := range moves {
		m.focus = mv.from
		press(m, key(mv.dir, tea.ModCtrl))
		if m.focus != mv.to {
			t.Errorf("Ctrl-%s from %v: %v, want %v", key(mv.dir).String(), mv.from, m.focus, mv.to)
		}
	}
	// a hidden panel is shown when jumped to
	m.focus = focusEditor
	press(m, key(tea.KeyF2)) // hide the left column
	press(m, key(tea.KeyLeft, tea.ModCtrl))
	if !m.showConns || m.focus != focusConns {
		t.Fatalf("Ctrl-Left to a hidden panel: show=%v focus=%v", m.showConns, m.focus)
	}
	m.focus = focusEditor
	press(m, key(tea.KeyF2))
	press(m, key(tea.KeyF12))
	if !m.showConns || m.focus != focusTree {
		t.Fatal("F12 should show the left column")
	}
	// Ctrl-Left in the editor no longer moves by word; Alt-Left does
	m.focus = focusEditor
	m.ed.SetText("one two")
	m.ed.SetCursor(0, 7)
	press(m, key(tea.KeyLeft, tea.ModAlt))
	if m.ed.CursorCol() != 4 {
		t.Fatalf("Alt-Left: col %d", m.ed.CursorCol())
	}
}

func TestTabOrder(t *testing.T) {
	m := treeModel(t)
	m.focus = focusConns
	for _, want := range []focus{focusTree, focusResults, focusEditor} {
		press(m, key(tea.KeyTab))
		if m.focus != want {
			t.Fatalf("Tab: %v, want %v", m.focus, want)
		}
	}
	press(m, key(tea.KeyTab)) // in the editor Tab indents
	if m.focus != focusEditor || m.ed.Text() == "" {
		t.Fatalf("Tab in the editor should indent: focus %v text %q", m.focus, m.ed.Text())
	}
	for _, want := range []focus{focusResults, focusTree, focusConns, focusEditor} { // Shift-Tab goes back (wraps)
		press(m, key(tea.KeyTab, tea.ModShift))
		if m.focus != want {
			t.Fatalf("Shift-Tab: %v, want %v", m.focus, want)
		}
	}
	// hidden panels are skipped
	press(m, key(tea.KeyF3)) // hide results
	m.focus = focusTree
	press(m, key(tea.KeyTab))
	if m.focus != focusEditor {
		t.Fatalf("Tab with results hidden: %v", m.focus)
	}
}
