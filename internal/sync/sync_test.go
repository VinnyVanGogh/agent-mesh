package sync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	_ "modernc.org/sqlite"
)

func createTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
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
}

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

	createTestSchema(t, db)

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

	createTestSchema(t, importDB)

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

func TestExportBundle_EmptyDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "telemetry_empty.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	createTestSchema(t, db)

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		MachineRole:     "personal",
	}

	exportPath := filepath.Join(tempDir, "empty_bundle.tar.gz")
	outPath, count, err := ExportBundle(exportPath, cfg)
	if err != nil {
		t.Fatalf("ExportBundle failed on empty db: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 records exported, got %d", count)
	}
	if outPath != exportPath {
		t.Fatalf("expected outPath %s, got %s", exportPath, outPath)
	}

	// Verify tar.gz contents
	f, err := os.Open(exportPath)
	if err != nil {
		t.Fatalf("failed to open exported bundle: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("failed to read gzip: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var foundMeta, foundRequests bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar error: %v", err)
		}
		if hdr.Name == "metadata.json" {
			foundMeta = true
			var meta BundleMetadata
			if err := json.NewDecoder(tr).Decode(&meta); err != nil {
				t.Fatalf("failed to decode metadata: %v", err)
			}
			if meta.Version != "0.1.0" {
				t.Errorf("expected version 0.1.0, got %s", meta.Version)
			}
			if meta.RecordCount != 0 {
				t.Errorf("expected RecordCount 0, got %d", meta.RecordCount)
			}
			if meta.MachineRole != "personal" {
				t.Errorf("expected MachineRole personal, got %s", meta.MachineRole)
			}
		}
		if hdr.Name == "requests.jsonl" {
			foundRequests = true
			body, _ := io.ReadAll(tr)
			if len(strings.TrimSpace(string(body))) != 0 {
				t.Errorf("expected empty requests.jsonl, got %q", string(body))
			}
		}
	}
	if !foundMeta {
		t.Errorf("metadata.json not found in archive")
	}
	if !foundRequests {
		t.Errorf("requests.jsonl not found in archive")
	}
}

