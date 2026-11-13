package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/history"
)

func historyModel(t *testing.T, entries ...string) *Model {
	t.Helper()
	h, err := history.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	for _, e := range entries {
		h.Add(history.Entry{Connection: "ora", SQL: e, ExecutedAt: time.Now()})
	}
	m := sessionModel(t)
	m.hist = h
	return m
}

func TestHistoryAddsAtEnd(t *testing.T) {
	m := historyModel(t, "select count(*) from emp")
	m.ed.SetText("select 1 from dual;\n\n  ")
	m.ed.SetCursor(0, 3) // cursor in the middle of the first statement
	press(m, key(tea.KeyF6))
	press(m, key(tea.KeyEnter))
	if got := m.ed.Text(); got != "select 1 from dual;\n\nselect count(*) from emp" {
		t.Fatalf("text: %q", got)
	}
	if m.ed.CursorRow() != 2 || m.ed.CursorCol() != 0 || m.mode != modeNormal || m.focus != focusEditor {
		t.Fatalf("cursor %d,%d mode %v", m.ed.CursorRow(), m.ed.CursorCol(), m.mode)
	}
	if !strings.Contains(m.status, "Added to the end") {
		t.Fatalf("status %q", m.status)
	}
	// one undo step removes it again
	m.ed.HandleKey("ctrl+_", "")
	if m.ed.Text() != "select 1 from dual;\n\n  " {
		t.Fatalf("undo: %q", m.ed.Text())
	}

	// into an empty panel: just the statement
	m.ed.SetText("")
	press(m, key(tea.KeyF6))
	press(m, key(tea.KeyEnter))
	if m.ed.Text() != "select count(*) from emp" || m.ed.CursorRow() != 0 {
		t.Fatalf("empty panel: %q", m.ed.Text())
	}
}

func TestHistoryTabInsertsAndCtrlRReplaces(t *testing.T) {
	m := historyModel(t, "x")
	m.ed.SetText("ab")
	m.ed.SetCursor(0, 1)
	press(m, key(tea.KeyF6))
	press(m, key(tea.KeyTab))
	if m.ed.Text() != "axb" {
		t.Fatalf("Tab: %q", m.ed.Text())
	}
	press(m, key(tea.KeyF6))
	press(m, key('r', tea.ModCtrl))
	if m.ed.Text() != "x" {
		t.Fatalf("Ctrl-R: %q", m.ed.Text())
	}
}

func TestHistoryShowsRunTime(t *testing.T) {
	m := historyModel(t)
	now := time.Now()
	m.hist.Add(history.Entry{Connection: "ora", SQL: "select 1 from dual", ExecutedAt: now, DurationMS: 42, Rows: 1})
	m.hist.Add(history.Entry{Connection: "ora", SQL: "select slow from big", ExecutedAt: now, DurationMS: 83500, Rows: 7})
	m.hist.Add(history.Entry{Connection: "rep", SQL: "select x from nope", ExecutedAt: now, DurationMS: 3200, Error: "ORA-00942: table or view does not exist"})
	press(m, key(tea.KeyF6))
	s := screen(m)
	row := func(conn, dur, flag, sql string) string { return fmt.Sprintf("%-12s %7s %s %s", conn, dur, flag, sql) }
	for _, want := range []string{row("rep", "3.2s", "✗", "select x from nope"), row("ora", "1m23s", " ", "select slow from big"),
		row("ora", "42ms", " ", "select 1 from dual"), "run time · SQL"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "run time 3.2s: ORA-00942") { // the newest (an error) is selected
		t.Fatalf("preview of an error:\n%s", s)
	}
	press(m, key(tea.KeyDown))
	if !strings.Contains(screen(m), "run time 1m23s (83500 ms), 7 row(s)") {
		t.Fatalf("preview:\n%s", screen(m))
	}
}
