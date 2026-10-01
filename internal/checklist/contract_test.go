package checklist_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
	_ "modernc.org/sqlite"
)

func TestEvaluateContract_FilePattern(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("hello staypoint world claude_work"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Success case
	c1 := checklist.Contract{
		Type:        checklist.ContractTypeFilePattern,
		FilePath:    "sample.txt",
		MustContain: []string{"staypoint", "claude_work"},
	}
	res1 := checklist.EvaluateContract(ctx, c1, tempDir, nil, "")
	if !res1.Passed {
		t.Fatalf("expected c1 to pass, got: %v", res1.Reason)
	}

	// 2. Failure: missing pattern
	c2 := checklist.Contract{
		Type:        checklist.ContractTypeFilePattern,
		FilePath:    "sample.txt",
		MustContain: []string{"gemini_ultra"},
	}
	res2 := checklist.EvaluateContract(ctx, c2, tempDir, nil, "")
	if res2.Passed {
		t.Fatal("expected c2 to fail for missing pattern")
	}

	// 3. Failure: contains forbidden pattern
	c3 := checklist.Contract{
		Type:           checklist.ContractTypeFilePattern,
		FilePath:       "sample.txt",
		MustNotContain: []string{"claude_work"},
	}
	res3 := checklist.EvaluateContract(ctx, c3, tempDir, nil, "")
	if res3.Passed {
		t.Fatal("expected c3 to fail for forbidden pattern")
	}

	// 4. Failure: missing file
	c4 := checklist.Contract{
		Type:     checklist.ContractTypeFilePattern,
		FilePath: "nonexistent.txt",
	}
	res4 := checklist.EvaluateContract(ctx, c4, tempDir, nil, "")
	if res4.Passed {
		t.Fatal("expected c4 to fail for missing file")
	}
}

func TestEvaluateContract_Command(t *testing.T) {
	ctx := context.Background()

	// Success command
	c1 := checklist.Contract{
		Type:    checklist.ContractTypeCommand,
		Command: "echo 'hello staypoint'",
	}
	res1 := checklist.EvaluateContract(ctx, c1, ".", nil, "")
	if !res1.Passed {
		t.Fatalf("expected command to pass, got: %v", res1.Reason)
	}

	// Failing command
	c2 := checklist.Contract{
		Type:    checklist.ContractTypeCommand,
		Command: "exit 1",
	}
	res2 := checklist.EvaluateContract(ctx, c2, ".", nil, "")
	if res2.Passed {
		t.Fatal("expected exit 1 to fail")
	}
}

func TestEvaluateContract_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","pools":["work","personal"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()

	// 1. Success HTTP
	c1 := checklist.Contract{
		Type:           checklist.ContractTypeHTTP,
		Path:           "/api/status",
		ExpectedStatus: 200,
		BodyContains:   []string{"status", "pools"},
	}
	res1 := checklist.EvaluateContract(ctx, c1, ".", srv.Client(), srv.URL)
	if !res1.Passed {
		t.Fatalf("expected HTTP contract to pass, got: %v", res1.Reason)
	}

	// 2. Status code mismatch
	c2 := checklist.Contract{
		Type:           checklist.ContractTypeHTTP,
		Path:           "/nonexistent",
		ExpectedStatus: 200,
	}
	res2 := checklist.EvaluateContract(ctx, c2, ".", srv.Client(), srv.URL)
	if res2.Passed {
		t.Fatal("expected 404 to fail expected 200")
	}

	// 3. Body pattern missing
	c3 := checklist.Contract{
		Type:           checklist.ContractTypeHTTP,
		Path:           "/api/status",
		ExpectedStatus: 200,
		BodyContains:   []string{"missing_key_xyz"},
	}
	res3 := checklist.EvaluateContract(ctx, c3, ".", srv.Client(), srv.URL)
	if res3.Passed {
		t.Fatal("expected body missing pattern to fail")
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
	CREATE TABLE checklist_items (
		id          TEXT PRIMARY KEY,
		sprint      TEXT NOT NULL DEFAULT 'STA-168',
		section     TEXT NOT NULL,
		title       TEXT NOT NULL,
		description TEXT,
		how_to_test TEXT,
		contract    TEXT,
		status      TEXT NOT NULL DEFAULT 'pending',
		notes       TEXT,
		version     INTEGER NOT NULL DEFAULT 1,
		created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	CREATE TABLE checklist_history (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id     TEXT NOT NULL REFERENCES checklist_items(id) ON DELETE CASCADE,
		status      TEXT NOT NULL,
		notes       TEXT,
		changed_by  TEXT NOT NULL DEFAULT 'user',
		changed_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestEvaluateSprint_DivergenceDowngradeAndAudit(t *testing.T) {
	tempDir := t.TempDir()
	sourceFile := filepath.Join(tempDir, "feature.go")
	if err := os.WriteFile(sourceFile, []byte("package main\n\n// old implementation"), 0644); err != nil {
		t.Fatal(err)
	}

	db := setupTestDB(t)
	defer db.Close()

	// Insert item 1: Previously passed, but contract requires "required_token" which is missing from feature.go!
	contract1 := `{"type":"file_pattern","file_path":"feature.go","must_contain":["required_token"]}`
	_, err := db.Exec(`INSERT INTO checklist_items (id, sprint, section, title, contract, status, notes, version)
		VALUES ('item-1', 'STA-168', 'Core', 'Feature Item 1', ?, 'pass', 'Verified previously', 1)`, contract1)
	if err != nil {
		t.Fatal(err)
	}

	// Insert item 2: Previously passed, contract passes (requires "package main")
	contract2 := `{"type":"file_pattern","file_path":"feature.go","must_contain":["package main"]}`
	_, err = db.Exec(`INSERT INTO checklist_items (id, sprint, section, title, contract, status, notes, version)
		VALUES ('item-2', 'STA-168', 'Core', 'Feature Item 2', ?, 'pass', 'Verified previously', 1)`, contract2)
	if err != nil {
		t.Fatal(err)
	}

	// Insert item 3: Pending item, contract fails
	_, err = db.Exec(`INSERT INTO checklist_items (id, sprint, section, title, contract, status, notes, version)
		VALUES ('item-3', 'STA-168', 'Core', 'Feature Item 3', ?, 'pending', '', 1)`, contract1)
	if err != nil {
		t.Fatal(err)
	}

	broadcastEvents := []string{}
	opts := checklist.EvaluateOptions{
		RepoRoot:  tempDir,
		Downgrade: true,
		BroadcastFn: func(event string, data any) {
			broadcastEvents = append(broadcastEvents, event)
		},
	}

	ctx := context.Background()
	summary, err := checklist.EvaluateSprint(ctx, db, "STA-168", opts)
	if err != nil {
		t.Fatalf("EvaluateSprint failed: %v", err)
	}

	if summary.Total != 3 {
		t.Fatalf("expected 3 total evaluated items, got %d", summary.Total)
	}
	if summary.Passed != 1 {
		t.Fatalf("expected 1 passed item, got %d", summary.Passed)
	}
	if summary.Failed != 2 {
		t.Fatalf("expected 2 failed items, got %d", summary.Failed)
	}
	if summary.Divergences != 1 {
		t.Fatalf("expected exactly 1 divergence, got %d", summary.Divergences)
	}

	// Check item 1 in database: should be downgraded to fail, notes updated, version incremented
	var status1, notes1 string
	var version1 int
	err = db.QueryRow(`SELECT status, notes, version FROM checklist_items WHERE id = 'item-1'`).Scan(&status1, &notes1, &version1)
	if err != nil {
		t.Fatal(err)
	}
	if status1 != "fail" {
		t.Fatalf("expected item-1 status to be 'fail', got %s", status1)
	}
	if version1 != 2 {
		t.Fatalf("expected item-1 version to be 2, got %d", version1)
	}
	if !strings.Contains(notes1, "[REGRESSION DIVERGENCE") {
		t.Fatalf("expected notes to contain REGRESSION DIVERGENCE, got: %s", notes1)
	}

	// Check history table for item 1
	var histStatus, histNotes, histChangedBy string
	err = db.QueryRow(`SELECT status, notes, changed_by FROM checklist_history WHERE item_id = 'item-1' ORDER BY id DESC LIMIT 1`).
		Scan(&histStatus, &histNotes, &histChangedBy)
	if err != nil {
		t.Fatal(err)
	}
	if histStatus != "fail" {
		t.Fatalf("expected history status 'fail', got %s", histStatus)
	}
	if histChangedBy != "divergence-detector" {
		t.Fatalf("expected changed_by 'divergence-detector', got %s", histChangedBy)
	}

	// Check item 2: should remain pass
	var status2 string
	_ = db.QueryRow(`SELECT status FROM checklist_items WHERE id = 'item-2'`).Scan(&status2)
	if status2 != "pass" {
		t.Fatalf("expected item-2 status 'pass', got %s", status2)
	}

	// Check item 3: was pending, remains pending (not diverged)
	var status3 string
	_ = db.QueryRow(`SELECT status FROM checklist_items WHERE id = 'item-3'`).Scan(&status3)
	if status3 != "pending" {
		t.Fatalf("expected item-3 status 'pending', got %s", status3)
	}

	// Check broadcast event
	if len(broadcastEvents) != 1 || broadcastEvents[0] != "checklist:divergence" {
		t.Fatalf("expected checklist:divergence broadcast, got %v", broadcastEvents)
	}
}

func TestRecentTasksContracts(t *testing.T) {
	ctx := context.Background()
	items := checklist.DefaultChecklist("STA-168")
	var testedCount int
	for _, it := range items {
		if it.Section == "Recent Tasks Page" && it.Contract != "" {
			var c checklist.Contract
			if err := json.Unmarshal([]byte(it.Contract), &c); err != nil {
				t.Fatalf("failed to unmarshal contract for %s: %v", it.Title, err)
			}
			res := checklist.EvaluateContract(ctx, c, "../..", nil, "")
			if !res.Passed {
				t.Fatalf("contract for %q failed: %s", it.Title, res.Reason)
			}
			testedCount++
		}
	}
	if testedCount < 2 {
		t.Fatalf("expected at least 2 Recent Tasks contracts, tested %d", testedCount)
	}
}
