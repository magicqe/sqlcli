package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func resultsText(m *Model) string { return m.res.view(true) }

func TestUIDBMSOutputShown(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "begin\n  dbms_output.put_line('Hello from PL/SQL');\n  dbms_output.put_line('second line');\nend;\n/")
	txt := resultsText(m)
	for _, want := range []string{"PL/SQL procedure successfully completed.", "DBMS_OUTPUT (2 line(s))", "Hello from PL/SQL", "second line"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("results pane lacks %q:\n%s", want, txt)
		}
	}
	if !strings.Contains(m.status, "2 line(s) of DBMS_OUTPUT") {
		t.Errorf("status: %q", m.status)
	}
}

func TestUIDBMSOutputScroll(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "begin for i in 1..200 loop dbms_output.put_line('out '||i); end loop; end;")
	if strings.Contains(resultsText(m), "out 200") {
		t.Fatal("last line visible without scrolling")
	}
	if !strings.Contains(m.res.summary(), "/202") { // message + header + 200 lines
		t.Errorf("summary: %q", m.res.summary())
	}
	m.focus = focusResults
	press(m, key(tea.KeyEnd))
	if txt := resultsText(m); !strings.Contains(txt, "out 200") {
		t.Fatalf("End did not scroll to the last line:\n%s", txt)
	}
	press(m, key(tea.KeyHome))
	if !strings.Contains(resultsText(m), "PL/SQL procedure successfully completed.") {
		t.Fatal("Home did not scroll to the top")
	}
}

func TestUIDBMSOutputOnError(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "begin dbms_output.put_line('step 1 done'); raise_application_error(-20001, 'step 2 failed'); end;")
	txt := resultsText(m)
	if !strings.Contains(txt, "ORA-20001: step 2 failed") || !strings.Contains(txt, "step 1 done") {
		t.Fatalf("error/output missing:\n%s", txt)
	}
	if !m.statusErr {
		t.Error("status should be an error")
	}
}

func TestUIQueryWithOutputToggle(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "with function f return number is begin dbms_output.put_line('called f'); return 42; end; select f() answer from dual;\n/")
	if !m.res.hasGrid() {
		t.Fatalf("WITH FUNCTION query failed: %s", m.res.err)
	}
	if m.res.showText() || !strings.Contains(m.res.summary(), "o: DBMS_OUTPUT") {
		t.Fatalf("grid should show first, summary %q", m.res.summary())
	}
	m.focus = focusResults
	press(m, key('o'))
	if !m.res.showText() || !strings.Contains(resultsText(m), "called f") {
		t.Fatalf("o did not show output:\n%s", resultsText(m))
	}
	press(m, key('o'))
	if m.res.showText() || !strings.Contains(resultsText(m), "42") {
		t.Fatal("o did not return to the grid")
	}
}

func TestUIScriptCollectsOutput(t *testing.T) {
	m := newTestModel(t, false)
	m.ed.SetText("begin dbms_output.put_line('A'); end;\n/\nbegin dbms_output.put_line('B'); end;\n/\n")
	press(m, key(tea.KeyF7))
	txt := resultsText(m)
	if !strings.Contains(txt, "DBMS_OUTPUT (2 line(s))") || !strings.Contains(txt, "[2] PL/SQL") {
		t.Fatalf("script output:\n%s", txt)
	}
	press(m, key(tea.KeyF9)) // roll back the transaction PL/SQL opened
}
