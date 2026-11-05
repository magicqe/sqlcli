package ui

import (
	"fmt"
	"os"
	"path/filepath"
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
	discard bool   // Esc was pressed once with unsaved changes
	file    string // last file saved or loaded (^K D, ^K I), offered next time
}

// isLongText reports whether a column type is edited in the text editor.
func isLongText(colType string) bool {
	t := strings.ToUpper(colType)
	return t == "CLOB" || t == "NCLOB"
}

// openTextEdit opens the text editor on text; onSave gets the edited text.
// file is the name offered for ^K D / ^K I.
func (m *Model) openTextEdit(title, text, file string, onSave func(string) tea.Cmd) {
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
	m.text = &textEdit{ed: ed, title: title, orig: text, back: back, onSave: onSave, file: file}
	m.mode = modeText
	m.status = ""
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
	m.status = "" // the last message goes with the next key
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
	case editor.ActionSavePrompt: // ^K D
		m.askTextFile("Save text to file:", func(path string) { m.saveTextFile(path, te.ed.Text()) })
	case editor.ActionWriteBlock: // ^K W
		m.askTextFile("Write block to file:", func(path string) { m.saveTextFile(path, te.ed.BlockText()) })
	case editor.ActionLoadFile: // ^K I
		m.askTextFile("Load file (replaces the text):", func(path string) {
			if t, ok := m.readTextFileStatus(path); ok {
				te.ed.SetText(t) // ^_ undoes
				te.ed.SetCursor(0, 0)
				te.file = path
				m.setStatus(fmt.Sprintf("Loaded %d line(s) from %s (^_ undoes)", len(te.ed.Lines()), path), false)
			}
		})
	case editor.ActionInsertFile: // ^K R
		m.askTextFile("Insert file at the cursor:", func(path string) {
			if t, ok := m.readTextFileStatus(path); ok {
				te.ed.InsertBlock(t)
				m.setStatus("Inserted "+path, false)
			}
		})
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
		hint = m.status
	}
	if hint == "" {
		hint = "^K X / Ctrl-S save   Esc cancel   ^K D / ^K I save / load file   ^K R insert file   ^K B/K/C/M/Y block   empty text = NULL"
	}
	pos := fmt.Sprintf(" Ln %d, Col %d ", te.ed.CursorRow()+1, te.ed.CursorCol()+1)
	room := max(m.w-ansi.StringWidth(pos), 0)
	hint = ansi.Truncate(" "+hint, room, "…")
	line := hintText.Render(hint + strings.Repeat(" ", max(room-ansi.StringWidth(hint), 0)) + pos)
	if m.mode == modePrompt { // a question (file name ...) on the bottom line
		line = m.bottomLine()
	}
	return body + "\n" + line
}

// askTextFile asks for a file name (Tab completes) for the text panel,
// offering the panel's last file, and calls do with the absolute path.
func (m *Model) askTextFile(label string, do func(path string)) {
	m.askFile(label, m.text.file, func(v string) tea.Cmd {
		name := strings.TrimSpace(v)
		if name == "" {
			m.setStatus("Cancelled", false)
			return nil
		}
		path := expandHome(name)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		do(path)
		return nil
	})
}

// saveTextFile writes text to path, asking before replacing another file.
func (m *Model) saveTextFile(path, text string) {
	write := func() {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			m.setStatus("Saving: "+err.Error(), true)
			return
		}
		if m.text != nil {
			m.text.file = path
		}
		m.setStatus(fmt.Sprintf("Saved %d character(s) to %s", len([]rune(text)), path), false)
	}
	if _, err := os.Stat(path); err == nil && (m.text == nil || path != m.text.file) {
		m.ask(path+" exists. Overwrite? (y/N)", "", false, func(v string) tea.Cmd {
			if strings.EqualFold(strings.TrimSpace(v), "y") {
				write()
			} else {
				m.setStatus("Not saved", false)
			}
			return nil
		})
		return
	}
	write()
}

// readTextFileStatus reads a text file, reporting a failure in the status.
func (m *Model) readTextFileStatus(path string) (string, bool) {
	t, err := readTextFile(path)
	if err != nil {
		m.setStatus("Loading "+path+": "+err.Error(), true)
		return "", false
	}
	return t, true
}

// textFileName is the file name first offered for a column's text:
// table_column.txt in lower case.
func textFileName(table, column string) string {
	name := strings.ToLower(table + "_" + column)
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ' ' || r == '"' {
			return '_'
		}
		return r
	}, name)
	return name + ".txt"
}
