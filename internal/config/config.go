// Package config loads and saves the application configuration as TOML.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Connection describes one saved Oracle connection.
type Connection struct {
	Name          string `toml:"name"`
	User          string `toml:"user"`
	Password      string `toml:"password,omitempty"`
	PasswordEnv   string `toml:"password_env,omitempty"` // read password from this env var
	ConnectString string `toml:"connect_string"`         // host:port/service, TNS alias, or full descriptor
	SysDBA        bool   `toml:"sysdba,omitempty"`
	ReadOnly      bool   `toml:"read_only,omitempty"` // refuse DML/DDL
}

// ResolvedPassword returns the password, preferring PasswordEnv when set.
func (c Connection) ResolvedPassword() string {
	if c.PasswordEnv != "" {
		if v, ok := os.LookupEnv(c.PasswordEnv); ok {
			return v
		}
	}
	return c.Password
}

type Editor struct {
	TabWidth        int  `toml:"tab_width"`
	AutoIndent      bool `toml:"auto_indent"`
	SyntaxHighlight Flag `toml:"syntax_highlight"` // colour SQL keywords, strings, comments ...
	Completion      Flag `toml:"completion"`       // code completion (Ctrl-Space, or while typing)
}

// Flag is an on/off setting. Besides TOML's true / false it accepts the
// strings "yes" / "no" and "on" / "off".
type Flag bool

func (f *Flag) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case bool:
		*f = Flag(x)
		return nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "yes", "y", "true", "on", "1":
			*f = true
			return nil
		case "no", "n", "false", "off", "0":
			*f = false
			return nil
		}
		return fmt.Errorf("%q is not a yes/no value (use true, false, \"yes\" or \"no\")", x)
	}
	return fmt.Errorf("%v is not a yes/no value (use true, false, \"yes\" or \"no\")", v)
}

func (f Flag) MarshalTOML() ([]byte, error) {
	if f {
		return []byte("true"), nil
	}
	return []byte("false"), nil
}

type Query struct {
	MaxRows        int  `toml:"max_rows"`        // rows fetched per batch; more are fetched on scroll
	FetchAll       bool `toml:"fetch_all"`       // fetch all rows of every query right away
	TimeoutSeconds int  `toml:"timeout_seconds"` // 0 = no timeout
	AutoCommit     bool `toml:"auto_commit"`
	DBMSOutput     bool `toml:"dbms_output"` // show DBMS_OUTPUT in the results pane
}

type UI struct {
	Mouse             bool   `toml:"mouse"`                // mouse wheel scrolls panes (hold Shift to select text)
	ShowConnections   bool   `toml:"show_connections"`     // connections panel visible at start (F2 toggles)
	RecordPage        int    `toml:"record_page"`          // records PgUp/PgDn move in the record view
	SaveSessionOnExit Flag   `toml:"save_session_on_exit"` // save the current session when quitting
	SelectionColor    string `toml:"selection_color"`      // background of the selected result row: 0-255 or "#rrggbb"
}

type History struct {
	Path       string `toml:"path,omitempty"` // empty = default location
	MaxEntries int    `toml:"max_entries"`    // shown in the history browser
}

type Oracle struct {
	// LibDir is the Oracle Instant Client directory. Empty = $ORACLE_HOME.
	LibDir string `toml:"lib_dir,omitempty"`
}

type Config struct {
	Oracle      Oracle       `toml:"oracle"`
	Editor      Editor       `toml:"editor"`
	Query       Query        `toml:"query"`
	History     History      `toml:"history"`
	UI          UI           `toml:"ui"`
	Connections []Connection `toml:"connections"`

	path string
}

func Default() *Config {
	return &Config{
		Editor:  Editor{TabWidth: 4, AutoIndent: true, SyntaxHighlight: true, Completion: true},
		Query:   Query{MaxRows: 1000, TimeoutSeconds: 0, AutoCommit: false, DBMSOutput: true},
		History: History{MaxEntries: 500},
		UI:      UI{Mouse: true, ShowConnections: true, RecordPage: 10, SaveSessionOnExit: true, SelectionColor: "24"},
	}
}

// DefaultPath returns $XDG_CONFIG_HOME/sqlcli/config.toml (or ~/.config/...).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sqlcli", "config.toml"), nil
}

// DefaultHistoryPath returns $XDG_STATE_HOME/sqlcli/history.db (or ~/.local/state/...).
func DefaultHistoryPath() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "sqlcli", "history.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "sqlcli", "history.db"), nil
}

// Load reads the config at path. A missing file creates one with defaults.
func Load(path string) (*Config, error) {
	cfg := Default()
	cfg.path = path
	_, err := toml.DecodeFile(path, cfg)
	if errors.Is(err, fs.ErrNotExist) {
		cfg.Connections = []Connection{{
			Name:          "example",
			User:          "scott",
			PasswordEnv:   "SQLCLI_PASSWORD",
			ConnectString: "localhost:1521/FREEPDB1",
		}}
		return cfg, cfg.Save()
	}
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.Editor.TabWidth <= 0 {
		cfg.Editor.TabWidth = 4
	}
	if cfg.Query.MaxRows <= 0 {
		cfg.Query.MaxRows = 1000
	}
	if cfg.History.MaxEntries <= 0 {
		cfg.History.MaxEntries = 500
	}
	if cfg.UI.RecordPage <= 0 {
		cfg.UI.RecordPage = 10
	}
	if !validColor(cfg.UI.SelectionColor) {
		return nil, fmt.Errorf("config %s: selection_color %q: use a number 0-255 or \"#rrggbb\"", path, cfg.UI.SelectionColor)
	}
	return cfg, nil
}

func (c *Config) Path() string { return c.path }

// Save writes the config back to its file (mode 0600, it may hold passwords).
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("# sqlcli configuration\n\n"); err != nil {
		f.Close()
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// OracleLibDir returns the Instant Client directory to use, or "".
func (c *Config) OracleLibDir() string {
	if c.Oracle.LibDir != "" {
		return c.Oracle.LibDir
	}
	return os.Getenv("ORACLE_HOME")
}

var reHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// validColor accepts "", a 256-colour number or "#rrggbb".
func validColor(c string) bool {
	c = strings.TrimSpace(c)
	if c == "" || reHexColor.MatchString(c) {
		return true
	}
	n, err := strconv.Atoi(c)
	return err == nil && n >= 0 && n <= 255
}
