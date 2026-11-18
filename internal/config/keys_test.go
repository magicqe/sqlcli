package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadString(t *testing.T, s string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(s), 0o600)
	return Load(p)
}

func TestKeysConfig(t *testing.T) {
	cfg, err := loadString(t, `
[keys]
  run = ["F5", "Ctrl-R"]
  commit = "ctrl+t"
  sessions = "f9"   # takes F9 from commit
`)
	if err != nil {
		t.Fatal(err)
	}
	km, _ := cfg.KeyMap()
	want := map[string]string{
		"f5": "f5", "ctrl+r": "f5", "ctrl+enter": "", "alt+enter": "", // run: only F5 and Ctrl-R
		"ctrl+t": "f9", "f9": "f10", "f10": "", // commit on Ctrl-T, sessions on F9
	}
	for k, v := range want {
		if got, ok := km[k]; !ok || got != v {
			t.Errorf("%s -> %q (%v), want %q", k, got, ok, v)
		}
	}
	if _, ok := km["f1"]; ok {
		t.Error("unconfigured keys stay out of the map")
	}
	if got := strings.Join(cfg.KeysFor("run"), ","); got != "f5,ctrl+r" || cfg.KeysFor("help")[0] != "f1" {
		t.Fatalf("KeysFor: %s", got)
	}
	for k, want := range map[string]string{"f5": "F5", "ctrl+s": "^S", "alt+r": "Alt-R", "ctrl+pgdown": "Ctrl-PgDn", "shift+tab": "Shift-Tab"} {
		if got := KeyLabel(k); got != want {
			t.Errorf("KeyLabel(%s) = %s", k, got)
		}
	}
	// saving keeps the section
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := Load(cfg.Path())
	if err != nil || strings.Join(again.KeysFor("run"), ",") != "f5,ctrl+r" || again.KeysFor("commit")[0] != "ctrl+t" {
		t.Fatalf("after save: %v %v", err, again.Keys)
	}
}

func TestKeysConfigErrors(t *testing.T) {
	for cfg, want := range map[string]string{
		"[keys]\n run_query = \"f5\"":             `unknown action "run_query"`,
		"[keys]\n run = \"f5\"\n commit = \"F5\"": `is given to both`,
		"[keys]\n run = []":                       "no key given",
		"[keys]\n run = 5":                        "string or a list",
	} {
		if _, err := loadString(t, cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", cfg, err, want)
		}
	}
}
