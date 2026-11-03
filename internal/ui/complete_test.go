package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/complete"
	"sqlcli/internal/db"
)

// completionModel has a fake connection "ora" with a loaded catalog.
func completionModel() *Model {
	m := mouseModel()
	m.conn = &db.Conn{Name: "ora"}
	cat := newCatalog()
	cat.setObjects([]db.ObjectInfo{{Name: "EMPLOYEES", Type: "TABLE"}, {Name: "DUAL", Type: "PUBLIC SYNONYM"}})
	cat.cols[complete.TableRef{Name: "EMPLOYEES"}] = []string{"EMP_ID", "NAME", "SALARY"}
	m.catalogs = map[string]*catalog{"ora": cat}
	m.focus = focusEditor
	return m
}

func popupTexts(m *Model) []string {
	var out []string
	if !m.ed.PopupOpen() {
		return nil
	}
	for {
		it, _ := m.ed.PopupSelected()
		out = append(out, it.Text)
		before := it
		m.ed.PopupMove(1)
		if it2, _ := m.ed.PopupSelected(); it2 == before {
			break
		}
	}
	m.ed.PopupMove(-1000)
	return out
}

func TestCompletionWhileTyping(t *testing.T) {
	m := completionModel()
	typeText(m, "s")
	if m.ed.PopupOpen() {
		t.Fatal("one letter should not open the list")
	}
	typeText(m, "el")
	got := popupTexts(m)
	if len(got) == 0 || got[0] != "SELECT" {
		t.Fatalf("list: %v", got)
	}
	if !strings.Contains(screen(m), "SELECT") || !strings.Contains(screen(m), "keyword") {
		t.Fatalf("popup not drawn:\n%s", screen(m))
	}
	press(m, key(tea.KeyTab))
	if m.ed.Text() != "select" || m.ed.PopupOpen() {
		t.Fatalf("Tab: %q open=%v", m.ed.Text(), m.ed.PopupOpen())
	}

	typeText(m, " * from emp")
	if got := popupTexts(m); len(got) == 0 || got[0] != "EMPLOYEES" {
		t.Fatalf("tables: %v", got)
	}
	press(m, key(tea.KeyEnter))
	if m.ed.Text() != "select * from employees" {
		t.Fatalf("Enter: %q", m.ed.Text())
	}

	// alias. lists the table's columns; a lower-case prefix inserts lower case
	typeText(m, " e where e.")
	if got := popupTexts(m); strings.Join(got, ",") != "EMP_ID,NAME,SALARY" {
		t.Fatalf("e.: %v", got)
	}
	typeText(m, "sa")
	press(m, key(tea.KeyTab))
	if !strings.HasSuffix(m.ed.Text(), "where e.salary") {
		t.Fatalf("column: %q", m.ed.Text())
	}

	// a complete word: Enter starts a new line instead of completing
	m.ed.SetText("")
	typeText(m, "from")
	press(m, key(tea.KeyEnter))
	if m.ed.Text() != "from\n" {
		t.Fatalf("Enter after a complete word: %q", m.ed.Text())
	}

	// Esc closes; a space closes
	typeText(m, "sel")
	press(m, key(tea.KeyEscape))
	if m.ed.PopupOpen() {
		t.Fatal("Esc did not close the list")
	}
	typeText(m, "e")
	typeText(m, " ")
	if m.ed.PopupOpen() {
		t.Fatal("space should close the list")
	}
}

func TestCompletionCtrlSpace(t *testing.T) {
	m := completionModel()
	m.ed.SetText("select * from employees where ")
	m.ed.HandleKey("end", "")
	press(m, key(tea.KeySpace, tea.ModCtrl))
	got := popupTexts(m)
	if len(got) < 3 || got[0] != "EMP_ID" { // columns of the statement's table come first
		t.Fatalf("Ctrl-Space: %v", got[:min(5, len(got))])
	}
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyTab))
	if !strings.HasSuffix(m.ed.Text(), "where NAME") {
		t.Fatalf("after Down+Tab: %q", m.ed.Text())
	}
}

func TestCompletionDisabled(t *testing.T) {
	m := completionModel()
	m.cfg.Editor.Completion = false
	typeText(m, "sel")
	press(m, key(tea.KeySpace, tea.ModCtrl))
	if m.ed.PopupOpen() {
		t.Fatal("completion = false should never open the list")
	}
	press(m, key(tea.KeyTab))
	if m.ed.Text() != "sel " {
		t.Fatalf("Tab should indent when completion is off: %q", m.ed.Text())
	}
}

func TestDDLReloadsCatalog(t *testing.T) {
	m := completionModel()
	old := m.catalogs["ora"]
	if !changesSchema([]string{"create table t (x number)"}) || changesSchema([]string{"select 1 from dual"}) {
		t.Fatal("changesSchema")
	}
	m.running = true
	_, cmd := m.Update(execDoneMsg{tab: m.id, ddl: true, last: &db.Result{Message: "CREATE executed."}, messages: []string{"x"}})
	if m.catalogs["ora"] == old || !m.catalogs["ora"].loading || cmd == nil {
		t.Fatal("DDL should start a catalog reload")
	}
}
