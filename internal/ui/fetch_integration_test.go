package ui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
)

// newTestModel connects to the integration database (see internal/db tests).
func newTestModel(t *testing.T, fetchAll bool) *Model {
	t.Helper()
	cs := os.Getenv("SQLCLI_TEST_CONNECT")
	if cs == "" {
		t.Skip("SQLCLI_TEST_CONNECT not set")
	}
	cfg := config.Default()
	cfg.Query.MaxRows = 1000
	cfg.Query.FetchAll = fetchAll
	cc := config.Connection{Name: "it", User: os.Getenv("SQLCLI_TEST_USER"), PasswordEnv: "SQLCLI_TEST_PASSWORD", ConnectString: cs}
	cfg.Connections = []config.Connection{cc}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := db.Open(ctx, cc, db.Options{LibDir: os.Getenv("ORACLE_HOME"), DBMSOutput: true})
	if err != nil {
		t.Fatal(db.CleanError(err))
	}
	m := New(cfg, nil, "it")
	m.conn = conn
	t.Cleanup(m.CloseAll) // every tab's session
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 35})
	return m
}

// drain runs cmd and every command produced by the resulting updates,
// skipping the status-bar ticker.
func drain(m *Model, cmd tea.Cmd) {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil, tickMsg:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func key(code rune, mod ...tea.KeyMod) tea.KeyPressMsg {
	k := tea.KeyPressMsg{Code: code}
	if len(mod) > 0 {
		k.Mod = mod[0]
	}
	return k
}

func press(m *Model, k tea.KeyPressMsg) { _, cmd := m.Update(k); drain(m, cmd) }

func run(m *Model, sql string) {
	m.ed.SetText(sql)
	m.focus = focusEditor
	press(m, key(tea.KeyF5))
}

func TestUIFetchOnScrollAndEnd(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "select level n, 'r'||level t from dual connect by level <= 2500")
	r := m.res.res
	if r == nil || len(r.Rows) != 1000 || !r.More {
		t.Fatalf("after F5: %+v", m.res)
	}
	if !strings.Contains(m.status, "more available") {
		t.Errorf("status: %q", m.status)
	}
	m.focus = focusResults
	for i := 0; i < 200 && len(r.Rows) == 1000; i++ { // page down until a batch is fetched
		press(m, key(tea.KeyPgDown))
	}
	if len(r.Rows) != 2000 || !r.More {
		t.Fatalf("after scrolling: %d rows more=%v", len(r.Rows), r.More)
	}
	if !strings.Contains(m.res.summary(), "/2000+") {
		t.Errorf("summary: %q", m.res.summary())
	}
	press(m, key(tea.KeyEnd))
	if len(r.Rows) != 2500 || r.More || m.res.row != 2499 || m.running {
		t.Fatalf("after End: %d rows more=%v row=%d running=%v", len(r.Rows), r.More, m.res.row, m.running)
	}
	if r.Rows[2499][0] != "2500" || m.status != "2500 row(s) selected." {
		t.Fatalf("last row %v status %q", r.Rows[2499], m.status)
	}
}

func TestUIWheelFetches(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "select level from dual connect by level <= 1500")
	r := m.res.res
	wheelY := m.h - 2 - m.res.h // inside the results box
	for i := 0; i < 400 && r.More; i++ {
		_, cmd := m.Update(tea.MouseWheelMsg{X: 60, Y: wheelY, Button: tea.MouseWheelDown})
		drain(m, cmd)
	}
	if len(r.Rows) != 1500 || r.More {
		t.Fatalf("wheel: %d rows more=%v", len(r.Rows), r.More)
	}
}

func TestUIFetchAllOption(t *testing.T) {
	m := newTestModel(t, true)
	run(m, "select level from dual connect by level <= 12345")
	r := m.res.res
	if len(r.Rows) != 12345 || r.More || m.running {
		t.Fatalf("fetch_all: %d rows more=%v", len(r.Rows), r.More)
	}
}

