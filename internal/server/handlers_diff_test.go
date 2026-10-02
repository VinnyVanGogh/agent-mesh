package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestGetTaskDiff_ReturnsFileStats(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "diff-stat-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/"+taskID+"/diff", nil)
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
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// file_stats key must be present (empty array when no git repo)
	if _, ok := body["file_stats"]; !ok {
		t.Errorf("response missing file_stats key; got %v", body)
	}
	if _, ok := body["files"]; !ok {
		t.Errorf("response missing files key; got %v", body)
	}
}

func TestRestoreFileHandler_MissingFilePath(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "restore-file-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{"checkpoint_id": "", "file_path": ""})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/tasks/"+taskID+"/checkpoint-restore-file", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for missing file_path, got %d", resp.StatusCode)
	}
}

func TestRestoreFileHandler_UnknownTask(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	payload, _ := json.Marshal(map[string]any{"checkpoint_id": "", "file_path": "foo.go"})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/tasks/unknown-task-id/checkpoint-restore-file", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 for unknown task, got %d", resp.StatusCode)
	}
}
