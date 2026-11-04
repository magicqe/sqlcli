package db

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestLineCommandsSplit(t *testing.T) {
	got := SplitStatements("desc emp\nselect * from emp\nexec dbms_output.put_line('a;b')\nDESCRIBE men.testpkg;\nselect 1 from dual;")
	// "desc emp" ends at its line; "select * from emp" has no ';' and no blank
	// line, so it continues on the next lines until a ';' (as in SQL*Plus)
	want := []string{"desc emp", "select * from emp\nexec dbms_output.put_line('a;b')\nDESCRIBE men.testpkg;", "select 1 from dual;"}
	// a line command after a finished statement:
	got2 := SplitStatements("select * from emp;\nexec p\ndesc emp")
	if !reflect.DeepEqual(got2, []string{"select * from emp;", "exec p", "desc emp"}) {
		t.Fatalf("after a statement: %#v", got2)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if b := execAsBlock("exec pkg.proc(1, 'x');"); b != "begin pkg.proc(1, 'x'); end;" {
		t.Fatalf("exec: %q", b)
	}
}

func TestColumnType(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"VARCHAR2", "30", "", "", "30", "B"}, "VARCHAR2(30)"},
		{[]string{"VARCHAR2", "120", "", "", "30", "C"}, "VARCHAR2(30 CHAR)"},
		{[]string{"NUMBER", "22", "10", "2", "", ""}, "NUMBER(10,2)"},
		{[]string{"NUMBER", "22", "5", "0", "", ""}, "NUMBER(5)"},
		{[]string{"NUMBER", "22", "", "0", "", ""}, "NUMBER(38)"},
		{[]string{"NUMBER", "22", "", "", "", ""}, "NUMBER"},
		{[]string{"DATE", "7", "", "", "", ""}, "DATE"},
		{[]string{"TIMESTAMP(6)", "11", "", "6", "", ""}, "TIMESTAMP(6)"},
		{[]string{"RAW", "16", "", "", "", ""}, "RAW(16)"},
	}
	for _, c := range cases {
		if got := columnType(c.args[0], c.args[1], c.args[2], c.args[3], c.args[4], c.args[5]); got != c.want {
			t.Errorf("%v: %q, want %q", c.args, got, c.want)
		}
	}
}

func TestIntegrationDescribe(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	defer c.Execute(ctx, "drop table sqlcli_test_desc purge", 1)
	defer c.Execute(ctx, "drop package sqlcli_test_pkg4", 1)
	for _, s := range SplitStatements(`create table sqlcli_test_desc (
  id number(10,2) not null, name varchar2(30 char), born date, n integer, note varchar2(20));
create or replace package sqlcli_test_pkg4 as
  function f (a number, b varchar2 default 'x') return varchar2;
  procedure p;
  procedure q (r out number);
end;`) {
		if _, err := c.Execute(ctx, s, 1); err != nil {
			t.Fatal(CleanError(err))
		}
	}

	r, err := c.Execute(ctx, "desc sqlcli_test_desc", 10)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	want := [][]string{
		{"ID", "NOT NULL", "NUMBER(10,2)"}, {"NAME", Null, "VARCHAR2(30 CHAR)"}, {"BORN", Null, "DATE"},
		{"N", Null, "NUMBER(38)"}, {"NOTE", Null, "VARCHAR2(20)"},
	}
	if !reflect.DeepEqual(r.Rows, want) || !r.IsQuery || !strings.HasPrefix(r.Message, "TABLE ") {
		t.Fatalf("table: %v %q", r.Rows, r.Message)
	}

	r, err = c.Execute(ctx, "DESCRIBE dual;", 10) // public synonym -> SYS.DUAL
	if err != nil || len(r.Rows) != 1 || r.Rows[0][0] != "DUMMY" || r.Rows[0][2] != "VARCHAR2(1)" {
		t.Fatalf("dual: %v %v", r, err)
	}

	r, err = c.Execute(ctx, "desc sqlcli_test_pkg4", 10)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	t.Logf("%s", r.Message)
	for _, row := range r.Rows {
		t.Logf("  %q", row)
	}
	if r.Rows[0][0] != "FUNCTION F" || r.Rows[0][2] != "RETURN VARCHAR2" ||
		r.Rows[1][1] != "A" || r.Rows[1][2] != "NUMBER" || r.Rows[2][1] != "B" || r.Rows[2][4] != "yes" ||
		r.Rows[3][0] != "PROCEDURE P" || r.Rows[4][0] != "PROCEDURE Q" || r.Rows[5][1] != "R" || r.Rows[5][3] != "OUT" {
		t.Fatalf("package rows: %q", r.Rows)
	}

	r, err = c.Execute(ctx, "desc dbms_output", 100)
	if err != nil || !strings.Contains(r.Message, "PACKAGE SYS.DBMS_OUTPUT") {
		t.Fatalf("dbms_output: %v %v", r, err)
	}

	r, err = c.Execute(ctx, "desc sqlcli_test_pkg4.f", 10) // not a table or package name
	if err == nil {
		t.Logf("desc pkg.f: %v", r.Message)
	}
	if _, err = c.Execute(ctx, "desc no_such_object_xyz", 10); err == nil || !strings.Contains(err.Error(), "ORA-04043") {
		t.Fatalf("missing object: %v", err)
	}

	r, err = c.Execute(ctx, "exec dbms_output.put_line('from exec')", 1)
	if err != nil || len(r.Output) != 1 || r.Output[0] != "from exec" {
		t.Fatalf("exec: %v %v", r, err)
	}
	_ = c.Rollback()
}
