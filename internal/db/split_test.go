package db

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitStatements(t *testing.T) {
	script := `select 1 from dual;
select 2
  from dual

begin
  null;
  dbms_output.put_line('x');
end;
/
-- comment
update t set a = 1 where b = 2;
create or replace procedure p is
begin
  null;
end;
/`
	got := SplitStatements(script)
	want := []string{
		"select 1 from dual;",
		"select 2\n  from dual",
		"begin\n  null;\n  dbms_output.put_line('x');\nend;",
		"-- comment\nupdate t set a = 1 where b = 2;",
		"create or replace procedure p is\nbegin\n  null;\nend;",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"select 1 from dual;":    "select 1 from dual",
		"begin null; end;\n/":    "begin null; end;",
		"  -- x\nDELETE from t;": "-- x\nDELETE from t",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if FirstKeyword("/* c */ -- d\n with x as (select 1 from dual) select * from x") != "WITH" {
		t.Error("FirstKeyword")
	}
}

func TestStatementAtCursor(t *testing.T) {
	script := `select * from netlist;
select * from nsm;

select a,
       b
  from t

select 1 from dual; select 'x;y' from dual; -- c;
begin
  null;

  null;
end;
/
`
	cases := []struct {
		row, col int
		want     string
	}{
		{0, 0, "select * from netlist;"},  // the reported bug
		{0, 22, "select * from netlist;"}, // just after the ';'
		{1, 5, "select * from nsm;"},
		{2, 0, "select * from nsm;"}, // blank line: statement above
		{3, 0, "select a,\n       b\n  from t"},
		{5, 3, "select a,\n       b\n  from t"},
		{7, 3, "select 1 from dual;"},
		{7, 25, "select 'x;y' from dual;"},         // ';' inside a string is not a separator
		{10, 0, "begin\n  null;\n\n  null;\nend;"}, // blank line inside PL/SQL
		{12, 1, "begin\n  null;\n\n  null;\nend;"},
	}
	for _, c := range cases {
		got, ok := StatementAtCursor(script, c.row, c.col)
		if !ok || got.Text != c.want {
			t.Errorf("cursor %d,%d: got %q, want %q", c.row, c.col, got.Text, c.want)
		}
	}
	if _, ok := StatementAtCursor("  \n\n", 0, 0); ok {
		t.Error("empty script returned a statement")
	}
}

func TestSplitComments(t *testing.T) {
	got := SplitStatements("/* a; b */ select 1 from dual;\nselect 2 -- x;\nfrom dual;")
	want := []string{"/* a; b */ select 1 from dual;", "select 2 -- x;\nfrom dual;"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitWithFunction(t *testing.T) {
	script := "with function f return number is\nbegin\n  return 1;\nend;\nselect f() from dual;\n/\nselect 2 from dual;"
	got := SplitStatements(script)
	want := []string{"with function f return number is\nbegin\n  return 1;\nend;\nselect f() from dual;", "select 2 from dual;"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if n := Normalize(got[0]); !strings.HasSuffix(n, "from dual") {
		t.Fatalf("Normalize kept the final ';': %q", n)
	}
}
