package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCompiledObject(t *testing.T) {
	cases := map[string][3]string{
		"create or replace package body sqlcli_x as":   {"PACKAGE BODY", "", "SQLCLI_X"},
		"CREATE PACKAGE  BODY hr.pkg IS":               {"PACKAGE BODY", "HR", "PKG"},
		"create or replace editionable procedure p is": {"PROCEDURE", "", "P"},
		`create function "MixedFn" return number is`:   {"FUNCTION", "", "MixedFn"},
		"-- c\ncreate or replace type body t as":       {"TYPE BODY", "", "T"},
		"create trigger trg before insert on t":        {"TRIGGER", "", "TRG"},
	}
	for stmt, want := range cases {
		k, o, n, ok := compiledObject(stmt)
		if !ok || [3]string{k, o, n} != want {
			t.Errorf("%q: %q %q %q %v", stmt, k, o, n, ok)
		}
	}
	if _, _, _, ok := compiledObject("create table t (x number)"); ok {
		t.Error("table is not compiled PL/SQL")
	}
}

// TestIntegrationPackageSpecAndBody creates a package (spec and body in one
// script, without "/"), checks both are valid, then shows compilation errors.
func TestIntegrationPackageSpecAndBody(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop package sqlcli_test_pkg", 1)
	spec := "create or replace package sqlcli_test_pkg as\n  procedure hello;\nend;"
	body := "create or replace package body sqlcli_test_pkg as\n  procedure hello is\n  begin\n    dbms_output.put_line('hi');\n  end;\nend;"
	stmts := SplitStatements(spec + "\n\n" + body)
	if len(stmts) != 2 {
		t.Fatalf("split: %d", len(stmts))
	}
	for _, s := range stmts {
		r, err := c.Execute(ctx, s, 1)
		if err != nil || len(r.Errors) != 0 {
			t.Fatalf("%q: %v %v", s[:30], err, r.Errors)
		}
		t.Logf("%s", r.Message)
	}
	r, _ := c.Execute(ctx, "select object_type || ' ' || status from user_objects where object_name = 'SQLCLI_TEST_PKG' order by 1", 10)
	if len(r.Rows) != 2 || r.Rows[0][0] != "PACKAGE BODY VALID" || r.Rows[1][0] != "PACKAGE VALID" {
		t.Fatalf("objects: %v", r.Rows)
	}

	// a body with an error: created, but invalid, and the error is reported
	bad := "create or replace package body sqlcli_test_pkg as\n  procedure hello is\n  begin\n    no_such_proc;\n  end;\nend;"
	r, err := c.Execute(ctx, bad, 1)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	t.Logf("%s %v", r.Message, r.Errors)
	if r.Message != "PACKAGE BODY SQLCLI_TEST_PKG created with compilation errors." || len(r.Errors) == 0 ||
		!strings.HasPrefix(r.Errors[0], "4/5") || !strings.Contains(strings.Join(r.Errors, " "), "NO_SUCH_PROC") {
		t.Fatalf("errors: %q %q", r.Message, r.Errors)
	}
}

func TestIntegrationPackageMembers(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop package sqlcli_test_pkg3", 1)
	for _, s := range SplitStatements(`create or replace package sqlcli_test_pkg3 as
  function call1 (v number) return number;
  function call1 (v varchar2) return varchar2;   -- overload
  procedure reset;
  function call2 return varchar2;
end;`) {
		if _, err := c.Execute(ctx, s, 1); err != nil {
			t.Fatal(CleanError(err))
		}
	}
	ms, err := c.Members(ctx, "", "SQLCLI_TEST_PKG3")
	if err != nil {
		t.Fatal(CleanError(err))
	}
	want := []Member{{"CALL1", true}, {"RESET", false}, {"CALL2", true}}
	if len(ms) != 3 || ms[0] != want[0] || ms[1] != want[1] || ms[2] != want[2] {
		t.Fatalf("members: %+v", ms)
	}
	// a public synonym to a SYS package
	ms, err = c.Members(ctx, "", "DBMS_OUTPUT")
	names := map[string]bool{}
	for _, m := range ms {
		names[m.Name] = m.IsFunction
	}
	if err != nil || !(len(names) > 5) || names["PUT_LINE"] || len(ms) == 0 {
		t.Fatalf("DBMS_OUTPUT: %v %v", ms, err)
	}
	if _, ok := names["PUT_LINE"]; !ok {
		t.Fatalf("PUT_LINE missing: %v", ms)
	}
	// owner given
	if ms, _ := c.Members(ctx, "SYS", "DBMS_OUTPUT"); len(ms) == 0 {
		t.Fatal("SYS.DBMS_OUTPUT")
	}
	// not a package
	if ms, _ := c.Members(ctx, "", "DUAL"); len(ms) != 0 {
		t.Fatalf("DUAL: %v", ms)
	}
}

