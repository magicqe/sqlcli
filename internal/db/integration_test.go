package db

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"sqlcli/internal/config"
)

// Integration tests run only when SQLCLI_TEST_CONNECT (connect string),
// SQLCLI_TEST_USER and SQLCLI_TEST_PASSWORD are set.
func testConn(t *testing.T) *Conn {
	t.Helper()
	cs := os.Getenv("SQLCLI_TEST_CONNECT")
	if cs == "" {
		t.Skip("SQLCLI_TEST_CONNECT not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Open(ctx, config.Connection{Name: "it", User: os.Getenv("SQLCLI_TEST_USER"),
		PasswordEnv: "SQLCLI_TEST_PASSWORD", ConnectString: cs}, Options{LibDir: os.Getenv("ORACLE_HOME"), DBMSOutput: true})
	if err != nil {
		t.Fatal(CleanError(err))
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestIntegrationReadOnly(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	r, err := c.Execute(ctx, "select banner from v$version", 10)
	if err != nil {
		r, err = c.Execute(ctx, "select version from product_component_version", 10)
	}
	if err != nil {
		t.Fatal(CleanError(err))
	}
	for _, row := range r.Rows {
		t.Logf("version: %v", row)
	}
	r, err = c.Execute(ctx, "select level n, 'x'||level s, sysdate d, null z from dual connect by level <= 5;", 3)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	t.Logf("cols=%v rows=%v truncated=%v msg=%q", r.Columns, r.Rows, r.More, r.Message)
	if len(r.Rows) != 3 || !r.More || r.Rows[0][3] != Null {
		t.Fatalf("unexpected result")
	}
	r, err = c.Execute(ctx, "select to_clob(rpad('x', 4000, 'x')) || to_clob(rpad('y', 1000, 'y')) big, to_clob(null) n, hextoraw('CAFE') rw from dual", 10)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	if len(r.Rows[0][0]) != 5000 || r.Rows[0][1] != Null || r.Rows[0][2] != "CAFE" {
		t.Fatalf("clob/raw: len=%d %q %q", len(r.Rows[0][0]), r.Rows[0][1], r.Rows[0][2])
	}
	_, err = c.Execute(ctx, "select * from no_such_table_xyz", 10)
	t.Logf("expected error: %s", CleanError(err))
	if err == nil {
		t.Fatal("expected error")
	}
	r, err = c.Execute(ctx, "begin null; end;", 10)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	t.Logf("plsql: %s", r.Message)
}

// TestIntegrationWrite uses a private temporary table (session-only, never
// visible to other sessions, gone at disconnect) to test DML and transactions.
func TestIntegrationWrite(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	must := func(stmt string) *Result {
		t.Helper()
		r, err := c.Execute(ctx, stmt, 100)
		if err != nil {
			t.Fatalf("%s: %s", stmt, CleanError(err))
		}
		return r
	}
	count := func() string { return must("select count(*) from ora$ptt_sqlcli").Rows[0][0] }

	must(`create private temporary table ora$ptt_sqlcli (
	        id number, name varchar2(30), note varchar2(100), ts timestamp, amount number(10,2))
	      on commit preserve definition`)
	defer c.Execute(ctx, "drop table ora$ptt_sqlcli", 1)

	r := must("insert into ora$ptt_sqlcli values (1, 'a', 'note text', timestamp '2026-01-02 03:04:05.123456', 12.5);")
	if r.Affected != 1 || !c.InTransaction() {
		t.Fatalf("insert: affected=%d tx=%v", r.Affected, c.InTransaction())
	}
	if got := count(); got != "1" {
		t.Fatalf("count inside tx = %s", got)
	}
	must("rollback")
	if c.InTransaction() || count() != "0" {
		t.Fatalf("after rollback: tx=%v count=%s", c.InTransaction(), count())
	}

	must("insert into ora$ptt_sqlcli values (1, 'a', 'note text', timestamp '2026-01-02 03:04:05.123456', 12.5)")
	must("insert into ora$ptt_sqlcli values (2, null, null, null, null)")
	must("commit")
	if c.InTransaction() || count() != "2" {
		t.Fatalf("after commit: tx=%v count=%s", c.InTransaction(), count())
	}

	r = must("select * from ora$ptt_sqlcli order by id")
	t.Logf("rows: %q", r.Rows)
	want := [][]string{
		{"1", "a", "note text", "2026-01-02 03:04:05.123456", "12.5"},
		{"2", Null, Null, Null, Null},
	}
	for i := range want {
		for j := range want[i] {
			if r.Rows[i][j] != want[i][j] {
				t.Errorf("row %d col %s = %q, want %q", i, r.Columns[j], r.Rows[i][j], want[i][j])
			}
		}
	}

	r = must("update ora$ptt_sqlcli set name = 'b'")
	if r.Affected != 2 || r.Message != "2 row(s) updated." {
		t.Fatalf("update: %d %q", r.Affected, r.Message)
	}
	must("rollback")

	// PL/SQL block in a script, then read-only enforcement
	stmts := SplitStatements("begin\n  insert into ora$ptt_sqlcli(id) values (3);\nend;\n/\nselect count(*) from ora$ptt_sqlcli;")
	if len(stmts) != 2 {
		t.Fatalf("split: %q", stmts)
	}
	if r := must(stmts[0]); !c.InTransaction() || r.Message != "PL/SQL procedure successfully completed." {
		t.Fatalf("plsql: tx=%v msg=%q", c.InTransaction(), r.Message)
	}
	if got := must(stmts[1]).Rows[0][0]; got != "3" {
		t.Fatalf("after plsql insert count=%s", got)
	}
	must("rollback")
	if got := count(); got != "2" {
		t.Fatalf("PL/SQL insert was not rolled back: count=%s", got)
	}

	// DDL while a transaction is open: Oracle commits it implicitly
	must("insert into ora$ptt_sqlcli(id) values (4)")
	r = must("create private temporary table ora$ptt_sqlcli2 (x number) on commit preserve definition")
	if c.InTransaction() {
		t.Fatalf("TX still open after DDL: %q", r.Message)
	}
	t.Logf("ddl: %s", r.Message)
	must("drop table ora$ptt_sqlcli2")
	must("rollback")
	if got := count(); got != "3" {
		t.Fatalf("row inserted before DDL should be committed: count=%s", got)
	}

	c.ReadOnly = true
	if _, err := c.Execute(ctx, "delete from ora$ptt_sqlcli", 1); err == nil {
		t.Fatal("read-only connection allowed DELETE")
	} else {
		t.Logf("read-only: %v", err)
	}
	c.ReadOnly = false
}

func TestIntegrationCancel(t *testing.T) {
	c := testConn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.Execute(ctx, "begin dbms_session.sleep(30); end;", 1)
	t.Logf("cancel after %s: %s", time.Since(start).Round(time.Millisecond), CleanError(err))
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatal("statement was not cancelled")
	}
	// connection must still be usable afterwards
	if _, err := c.Execute(context.Background(), "select 1 from dual", 1); err != nil {
		t.Fatalf("connection unusable after cancel: %s", CleanError(err))
	}
}

func TestIntegrationFetchMore(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	r, err := c.Execute(ctx, "select level n from dual connect by level <= 2500", 1000)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	if len(r.Rows) != 1000 || !r.More || !c.HasMore() {
		t.Fatalf("first batch: %d rows more=%v", len(r.Rows), r.More)
	}
	rows, done, err := c.FetchMore(ctx, 1000)
	if err != nil || done || len(rows) != 1000 || rows[0][0] != "1001" {
		t.Fatalf("second batch: %d rows done=%v err=%v first=%v", len(rows), done, err, rows[0])
	}
	rows, done, err = c.FetchMore(ctx, -1) // the rest
	if err != nil || !done || len(rows) != 500 || rows[499][0] != "2500" || c.HasMore() {
		t.Fatalf("rest: %d rows done=%v err=%v", len(rows), done, err)
	}

	// exactly maxRows rows: no "more"
	r, _ = c.Execute(ctx, "select level from dual connect by level <= 10", 10)
	if r.More || len(r.Rows) != 10 {
		t.Fatalf("exact: %d more=%v", len(r.Rows), r.More)
	}

	// a new statement while a cursor is open must not block, and closes it
	if _, err := c.Execute(ctx, "select level from dual connect by level <= 50", 10); err != nil {
		t.Fatal(err)
	}
	done2 := make(chan error, 1)
	go func() { _, err := c.Execute(ctx, "select 1 from dual", 10); done2 <- err }()
	select {
	case err := <-done2:
		if err != nil {
			t.Fatal(CleanError(err))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute blocked while a cursor was open")
	}
	if c.HasMore() {
		t.Fatal("old cursor not closed")
	}
}

func TestIntegrationFetchAllCancel(t *testing.T) {
	c := testConn(t)
	if _, err := c.Execute(context.Background(), "select level, rpad('x', 200, 'x') from dual connect by level <= 3000000", 100); err != nil {
		t.Fatal(CleanError(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	rows, done, err := c.FetchMore(ctx, -1)
	t.Logf("fetched %d rows in %s, err=%v", len(rows), time.Since(start).Round(time.Millisecond), err)
	if err == nil || !done || c.HasMore() || time.Since(start) > 10*time.Second {
		t.Fatalf("fetch was not cancelled: done=%v err=%v", done, err)
	}
	if _, err := c.Execute(context.Background(), "select 1 from dual", 1); err != nil {
		t.Fatalf("connection unusable after cancelled fetch: %s", CleanError(err))
	}
}

func TestIntegrationCursorInTransaction(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	must := func(stmt string, n int) *Result {
		t.Helper()
		r, err := c.Execute(ctx, stmt, n)
		if err != nil {
			t.Fatalf("%s: %s", stmt, CleanError(err))
		}
		return r
	}
	must("create private temporary table ora$ptt_cur (id number) on commit preserve definition", 1)
	defer c.Execute(ctx, "drop table ora$ptt_cur", 1)
	must("insert into ora$ptt_cur select level from dual connect by level <= 30", 1)
	if !c.InTransaction() {
		t.Fatal("no transaction")
	}
	r := must("select id from ora$ptt_cur order by id", 10) // sees uncommitted rows
	if len(r.Rows) != 10 || !r.More {
		t.Fatalf("in tx: %d more=%v", len(r.Rows), r.More)
	}
	rows, done, err := c.FetchMore(ctx, -1)
	if err != nil || !done || len(rows) != 20 {
		t.Fatalf("fetch in tx: %d done=%v err=%v", len(rows), done, err)
	}
	must("select id from ora$ptt_cur", 5) // leave a cursor open, then roll back
	if err := c.Rollback(); err != nil {
		t.Fatal(err)
	}
	if c.HasMore() || c.InTransaction() {
		t.Fatal("rollback left cursor or transaction open")
	}
	if got := must("select count(*) from ora$ptt_cur", 1).Rows[0][0]; got != "0" {
		t.Fatalf("after rollback count=%s", got)
	}
}

func TestIntegrationDBMSOutput(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	r, err := c.Execute(ctx, "begin dbms_output.put_line('hello'); dbms_output.put_line(''); dbms_output.put_line('ž world'); end;", 1)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	if want := []string{"hello", "", "ž world"}; !reflect.DeepEqual(r.Output, want) {
		t.Fatalf("output %q, want %q", r.Output, want)
	}

	// buffer is cleared after reading
	r, _ = c.Execute(ctx, "begin null; end;", 1)
	if r.Output != nil {
		t.Fatalf("stale output: %q", r.Output)
	}

	// more lines than one get_lines call returns (128)
	r, err = c.Execute(ctx, "begin for i in 1..1000 loop dbms_output.put_line('line '||i); end loop; end;", 1)
	if err != nil || len(r.Output) != 1000 || r.Output[999] != "line 1000" {
		t.Fatalf("1000 lines: n=%d err=%v", len(r.Output), err)
	}

	// output printed before an exception is returned with the error
	r, err = c.Execute(ctx, "begin dbms_output.put_line('before failure'); raise_application_error(-20001, 'boom'); end;", 1)
	if err == nil || r == nil || len(r.Output) != 1 || r.Output[0] != "before failure" {
		t.Fatalf("failure: err=%v res=%+v", err, r)
	}
	t.Logf("error with output: %s / %q", CleanError(err), r.Output)

	// inside an open transaction (PL/SQL opens one when auto-commit is off)
	r, err = c.Execute(ctx, "begin dbms_output.put_line('in tx'); end;", 1)
	if err != nil || !c.InTransaction() || len(r.Output) != 1 || r.Output[0] != "in tx" {
		t.Fatalf("tx: err=%v tx=%v out=%q", err, c.InTransaction(), r.Output)
	}
	_ = c.Rollback()

	// a query calling a function that prints
	r, err = c.Execute(ctx, "with function f return number is begin dbms_output.put_line('from select'); return 1; end; select f() from dual", 10)
	if err != nil {
		t.Logf("WITH FUNCTION not supported here: %s", CleanError(err))
	} else if len(r.Output) != 1 || r.Output[0] != "from select" {
		t.Fatalf("query output: %q", r.Output)
	}
}

func TestIntegrationDBMSOutputDisabled(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("SQLCLI_TEST_CONNECT") == "" {
		t.Skip("SQLCLI_TEST_CONNECT not set")
	}
	c, err := Open(ctx, config.Connection{Name: "it", User: os.Getenv("SQLCLI_TEST_USER"),
		PasswordEnv: "SQLCLI_TEST_PASSWORD", ConnectString: os.Getenv("SQLCLI_TEST_CONNECT")},
		Options{LibDir: os.Getenv("ORACLE_HOME")})
	if err != nil {
		t.Fatal(CleanError(err))
	}
	defer c.Close()
	r, err := c.Execute(ctx, "begin dbms_output.put_line('x'); end;", 1)
	if err != nil || r.Output != nil {
		t.Fatalf("disabled: err=%v out=%q", err, r.Output)
	}
}

// TestIntegrationNoLeftoverTimeout: with timeout_seconds = 0 a statement may
// run as long as it needs. A deadline used while connecting or reading
// DBMS_OUTPUT must not stay on the session as an Oracle call timeout.
func TestIntegrationNoLeftoverTimeout(t *testing.T) {
	if os.Getenv("SQLCLI_TEST_CONNECT") == "" {
		t.Skip("SQLCLI_TEST_CONNECT not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second) // like the app's connect timeout
	c, err := Open(ctx, config.Connection{Name: "it", User: os.Getenv("SQLCLI_TEST_USER"),
		PasswordEnv: "SQLCLI_TEST_PASSWORD", ConnectString: os.Getenv("SQLCLI_TEST_CONNECT")},
		Options{LibDir: os.Getenv("ORACLE_HOME"), DBMSOutput: true})
	cancel()
	if err != nil {
		t.Fatal(CleanError(err))
	}
	defer c.Close()
	for i := 0; i < 2; i++ { // the second run follows a DBMS_OUTPUT read
		start := time.Now()
		r, err := c.Execute(context.Background(), "begin dbms_output.put_line('x'); dbms_session.sleep(2.5); end;", 1)
		if err != nil {
			t.Fatalf("run %d cancelled after %s: %s", i+1, time.Since(start).Round(time.Millisecond), CleanError(err))
		}
		if len(r.Output) != 1 {
			t.Fatalf("output: %q", r.Output)
		}
	}
	_ = c.Rollback()
}

func TestIntegrationCatalog(t *testing.T) {
	c := testConn(t)
	ctx := context.Background()
	objs, err := c.Objects(ctx)
	if err != nil {
		t.Fatal(CleanError(err))
	}
	kinds := map[string]int{}
	hasDual := false
	for _, o := range objs {
		kinds[o.Type]++
		hasDual = hasDual || (o.Name == "DUAL" && o.Type == "PUBLIC SYNONYM")
	}
	t.Logf("%d objects: %v", len(objs), kinds)
	if !hasDual {
		t.Fatal("public synonym DUAL missing")
	}

	// a table in the current schema, via a private temporary table's sibling: use a real dictionary view
	cols, err := c.Columns(ctx, "SYS", "DUAL")
	if err != nil || len(cols) != 1 || cols[0] != "DUMMY" {
		t.Fatalf("SYS.DUAL: %v %v", cols, err)
	}
	// through a public synonym (no owner given)
	cols, err = c.Columns(ctx, "", "ALL_TAB_COLUMNS")
	if err != nil || len(cols) < 10 || cols[0] != "OWNER" {
		t.Fatalf("ALL_TAB_COLUMNS via synonym: %v %v", cols, err)
	}
	if cols, _ := c.Columns(ctx, "", "NO_SUCH_TABLE_XYZ"); len(cols) != 0 {
		t.Fatalf("unknown table: %v", cols)
	}

	// metadata while a result cursor is open must not disturb the cursor
	r, err := c.Execute(ctx, "select level from dual connect by level <= 30", 10)
	if err != nil || !r.More {
		t.Fatal("no open cursor")
	}
	if _, err := c.Columns(ctx, "SYS", "DUAL"); err != nil {
		t.Fatal(err)
	}
	rows, done, err := c.FetchMore(ctx, -1)
	if err != nil || !done || len(rows) != 20 || rows[0][0] != "11" {
		t.Fatalf("cursor after metadata: %d rows err=%v", len(rows), err)
	}
}
