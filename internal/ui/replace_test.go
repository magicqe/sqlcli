package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestReplaceInEditor(t *testing.T) {
	m := mouseModel()
	m.focus = focusEditor
	m.ed.SetText("select * from emp;\nselect EMP.id from emp e;\ndelete emp;")
	m.ed.SetCursor(0, 0)

	press(m, key('r', tea.ModCtrl))
	answer(m, "emp")
	answer(m, "dept")
	if m.mode != modeReplace || !strings.Contains(m.bottomLine(), `Replace "emp" with "dept"?`) {
		t.Fatalf("asking: mode %v, %q", m.mode, m.bottomLine())
	}
	if !strings.Contains(m.View().Content, "48;5;220m") {
		t.Fatal("the match should be highlighted")
	}
	press(m, key('y')) // emp;
	press(m, key('n')) // EMP.id
	press(m, key('y')) // from emp e
	press(m, key(tea.KeyEscape))
	if m.mode != modeNormal || m.ed.Text() != "select * from dept;\nselect EMP.id from dept e;\ndelete emp;" {
		t.Fatalf("after y n y Esc: mode %v\n%s", m.mode, m.ed.Text())
	}
	if m.status != "2 occurrences replaced (^_ undoes all)" {
		t.Fatalf("status %q", m.status)
	}

	// ^K F with options: c (match case) + a (all, no questions)
	m.ed.SetCursor(0, 0)
	ctrlK(m, 'f')
	answer(m, "EMP")
	answer(m, "ca")
	answer(m, "X")
	if m.mode != modeNormal || m.ed.Text() != "select * from dept;\nselect X.id from dept e;\ndelete emp;" {
		t.Fatalf("match case, all: %q", m.ed.Text())
	}

	// ^K F without options just finds
	m.ed.SetCursor(0, 0)
	ctrlK(m, 'f')
	answer(m, "delete")
	answer(m, "")
	if m.ed.CursorRow() != 2 || m.ed.CursorCol() != 0 || m.mode != modeNormal {
		t.Fatalf("find: %d,%d", m.ed.CursorRow(), m.ed.CursorCol())
	}

	// nothing to replace
	press(m, key('r', tea.ModCtrl))
	answer(m, "nosuch")
	answer(m, "x")
	if m.mode != modeNormal || !m.statusErr || !strings.Contains(m.status, "Not found") {
		t.Fatalf("not found: %v %q", m.mode, m.status)
	}
}
