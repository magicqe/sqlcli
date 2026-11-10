package export

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"sqlcli/internal/db"
)

var (
	xCols  = []string{"ID", "NAME", "BORN", "BIG", "NOTE"}
	xTypes = []string{"NUMBER", "VARCHAR2", "DATE", "NUMBER", "CLOB"}
	xRows  = [][]string{
		{"1", "Žofia", "1999-12-31 23:59:00", "12345678901234567890", "a <b> & \"c\"\nline 2"},
		{"-.5", db.Null, db.Null, "3", ""},
	}
)

func TestXLSX(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.xlsx")
	if err := WriteFile(p, xCols, xTypes, xRows); err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := f.GetRows("Result")
	if err != nil || len(rows) != 3 || strings.Join(rows[0], ",") != "ID,NAME,BORN,BIG,NOTE" {
		t.Fatalf("rows %v %v", rows, err)
	}
	if rows[1][1] != "Žofia" || rows[1][2] != "1999-12-31 23:59:00" || rows[1][3] != "12345678901234567890" || rows[1][4] != "a <b> & \"c\"\nline 2" {
		t.Fatalf("row 1: %q", rows[1])
	}
	// numbers and dates are numeric cells (no string type); the 20-digit number stays text
	for _, cell := range []string{"A2", "C2", "D3"} {
		if ct, _ := f.GetCellType("Result", cell); ct != excelize.CellTypeUnset && ct != excelize.CellTypeNumber {
			t.Errorf("%s: type %v, want a number", cell, ct)
		}
	}
	if ct, _ := f.GetCellType("Result", "D2"); ct != excelize.CellTypeSharedString && ct != excelize.CellTypeInlineString {
		t.Errorf("D2: type %v, want text", ct)
	}
	if v, _ := f.GetCellValue("Result", "C2", excelize.Options{RawCellValue: true}); !strings.HasPrefix(v, "36525.99") {
		t.Errorf("C2 date serial = %q", v)
	}
	if v, _ := f.GetCellValue("Result", "A3", excelize.Options{RawCellValue: true}); v != "-0.5" {
		t.Fatalf("A3 = %q", v)
	}
	if v, _ := f.GetCellValue("Result", "B3"); v != "" {
		t.Fatalf("NULL = %q", v)
	}
}

func TestXLS(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.xls")
	if err := WriteFile(p, xCols, xTypes, xRows); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	var v any
	if err := xml.Unmarshal(b, &v); err != nil {
		t.Fatalf("not XML: %v\n%s", err, s)
	}
	for _, want := range []string{
		`<?mso-application progid="Excel.Sheet"?>`,
		`<Cell ss:StyleID="h"><Data ss:Type="String">ID</Data></Cell>`,
		`<Data ss:Type="Number">1</Data>`,
		`<Data ss:Type="DateTime">1999-12-31T23:59:00.000</Data>`,
		`<Data ss:Type="String">12345678901234567890</Data>`,
		`a &lt;b&gt; &amp; &#34;c&#34;&#10;line 2`,
		`<Data ss:Type="Number">-0.5</Data>`,
		`<Cell/>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestExcelRowLimit(t *testing.T) {
	rows := make([][]string, maxXLSRows+1)
	for i := range rows {
		rows[i] = []string{"1"}
	}
	err := WriteFile(filepath.Join(t.TempDir(), "big.xls"), []string{"N"}, []string{"NUMBER"}, rows)
	if err == nil || !strings.Contains(err.Error(), "use .xlsx") {
		t.Fatalf("limit: %v", err)
	}
	if f, err := FormatFor("x.XLSX"); err != nil || f != XLSX {
		t.Fatal("FormatFor .XLSX")
	}
}
