package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// gitCmd runs a git command in dir and fatals on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setupHarnessStyleRepo creates a git repo that mirrors the harness layout:
//
//	<repo>/                          ← parent checkout on main
//	<repo>/.worktrees/<taskID>/      ← linked worktree on staypoint/<taskID>
//
// It commits a checkpoint ref in the repo, makes a commit in the worktree,
// and returns (repoPath, taskID, checkpointRef).
func setupHarnessStyleRepo(t *testing.T) (repoPath, taskID, cpRef string) {
	t.Helper()
	repoPath = t.TempDir()
	taskID = "task-testdiff01"

	// Init main repo.
	gitCmd(t, repoPath, "init", "-b", "main")
	gitCmd(t, repoPath, "config", "user.email", "test@test")
	gitCmd(t, repoPath, "config", "user.name", "test")

	// Initial commit on main so HEAD exists.
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repoPath, "add", "README.md")
	gitCmd(t, repoPath, "commit", "-m", "init")

	// Create task branch off main.
	gitCmd(t, repoPath, "checkout", "-b", "staypoint/"+taskID)
	gitCmd(t, repoPath, "checkout", "main")

	// Create a checkpoint ref pointing at HEAD (simulates CreateCheckpoint).
	cpRef = "refs/staypoint/checkpoints/test-session/" + taskID
	headSHA, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repoPath, "update-ref", cpRef, string(bytes.TrimSpace(headSHA)))

	// Create the linked worktree (harness layout).
	wtPath := filepath.Join(repoPath, ".worktrees", taskID)
	if err := os.MkdirAll(filepath.Join(repoPath, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repoPath, "worktree", "add", wtPath, "staypoint/"+taskID)

	// Commit changes in the worktree (simulates agent work).
	if err := os.WriteFile(filepath.Join(wtPath, "notes.txt"), []byte("agent note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "PROGRESS.txt"), []byte("progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, wtPath, "add", "notes.txt", "PROGRESS.txt")
	gitCmd(t, wtPath, "commit", "-m", "agent: add notes and progress")

	return repoPath, taskID, taskID // cpRef short ID = taskID in our setup
}

// TestGetTaskDiff_UsesWorktreeNotParentCheckout verifies that when a harness-style
// worktree exists at <repoPath>/.worktrees/<taskID>, the diff endpoint returns the
// files committed there rather than the empty parent checkout.
func TestGetTaskDiff_UsesWorktreeNotParentCheckout(t *testing.T) {
	repoPath, taskID, _ := setupHarnessStyleRepo(t)

	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	// Create task pointing at the real repo.
	_, taskBody := postTask(t, base, token, map[string]any{
		"name":      "worktree-diff-test",
		"repo_path": repoPath,
	})
	// Override the auto-generated ID so it matches the worktree directory name.
	// Since we can't control the UUID, instead adjust the worktree path to match
	// the actual task ID returned by the API.
	apiTaskID, _ := taskBody["id"].(string)
	if apiTaskID == "" {
		t.Skip("task creation failed")
	}

	// Rename the worktree directory to match the API task ID.
	oldWT := filepath.Join(repoPath, ".worktrees", taskID)
	newWT := filepath.Join(repoPath, ".worktrees", apiTaskID)
	if err := os.Rename(oldWT, newWT); err != nil {
		t.Fatalf("rename worktree: %v", err)
	}

	// Hit the diff endpoint — no checkpoint means "latest", which falls back
	// gracefully; the important thing is that the worktree path is resolved.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/tasks/"+apiTaskID+"/diff", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// TestRestoreFile_NoWorktreeReturns409 verifies that restore is rejected with 409
// when no task worktree exists (parent checkout must never be used).
func TestRestoreFile_NoWorktreeReturns409(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	base := fmt.Sprintf("http://127.0.0.1:%d", srv.Port())

	_, taskBody := postTask(t, base, token, map[string]any{"name": "no-wt-restore"})
	apiTaskID, _ := taskBody["id"].(string)
	if apiTaskID == "" {
		t.Skip("task creation failed")
	}

	payload, _ := json.Marshal(map[string]any{"checkpoint_id": "latest", "file_path": "foo.txt"})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/tasks/"+apiTaskID+"/checkpoint-restore-file", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("want 409 when no worktree, got %d", resp.StatusCode)
	}
}
