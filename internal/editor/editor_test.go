package editor

import "testing"

func typ(e *Editor, s string) {
	for _, r := range s {
		if r == '\n' {
			e.HandleKey("enter", "")
		} else if r == ' ' {
			e.HandleKey("space", " ")
		} else {
			e.HandleKey(string(r), string(r))
		}
	}
}

func TestTypingAndJoeDeletes(t *testing.T) {
	e := New()
	e.SetSize(80, 10)
	typ(e, "select foo from bar")
	e.HandleKey("ctrl+a", "")
	e.HandleKey("ctrl+w", "") // delete word "select"
	if got := e.Text(); got != " foo from bar" {
		t.Fatalf("after ^W: %q", got)
	}
	e.HandleKey("ctrl+e", "")
	e.HandleKey("ctrl+o", "") // delete word left "bar"
	if got := e.Text(); got != " foo from " {
		t.Fatalf("after ^O: %q", got)
	}
	e.HandleKey("ctrl+a", "")
	e.HandleKey("ctrl+j", "") // delete to eol
	if got := e.Text(); got != "" {
		t.Fatalf("after ^J: %q", got)
	}
	e.HandleKey("ctrl+_", "")
	if got := e.Text(); got != " foo from " {
		t.Fatalf("after undo: %q", got)
	}
	e.HandleKey("ctrl+^", "")
	if got := e.Text(); got != "" {
		t.Fatalf("after redo: %q", got)
	}
}

func TestBlockCopyMoveDelete(t *testing.T) {
	e := New()
	e.SetSize(80, 10)
	e.SetText("aaa\nbbb\nccc")
	// mark "bbb\n"
	e.GotoLine(2)
	e.HandleKey("ctrl+k", "")
	e.HandleKey("b", "b")
	e.GotoLine(3)
	e.HandleKey("ctrl+k", "")
	e.HandleKey("k", "k")
	if got := e.BlockText(); got != "bbb\n" {
		t.Fatalf("block: %q", got)
	}
	// copy to top
	e.HandleKey("ctrl+k", "")
	e.HandleKey("u", "u")
	e.HandleKey("ctrl+k", "")
	e.HandleKey("c", "c")
	if got := e.Text(); got != "bbb\naaa\nbbb\nccc" {
		t.Fatalf("copy: %q", got)
	}
	// block now is the copy at top; move it to end
	e.HandleKey("ctrl+k", "")
	e.HandleKey("ctrl+v", "") // ^K ^V = end of file
	e.HandleKey("enter", "")
	e.HandleKey("ctrl+k", "")
	e.HandleKey("m", "m")
	if got := e.Text(); got != "aaa\nbbb\nccc\nbbb\n" {
		t.Fatalf("move: %q", got)
	}
	e.HandleKey("ctrl+k", "")
	e.HandleKey("y", "y")
	if got := e.Text(); got != "aaa\nbbb\nccc\n" {
		t.Fatalf("delete block: %q", got)
	}
}

func TestFindAndDeleteLine(t *testing.T) {
	e := New()
	e.SetSize(80, 10)
	e.SetText("one\ntwo\nthree two")
	if !e.Find("TWO") || e.CursorRow() != 1 {
		t.Fatalf("find 1: row %d", e.CursorRow())
	}
	e.HandleKey("ctrl+l", "")
	if e.CursorRow() != 2 || e.CursorCol() != 6 {
		t.Fatalf("find next: %d,%d", e.CursorRow(), e.CursorCol())
	}
	e.HandleKey("ctrl+y", "")
	if got := e.Text(); got != "one\ntwo" {
		t.Fatalf("^Y: %q", got)
	}
}

func TestAutoIndent(t *testing.T) {
	e := New()
	e.SetSize(80, 10)
	typ(e, "  select\nx")
	if got := e.Text(); got != "  select\n  x" {
		t.Fatalf("autoindent: %q", got)
	}
}
