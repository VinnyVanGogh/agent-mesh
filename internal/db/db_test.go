package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestDB_OpenAndSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	if store.DB() == nil {
		t.Fatalf("expected non-nil sql.DB")
	}

	// Verify WAL mode pragma
	var journalMode string
	if err := store.DB().QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode = wal, got %s", journalMode)
	}

	tables := []string{
		"schema_versions", "accounts", "quota_windows", "tasks", "wire_messages",
		"wire_cursors", "agent_sessions", "agent_working_files", "agent_circuit_breakers",
		"chat_sessions", "chat_messages", "chat_tool_calls", "session_provider_handles",
		"run_steps", "run_errors",
	}
	for _, tbl := range tables {
		var count int
		err := store.DB().QueryRow(fmt.Sprintf("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='%s';", tbl)).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("expected table %q to exist, count=%d err=%v", tbl, count, err)
		}
	}
}

func TestDB_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	const workers = 10
	const iterations = 20

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, err := store.DB().Exec(
					"INSERT INTO wire_messages (channel, author, repo_path, content, ttl_seconds, expires_at) VALUES (?, ?, ?, ?, ?, datetime('now', '+1 hour'));",
					"test-channel",
					fmt.Sprintf("worker-%d", workerID),
					"/test/repo",
					fmt.Sprintf("msg-%d-%d", workerID, j),
					3600,
				)
				if err != nil {
					t.Errorf("worker %d insert failed at %d: %v", workerID, j, err)
					return
				}

				// Concurrent read
				var cnt int
				_ = store.DB().QueryRow("SELECT count(*) FROM wire_messages WHERE channel='test-channel';").Scan(&cnt)
			}
		}(i)
	}

	wg.Wait()

	var total int
	if err := store.DB().QueryRow("SELECT count(*) FROM wire_messages WHERE channel='test-channel';").Scan(&total); err != nil {
		t.Fatalf("failed to query total wire_messages: %v", err)
	}
	if total != workers*iterations {
		t.Errorf("expected %d total messages, got %d", workers*iterations, total)
	}
}

func TestDB_SchemaMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Re-run migrateSchema on existing database to verify idempotency
	if err := applyMigrations(dbPath, store.DB()); err != nil {
		t.Fatalf("idempotent applyMigrations failed: %v", err)
	}
	store.Close()
}

func TestDB_SchemaMigration_FailureRestore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate_fail.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	// populate db
	_, err = store.DB().Exec("INSERT INTO accounts (account_key, label, plan_tier, role) VALUES ('acc1', 'lbl1', 'free', 'work')")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	store.Close()

	origMigrations := Migrations
	defer func() { Migrations = origMigrations }()

	Migrations = append(Migrations, Migration{
		Version: 99,
		Name:    "bad_migration",
		Up: func(conn *sql.DB) error {
			_, _ = conn.Exec("CREATE TABLE bad_table (id INTEGER);")
			// Insert some data into an existing table to see if it rolls back
			_, _ = conn.Exec("INSERT INTO accounts (account_key, label, plan_tier, role) VALUES ('acc2', 'lbl2', 'free', 'work')")
			return fmt.Errorf("forced failure")
		},
	})

	// Open should fail and restore
	store2, err := Open(dbPath)
	if err == nil {
		store2.Close()
		t.Fatalf("expected error from Open due to failed migration")
	}

	// Verify backup was restored
	Migrations = origMigrations
	store, err = Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed after restore: %v", err)
	}
	defer store.Close()

	var count int
	_ = store.DB().QueryRow("SELECT count(*) FROM accounts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 account, got %d", count)
	}

	_ = store.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='bad_table';").Scan(&count)
	if count != 0 {
		t.Errorf("expected bad_table to be reverted")
	}
}

