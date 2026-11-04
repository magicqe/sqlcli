package script

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sqlcli/internal/db"
)

func expand(t *testing.T, text string, env *Env, answers map[string]string) ([]Step, *Env, []Var) {
	t.Helper()
	steps, env2, missing, err := Expand(db.SplitStatements(text), env, "", answers)
	if err != nil {
		t.Fatal(err)
	}
	return steps, env2, missing
}

func sqls(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Kind == SQL {
			out = append(out, s.Text)
		}
	}
	return out
}

func TestDefineAndSubstitution(t *testing.T) {
	env := NewEnv(true)
	steps, env2, missing := expand(t, `define tab = emp
define who = 'Tom & Jerry'
select * from &tab where name = '&who' and x = &tab._id;
select 'A & B' -- a comment with &nothing stays
  from /* &nor_this */ dual;`, env, nil)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	want := []string{"select * from emp where name = 'Tom & Jerry' and x = emp_id;", "select 'A & B' -- a comment with &nothing stays\n  from /* &nor_this */ dual;"}
	if !reflect.DeepEqual(sqls(steps), want) {
		t.Fatalf("sql: %q", sqls(steps))
	}
	if env2.Defines["TAB"] != "emp" || len(env.Defines) != 0 {
		t.Fatal("DEFINE should go to the returned env only")
	}

	// undefined variables are asked for: & once per run, && kept
	_, _, missing = expand(t, "select &a, &&b, &a from dual", NewEnv(true), nil)
	if !reflect.DeepEqual(missing, []Var{{Name: "A"}, {Name: "B", Keep: true}}) {
		t.Fatalf("missing: %+v", missing)
	}
	steps, env2, missing = expand(t, "select &a, &&b, &a from dual", NewEnv(true), map[string]string{"A": "1", "B": "2"})
	if len(missing) != 0 || sqls(steps)[0] != "select 1, 2, 1 from dual" || env2.Defines["B"] != "2" || env2.Defines["A"] != "" {
		t.Fatalf("answers: %q %v", sqls(steps), env2.Defines)
	}

	// SET DEFINE OFF leaves & alone
	steps, _, missing = expand(t, "set define off\nselect '&x' from dual;", NewEnv(true), nil)
	if len(missing) != 0 || sqls(steps)[0] != "select '&x' from dual;" {
		t.Fatalf("define off: %q %v", sqls(steps), missing)
	}

	// DEFINE listing, UNDEFINE, PROMPT, REM, ignored commands
	steps, env2, _ = expand(t, "def b = 2\ndef a = 1\nundefine b\ndefine\nprompt it's done; really\nrem nothing\ncolumn x format a10", NewEnv(true), nil)
	var table *Step
	var outputs, infos []string
	for i := range steps {
		switch steps[i].Kind {
		case Table:
			table = &steps[i]
		case Output:
			outputs = append(outputs, steps[i].Text)
		case Info:
			infos = append(infos, steps[i].Text)
		}
	}
	if table == nil || !reflect.DeepEqual(table.Rows, [][]string{{"A", "1"}}) {
		t.Fatalf("define listing: %+v", table)
	}
	if !reflect.DeepEqual(outputs, []string{"it's done; really"}) || !strings.Contains(strings.Join(infos, "|"), "COLUMN has no effect") {
		t.Fatalf("outputs %q infos %q", outputs, infos)
	}
	if _, ok := env2.Defines["B"]; ok {
		t.Fatal("UNDEFINE")
	}
}

func TestAccept(t *testing.T) {
	_, _, missing := expand(t, "accept dept number prompt 'Department: '\nselect * from emp where deptno = &dept", NewEnv(true), nil)
	if len(missing) != 1 || missing[0].Name != "DEPT" || missing[0].Prompt != "Department: " || !missing[0].Keep {
		t.Fatalf("accept: %+v", missing)
	}
	steps, env2, missing := expand(t, "accept dept prompt 'Department: '\nselect &dept from dual", NewEnv(true), map[string]string{"DEPT": "10"})
	if len(missing) != 0 || sqls(steps)[0] != "select 10 from dual" || env2.Defines["DEPT"] != "10" {
		t.Fatalf("accept with answer: %q %+v", sqls(steps), missing)
	}
}

func TestScripts(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.sql"), []byte("prompt main &1\n@@sub/child 'x y'\nselect 'after child' from dual;\nexit\nselect 'never' from dual;"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "child.sql"), []byte("select '&1' from dual;\n@@grand"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "grand.sql"), []byte("select 'grand' from dual;"), 0o644)
	steps, _, missing, err := Expand([]string{"@" + filepath.Join(dir, "main") + " hello", "select 'next' from dual"}, NewEnv(true), "", nil)
	if err != nil || len(missing) != 0 {
		t.Fatalf("err %v missing %v", err, missing)
	}
	want := []string{"select 'x y' from dual;", "select 'grand' from dual;", "select 'after child' from dual;", "select 'next' from dual"}
	if !reflect.DeepEqual(sqls(steps), want) {
		t.Fatalf("sql: %q", sqls(steps))
	}
	if steps[0].Kind != Output || steps[0].Text != "main hello" {
		t.Fatalf("prompt with &1: %+v", steps[0])
	}
	// START works like @; errors name the file
	if _, _, _, err := Expand([]string{"start " + filepath.Join(dir, "nope.sql")}, NewEnv(true), "", nil); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("missing script: %v", err)
	}
	// a script calling itself stops with an error
	os.WriteFile(filepath.Join(dir, "loop.sql"), []byte("@@loop"), 0o644)
	if _, _, _, err := Expand([]string{"@" + filepath.Join(dir, "loop.sql")}, NewEnv(true), "", nil); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("recursion: %v", err)
	}
}
