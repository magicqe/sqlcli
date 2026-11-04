package ui

import (
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
