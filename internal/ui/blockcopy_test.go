package ui

import (
	"strings"
	"testing"
)

// mark sets the block in the active tab from (r1,c1) to (r2,c2) with ^K B / ^K K.
func mark(m *Model, r1, c1, r2, c2 int) {
	m.ed.SetCursor(r1, c1)
	ctrlK(m, 'b')
	m.ed.SetCursor(r2, c2)
	ctrlK(m, 'k')
}

func TestBlockCopyAndMoveBetweenTabs(t *testing.T) {
	m := mouseModel()
	m.focus = focusEditor
	m.ed.SetText("select a\n  from t1\nwhere x = 1")
	mark(m, 0, 0, 2, 0) // "select a\n  from t1\n"

	m.openTab()
	m.ed.SetText("-- copy here:\n")
	m.ed.SetCursor(1, 0)
	ctrlK(m, 'c')
	if m.ed.Text() != "-- copy here:\nselect a\n  from t1\n" {
		t.Fatalf("copied: %q (status %q)", m.ed.Text(), m.status)
	}
	if m.ed.BlockText() != "select a\n  from t1\n" || m.tabs[0].ed.HasBlock() {
		t.Fatal("the copy should become the block (one block at a time)")
	}
	if m.tabs[0].ed.Text() != "select a\n  from t1\nwhere x = 1" || !strings.HasPrefix(m.status, "Copied 3 line(s) from tab 1") {
		t.Fatalf("source changed / status %q", m.status)
	}

	// ^K M moves it back into tab 1, deleting it here
	m.switchTab(0)
	m.ed.SetCursor(2, 11) // end of "where x = 1"
	ctrlK(m, 'm')
	if m.tabs[1].ed.Text() != "-- copy here:\n" || !strings.HasSuffix(m.ed.Text(), "where x = 1select a\n  from t1\n") {
		t.Fatalf("move: tab1 %q tab2 %q", m.ed.Text(), m.tabs[1].ed.Text())
	}
	if !strings.HasPrefix(m.status, "Moved") {
		t.Fatalf("status %q", m.status)
	}

	// undo takes a copy back out
	m.switchTab(1)
	m.tabs[0].ed.SetText("abc")
	m.tabs[0].ed.SetBlock(0, 0, 0, 3)
	m.ed.SetText("x")
	m.ed.SetCursor(0, 1)
	ctrlK(m, 'c')
	if m.ed.Text() != "xabc" {
		t.Fatalf("copy: %q", m.ed.Text())
	}
	ctrlUndo(m)
	if m.ed.Text() != "x" {
		t.Fatalf("undo after copy: %q", m.ed.Text())
	}
}

func ctrlUndo(m *Model) { m.ed.HandleKey("ctrl+_", "") }

func TestBlockCopyNewestBlockAndNoBlock(t *testing.T) {
	m := mouseModel()
	m.focus = focusEditor
	ctrlK(m, 'c')
	if !m.statusErr || !strings.HasPrefix(m.status, "No block") {
		t.Fatalf("no block anywhere: %q", m.status)
	}
	m.ed.SetText("first")
	mark(m, 0, 0, 0, 5)
	m.openTab()
	m.ed.SetText("second")
	mark(m, 0, 0, 0, 6) // marked later: the newest block
	m.openTab()
	ctrlK(m, 'c')
	if m.ed.Text() != "second" {
		t.Fatalf("should copy the newest block: %q", m.ed.Text())
	}
	// within one tab ^K C works as before
	m.ed.SetCursor(0, 6)
	ctrlK(m, 'c')
	if m.ed.Text() != "secondsecond" {
		t.Fatalf("in-tab copy: %q", m.ed.Text())
	}
}
