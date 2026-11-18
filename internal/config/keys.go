package config

import (
	"fmt"
	"sort"
	"strings"
)

// Configurable keys: the global commands (F-keys, Ctrl-S, tab and panel
// navigation). [keys] in the config maps an action to one key or a list
// of keys, named as sqlcli reports them (SQLCLI_DEBUG_KEYS=1): "f5",
// "ctrl+s", "alt+r", "ctrl+shift+up" ... A configured action loses its
// default keys.
//
//	[keys]
//	  run = ["f5", "ctrl+r"]
//	  commit = "ctrl+t"

// KeyAction is a configurable command with its default keys.
type KeyAction struct {
	Name     string
	Defaults []string
	Help     string
}

// KeyActions are the configurable commands, in help order.
var KeyActions = []KeyAction{
	{"help", []string{"f1"}, "help screen"},
	{"connections", []string{"f2"}, "show / hide the connections panel"},
	{"results", []string{"f3"}, "show / hide the results panel"},
	{"sql", []string{"f4"}, "show / hide the SQL panel"},
	{"run", []string{"f5", "ctrl+enter", "alt+enter"}, "run the statement at the cursor (or the block)"},
	{"history", []string{"f6"}, "SQL history"},
	{"run_all", []string{"f7"}, "run the whole SQL panel"},
	{"rollback", []string{"f8"}, "rollback"},
	{"commit", []string{"f9"}, "commit"},
	{"sessions", []string{"f10"}, "session window"},
	{"save_session", []string{"ctrl+s"}, "save the session"},
	{"object_tree", []string{"f12"}, "jump to the object tree"},
	{"next_tab", []string{"ctrl+pgdown"}, "next SQL tab"},
	{"prev_tab", []string{"ctrl+pgup"}, "previous SQL tab"},
	{"panel_left", []string{"ctrl+left"}, "panel to the left"},
	{"panel_right", []string{"ctrl+right"}, "panel to the right"},
	{"panel_up", []string{"ctrl+up"}, "panel above"},
	{"panel_down", []string{"ctrl+down"}, "panel below"},
	{"prev_panel", []string{"shift+tab"}, "previous panel"},
	{"cancel", []string{"ctrl+c"}, "cancel the running statement"},
}

// KeyList is one key or a list of keys in the config.
type KeyList []string

func (k *KeyList) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case string:
		*k = KeyList{x}
	case []any:
		*k = nil
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("keys must be strings, not %v", e)
			}
			*k = append(*k, s)
		}
	default:
		return fmt.Errorf("a key is a string or a list of strings, not %v", v)
	}
	return nil
}

func (k KeyList) MarshalTOML() ([]byte, error) {
	q := make([]string, len(k))
	for i, s := range k {
		q[i] = fmt.Sprintf("%q", s)
	}
	if len(q) == 1 {
		return []byte(q[0]), nil
	}
	return []byte("[" + strings.Join(q, ", ") + "]"), nil
}

// normKey writes a key as Bubble Tea names it: lower case, "+" between
// the parts ("Ctrl-S", "ctrl+S" -> "ctrl+s"; "F5" -> "f5").
func normKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "++", "+-") // "ctrl+-" written as "ctrl--"
	return s
}

func findAction(name string) (KeyAction, bool) {
	for _, a := range KeyActions {
		if a.Name == name {
			return a, true
		}
	}
	return KeyAction{}, false
}

// KeyMap returns how pressed keys translate to the actions' default keys:
// a configured key maps to its action's first default key, a default key
// the config took away maps to "" (no command). Keys not in the map keep
// their default meaning.
func (c *Config) KeyMap() (map[string]string, error) {
	m := map[string]string{}
	owner := map[string]string{} // configured key -> action
	names := make([]string, 0, len(c.Keys))
	for n := range c.Keys {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a, ok := findAction(n)
		if !ok {
			var valid []string
			for _, a := range KeyActions {
				valid = append(valid, a.Name)
			}
			return nil, fmt.Errorf("[keys] unknown action %q; actions: %s", n, strings.Join(valid, ", "))
		}
		if len(c.Keys[n]) == 0 {
			return nil, fmt.Errorf("[keys] %s: no key given", n)
		}
		for _, k := range a.Defaults { // replaced defaults do nothing, unless another action takes them
			if _, taken := owner[k]; !taken {
				m[k] = ""
			}
		}
		for _, raw := range c.Keys[n] {
			k := normKey(raw)
			if k == "" {
				return nil, fmt.Errorf("[keys] %s: empty key", n)
			}
			if o, dup := owner[k]; dup && o != n {
				return nil, fmt.Errorf("[keys] %q is given to both %s and %s", raw, o, n)
			}
			owner[k] = n
			m[k] = a.Defaults[0]
		}
	}
	return m, nil
}

// KeysFor returns the keys of an action: the configured ones or the defaults.
func (c *Config) KeysFor(action string) []string {
	if ks, ok := c.Keys[action]; ok && len(ks) > 0 {
		out := make([]string, len(ks))
		for i, k := range ks {
			out[i] = normKey(k)
		}
		return out
	}
	a, _ := findAction(action)
	return a.Defaults
}

// KeyLabel writes a key for the screen: "f5" -> "F5", "ctrl+s" -> "^S",
// "alt+r" -> "Alt-R", "ctrl+shift+up" -> "Ctrl-Shift-Up".
func KeyLabel(k string) string {
	parts := strings.Split(k, "+")
	for i, p := range parts {
		switch {
		case len(p) == 1:
			parts[i] = strings.ToUpper(p)
		case p == "pgup":
			parts[i] = "PgUp"
		case p == "pgdown":
			parts[i] = "PgDn"
		default:
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	if len(parts) == 2 && parts[0] == "Ctrl" && len(parts[1]) == 1 {
		return "^" + parts[1]
	}
	return strings.Join(parts, "-")
}
