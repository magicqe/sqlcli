package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sqlcli/internal/db"
)

// sqlFooter returns the SQL panel's bottom border line.
func sqlFooter(t *testing.T, m *Model) string {
	t.Helper()
	lines := strings.Split(screen(m), "\n")
	sqlH, _ := m.paneHeights()
	y := m.tabBarH() + sqlH - 1
	f := string([]rune(lines[y])[m.connWidth():])
	if !strings.HasPrefix(f, "╰") {
		t.Fatalf("line %d is not the SQL panel's bottom border: %q", y, f)
	}
	return f
}

func TestFmtDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		12 * time.Millisecond:   "12ms",
		3200 * time.Millisecond: "3.2s",
		65 * time.Second:        "1m05s",
	} {
		if got := fmtDuration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

func TestRunFooter(t *testing.T) {
	m := mouseModel()
	m.showConns = false
	m.layout()
	if f := sqlFooter(t, m); strings.ContainsAny(f, "✓✗●■") {
		t.Fatalf("footer before any run: %q", f)
	}

	m.running, m.runStart = true, time.Now().Add(-3200*time.Millisecond)
	if f := sqlFooter(t, m); !strings.Contains(f, "● running 3.2s") {
		t.Fatalf("running: %q", f)
	}

	q := &db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"1"}, {"2"}}, More: true}
	m.Update(execDoneMsg{tab: m.id, last: q, messages: []string{"2 row(s) fetched"}, elapsed: 12 * time.Millisecond})
	f := sqlFooter(t, m)
	if !strings.Contains(f, "✓ 2+ rows · 12ms · at ") {
		t.Fatalf("query: %q", f)
	}
	m.res.appendRows([][]string{{"3"}}) // a later fetch updates the count
	q.More = false
	if f := sqlFooter(t, m); !strings.Contains(f, "✓ 3 rows · 12ms") {
		t.Fatalf("after fetch: %q", f)
	}

	m.running = true
	m.Update(execDoneMsg{tab: m.id, last: &db.Result{Message: "3 row(s) updated."}, messages: []string{"x"}, elapsed: 5 * time.Millisecond})
	if f := sqlFooter(t, m); !strings.Contains(f, "✓ 3 row(s) updated · 5ms") {
		t.Fatalf("dml: %q", f)
	}

	m.running = true
	m.Update(execDoneMsg{tab: m.id, last: &db.Result{Message: "PL/SQL procedure successfully completed."}, messages: []string{"x"}, elapsed: time.Second})
	if f := sqlFooter(t, m); !strings.Contains(f, "✓ PL/SQL OK · 1.0s") {
		t.Fatalf("plsql: %q", f)
	}

	m.running = true
	m.Update(execDoneMsg{tab: m.id, err: errors.New("dpiStmt_execute: ORA-00942: table or view does not exist\nHelp: x"), elapsed: 8 * time.Millisecond})
	if f := sqlFooter(t, m); !strings.Contains(f, "✗ ORA-00942: table or view does not exist · 8ms · at ") {
		t.Fatalf("error: %q", f)
	}

	m.running = true
	m.Update(execDoneMsg{tab: m.id, err: context.Canceled, elapsed: 2100 * time.Millisecond})
	if f := sqlFooter(t, m); !strings.Contains(f, "■ cancelled after 2.1s") {
		t.Fatalf("cancelled: %q", f)
	}

	m.running = true
	m.cfg.Query.TimeoutSeconds = 30
	m.Update(execDoneMsg{tab: m.id, err: context.DeadlineExceeded, elapsed: 30 * time.Second})
	if f := sqlFooter(t, m); !strings.Contains(f, "✗ timed out after 30s (query.timeout_seconds)") {
		t.Fatalf("timeout: %q", f)
	}

	m.running = true
	m.Update(execDoneMsg{tab: m.id, last: q, messages: []string{"a", "b", "c"}, elapsed: time.Millisecond})
	if f := sqlFooter(t, m); !strings.Contains(f, "✓ 3 statements, last: 3 rows") {
		t.Fatalf("script: %q", f)
	}

	// a long error is cut to fit the border
	m.running = true
	m.Update(execDoneMsg{tab: m.id, err: errors.New("ORA-20001: " + strings.Repeat("x", 300))})
	for _, l := range strings.Split(screen(m), "\n") {
		if len([]rune(l)) > 100 {
			t.Fatalf("line wider than the screen: %q", l)
		}
	}
}

func TestFooterPerTab(t *testing.T) {
	m := mouseModel()
	m.running = true
	m.Update(execDoneMsg{tab: m.id, last: &db.Result{Message: "7 row(s) deleted."}, messages: []string{"x"}})
	m.openTab()
	if strings.Contains(screen(m), "7 row(s) deleted") {
		t.Fatal("new tab shows the other tab's run")
	}
	m.switchTab(0)
	if !strings.Contains(screen(m), "✓ 7 row(s) deleted") {
		t.Fatal("tab 1 lost its run status")
	}
}

// statusBar returns the status line (second line from the bottom).
func statusBar(m *Model) string {
	lines := strings.Split(screen(m), "\n")
	return lines[len(lines)-2]
}

func TestStatusLineHasNoTimes(t *testing.T) {
	m := mouseModel()
	m.running, m.runStart = true, time.Now().Add(-3200*time.Millisecond)
	if s := statusBar(m); !strings.Contains(s, "Running… (Ctrl-C cancels)") || strings.Contains(s, "3.2s") {
		t.Fatalf("running: %q", s)
	}
	m.fetching = true
	m.res.set(&db.Result{IsQuery: true, Columns: []string{"N"}, Rows: [][]string{{"1"}}}, nil, nil, "")
	if s := statusBar(m); !strings.Contains(s, "Fetching… 1 rows (Ctrl-C stops)") || strings.Contains(s, "3.2s") {
		t.Fatalf("fetching: %q", s)
	}
	m.fetching = false
	m.Update(execDoneMsg{tab: m.id, last: &db.Result{IsQuery: true, Message: "25 row(s) selected."},
		messages: []string{"25 row(s) selected.  (12ms)"}, elapsed: 12 * time.Millisecond})
	if s := statusBar(m); !strings.Contains(s, "25 row(s) selected.") || strings.Contains(s, "12ms") {
		t.Fatalf("done: %q", s)
	}
	if !strings.Contains(sqlFooter(t, m), "12ms") {
		t.Fatal("the SQL panel footer should still show the time")
	}
	m.running = true
	m.Update(execDoneMsg{tab: m.id, last: &db.Result{Message: "x"}, messages: []string{"[1] a  (1ms)", "[2] 3 row(s) deleted.  (4ms)"}})
	if s := statusBar(m); !strings.Contains(s, "2 statements OK; last: [2] 3 row(s) deleted.") || strings.Contains(s, "4ms") {
		t.Fatalf("script: %q", s)
	}
}

func TestWithoutTime(t *testing.T) {
	for in, want := range map[string]string{
		"25 row(s) selected.  (12ms)": "25 row(s) selected.",
		"[2] PL/SQL done.  (1.5s)":    "[2] PL/SQL done.",
		"no time here":                "no time here",
		"keeps (this)":                "keeps (this)",
	} {
		if got := withoutTime(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
