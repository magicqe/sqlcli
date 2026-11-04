package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFileNameCompletion(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"daily.sql", "data.sql", "other.txt", ".hidden.sql", "docs/inside.sql"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("select 1 from dual\n"), 0o644)
	}
	m := mouseModel()
	m.focus = focusEditor
	tab := func(in string) string {
		m.askFile("File:", in, func(string) tea.Cmd { return nil })
		press(m, key(tea.KeyTab))
		return string(m.prompt.input)
	}
	d := dir + "/"

	if got := tab(d + "o"); got != d+"other.txt" {
		t.Fatalf("single match: %q", got)
	}
	if got := tab(d + "do"); got != d+"docs/" {
		t.Fatalf("directory: %q", got)
	}
	press(m, key(tea.KeyTab)) // inside the directory
	if got := string(m.prompt.input); got != d+"docs/inside.sql" {
		t.Fatalf("into the directory: %q", got)
	}

	// several matches sharing more than is typed: complete the common part
	os.WriteFile(filepath.Join(dir, "report_jan.sql"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "report_feb.sql"), nil, 0o644)
	if got := tab(d + "rep"); got != d+"report_" || !strings.Contains(m.status, "report_feb.sql  report_jan.sql") {
		t.Fatalf("common prefix: %q status %q", got, m.status)
	}
	press(m, key(tea.KeyTab)) // then Tab cycles
	if got := string(m.prompt.input); got != d+"report_feb.sql" {
		t.Fatalf("cycle after common prefix: %q", got)
	}

	// typed part already common to all matches: Tab cycles through them at once
	if got := tab(d + "da"); got != d+"daily.sql" || !strings.Contains(m.status, "daily.sql  data.sql") {
		t.Fatalf("cycle 1: %q status %q", got, m.status)
	}
	press(m, key(tea.KeyTab))
	if got := string(m.prompt.input); got != d+"data.sql" {
		t.Fatalf("cycle 2: %q", got)
	}
	press(m, key(tea.KeyTab))
	if got := string(m.prompt.input); got != d+"daily.sql" {
		t.Fatalf("cycle wraps: %q", got)
	}
	// "d" matches daily.sql, data.sql and docs/: the common part is already typed
	if got := tab(d + "d"); got != d+"daily.sql" {
		t.Fatalf("no common progress starts cycling: %q", got)
	}

	// hidden files only with a leading dot; no match
	if got := tab(d + "."); got != d+".hidden.sql" {
		t.Fatalf("hidden: %q", got)
	}
	if got := tab(d + "zz"); got != d+"zz" || m.status != "No matching files" {
		t.Fatalf("no match: %q %q", got, m.status)
	}

	// typing ends the cycle; Enter submits the completed name
	m.askFile("File:", d+"da", func(string) tea.Cmd { return nil })
	press(m, key(tea.KeyTab)) // daily.sql
	typeText(m, "x")
	press(m, key(tea.KeyTab))
	if got := string(m.prompt.input); got != d+"daily.sqlx" {
		t.Fatalf("after typing: %q", got)
	}

	// ~/ expands for listing but stays as typed
	home, _ := os.UserHomeDir()
	if home != "" {
		os.WriteFile(filepath.Join(home, ".sqlcli_complete_test_tmp"), nil, 0o600)
		defer os.Remove(filepath.Join(home, ".sqlcli_complete_test_tmp"))
		if got := tab("~/.sqlcli_complete_test"); got != "~/.sqlcli_complete_test_tmp" {
			t.Fatalf("home: %q", got)
		}
	}
}

func TestLoadWithCompletion(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.sql"), []byte("select 42 from dual\n"), 0o644)
	m := mouseModel()
	m.focus = focusEditor
	ctrlK(m, 'i')
	press(m, key('y', tea.ModCtrl))
	typeText(m, dir+"/rep")
	press(m, key(tea.KeyTab))
	press(m, key(tea.KeyEnter))
	if m.ed.Text() != "select 42 from dual" {
		t.Fatalf("loaded %q (status %q)", m.ed.Text(), m.status)
	}
}
