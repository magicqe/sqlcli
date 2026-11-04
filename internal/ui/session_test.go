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
	// with no current session, an empty name cancels
	m.sessionName = ""
	press(m, key('s', tea.ModCtrl))
	answer(m, "")
	if m.mode != modeNormal {
		t.Fatalf("empty name: mode %v", m.mode)
	}
	m.sessionName = "quick"

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
	if hints := lines[len(lines)-1]; !strings.Contains(hints, "F9 commit  F10") {
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
	// started with -s: the connections panel and object tree stay as configured
	if !n.showConns || !strings.Contains(screen(n), "Connections") || !strings.Contains(screen(n), "Objects") {
		t.Fatal("connections panel should be shown after -s")
	}

	// loading from inside sqlcli (F10) leaves the panel as it is
	n.Update(key(tea.KeyF10))
	n.Update(key(tea.KeyEnter))
	if !n.showConns {
		t.Fatal("F10 load should not hide the connections panel")
	}
}

func TestF5InCommentRunsNothing(t *testing.T) {
	m := mouseModel()
	m.conn = nil // would say "Not connected" if it tried to run
	m.focus = focusEditor
	m.ed.SetText("-- count rows\nselect count(*) from t;\n/* old\nselect 1 from dual;\n*/")
	for _, pos := range [][2]int{{0, 0}, {0, 5}, {2, 2}, {3, 4}, {4, 1}} {
		m.ed.SetCursor(pos[0], pos[1])
		press(m, key(tea.KeyF5))
		if m.status != "The cursor is in a comment: nothing to run" || m.running {
			t.Fatalf("cursor %v: status %q", pos, m.status)
		}
	}
	// on the statement itself it tries to run (and reports the missing connection)
	m.ed.SetCursor(1, 3)
	press(m, key(tea.KeyF5))
	if !strings.HasPrefix(m.status, "Not connected") {
		t.Fatalf("on the statement: %q", m.status)
	}
	// in a "--" comment at the end of a code line it runs the code before it
	m.ed.SetText("-- count rows\nselect count(*) from t; -- all of them\n/* old\nselect 1 from dual;\n*/")
	m.ed.SetCursor(1, 30)
	m.status = ""
	press(m, key(tea.KeyF5))
	if !strings.HasPrefix(m.status, "Not connected") {
		t.Fatalf("trailing comment: %q", m.status)
	}
	if st := m.currentStatements(); len(st) != 1 || st[0] != "select count(*) from t;" {
		t.Fatalf("trailing comment runs %q", st)
	}
	// a marked block runs whatever the cursor is on
	m.ed.SetCursor(1, 0)
	m.ed.HandleKey("ctrl+k", "")
	m.ed.HandleKey("b", "b")
	m.ed.SetCursor(2, 0)
	m.ed.HandleKey("ctrl+k", "")
	m.ed.HandleKey("k", "k")
	m.ed.SetCursor(3, 2) // inside the comment
	press(m, key(tea.KeyF5))
	if !strings.HasPrefix(m.status, "Not connected") {
		t.Fatalf("block: %q", m.status)
	}
}

// topLine returns the first screen line.
func topLine(m *Model) string { return strings.Split(screen(m), "\n")[0] }

