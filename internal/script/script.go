// Package script implements the SQL*Plus client side of sqlcli: substitution
// variables (&name, DEFINE, ACCEPT), PROMPT, scripts (@file, @@file, START)
// and the formatting commands that don't apply to sqlcli. It turns what the
// user runs into a list of steps; SQL steps go to the database.
package script

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"sqlcli/internal/db"
)

// Env holds substitution variables and settings; each SQL tab has one.
type Env struct {
	Defines  map[string]string // upper-case names
	DefineOn bool              // substitution on (SET DEFINE ON|OFF)
}

func NewEnv(defineOn bool) *Env { return &Env{Defines: map[string]string{}, DefineOn: defineOn} }

func (e *Env) clone() *Env {
	c := &Env{Defines: make(map[string]string, len(e.Defines)), DefineOn: e.DefineOn}
	for k, v := range e.Defines {
		c.Defines[k] = v
	}
	return c
}

// Kind of a step.
type Kind int

const (
	SQL    Kind = iota // run Text on the database
	Output             // print Text (PROMPT)
	Info               // show Text as a message
	Table              // show Columns/Rows as a result (DEFINE listing)
	Spool              // SPOOL command; Text is its argument
)

type Step struct {
	Kind    Kind
	Text    string
	Columns []string
	Rows    [][]string
}

// Var is a substitution variable that needs a value from the user.
type Var struct {
	Name   string
	Prompt string // ACCEPT's prompt text, or ""
	Keep   bool   // && or ACCEPT: keep the value as a DEFINE
}

const maxDepth = 20
const maxScriptSize = 10 << 20

// Expand turns statements into steps. Variables without a value are taken
// from answers; any still missing are returned (ask the user, then call
// Expand again with the answers). The returned Env carries DEFINEs and
// settings made by the statements; keep it once the steps have been run.
func Expand(stmts []string, env *Env, dir string, answers map[string]string) ([]Step, *Env, []Var, error) {
	x := &expander{env: env.clone(), answers: answers, seen: map[string]bool{}}
	err := x.run(stmts, dir, 0)
	return x.steps, x.env, x.missing, err
}

type expander struct {
	env     *Env
	answers map[string]string
	steps   []Step
	missing []Var
	seen    map[string]bool
}

var errExit = errors.New("exit")

var ignored = map[string]bool{"WHENEVER": true, "COLUMN": true, "COL": true, "TTITLE": true, "BTITLE": true,
	"BREAK": true, "COMPUTE": true, "CLEAR": true, "PAUSE": true, "TIMING": true}

func (x *expander) run(stmts []string, dir string, depth int) error {
	for _, raw := range stmts {
		s := strings.TrimSpace(raw)
		word := strings.ToUpper(firstWord(s))
		switch {
		case word == "REM" || word == "REMARK":
			continue
		case word == "EXIT" || word == "QUIT":
			return errExit // ends this script (not sqlcli)
		case word == "HOST":
			x.steps = append(x.steps, Step{Kind: Info, Text: "HOST is not supported in sqlcli."})
			continue
		case ignored[word]:
			x.steps = append(x.steps, Step{Kind: Info, Text: word + " has no effect in sqlcli."})
			continue
		}
		s = x.subst(s)
		args := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s[len(firstWord(s)):]), ";"))
		switch {
		case word == "PROMPT" || word == "PRO":
			x.steps = append(x.steps, Step{Kind: Output, Text: strings.TrimSpace(s[len(firstWord(s)):])})
		case word == "DEFINE" || word == "DEF":
			if err := x.define(args); err != nil {
				return err
			}
		case word == "UNDEFINE" || word == "UNDEF":
			for _, n := range strings.Fields(args) {
				delete(x.env.Defines, strings.ToUpper(n))
			}
			x.steps = append(x.steps, Step{Kind: Info, Text: "UNDEFINE " + args})
		case word == "ACCEPT" || word == "ACC":
			x.accept(args)
		case word == "SET" && isSetDefine(args):
			on, err := setDefine(args)
			if err != nil {
				return err
			}
			x.env.DefineOn = on
			x.steps = append(x.steps, Step{Kind: Info, Text: map[bool]string{true: "DEFINE is ON.", false: "DEFINE is OFF."}[on]})
		case word == "SPOOL" || word == "SPO":
			x.steps = append(x.steps, Step{Kind: Spool, Text: args})
		case strings.HasPrefix(s, "@") || word == "START" || word == "STA":
			if err := x.include(s, dir, depth); err != nil {
				return err
			}
		default:
			x.steps = append(x.steps, Step{Kind: SQL, Text: s})
		}
	}
	return nil
}

func firstWord(s string) string {
	if strings.HasPrefix(s, "@@") {
		return "@@"
	}
	if strings.HasPrefix(s, "@") {
		return "@"
	}
	i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ';' })
	if i < 0 {
		return s
	}
	return s[:i]
}

// ---- DEFINE / ACCEPT / SET DEFINE ----

var reDefine = regexp.MustCompile(`^([A-Za-z0-9_$#]+)\s*=\s*(.*)$`)

