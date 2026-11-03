package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
)

// connModel loads a real config file so edits are saved to disk.
func connModel(t *testing.T) (*Model, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(`
[[connections]]
  name = "ora"
  user = "scott"
  password_env = "SQLCLI_TEST_ENV_PW"
  connect_string = "dbhost:1521/ORCL"

[[connections]]
  name = "rep"
  user = "report"
  password = "secret"
  connect_string = "REPDB"
  read_only = true
`), 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	m := New(cfg, nil, "ora")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.focus = focusConns
	return m, p
}

func reload(t *testing.T, p string) *config.Config {
	t.Helper()
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEditConnection(t *testing.T) {
	m, p := connModel(t)
	press(m, key('e'))
	if m.mode != modePrompt || string(m.prompt.input) != "ora" {
		t.Fatalf("name prompt: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEnter)) // keep name
	answer(m, "hr")             // new user
	if string(m.prompt.input) != "" && m.prompt.label != "Connect string:" {
		t.Fatalf("unexpected prompt %q", m.prompt.label)
	}
	press(m, key(tea.KeyEnter)) // keep connect string
	if !m.prompt.mask {
		t.Fatal("password prompt must hide input")
	}
	press(m, key(tea.KeyEnter)) // keep password
	if string(m.prompt.input) != "SQLCLI_TEST_ENV_PW" {
		t.Fatalf("env var prompt should show the current one: %q", string(m.prompt.input))
	}
	press(m, key(tea.KeyEnter)) // keep env var
	answer(m, "yes")            // read-only
	press(m, key(tea.KeyEnter)) // keep SYSDBA (no)
	if m.mode != modeNormal || !strings.Contains(m.status, `Connection "ora" saved`) {
		t.Fatalf("not saved: mode=%v status=%q", m.mode, m.status)
	}
	c := reload(t, p).Connections[0]
	if c.Name != "ora" || c.User != "hr" || c.ConnectString != "dbhost:1521/ORCL" || c.PasswordEnv != "SQLCLI_TEST_ENV_PW" || !c.ReadOnly || c.SysDBA {
		t.Fatalf("saved: %+v", c)
	}
	if !strings.Contains(screen(m), "ora (ro)") {
		t.Fatal("panel should show the connection as read-only")
	}
}

func TestEditConnectionPasswordAndRename(t *testing.T) {
	m, p := connModel(t)
	press(m, key(tea.KeyDown)) // "rep"
	press(m, key('e'))
	answer(m, "reporting")
	press(m, key(tea.KeyEnter))
	press(m, key(tea.KeyEnter))
	typeText(m, "n3w")
	if strings.Contains(screen(m), "n3w") {
		t.Fatal("password shown on screen")
	}
	press(m, key(tea.KeyEnter)) // typed password: no env var question
	answer(m, "no")             // read-only off
	answer(m, "y")              // SYSDBA on
	c := reload(t, p).Connections[1]
	if c.Name != "reporting" || c.Password != "n3w" || c.PasswordEnv != "" || c.ReadOnly || !c.SysDBA {
		t.Fatalf("saved: %+v", c)
	}
}

func TestEditConnectionRefusedOrCancelled(t *testing.T) {
	m, p := connModel(t)
	before, _ := os.ReadFile(p)

	press(m, key('e'))
	answer(m, "REP") // name of the other connection (any case)
	if !m.statusErr || !strings.Contains(m.status, "already exists") || m.mode != modeNormal {
		t.Fatalf("duplicate name: %q", m.status)
	}

	press(m, key('e'))
	press(m, key(tea.KeyEnter))
	answer(m, "changed")
	press(m, key(tea.KeyEscape)) // cancel at the connect string
	if m.mode != modeNormal || m.cfg.Connections[0].User != "scott" {
		t.Fatalf("Esc should cancel: user=%q", m.cfg.Connections[0].User)
	}

	press(m, key('e'))
	for i := 0; i < 5; i++ { // name, user, connect string, password, env var
		press(m, key(tea.KeyEnter))
	}
	if !strings.HasPrefix(m.prompt.label, "Read-only?") {
		t.Fatalf("expected the read-only question, got %q", m.prompt.label)
	}
	answer(m, "maybe")
	if !m.statusErr || !strings.Contains(m.status, "not yes or no") {
		t.Fatalf("bad yes/no: %q", m.status)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatal("config file changed although every edit was refused or cancelled")
	}
}

func TestViewConnection(t *testing.T) {
	m, _ := connModel(t)
	os.Setenv("SQLCLI_TEST_ENV_PW", "x")
	defer os.Unsetenv("SQLCLI_TEST_ENV_PW")
	press(m, key('v'))
	s := screen(m)
	for _, want := range []string{"Connection ora", "scott", "dbhost:1521/ORCL", "(Easy Connect)",
		"environment variable SQLCLI_TEST_ENV_PW (set)", "Read-only", "not connected", "config.toml"} {
		if !strings.Contains(s, want) {
			t.Fatalf("view lacks %q:\n%s", want, s)
		}
	}
	press(m, key('q'))
	if m.mode != modeNormal {
		t.Fatal("any key should close the view")
	}
	press(m, key(tea.KeyDown))
	press(m, key('v'))
	s = screen(m)
	if !strings.Contains(s, "stored in the config file (hidden)") || strings.Contains(s, "secret") || !strings.Contains(s, "TNS alias") {
		t.Fatalf("rep view:\n%s", s)
	}
}
