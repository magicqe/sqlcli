package editor

import (
	"strings"
	"testing"
)

// classes renders the kind of each character as one letter:
// . plain, K keyword, T type, F function, S string, N number, C comment, B bind.
func classes(kinds []tokKind) string {
	const letters = ".KTFSNCB"
	var b strings.Builder
	for _, k := range kinds {
		b.WriteByte(letters[k])
	}
	return b.String()
}

func lexAll(lines ...string) ([]string, lexState) {
	var st lexState
	var out []string
	for _, l := range lines {
		var k []tokKind
		k, st = lexLine([]rune(l), st)
		out = append(out, classes(k))
	}
	return out, st
}

func TestLexLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"select a from t", "KKKKKK...KKKK.."},
		{"SELECT count(*) FROM t1", "KKKKKK.FFFFF....KKKK..."},
		{"where n > 1.5e3 and x = 42", "KKKKK.....NNNNN.KKK.....NN"},
		{"x = 'it''s' -- note", "....SSSSSSS.CCCCCCC"},
		{"v := :id || :2", ".....BBB....BB"},
		{"amount number(10,2)", ".......TTTTTT.NN.N."},
		{"t.from s.select", "..............."},
		{`"select" x`, ".........."},
		{"count(x) count", "FFFFF........."},
		{"q'[it's]' x", "SSSSSSSSS.."},
		{"N'žluť'", "SSSSSSS"},
		{"t1 tab2col", ".........."},
	}
	for _, c := range cases {
		got, _ := lexAll(c.in)
		if got[0] != c.want {
			t.Errorf("%q\n got  %s\n want %s", c.in, got[0], c.want)
		}
	}
}

func TestLexAcrossLines(t *testing.T) {
	got, st := lexAll("select /* a", "still comment */ from t", "x := 'line one", "line two' || y")
	want := []string{"KKKKKK.CCCC", "CCCCCCCCCCCCCCCC.KKKK..", ".....SSSSSSSSS", "SSSSSSSSS....."}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d\n got  %s\n want %s", i, got[i], want[i])
		}
	}
	if st.inComment || st.inString {
		t.Fatalf("state leaked: %+v", st)
	}
	// unterminated constructs carry over
	if _, st = lexAll("/* open"); !st.inComment {
		t.Fatal("open comment not carried")
	}
	if _, st = lexAll("q'{abc"); !st.inString || st.closer != '}' {
		t.Fatalf("q-quote state: %+v", st)
	}
}

func TestViewHighlight(t *testing.T) {
	render := func(on bool) string {
		e := New()
		e.Highlight = on
		e.SetSize(40, 3)
		e.SetText("select 'x' from dual -- hi")
		return e.View()
	}
	on, off := render(true), render(false)
	if on == off {
		t.Fatal("highlighting changed nothing")
	}
	// keyword colour (256-colour 75) and comment colour (244) appear only when on
	for _, code := range []string{"38;5;75", "38;5;244", "38;5;114"} {
		if !strings.Contains(on, code) {
			t.Fatalf("colour %s missing with highlighting on:\n%q", code, on)
		}
		if strings.Contains(off, code) {
			t.Fatalf("colour %s present with highlighting off:\n%q", code, off)
		}
	}
	// text is unchanged apart from colour codes
	strip := func(s string) string {
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
	if strip(on) != strip(off) {
		t.Fatalf("visible text differs:\n%q\n%q", strip(on), strip(off))
	}
}

// scanState must agree with lexLine on the carried state for any input.
func TestScanStateMatchesLexLine(t *testing.T) {
	lines := []string{
		"select 'a''b' from t -- '", "/* x", "y */ q'[a", "b]' z", "n'x", "y'", `"we'ird" 'ok'`,
		"seq'1' x", "a := q'<x>' || 'y", "z'", "-- /* not a comment start", "/* a */ /* b", "c */",
	}
	var a, b lexState
	for i, l := range lines {
		_, a = lexLine([]rune(l), a)
		b = scanState([]rune(l), b)
		if a != b {
			t.Fatalf("line %d %q: lexLine %+v, scanState %+v", i, l, a, b)
		}
	}
}
