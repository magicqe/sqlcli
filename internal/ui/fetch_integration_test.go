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
		press(m, key(tea.KeyDown))
	}
	if len(r.Rows) != 1500 || r.More {
		t.Fatalf("Down: rows=%d more=%v", len(r.Rows), r.More)
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
