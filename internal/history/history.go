// Package history stores executed SQL statements in a SQLite database.
package history

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Entry struct {
	ID         int64
	Connection string
	SQL        string
	ExecutedAt time.Time
	DurationMS int64
	Rows       int64
	Error      string
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS history (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	connection  TEXT    NOT NULL,
	sql         TEXT    NOT NULL,
	executed_at INTEGER NOT NULL,  -- unix millis
	duration_ms INTEGER NOT NULL DEFAULT 0,
	rows        INTEGER NOT NULL DEFAULT 0,
	error       TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS history_executed_at ON history(executed_at DESC);
`

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Add(e Entry) error {
	_, err := s.db.Exec(
		`INSERT INTO history(connection, sql, executed_at, duration_ms, rows, error) VALUES (?,?,?,?,?,?)`,
		e.Connection, e.SQL, e.ExecutedAt.UnixMilli(), e.DurationMS, e.Rows, e.Error)
	return err
}

// Recent returns up to limit entries, newest first, optionally filtered by a
// case-insensitive substring of the SQL text.
func (s *Store) Recent(limit int, filter string) ([]Entry, error) {
	rows, err := s.db.Query(
		`SELECT id, connection, sql, executed_at, duration_ms, rows, error
		   FROM history
		  WHERE ? = '' OR sql LIKE '%' || ? || '%'
		  ORDER BY id DESC LIMIT ?`, filter, filter, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ts int64
		if err := rows.Scan(&e.ID, &e.Connection, &e.SQL, &ts, &e.DurationMS, &e.Rows, &e.Error); err != nil {
			return nil, err
		}
		e.ExecutedAt = time.UnixMilli(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM history WHERE id = ?`, id)
	return err
}
