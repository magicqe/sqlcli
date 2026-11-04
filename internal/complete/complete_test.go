package complete

import (
	"reflect"
	"strings"
	"testing"
)

type fakeCat struct {
	objects []Item
	cols    map[TableRef][]string
	members map[TableRef][]Item
}

func (f fakeCat) Objects() []Item { return f.objects }
func (f fakeCat) Members(t TableRef) ([]Item, bool) {
	m, ok := f.members[t]
	return m, ok
}
func (f fakeCat) HasObject(n string) bool {
	for _, o := range f.objects {
		if o.Text == n {
			return true
		}
	}
	return false
}
func (f fakeCat) Columns(t TableRef) ([]string, bool) {
	c, ok := f.cols[t]
	return c, ok
}

var cat = fakeCat{
	objects: []Item{{"EMPLOYEES", KindTable}, {"EMP_VIEW", KindView}, {"DEPT", KindTable}, {"DUAL", KindPublic}, {"MixedCase", KindTable},
		{"TESTPKG", "package"}, {"DBMS_OUTPUT", KindPublic}},
	members: map[TableRef][]Item{
		{Name: "TESTPKG"}:               {{"CALL1", KindFunction}, {"CALL2", KindFunction}, {"RESET", KindProcedure}},
		{Owner: "MEN", Name: "TESTPKG"}: {{"CALL1", KindFunction}, {"CALL2", KindFunction}, {"RESET", KindProcedure}},
	},
	cols: map[TableRef][]string{
		{Name: "EMPLOYEES"}:         {"EMP_ID", "NAME", "DEPT_ID", "SALARY"},
		{Name: "DEPT"}:              {"DEPT_ID", "DNAME"},
		{Owner: "HR", Name: "JOBS"}: {"JOB_ID", "TITLE"},
	},
}

