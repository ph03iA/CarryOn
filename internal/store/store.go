// Package store keeps all CarryOn state in SQLite so a restart resumes safely.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schemaVersion = 4

// DB is the SQLite-backed state store.
type DB struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS snapshots (
  provider   TEXT PRIMARY KEY,
  data       TEXT NOT NULL,
  observed_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS incidents (
  id           TEXT PRIMARY KEY,
  provider     TEXT NOT NULL,
  account_id   TEXT NOT NULL,
  window_id    TEXT NOT NULL,
  window_label TEXT NOT NULL,
  used_percent REAL NOT NULL,
  reset_at     INTEGER NOT NULL,
  detected_at  INTEGER NOT NULL,
  state        TEXT NOT NULL,
  session_id   TEXT NOT NULL DEFAULT '',
  session_path TEXT NOT NULL DEFAULT '',
  session_cwd  TEXT NOT NULL DEFAULT '',
  cut_off_at   INTEGER NOT NULL DEFAULT 0,
  cut_off_offset INTEGER NOT NULL DEFAULT 0,
  window_duration INTEGER NOT NULL DEFAULT 0,
  simulated    INTEGER NOT NULL DEFAULT 0,
  reason       TEXT NOT NULL DEFAULT '',
  updated_at   INTEGER NOT NULL,
  UNIQUE(provider, account_id, window_id, reset_at)
);
CREATE INDEX IF NOT EXISTS incidents_state ON incidents(state, reset_at);

CREATE TABLE IF NOT EXISTS resumes (
  id            TEXT PRIMARY KEY,
  incident_id   TEXT NOT NULL,
  provider      TEXT NOT NULL,
  session_id    TEXT NOT NULL,
  cwd           TEXT NOT NULL,
  attempts      INTEGER NOT NULL DEFAULT 0,
  state         TEXT NOT NULL,
  prompt        TEXT NOT NULL DEFAULT '',
  decision_id   TEXT NOT NULL DEFAULT '',
  exit_code     INTEGER NOT NULL DEFAULT 0,
  started_at    INTEGER NOT NULL DEFAULT 0,
  finished_at   INTEGER NOT NULL DEFAULT 0,
  last_message  TEXT NOT NULL DEFAULT '',
  files_changed TEXT NOT NULL DEFAULT '',
  simulated     INTEGER NOT NULL DEFAULT 0,
  updated_at    INTEGER NOT NULL,
  UNIQUE(provider, session_id, incident_id)
);
CREATE INDEX IF NOT EXISTS resumes_state ON resumes(state, updated_at);

CREATE TABLE IF NOT EXISTS emails (
  id            TEXT PRIMARY KEY,
  event_key     TEXT NOT NULL UNIQUE,
  kind          TEXT NOT NULL,
  subject       TEXT NOT NULL,
  body          TEXT NOT NULL,
  state         TEXT NOT NULL DEFAULT 'pending',
  attempts      INTEGER NOT NULL DEFAULT 0,
  first_attempt INTEGER NOT NULL DEFAULT 0,
  next_attempt  INTEGER NOT NULL,
  lease_until   INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT NOT NULL DEFAULT '',
  simulated     INTEGER NOT NULL DEFAULT 0,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS emails_due ON emails(state, next_attempt);

CREATE TABLE IF NOT EXISTS decisions (
  id          TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL,
  resume_id   TEXT NOT NULL DEFAULT '',
  stage       TEXT NOT NULL,
  action      TEXT NOT NULL,
  reason      TEXT NOT NULL,
  prompt      TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL,
  input_chars INTEGER NOT NULL DEFAULT 0,
  redactions  INTEGER NOT NULL DEFAULT 0,
  fallback    INTEGER NOT NULL DEFAULT 0,
  simulated   INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS decisions_incident ON decisions(incident_id, created_at);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// Open opens or creates the database at path.
func Open(path string) (*DB, error) {
	if err := ensureFile(path); err != nil {
		return nil, err
	}
	dsn := fileURL(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func ensureFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("state database must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create state database: %w", err)
	}
	return file.Close()
}

func fileURL(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	target := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(absolute), "/")}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "foreign_keys(ON)")
	target.RawQuery = query.Encode()
	return target.String()
}

func migrate(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return errors.New("state database is from a newer CarryOn; upgrade this binary")
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

// Close releases the database handle.
func (d *DB) Close() error { return d.db.Close() }

// unix converts a time to the stored integer form.
func unix(t time.Time) int64 { return t.UTC().Unix() }

// fromUnix converts a stored integer back to UTC time, preserving zero.
func fromUnix(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

// boolInt maps a boolean to SQLite's integer representation.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// isNoRows reports whether a query matched no rows.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