func TestIntegrationDDL(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop table sqlcli_test_ddl purge", 1)
	defer c.Execute(ctx, "drop package sqlcli_test_pkg6", 1)
	defer c.Execute(ctx, "drop package sqlcli_test_spec_only", 1)
	for _, s := range SplitStatements(`create table sqlcli_test_ddl (id number(5) not null, name varchar2(10));
create or replace package sqlcli_test_pkg6 as
  function f return number;
end;
/
create or replace package body sqlcli_test_pkg6 as
  function f return number is begin return 1; end;
end;
/
create or replace package sqlcli_test_spec_only as
  c constant number := 1;
end;
/`) {
		if _, err := c.Execute(ctx, s, 1); err != nil {
			t.Fatal(CleanError(err))
		}
	}
	ddl, err := c.DDL(ctx, "TABLE", "SQLCLI_TEST_DDL")
	if err != nil || !strings.Contains(ddl, `CREATE TABLE "`) || !strings.Contains(ddl, `"SQLCLI_TEST_DDL"`) || !strings.Contains(ddl, ";") {
		t.Fatalf("table: %v\n%s", err, ddl)
	}
	if c.InTransaction() {
		t.Fatal("DDL opened a transaction")
	}
	ddl, err = c.DDL(ctx, "PACKAGE", "SQLCLI_TEST_PKG6")
	t.Logf("package DDL:\n%s", ddl)
	spec := strings.Index(ddl, "PACKAGE \"")
	body := strings.Index(ddl, "PACKAGE BODY")
	if err != nil || spec < 0 || body < spec || strings.Count(ddl, "\n/") < 2 {
		t.Fatalf("package: %v\n%s", err, ddl)
	}
	// the DDL runs as it is (spec and body, separated by "/")
	if got := SplitStatements(ddl); len(got) != 2 {
		t.Fatalf("DDL splits into %d statements", len(got))
	}
	ddl, err = c.DDL(ctx, "PACKAGE", "SQLCLI_TEST_SPEC_ONLY")
	if err != nil || strings.Contains(ddl, "PACKAGE BODY") || !strings.Contains(ddl, "SQLCLI_TEST_SPEC_ONLY") {
		t.Fatalf("spec only: %v\n%s", err, ddl)
	}
	if _, err := c.DDL(ctx, "TABLE", "NO_SUCH_TABLE_XYZ"); err == nil || !strings.Contains(err.Error(), "ORA-31603") {
		t.Fatalf("missing: %v", err)
	}
}

