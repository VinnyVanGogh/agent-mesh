package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
    pool_key      TEXT NOT NULL,
    window_type   TEXT NOT NULL CHECK (window_type IN ('rolling_5h', 'weekly_7d', 'monthly')),
    used_percent  REAL NOT NULL DEFAULT 0.0,
    remaining_pct REAL NOT NULL DEFAULT 100.0,
    is_locked     INTEGER NOT NULL DEFAULT 0,
    resets_at     TEXT,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (pool_key, window_type)
);

-- Per-provider fetch bookkeeping: throttle (next_attempt_at) and last outcome.
CREATE TABLE IF NOT EXISTS quota_fetch_state (
    provider        TEXT PRIMARY KEY,
    last_attempt_at TEXT NOT NULL,
    next_attempt_at TEXT NOT NULL,
    last_success_at TEXT,
    last_status     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    repo_path      TEXT NOT NULL,
    git_branch     TEXT,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done', 'soft_deleted')),
    account_role   TEXT NOT NULL DEFAULT 'work',
    max_budget_usd REAL NOT NULL DEFAULT 0.0,
    max_turns      INTEGER NOT NULL DEFAULT 0,
    spent_tokens   INTEGER NOT NULL DEFAULT 0,
    spent_usd      REAL NOT NULL DEFAULT 0.0,
    spent_turns    INTEGER NOT NULL DEFAULT 0,
    organization   TEXT,
    project        TEXT,
    is_blocked     INTEGER NOT NULL DEFAULT 0,
    block_reason   TEXT,
    parent_id      TEXT REFERENCES tasks(id),
    execution_stage TEXT NOT NULL DEFAULT 'todo',
    checkout_run_id TEXT,
    checkout_agent_id TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deleted_at     TEXT
);

CREATE TABLE IF NOT EXISTS task_comments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author      TEXT NOT NULL DEFAULT 'system',
    message     TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS task_relations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    blocks_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(task_id, blocks_id)
);

CREATE TABLE IF NOT EXISTS task_documents (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    doc_key       TEXT NOT NULL,
    version       INTEGER NOT NULL DEFAULT 1,
    content       TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(task_id, doc_key, version)
);

CREATE TABLE IF NOT EXISTS task_interactions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    interaction_kind TEXT NOT NULL,
    payload       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    resolved_at   TEXT
);

