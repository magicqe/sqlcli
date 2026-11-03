package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/db"
)

func TestToggleResultsAndSQL(t *testing.T) {
	m := mouseModel()
	m.res.set(&db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"42"}}}, nil, nil, "")
	m.ed.SetText("select 42 n from dual")
	full := m.paneH()
	sqlH, resH := m.paneHeights()
	if sqlH == 0 || resH == 0 || sqlH+resH != full {
		t.Fatalf("both visible: %d + %d != %d", sqlH, resH, full)
	}

	// F3 hides the results; the SQL panel takes the whole height
	m.focus = focusResults
	press(m, key(tea.KeyF3))
	s := screen(m)
	if m.showResults || strings.Contains(s, "Results") || !strings.Contains(s, "SQL") || m.focus != focusEditor {
		t.Fatalf("F3 hide: show=%v focus=%v\n%s", m.showResults, m.focus, s)
	}
	if h, _ := m.paneHeights(); h != full || m.ed.View() == "" {
		t.Fatalf("SQL panel height %d, want %d", h, full)
	}
	// F3 shows them again, focused
	press(m, key(tea.KeyF3))
	if !m.showResults || m.focus != focusResults || !strings.Contains(screen(m), "Results") {
		t.Fatal("F3 show")
	}

	// F4 hides the SQL panel; results take the whole height and get focus
	m.focus = focusEditor
	press(m, key(tea.KeyF4))
	s = screen(m)
	if m.showSQL || strings.Contains(s, "SQL ·") || !strings.Contains(s, "│ 42") || m.focus != focusResults {
		t.Fatalf("F4 hide: show=%v focus=%v\n%s", m.showSQL, m.focus, s)
	}
	if _, h := m.paneHeights(); h != full {
		t.Fatalf("results height %d, want %d", h, full)
	}
	// never both hidden: hiding results now brings the SQL panel back
	press(m, key(tea.KeyF3))
	if m.showResults || !m.showSQL || m.focus != focusEditor {
		t.Fatalf("both hidden? results=%v sql=%v focus=%v", m.showResults, m.showSQL, m.focus)
	}
	press(m, key(tea.KeyF4)) // and hiding SQL brings the results back
	if m.showSQL || !m.showResults || m.focus != focusResults {
		t.Fatalf("swap: results=%v sql=%v", m.showResults, m.showSQL)
	}
	press(m, key(tea.KeyF4))
	if !m.showSQL || m.focus != focusEditor {
		t.Fatal("F4 show")
	}
	for _, l := range strings.Split(screen(m), "\n") {
		if len([]rune(l)) > 100 {
			t.Fatalf("line too wide: %q", l)
		}
	}
}

func TestEscShowsHiddenSQL(t *testing.T) {
	m := mouseModel()
	press(m, key(tea.KeyF4)) // results only
	press(m, key(tea.KeyEscape))
	if !m.showSQL || m.focus != focusEditor {
		t.Fatal("Esc from results should go back to a visible editor")
	}
}

func TestClicksWithHiddenPanels(t *testing.T) {
	m := mouseModel()
	m.res.set(&db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"10"}, {"20"}}}, nil, nil, "")
	press(m, key(tea.KeyF4)) // SQL hidden: the results start at the top
	x, y := find(t, m, "│ 20")
	m.focus = focusConns
	click(m, x+2, y)
	if m.focus != focusResults || m.res.row != 1 {
		t.Fatalf("click on results: focus=%v row=%d", m.focus, m.res.row)
	}
	press(m, key(tea.KeyF4))
	press(m, key(tea.KeyF3)) // results hidden: the editor fills the height
	m.ed.SetText("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\nq\nr\ns\nt")
	x, y = find(t, m, "20 t")
	click(m, x+3, y)
	if m.focus != focusEditor || m.ed.CursorRow() != 19 {
		t.Fatalf("click on line 20: focus=%v row=%d", m.focus, m.ed.CursorRow())
	}
}
