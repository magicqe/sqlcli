package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"sqlcli/internal/config"
	"sqlcli/internal/db"
)

func ctrlK(m *Model, k rune) {
	press(m, key('k', tea.ModCtrl))
	press(m, key(k))
}

func newTabsModel() *Model {
	cfg := config.Default()
	cfg.Connections = []config.Connection{{Name: "ora"}}
	m := New(cfg, nil, "ora")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestTabsOpenSwitchClose(t *testing.T) {
	m := newTabsModel()
	m.ed.SetText("select 1 from dual")
	if strings.Contains(screen(m), " 1 not connected ") {
		t.Fatal("tab bar shown with a single tab")
	}
	paneH := m.res.h

	ctrlK(m, 'o') // new tab (not connected: no connect attempt)
	if len(m.tabs) != 2 || m.tabIndex(m.tab) != 1 {
		t.Fatalf("tabs=%d active=%d", len(m.tabs), m.tabIndex(m.tab))
	}
	if m.ed.Text() != "" {
		t.Fatal("new tab should have an empty editor")
	}
	s := screen(m)
	if !strings.Contains(s, " 1 not connected ") || !strings.Contains(s, "SQL 2 · not connected") {
		t.Fatalf("tab bar / title missing:\n%s", s)
	}
	for _, l := range strings.Split(s, "\n") {
		if len([]rune(l)) > 100 {
			t.Fatalf("line too wide: %q", l)
		}
	}
	if m.res.h >= paneH && m.tabs[0].ed != nil && len(strings.Split(s, "\n")) > 30 {
		t.Fatal("tab bar pushed the screen past its height")
	}
	m.ed.SetText("select 2 from dual")

	ctrlK(m, 'p')
	if m.tabIndex(m.tab) != 0 || m.ed.Text() != "select 1 from dual" {
		t.Fatalf("^K P: active=%d text=%q", m.tabIndex(m.tab), m.ed.Text())
	}
	ctrlK(m, 'n')
	if m.tabIndex(m.tab) != 1 || m.ed.Text() != "select 2 from dual" {
		t.Fatal("^K N did not return to tab 2")
	}
	press(m, key(tea.KeyPgUp, tea.ModCtrl))
	if m.tabIndex(m.tab) != 0 {
		t.Fatal("Ctrl-PgUp")
	}
	press(m, key('2', tea.ModAlt))
	if m.tabIndex(m.tab) != 1 {
		t.Fatal("Alt-2")
	}
	press(m, key(tea.KeyPgDown, tea.ModCtrl)) // wraps around
	if m.tabIndex(m.tab) != 0 {
		t.Fatal("Ctrl-PgDn should wrap to the first tab")
	}

	ctrlK(m, 'q') // close tab 1
	if len(m.tabs) != 1 || m.ed.Text() != "select 2 from dual" {
		t.Fatalf("close: tabs=%d text=%q", len(m.tabs), m.ed.Text())
	}
	if strings.Contains(screen(m), "SQL 1 ·") || m.res.h != paneH {
		t.Fatal("single tab should hide the tab bar and restore pane height")
	}

	_, cmd := m.Update(key('k', tea.ModCtrl))
	_, cmd = m.Update(key('q')) // closing the last tab quits
	if cmd == nil || !m.quitting {
		t.Fatal("^K Q on the last tab should quit")
	}
}

func TestBackgroundResultGoesToItsTab(t *testing.T) {
	m := newTabsModel()
	first := m.tab
	first.running = true
	ctrlK(m, 'o')
	second := m.tab

	// tab 1's statement finishes while tab 2 is active
	m.Update(execDoneMsg{tab: first.id, last: &db.Result{Message: "7 row(s) updated."}, messages: []string{"7 row(s) updated.  (5ms)"}})
	if m.tab != second {
		t.Fatal("active tab changed")
	}
	if first.running || first.status != "7 row(s) updated." || !strings.Contains(first.res.view(false), "7 row(s) updated") {
		t.Fatalf("tab 1 not updated: running=%v status=%q", first.running, first.status)
	}
	if second.status == first.status || strings.Contains(second.res.view(false), "7 row(s)") {
		t.Fatal("result leaked into the active tab")
	}
	// a result for a closed tab is ignored
	m.Update(execDoneMsg{tab: 999})
}

func TestTabBarScrolls(t *testing.T) {
	m := newTabsModel()
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	for i := 0; i < 11; i++ { // 12 tabs: " 1 SQL " ... wider than 60
		m.newTab()
	}
	bar := func() string { return ansi.Strip(m.tabBar()) }
	m.switchTab(0)
	if b := bar(); !strings.Contains(b, " 1 ") || strings.Contains(b, " 12 ") || !strings.HasSuffix(b, "▶") || strings.Contains(b, "◀") {
		t.Fatalf("start: %q", b)
	}
	if w := ansi.StringWidth(bar()); w > 60 {
		t.Fatalf("bar too wide: %d", w)
	}
	// the active tab is kept in view
	m.switchTab(11)
	if b := bar(); !strings.Contains(b, " 12 ") || !strings.HasPrefix(b, "◀") || strings.Contains(b, "▶") {
		t.Fatalf("last tab active: %q", b)
	}
	// clicks: ◀ scrolls, a label switches
	m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	first := m.tabFirst
	if m.tabIndex(m.tab) != 11 || !strings.HasSuffix(bar(), "▶") {
		t.Fatalf("◀ should scroll, not switch: active %d bar %q", m.tabIndex(m.tab), bar())
	}
	items := m.tabLayout()
	var it tabItem
	for _, x := range items {
		if x.idx == first {
			it = x
		}
	}
	m.Update(tea.MouseClickMsg{X: it.x + 1, Y: 0, Button: tea.MouseLeft})
	if m.tabIndex(m.tab) != first {
		t.Fatalf("click on tab %d: active %d", first, m.tabIndex(m.tab))
	}
	// the wheel over the bar scrolls it
	m.switchTab(0)
	bar()
	m.Update(tea.MouseWheelMsg{X: 5, Y: 0, Button: tea.MouseWheelDown})
	if m.tabFirst != 1 || !strings.HasPrefix(bar(), "◀") || m.tabIndex(m.tab) != 0 {
		t.Fatalf("wheel: first %d bar %q", m.tabFirst, bar())
	}
	// a wide screen shows every tab, no markers
	m.Update(tea.WindowSizeMsg{Width: 400, Height: 30})
	if b := bar(); strings.ContainsAny(b, "◀▶") || !strings.Contains(b, " 12 ") {
		t.Fatalf("wide: %q", b)
	}
}
