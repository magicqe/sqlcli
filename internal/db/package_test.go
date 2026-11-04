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
