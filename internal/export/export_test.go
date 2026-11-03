package export

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlcli/internal/db"
)

var (
	cols  = []string{"ID", "NAME", "AMOUNT", "ID"}
	types = []string{"NUMBER", "VARCHAR2", "NUMBER", "NUMBER"}
	rows  = [][]string{
		{"1", `say "hi", ok`, ".5", "7"},
		{"2", db.Null, "-.25", db.Null},
		{"3", "(null)", "1e5", "8"},
		{"4", "multi\nline", "12345678901234567890.123", "9"},
	}
)

func TestCSV(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, CSV, cols, types, rows); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v\n%s", err, b.String())
	}
	if len(recs) != 5 || recs[1][1] != `say "hi", ok` || recs[2][1] != "" || recs[3][1] != "(null)" || recs[4][1] != "multi\nline" {
		t.Fatalf("records: %q", recs)
	}
}

func TestTSV(t *testing.T) {
	var b strings.Builder
	_ = Write(&b, TSV, cols, types, rows[:1])
	if first := strings.SplitN(b.String(), "\n", 2)[0]; first != "ID\tNAME\tAMOUNT\tID" {
		t.Fatalf("header: %q", first)
	}
}

func TestJSON(t *testing.T) {
	var b strings.Builder
	if err := Write(&b, JSON, cols, types, rows); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b.String())
	}
	if len(got) != 4 {
		t.Fatalf("rows: %d", len(got))
	}
	if got[0]["ID"] != 1.0 || got[0]["AMOUNT"] != 0.5 || got[0]["ID_2"] != 7.0 || got[0]["NAME"] != `say "hi", ok` {
		t.Fatalf("row 1: %v", got[0])
	}
	if got[1]["NAME"] != nil || got[1]["AMOUNT"] != -0.25 || got[1]["ID_2"] != nil {
		t.Fatalf("row 2: %v", got[1])
	}
	if got[2]["NAME"] != "(null)" {
		t.Fatalf("the string '(null)' must stay a string: %v", got[2])
	}
	// big numbers are written exactly, not rounded through float64
	if !strings.Contains(b.String(), `"AMOUNT": 12345678901234567890.123`) {
		t.Fatalf("big number changed:\n%s", b.String())
	}
	// column order is kept
	if !strings.Contains(b.String(), `{"ID": 1, "NAME": `) {
		t.Fatalf("key order:\n%s", b.String())
	}
}

func TestJSONEmpty(t *testing.T) {
	var b strings.Builder
	_ = Write(&b, JSON, cols, types, nil)
	if b.String() != "[]\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteFile(filepath.Join(dir, "out.xlsx"), cols, types, rows); err == nil {
		t.Fatal("unknown extension accepted")
	}
	p := filepath.Join(dir, "out.CSV")
	if err := WriteFile(p, cols, types, rows); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.HasPrefix(string(b), "ID,NAME,AMOUNT,ID\n") {
		t.Fatalf("file: %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temporary file left behind: %v", ents)
	}
}
