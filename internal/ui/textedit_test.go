package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func clobModel() *Model {
	return editModel("select rowid, t.* from docs t", []string{"ROWID", "ID", "DOC"},
		[][]string{{"AAA", "1", "first line\nsecond line"}})
}

func clobReady(m *Model, typ string) {
	_, cmd := m.Update(editReadyMsg{tab: m.id, row: 0, col: 2, table: "DOCS", colTy: typ, rowid: "AAA", current: m.res.res.Rows[0][2]})
	drain(m, cmd)
}

func TestClobOpensTextEditor(t *testing.T) {
	m := clobModel()
	clobReady(m, "VARCHAR2")
	if m.mode != modePrompt {
		t.Fatalf("VARCHAR2 uses the prompt, mode %v", m.mode)
	}
	press(m, key(tea.KeyEscape))

	clobReady(m, "CLOB")
	if m.mode != modeText || m.text.ed.Text() != "first line\nsecond line" {
		t.Fatalf("CLOB opens the text editor: mode %v", m.mode)
	}
	view := m.View().Content
	if !strings.Contains(view, "DOCS.DOC (clob)") || !strings.Contains(view, "second line") || !strings.Contains(view, "^K X") {
		t.Fatalf("text editor view:\n%s", view)
	}
	if n := len(strings.Split(view, "\n")); n != 30 {
		t.Fatalf("view has %d lines, want 30", n)
	}

	// edits; the first Esc warns, the second discards
	typeText(m, "new ")
	press(m, key(tea.KeyEnter))
	press(m, key(tea.KeyEscape))
	if m.mode != modeText || !strings.Contains(m.View().Content, "Esc again discards") {
		t.Fatal("first Esc with changes should warn")
	}
	press(m, key(tea.KeyEscape))
	if m.mode != modeNormal || m.text != nil || m.res.res.Rows[0][2] != "first line\nsecond line" {
		t.Fatalf("second Esc discards: mode %v", m.mode)
	}

	// ^K X saves: the update runs (the test connection is not open, so it fails)
	clobReady(m, "CLOB")
	typeText(m, "x")
	ctrlK(m, 'x')
	if m.mode != modeNormal || m.text != nil {
		t.Fatalf("^K X closes the editor: mode %v", m.mode)
	}
	if !m.statusErr {
		t.Fatalf("the update should have been attempted: %q", m.status)
	}

	// unchanged text: Ctrl-S saves nothing
	clobReady(m, "CLOB")
	press(m, key('s', tea.ModCtrl))
	if m.mode != modeNormal || m.status != "Not changed" {
		t.Fatalf("unchanged: mode %v status %q", m.mode, m.status)
	}
}

func TestClobEditFromRecordView(t *testing.T) {
	m := clobModel()
	m.mode = modeRecord
	clobReady(m, "NCLOB")
	if m.mode != modeText {
		t.Fatalf("mode %v", m.mode)
	}
	press(m, key(tea.KeyEscape)) // unchanged: one Esc
	if m.mode != modeRecord {
		t.Fatalf("back to the record view, mode %v", m.mode)
	}
}

func TestTextEditorPaste(t *testing.T) {
	m := clobModel()
	clobReady(m, "CLOB")
	m.Update(tea.PasteMsg{Content: "pasted\ntext "})
	if !strings.HasPrefix(m.text.ed.Text(), "pasted\ntext first line") {
		t.Fatalf("paste: %q", m.text.ed.Text())
	}
}

func TestTextEditorFiles(t *testing.T) {
	dir := t.TempDir()
	m := clobModel()
	clobReady(m, "CLOB")

	// ^K D offers table_column.txt and saves the text as it is
	ctrlK(m, 'd')
	if m.mode != modePrompt || string(m.prompt.input) != "docs_doc.txt" {
		t.Fatalf("save question: mode %v input %q", m.mode, string(m.prompt.input))
	}
	if !strings.Contains(screen(m), "DOCS.DOC") {
		t.Fatal("the text panel should stay on screen during the question")
	}
	saved := dir + "/doc.txt"
	answer(m, saved)
	if m.mode != modeText {
		t.Fatalf("back in the text panel: mode %v", m.mode)
	}
	if b, err := os.ReadFile(saved); err != nil || string(b) != "first line\nsecond line" {
		t.Fatalf("saved file: %q %v", b, err)
	}
	if !strings.Contains(screen(m), "Saved 22 character(s)") {
		t.Fatalf("status in the panel:\n%s", screen(m))
	}

	// ^K I replaces the text with a file (offering the last file)
	other := dir + "/other.txt"
	os.WriteFile(other, []byte("from a file\nline 2\n"), 0o644)
	ctrlK(m, 'i')
	if string(m.prompt.input) != saved {
		t.Fatalf("load offers the last file: %q", string(m.prompt.input))
	}
	answer(m, other)
	if m.mode != modeText || m.text.ed.Text() != "from a file\nline 2\n" {
		t.Fatalf("loaded: %q", m.text.ed.Text())
	}

	// ^K R inserts a file at the cursor
	m.text.ed.SetCursor(0, 0)
	ctrlK(m, 'r')
	answer(m, saved)
	if !strings.HasPrefix(m.text.ed.Text(), "first line\nsecond linefrom a file") {
		t.Fatalf("inserted: %q", m.text.ed.Text())
	}

	// saving over another existing file asks first
	ctrlK(m, 'd')
	answer(m, saved)
	if m.mode != modePrompt || !strings.Contains(m.prompt.label, "Overwrite") {
		t.Fatalf("overwrite question: %q", m.prompt.label)
	}
	answer(m, "n")
	if b, _ := os.ReadFile(saved); string(b) != "first line\nsecond line" || m.mode != modeText {
		t.Fatalf("not overwritten: %q, mode %v", b, m.mode)
	}

	// a missing file is reported, the text stays
	before := m.text.ed.Text()
	ctrlK(m, 'i')
	answer(m, dir+"/missing.txt")
	if m.text.ed.Text() != before || !strings.Contains(screen(m), "no such file") {
		t.Fatalf("missing file:\n%s", screen(m))
	}
}
