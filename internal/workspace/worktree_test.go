package workspace

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
	_ "modernc.org/sqlite"
)

func setupTestRepo(t *testing.T) (string, *sql.DB) {
	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "initial")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}

	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Setup basic tables
	tables := []string{
		`CREATE TABLE agent_sessions (id TEXT PRIMARY KEY, agent_type TEXT, repo_path TEXT, git_branch TEXT, pid INTEGER, hostname TEXT, status TEXT, started_at TEXT, last_heartbeat_at TEXT, metadata_json TEXT)`,
		`CREATE TABLE agent_working_files (session_id TEXT, repo_path TEXT, file_path TEXT, access_type TEXT, first_touched_at TEXT, last_touched_at TEXT, expires_at TEXT, PRIMARY KEY (session_id, file_path))`,
	}
	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			t.Fatalf("failed to create table: %v", err)
		}
	}

	return dir, db
}

func TestWorktreeManager_CreateAndPrune(t *testing.T) {
	repoRoot, db := setupTestRepo(t)
	defer db.Close()

	wm := NewWorktreeManager(repoRoot, db)
	taskID := "task-123"
	sessionID := "sess-abc"

	// Register session
	err := telemetry.HeartbeatSession(db, telemetry.AgentSession{
		ID:       sessionID,
		Status:   "active",
		RepoPath: repoRoot,
	})
	if err != nil {
		t.Fatalf("failed to heartbeat session: %v", err)
	}

	wtPath, err := wm.Create(taskID, sessionID)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Errorf("worktree path %s does not exist", wtPath)
	}

	// Check that lock was recorded
	var count int
	relPath := filepath.Join(".worktrees", taskID)
	err = db.QueryRow(`SELECT COUNT(1) FROM agent_working_files WHERE session_id = ? AND file_path = ?`, sessionID, relPath).Scan(&count)
	if err != nil || count == 0 {
		t.Errorf("expected working file record for worktree %s, got count %d", relPath, count)
	}

	err = wm.Prune(taskID)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree path %s still exists after prune", wtPath)
	}
}

func TestWorktreeManager_SweepOrphans(t *testing.T) {
	repoRoot, db := setupTestRepo(t)
	defer db.Close()

	wm := NewWorktreeManager(repoRoot, db)

	// Create an orphan
	taskID := "orphan-task"
	_, err := wm.Create(taskID, "non-existent-session")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	wtDir := filepath.Join(repoRoot, ".worktrees", taskID)
	if _, err := os.Stat(wtDir); os.IsNotExist(err) {
		t.Fatalf("orphan worktree not created")
	}

	// Sweep
	err = wm.SweepOrphans()
	if err != nil {
		t.Fatalf("SweepOrphans failed: %v", err)
	}

	// Should be gone
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Errorf("orphan worktree still exists after sweep")
	}
}
