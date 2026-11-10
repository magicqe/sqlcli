package export

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"sqlcli/internal/db"
)

// Excel export: .xlsx (Office Open XML, written with excelize) and .xls
// (Excel 2003 XML Spreadsheet, which Excel and LibreOffice open). NUMBER
// columns become numeric cells, DATE and TIMESTAMP columns date cells;
// the header row is bold and frozen.

const (
	maxXLSXRows = 1048576 - 1 // the sheet's rows, less the header
	maxXLSRows  = 65536 - 1
)

// excelNumber parses a NUMBER value for a numeric cell; values with more
// digits than a float64 keeps exactly (e.g. long IDs) stay text.
func excelNumber(v string) (float64, bool) {
	n := jsonNumber(v)
	if n == "" {
		return 0, false
	}
	digits := 0
	for _, r := range strings.TrimLeft(strings.SplitN(n, "e", 2)[0], "-0.") {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits > 15 {
		return 0, false
	}
	f, err := strconv.ParseFloat(n, 64)
	return f, err == nil
}

func isDateType(t string) bool {
	t = strings.ToUpper(t)
	return t == "DATE" || strings.HasPrefix(t, "TIMESTAMP")
}

// excelDate parses a date as sqlcli shows it (db.FormatValue).
func excelDate(v string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05.000000", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// cellValue converts a value for a cell: nil (NULL), float64, time.Time or
// the string.
func cellValue(v, typ string) any {
	switch {
	case v == db.Null:
		return nil
	case isNumeric(typ):
		if f, ok := excelNumber(v); ok {
			return f
		}
	case isDateType(typ):
		if t, ok := excelDate(v); ok {
			return t
		}
	}
	return v
}

func colType(types []string, i int) string {
	if i < len(types) {
		return types[i]
	}
	return ""
}

func writeXLSX(w io.Writer, cols, types []string, rows [][]string) error {
	if len(rows) > maxXLSXRows {
		return fmt.Errorf("%d rows don't fit an .xlsx sheet (at most %d)", len(rows), maxXLSXRows)
	}
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Result"
	f.SetSheetName("Sheet1", sheet)
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}
	dateFmt := "yyyy-mm-dd hh:mm:ss"
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &dateFmt})
	if err != nil {
		return err
	}
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	// column widths from the header and the first rows
	for i := range cols {
		wd := len([]rune(cols[i]))
		for n := 0; n < len(rows) && n < 200; n++ {
			wd = max(wd, len([]rune(rows[n][i])))
		}
		if isDateType(colType(types, i)) {
			wd = max(wd, 19)
		}
		if err := sw.SetColWidth(i+1, i+1, float64(min(max(wd, 4), 60)+2)); err != nil {
			return err
		}
	}
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return err
	}
	hdr := make([]any, len(cols))
	for i, c := range cols {
		hdr[i] = excelize.Cell{StyleID: bold, Value: c}
	}
	if err := sw.SetRow("A1", hdr); err != nil {
		return err
	}
	vals := make([]any, len(cols))
	for n, row := range rows {
		for i, v := range row {
			cv := cellValue(v, colType(types, i))
			if _, ok := cv.(time.Time); ok {
				vals[i] = excelize.Cell{StyleID: dateStyle, Value: cv}
			} else {
				vals[i] = cv
			}
		}
		cell, _ := excelize.CoordinatesToCellName(1, n+2)
		if err := sw.SetRow(cell, vals); err != nil {
			return err
		}
	}
	if err := sw.Flush(); err != nil {
		return err
	}
	if len(cols) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(cols), len(rows)+1)
		if err := f.AutoFilter(sheet, "A1:"+last, nil); err != nil {
			return err
		}
	}
	return f.Write(w)
}

// writeXLS writes an Excel 2003 XML Spreadsheet.
func writeXLS(w io.Writer, cols, types []string, rows [][]string) error {
	if len(rows) > maxXLSRows {
		return fmt.Errorf("%d rows don't fit an .xls sheet (at most %d): use .xlsx", len(rows), maxXLSRows)
	}
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return strings.ReplaceAll(b.String(), "&#xA;", "&#10;")
	}
	var b strings.Builder
	put := func(s string) error {
		_, err := io.WriteString(w, s)
		return err
	}
	if err := put(`<?xml version="1.0" encoding="UTF-8"?>
<?mso-application progid="Excel.Sheet"?>
<Workbook xmlns="urn:schemas-microsoft-com:office:spreadsheet" xmlns:ss="urn:schemas-microsoft-com:office:spreadsheet">
 <Styles>
  <Style ss:ID="h"><Font ss:Bold="1"/></Style>
  <Style ss:ID="d"><NumberFormat ss:Format="yyyy\-mm\-dd\ hh:mm:ss"/></Style>
 </Styles>
 <Worksheet ss:Name="Result">
  <Table>
`); err != nil {
		return err
	}
	b.WriteString("   <Row>")
	for _, c := range cols {
		b.WriteString(`<Cell ss:StyleID="h"><Data ss:Type="String">` + esc(c) + `</Data></Cell>`)
	}
	b.WriteString("</Row>\n")
	if err := put(b.String()); err != nil {
		return err
	}
	for _, row := range rows {
		b.Reset()
		b.WriteString("   <Row>")
		for i, v := range row {
			switch cv := cellValue(v, colType(types, i)).(type) {
			case nil:
				b.WriteString("<Cell/>")
			case float64:
				b.WriteString(`<Cell><Data ss:Type="Number">` + strconv.FormatFloat(cv, 'g', -1, 64) + `</Data></Cell>`)
			case time.Time:
				b.WriteString(`<Cell ss:StyleID="d"><Data ss:Type="DateTime">` + cv.Format("2006-01-02T15:04:05.000") + `</Data></Cell>`)
			default:
				b.WriteString(`<Cell><Data ss:Type="String">` + esc(v) + `</Data></Cell>`)
			}
		}
		b.WriteString("</Row>\n")
		if err := put(b.String()); err != nil {
			return err
		}
	}
	return put(`  </Table>
  <WorksheetOptions xmlns="urn:schemas-microsoft-com:office:excel">
   <FreezePanes/><FrozenNoSplit/><SplitHorizontal>1</SplitHorizontal><TopRowBottomPane>1</TopRowBottomPane><ActivePane>2</ActivePane>
  </WorksheetOptions>
  <AutoFilter x:Range="R1C1:R` + strconv.Itoa(len(rows)+1) + `C` + strconv.Itoa(max(len(cols), 1)) + `" xmlns="urn:schemas-microsoft-com:office:excel" xmlns:x="urn:schemas-microsoft-com:office:excel"/>
 </Worksheet>
</Workbook>
`)
}
