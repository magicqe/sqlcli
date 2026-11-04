package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlcli/internal/history"
)

// setup writes a config whose history database is in a temp dir.
func setup(t *testing.T) (cfgPath, histPath string) {
	t.Helper()
	dir := t.TempDir()
	histPath = filepath.Join(dir, "history.db")
	cfgPath = filepath.Join(dir, "config.toml")
	cfg := "[history]\n  path = \"" + histPath + "\"\n\n[[connections]]\n  name = \"ora\"\n  connect_string = \"x\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, histPath
}

func TestListSessions(t *testing.T) {
	cfgPath, histPath := setup(t)
	var out bytes.Buffer
	if err := run(options{cfgPath: cfgPath, listSessions: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No saved sessions") {
		t.Fatalf("empty list: %q", out.String())
	}

	h, err := history.Open(histPath)
	if err != nil {
		t.Fatal(err)
	}
	h.SaveSession("daily reports", 0, []history.SessionTab{{Connection: "ora", SQL: "a"}, {Connection: "rep", SQL: "b"}, {SQL: "c"}})
	h.SaveSession("scratch", 0, []history.SessionTab{{SQL: "x"}})
	h.Close()

	out.Reset()
	if err := run(options{cfgPath: cfgPath, listSessions: true}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("table:\n%s", out.String())
	}
	if !strings.HasPrefix(lines[1], "scratch") || !strings.HasSuffix(lines[1], "-") { // newest first, no connections
		t.Fatalf("row 1: %q", lines[1])
	}
	f := strings.Fields(lines[2])
	if lines[2][:13] != "daily reports" || f[len(f)-2] != "ora," || f[len(f)-1] != "rep" || f[len(f)-3] != "3" {
		t.Fatalf("row 2: %q", lines[2])
	}
}

func TestStartOptionErrors(t *testing.T) {
	cfgPath, _ := setup(t)
	err := run(options{cfgPath: cfgPath, sessionName: "nope"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `no session named "nope"`) || !strings.Contains(err.Error(), "sqlcli -l") {
		t.Fatalf("unknown session: %v", err)
	}
	err = run(options{cfgPath: cfgPath, sessionName: "x", connName: "ora"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "either -c or -s") {
		t.Fatalf("-c with -s: %v", err)
	}
	err = run(options{cfgPath: cfgPath, connName: "missing"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `no connection named "missing"`) {
		t.Fatalf("unknown connection: %v", err)
	}
}