func TestUICtrlCStopsFetchAll(t *testing.T) {
	m := newTestModel(t, false)
	run(m, `select a.n, rpad('x', 300, 'x') pad
	          from (select level n from dual connect by level <= 4000) a,
	               (select level n from dual connect by level <= 4000) b`) // 16M rows
	m.focus = focusResults
	_, cmd := m.Update(key(tea.KeyEnd))

	msgs := make(chan tea.Msg, 16)
	var spawn func(tea.Cmd)
	spawn = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, sc := range b {
					spawn(sc)
				}
				return
			}
			msgs <- msg
		}()
	}
	spawn(cmd)
	stopAt := time.After(400 * time.Millisecond)
	deadline := time.After(30 * time.Second)
	for m.fetching {
		select {
		case msg := <-msgs:
			if _, ok := msg.(tickMsg); ok {
				continue
			}
			_, next := m.Update(msg)
			spawn(next)
		case <-stopAt:
			t.Logf("Ctrl-C at %d rows", len(m.res.res.Rows))
			m.Update(key('c', tea.ModCtrl))
		case <-deadline:
			t.Fatal("fetch did not stop")
		}
	}
	n := len(m.res.res.Rows)
	t.Logf("stopped: %q", m.status)
	if !strings.HasPrefix(m.status, "Fetch stopped at") || m.res.res.More || n < 1000 || n >= 16000000 {
		t.Fatalf("status %q rows %d more %v", m.status, n, m.res.res.More)
	}
	// session still usable
	run(m, "select 'ok' from dual")
	if m.res.res == nil || m.res.res.Rows[0][0] != "ok" {
		t.Fatalf("after stop: %+v", m.res)
	}
}

func TestUIRecordFetches(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "select level n from dual connect by level <= 2500")
	m.focus = focusResults
	press(m, key(tea.KeyEnter))
	if m.mode != modeRecord {
		t.Fatal("no record view")
	}
	// End in the record view fetches everything and shows the last record
	press(m, key(tea.KeyEnd))
	r := m.res.res
	if len(r.Rows) != 2500 || r.More || m.res.row != 2499 || m.mode != modeRecord {
		t.Fatalf("End: rows=%d more=%v record=%d mode=%v", len(r.Rows), r.More, m.res.row+1, m.mode)
	}
	if !strings.Contains(screen(m), "Record 2500 of 2500") {
		t.Fatalf("screen:\n%s", screen(m))
	}

	// stepping down through the first batch fetches the next one
	press(m, key(tea.KeyEscape))
	run(m, "select level n from dual connect by level <= 1500")
	m.focus = focusResults
	press(m, key(tea.KeyEnter))
	r = m.res.res
	for i := 0; i < 1100 && r.More; i++ {
		press(m, key(tea.KeyRight))
	}
	if len(r.Rows) != 1500 || r.More {
		t.Fatalf("Right: rows=%d more=%v", len(r.Rows), r.More)
	}
}

func TestUIRecordPageFetches(t *testing.T) {
	m := newTestModel(t, false)
	m.cfg.UI.RecordPage = 100
	run(m, "select level n from dual connect by level <= 1500")
	m.focus = focusResults
	press(m, key(tea.KeyEnter))
	r := m.res.res
	for i := 0; i < 20 && r.More; i++ {
		press(m, key(tea.KeyPgDown))
	}
	if len(r.Rows) != 1500 || r.More {
		t.Fatalf("PgDn: rows=%d more=%v", len(r.Rows), r.More)
	}
	if m.res.row%100 != 0 || m.res.row < 1000 {
		t.Fatalf("PgDn should move in steps of 100: row index %d", m.res.row)
	}
}

func TestUIRunFooter(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "select level from dual connect by level <= 2500")
	if s := screen(m); !strings.Contains(s, "✓ 1000+ rows · ") {
		t.Fatalf("query footer:\n%s", s)
	}
	m.focus = focusResults
	press(m, key(tea.KeyEnd))
	if !strings.Contains(screen(m), "✓ 2500 rows · ") {
		t.Fatal("footer should follow the fetched row count")
	}
	run(m, "select * from no_such_table_xyz")
	if !strings.Contains(screen(m), "✗ ORA-00942") {
		t.Fatal("error footer")
	}
	run(m, "begin null; end;")
	if !strings.Contains(screen(m), "✓ PL/SQL OK · ") {
		t.Fatal("PL/SQL footer")
	}
	press(m, key(tea.KeyF8))

	// Ctrl-C during a long statement
	m.ed.SetText("begin dbms_session.sleep(30); end;")
	m.focus = focusEditor
	_, cmd := m.Update(key(tea.KeyF5))
	done := make(chan tea.Msg, 1)
	go func() {
		for _, c := range cmd().(tea.BatchMsg) {
			if c == nil {
				continue
			}
			go func(c tea.Cmd) {
				if msg := c(); msg != nil {
					if _, ok := msg.(tickMsg); !ok {
						done <- msg
					}
				}
			}(c)
		}
	}()
	time.Sleep(700 * time.Millisecond)
	if !strings.Contains(screen(m), "● running ") {
		t.Fatalf("running footer:\n%s", screen(m))
	}
	m.Update(key('c', tea.ModCtrl))
	select {
	case msg := <-done:
		t.Logf("cancel error: %v", msg.(execDoneMsg).err)
		m.Update(msg)
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not finish")
	}
	if !strings.Contains(screen(m), "■ cancelled after ") {
		t.Fatalf("cancelled footer:\n%s", screen(m))
	}
}

