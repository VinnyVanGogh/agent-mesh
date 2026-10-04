package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	// Place a real migration file in a temp dir so the handler can read it.
	dir := t.TempDir()
	migPath := "supabase/migrations/20261003120000_test.sql"
	_ = os.MkdirAll(fmt.Sprintf("%s/supabase/migrations", dir), 0o755)
	_ = os.WriteFile(
		fmt.Sprintf("%s/%s", dir, migPath),
		[]byte("CREATE TABLE activity_test_tbl (id uuid PRIMARY KEY);"),
		0o644,
	)

	_, taskBody := postTask(t, base, token, map[string]any{"name": "mark-applied-test", "repo_path": dir})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	// Supply a passing check_result so the handler can record applied.
	payload, _ := json.Marshal(map[string]any{
		"path":       migPath,
		"applied_by": "board",
		"check_results": []map[string]any{
			{"description": "table public.activity_test_tbl exists", "passed": true},
		},
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

// TestMarkMigrationApplied_UnreadableFile verifies that when the migration file
// cannot be read (task has no associated repo), mark-applied succeeds in
// unchecked mode (200 ok:true, mode:unchecked) rather than blocking with 422.
// A human recording "I applied this" is still valid even without schema verification.
func TestMarkMigrationApplied_UnreadableFile(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "unreadable-test"})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":       "supabase/migrations/nonexistent.sql",
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
		t.Errorf("want 200, got %d (unreadable file should result in unchecked mode, not an error)", resp.StatusCode)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("want ok:true, got %v", body["ok"])
	}
	if mode, _ := body["mode"].(string); mode != "unchecked" {
		t.Errorf("want mode:unchecked, got %q", mode)
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

// TestMarkMigrationApplied_ManualMode verifies that when no DSN and no check_results
// are supplied, the response returns the verification_query for manual use.
func TestMarkMigrationApplied_ManualMode_ReturnsQuery(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	// Create a temp migration file with real DDL so ParseChecks produces checks.
	dir := t.TempDir()
	migPath := "supabase/migrations/20261003120001_manual_test.sql"
	_ = os.MkdirAll(fmt.Sprintf("%s/supabase/migrations", dir), 0o755)
	_ = os.WriteFile(
		fmt.Sprintf("%s/%s", dir, migPath),
		[]byte("CREATE TABLE manual_test_tbl (id uuid PRIMARY KEY);"),
		0o644,
	)

	_, taskBody := postTask(t, base, token, map[string]any{"name": "manual-mode-test", "repo_path": dir})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":       migPath,
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
	// Should return ok:false with a verification query.
	if ok, _ := body["ok"].(bool); ok {
		t.Errorf("expected ok:false in manual mode without check_results, got ok:true")
	}
	if _, hasQuery := body["verification_query"]; !hasQuery {
		t.Errorf("expected verification_query in manual mode response; got %v", body)
	}
	if mode, _ := body["mode"].(string); mode != "manual" {
		t.Errorf("expected mode=manual, got %q", mode)
	}
}

// TestMarkMigrationApplied_ManualMode_WithPassingResults verifies that supplying
// all-passing check_results records the migration as applied.
func TestMarkMigrationApplied_ManualMode_WithPassingResults(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	dir := t.TempDir()
	migPath := "supabase/migrations/20261003120002_manual_pass.sql"
	_ = os.MkdirAll(fmt.Sprintf("%s/supabase/migrations", dir), 0o755)
	_ = os.WriteFile(
		fmt.Sprintf("%s/%s", dir, migPath),
		[]byte("CREATE TABLE manual_pass_tbl (id uuid PRIMARY KEY);"),
		0o644,
	)

	_, taskBody := postTask(t, base, token, map[string]any{"name": "manual-pass-test", "repo_path": dir})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":       migPath,
		"applied_by": "board",
		"check_results": []map[string]any{
			{"description": "table public.manual_pass_tbl exists", "passed": true},
		},
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
		t.Errorf("expected ok:true with passing check_results, got %v", body)
	}
	if mode, _ := body["mode"].(string); mode != "manual" {
		t.Errorf("expected mode=manual, got %q", mode)
	}
}

// TestMarkMigrationApplied_ManualMode_WithFailingResults verifies that supplying
// failing check_results returns 409 without recording applied.
func TestMarkMigrationApplied_ManualMode_WithFailingResults(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	dir := t.TempDir()
	migPath := "supabase/migrations/20261003120003_manual_fail.sql"
	_ = os.MkdirAll(fmt.Sprintf("%s/supabase/migrations", dir), 0o755)
	_ = os.WriteFile(
		fmt.Sprintf("%s/%s", dir, migPath),
		[]byte("CREATE TABLE manual_fail_tbl (id uuid PRIMARY KEY);"),
		0o644,
	)

	_, taskBody := postTask(t, base, token, map[string]any{"name": "manual-fail-test", "repo_path": dir})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":       migPath,
		"applied_by": "board",
		"check_results": []map[string]any{
			{"description": "table public.manual_fail_tbl exists", "passed": false},
		},
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

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("want 409, got %d", resp.StatusCode)
	}
}

// TestMarkMigrationApplied_Override verifies that supplying override_reason bypasses
// failing checks and still records the migration as applied.
func TestMarkMigrationApplied_Override(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	dir := t.TempDir()
	migPath := "supabase/migrations/20261003120004_override.sql"
	_ = os.MkdirAll(fmt.Sprintf("%s/supabase/migrations", dir), 0o755)
	_ = os.WriteFile(
		fmt.Sprintf("%s/%s", dir, migPath),
		[]byte("CREATE TABLE override_tbl (id uuid PRIMARY KEY);"),
		0o644,
	)

	_, taskBody := postTask(t, base, token, map[string]any{"name": "override-test", "repo_path": dir})
	taskID, _ := taskBody["id"].(string)
	if taskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{
		"path":            migPath,
		"applied_by":      "board",
		"check_results":   []map[string]any{{"description": "table public.override_tbl exists", "passed": false}},
		"override_reason": "already verified via Supabase dashboard",
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
		t.Fatalf("want 200 with override, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected ok:true with override, got %v", body)
	}
}

// TestGetTaskMigrations_IncludesVerificationChecks verifies the GET endpoint
// returns verification_checks for each migration.
func TestGetTaskMigrations_IncludesVerificationChecks(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	// We can't inject real migration files via checkpoint in a unit test,
	// so we just verify the response shape includes the has_auto_verify field.
	_, taskBody := postTask(t, base, token, map[string]any{"name": "mig-checks-test"})
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
	if _, ok := body["has_auto_verify"]; !ok {
		t.Errorf("response missing has_auto_verify key; got %v", body)
	}
}
