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
	m.openTab()
	m.ed.SetText("-- third")
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
	if m.tabs[0].ed.Text() != "select 1 from dual;\nselect 2 from dual" || m.tabs[0].ed.CursorRow() != 1 || m.tabs[0].ed.CursorCol() != 7 {
		t.Fatalf("tab 1: %q at %d,%d", m.tabs[0].ed.Text(), m.tabs[0].ed.CursorRow(), m.tabs[0].ed.CursorCol())
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
