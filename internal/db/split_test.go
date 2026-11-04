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
		"update t set a = 1 where b = 2;", // the comment line above it is skipped
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
	want := []string{"select 1 from dual;", "select 2 -- x;\nfrom dual;"}
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

func TestCommentsSkipped(t *testing.T) {
	script := `select * from a;
-- select * from b;
/*
 old query;

 still old
*/
select * from c;

-- just a note

/* header
   text */ select 1 from dual;
select /*+ index(t i) */ x,
       -- inline comment stays
       y from t -- trailing
-- more trailing
/

begin
  /* comment in PL/SQL */ null;
end;
/
-- the end`
	got := SplitStatements(script)
	want := []string{
		"select * from a;",
		"select * from c;",
		"select 1 from dual;",
		"select /*+ index(t i) */ x,\n       -- inline comment stays\n       y from t",
		"begin\n  /* comment in PL/SQL */ null;\nend;",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	// a script of comments only has no statements
	if got := SplitStatements("-- a\n/* b\n\n c */\n-- d;"); len(got) != 0 {
		t.Fatalf("comments only: %#v", got)
	}
	// a comment line right above a statement belongs to it (F5 runs the statement)
	if st, _ := StatementAtCursor("-- get the count\nselect count(*) from t", 0, 3); st.Text != "select count(*) from t" {
		t.Fatalf("cursor on leading comment: %q", st.Text)
	}
	// a stand-alone comment block runs the statement above it, never the comment
	if st, _ := StatementAtCursor("select 1 from dual;\n\n-- note\n", 2, 2); st.Text != "select 1 from dual;" {
		t.Fatalf("cursor on lone comment: %q", st.Text)
	}
}

func TestInComment(t *testing.T) {
	script := "select 1 from dual; -- note\n" + // 0
		"/* block\n" + // 1
		"   still */ select 'a -- b /* c' from dual\n" + // 2
		"-- whole line\n" + // 3
		"select 2 from dual"
	cases := []struct {
		row, col int
		want     bool
	}{
		{0, 0, false},  // select
		{0, 19, false}, // the space after ';'
		{0, 20, true},  // on "--"
		{0, 24, true},  // in the note
		{0, 27, true},  // end of the line after "--"
		{1, 0, true},   // on "/*"
		{1, 1, true},   // on the '*' of "/*"
		{1, 8, true},   // end of a line inside the block
		{2, 3, true},   // inside the block
		{2, 10, true},  // on the closing '/'
		{2, 11, false}, // just after "*/"
		{2, 20, false}, // "--" inside a string
		{2, 26, false}, // "/*" inside a string
		{3, 5, true},
		{4, 3, false},
	}
	for _, c := range cases {
		if got := InComment(script, c.row, c.col); got != c.want {
			t.Errorf("(%d,%d) %q: got %v, want %v", c.row, c.col, strings.Split(script, "\n")[c.row], got, c.want)
		}
	}
}

func TestInCommentWholeCommentLines(t *testing.T) {
	script := "insert into t values(1,'abc');\n" + // 0
		"   -- indented comment\n" + // 1
		"/* more coments\n" + // 2
		"line\n" + // 3
		"*/\n" + // 4
		"insert into t values(2,'klm') -- x\n" + // 5
		"\n" + // 6
		"/* a */ select 1 from dual /* b */" // 7
	cases := []struct {
		row, col int
		want     bool
	}{
		{0, 30, false}, // end of the first insert
		{1, 0, true},   // indentation before "--"
		{4, 2, true},   // just after the closing "*/"
		{5, 0, false},  // second insert
		{5, 29, false}, // the space before the trailing comment
		{5, 33, true},  // in the trailing comment
		{6, 0, false},  // blank line: not a comment (runs the statement above)
		{7, 8, false},  // code between two comments
		{7, 3, true},   // inside /* a */
	}
	for _, c := range cases {
		if got := InComment(script, c.row, c.col); got != c.want {
			t.Errorf("(%d,%d) %q: got %v, want %v", c.row, c.col, strings.Split(script, "\n")[c.row], got, c.want)
		}
	}
}

func TestSplitPackageSpecAndBody(t *testing.T) {
	spec := "create or replace package p as\n  procedure x;\nend;"
	body := "create or replace package body p as\n  procedure x is\n  begin\n    null;\n  end;\nend;"
	want := []string{spec, body}
	for name, script := range map[string]string{
		"slash":        spec + "\n/\n" + body + "\n/",
		"blank line":   spec + "\n\n" + body,
		"no separator": spec + "\n" + body,
		"comment":      spec + "\n-- the body\n" + body + "\n/",
	} {
		if got := SplitStatements(script); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %#v", name, got)
		}
	}
	// DDL inside a string (dynamic SQL) doesn't split a block
	plsql := "begin\n  execute immediate '\ncreate table t (x number)';\nend;"
	if got := SplitStatements(plsql + "\n/"); len(got) != 1 || got[0] != plsql {
		t.Fatalf("dynamic SQL: %#v", got)
	}
	// a procedure followed by a plain DDL statement
	proc := "create or replace procedure q is\nbegin\n  null;\nend;"
	if got := SplitStatements(proc + "\ndrop table x;"); !reflect.DeepEqual(got, []string{proc, "drop table x;"}) {
		t.Fatalf("procedure then drop: %#v", got)
	}
}
