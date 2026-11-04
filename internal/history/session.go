package history

import (
	"database/sql"
	"errors"
	"time"
)

// Sessions are saved sets of SQL tabs, stored next to the history.
const sessionSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	name     TEXT    NOT NULL UNIQUE,
	saved_at INTEGER NOT NULL,           -- unix millis
	active   INTEGER NOT NULL DEFAULT 0  -- index of the active tab
);
CREATE TABLE IF NOT EXISTS session_tabs (
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	position   INTEGER NOT NULL,
	connection TEXT    NOT NULL DEFAULT '', -- saved connection name; '' = not connected
	sql        TEXT    NOT NULL DEFAULT '',
	cursor_row INTEGER NOT NULL DEFAULT 0,
	cursor_col INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (session_id, position)
);
`

// SessionTab is one saved tab.
type SessionTab struct {
	Connection string
	SQL        string
	Row, Col   int
}

// SessionInfo describes a saved session for the session list.
type SessionInfo struct {
	Name        string
	SavedAt     time.Time
	Tabs        int
	Connections string // distinct connection names, comma-separated
}

// Session is a saved session with its tabs.
type Session struct {
	Name   string
	Active int
	Tabs   []SessionTab
}

// ErrNoSession is returned for a session name that doesn't exist.
var ErrNoSession = errors.New("no such session")

// SaveSession stores tabs under name, replacing a session of that name.
func (s *Store) SaveSession(name string, active int, tabs []SessionTab) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM session_tabs WHERE session_id = (SELECT id FROM sessions WHERE name = ?)`, name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE name = ?`, name); err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO sessions(name, saved_at, active) VALUES (?, ?, ?)`, name, time.Now().UnixMilli(), active)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	for i, t := range tabs {
		if _, err := tx.Exec(`INSERT INTO session_tabs(session_id, position, connection, sql, cursor_row, cursor_col)
			VALUES (?, ?, ?, ?, ?, ?)`, id, i, t.Connection, t.SQL, t.Row, t.Col); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SessionExists reports whether a session of that name is saved.
func (s *Store) SessionExists(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sessions WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

// Sessions lists saved sessions, most recently saved first.
func (s *Store) Sessions() ([]SessionInfo, error) {
	rows, err := s.db.Query(`
		SELECT s.name, s.saved_at, count(t.position),
		       coalesce((SELECT group_concat(c, ', ') FROM
		                   (SELECT DISTINCT connection c FROM session_tabs
		                     WHERE session_id = s.id AND connection <> '' ORDER BY connection)), '')
		  FROM sessions s LEFT JOIN session_tabs t ON t.session_id = s.id
		 GROUP BY s.id ORDER BY s.saved_at DESC, s.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var ts int64
		if err := rows.Scan(&si.Name, &ts, &si.Tabs, &si.Connections); err != nil {
			return nil, err
		}
		si.SavedAt = time.UnixMilli(ts)
		out = append(out, si)
	}
	return out, rows.Err()
}

// LoadSession returns a saved session with its tabs in order.
func (s *Store) LoadSession(name string) (Session, error) {
	se := Session{Name: name}
	var id int64
	err := s.db.QueryRow(`SELECT id, active FROM sessions WHERE name = ?`, name).Scan(&id, &se.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return se, ErrNoSession
	}
	if err != nil {
		return se, err
	}
	rows, err := s.db.Query(`SELECT connection, sql, cursor_row, cursor_col FROM session_tabs
		WHERE session_id = ? ORDER BY position`, id)
	if err != nil {
		return se, err
	}
	defer rows.Close()
	for rows.Next() {
		var t SessionTab
		if err := rows.Scan(&t.Connection, &t.SQL, &t.Row, &t.Col); err != nil {
			return se, err
		}
		se.Tabs = append(se.Tabs, t)
	}
	return se, rows.Err()
}

// DeleteSession removes a saved session.
func (s *Store) DeleteSession(name string) error {
	if _, err := s.db.Exec(`DELETE FROM session_tabs WHERE session_id = (SELECT id FROM sessions WHERE name = ?)`, name); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE name = ?`, name)
	return err
}
