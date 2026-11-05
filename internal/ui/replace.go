package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/editor"
)

// Search and replace in the SQL editor, the JOE way: ^K F asks for the
// text and the options (r replace, c match case); Ctrl-R goes straight to
// replacing. Replacing asks at each match: y, n, a (all the rest), Esc.

// findEditor is the editor searched: the text panel's when it is open
// (a CLOB value), else the SQL editor.
func (m *Model) findEditor() *editor.Editor {
	if m.text != nil {
		return m.text.ed
	}
	return m.ed
}

// askFind asks for the search text, then the options (^K F) or the
// replacement (Ctrl-R, replace set).
func (m *Model) askFind(replace bool) {
	ed := m.findEditor()
	label := "Find:"
	if replace {
		label = "Replace:"
	}
	m.ask(label, ed.LastSearch(), false, func(find string) tea.Cmd {
		if find == "" {
			m.setStatus("Cancelled", false)
			return nil
		}
		if replace {
			m.askReplaceWith(find, false, false)
			return nil
		}
		m.ask("Options: r replace, c match case, a replace all without asking (Enter = find):", "", false, func(opts string) tea.Cmd {
			opts = strings.ToLower(opts)
			matchCase := strings.Contains(opts, "c")
			if strings.ContainsAny(opts, "ra") {
				m.askReplaceWith(find, matchCase, strings.Contains(opts, "a"))
				return nil
			}
			ed.FindCase(find, matchCase)
			m.setStatus(ed.Status, ed.Status != "")
			return nil
		})
		return nil
	})
}

func (m *Model) askReplaceWith(find string, matchCase, all bool) {
	ed := m.findEditor()
	m.ask(fmt.Sprintf("Replace %q with:", find), "", false, func(with string) tea.Cmd {
		r := ed.StartReplace(find, with, matchCase)
		if r == nil {
			m.setStatus(ed.Status, true)
			return nil
		}
		m.replace = r
		if all {
			ed.ReplaceRest(r)
			m.endReplace()
			return nil
		}
		m.mode = modeReplace
		m.setStatus("", false)
		return nil
	})
}

func (m *Model) updateReplace(key string) tea.Cmd {
	r, ed := m.replace, m.findEditor()
	more := true
	switch key {
	case "y", "Y", "space", "enter":
		more = ed.ReplaceStep(r, true)
	case "n", "N", "backspace", "delete":
		more = ed.ReplaceStep(r, false)
	case "a", "A", "r", "R", "!":
		ed.ReplaceRest(r)
		more = false
	case "esc", "q", "Q", "ctrl+c", "ctrl+g":
		more = false
	}
	if !more {
		m.endReplace()
	}
	return nil
}

func (m *Model) endReplace() {
	r := m.replace
	m.findEditor().EndReplace()
	m.replace = nil
	m.mode = modeNormal
	if m.text != nil {
		m.mode = modeText
	}
	switch r.Count {
	case 0:
		m.setStatus("Nothing replaced", false)
	case 1:
		m.setStatus("1 occurrence replaced (^_ undoes)", false)
	default:
		m.setStatus(fmt.Sprintf("%d occurrences replaced (^_ undoes all)", r.Count), false)
	}
}
