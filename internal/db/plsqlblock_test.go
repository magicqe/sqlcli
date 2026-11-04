package db

import (
	"reflect"
	"strings"
	"testing"
)

// Each case: a PL/SQL unit followed directly (no "/") by a query. The unit
// must end at its last "END ...;" and the query must be a statement of its own.
func TestPLSQLUnitEnd(t *testing.T) {
	q := "select 1 from dual;"
	units := map[string]string{
		"user's package body": `CREATE OR REPLACE package body MEN.testpkg as

function call1 (v_num in number) return number is
begin
 return v_num+19;
end;

function call2 (v_txt in varchar2) return varchar2 is
begin
 return 'abcd: '||v_txt;
end;

end;`,
		"package spec": `create or replace package p authid definer as
  function f (a number := cast(1 as number)) return number;
  procedure q;
  type rec is record (a number);
  cursor c is select * from dual;
end p;`,
		"package body with init and nesting": `create or replace package body p as
  g number;
  function f (a number := cast(1 as number)) return number;   -- forward declaration
  procedure q is
    procedure inner is
    begin
      null;
    end inner;
  begin
    for i in 1 .. 3 loop
      if i = 2 then inner; end if;
    end loop;
    <<lbl>> loop exit; end loop lbl;
    g := case when g is null then 0 else g end;
    case g when 0 then null; else null; end case;
  exception when others then
    begin null; end;
  end q;
  function f (a number := cast(1 as number)) return number is
  begin
    return a;
  end;
begin
  g := 1;   -- initialisation section
end p;`,
		"standalone function with nested function": `create or replace function f return number is
  function g return number is begin return 1; end;
begin
  return g;
end;`,
		"procedure with dynamic SQL": `create procedure p is
begin
  execute immediate 'begin null; end;';
  execute immediate q'[select 'end;' from dual]';
end;`,
		"anonymous block with declare": `declare
  function g return number is begin return 1; end;
  x number;
begin
  x := g;
end;`,
		"anonymous block": `begin
  dbms_output.put_line('end;');
end;`,
		"trigger with referencing as": `create or replace trigger trg
  before insert on t referencing new as n old as o
  for each row
declare
  x number;
begin
  :n.id := 1;
end trg;`,
		"compound trigger": `create or replace trigger trg for insert on t compound trigger
  cnt number;
  before statement is
  begin
    cnt := 0;
  end before statement;
  after each row is
  begin
    cnt := cnt + 1;
  end after each row;
end trg;`,
		"type body": `create or replace type body tp as
  member function m return number is
  begin
    return 1;
  end;
end;`,
	}
	for name, unit := range units {
		got := SplitStatements(unit + "\n" + q)
		if !reflect.DeepEqual(got, []string{unit, q}) {
			t.Errorf("%s: %d statement(s):\n%s", name, len(got), strings.Join(got, "\n-----\n"))
		}
		// with a "/" line it is the same
		if got := SplitStatements(unit + "\n/\n" + q); !reflect.DeepEqual(got, []string{unit, q}) {
			t.Errorf("%s with /: %#v", name, got)
		}
	}

	// not followed: these still end at "/" (or a DDL line)
	for name, unit := range map[string]string{
		"type spec":          "create or replace type tp as object (a number);",
		"external procedure": "create procedure px as language java name 'X.run()';",
		"with function":      "with function f return number is begin return 1; end;\nselect f() from dual",
	} {
		if got := SplitStatements(unit + "\n/\n" + q); !reflect.DeepEqual(got, []string{unit, q}) {
			t.Errorf("%s: %#v", name, got)
		}
	}
}