func TestIntegrationUpdateCell(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop table sqlcli_test_edit purge", 1)
	if _, err := c.Execute(ctx, "create table sqlcli_test_edit (id number(5), name varchar2(20), amount number(10,2), born date)", 1); err != nil {
		t.Fatal(CleanError(err))
	}
	for i := 1; i <= 30; i++ {
		c.Execute(ctx, fmt.Sprintf("insert into sqlcli_test_edit values (%d, 'n%d', %d.5, date '2026-01-01')", i, i, i), 1)
	}
	c.Commit()
	r, err := c.Execute(ctx, "select rowid, t.* from sqlcli_test_edit t order by id", 10)
	if err != nil || !r.More || r.SQL == "" {
		t.Fatalf("query: %v %v", err, r)
	}
	rowid := r.Rows[0][0]

	typ, err := c.ColumnType(ctx, "", "SQLCLI_TEST_EDIT", "AMOUNT")
	if err != nil || typ != "NUMBER" {
		t.Fatalf("column type: %q %v", typ, err)
	}
	if _, err := c.ColumnType(ctx, "", "SQLCLI_TEST_EDIT", "AMOUNT*2"); err == nil {
		t.Fatal("a computed column has no type")
	}
	for _, u := range []struct{ col, typ, val string }{{"NAME", "VARCHAR2", "Žofia"}, {"AMOUNT", "NUMBER", "12,75"}, {"BORN", "DATE", "1999-12-31 23:59"}} {
		if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_EDIT", u.col, u.typ, u.val, false, rowid); err != nil {
			t.Fatalf("%s: %s", u.col, CleanError(err))
		}
	}
	if !c.InTransaction() {
		t.Fatal("UpdateCell should run in a transaction")
	}
	// the open result cursor is still usable
	rows, done, err := c.FetchMore(ctx, -1)
	if err != nil || !done || len(rows) != 20 {
		t.Fatalf("cursor after update: %d %v", len(rows), err)
	}
	r, _ = c.Execute(ctx, "select name, to_char(amount), to_char(born, 'YYYY-MM-DD HH24:MI') from sqlcli_test_edit where id = 1", 1)
	if r.Rows[0][0] != "Žofia" || r.Rows[0][1] != "12.75" || r.Rows[0][2] != "1999-12-31 23:59" {
		t.Fatalf("updated row: %v", r.Rows[0])
	}
	if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_EDIT", "NAME", "VARCHAR2", "", true, rowid); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_EDIT", "AMOUNT", "NUMBER", "abc", false, rowid); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Fatalf("bad number: %v", err)
	}
	if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_EDIT", "BORN", "DATE", "31.12.1999", false, rowid); err == nil || !strings.Contains(err.Error(), "not a date") {
		t.Fatalf("bad date: %v", err)
	}
	c.Rollback()
	r, _ = c.Execute(ctx, "select name from sqlcli_test_edit where id = 1", 1)
	if r.Rows[0][0] != "n1" {
		t.Fatalf("rollback: %v", r.Rows[0])
	}
	c.Execute(ctx, "delete from sqlcli_test_edit where id = 1", 1)
	if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_EDIT", "NAME", "VARCHAR2", "x", false, rowid); err == nil || !(strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "ORA-08006")) {
		t.Fatalf("deleted row: %v", err)
	}
	c.Rollback()
}

func TestIntegrationInsertDeleteRow(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop table sqlcli_test_ins purge", 1)
	if _, err := c.Execute(ctx, "create table sqlcli_test_ins (id number(5) not null, name varchar2(20), born date)", 1); err != nil {
		t.Fatal(CleanError(err))
	}
	types, err := c.ColumnTypes(ctx, "", "SQLCLI_TEST_INS")
	if err != nil || types["ID"] != "NUMBER" || types["BORN"] != "DATE" {
		t.Fatalf("types: %v %v", types, err)
	}
	rowid, err := c.InsertRow(ctx, "", "SQLCLI_TEST_INS", []string{"ID", "NAME", "BORN"}, []string{"NUMBER", "VARCHAR2", "DATE"}, []string{"7", "seven", "2020-02-29"})
	if err != nil || rowid == "" || !c.InTransaction() {
		t.Fatalf("insert: %q %v", rowid, err)
	}
	r, _ := c.Execute(ctx, "select id, name, to_char(born, 'YYYY-MM-DD') from sqlcli_test_ins where rowid = chartorowid('"+rowid+"')", 1)
	if len(r.Rows) != 1 || r.Rows[0][0] != "7" || r.Rows[0][2] != "2020-02-29" {
		t.Fatalf("inserted row: %v", r.Rows)
	}
	if _, err := c.InsertRow(ctx, "", "SQLCLI_TEST_INS", []string{"ID", "NAME"}, []string{"NUMBER", "VARCHAR2"}, []string{"", "no id"}); err == nil || !strings.Contains(err.Error(), "ORA-01400") {
		t.Fatalf("NOT NULL violation: %v", err)
	}
	if err := c.DeleteRow(ctx, "", "SQLCLI_TEST_INS", rowid); err != nil {
		t.Fatal(CleanError(err))
	}
	if r, _ := c.Execute(ctx, "select count(*) from sqlcli_test_ins", 1); r.Rows[0][0] != "0" {
		t.Fatalf("after delete: %v", r.Rows)
	}
	c.Rollback()
	if r, _ := c.Execute(ctx, "select count(*) from sqlcli_test_ins", 1); r.Rows[0][0] != "0" {
		t.Fatalf("rollback of insert+delete: %v", r.Rows)
	}
}

