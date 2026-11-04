package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/editor"
)

// The text editor panel edits a long text value (a CLOB cell, or a CLOB
// column of a new row) in a full-screen editor with the JOE keys, instead
// of the one-line prompt. ^K X (or Ctrl-S) saves, Esc cancels.

type textEdit struct {
	ed      *editor.Editor
	title   string
	orig    string
	back    mode // where saving or cancelling returns
	onSave  func(string) tea.Cmd
	discard bool // Esc was pressed once with unsaved changes
}

// isLongText reports whether a column type is edited in the text editor.
func isLongText(colType string) bool {
	t := strings.ToUpper(colType)
	return t == "CLOB" || t == "NCLOB"
}

// openTextEdit opens the text editor on text; onSave gets the edited text.
func (m *Model) openTextEdit(title, text string, onSave func(string) tea.Cmd) {
	back := m.mode
	if back == modePrompt {
		back = m.prompt.back
	}
	if back != modeRecord {
		back = modeNormal
	}
	ed := editor.New()
	ed.TabWidth = m.cfg.Editor.TabWidth
	ed.AutoIndent = m.cfg.Editor.AutoIndent
	ed.Focused = true
	ed.SetText(text)
	ed.SetCursor(0, 0)
	m.text = &textEdit{ed: ed, title: title, orig: text, back: back, onSave: onSave}
	m.mode = modeText
	m.layoutText()
}

func (m *Model) layoutText() {
	if m.text != nil {
		m.text.ed.SetSize(max(m.w-2, 1), max(m.h-3, 1)) // box borders, hint line
	}
}

func (m *Model) closeTextEdit(status string) {
	m.mode = m.text.back
	m.text = nil
	if status != "" {
		m.setStatus(status, false)
	}
}

func (m *Model) updateText(key, text string) tea.Cmd {
	te := m.text
	changed := te.ed.Text() != te.orig
	if !te.ed.PrefixPending() {
		switch key {
		case "ctrl+s":
			return m.saveText()
		case "esc", "ctrl+c":
			if changed && !te.discard {
				te.discard = true
				te.ed.Status = "Unsaved changes: Esc again discards them, ^K X saves"
				return nil
			}
			m.closeTextEdit("Cancelled")
			return nil
		}
	}
	te.discard = false
	switch te.ed.HandleKey(key, text) {
	case editor.ActionExit: // ^K X
		return m.saveText()
	case editor.ActionCloseWindow: // ^K Q
		m.closeTextEdit("Cancelled")
	case editor.ActionNone:
	default:
		te.ed.Status = "Not available here: ^K X saves, Esc cancels"
	}
	return nil
}

func (m *Model) saveText() tea.Cmd {
	te := m.text
	v := te.ed.Text()
	m.closeTextEdit("")
	if te.onSave != nil {
		return te.onSave(v)
	}
	return nil
}

func (m *Model) textView() string {
	te := m.text
	title := te.title
	if te.ed.Text() != te.orig {
		title += " [modified]"
	}
	body := box(title, te.ed.View(), m.w, m.h-1, true)
	hint := te.ed.Status
	if hint == "" {
		hint = "^K X / Ctrl-S save   Esc cancel   ^K B/K/C/M/Y block   ^Y delete line   empty text = NULL"
	}
	pos := fmt.Sprintf(" Ln %d, Col %d ", te.ed.CursorRow()+1, te.ed.CursorCol()+1)
	room := max(m.w-ansi.StringWidth(pos), 0)
	hint = ansi.Truncate(" "+hint, room, "…")
	line := hintText.Render(hint + strings.Repeat(" ", max(room-ansi.StringWidth(hint), 0)) + pos)
	return body + "\n" + line
}
