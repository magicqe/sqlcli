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

func TestLoadSQLFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "daily.sql")
	os.WriteFile(p, []byte("-- daily\nselect sysdate from dual;\n"), 0o644)
	m := mouseModel()
	m.focus = focusResults // ^K I works from other panes too
	m.ed.SetText("old text")
	m.ed.SetCursor(0, 5)

	ctrlK(m, 'i')
	if m.mode != modePrompt || !strings.HasPrefix(m.prompt.label, "Load file") {
		t.Fatalf("prompt: %q", m.prompt.label)
	}
	answer(m, p)
	if m.ed.Text() != "-- daily\nselect sysdate from dual;" || m.ed.CursorRow() != 0 || m.ed.CursorCol() != 0 {
		t.Fatalf("loaded %q at %d,%d", m.ed.Text(), m.ed.CursorRow(), m.ed.CursorCol())
	}
	if m.focus != focusEditor || m.status != "Loaded 2 line(s) from "+p+" (^_ undoes)" {
		t.Fatalf("focus %v status %q", m.focus, m.status)
	}
	if !strings.Contains(screen(m), "· daily.sql") {
		t.Fatal("title should show the loaded file")
	}

	// ^K D now offers the loaded file and saves back to it without asking
	m.ed.SetText("select 1 from dual")
	ctrlK(m, 'd')
	if string(m.prompt.input) != p {
		t.Fatalf("save prompt: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEnter))
	if b, _ := os.ReadFile(p); string(b) != "select 1 from dual\n" {
		t.Fatalf("saved back: %q", b)
	}

	// undo brings back the text from before the load
	m.ed.SetText("x")
	ctrlK(m, 'i')
	answer(m, p)
	press(m, key('_', tea.ModCtrl))
	if m.ed.Text() != "x" {
		t.Fatalf("undo after load: %q", m.ed.Text())
	}

	// refused: missing file, directory, binary file; the text stays
	bin := filepath.Join(dir, "x.bin")
	os.WriteFile(bin, []byte{0x7f, 'E', 'L', 'F', 0, 1}, 0o644)
	for path, want := range map[string]string{
		filepath.Join(dir, "nope.sql"): "no such file",
		dir:                            "it is a directory",
		bin:                            "not a text file",
	} {
		m.ed.SetText("keep")
		ctrlK(m, 'i')
		answer(m, path)
		if !m.statusErr || !strings.Contains(m.status, want) || m.ed.Text() != "keep" {
			t.Fatalf("%s: status %q text %q", path, m.status, m.ed.Text())
		}
	}
}
