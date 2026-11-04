package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSaveSQLToFile(t *testing.T) {
	dir := t.TempDir()
	m := mouseModel()
	m.focus = focusEditor
	m.ed.SetText("select 1 from dual;\nselect 2 from dual")

	ctrlK(m, 'd')
	if m.mode != modePrompt || string(m.prompt.input) != "query.sql" {
		t.Fatalf("prompt: %q", string(m.prompt.input))
	}
	p := filepath.Join(dir, "report.sql")
	answer(m, p)
	b, _ := os.ReadFile(p)
	if string(b) != "select 1 from dual;\nselect 2 from dual\n" || m.status != "Saved 2 line(s) to "+p {
		t.Fatalf("file %q status %q", b, m.status)
	}
	if !strings.Contains(screen(m), "SQL · not connected · report.sql") {
		t.Fatalf("title should show the file:\n%s", screen(m))
	}

	// saving again offers the same file and doesn't ask before overwriting it
	m.ed.SetText("select 3 from dual")
	m.focus = focusResults // ^K D works from other panes too
	ctrlK(m, 'd')
	if string(m.prompt.input) != p {
		t.Fatalf("second prompt: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEnter))
	if b, _ := os.ReadFile(p); string(b) != "select 3 from dual\n" || m.mode != modeNormal {
		t.Fatalf("resave: %q mode %v", b, m.mode)
	}

	// another existing file: ask; "n" leaves it alone
	other := filepath.Join(dir, "other.sql")
	os.WriteFile(other, []byte("keep me\n"), 0o644)
	ctrlK(m, 'd')
	answer(m, other)
	if !strings.Contains(m.prompt.label, "Overwrite?") {
		t.Fatalf("no overwrite question: %q", m.prompt.label)
	}
	answer(m, "n")
	if b, _ := os.ReadFile(other); string(b) != "keep me\n" || m.status != "Not saved" {
		t.Fatalf("declined overwrite: %q %q", b, m.status)
	}

	// each tab remembers its own file
	m.openTab()
	ctrlK(m, 'd')
	if string(m.prompt.input) != "query.sql" {
		t.Fatalf("new tab should start with query.sql, got %q", string(m.prompt.input))
	}
}
