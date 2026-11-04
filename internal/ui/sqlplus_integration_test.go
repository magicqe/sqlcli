package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestUISQLPlusCommands(t *testing.T) {
	m := newTestModel(t, false)
	defer m.conn.Execute(context.Background(), "drop procedure sqlcli_test_bad", 1)
	dir := t.TempDir()
	spoolFile := filepath.Join(dir, "report.lst")
	os.WriteFile(filepath.Join(dir, "main.sql"), []byte(`set linesize 200
column dummy format a10
define greeting = 'Hello'
prompt &greeting from &1
spool `+spoolFile+`
select '&1' as who, 1 + 1 as two from dual;
@@part
set serveroutput off
begin dbms_output.put_line('not shown'); end;
/
set serveroutput on
create or replace procedure sqlcli_test_bad is
begin
  oops;
end;
/
show errors
spool off
`), 0o644)
	os.WriteFile(filepath.Join(dir, "part.sql"), []byte("prompt in part\nexec dbms_output.put_line('from part')\n"), 0o644)

	m.ed.SetText("@" + filepath.Join(dir, "main") + " world")
	m.focus = focusEditor
	press(m, key(tea.KeyF5))
	all := strings.Join(m.res.textLines(), "\n") // the whole results text, not only what fits on screen
	for _, want := range []string{"Hello from world", "in part", "from part", "PLS-00201", "Spooling to " + spoolFile, "Spool file closed", "SET LINESIZE has no effect"} {
		if !strings.Contains(all, want) {
			t.Errorf("results lack %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "not shown") {
		t.Error("output while SERVEROUTPUT was off")
	}
	b, err := os.ReadFile(spoolFile)
	if err != nil {
		t.Fatal(err)
	}
	spool := string(b)
	t.Logf("spool file:\n%s", spool)
	for _, want := range []string{"SQL> select 'world' as who, 1 + 1 as two from dual;", "WHO   TWO", "----- ---", "world 2", "1 row(s) selected.", "in part", "from part", "PLS-00201"} {
		if !strings.Contains(spool, want) {
			t.Errorf("spool lacks %q", want)
		}
	}
	if m.env.Defines["GREETING"] != "Hello" {
		t.Fatalf("defines after the script: %v", m.env.Defines)
	}

	// an undefined variable in the panel: sqlcli asks, && keeps the value
	m.ed.SetText("select '&&city' as c from dual")
	press(m, key(tea.KeyF5))
	if m.mode != modePrompt || m.prompt.label != "Enter value for city:" {
		t.Fatalf("prompt: mode %v label %q", m.mode, m.prompt.label)
	}
	answer(m, "Bratislava")
	if !m.res.hasGrid() || m.res.res.Rows[0][0] != "Bratislava" {
		t.Fatalf("substituted query: %+v %q", m.res.res, m.status)
	}
	press(m, key(tea.KeyF5)) // && was kept: no question this time
	if m.mode != modeNormal || m.res.res.Rows[0][0] != "Bratislava" {
		t.Fatal("&& value not kept")
	}
	// DEFINE alone lists the variables
	m.ed.SetText("define")
	press(m, key(tea.KeyF5))
	if !m.res.hasGrid() || m.res.res.Columns[0] != "NAME" || !strings.Contains(m.res.view(true), "BRATISLAVA") && !strings.Contains(m.res.view(true), "Bratislava") {
		t.Fatalf("define listing:\n%s", m.res.view(true))
	}
	press(m, key(tea.KeyF8))
}
