package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	_ "modernc.org/sqlite"
)

func setupTestWatcher(t *testing.T) (*Watcher, string, *sql.DB) {
	t.Helper()
	tempDir := t.TempDir()

	dbPath := filepath.Join(tempDir, "telemetry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	schema := `
	CREATE TABLE requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		idempotency_key TEXT UNIQUE,
		detected_via TEXT,
		ts TEXT,
		model TEXT,
		model_family TEXT,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cache_read_tokens INTEGER,
		cache_creation_tokens INTEGER,
		total_tokens INTEGER,
		session_id TEXT,
		account_email TEXT,
		raw_json TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create requests table: %v", err)
	}

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		WorkEmail:       "work@company.com",
		PersonalEmail:   "personal@gmail.com",
		MachineRole:     "personal",
		DataDir:         tempDir,
	}

	cursors := LoadCursors(filepath.Join(tempDir, "cursors.json"))
	w := &Watcher{
		cfg:     cfg,
		db:      db,
		cursors: cursors,
	}

	return w, tempDir, db
}

func TestWatcher_PartialLineHandling(t *testing.T) {
	w, tempDir, db := setupTestWatcher(t)
	defer db.Close()

	logFile := filepath.Join(tempDir, "session.jsonl")

	// Step 1: Slow writer writes an incomplete JSON line without trailing newline
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}

	partialChunk1 := `{"sessionId":"sess-slow","message":{"model":"claude-3-5-sonnet","usage":{"input_tokens":150`
	if _, err := f.WriteString(partialChunk1); err != nil {
		t.Fatalf("failed to write partial chunk 1: %v", err)
	}
	_ = f.Sync()

	// Process the file. Since no newline delimiter exists, cursor must remain 0 and no records ingested.
	w.processFile(logFile)

	if offset := w.cursors.Get(logFile); offset != 0 {
		t.Fatalf("expected cursor to remain at 0 on partial line, got %d", offset)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&count); err != nil {
		t.Fatalf("failed to count requests: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 requests ingested from partial line, got %d", count)
	}

	// Step 2: Slow writer completes the line with remainder and newline
	partialChunk2 := `,"output_tokens":250}}}` + "\n"
	if _, err := f.WriteString(partialChunk2); err != nil {
		t.Fatalf("failed to write partial chunk 2: %v", err)
	}
	_ = f.Close()

	// Process the completed file
	w.processFile(logFile)

	expectedOffset := int64(len(partialChunk1) + len(partialChunk2))
	if offset := w.cursors.Get(logFile); offset != expectedOffset {
		t.Fatalf("expected cursor to advance to %d, got %d", expectedOffset, offset)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&count); err != nil {
		t.Fatalf("failed to count requests after completion: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 request ingested after line completion, got %d", count)
	}
}

func TestWatcher_OversizedLineHandling(t *testing.T) {
	w, tempDir, db := setupTestWatcher(t)
	defer db.Close()

	logFile := filepath.Join(tempDir, "oversized.jsonl")

	// Generate an oversized JSON line (>128KB, larger than default 64KB reader buffer)
	largePadding := strings.Repeat("A", 140*1024)
	oversizedJSON := fmt.Sprintf(`{"sessionId":"sess-large","padding":"%s","message":{"model":"claude-3-5-sonnet","usage":{"input_tokens":1200,"output_tokens":3400}}}`+"\n", largePadding)

	if err := os.WriteFile(logFile, []byte(oversizedJSON), 0644); err != nil {
		t.Fatalf("failed to write oversized line: %v", err)
	}

	w.processFile(logFile)

	expectedOffset := int64(len(oversizedJSON))
	if offset := w.cursors.Get(logFile); offset != expectedOffset {
		t.Fatalf("expected cursor to advance to %d for oversized line, got %d", expectedOffset, offset)
	}

	var count int
	var totalTokens int
	if err := db.QueryRow("SELECT COUNT(*), total_tokens FROM requests GROUP BY id").Scan(&count, &totalTokens); err != nil {
		t.Fatalf("failed to query request from oversized line: %v", err)
	}
	if count != 1 || totalTokens != 4600 {
		t.Fatalf("expected 1 request with 4600 total tokens, got count=%d tokens=%d", count, totalTokens)
	}
}
