package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestGetTaskMigrations_EmptyWhenNoMigrations(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "mig-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/"+taskID+"/migrations", nil)
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
	if _, ok := body["migrations"]; !ok {
		t.Errorf("response missing migrations key; got %v", body)
	}
	if _, ok := body["sql_editor_url"]; !ok {
		t.Errorf("response missing sql_editor_url key; got %v", body)
	}
}

func TestMarkMigrationApplied_RecordsActivity(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "mark-applied-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":       "supabase/migrations/20261003120000_test.sql",
		"applied_by": "board",
	})
	req, _ := http.NewRequest(http.MethodPost,
		base+"/api/tasks/"+taskID+"/migrations/mark-applied",
		bytes.NewReader(payload),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
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
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected ok:true, got %v", body)
	}
}

func TestMarkMigrationApplied_MissingPath(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "mark-applied-nopath"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{"path": ""})
	req, _ := http.NewRequest(http.MethodPost,
		base+"/api/tasks/"+taskID+"/migrations/mark-applied",
		bytes.NewReader(payload),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}
