package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/history"
)

func sessionModel(t *testing.T) *Model {
	t.Helper()
	h, err := history.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	cfg := config.Default()
	m := New(cfg, h, "x")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.focus = focusEditor
	return m
}

func TestSaveAndLoadSession(t *testing.T) {
	m := sessionModel(t)
	m.ed.SetText("select 1 from dual;\nselect 2 from dual")
	m.ed.SetCursor(1, 7)
	m.openTab()
	m.ed.SetText("begin null; end;\n/")
	m.ed.SetCursor(1, 5)
	m.openTab()
	m.ed.SetText("-- third")
	m.ed.SetCursor(0, 8)
	m.switchTab(1) // the middle tab is active

	press(m, key(tea.KeyF10))
	if m.mode != modeSessions || !strings.Contains(screen(m), "No saved sessions") {
		t.Fatalf("empty session window:\n%s", screen(m))
	}
	press(m, key('s'))
	if m.mode != modePrompt || !strings.HasPrefix(m.prompt.label, "Save session as") {
		t.Fatalf("save prompt: %q", m.prompt.label)
	}
	answer(m, "work")
	if !strings.Contains(m.status, `Session "work" saved: 3 tab(s)`) {
		t.Fatalf("status: %q", m.status)
	}

	// change everything, then load the session back
	ctrlK(m, 'q')
	ctrlK(m, 'q')
	m.ed.SetText("something else")
	if len(m.tabs) != 1 {
		t.Fatalf("tabs: %d", len(m.tabs))
	}
	press(m, key(tea.KeyF10))
	s := screen(m)
	if !strings.Contains(s, "work") || !strings.Contains(s, " 3 tab(s)") {
		t.Fatalf("session list:\n%s", s)
	}
	press(m, key(tea.KeyEnter))
	if m.mode != modeNormal || len(m.tabs) != 3 || m.tabIndex(m.tab) != 1 {
		t.Fatalf("loaded: mode=%v tabs=%d active=%d", m.mode, len(m.tabs), m.tabIndex(m.tab))
	}
	if m.tabs[0].ed.Text() != "select 1 from dual;\nselect 2 from dual" {
		t.Fatalf("tab 1: %q", m.tabs[0].ed.Text())
	}
	// every tab starts at the top-left corner, whatever the saved cursor was
	for i, tb := range m.tabs {
		if tb.ed.CursorRow() != 0 || tb.ed.CursorCol() != 0 {
			t.Fatalf("tab %d cursor at %d,%d, want 0,0", i+1, tb.ed.CursorRow(), tb.ed.CursorCol())
		}
	}
	if m.tabs[1].ed.Text() != "begin null; end;\n/" || m.tabs[2].ed.Text() != "-- third" {
		t.Fatal("tab texts not restored")
	}

	// saving again offers the session's name and asks before overwriting
	press(m, key(tea.KeyF10))
	press(m, key('s'))
	if string(m.prompt.input) != "work" {
		t.Fatalf("default name: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEnter))
	if !strings.Contains(m.prompt.label, "Overwrite?") {
		t.Fatalf("no overwrite question: %q", m.prompt.label)
	}
	answer(m, "n")
	if m.status != "Session not saved" {
		t.Fatalf("status: %q", m.status)
	}

	// delete
	press(m, key(tea.KeyF10))
	press(m, key('d'))
	answer(m, "y")
	if m.mode != modeSessions || !strings.Contains(screen(m), "No saved sessions") {
		t.Fatalf("after delete:\n%s", screen(m))
	}
	press(m, key(tea.KeyEscape))
	if m.mode != modeNormal {
		t.Fatal("Esc should close the session window")
	}
}

func TestLoadSessionMissingConnection(t *testing.T) {
	m := sessionModel(t)
	if err := m.hist.SaveSession("old", 0, []history.SessionTab{{Connection: "gone", SQL: "select 1 from dual"}}); err != nil {
		t.Fatal(err)
	}
	press(m, key(tea.KeyF10))
	press(m, key(tea.KeyEnter))
	if len(m.tabs) != 1 || m.conn != nil || m.ed.Text() != "select 1 from dual" {
		t.Fatalf("loaded: tabs=%d conn=%v", len(m.tabs), m.conn)
	}
	if !m.statusErr || !strings.Contains(m.status, "not in the config: gone") {
		t.Fatalf("status: %q", m.status)
	}
}

func TestSessionShortcuts(t *testing.T) {
	m := sessionModel(t)
	m.ed.SetText("select 1 from dual")

	// Ctrl-S saves directly (asks for a name), from any pane
	for _, f := range []focus{focusEditor, focusResults} {
		m.focus = f
		press(m, key('s', tea.ModCtrl))
		if m.mode != modePrompt || !strings.HasPrefix(m.prompt.label, "Save session as") {
			t.Fatalf("Ctrl-S (focus %v): mode=%v label=%q", f, m.mode, m.prompt.label)
		}
		press(m, key(tea.KeyEscape))
	}
	press(m, key('s', tea.ModCtrl))
	answer(m, "quick")
	if m.mode != modeNormal || !strings.Contains(m.status, `Session "quick" saved`) {
		t.Fatalf("Ctrl-S save: mode=%v status=%q", m.mode, m.status)
	}
	// an empty name cancels and goes back to where Ctrl-S was pressed
	press(m, key('s', tea.ModCtrl))
	answer(m, "")
	if m.mode != modeNormal {
		t.Fatalf("empty name: mode %v", m.mode)
	}

	// ^K E opens the session list from the editor, results and connections pane
	for _, f := range []focus{focusEditor, focusResults, focusConns} {
		m.focus = f
		ctrlK(m, 'e')
		if m.mode != modeSessions || !strings.Contains(screen(m), "quick") {
			t.Fatalf("^K E (focus %v): mode=%v", f, m.mode)
		}
		press(m, key(tea.KeyEscape))
		if m.mode != modeNormal {
			t.Fatal("Esc should close the session list")
		}
	}

	// Ctrl-E is end of line again (and doesn't open the session list)
	m.focus = focusEditor
	m.ed.SetCursor(0, 0)
	press(m, key('e', tea.ModCtrl))
	if m.mode != modeNormal || m.ed.CursorCol() != len("select 1 from dual") {
		t.Fatalf("Ctrl-E: mode=%v col=%d", m.mode, m.ed.CursorCol())
	}

	// End still moves to the end of the line
	m.focus = focusEditor
	m.ed.SetCursor(0, 0)
	press(m, key(tea.KeyEnd))
	if m.ed.CursorCol() != len("select 1 from dual") {
		t.Fatalf("End: col %d", m.ed.CursorCol())
	}

	// F10 comes right after F9 in the key hints
	lines := strings.Split(screen(m), "\n")
	if hints := lines[len(lines)-1]; !strings.Contains(hints, "F9 rollback  F10") {
		t.Fatalf("key hints: %q", hints)
	}
}

func TestCtrlELastRow(t *testing.T) {
	m := recordModel(5) // results pane focused
	press(m, key('e', tea.ModCtrl))
	if m.mode != modeNormal || m.res.row != 4 {
		t.Fatalf("results Ctrl-E: mode=%v row=%d", m.mode, m.res.row)
	}
}

func TestCtrlEInRecordViewStillLastRecord(t *testing.T) {
	m := recordModel(5)
	press(m, key(tea.KeyEnter))
	press(m, key('e', tea.ModCtrl))
	if m.mode != modeRecord || m.res.row != 4 {
		t.Fatalf("record view Ctrl-E: mode=%v row=%d", m.mode, m.res.row)
	}
}

func TestStartWithSession(t *testing.T) {
	m := sessionModel(t)
	m.hist.SaveSession("s", 1, []history.SessionTab{{SQL: "select 1 from dual"}, {SQL: "select 2 from dual"}})
	n := New(m.cfg, m.hist, "")
	n.StartWithSession("s")
	n.Init()
	n.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if len(n.tabs) != 2 || n.tabIndex(n.tab) != 1 || n.ed.Text() != "select 2 from dual" || n.focus != focusEditor {
		t.Fatalf("start: tabs=%d active=%d text=%q focus=%v", len(n.tabs), n.tabIndex(n.tab), n.ed.Text(), n.focus)
	}
	if !strings.Contains(screen(n), "SQL 2") || !strings.Contains(n.status, `Session "s" loaded`) {
		t.Fatalf("screen / status: %q", n.status)
	}
}