func TestExportBundle_MultipleRecordsAndMetadata(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "telemetry_multi.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	createTestSchema(t, db)

	records := []struct {
		idKey       string
		detectedVia string
		ts          string
		model       string
		modelFamily string
		inTok       int64
		outTok      int64
		cacheRead   int64
		cacheCreate int64
		totalTok    int64
		sessID      string
		email       string
		rawJSON     string
	}{
		{"k1", "transcript", "2026-10-01T10:00:00Z", "claude-3-5-sonnet", "claude", 100, 50, 20, 10, 180, "s1", "work@company.com", `{"line":1}`},
		{"k2", "transcript", "2026-10-01T10:01:00Z", "gemini-1.5-flash", "gemini", 200, 80, 0, 0, 280, "s2", "personal@home.com", `{"line":2}`},
		{"k3", "transcript", "2026-10-01T10:02:00Z", "gpt-4o", "openai", 300, 120, 50, 0, 470, "s3", "work@company.com", `{"line":3}`},
	}

	for _, r := range records {
		_, err := db.Exec(`
			INSERT INTO requests (idempotency_key, detected_via, ts, model, model_family,
				input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens,
				session_id, account_email, raw_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.idKey, r.detectedVia, r.ts, r.model, r.modelFamily,
			r.inTok, r.outTok, r.cacheRead, r.cacheCreate, r.totalTok,
			r.sessID, r.email, r.rawJSON,
		)
		if err != nil {
			t.Fatalf("failed to insert record %s: %v", r.idKey, err)
		}
	}

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		MachineRole:     "work",
	}

	exportPath := filepath.Join(tempDir, "multi_bundle.tar.gz")
	_, count, err := ExportBundle(exportPath, cfg)
	if err != nil {
		t.Fatalf("ExportBundle failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 records, got %d", count)
	}

	// Verify imported count into target DB
	targetDBPath := filepath.Join(tempDir, "target.db")
	targetDB, err := sql.Open("sqlite", targetDBPath)
	if err != nil {
		t.Fatalf("failed to open target db: %v", err)
	}
	defer targetDB.Close()
	createTestSchema(t, targetDB)

	targetCfg := &config.Config{
		TelemetryDBPath: targetDBPath,
		MachineRole:     "work",
	}

	imported, err := ImportBundle(exportPath, targetCfg)
	if err != nil {
		t.Fatalf("ImportBundle failed: %v", err)
	}
	if imported != 3 {
		t.Fatalf("expected 3 imported records, got %d", imported)
	}

	// Verify individual fields
	var rowCount int
	_ = targetDB.QueryRow("SELECT COUNT(*) FROM requests").Scan(&rowCount)
	if rowCount != 3 {
		t.Fatalf("expected 3 rows in target DB, got %d", rowCount)
	}

	var totalTokensSum int64
	_ = targetDB.QueryRow("SELECT SUM(total_tokens) FROM requests").Scan(&totalTokensSum)
	if totalTokensSum != (180 + 280 + 470) {
		t.Errorf("expected total tokens %d, got %d", 180+280+470, totalTokensSum)
	}
}

func TestExportBundle_InvalidDB(t *testing.T) {
	cfg := &config.Config{
		TelemetryDBPath: "/nonexistent/invalid/path/db.sqlite",
		MachineRole:     "personal",
	}
	_, _, err := ExportBundle(filepath.Join(t.TempDir(), "fail.tar.gz"), cfg)
	if err == nil {
		t.Fatal("expected error on invalid db path, got nil")
	}
}

func TestImportBundle_CorruptedArchive(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "target.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()
	createTestSchema(t, db)

	cfg := &config.Config{TelemetryDBPath: dbPath}

	// 1. Completely invalid file (not gzip)
	garbagePath := filepath.Join(tempDir, "garbage.tar.gz")
	if err := os.WriteFile(garbagePath, []byte("this is not a valid gzip file"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBundle(garbagePath, cfg); err == nil {
		t.Error("expected error importing non-gzip file")
	}

	// 2. Non-existent file
	if _, err := ImportBundle(filepath.Join(tempDir, "missing.tar.gz"), cfg); err == nil {
		t.Error("expected error importing nonexistent file")
	}

	// 3. Valid gzip but corrupt tar
	corruptTarPath := filepath.Join(tempDir, "corrupt_tar.tar.gz")
	cf, err := os.Create(corruptTarPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(cf)
	_, _ = gw.Write([]byte("not valid tar header content"))
	_ = gw.Close()
	_ = cf.Close()

	if _, err := ImportBundle(corruptTarPath, cfg); err == nil {
		t.Error("expected error importing corrupted tar")
	}
}

func TestWriteTarEntry(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	testData := []byte("hello staypoint sync test")
	if err := writeTarEntry(tw, "test.txt", testData); err != nil {
		t.Fatalf("writeTarEntry failed: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close failed: %v", err)
	}

	tr := tar.NewReader(&buf)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar read header failed: %v", err)
	}
	if hdr.Name != "test.txt" {
		t.Errorf("expected name test.txt, got %s", hdr.Name)
	}
	if hdr.Size != int64(len(testData)) {
		t.Errorf("expected size %d, got %d", len(testData), hdr.Size)
	}
	if hdr.Mode != 0644 {
		t.Errorf("expected mode 0644, got %o", hdr.Mode)
	}
	readBack, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("failed to read tar content: %v", err)
	}
	if string(readBack) != string(testData) {
		t.Errorf("content mismatch: got %q, want %q", string(readBack), string(testData))
	}
}

func TestSyncResult_Fields(t *testing.T) {
	sr := SyncResult{
		Host:              "node-1",
		Duration:          500 * time.Millisecond,
		ClaudeTranscripts: 3,
		BrainLogs:         2,
		RecordsIngested:   15,
	}

	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("failed to marshal SyncResult: %v", err)
	}

	var parsed SyncResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal SyncResult: %v", err)
	}
	if parsed.Host != "node-1" || parsed.RecordsIngested != 15 || parsed.ClaudeTranscripts != 3 || parsed.BrainLogs != 2 {
		t.Errorf("parsed SyncResult does not match: %+v", parsed)
	}
}

func TestIngestTranscripts_ClaudeProjects(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	projectsDir := filepath.Join(tempHome, ".claude", "projects", "sample-project")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		t.Fatalf("failed to create projects dir: %v", err)
	}

	// Prepare jsonl lines
	lines := []string{
		// Line 1: Claude model
		`{"sessionId":"sess-1","timestamp":"2026-10-01T12:00:00Z","message":{"model":"claude-3-7-sonnet","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}}`,
		// Line 2: Gemini model
		`{"session_id":"sess-2","timestamp":"2026-10-01T12:05:00Z","message":{"model":"gemini-1.5-pro","usage":{"input_tokens":200,"output_tokens":80}}}`,
		// Line 3: OpenAI model
		`{"sessionId":"sess-3","timestamp":"2026-10-01T12:10:00Z","message":{"model":"gpt-4o","usage":{"input_tokens":150,"output_tokens":60}}}`,
		// Line 4: Zero tokens (should be skipped)
		`{"sessionId":"sess-4","timestamp":"2026-10-01T12:15:00Z","message":{"model":"claude-3-haiku","usage":{"input_tokens":0,"output_tokens":0}}}`,
		// Line 5: Empty line (should be skipped)
		"",
		// Line 6: Non-message line (should be skipped)
		`{"type":"system","content":"startup"}`,
	}

	transcriptPath := filepath.Join(projectsDir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	dbPath := filepath.Join(tempHome, "telemetry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()
	createTestSchema(t, db)

	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		MachineRole:     "work",
		WorkEmail:       "engineer@work.com",
		PersonalEmail:   "dev@personal.com",
	}

	inserted, err := IngestTranscripts(cfg)
	if err != nil {
		t.Fatalf("IngestTranscripts failed: %v", err)
	}
	if inserted != 3 {
		t.Fatalf("expected 3 inserted rows, got %d", inserted)
	}

	// Verify model families and account email
	rows, err := db.Query("SELECT model_family, total_tokens, account_email FROM requests ORDER BY ts ASC")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	var families []string
	var tokenSums []int64
	for rows.Next() {
		var family, email string
		var total int64
		if err := rows.Scan(&family, &total, &email); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		families = append(families, family)
		tokenSums = append(tokenSums, total)
		if email != "engineer@work.com" {
			t.Errorf("expected email engineer@work.com, got %s", email)
		}
	}

	if len(families) != 3 || families[0] != "claude" || families[1] != "gemini" || families[2] != "openai" {
		t.Errorf("unexpected model families: %v", families)
	}
	// Line 1: 100 + 50 + 10 + 5 = 165
	// Line 2: 200 + 80 = 280
	// Line 3: 150 + 60 = 210
	if tokenSums[0] != 165 || tokenSums[1] != 280 || tokenSums[2] != 210 {
		t.Errorf("unexpected token sums: %v", tokenSums)
	}

	// Verify idempotency on second run
	secondInserted, err := IngestTranscripts(cfg)
	if err != nil {
		t.Fatalf("second IngestTranscripts failed: %v", err)
	}
	if secondInserted != 0 {
		t.Errorf("expected 0 new records on re-ingest, got %d", secondInserted)
	}
}

func TestPullAndPushHandoff_UnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	unreachableHost := "nonexistent-sync-host-test-12345.local"

	err := PushHandoff(ctx, unreachableHost, &meshContext.HandoffRecord{
		HandoffPrompt: "test prompt",
	})
	if err == nil {
		t.Error("expected error for PushHandoff to unreachable host, got nil")
	}

	_, err = PullHandoff(ctx, unreachableHost)
	if err == nil {
		t.Error("expected error for PullHandoff from unreachable host, got nil")
	}
}
