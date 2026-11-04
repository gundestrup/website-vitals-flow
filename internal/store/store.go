// Package store owns the SQLite state database: schema, migrations,
// site/run/job access. WAL mode, busy_timeout, single writer —
// SQLite allows one writer; all writes funnel through *Store.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schemaVersion = 4

const schemaV1 = `
CREATE TABLE IF NOT EXISTS sites (
  id           INTEGER PRIMARY KEY,
  key          TEXT NOT NULL UNIQUE,
  url          TEXT NOT NULL,
  label        TEXT NOT NULL DEFAULT '',
  schedule     TEXT NOT NULL DEFAULT '',
  keep_last    INTEGER NOT NULL DEFAULT 100,
  max_age_days INTEGER NOT NULL DEFAULT 56,
  created_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  id              INTEGER PRIMARY KEY,
  site_id         INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  engine          TEXT NOT NULL DEFAULT 'cdp',
  trigger         TEXT NOT NULL DEFAULT 'cli',
  status          TEXT NOT NULL DEFAULT 'ok',
  error           TEXT NOT NULL DEFAULT '',
  started_at      TEXT NOT NULL,
  finished_at     TEXT NOT NULL,
  browser_version TEXT NOT NULL DEFAULT '',
  ttfb_ms REAL, fcp_ms REAL, lcp_ms REAL, cls REAL, tbt_ms REAL,
  request_count   INTEGER NOT NULL DEFAULT 0,
  page_bytes      INTEGER NOT NULL DEFAULT 0,
  http_protocol   TEXT NOT NULL DEFAULT '',
  detail_json     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_runs_site ON runs(site_id, started_at DESC);
CREATE TABLE IF NOT EXISTS jobs (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL,
  site_id     INTEGER REFERENCES sites(id) ON DELETE CASCADE,
  state       TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  queued_at   TEXT NOT NULL,
  started_at  TEXT,
  finished_at TEXT
);`

// Store wraps the single write connection.
type Store struct {
	db *sql.DB
}

// Open opens (creating + migrating) the state db at path.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if v > schemaVersion {
		return fmt.Errorf("state db schema v%d newer than binary supports (v%d) — upgrade wvf", v, schemaVersion)
	}
	if v < 1 {
		if _, err := s.db.ExecContext(ctx, schemaV1); err != nil {
			return fmt.Errorf("schema v1: %w", err)
		}
	}
	if v < 2 {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE jobs ADD COLUMN run_id INTEGER`); err != nil {
			return fmt.Errorf("schema v2: %w", err)
		}
	}
	if v < 3 {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE jobs ADD COLUMN deep INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("schema v3: %w", err)
		}
	}
	if v < 4 {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE sites ADD COLUMN deep INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("schema v4: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return fmt.Errorf("stamp user_version: %w", err)
	}
	return nil
}
