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
