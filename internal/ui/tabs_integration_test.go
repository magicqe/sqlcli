package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func sid(t *testing.T, m *Model) string {
	t.Helper()
	run(m, "select sys_context('userenv', 'sid') from dual")
	if !m.res.hasGrid() {
		t.Fatalf("sid query failed: %s", m.res.err)
	}
	return m.res.res.Rows[0][0]
}

func TestUITabsSeparateSessions(t *testing.T) {
	m := newTestModel(t, false)
	sid1 := sid(t, m)

	_, cmd := m.Update(key('k', tea.ModCtrl))
	drain(m, cmd)
	_, cmd = m.Update(key('o')) // new tab on the same connection
	drain(m, cmd)
	if len(m.tabs) != 2 || m.conn == nil || m.conn.Name != "it" {
		t.Fatalf("new tab not connected: tabs=%d status=%q", len(m.tabs), m.status)
	}
	sid2 := sid(t, m)
	t.Logf("tab 1 sid %s, tab 2 sid %s", sid1, sid2)
	if sid1 == sid2 {
		t.Fatal("tabs share one Oracle session")
	}

	// transactions are per tab
	run(m, "begin null; end;")
	if !m.tabs[1].conn.InTransaction() || m.tabs[0].conn.InTransaction() {
		t.Fatal("transaction state leaked between tabs")
	}
	if !strings.Contains(m.tabBar(), "2 it TX") {
		t.Errorf("tab bar should mark TX: %q", m.tabBar())
	}
	press(m, key(tea.KeyF9))
}

func TestUITabsRunConcurrently(t *testing.T) {
	m := newTestModel(t, false)
	_, cmd := m.Update(key('k', tea.ModCtrl))
	drain(m, cmd)
	_, cmd = m.Update(key('o'))
	drain(m, cmd)
	slow, fast := m.tabs[0], m.tabs[1]

	// start a 3-second statement in tab 1 without waiting for it
	m.switchTab(0)
	m.ed.SetText("begin dbms_session.sleep(3); end;")
	m.focus = focusEditor
	_, slowCmd := m.Update(key(tea.KeyF5))
	msgs := make(chan tea.Msg, 4)
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
			if _, ok := msg.(tickMsg); !ok {
				msgs <- msg
			}
		}()
	}
	spawn(slowCmd)
	if !slow.running {
		t.Fatal("tab 1 not running")
	}

	// tab 2 works meanwhile
	m.switchTab(1)
	start := time.Now()
	run(m, "select 'fast' from dual")
	took := time.Since(start)
	t.Logf("tab 2 query took %s while tab 1 was running", took.Round(time.Millisecond))
	if !fast.res.hasGrid() || fast.res.res.Rows[0][0] != "fast" || took > 2*time.Second || !slow.running {
		t.Fatalf("tab 2 was blocked: took %s, slow running=%v", took, slow.running)
	}
	if !strings.Contains(m.tabBar(), "1 it TX …") && !strings.Contains(m.tabBar(), "1 it …") {
		t.Errorf("tab bar should show tab 1 running: %q", m.tabBar())
	}

	// tab 1 finishes in the background; tab 2 stays active
	select {
	case msg := <-msgs:
		m.Update(msg)
	case <-time.After(15 * time.Second):
		t.Fatal("slow statement never finished")
	}
	if slow.running || !strings.Contains(slow.status, "PL/SQL procedure successfully completed") || m.tab != fast {
		t.Fatalf("background finish: running=%v status=%q active=%d", slow.running, slow.status, m.tabIndex(m.tab))
	}
	_ = slow.conn.Rollback()
}

func TestUITabsDifferentConnections(t *testing.T) {
	m := newTestModel(t, false)
	// a second saved connection (same database, different name)
	c2 := m.cfg.Connections[0]
	c2.Name = "other"
	m.cfg.Connections = append(m.cfg.Connections, c2)

	_, cmd := m.Update(key('k', tea.ModCtrl))
	drain(m, cmd)
	_, cmd = m.Update(key('o'))
	drain(m, cmd)
	press(m, key(tea.KeyF2)) // hide the (visible) panel
	press(m, key(tea.KeyF2)) // show it again, focused
	press(m, key(tea.KeyDown))
	press(m, key(tea.KeyEnter)) // connect tab 2 to "other"
	if m.conn == nil || m.conn.Name != "other" || m.tabs[0].conn.Name != "it" {
		t.Fatalf("tab 2 conn=%v tab 1 conn=%v", m.conn, m.tabs[0].conn)
	}
	bar := m.tabBar()
	if !strings.Contains(bar, "1 it") || !strings.Contains(bar, "2 other") {
		t.Fatalf("tab bar: %q", bar)
	}
	s := screen(m)
	if !strings.Contains(s, "○ it") || !strings.Contains(s, "● other") || !strings.Contains(s, "SQL 2 · other") {
		t.Fatalf("panel markers / title:\n%s", s)
	}
	run(m, "select 'from other' from dual")
	if m.res.res.Rows[0][0] != "from other" {
		t.Fatal("query in tab 2 failed")
	}
	m.CloseAll()
}
