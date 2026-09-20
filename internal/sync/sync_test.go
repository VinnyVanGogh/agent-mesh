package sync

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
	_ "modernc.org/sqlite"
)

func TestExportAndImportBundle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "telemetry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

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
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	insertSQL := `
	INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family, input_tokens, output_tokens, total_tokens, session_id, account_email)
	VALUES ('test:1', 'transcript', '2026-09-19T12:00:00Z', 'claude-3-7-sonnet', 'claude', 100, 50, 150, 'sess-1', 'work@company.com');
	`
	if _, err := db.Exec(insertSQL); err != nil {
		t.Fatalf("failed to insert test record: %v", err)
	}

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		MachineRole:     "work",
	}

	exportPath := filepath.Join(tempDir, "bundle.tar.gz")
	outPath, count, err := ExportBundle(exportPath, cfg)
	if err != nil {
		t.Fatalf("ExportBundle failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 record exported, got %d", count)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("export file not created: %v", err)
	}

	// Create a new target DB for import
	importDBPath := filepath.Join(tempDir, "import.db")
	importDB, err := sql.Open("sqlite", importDBPath)
	if err != nil {
		t.Fatalf("failed to open import db: %v", err)
	}
	defer importDB.Close()

	if _, err := importDB.Exec(schema); err != nil {
		t.Fatalf("failed to create schema on import db: %v", err)
	}

	importCfg := &config.Config{
		TelemetryDBPath: importDBPath,
		MachineRole:     "personal",
	}

	importedCount, err := ImportBundle(exportPath, importCfg)
	if err != nil {
		t.Fatalf("ImportBundle failed: %v", err)
	}
	if importedCount != 1 {
		t.Fatalf("expected 1 record imported, got %d", importedCount)
	}

	// Verify idempotency (importing twice should insert 0 rows)
	secondImport, err := ImportBundle(exportPath, importCfg)
	if err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if secondImport != 0 {
		t.Fatalf("expected 0 rows on duplicate import, got %d", secondImport)
	}
}
