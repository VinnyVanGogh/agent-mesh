package db

import (
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

	// Verify tables exist
	tables := []string{"accounts", "quota_windows", "tasks", "wire_messages", "wire_cursors", "agent_sessions", "agent_working_files", "agent_circuit_breakers"}
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
	if err := migrateSchema(store.DB()); err != nil {
		t.Fatalf("idempotent migrateSchema failed: %v", err)
	}
	store.Close()
}
