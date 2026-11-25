package export

import (
	"strings"
	"testing"

	"sqlcli/internal/db"
)

func TestWriteInserts(t *testing.T) {
	var b strings.Builder
	tbl := InsertTable{Name: "EMP", ColTypes: map[string]string{
		"ID": "NUMBER", "NAME": "VARCHAR2", "BORN": "DATE", "AT": "TIMESTAMP(6)", "PIC": "BLOB", "BIN": "RAW", "Mixed": "VARCHAR2",
	}}
	cols := []string{"ROWID", "ID", "NAME", "BORN", "AT", "PIC", "BIN", "Mixed", "ID"}
	rows := [][]string{
		{"AAA", "-.5", "O'Brien & co\nline 2", "1999-12-31 23:59:00", "2026-01-02 03:04:05.123456", "(lob)", "0A0B", "x", "-.5"},
		{"AAB", "2", db.Null, db.Null, "2026-01-02 03:04:05", db.Null, db.Null, db.Null, "2"},
	}
	names, err := WriteInserts(&b, tbl, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != `ID,NAME,BORN,AT,BIN,"Mixed"` {
		t.Fatalf("columns: %v", names)
	}
	s := b.String()
	for _, want := range []string{
		"-- left out (no literal for these types): PIC (BLOB)",
		"SET DEFINE OFF;",
		`INSERT INTO EMP (ID, NAME, BORN, AT, BIN, "Mixed")`,
		`VALUES (-.5, 'O''Brien & co` + "\nline 2', TO_DATE('1999-12-31 23:59:00', 'YYYY-MM-DD HH24:MI:SS'), TO_TIMESTAMP('2026-01-02 03:04:05.123456', 'YYYY-MM-DD HH24:MI:SS.FF6'), HEXTORAW('0A0B'), 'x');",
		"VALUES (2, NULL, NULL, TO_TIMESTAMP('2026-01-02 03:04:05.000000', 'YYYY-MM-DD HH24:MI:SS.FF6'), NULL, NULL);",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	// long CLOBs are written in pieces; owner-qualified names
	b.Reset()
	long := strings.Repeat("ab", 1200)
	WriteInserts(&b, InsertTable{Owner: "MEN", Name: "DOCS", ColTypes: map[string]string{"DOC": "CLOB"}}, []string{"DOC"}, [][]string{{long}})
	if !strings.Contains(b.String(), "INSERT INTO MEN.DOCS (DOC)") || strings.Count(b.String(), "TO_CLOB(") != 3 {
		t.Fatalf("clob:\n%s", b.String()[:300])
	}
	if _, err := WriteInserts(&b, InsertTable{Name: "T", ColTypes: map[string]string{"X": "NUMBER"}}, []string{"Y"}, nil); err == nil {
		t.Fatal("no matching column should be an error")
	}
	if f, _ := FormatFor("out.SQL"); f != SQL {
		t.Fatal("FormatFor .sql")
	}
}
