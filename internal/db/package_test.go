package db

import (
	"context"
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
