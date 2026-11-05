package editor

import "testing"

func TestReplaceAskEach(t *testing.T) {
	e := New()
	e.SetText("select a from t;\nselect A, aa from a;")
	e.SetCursor(0, 0)
	r := e.StartReplace("a", "x", false)
	if r == nil || e.CursorRow() != 0 || e.CursorCol() != 7 {
		t.Fatalf("first match: %v %d,%d", r, e.CursorRow(), e.CursorCol())
	}
	// yes, no (the 'A'), then all the rest
	if !e.ReplaceStep(r, true) || e.CursorRow() != 1 || e.CursorCol() != 7 {
		t.Fatalf("second match at %d,%d", e.CursorRow(), e.CursorCol())
	}
	e.ReplaceStep(r, false)
	e.ReplaceRest(r)
	if got := e.Text(); got != "select x from t;\nselect A, xx from x;" || r.Count != 4 {
		t.Fatalf("got %q, %d replacements", got, r.Count)
	}
	e.HandleKey("ctrl+_", "") // one undo step
	if e.Text() != "select a from t;\nselect A, aa from a;" {
		t.Fatalf("undo: %q", e.Text())
	}
}

func TestReplaceWrapsAndStops(t *testing.T) {
	e := New()
	e.SetText("ab ab\nab")
	e.SetCursor(0, 3) // second "ab": goes to the end, wraps, stops here
	r := e.StartReplace("ab", "abab", true)
	e.ReplaceRest(r)
	if got := e.Text(); got != "abab abab\nabab" || r.Count != 3 {
		t.Fatalf("got %q, %d", got, r.Count)
	}
}

func TestReplaceMatchCase(t *testing.T) {
	e := New()
	e.SetText("Emp emp EMP")
	r := e.StartReplace("emp", "dept", true)
	e.ReplaceRest(r)
	if e.Text() != "Emp dept EMP" {
		t.Fatalf("match case: %q", e.Text())
	}
	if e.StartReplace("nothing", "x", false) != nil || e.Status == "" {
		t.Fatal("no match should return nil with a status")
	}
}

func TestFindCase(t *testing.T) {
	e := New()
	e.SetText("Abc abc")
	if !e.FindCase("abc", true) || e.CursorCol() != 4 {
		t.Fatalf("case-sensitive find at %d", e.CursorCol())
	}
	if !e.Find("") || e.CursorCol() != 4 { // only one: wraps back to itself
		t.Fatalf("find next at %d", e.CursorCol())
	}
	if !e.FindCase("abc", false) || e.CursorCol() != 0 {
		t.Fatalf("ignore case wraps to %d", e.CursorCol())
	}
}
