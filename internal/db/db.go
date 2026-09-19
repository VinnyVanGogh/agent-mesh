package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const Schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    account_key   TEXT UNIQUE,
    email_domain  TEXT UNIQUE,
    role          TEXT NOT NULL CHECK (role IN ('work', 'personal', 'other')),
    label         TEXT NOT NULL,
    plan_tier     TEXT NOT NULL,
    notes         TEXT,
    registered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS quota_windows (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    pool_key      TEXT NOT NULL UNIQUE,
    window_type   TEXT NOT NULL CHECK (window_type IN ('rolling_5h', 'weekly_7d')),
    used_percent  REAL NOT NULL DEFAULT 0.0,
    remaining_pct REAL NOT NULL DEFAULT 100.0,
    is_locked     INTEGER NOT NULL DEFAULT 0,
    resets_at     TEXT,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    repo_path    TEXT NOT NULL,
    git_branch   TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'soft_deleted')),
    account_role TEXT NOT NULL DEFAULT 'work',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at   TEXT
);
`

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(Schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}

	return &Store{db: conn}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}
