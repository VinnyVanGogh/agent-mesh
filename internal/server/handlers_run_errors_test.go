package server_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/context"
)

func insertRunErrorRow(t *testing.T, database *sql.DB, id, runID, taskID, adapter, stderr string, exitCode int) {
	t.Helper()
	_, err := database.Exec(
		`INSERT INTO run_errors (id, run_id, task_id, turn, exit_code, stderr_tail, duration_ms, model, adapter)
		 VALUES (?, ?, ?, 1, ?, ?, 1000, ?, ?)`,
		id, runID, taskID, exitCode, stderr, adapter, adapter,
	)
	if err != nil {
		t.Fatalf("insertRunErrorRow: %v", err)
	}
}

func TestGetRunErrors_EmptyForNewTask(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "run-err-empty"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/"+taskID+"/run-errors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].([]any)
	if errs == nil {
		errs = []any{}
	}
	if len(errs) != 0 {
		t.Errorf("want 0 run errors, got %d", len(errs))
	}
}

func TestGetRunErrors_ReturnsErrors(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "run-err-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}
	insertRunErrorRow(t, database, "err-test-1", "run-test-1", taskID, "claude", "fatal: bad model name", 1)

	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/"+taskID+"/run-errors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("want 1 run error, got %d", len(errs))
	}
	row, _ := errs[0].(map[string]any)
	if row["stderr_tail"] != "fatal: bad model name" {
		t.Errorf("want stderr_tail 'fatal: bad model name', got %v", row["stderr_tail"])
	}
}

func TestGetRunErrors_404ForUnknownTask(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/nonexistent-task-id/run-errors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 for unknown task, got %d", resp.StatusCode)
	}
}

func TestGetAllRunErrors_Empty(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	req, _ := http.NewRequest(http.MethodGet, base+"/api/run-errors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].([]any)
	if errs == nil {
		errs = []any{}
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestGetAllRunErrors_CrossTask(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, b1 := postTask(t, base, token, map[string]any{"name": "task-a"})
	_, b2 := postTask(t, base, token, map[string]any{"name": "task-b"})
	id1, _ := b1["id"].(string)
	id2, _ := b2["id"].(string)
	if id1 == "" || id2 == "" {
		t.Skip("task creation failed")
	}

	insertRunErrorRow(t, database, "err-a1", "run-a1", id1, "claude", "error in task a", 1)
	insertRunErrorRow(t, database, "err-b1", "run-b1", id2, "gemini", "error in task b", 2)

	req, _ := http.NewRequest(http.MethodGet, base+"/api/run-errors", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	errs, _ := body["errors"].([]any)
	if len(errs) != 2 {
		t.Errorf("want 2 errors, got %d", len(errs))
	}
}

// compile-time check: context.RunError fields are accessible.
var _ = context.RunError{}