func TestIntegrationClobEdit(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop table sqlcli_test_clob purge", 1)
	if _, err := c.Execute(ctx, "create table sqlcli_test_clob (id number(5), doc clob)", 1); err != nil {
		t.Fatal(CleanError(err))
	}
	long := strings.Repeat("line of text, Žofia\n", 3000) + "end" // 60k: over a string bind's 32k
	rowid, err := c.InsertRow(ctx, "", "SQLCLI_TEST_CLOB", []string{"ID", "DOC"}, []string{"NUMBER", "CLOB"}, []string{"1", "short"})
	if err != nil {
		t.Fatal(CleanError(err))
	}
	if err := c.UpdateCell(ctx, "", "SQLCLI_TEST_CLOB", "DOC", "CLOB", long, false, rowid); err != nil {
		t.Fatal(CleanError(err))
	}
	r, err := c.Execute(ctx, "select doc from sqlcli_test_clob where id = 1", 1)
	if err != nil || len(r.Rows) != 1 || r.Rows[0][0] != long {
		t.Fatalf("CLOB after update: %v (%d chars)", err, len(r.Rows[0][0]))
	}
	if _, err := c.InsertRow(ctx, "", "SQLCLI_TEST_CLOB", []string{"ID", "DOC"}, []string{"NUMBER", "CLOB"}, []string{"2", long}); err != nil {
		t.Fatal(CleanError(err))
	}
	if r, _ := c.Execute(ctx, "select dbms_lob.getlength(doc) from sqlcli_test_clob where id = 2", 1); r.Rows[0][0] != fmt.Sprint(len([]rune(long))) {
		t.Fatalf("inserted CLOB length: %v", r.Rows)
	}
	c.Rollback()
}

func TestIntegrationJavaSource(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	src := "create or replace and compile java source named \"SqlcliTestHello\" as\n" +
		"public class SqlcliTestHello {\n  public static String hi() {\n    int i = 1; i--;\n    return \"hi\" + i;\n  }\n}\n/"
	stmts := SplitStatements(src)
	if len(stmts) != 1 {
		t.Fatalf("split: %q", stmts)
	}
	if _, err := c.Execute(ctx, stmts[0], 1); err != nil {
		if strings.Contains(err.Error(), "ORA-29538") || strings.Contains(err.Error(), "ORA-00922") || strings.Contains(err.Error(), "ORA-00901") {
			t.Skip("no Java in this database: " + CleanError(err))
		}
		t.Fatal(CleanError(err))
	}
	defer c.Execute(ctx, `drop java source "SqlcliTestHello"`, 1)
	objs, err := c.SchemaObjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range objs {
		if o.Type == "JAVA SOURCE" && o.Name == "SqlcliTestHello" {
			found = true
		}
		if o.Type == "PACKAGE BODY" || o.Type == "LOB" {
			t.Fatalf("%s should not be listed", o.Type)
		}
	}
	if !found {
		t.Fatal("JAVA SOURCE not among the schema objects")
	}
	ddl, err := c.DDL(ctx, "JAVA SOURCE", "SqlcliTestHello")
	// the source keeps its lines
	if err != nil || !strings.Contains(ddl, "public class SqlcliTestHello {\n  public static String hi() {\n    int i = 1; i--;\n") ||
		!strings.HasPrefix(ddl, "CREATE OR REPLACE AND COMPILE JAVA SOURCE NAMED ") || !strings.HasSuffix(ddl, "}\n/\n") {
		t.Fatalf("DDL: %v\n%s", err, ddl)
	}
	// the DDL runs again as it is (OR REPLACE)
	again := SplitStatements(ddl)
	if len(again) != 1 {
		t.Fatalf("DDL splits into %d statements:\n%s", len(again), ddl)
	}
	if _, err := c.Execute(ctx, again[0], 1); err != nil {
		t.Fatalf("re-run DDL: %s\n%s", CleanError(err), again[0])
	}
}