func TestUICompletionFromDatabase(t *testing.T) {
	m := newTestModel(t, false)
	drain(m, m.loadObjects(false)) // as after connecting
	cat := m.catalogs[m.conn.Name]
	if cat == nil || len(cat.objects) == 0 {
		t.Fatal("objects not loaded")
	}
	m.ed.SetText("")
	m.focus = focusEditor
	typeText(m, "select * from du")
	got := popupTexts(m)
	if len(got) == 0 || !contains(got, "DUAL") {
		t.Fatalf("du: %v", got[:min(10, len(got))])
	}
	press(m, key(tea.KeyEscape))

	// columns are loaded from the database on demand
	m.ed.SetText("select  from dual d")
	m.ed.HandleKey("home", "")
	for i := 0; i < 7; i++ {
		m.ed.HandleKey("right", "")
	}
	typeText(m, "d.") // triggers loading DUAL's columns; drain delivers them
	if got := popupTexts(m); strings.Join(got, ",") != "DUMMY" {
		t.Fatalf("d.: %v", got)
	}
	press(m, key(tea.KeyTab))
	if m.ed.Text() != "select d.DUMMY from dual d" {
		t.Fatalf("inserted: %q", m.ed.Text())
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func TestUIScriptWithComments(t *testing.T) {
	m := newTestModel(t, false)
	m.ed.SetText(`-- daily check
select 1 from dual;
-- select * from not_there;
/*
  old query;
*/
select 'last' from dual;
-- the end`)
	m.focus = focusEditor
	press(m, key(tea.KeyF7))
	if m.res.err != "" || !m.res.hasGrid() || m.res.res.Rows[0][0] != "last" {
		t.Fatalf("script with comments failed: err=%q status=%q", m.res.err, m.status)
	}
	if !strings.HasPrefix(m.status, "2 statements OK") {
		t.Fatalf("status: %q", m.status)
	}
}

// The user's example: two inserts around a line comment and a block comment.
func TestUIInsertsAroundComments(t *testing.T) {
	m := newTestModel(t, false)
	run(m, "create private temporary table ora$ptt_stest (n number, s varchar2(10)) on commit preserve definition")
	defer m.conn.Execute(context.Background(), "drop table ora$ptt_stest", 1)
	buf := "insert into ora$ptt_stest values(1,'abc');\n-- comment\n/* more coments\nline\n*/\ninsert into ora$ptt_stest values(2,'klm')"
	lines := strings.Split(buf, "\n")
	for row, line := range lines {
		for _, col := range []int{0, len(line)} {
			m.ed.SetText(buf)
			m.ed.SetCursor(row, col)
			m.focus = focusEditor
			press(m, key(tea.KeyF5))
			want := "The cursor is in a comment: nothing to run"
			if row == 0 || row == 5 {
				want = "1 row(s) inserted."
			}
			if m.status != want {
				t.Fatalf("F5 on line %d col %d %q: %q, want %q", row+1, col, line, m.status, want)
			}
		}
	}
	press(m, key(tea.KeyF8))
	m.ed.SetText(buf)
	press(m, key(tea.KeyF7))
	run(m, "select n || s from ora$ptt_stest order by n")
	if m.res.err != "" || len(m.res.res.Rows) != 2 || m.res.res.Rows[0][0] != "1abc" || m.res.res.Rows[1][0] != "2klm" {
		t.Fatalf("F7: %v %q", m.res.res, m.res.err)
	}
	press(m, key(tea.KeyF8))
}

func TestUIPackageSpecAndBody(t *testing.T) {
	m := newTestModel(t, false)
	defer m.conn.Execute(context.Background(), "drop package sqlcli_test_pkg", 1)
	buf := `create or replace package sqlcli_test_pkg as
  procedure hello;
end;

create or replace package body sqlcli_test_pkg as
  procedure hello is
  begin
    dbms_output.put_line('hi');
  end;
end;`
	status := func(name string) string {
		r, _ := m.conn.Execute(context.Background(), "select status from user_objects where object_name = 'SQLCLI_TEST_PKG' and object_type = '"+name+"'", 1)
		if len(r.Rows) == 0 {
			return "missing"
		}
		return r.Rows[0][0]
	}

	// F5 on the spec creates only the spec; F5 on the body creates the body
	m.ed.SetText(buf)
	m.ed.SetCursor(1, 3)
	m.focus = focusEditor
	press(m, key(tea.KeyF5))
	if m.status != "PACKAGE SQLCLI_TEST_PKG created." {
		t.Fatalf("F5 on spec: %q", m.status)
	}
	m.ed.SetCursor(6, 3)
	press(m, key(tea.KeyF5))
	if m.status != "PACKAGE BODY SQLCLI_TEST_PKG created." || status("PACKAGE") != "VALID" || status("PACKAGE BODY") != "VALID" {
		t.Fatalf("F5 on body: %q spec=%s body=%s", m.status, status("PACKAGE"), status("PACKAGE BODY"))
	}

	// F7 creates both
	m.conn.Execute(context.Background(), "drop package sqlcli_test_pkg", 1)
	m.ed.SetText(buf)
	press(m, key(tea.KeyF7))
	if !strings.HasPrefix(m.status, "2 statements OK") || status("PACKAGE BODY") != "VALID" {
		t.Fatalf("F7: %q body=%s", m.status, status("PACKAGE BODY"))
	}

	// a body with an error: red status, errors in the results pane, ✗ in the footer
	m.ed.SetText(strings.Replace(buf, "dbms_output.put_line('hi')", "no_such_proc", 1))
	press(m, key(tea.KeyF7))
	txt := m.res.view(true)
	if !m.statusErr || !strings.Contains(m.status, "[2] PACKAGE BODY SQLCLI_TEST_PKG created with compilation errors.") ||
		!strings.Contains(txt, "Compilation errors") || !strings.Contains(txt, "4/5  PLS-00201") {
		t.Fatalf("compile errors: status %q\n%s", m.status, txt)
	}
	if !strings.Contains(screen(m), "✗ [2] PACKAGE BODY SQLCLI_TEST_PKG created with compilation errors") {
		t.Fatalf("footer:\n%s", screen(m))
	}
}

// The user's example: a package body followed directly by a query using it.
func TestUIPackageBodyThenSelect(t *testing.T) {
	m := newTestModel(t, false)
	defer m.conn.Execute(context.Background(), "drop package sqlcli_test_pkg2", 1)
	buf := `create or replace package sqlcli_test_pkg2 as
function call1 (v_num in number) return number;
function call2 (v_txt in varchar2) return varchar2;
end;

CREATE OR REPLACE package body sqlcli_test_pkg2 as

function call1 (v_num in number) return number is
begin
 return v_num+19;
end;

function call2 (v_txt in varchar2) return varchar2 is
begin
 return 'abcd: '||v_txt;
end;

end;

select sqlcli_test_pkg2.call1(1) a, sqlcli_test_pkg2.call2('x') b from dual;`
	m.ed.SetText(buf)
	m.focus = focusEditor
	press(m, key(tea.KeyF7))
	if m.res.err != "" || !m.res.hasGrid() || m.res.res.Rows[0][0] != "20" || m.res.res.Rows[0][1] != "abcd: x" {
		t.Fatalf("F7: status %q err %q", m.status, m.res.err)
	}
	if !strings.HasPrefix(m.status, "3 statements OK") {
		t.Fatalf("status: %q", m.status)
	}
	// F5 on the query runs only the query
	lines := strings.Split(buf, "\n")
	m.ed.SetText(buf)
	m.ed.SetCursor(len(lines)-1, 5)
	press(m, key(tea.KeyF5))
	if m.status != "1 row(s) selected." || m.res.res.Rows[0][0] != "20" {
		t.Fatalf("F5 on query: %q", m.status)
	}
	// F5 inside the body runs only the body
	m.ed.SetCursor(8, 2)
	press(m, key(tea.KeyF5))
	if m.status != "PACKAGE BODY SQLCLI_TEST_PKG2 created." {
		t.Fatalf("F5 in body: %q", m.status)
	}
}

func TestUICompletePackageMembers(t *testing.T) {
	m := newTestModel(t, false)
	defer m.conn.Execute(context.Background(), "drop package sqlcli_test_pkg3", 1)
	run(m, "create or replace package sqlcli_test_pkg3 as\n  function call1 (v number) return number;\n  procedure reset;\nend;")
	drain(m, m.loadObjects(true)) // the new package is in the catalog (as after the CREATE)
	m.ed.SetText("")
	m.focus = focusEditor
	typeText(m, "select sqlcli_test_pkg3.") // members load from the database
	got := popupTexts(m)
	if strings.Join(got, ",") != "CALL1,RESET" {
		t.Fatalf("members: %v", got)
	}
	if !strings.Contains(screen(m), "function") || !strings.Contains(screen(m), "procedure") {
		t.Fatal("kinds not shown")
	}
	typeText(m, "ca")
	press(m, key(tea.KeyTab))
	if m.ed.Text() != "select sqlcli_test_pkg3.call1" {
		t.Fatalf("inserted: %q", m.ed.Text())
	}
	// a standard package through its public synonym
	m.ed.SetText("")
	typeText(m, "begin dbms_output.put")
	if got := popupTexts(m); !contains(got, "PUT_LINE") || !contains(got, "PUT") {
		t.Fatalf("dbms_output.put: %v", got)
	}
}

func TestUIDescribeAndExec(t *testing.T) {
	m := newTestModel(t, false)
	m.ed.SetText("desc dual\nselect dummy from dual;\nexec dbms_output.put_line('hello')")
	m.focus = focusEditor
	press(m, key(tea.KeyF5)) // cursor on line 1: only the DESC
	if !m.res.hasGrid() || m.res.res.Columns[0] != "NAME" || m.res.res.Rows[0][0] != "DUMMY" || m.res.res.Rows[0][2] != "VARCHAR2(1)" {
		t.Fatalf("desc: %q %+v", m.status, m.res.res)
	}
	if !strings.Contains(m.status, "SYS.DUAL: 1 column(s)") {
		t.Fatalf("status %q", m.status)
	}
	m.ed.SetCursor(1, 3)
	press(m, key(tea.KeyF5))
	if m.res.res.Columns[0] != "DUMMY" {
		t.Fatalf("select after desc: %+v", m.res.res)
	}
	m.ed.SetCursor(2, 3)
	press(m, key(tea.KeyF5))
	if !strings.Contains(m.res.view(true), "hello") {
		t.Fatalf("exec output:\n%s", m.res.view(true))
	}
	press(m, key(tea.KeyF8))
}

func TestUIObjectTree(t *testing.T) {
	m := newTestModel(t, false)
	ctx := context.Background()
	defer m.conn.Execute(ctx, "drop table sqlcli_test_tree purge", 1)
	defer m.conn.Execute(ctx, "drop package sqlcli_test_pkg5", 1)
	run(m, "create table sqlcli_test_tree (id number(5) not null, name varchar2(10 char))")
	run(m, "create or replace package sqlcli_test_pkg5 as\n  function f return number;\n  procedure p;\nend;")
	drain(m, m.loadTree(true))
	tr := m.tree()
	find := func(typ, name string) (*treeNode, int) {
		nodes, _ := tr.visible()
		for i, n := range nodes {
			if n.objType == typ && (n.name == name || (name == "" && n.name == "")) {
				return n, i
			}
		}
		return nil, -1
	}
	g, gi := find("TABLE", "")
	if g == nil {
		t.Fatal("no Tables group")
	}
	m.focus = focusTree
	tr.sel = gi
	press(m, key(tea.KeyEnter))
	obj, oi := find("TABLE", "SQLCLI_TEST_TREE")
	if obj == nil {
		t.Fatal("table not in the tree")
	}
	tr.sel = oi
	press(m, key(tea.KeyEnter)) // drain loads the columns
	if len(obj.children) != 2 || obj.children[0].label != "ID" || obj.children[0].extra != "NUMBER(5) NOT NULL" || obj.children[1].extra != "VARCHAR2(10 CHAR)" {
		t.Fatalf("columns: %+v", obj.children)
	}
	press(m, key('s')) // select * from the table, in a new tab
	if len(m.tabs) != 2 || m.ed.Text() != "select * from SQLCLI_TEST_TREE;" || !m.res.hasGrid() || m.res.res.Columns[0] != "ID" {
		t.Fatalf("s: tabs %d %q %q %+v", len(m.tabs), m.ed.Text(), m.status, m.res.res)
	}
	m.focus = focusTree
	press(m, key('d'))
	if !m.res.hasGrid() || m.res.res.Rows[0][2] != "NUMBER(5)" {
		t.Fatalf("d: %q %+v", m.status, m.res.res)
	}

	g, gi = find("PACKAGE", "")
	m.focus = focusTree
	tr.sel = gi
	press(m, key(tea.KeyEnter))
	pkg, pi := find("PACKAGE", "SQLCLI_TEST_PKG5")
	tr.sel = pi
	press(m, key(tea.KeyEnter))
	if len(pkg.children) != 2 || pkg.children[0].label != "F" || pkg.children[0].extra != "function" || pkg.children[1].extra != "procedure" {
		t.Fatalf("members: %+v", pkg.children)
	}
	press(m, key(tea.KeyDown))
	press(m, key('i'))
	if !strings.HasPrefix(m.ed.Text(), "SQLCLI_TEST_PKG5.F") { // inserted at the cursor
		t.Fatalf("insert member: %q", m.ed.Text())
	}

	// DDL reloads the tree
	run(m, "drop package sqlcli_test_pkg5")
	if pkg, _ := find("PACKAGE", "SQLCLI_TEST_PKG5"); pkg != nil {
		if tr2 := m.tree(); tr2 == tr {
			t.Fatal("tree not reloaded after DDL")
		}
	}
}

func TestUIObjectDDL(t *testing.T) {
	m := newTestModel(t, false)
	ctx := context.Background()
	defer m.conn.Execute(ctx, "drop package sqlcli_test_pkg7", 1)
	run(m, "create or replace package sqlcli_test_pkg7 as\n  function f return number;\nend;")
	run(m, "create or replace package body sqlcli_test_pkg7 as\n  function f return number is begin return 7; end;\nend;")
	drain(m, m.loadTree(true))
	tr := m.tree()
	nodes, _ := tr.visible()
	for i, n := range nodes {
		if n.label == "Packages" {
			tr.sel = i
		}
	}
	m.focus = focusTree
	press(m, key(tea.KeyEnter))
	nodes, _ = tr.visible()
	for i, n := range nodes {
		if n.name == "SQLCLI_TEST_PKG7" {
			tr.sel = i
		}
	}
	press(m, key('d', tea.ModCtrl)) // drain: DDL, new tab, its connection
	if len(m.tabs) != 2 || m.tabIndex(m.tab) != 1 || m.conn == nil || m.conn.Name != "it" {
		t.Fatalf("new tab: tabs=%d active=%d conn=%v status=%q", len(m.tabs), m.tabIndex(m.tab), m.conn, m.status)
	}
	text := m.ed.Text()
	if !strings.Contains(text, "PACKAGE \"") || !strings.Contains(text, "PACKAGE BODY") || m.ddlName != "PACKAGE SQLCLI_TEST_PKG7" {
		t.Fatalf("DDL tab:\n%s", text)
	}
	if !strings.Contains(screen(m), "· DDL PACKAGE SQLCLI_TEST_PKG7") {
		t.Fatalf("title:\n%s", screen(m))
	}
	if m.tabs[0].conn.InTransaction() {
		t.Fatal("reading DDL opened a transaction")
	}
	// the DDL runs as it is in the new tab
	press(m, key(tea.KeyF7))
	if !strings.HasPrefix(m.status, "2 statements OK") {
		t.Fatalf("running the DDL: %q", m.status)
	}
	// ^K D offers the object's name as the file name
	ctrlK(m, 'd')
	if string(m.prompt.input) != "sqlcli_test_pkg7.sql" {
		t.Fatalf("save name: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEscape))
}

func TestUITreeSelectShowsResults(t *testing.T) {
	m := newTestModel(t, false)
	drain(m, m.loadTree(true))
	tr := m.tree()
	nodes, _ := tr.visible()
	for i, n := range nodes {
		if n.label == "Tables" {
			tr.sel = i
		}
	}
	m.focus = focusTree
	press(m, key(tea.KeyEnter))
	press(m, key(tea.KeyDown)) // first table
	m.showSQL = false
	press(m, key('s')) // a new tab with the select, run at once
	if len(m.tabs) != 2 || m.tabIndex(m.tab) != 1 || !strings.HasPrefix(m.ed.Text(), "select * from ") {
		t.Fatalf("s: tabs %d active %d text %q", len(m.tabs), m.tabIndex(m.tab), m.ed.Text())
	}
	if !m.showSQL || !m.showResults || m.focus != focusResults || !m.res.hasGrid() || m.conn == nil {
		t.Fatalf("s: sql=%v results=%v focus=%v status=%q", m.showSQL, m.showResults, m.focus, m.status)
	}
}

func TestUIEditCells(t *testing.T) {
	m := newTestModel(t, false)
	ctx := context.Background()
	defer m.conn.Execute(ctx, "drop table sqlcli_test_grid purge", 1)
	run(m, "create table sqlcli_test_grid (id number(5), name varchar2(20), amount number(8,2))")
	run(m, "insert into sqlcli_test_grid values (1, 'one', 1.5)")
	run(m, "insert into sqlcli_test_grid values (2, 'two', 2.5)")
	press(m, key(tea.KeyF9))
	drain(m, m.loadTree(true))
	tr := m.tree()
	nodes, _ := tr.visible()
	for i, n := range nodes {
		if n.label == "Tables" {
			tr.sel = i
		}
	}
	m.focus = focusTree
	press(m, key(tea.KeyEnter))
	nodes, _ = tr.visible()
	for i, n := range nodes {
		if n.name == "SQLCLI_TEST_GRID" {
			tr.sel = i
		}
	}
	press(m, key('e')) // a new tab: select rowid, the columns ... (editable)
	want := "select rowid,\n       ID,\n       NAME,\n       AMOUNT\n  from SQLCLI_TEST_GRID;"
	if len(m.tabs) != 2 || m.ed.Text() != want || m.focus != focusResults || m.res.res == nil || m.res.res.Columns[0] != "ROWID" {
		t.Fatalf("e: tabs %d text %q status %q", len(m.tabs), m.ed.Text(), m.status)
	}
	value := func() string {
		r, _ := m.conn.Execute(ctx, "select name from sqlcli_test_grid where id = 1", 1)
		return r.Rows[0][0]
	}
	// row with ID 1, column NAME
	for i, row := range m.res.res.Rows {
		if row[1] == "1" {
			m.res.row = i
		}
	}
	m.res.selCol = 2
	press(m, key('u')) // drain: column type, then the question
	if m.mode != modePrompt || string(m.prompt.input) != "one" || !strings.Contains(m.prompt.label, "NAME (varchar2") {
		t.Fatalf("prompt: %q %q status %q", m.prompt.label, string(m.prompt.input), m.status)
	}
	answer(m, "ONE edited")
	if m.res.res.Rows[m.res.row][2] != "ONE edited" || value() != "ONE edited" || !m.conn.InTransaction() {
		t.Fatalf("after edit: grid %q db %q status %q", m.res.res.Rows[m.res.row][2], value(), m.status)
	}
	// F8: database and grid go back
	m.focus = focusResults
	press(m, key(tea.KeyF8))
	if m.res.res.Rows[m.res.row][2] != "one" || value() != "one" || !strings.Contains(m.status, "undone in the grid") {
		t.Fatalf("rollback: grid %q db %q status %q", m.res.res.Rows[m.res.row][2], value(), m.status)
	}
	// edit again and commit; a number column too
	press(m, key('u'))
	answer(m, "kept")
	m.res.selCol = 3
	press(m, key('u'))
	answer(m, "9,25")
	press(m, key(tea.KeyF9))
	r, _ := m.conn.Execute(ctx, "select name, amount from sqlcli_test_grid where id = 1", 1)
	if r.Rows[0][0] != "kept" || r.Rows[0][1] != "9.25" || len(m.res.edits) != 0 {
		t.Fatalf("commit: %v edits %d", r.Rows[0], len(m.res.edits))
	}
	// empty value = NULL; a bad number is refused
	press(m, key('u'))
	answer(m, "")
	if m.res.res.Rows[m.res.row][3] != db.Null {
		t.Fatalf("NULL: %q", m.res.res.Rows[m.res.row][3])
	}
	press(m, key('u'))
	answer(m, "abc")
	if !m.statusErr || !strings.Contains(m.status, "not a number") {
		t.Fatalf("bad number: %q", m.status)
	}
	press(m, key(tea.KeyF8))
}

func TestUIInsertDeleteRows(t *testing.T) {
	m := newTestModel(t, false)
	ctx := context.Background()
	defer m.conn.Execute(ctx, "drop table sqlcli_test_rows purge", 1)
	run(m, "create table sqlcli_test_rows (id number(5) not null, name varchar2(20))")
	run(m, "insert into sqlcli_test_rows values (1, 'one')")
	run(m, "insert into sqlcli_test_rows values (2, 'two')")
	press(m, key(tea.KeyF9))
	count := func() string {
		r, _ := m.conn.Execute(ctx, "select count(*) || ':' || listagg(name, ',') within group (order by id) from sqlcli_test_rows", 1)
		return r.Rows[0][0]
	}
	run(m, "select rowid, t.* from sqlcli_test_rows t order by id")
	m.focus = focusResults

	// record view: edit a field and stay there
	press(m, key(tea.KeyEnter))
	press(m, key(tea.KeyTab))
	press(m, key(tea.KeyTab))   // NAME
	press(m, key(tea.KeyEnter)) // in the text editor panel
	if m.mode != modeText || m.text.ed.Text() != "one" {
		t.Fatalf("record edit: mode %v", m.mode)
	}
	m.text.ed.SetText("ONE")
	press(m, key('s', tea.ModCtrl))
	if m.mode != modeRecord || !strings.Contains(screen(m), "NAME updated") || !strings.Contains(m.recordView(), "ONE") || count() != "2:ONE,two" {
		t.Fatalf("after record edit: mode %v db %q status %q", m.mode, count(), m.status)
	}

	// insert from the record view: one question per column
	press(m, key('i'))
	if !strings.Contains(m.prompt.label, "New row: ID (number, empty = NULL) [1/2]") {
		t.Fatalf("insert question: %q (status %q)", m.prompt.label, m.status)
	}
	answer(m, "3")
	answer(m, "three")
	if m.mode != modeRecord || m.res.row != 1 || m.res.res.Rows[1][1] != "3" || m.res.res.Rows[1][0] == db.Null || count() != "3:ONE,two,three" {
		t.Fatalf("after insert: row %d %v db %q status %q", m.res.row, m.res.res.Rows, count(), m.status)
	}
	press(m, key(tea.KeyEscape)) // back to the grid

	// delete the inserted row (it has its ROWID), then "two"
	press(m, key('d'))
	answer(m, "y")
	m.res.row = 1 // "two"
	press(m, key('d'))
	answer(m, "y")
	if len(m.res.res.Rows) != 1 || count() != "1:ONE" {
		t.Fatalf("after deletes: %v db %q", m.res.res.Rows, count())
	}
	// a NOT NULL violation is reported and nothing changes
	press(m, key('i'))
	answer(m, "")
	answer(m, "no id")
	if !m.statusErr || !strings.Contains(m.status, "ORA-01400") || len(m.res.res.Rows) != 1 {
		t.Fatalf("NOT NULL: %q", m.status)
	}

	// F8: the database and the grid go back to the committed state
	press(m, key(tea.KeyF8))
	if count() != "2:one,two" || len(m.res.res.Rows) != 2 || m.res.res.Rows[0][2] != "one" || m.res.res.Rows[1][2] != "two" {
		t.Fatalf("rollback: db %q grid %v status %q", count(), m.res.res.Rows, m.status)
	}
}

func TestUIExplainPlan(t *testing.T) {
	m := newTestModel(t, false)
	m.ed.SetText("select 1 from dual;\n\nselect * from dual where dummy = 'X';")
	m.ed.SetCursor(2, 5)
	m.focus = focusEditor
	press(m, key(tea.KeyF5, tea.ModShift))
	if m.running || m.explaining || len(m.res.plan) < 5 || !m.res.showText() {
		t.Fatalf("plan: %q status %q err %q", m.res.plan, m.status, m.res.err)
	}
	plan := strings.Join(m.res.plan, "\n")
	for _, want := range []string{"Plan hash value", "TABLE ACCESS FULL", "DUAL", "Predicate Information", `"DUMMY"='X'`, "Column Projection"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("plan lacks %q:\n%s", want, plan)
		}
	}
	if s := screen(m); !strings.Contains(s, "Execution plan") || !strings.Contains(s, "Plan hash value") {
		t.Fatalf("screen:\n%s", s)
	}
	// o shows the plan rows as a grid; a normal run clears the plan
	m.focus = focusResults
	press(m, key('o'))
	if m.res.showText() || m.res.res.Columns[0] != "PLAN_TABLE_OUTPUT" {
		t.Fatalf("o: %v", m.res.showText())
	}
	run(m, "select 1 from dual")
	if len(m.res.plan) != 0 || m.res.showText() {
		t.Fatal("a normal run should show the grid without a plan")
	}
	if m.conn.InTransaction() {
		t.Fatal("explain plan should not leave a transaction open")
	}
}