func TestDB_RunErrors(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := Open(filepath.Join(tmpDir, "run_errors.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Insert a task so we can reference it via FK.
	_, err = store.DB().Exec(
		`INSERT INTO tasks (id, name, repo_path) VALUES (?, ?, ?)`,
		"task-001", "test task", "/repo",
	)
	if err != nil {
		t.Fatalf("insert task failed: %v", err)
	}

	_, err = store.DB().Exec(
		`INSERT INTO run_errors (id, run_id, task_id, turn, exit_code, stderr_tail, duration_ms, model, adapter)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"err-001", "run-abc", "task-001", 2, 1, "fatal: out of memory", 3500, "claude-opus-4-5", "claude_local",
	)
	if err != nil {
		t.Fatalf("insert run_errors failed: %v", err)
	}

	var id, runID, taskID, stderrTail, model, adapter, createdAt string
	var turn, exitCode, durationMs int
	err = store.DB().QueryRow(
		`SELECT id, run_id, task_id, turn, exit_code, stderr_tail, duration_ms, model, adapter, created_at
		 FROM run_errors WHERE run_id = ?`, "run-abc",
	).Scan(&id, &runID, &taskID, &turn, &exitCode, &stderrTail, &durationMs, &model, &adapter, &createdAt)
	if err != nil {
		t.Fatalf("SELECT run_errors failed: %v", err)
	}

	if id != "err-001" {
		t.Errorf("id: want err-001, got %s", id)
	}
	if runID != "run-abc" {
		t.Errorf("run_id: want run-abc, got %s", runID)
	}
	if taskID != "task-001" {
		t.Errorf("task_id: want task-001, got %s", taskID)
	}
	if turn != 2 {
		t.Errorf("turn: want 2, got %d", turn)
	}
	if exitCode != 1 {
		t.Errorf("exit_code: want 1, got %d", exitCode)
	}
	if stderrTail != "fatal: out of memory" {
		t.Errorf("stderr_tail: want 'fatal: out of memory', got %s", stderrTail)
	}
	if durationMs != 3500 {
		t.Errorf("duration_ms: want 3500, got %d", durationMs)
	}
	if model != "claude-opus-4-5" {
		t.Errorf("model: want claude-opus-4-5, got %s", model)
	}
	if adapter != "claude_local" {
		t.Errorf("adapter: want claude_local, got %s", adapter)
	}
	if createdAt == "" {
		t.Error("created_at should be auto-populated")
	}
}

func TestDB_RunErrors_NullTaskID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := Open(filepath.Join(tmpDir, "run_errors_null.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// task_id is nullable — orphaned run errors are allowed.
	_, err = store.DB().Exec(
		`INSERT INTO run_errors (id, run_id, task_id, turn, exit_code, stderr_tail, duration_ms, model, adapter)
		 VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?)`,
		"err-002", "run-xyz", 0, 2, "adapter crashed", 1000, "gemini-flash", "gemini_local",
	)
	if err != nil {
		t.Fatalf("insert with NULL task_id failed: %v", err)
	}

	var taskID sql.NullString
	err = store.DB().QueryRow(`SELECT task_id FROM run_errors WHERE id = ?`, "err-002").Scan(&taskID)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if taskID.Valid {
		t.Errorf("expected NULL task_id, got %s", taskID.String)
	}
}

func TestDB_RunErrors_CascadeDelete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := Open(filepath.Join(tmpDir, "run_errors_cascade.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Enable FK enforcement (SQLite requires explicit PRAGMA).
	if _, err := store.DB().Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("enable FK pragma failed: %v", err)
	}

	_, err = store.DB().Exec(
		`INSERT INTO tasks (id, name, repo_path) VALUES (?, ?, ?)`,
		"task-cascade", "cascade task", "/repo",
	)
	if err != nil {
		t.Fatalf("insert task failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, err = store.DB().Exec(
			`INSERT INTO run_errors (id, run_id, task_id) VALUES (?, ?, ?)`,
			fmt.Sprintf("err-cascade-%d", i), fmt.Sprintf("run-%d", i), "task-cascade",
		)
		if err != nil {
			t.Fatalf("insert run_error %d failed: %v", i, err)
		}
	}

	var count int
	_ = store.DB().QueryRow(`SELECT count(*) FROM run_errors WHERE task_id = ?`, "task-cascade").Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 run_errors before delete, got %d", count)
	}

	if _, err := store.DB().Exec(`DELETE FROM tasks WHERE id = ?`, "task-cascade"); err != nil {
		t.Fatalf("delete task failed: %v", err)
	}

	_ = store.DB().QueryRow(`SELECT count(*) FROM run_errors WHERE task_id = ?`, "task-cascade").Scan(&count)
	if count != 0 {
		t.Errorf("expected run_errors to be cascade-deleted, got %d rows", count)
	}
}

func TestDB_RunErrors_Indexes(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := Open(filepath.Join(tmpDir, "run_errors_idx.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	indexes := []string{"idx_run_errors_task", "idx_run_errors_run"}
	for _, idx := range indexes {
		var count int
		err := store.DB().QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&count)
		if err != nil || count != 1 {
			t.Errorf("expected index %q to exist, count=%d err=%v", idx, count, err)
		}
	}
}

func TestDB_RunSteps(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := Open(filepath.Join(tmpDir, "run_steps.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	_, err = store.DB().Exec(
		`INSERT INTO run_steps (id, run_id, seq, kind, title, body, started_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"step-1", "run-abc", 1, "tool_call", "Read file", "reading main.go",
		"2026-10-01T00:00:00.000Z", "2026-10-01T00:00:01.000Z",
	)
	if err != nil {
		t.Fatalf("insert run_steps failed: %v", err)
	}

	var id, runID, kind, title string
	var seq int
	err = store.DB().QueryRow(`SELECT id, run_id, seq, kind, title FROM run_steps WHERE run_id = ?`, "run-abc").
		Scan(&id, &runID, &seq, &kind, &title)
	if err != nil {
		t.Fatalf("SELECT run_steps failed: %v", err)
	}
	if id != "step-1" || runID != "run-abc" || seq != 1 || kind != "tool_call" || title != "Read file" {
		t.Errorf("unexpected row: id=%s run_id=%s seq=%d kind=%s title=%s", id, runID, seq, kind, title)
	}
}
