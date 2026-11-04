package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
	"sqlcli/internal/version"
)

func screen(m *Model) string { return ansi.Strip(m.View().Content) }

func TestConnectionsPanelToggle(t *testing.T) {
	cfg := config.Default()
	cfg.Connections = []config.Connection{{Name: "ora"}, {Name: "rep", ReadOnly: true}}
	m := New(cfg, nil, "ora")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	if !m.showConns || !strings.Contains(screen(m), "Connections") {
		t.Fatal("panel should be visible by default")
	}
	edW := m.res.w

	m.Update(key(tea.KeyF2))
	s := screen(m)
	if m.showConns || strings.Contains(s, "Connections") {
		t.Fatal("F2 did not hide the panel")
	}
	if m.focus != focusEditor {
		t.Fatalf("focus = %v with the panel hidden", m.focus)
	}
	if m.res.w != 98 || m.res.w <= edW {
		t.Fatalf("panes did not widen: %d (was %d)", m.res.w, edW)
	}
	for _, l := range strings.Split(s, "\n") {
		if ansi.StringWidth(l) > 100 {
			t.Fatalf("line wider than screen: %q", l)
		}
	}

	// F2 shows the panel again and focuses it
	m.Update(key(tea.KeyF2))
	if !m.showConns || m.focus != focusConns || !strings.Contains(screen(m), "Connections") {
		t.Fatal("F2 did not show and focus the panel")
	}

	// hiding while the panel has focus moves focus to the editor
	m.Update(key(tea.KeyF2))
	if m.showConns || m.focus != focusEditor {
		t.Fatalf("hide: show=%v focus=%v", m.showConns, m.focus)
	}

	// F4 no longer does anything
	m.Update(key(tea.KeyF4))
	if m.showConns {
		t.Fatal("F4 still toggles the panel")
	}
}

func TestSQLTitle(t *testing.T) {
	cfg := config.Default()
	cfg.Connections = []config.Connection{{Name: "ora"}}
	m := New(cfg, nil, "ora")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(screen(m), "SQL · not connected") {
		t.Fatal("missing 'not connected' title")
	}
	m.connecting, m.connectingTo = true, "ora"
	if !strings.Contains(screen(m), "SQL · connecting to ora…") {
		t.Fatal("missing connecting title")
	}
	m.connecting = false
	m.conn = &db.Conn{Name: "ora"}
	if !strings.Contains(screen(m), "╭─ SQL · ora ─") {
		t.Fatalf("missing connection name in SQL title:\n%s", screen(m))
	}
	m.conn = &db.Conn{Name: "rep", ReadOnly: true}
	if !strings.Contains(screen(m), "SQL · rep (read-only)") {
		t.Fatal("missing read-only marker")
	}
}

func TestPanelHiddenAtStartFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.UI.ShowConnections = false
	cfg.Connections = []config.Connection{{Name: "ora"}}
	if m := New(cfg, nil, "ora"); m.showConns {
		t.Fatal("show_connections = false ignored")
	}
	if m := New(cfg, nil, ""); !m.showConns { // no -c: user must pick one
		t.Fatal("panel must be shown when no connection is given")
	}
}

func TestVersionInKeyHints(t *testing.T) {
	for _, w := range []int{140, 100, 60} {
		cfg := config.Default()
		cfg.Connections = []config.Connection{{Name: "ora"}}
		m := New(cfg, nil, "ora")
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		lines := strings.Split(screen(m), "\n")
		last := lines[len(lines)-1]
		if !strings.HasSuffix(last, "sqlcli "+version.Version+" ") || !strings.HasPrefix(last, " F1 help") {
			t.Fatalf("width %d: key line %q", w, last)
		}
		if ansi.StringWidth(last) != w {
			t.Fatalf("width %d: key line is %d wide", w, ansi.StringWidth(last))
		}
	}
}

func TestKeyHintOrder(t *testing.T) {
	cfg := config.Default()
	m := New(cfg, nil, "x")
	m.Update(tea.WindowSizeMsg{Width: 250, Height: 30})
	lines := strings.Split(screen(m), "\n")
	if !strings.Contains(lines[len(lines)-1], "F5 run  F6 history  F7 run all  F8 rollback  F9 commit  F10 sessions") {
		t.Fatalf("key hints: %q", lines[len(lines)-1])
	}
}
