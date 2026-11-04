package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sqlcli/internal/db"
)

// spooler writes what a tab shows to a text file (SQL*Plus SPOOL).
type spooler struct {
	path string
	f    *os.File
}

func (s *spooler) on() bool { return s != nil && s.f != nil }

// command handles SPOOL [file [APPEND|REPLACE|CREATE] | OFF] and returns a
// message for the results pane.
func (s *spooler) command(args string) (string, error) {
	f := strings.Fields(args)
	switch {
	case len(f) == 0:
		if s.on() {
			return "Spooling to " + s.path, nil
		}
		return "Not spooling.", nil
	case strings.EqualFold(f[0], "OFF") || strings.EqualFold(f[0], "OUT"):
		if !s.on() {
			return "Not spooling.", nil
		}
		path := s.path
		err := s.f.Close()
		s.f, s.path = nil, ""
		return "Spool file closed: " + path, err
	}
	path := expandHome(strings.Trim(f[0], `'"`))
	if filepath.Ext(path) == "" {
		path += ".lst" // as SQL*Plus
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	mode := "Spooling to "
	if len(f) > 1 {
		switch strings.ToUpper(f[1]) {
		case "APPEND", "APP":
			flags, mode = os.O_CREATE|os.O_WRONLY|os.O_APPEND, "Appending to "
		case "CREATE", "CRE":
			if _, err := os.Stat(path); err == nil {
				return "", fmt.Errorf("%s exists (SPOOL file CREATE doesn't replace files)", path)
			}
		case "REPLACE", "REP":
		default:
			return "", fmt.Errorf("usage: SPOOL file [APPEND|REPLACE|CREATE] | OFF")
		}
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return "", err
	}
	if s.on() {
		s.f.Close()
	}
	s.f, s.path = file, path
	return mode + path, nil
}

func (s *spooler) write(text string) {
	if s.on() {
		fmt.Fprintln(s.f, text)
	}
}

// statement writes a statement the way SQL*Plus echoes it.
func (s *spooler) statement(sql string) {
	if !s.on() {
		return
	}
	for i, l := range strings.Split(sql, "\n") {
		if i == 0 {
			s.write("SQL> " + l)
		} else {
			s.write(fmt.Sprintf("%4d  %s", i+1, l))
		}
	}
}

// result writes a result: a query as a text table, otherwise its message.
func (s *spooler) result(res *db.Result) {
	if !s.on() || res == nil {
		return
	}
	if res.IsQuery {
		w := make([]int, len(res.Columns))
		for i, c := range res.Columns {
			w[i] = len([]rune(c))
		}
		cell := func(v string) string {
			if v == db.Null {
				return ""
			}
			return strings.ReplaceAll(v, "\n", " ")
		}
		for _, r := range res.Rows {
			for i, v := range r {
				w[i] = max(w[i], len([]rune(cell(v))))
			}
		}
		line := func(vals []string) {
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = v + strings.Repeat(" ", max(w[i]-len([]rune(v)), 0))
			}
			s.write(strings.TrimRight(strings.Join(parts, " "), " "))
		}
		s.write("")
		line(res.Columns)
		dashes := make([]string, len(w))
		for i := range w {
			dashes[i] = strings.Repeat("-", w[i])
		}
		line(dashes)
		for _, r := range res.Rows {
			vals := make([]string, len(r))
			for i, v := range r {
				vals[i] = cell(v)
			}
			line(vals)
		}
		s.write("")
	}
	if res.Message != "" {
		s.write(res.Message)
	}
	for _, e := range res.Errors {
		s.write(e)
	}
	for _, o := range res.Output {
		s.write(o)
	}
	s.write("")
}

func (s *spooler) close() {
	if s.on() {
		s.f.Close()
		s.f = nil
	}
}
