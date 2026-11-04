package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordPage(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		toml string
		want int
	}{
		{"[ui]\nrecord_page = 25\n", 25},
		{"[ui]\nrecord_page = 0\n", 10},  // invalid: default
		{"[ui]\nrecord_page = -5\n", 10}, // invalid: default
		{"[query]\nmax_rows = 50\n", 10}, // missing: default
	} {
		p := filepath.Join(dir, "c.toml")
		if err := os.WriteFile(p, []byte(c.toml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.UI.RecordPage != c.want {
			t.Errorf("%q: record_page = %d, want %d", c.toml, cfg.UI.RecordPage, c.want)
		}
		if !cfg.UI.Mouse || !cfg.UI.ShowConnections {
			t.Errorf("%q: other [ui] defaults lost", c.toml)
		}
	}
}

func TestNewConfigFileHasDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if cfg.UI.RecordPage != 10 || !contains(string(b), "record_page = 10") {
		t.Fatalf("new config file:\n%s", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v, want 0600", st.Mode().Perm())
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestSyntaxHighlightFlag(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		val  string
		want bool
		bad  bool
	}{
		{"true", true, false},
		{"false", false, false},
		{`"yes"`, true, false},
		{`"no"`, false, false},
		{`"YES"`, true, false},
		{`"on"`, true, false},
		{`"off"`, false, false},
		{`"maybe"`, false, true},
		{"", true, false}, // not set: default on
	} {
		p := filepath.Join(dir, "c.toml")
		body := "[editor]\n"
		if c.val != "" {
			body += "syntax_highlight = " + c.val + "\n"
		}
		_ = os.WriteFile(p, []byte(body), 0o600)
		cfg, err := Load(p)
		if c.bad {
			if err == nil || !strings.Contains(err.Error(), "yes/no") {
				t.Errorf("%s: want an error, got %v", c.val, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.val, err)
		}
		if bool(cfg.Editor.SyntaxHighlight) != c.want {
			t.Errorf("%s: got %v, want %v", c.val, cfg.Editor.SyntaxHighlight, c.want)
		}
	}

	// saving writes a plain TOML boolean
	cfg := Default()
	cfg.path = filepath.Join(dir, "saved.toml")
	cfg.Editor.SyntaxHighlight = false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg.path)
	if !strings.Contains(string(b), "syntax_highlight = false") {
		t.Fatalf("saved:\n%s", b)
	}
}

func TestSaveSessionOnExitOption(t *testing.T) {
	dir := t.TempDir()
	for val, want := range map[string]bool{"": true, "true": true, "false": false, `"yes"`: true, `"no"`: false} {
		p := filepath.Join(dir, "c.toml")
		body := "[ui]\n"
		if val != "" {
			body += "save_session_on_exit = " + val + "\n"
		}
		os.WriteFile(p, []byte(body), 0o600)
		cfg, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if bool(cfg.UI.SaveSessionOnExit) != want {
			t.Errorf("%q: got %v, want %v", val, cfg.UI.SaveSessionOnExit, want)
		}
	}
}
