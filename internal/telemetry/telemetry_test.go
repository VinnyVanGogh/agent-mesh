package telemetry

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/agent-mesh/internal/config"
	_ "modernc.org/sqlite"
)

func TestIngestLineDynamicModelFamily(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-watcher-test-*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "telemetry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("db open error: %v", err)
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
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema error: %v", err)
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

	// 1. Ingest Claude entry
	claudeJSON := `{"sessionId":"sess-claude","message":{"model":"claude-3-5-sonnet","usage":{"input_tokens":100,"output_tokens":200}}}`
	w.ingestLine([]byte(claudeJSON), "/path/test.jsonl")

	// 2. Ingest Gemini entry
	geminiJSON := `{"sessionId":"sess-gemini","message":{"model":"gemini-1.5-pro","usage":{"input_tokens":300,"output_tokens":400}}}`
	w.ingestLine([]byte(geminiJSON), "/path/test.jsonl")

	// 3. Ingest OpenAI entry
	gptJSON := `{"sessionId":"sess-gpt","message":{"model":"gpt-4o","usage":{"input_tokens":500,"output_tokens":600}}}`
	w.ingestLine([]byte(gptJSON), "/path/test.jsonl")

	rows, err := db.Query("SELECT model, model_family, total_tokens FROM requests ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	defer rows.Close()

	type result struct {
		model       string
		modelFamily string
		totalTokens int64
	}
	var results []result
	for rows.Next() {
		var r result
		if err := rows.Scan(&r.model, &r.modelFamily, &r.totalTokens); err != nil {
			t.Fatalf("scan error: %v", err)
		}
		results = append(results, r)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].modelFamily != "claude" || results[0].totalTokens != 300 {
		t.Errorf("expected claude/300, got %s/%d", results[0].modelFamily, results[0].totalTokens)
	}
	if results[1].modelFamily != "gemini" || results[1].totalTokens != 700 {
		t.Errorf("expected gemini/700, got %s/%d", results[1].modelFamily, results[1].totalTokens)
	}
	if results[2].modelFamily != "openai" || results[2].totalTokens != 1100 {
		t.Errorf("expected openai/1100, got %s/%d", results[2].modelFamily, results[2].totalTokens)
	}
}

func TestNotifierEscaping(t *testing.T) {
	title := `Notice with "quotes" and \backslashes\`
	msg := `Message with "quotes" and \backslashes\`

	safeTitle := strings.ReplaceAll(title, `\`, `\\`)
	safeTitle = strings.ReplaceAll(safeTitle, `"`, `\"`)
	safeMsg := strings.ReplaceAll(msg, `\`, `\\`)
	safeMsg = strings.ReplaceAll(safeMsg, `"`, `\"`)

	if strings.Contains(safeTitle, `"quotes"`) {
		t.Errorf("quotes were not properly escaped in title")
	}
	if strings.Contains(safeMsg, `"quotes"`) {
		t.Errorf("quotes were not properly escaped in msg")
	}
}