func TestSessionNameTopLeftAndQuickSave(t *testing.T) {
	m := sessionModel(t)
	m.ed.SetText("select 1 from dual")
	if strings.Contains(topLine(m), "work") || m.tabBarH() != 0 {
		t.Fatalf("no session yet: top line %q", topLine(m))
	}

	press(m, key('s', tea.ModCtrl))
	answer(m, "work")
	if !strings.HasPrefix(topLine(m), " work ") {
		t.Fatalf("session name not top-left after save: %q", topLine(m))
	}
	for _, l := range strings.Split(screen(m), "\n") {
		if len([]rune(l)) > 120 {
			t.Fatalf("line too wide: %q", l)
		}
	}
	if lines := strings.Split(screen(m), "\n"); len(lines) != 30 {
		t.Fatalf("screen has %d lines, want 30", len(lines))
	}

	// Ctrl-S on a current session saves at once: no name, no overwrite question
	m.ed.SetText("select 2 from dual")
	press(m, key('s', tea.ModCtrl))
	if m.mode != modeNormal || m.status != `Session "work" saved: 1 tab(s)` {
		t.Fatalf("quick save: mode=%v status=%q", m.mode, m.status)
	}
	se, _ := m.hist.LoadSession("work")
	if se.Tabs[0].SQL != "select 2 from dual" {
		t.Fatalf("saved SQL: %q", se.Tabs[0].SQL)
	}

	// with several tabs the session name comes first, then the tabs; clicks still hit the tabs
	m.openTab()
	top := topLine(m)
	if !strings.HasPrefix(top, " work │ 1 not connected ") || !strings.Contains(top, "2 not connected") {
		t.Fatalf("top line with tabs: %q", top)
	}
	x, y := find(t, m, " 1 not connected ")
	click(m, x+2, y)
	if m.tabIndex(m.tab) != 0 {
		t.Fatal("click on tab 1 with the session name in front")
	}
	click(m, 2, 0) // on the session name: nothing happens
	if m.tabIndex(m.tab) != 0 {
		t.Fatal("click on the session name changed the tab")
	}

	// "s" in the session window still asks (save under another name)
	press(m, key(tea.KeyF10))
	press(m, key('s'))
	if m.mode != modePrompt || string(m.prompt.input) != "work" {
		t.Fatalf("F10 s should ask: mode=%v input=%q", m.mode, string(m.prompt.input))
	}
	answer(m, "work copy")
	if m.sessionName != "work copy" || !strings.HasPrefix(topLine(m), " work copy ") {
		t.Fatalf("save as: name %q top %q", m.sessionName, topLine(m))
	}

	// deleting the current session clears the name
	press(m, key(tea.KeyF10))
	if m.sessList[0].Name != "work copy" {
		t.Fatalf("list order: %v", m.sessList)
	}
	press(m, key('d'))
	answer(m, "y")
	press(m, key(tea.KeyEscape))
	if m.sessionName != "" || strings.Contains(topLine(m), "work copy") {
		t.Fatalf("after deleting the current session: name %q top %q", m.sessionName, topLine(m))
	}
}

func TestSessionNameAfterLoad(t *testing.T) {
	m := sessionModel(t)
	m.hist.SaveSession("ms1", 0, []history.SessionTab{{SQL: "a"}, {SQL: "b"}})
	press(m, key(tea.KeyF10))
	press(m, key(tea.KeyEnter))
	if !strings.HasPrefix(topLine(m), " ms1 │ 1 ") {
		t.Fatalf("after load: %q", topLine(m))
	}
	m.ed.SetText("changed")
	press(m, key('s', tea.ModCtrl))
	se, _ := m.hist.LoadSession("ms1")
	if m.mode != modeNormal || se.Tabs[0].SQL != "changed" {
		t.Fatalf("Ctrl-S after load: mode=%v saved=%q", m.mode, se.Tabs[0].SQL)
	}
}

func TestSaveSessionOnExit(t *testing.T) {
	quitWith := func(m *Model) tea.Cmd {
		_, cmd := m.Update(key('k', tea.ModCtrl))
		_, cmd = m.Update(key('x'))
		return cmd
	}

	// on (the default) with a current session: saved when quitting
	m := sessionModel(t)
	if !m.cfg.UI.SaveSessionOnExit {
		t.Fatal("save_session_on_exit should default to on")
	}
	m.ed.SetText("v1")
	if err := m.saveSession("daily"); err != nil {
		t.Fatal(err)
	}
	m.ed.SetText("v2 edited")
	m.openTab()
	if cmd := quitWith(m); cmd == nil || !m.quitting {
		t.Fatal("did not quit")
	}
	se, _ := m.hist.LoadSession("daily")
	if len(se.Tabs) != 2 || se.Tabs[0].SQL != "v2 edited" || m.ExitMessage() != `session "daily" saved` {
		t.Fatalf("on exit: %d tabs, %q, msg %q", len(se.Tabs), se.Tabs[0].SQL, m.ExitMessage())
	}

	// off: nothing saved
	m = sessionModel(t)
	m.cfg.UI.SaveSessionOnExit = false
	m.ed.SetText("v1")
	m.saveSession("daily")
	m.ed.SetText("changed")
	quitWith(m)
	if se, _ := m.hist.LoadSession("daily"); se.Tabs[0].SQL != "v1" || m.ExitMessage() != "" {
		t.Fatalf("off: saved %q msg %q", se.Tabs[0].SQL, m.ExitMessage())
	}

	// no current session: nothing saved, no session created
	m = sessionModel(t)
	m.ed.SetText("x")
	quitWith(m)
	if list, _ := m.hist.Sessions(); len(list) != 0 || m.ExitMessage() != "" {
		t.Fatalf("no session: %v %q", list, m.ExitMessage())
	}

	// a cancelled quit doesn't save
	m = sessionModel(t)
	m.ed.SetText("v1")
	m.saveSession("daily")
	m.ed.SetText("changed")
	m.running = true // makes quit ask first
	quitWith(m)
	answer(m, "n")
	if se, _ := m.hist.LoadSession("daily"); se.Tabs[0].SQL != "v1" || m.quitting {
		t.Fatalf("cancelled quit saved %q", se.Tabs[0].SQL)
	}
}
