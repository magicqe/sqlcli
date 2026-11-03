package editor

import (
	"strings"
	"testing"
)

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestPopupRenderAndMove(t *testing.T) {
	e := New()
	e.Focused = true
	e.SetSize(60, 12)
	e.SetText("select em")
	e.HandleKey("end", "")
	items := []PopupItem{{"EMPLOYEES", "table"}, {"EMP_VIEW", "view"}, {"EMPTY_BLOB", "function"}}
	e.OpenPopup(items, 7)
	lines := strings.Split(stripANSI(e.View()), "\n")
	// gutter "1 " is 2 wide; the list starts under column 7 of the text
	if !strings.HasPrefix(lines[1][2+7:], " EMPLOYEES     table ") || !strings.Contains(lines[3], " EMPTY_BLOB function ") {
		t.Fatalf("popup:\n%s", strings.Join(lines, "\n"))
	}
	e.PopupMove(1)
	if it, _ := e.PopupSelected(); it.Text != "EMP_VIEW" {
		t.Fatalf("selected %v", it)
	}
	e.PopupMove(10)
	if it, _ := e.PopupSelected(); it.Text != "EMPTY_BLOB" {
		t.Fatal("move past the end should stop at the last entry")
	}
	// refining the list keeps the selected entry when it's still there
	e.OpenPopup(items[1:], 7)
	if it, _ := e.PopupSelected(); it.Text != "EMPTY_BLOB" {
		t.Fatalf("selection lost on refine: %v", it)
	}
	e.ReplaceWord(7, "employees")
	if e.Text() != "select employees" || e.CursorCol() != 16 {
		t.Fatalf("replace: %q col %d", e.Text(), e.CursorCol())
	}
	e.HandleKey("ctrl+_", "")
	if e.Text() != "select em" {
		t.Fatalf("undo after completion: %q", e.Text())
	}
	e.OpenPopup(nil, 0)
	if e.PopupOpen() {
		t.Fatal("empty list should close the popup")
	}
}

func TestPopupOpensUpwardsAtBottom(t *testing.T) {
	e := New()
	e.Focused = true
	e.SetSize(60, 6)
	e.SetText("a\nb\nc\nd\nselect em")
	e.HandleKey("ctrl+end", "")
	e.OpenPopup([]PopupItem{{"EMPLOYEES", "table"}, {"EMP_VIEW", "view"}}, 7)
	lines := strings.Split(stripANSI(e.View()), "\n")
	if !strings.Contains(lines[2], "EMPLOYEES") || !strings.Contains(lines[3], "EMP_VIEW") || !strings.Contains(lines[4], "select em") {
		t.Fatalf("popup should sit above the cursor line:\n%s", strings.Join(lines, "\n"))
	}
}