func texts(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

func complete(t *testing.T, text string) Result {
	t.Helper()
	// | marks the cursor
	i := strings.Index(text, "|")
	before := text[:i]
	row := strings.Count(before, "\n")
	col := len([]rune(before[strings.LastIndex(before, "\n")+1:]))
	return Complete(strings.Split(strings.Replace(text, "|", "", 1), "\n"), row, col, cat)
}

func TestTableRefs(t *testing.T) {
	got := TableRefs(`select * from employees e, hr.jobs j
	  join dept d on d.dept_id = e.dept_id
	  left join (select 1 from dual) x on 1=1
	  where e.salary > 0`)
	want := map[string]TableRef{
		"EMPLOYEES": {Name: "EMPLOYEES"}, "E": {Name: "EMPLOYEES"},
		"JOBS": {Owner: "HR", Name: "JOBS"}, "J": {Owner: "HR", Name: "JOBS"},
		"DEPT": {Name: "DEPT"}, "D": {Name: "DEPT"},
		"DUAL": {Name: "DUAL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// AS alias, UPDATE, INSERT INTO, and keywords not taken as aliases
	got = TableRefs("update employees set x = 1")
	if _, ok := got["SET"]; ok || got["EMPLOYEES"].Name != "EMPLOYEES" {
		t.Fatalf("update: %v", got)
	}
	got = TableRefs("insert into dept (dept_id) values (1)")
	if got["DEPT"].Name != "DEPT" || len(got) != 1 {
		t.Fatalf("insert: %v", got)
	}
	got = TableRefs("select * from employees as emp where 1=1")
	if got["EMP"].Name != "EMPLOYEES" {
		t.Fatalf("as alias: %v", got)
	}
}

func TestCompleteQualified(t *testing.T) {
	r := complete(t, "select e.| from employees e")
	if !reflect.DeepEqual(texts(r.Items), []string{"EMP_ID", "NAME", "DEPT_ID", "SALARY"}) {
		t.Fatalf("e.: %v", texts(r.Items))
	}
	r = complete(t, "select e.sa| from employees e")
	if !reflect.DeepEqual(texts(r.Items), []string{"SALARY"}) || r.Prefix != "sa" {
		t.Fatalf("e.sa: %+v", r)
	}
	r = complete(t, "select j.| from hr.jobs j")
	if !reflect.DeepEqual(texts(r.Items), []string{"JOB_ID", "TITLE"}) {
		t.Fatalf("owner-qualified table: %v", texts(r.Items))
	}
	// table name used directly as qualifier
	r = complete(t, "select dept.d| from dept")
	if !reflect.DeepEqual(texts(r.Items), []string{"DEPT_ID", "DNAME"}) {
		t.Fatalf("dept.d: %v", texts(r.Items))
	}
	// unknown columns: ask the caller to load them
	r = complete(t, "select x.| from locations x")
	if len(r.Items) != 0 || len(r.Need) != 1 || r.Need[0].Name != "LOCATIONS" {
		t.Fatalf("need: %+v", r)
	}
}

func TestCompleteUnqualified(t *testing.T) {
	r := complete(t, "select d| from employees e")
	got := texts(r.Items)
	// columns of the statement's tables first, then tables, then keywords ...
	if got[0] != "DEPT_ID" || !contains(got, "DEPT") || !contains(got, "DELETE") || !contains(got, "DECODE") || !contains(got, "DATE") || !contains(got, "DUAL") {
		t.Fatalf("d: %v", got)
	}
	if idx(got, "DEPT_ID") > idx(got, "DEPT") || idx(got, "DEPT") > idx(got, "DELETE") || idx(got, "DELETE") > idx(got, "DECODE") {
		t.Fatalf("ranking: %v", got)
	}
	r = complete(t, "sel|")
	if texts(r.Items)[0] != "SELECT" || r.Start != 0 {
		t.Fatalf("sel: %+v", r)
	}
	// multi-line statement, cursor on the second line
	r = complete(t, "select *\n  from emp|")
	if !reflect.DeepEqual(texts(r.Items)[:2], []string{"EMPLOYEES", "EMP_VIEW"}) {
		t.Fatalf("emp: %v", texts(r.Items))
	}
	// a half-typed table name doesn't ask for columns; a known one does
	if r = complete(t, "select | from emp"); len(r.Need) != 0 {
		t.Fatalf("half-typed name requested columns: %v", r.Need)
	}
	r = Complete([]string{"select  from emp_view"}, 0, 7, cat)
	if len(r.Need) != 1 || r.Need[0].Name != "EMP_VIEW" {
		t.Fatalf("known view should request columns: %v", r.Need)
	}
	// only the statement under the cursor counts
	r = complete(t, "select * from dept;\nselect na| from employees")
	if contains(texts(r.Items), "DNAME") || !contains(texts(r.Items), "NAME") {
		t.Fatalf("statement scope: %v", texts(r.Items))
	}
}

func TestInsertCase(t *testing.T) {
	for _, c := range []struct {
		it     Item
		prefix string
		want   string
	}{
		{Item{"SELECT", KindKeyword}, "sel", "select"},
		{Item{"SELECT", KindKeyword}, "SEL", "SELECT"},
		{Item{"SELECT", KindKeyword}, "Sel", "SELECT"},
		{Item{"EMPLOYEES", KindTable}, "emp", "employees"},
		{Item{"EMPLOYEES", KindTable}, "", "EMPLOYEES"},
		{Item{"MixedCase", KindTable}, "mix", `"MixedCase"`},
		{Item{"MY COL", KindColumn}, "", `"MY COL"`},
	} {
		if got := Insert(c.it, c.prefix); got != c.want {
			t.Errorf("Insert(%v, %q) = %q, want %q", c.it, c.prefix, got, c.want)
		}
	}
}

func contains(s []string, x string) bool { return idx(s, x) >= 0 }
func idx(s []string, x string) int {
	for i, v := range s {
		if v == x {
			return i
		}
	}
	return -1
}

func TestCompletePackageMembers(t *testing.T) {
	r := complete(t, "select testpkg.| from dual")
	if strings.Join(texts(r.Items), ",") != "CALL1,CALL2,RESET" {
		t.Fatalf("testpkg.: %v", texts(r.Items))
	}
	r = complete(t, "select testpkg.ca| from dual")
	if strings.Join(texts(r.Items), ",") != "CALL1,CALL2" || r.Items[0].Kind != KindFunction {
		t.Fatalf("testpkg.ca: %+v", r.Items)
	}
	r = complete(t, "select men.testpkg.c| from dual")
	if strings.Join(texts(r.Items), ",") != "CALL1,CALL2" {
		t.Fatalf("owner-qualified: %v", texts(r.Items))
	}
	r = complete(t, "begin dbms_output.| end;") // members not loaded yet: ask for them
	if len(r.NeedMembers) != 1 || r.NeedMembers[0].Name != "DBMS_OUTPUT" {
		t.Fatalf("need members: %+v", r)
	}
	r = complete(t, "select nothing_known.| from dual") // unknown name: no member lookup
	if len(r.NeedMembers) != 0 {
		t.Fatalf("unknown name asked for members: %+v", r.NeedMembers)
	}
	r = complete(t, "select e.| from employees e") // an alias still lists columns only
	if len(r.NeedMembers) != 0 || texts(r.Items)[0] != "EMP_ID" {
		t.Fatalf("alias: %+v", r)
	}
}