CREATE TABLE IF NOT EXISTS task_work_products (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    product_type  TEXT NOT NULL CHECK (product_type IN ('pull_request', 'commit', 'branch', 'workspace_file')),
    reference     TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS activity_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event_type    TEXT NOT NULL,
    details       TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS wire_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    channel     TEXT NOT NULL DEFAULT 'global',
    author      TEXT NOT NULL,
    repo_path   TEXT,
    content     TEXT NOT NULL,
    ttl_seconds INTEGER NOT NULL DEFAULT 86400,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_wire_messages_channel_expires ON wire_messages (channel, expires_at);
CREATE INDEX IF NOT EXISTS idx_wire_messages_repo_expires ON wire_messages (repo_path, expires_at);
CREATE INDEX IF NOT EXISTS idx_wire_messages_id_expires ON wire_messages (id, expires_at);

CREATE TABLE IF NOT EXISTS wire_cursors (
    consumer_key TEXT PRIMARY KEY,
    last_read_id INTEGER NOT NULL DEFAULT 0,
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS agent_sessions (
    id                TEXT PRIMARY KEY,
    agent_type        TEXT NOT NULL CHECK (agent_type IN ('claude', 'gemini', 'codex', 'other')),
    repo_path         TEXT NOT NULL,
    git_branch        TEXT NOT NULL DEFAULT 'main',
    pid               INTEGER,
    hostname          TEXT NOT NULL DEFAULT 'local',
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'idle', 'closed')),
    started_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_heartbeat_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    metadata_json     TEXT
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_repo ON agent_sessions (repo_path, status, last_heartbeat_at);

CREATE TABLE IF NOT EXISTS agent_working_files (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    repo_path         TEXT NOT NULL,
    file_path         TEXT NOT NULL,
    access_type       TEXT NOT NULL CHECK (access_type IN ('read', 'write', 'lock')),
    first_touched_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_touched_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at        TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_working_files_sess_path ON agent_working_files (session_id, file_path);
CREATE INDEX IF NOT EXISTS idx_agent_working_files_repo_path ON agent_working_files (repo_path, expires_at);

CREATE TABLE IF NOT EXISTS agent_circuit_breakers (
    session_id        TEXT PRIMARY KEY,
    repo_path         TEXT NOT NULL,
    agent_type        TEXT NOT NULL,
    is_tripped        INTEGER NOT NULL DEFAULT 0,
    trip_count        INTEGER NOT NULL DEFAULT 0,
    failure_signature TEXT,
    failing_tool      TEXT,
    failing_command   TEXT,
    last_error        TEXT,
    tripped_at        TEXT,
    cleared_at        TEXT
);

CREATE INDEX IF NOT EXISTS idx_circuit_breakers_active ON agent_circuit_breakers (repo_path, is_tripped);
`

type Store struct {
	db *sql.DB
}

// migrateLegacyQuotaWindows drops the pre-T3 quota_windows table, which held
// one row per pool (pool_key UNIQUE) and could not store both windows. Nothing
// wrote to it before T3, so there is no data to carry over.
func migrateLegacyQuotaWindows(conn *sql.DB) error {
	var ddl string
	if err := conn.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='quota_windows'").Scan(&ddl); err != nil {
		return nil
	}
	if strings.Contains(ddl, "UNIQUE (pool_key, window_type)") {
		return nil
	}
	if _, err := conn.Exec("DROP TABLE quota_windows"); err != nil {
		return err
	}
	_, err := conn.Exec(Schema)
	return err
}

func migrateSchemaTasksCols(conn *sql.DB) error {
	if err := migrateLegacyQuotaWindows(conn); err != nil {
		return err
	}
	rows, err := conn.Query("PRAGMA table_info(tasks);")
	if err != nil {
		return err
	}
	defer rows.Close()

	existingCols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err == nil {
			existingCols[name] = true
		} else {
			return fmt.Errorf("failed to scan table_info for column: %v", err)
		}
	}

	cols := []struct {
		name string
		def  string
	}{
		{"max_budget_usd", "REAL NOT NULL DEFAULT 0.0"},
		{"max_turns", "INTEGER NOT NULL DEFAULT 0"},
		{"spent_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"spent_usd", "REAL NOT NULL DEFAULT 0.0"},
		{"spent_turns", "INTEGER NOT NULL DEFAULT 0"},
		{"organization", "TEXT"},
		{"project", "TEXT"},
		{"is_blocked", "INTEGER NOT NULL DEFAULT 0"},
		{"block_reason", "TEXT"},
	}

	for _, c := range cols {
		if !existingCols[c.name] {
			if _, err := conn.Exec(fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s;", c.name, c.def)); err != nil {
				return err
			}
		}
	}
	return nil
}

type Migration struct {
	Version int
	Name    string
	Up      func(*sql.DB) error
}

var Migrations = []Migration{
	{
		Version: 1,
		Name:    "baseline",
		Up: func(conn *sql.DB) error {
			if err := migrateLegacyQuotaWindows(conn); err != nil {
				return err
			}
			if _, err := conn.Exec(Schema); err != nil {
				return err
			}
			return migrateSchemaTasksCols(conn)
		},
	},
	{
		Version: 2,
		Name:    "task_graph_and_activity",
		Up: func(conn *sql.DB) error {
			cols := []struct {
				name string
				def  string
			}{
				{"parent_id", "TEXT REFERENCES tasks(id)"},
				{"execution_stage", "TEXT NOT NULL DEFAULT 'todo'"},
				{"checkout_run_id", "TEXT"},
				{"checkout_agent_id", "TEXT"},
			}
			for _, c := range cols {
				if _, err := conn.Exec(fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s;", c.name, c.def)); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
					return err
				}
			}
			queries := []string{
				"CREATE TABLE IF NOT EXISTS task_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, blocks_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), UNIQUE(task_id, blocks_id));",
				"CREATE TABLE IF NOT EXISTS task_documents (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, doc_key TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1, content TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), UNIQUE(task_id, doc_key, version));",
				"CREATE TABLE IF NOT EXISTS task_interactions (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, interaction_kind TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), resolved_at TEXT);",
				"CREATE TABLE IF NOT EXISTS task_work_products (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, product_type TEXT NOT NULL CHECK (product_type IN ('pull_request', 'commit', 'branch', 'workspace_file')), reference TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));",
				"CREATE TABLE IF NOT EXISTS activity_log (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, event_type TEXT NOT NULL, details TEXT, created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));",
			}
			for _, q := range queries {
				if _, err := conn.Exec(q); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

func applyMigrations(dbPath string, conn *sql.DB) error {
	if _, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_versions (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);
	`); err != nil {
		return fmt.Errorf("failed to create schema_versions: %w", err)
	}

	var currentVersion int
	err := conn.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_versions;`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	var pending []Migration
	for _, m := range Migrations {
		if m.Version > currentVersion {
			pending = append(pending, m)
		}
	}

	if len(pending) == 0 {
		return nil
	}

	// Backup before migrating
	if _, err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		return fmt.Errorf("failed to checkpoint wal before backup: %w", err)
	}

	backupPath := dbPath + ".bak"
	if err := copyFile(dbPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup database: %w", err)
	}

	for _, m := range pending {
		if err := m.Up(conn); err != nil {
			// Restore backup
			conn.Close()
			_ = copyFile(backupPath, dbPath)
			_ = os.Remove(dbPath + "-wal")
			_ = os.Remove(dbPath + "-shm")
			return fmt.Errorf("migration %d (%s) failed, database restored from backup: %w", m.Version, m.Name, err)
		}
		if _, err := conn.Exec(`INSERT INTO schema_versions (version) VALUES (?)`, m.Version); err != nil {
			conn.Close()
			_ = copyFile(backupPath, dbPath)
			_ = os.Remove(dbPath + "-wal")
			_ = os.Remove(dbPath + "-shm")
			return fmt.Errorf("failed to record migration %d: %w", m.Version, err)
		}
	}

	_ = os.Remove(backupPath)
	return nil
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

	if err := applyMigrations(dbPath, conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	// Always execute pragmas on connect just in case
	if _, err := conn.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA busy_timeout = 5000;
		PRAGMA foreign_keys = ON;
	`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to apply pragmas: %w", err)
	}

	return &Store{db: conn}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}
