// Package export writes query results as CSV, TSV, JSON or Excel (.xlsx, .xls).
package export

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sqlcli/internal/db"
)

// Format is an export file format.
type Format int

const (
	CSV Format = iota
	TSV
	JSON
	XLSX
	XLS
	SQL // INSERT statements (WriteInsertsFile; needs the table)
)

// FormatFor picks the format from a file name's extension.
func FormatFor(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return CSV, nil
	case ".tsv":
		return TSV, nil
	case ".json":
		return JSON, nil
	case ".xlsx":
		return XLSX, nil
	case ".xls":
		return XLS, nil
	case ".sql":
		return SQL, nil
	}
	return 0, fmt.Errorf("unknown file type %q: use .csv, .tsv, .json, .xlsx, .xls or .sql", filepath.Ext(path))
}

// Write writes columns and rows to w. NULL (db.Null) becomes an empty
// field in CSV/TSV and null in JSON; NUMBER columns become JSON numbers.
func Write(w io.Writer, f Format, cols, types []string, rows [][]string) error {
	bw := bufio.NewWriterSize(w, 1<<16)
	var err error
	switch f {
	case CSV, TSV:
		err = writeCSV(bw, f, cols, rows)
	case JSON:
		err = writeJSON(bw, cols, types, rows)
	case XLSX:
		err = writeXLSX(bw, cols, types, rows)
	case XLS:
		err = writeXLS(bw, cols, types, rows)
	case SQL:
		err = fmt.Errorf("INSERT statements need the table: use WriteInsertsFile")
	}
	if err != nil {
		return err
	}
	return bw.Flush()
}

func writeCSV(w io.Writer, f Format, cols []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if f == TSV {
		cw.Comma = '\t'
	}
	if err := cw.Write(cols); err != nil {
		return err
	}
	rec := make([]string, len(cols))
	for _, row := range rows {
		for i, v := range row {
			if v == db.Null {
				v = ""
			}
			rec[i] = v
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

var reNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// jsonNumber returns v as a JSON number literal, or "" if it isn't one.
// Oracle may print ".5" or "-.5", which JSON doesn't allow.
func jsonNumber(v string) string {
	if strings.HasPrefix(v, ".") {
		v = "0" + v
	} else if strings.HasPrefix(v, "-.") {
		v = "-0" + v[1:]
	}
	if reNumber.MatchString(v) {
		return v
	}
	return ""
}

func isNumeric(t string) bool {
	switch strings.ToUpper(t) {
	case "NUMBER", "FLOAT", "BINARY_FLOAT", "BINARY_DOUBLE", "INTEGER":
		return true
	}
	return false
}

// uniqueKeys makes column names usable as JSON object keys: duplicates
// (select a.id, b.id ...) get a _2, _3 ... suffix.
func uniqueKeys(cols []string) []string {
	seen := map[string]int{}
	out := make([]string, len(cols))
	for i, c := range cols {
		seen[c]++
		if n := seen[c]; n > 1 {
			c = fmt.Sprintf("%s_%d", c, n)
		}
		out[i] = c
	}
	return out
}

func writeJSON(w io.Writer, cols, types []string, rows [][]string) error {
	keys := make([][]byte, len(cols))
	for i, k := range uniqueKeys(cols) {
		b, _ := json.Marshal(k)
		keys[i] = b
	}
	numeric := make([]bool, len(cols))
	for i := range cols {
		numeric[i] = i < len(types) && isNumeric(types[i])
	}
	var b strings.Builder
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	for n, row := range rows {
		b.Reset()
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n  {")
		for i, v := range row {
			if i > 0 {
				b.WriteString(", ")
			}
			b.Write(keys[i])
			b.WriteString(": ")
			switch num := jsonNumber(v); {
			case v == db.Null:
				b.WriteString("null")
			case numeric[i] && num != "":
				b.WriteString(num)
			default:
				s, _ := json.Marshal(v)
				b.Write(s)
			}
		}
		b.WriteString("}")
		if _, err := io.WriteString(w, b.String()); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		_, err := io.WriteString(w, "\n]\n")
		return err
	}
	_, err := io.WriteString(w, "]\n")
	return err
}

// WriteFile exports to path atomically: it writes a temporary file next to
// it and renames it into place, so a failed export never leaves half a file.
func WriteFile(path string, cols, types []string, rows [][]string) error {
	f, err := FormatFor(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := Write(tmp, f, cols, types, rows); err != nil {
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
