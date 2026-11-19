package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Windows reports Ctrl, Shift ... pressed alone, and repeats them while held.
func modifier(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, BaseCode: code, Mod: mod}
}

func TestModifierOnlyKeysAreIgnored(t *testing.T) {
	m := mouseModel()
	m.focus = focusEditor
	m.ed.SetText("select emp from dual")
	m.ed.SetCursor(0, 0)
	ctrl := modifier(tea.KeyLeftCtrl, tea.ModCtrl)
	// ^K F with Ctrl held: ctrl, ctrl+k, ctrl (repeat), ctrl+f
	press(m, ctrl)
	press(m, key('k', tea.ModCtrl))
	press(m, ctrl)
	press(m, ctrl)
	press(m, key('f', tea.ModCtrl))
	if m.mode != modePrompt || !strings.Contains(m.prompt.label, "Find") {
		t.Fatalf("^K F with modifier repeats: mode %v status %q", m.mode, m.ed.Status)
	}
	shift := modifier(tea.KeyLeftShift, tea.ModShift)
	press(m, shift) // Shift for a capital letter: nothing typed
	typeText(m, "EMP")
	press(m, key(tea.KeyEnter))
	answer(m, "") // options: just find
	if m.ed.CursorCol() != 7 || m.mode != modeNormal {
		t.Fatalf("find: col %d mode %v", m.ed.CursorCol(), m.mode)
	}
	if m.ed.Text() != "select emp from dual" {
		t.Fatalf("text changed: %q", m.ed.Text())
	}

	// Shift-arrow selection in the results survives Shift repeats
	m = editModel("select * from t", []string{"ID"}, [][]string{{"1"}, {"2"}, {"3"}, {"4"}})
	press(m, shift)
	press(m, key(tea.KeyDown, tea.ModShift))
	press(m, shift)
	press(m, key(tea.KeyDown, tea.ModShift))
	press(m, shift)
	if r1, r2, _, _, ok := m.res.blockRange(); !ok || r1 != 0 || r2 != 2 {
		t.Fatalf("selection: %d-%d %v", r1, r2, ok)
	}
}

func TestClipboardToolPanicIsCaught(t *testing.T) {
	old := systemCopy
	systemCopy = func(string) { panic("clipboard tool failed") }
	t.Cleanup(func() { systemCopy = old })
	m := editModel("select * from t", []string{"ID"}, [][]string{{"1"}})
	m.res.res.Rows[0][0] = "x"
	press(m, key('c')) // drain runs the commands: must not panic
	if !strings.Contains(m.status, "Copied") {
		t.Fatalf("status %q", m.status)
	}
}
