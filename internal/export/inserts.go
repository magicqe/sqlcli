package export

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sqlcli/internal/db"
)

// SQL INSERT export (.sql): one INSERT statement per row for the table a
// result comes from. Values are written as Oracle literals by the table
// column's type: numbers as they are, dates as TO_DATE / TO_TIMESTAMP,
// RAW as HEXTORAW, long CLOBs as TO_CLOB pieces, everything else as a
// quoted string; NULL as NULL. Columns whose values can't be written as
// literals (BLOB, BFILE, LONG RAW, objects ...) are left out.

// InsertTable is the target of a .sql export.
type InsertTable struct {
	Owner, Name string            // Owner may be ""
	ColTypes    map[string]string // the table's columns (upper case) and their data types
}

// clobPiece is the size of the pieces long CLOB values are written in: a
// string literal may hold at most 4000 bytes.
const clobPiece = 1000

// insertType says how a column type is written: "num", "date", "ts",
// "raw", "clob", "str", or "" when it can't be written as a literal.
func insertType(t string) string {
	t = strings.ToUpper(t)
	switch {
	case isNumeric(t):
		return "num"
	case t == "DATE":
		return "date"
	case strings.HasPrefix(t, "TIMESTAMP"):
		return "ts"
	case t == "RAW":
		return "raw"
	case strings.HasSuffix(t, "CLOB"):
		return "clob"
	case strings.Contains(t, "CHAR"), t == "LONG", strings.HasPrefix(t, "INTERVAL"), t == "ROWID", t == "UROWID":
		return "str"
	}
	return ""
}

func quoteSQL(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func ident(s string) string {
	plain := s != ""
	for i, r := range s {
		if !(r >= 'A' && r <= 'Z' || r == '_' || r == '$' || r == '#' || i > 0 && r >= '0' && r <= '9') {
			plain = false
		}
	}
	if plain {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqlLiteral writes v as an Oracle literal of the given kind.
func sqlLiteral(v, kind string) string {
	if v == db.Null {
		return "NULL"
	}
	switch kind {
	case "num":
		if n := jsonNumber(v); n != "" {
			return v
		}
	case "date":
		if t, ok := excelDate(v); ok {
			return "TO_DATE('" + t.Format("2006-01-02 15:04:05") + "', 'YYYY-MM-DD HH24:MI:SS')"
		}
	case "ts":
		if t, ok := excelDate(v); ok {
			return "TO_TIMESTAMP('" + t.Format("2006-01-02 15:04:05.000000") + "', 'YYYY-MM-DD HH24:MI:SS.FF6')"
		}
	case "raw":
		return "HEXTORAW(" + quoteSQL(v) + ")"
	case "clob":
		r := []rune(v)
		if len(r) > clobPiece {
			var parts []string
			for i := 0; i < len(r); i += clobPiece {
				parts = append(parts, "TO_CLOB("+quoteSQL(string(r[i:min(i+clobPiece, len(r))]))+")")
			}
			return strings.Join(parts, "\n    || ")
		}
	}
	return quoteSQL(v)
}

// WriteInserts writes the rows as INSERT statements into t. Result columns
// that aren't columns of the table (ROWID, expressions) or can't be
// written as literals are left out; it returns the columns written.
func WriteInserts(w io.Writer, t InsertTable, cols []string, rows [][]string) ([]string, error) {
	var idx []int
	var names, kinds, skipped []string
	seen := map[string]bool{}
	for i, c := range cols {
		u := c // the dictionary name: as returned (quoted names), else upper case
		typ, ok := t.ColTypes[u]
		if !ok {
			u = strings.ToUpper(c)
			typ, ok = t.ColTypes[u]
		}
		if !ok || seen[u] {
			continue
		}
		seen[u] = true
		k := insertType(typ)
		if k == "" {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", u, typ))
			continue
		}
		idx, names, kinds = append(idx, i), append(names, ident(u)), append(kinds, k)
	}
	if len(idx) == 0 {
		return nil, fmt.Errorf("none of the result's columns can be written as an INSERT into %s", t.Name)
	}
	table := ident(t.Name)
	if t.Owner != "" {
		table = ident(t.Owner) + "." + table
	}
	bw := bufio.NewWriterSize(w, 1<<16)
	fmt.Fprintf(bw, "-- %d row(s) of %s, exported by sqlcli on %s\n", len(rows), table, time.Now().Format("2006-01-02 15:04:05"))
	if len(skipped) > 0 {
		fmt.Fprintf(bw, "-- left out (no literal for these types): %s\n", strings.Join(skipped, ", "))
	}
	bw.WriteString("SET DEFINE OFF;\n\n") // & in values must not ask for substitution variables
	head := "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ")\n  VALUES ("
	vals := make([]string, len(idx))
	for _, row := range rows {
		for j, i := range idx {
			vals[j] = sqlLiteral(row[i], kinds[j])
		}
		bw.WriteString(head + strings.Join(vals, ", ") + ");\n")
	}
	return names, bw.Flush()
}

// WriteInsertsFile writes the INSERT statements to path atomically.
func WriteInsertsFile(path string, t InsertTable, cols []string, rows [][]string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := WriteInserts(tmp, t, cols, rows); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
