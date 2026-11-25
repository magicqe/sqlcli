package ui

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeText(m *Model, s string) {
	for _, r := range s {
		press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func answer(m *Model, s string) {
	press(m, key('y', tea.ModCtrl)) // clear the pre-filled input
	typeText(m, s)
	press(m, key(tea.KeyEnter))
}

func TestUIExport(t *testing.T) {
	m := newTestModel(t, false)
	dir := t.TempDir()
	run(m, `select level id, decode(mod(level, 10), 0, null, 'name '||level) name,
	               level / 4 amount, 'say "hi", ok' quote
	          from dual connect by level <= 2500`)
	if len(m.res.res.Rows) != 1000 || !m.res.res.More {
		t.Fatalf("expected a partial first batch, got %d", len(m.res.res.Rows))
	}
	m.focus = focusResults

	// JSON: all 2500 rows, although only 1000 were fetched
	jsonPath := filepath.Join(dir, "out.json")
	press(m, key('e'))
	if m.mode != modePrompt || string(m.prompt.input) != "result.csv" {
		t.Fatalf("prompt: mode=%v input=%q", m.mode, string(m.prompt.input))
	}
	answer(m, jsonPath)
	if !strings.HasPrefix(m.status, "Exported 2500 row(s) to ") {
		t.Fatalf("status: %q", m.status)
	}
	b, _ := os.ReadFile(jsonPath)
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 2500 || got[0]["ID"] != 1.0 || got[0]["AMOUNT"] != 0.25 || got[9]["NAME"] != nil || got[0]["QUOTE"] != `say "hi", ok` {
		t.Fatalf("JSON rows: n=%d first=%v tenth=%v", len(got), got[0], got[9])
	}

	// CSV, then export again over it (overwrite confirmation)
	csvPath := filepath.Join(dir, "out.csv")
	for i, confirm := range []string{"", "y"} {
		press(m, key('e'))
		if i == 1 && string(m.prompt.input) != csvPath {
			t.Fatalf("prompt should offer the last file, got %q", string(m.prompt.input))
		}
		answer(m, csvPath)
		if confirm != "" {
			if !strings.Contains(m.prompt.label, "Overwrite?") {
				t.Fatalf("no overwrite question: %q", m.prompt.label)
			}
			answer(m, confirm)
		}
	}
	f, _ := os.Open(csvPath)
	recs, err := csv.NewReader(f).ReadAll()
	f.Close()
	if err != nil || len(recs) != 2501 || recs[0][1] != "NAME" || recs[10][1] != "" || recs[1][3] != `say "hi", ok` {
		t.Fatalf("CSV: err=%v n=%d", err, len(recs))
	}

	// declining the overwrite leaves the file alone
	before, _ := os.ReadFile(csvPath)
	press(m, key('e'))
	answer(m, csvPath)
	answer(m, "n")
	after, _ := os.ReadFile(csvPath)
	if string(before) != string(after) || m.status != "Export cancelled" {
		t.Fatalf("declined overwrite changed the file / status %q", m.status)
	}

	// bad extension
	press(m, key('e'))
	answer(m, filepath.Join(dir, "out.xml"))
	if !m.statusErr || !strings.Contains(m.status, "use .csv, .tsv, .json, .xlsx, .xls or .sql") {
		t.Fatalf("status: %q", m.status)
	}

	// Excel: all rows, numbers as numbers
	for _, name := range []string{"out.xlsx", "out.xls"} {
		press(m, key('e'))
		answer(m, filepath.Join(dir, name))
		if m.statusErr || !strings.Contains(m.status, "Exported 2500 row(s)") {
			t.Fatalf("%s: %q", name, m.status)
		}
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() < 1000 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	b, _ = os.ReadFile(filepath.Join(dir, "out.xls"))
	if strings.Count(string(b), "<Row>") != 2501 || !strings.Contains(string(b), `<Data ss:Type="Number">2500</Data>`) {
		t.Fatalf(".xls content: %d rows", strings.Count(string(b), "<Row>"))
	}
}

func TestUIExportInserts(t *testing.T) {
	m := newTestModel(t, false)
	ctx := context.Background()
	defer m.conn.Execute(ctx, "drop table sqlcli_test_insexp purge", 1)
	m.conn.Execute(ctx, "drop table sqlcli_test_insexp purge", 1)
	run(m, "create table sqlcli_test_insexp (id number(10,2), name varchar2(100), born date, at timestamp(6), doc clob, bin raw(16))")
	run(m, `insert into sqlcli_test_insexp values (1.5, 'O''Brien & co'||chr(10)||'line 2', date '1999-12-31', timestamp '2026-01-02 03:04:05.123456', rpad('x', 5000, 'yz'), hextoraw('0A0B'))`)
	run(m, "insert into sqlcli_test_insexp (id) values (-2)")
	press(m, key(tea.KeyF9))
	snapshot := func() string {
		r, err := m.conn.Execute(ctx, `select id || '|' || name || '|' || to_char(born, 'YYYY-MM-DD HH24:MI:SS') || '|' ||
			to_char(at, 'YYYY-MM-DD HH24:MI:SS.FF6') || '|' || dbms_lob.getlength(doc) || '|' || dbms_lob.substr(doc, 20, 4980) || '|' || rawtohex(bin)
			from sqlcli_test_insexp order by id`, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, row := range r.Rows {
			out = append(out, row[0])
		}
		return strings.Join(out, "\n")
	}
	before := snapshot()

	dir := t.TempDir()
	run(m, "select rowid, t.* from sqlcli_test_insexp t order by id")
	m.focus = focusResults
	press(m, key('e'))
	answer(m, filepath.Join(dir, "rows.sql"))
	if m.statusErr || !strings.Contains(m.status, "Exported 2 row(s)") {
		t.Fatalf("export: %q", m.status)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "rows.sql"))
	if !strings.Contains(string(b), "INSERT INTO SQLCLI_TEST_INSEXP (ID, NAME, BORN, AT, DOC, BIN)") || strings.Contains(string(b), "ROWID") {
		t.Fatalf("file:\n%s", b)
	}

	// run the file back into the empty table: the rows come back the same
	run(m, "delete from sqlcli_test_insexp")
	m.ed.SetText(string(b))
	m.focus = focusEditor
	press(m, key(tea.KeyF7))
	if m.res.err != "" {
		t.Fatalf("running the export: %s", m.res.err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("rows differ:\nbefore %s\nafter  %s", before, after)
	}
	press(m, key(tea.KeyF9)) // keep the rows for the join below

	// a join can't be exported as INSERTs
	run(m, "select t.id, d.dummy from sqlcli_test_insexp t join dual d on 1 = 1")
	m.focus = focusResults
	press(m, key('e'))
	answer(m, filepath.Join(dir, "join.sql"))
	if !m.statusErr || !strings.Contains(m.status, "must read one table") {
		t.Fatalf("join: %q", m.status)
	}
}