func (x *expander) define(args string) error {
	switch {
	case args == "": // list all
		names := make([]string, 0, len(x.env.Defines))
		for n := range x.env.Defines {
			names = append(names, n)
		}
		sort.Strings(names)
		st := Step{Kind: Table, Columns: []string{"NAME", "VALUE"}}
		for _, n := range names {
			st.Rows = append(st.Rows, []string{n, x.env.Defines[n]})
		}
		x.steps = append(x.steps, st)
	case reDefine.MatchString(args):
		m := reDefine.FindStringSubmatch(args)
		v := strings.TrimSpace(m[2])
		if len(v) >= 2 && (v[0] == '\'' && v[len(v)-1] == '\'' || v[0] == '"' && v[len(v)-1] == '"') {
			v = v[1 : len(v)-1]
		}
		x.env.Defines[strings.ToUpper(m[1])] = v
		x.steps = append(x.steps, Step{Kind: Info, Text: fmt.Sprintf("DEFINE %s = %q", strings.ToUpper(m[1]), v)})
	default: // DEFINE name: show it
		n := strings.ToUpper(strings.Fields(args)[0])
		v, ok := x.env.Defines[n]
		if !ok {
			return fmt.Errorf("symbol %s is UNDEFINED", n)
		}
		x.steps = append(x.steps, Step{Kind: Info, Text: fmt.Sprintf("DEFINE %s = %q", n, v)})
	}
	return nil
}

var reAccept = regexp.MustCompile(`(?i)^([A-Za-z0-9_$#]+)(?:\s+(?:CHAR|NUMBER|NUM|DATE))?(?:.*?\bPROMPT\s+(?:'([^']*)'|"([^"]*)"|(\S+)))?`)

// accept handles ACCEPT name [type] [PROMPT text]: the value is asked like a
// && variable and kept as a DEFINE.
func (x *expander) accept(args string) {
	m := reAccept.FindStringSubmatch(args)
	if m == nil {
		return
	}
	name := strings.ToUpper(m[1])
	prompt := m[2] + m[3] + m[4]
	if v, ok := x.answers[name]; ok {
		x.env.Defines[name] = v
		return
	}
	x.need(Var{Name: name, Prompt: prompt, Keep: true})
}

func isSetDefine(args string) bool {
	f := strings.Fields(strings.ToUpper(args))
	return len(f) > 0 && (f[0] == "DEFINE" || f[0] == "DEF" || f[0] == "SCAN")
}

func setDefine(args string) (bool, error) {
	f := strings.Fields(strings.ToUpper(args))
	if len(f) == 2 {
		switch f[1] {
		case "ON":
			return true, nil
		case "OFF":
			return false, nil
		}
	}
	return false, fmt.Errorf("usage: SET DEFINE ON|OFF")
}

func (x *expander) need(v Var) {
	if !x.seen[v.Name] {
		x.seen[v.Name] = true
		x.missing = append(x.missing, v)
	}
}

// ---- substitution ----

// subst replaces &name and &&name (a '.' right after the name ends it). It
// works in strings too, but not in comments.
func (x *expander) subst(s string) string {
	if !x.env.DefineOn || !strings.Contains(s, "&") {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	inLine, inBlock := false, false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
			}
		case inBlock:
			if c == '*' && next == '/' {
				inBlock = false
				b.WriteString("*/")
				i++
				continue
			}
		case c == '-' && next == '-':
			inLine = true
		case c == '/' && next == '*':
			inBlock = true
		case c == '&':
			j, keep := i+1, false
			if next == '&' {
				j, keep = i+2, true
			}
			k := j
			for k < len(rs) && isName(rs[k]) {
				k++
			}
			if k == j { // "&" not followed by a name
				break
			}
			name := strings.ToUpper(string(rs[j:k]))
			if k < len(rs) && rs[k] == '.' {
				k++ // the terminating '.' is consumed
			}
			if v, ok := x.env.Defines[name]; ok {
				b.WriteString(v)
				i = k - 1
				continue
			}
			if v, ok := x.answers[name]; ok {
				if keep {
					x.env.Defines[name] = v
				}
				b.WriteString(v)
				i = k - 1
				continue
			}
			x.need(Var{Name: name, Keep: keep})
		}
		b.WriteRune(c)
	}
	return b.String()
}

func isName(r rune) bool {
	return r == '_' || r == '$' || r == '#' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// ---- scripts ----

// include runs @file [args], @@file [args] or START file [args].
func (x *expander) include(s, dir string, depth int) error {
	nested := strings.HasPrefix(s, "@@")
	rest := strings.TrimSpace(strings.TrimLeft(s, "@"))
	if !strings.HasPrefix(s, "@") {
		rest = strings.TrimSpace(s[len(firstWord(s)):])
	}
	words := splitArgs(strings.TrimSuffix(rest, ";"))
	if len(words) == 0 {
		return errors.New("usage: @file [arguments]")
	}
	if depth >= maxDepth {
		return fmt.Errorf("scripts nested more than %d deep", maxDepth)
	}
	path := words[0]
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if filepath.Ext(path) == "" {
		path += ".sql"
	}
	if !filepath.IsAbs(path) && nested && dir != "" {
		path = filepath.Join(dir, path) // @@: relative to the calling script
	}
	text, err := readScript(path)
	if err != nil {
		return fmt.Errorf("%s: %w", words[0], err)
	}
	for i, a := range words[1:] { // arguments are &1, &2 ...
		x.env.Defines[fmt.Sprint(i+1)] = a
	}
	abs, _ := filepath.Abs(path)
	err = x.run(db.SplitStatements(text), filepath.Dir(abs), depth+1)
	if errors.Is(err, errExit) {
		return nil
	}
	return err
}

// splitArgs splits a command line into words; '...' and "..." group words.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case unicode.IsSpace(r):
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func readScript(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("no such file")
		}
		return "", err
	}
	if st.IsDir() {
		return "", errors.New("it is a directory")
	}
	if st.Size() > maxScriptSize {
		return "", errors.New("file is too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", errors.New("not a text file")
	}
	return string(b), nil
}
