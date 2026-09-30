package workspace

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
)

// WorktreeManager handles isolation of agent runs into separate git worktrees.
type WorktreeManager struct {
	RepoRoot string
	DB       *sql.DB
}

// NewWorktreeManager creates a new worktree manager for the given repository root.
func NewWorktreeManager(repoRoot string, db *sql.DB) *WorktreeManager {
	return &WorktreeManager{
		RepoRoot: repoRoot,
		DB:       db,
	}
}

// Create sets up a new git worktree for the given task.
// Branch name is staypoint/<taskID>, path is .worktrees/<taskID>.
func (w *WorktreeManager) Create(taskID string, sessionID string) (string, error) {
	wtPath := filepath.Join(w.RepoRoot, ".worktrees", taskID)
	branchName := fmt.Sprintf("staypoint/%s", taskID)

	// Ensure .worktrees exists
	if err := os.MkdirAll(filepath.Join(w.RepoRoot, ".worktrees"), 0755); err != nil {
		return "", fmt.Errorf("failed to create .worktrees dir: %w", err)
	}

	// Check if worktree already exists (e.g. from a previous crash)
	if _, err := os.Stat(wtPath); err == nil {
		// Clean up the stale worktree first
		if err := w.Prune(taskID); err != nil {
			return "", fmt.Errorf("failed to clean up stale worktree %s: %w", taskID, err)
		}
	}

	// git worktree add -b <branch> <path> HEAD
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, wtPath, "HEAD")
	cmd.Dir = w.RepoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// If branch already exists, we might need to just use it.
		// For simplicity, if it fails, try without -b assuming branch exists.
		if strings.Contains(string(out), "already exists") {
			cmd = exec.Command("git", "worktree", "add", wtPath, branchName)
			cmd.Dir = w.RepoRoot
			if out2, err2 := cmd.CombinedOutput(); err2 != nil {
				return "", fmt.Errorf("git worktree add failed: %s (first error: %s)", string(out2), string(out))
			}
		} else {
			return "", fmt.Errorf("git worktree add failed: %s", string(out))
		}
	}

	// Integrate with collision detection
	// We record the worktree root as a locked path to prevent other agents from claiming the same worktree.
	if w.DB != nil && sessionID != "" {
		err := telemetry.RecordWorkingFile(w.DB, sessionID, w.RepoRoot, wtPath, "worktree_lock", 24*time.Hour)
		if err != nil {
			// Non-fatal, but log it
			fmt.Fprintf(os.Stderr, "failed to record worktree lock for %s: %v\n", taskID, err)
		}
	}

	return wtPath, nil
}

// Prune removes the worktree for the given task and deletes its branch.
func (w *WorktreeManager) Prune(taskID string) error {
	wtPath := filepath.Join(w.RepoRoot, ".worktrees", taskID)
	branchName := fmt.Sprintf("staypoint/%s", taskID)

	// 1. git worktree remove --force <path>
	cmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
	cmd.Dir = w.RepoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		// If it's not a registered worktree but exists, rm -rf
		if _, statErr := os.Stat(wtPath); statErr == nil {
			_ = os.RemoveAll(wtPath)
		} else {
			// ignore if it doesn't exist at all
			if !strings.Contains(string(out), "does not exist") && !strings.Contains(string(out), "is not a working tree") {
				return fmt.Errorf("git worktree remove failed: %s", string(out))
			}
		}
	} else {
		// Also clean up lingering files just in case
		_ = os.RemoveAll(wtPath)
	}

	// 2. git branch -D <branch>
	cmd = exec.Command("git", "branch", "-D", branchName)
	cmd.Dir = w.RepoRoot
	_ = cmd.Run() // Ignore error if branch doesn't exist

	return nil
}

// SweepOrphans scans the .worktrees directory for any directories that do not
// have an active session in the database, and prunes them.
func (w *WorktreeManager) SweepOrphans() error {
	wtDir := filepath.Join(w.RepoRoot, ".worktrees")
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read .worktrees: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		
		// Check if this task is currently active.
		isActive := false
		if w.DB != nil {
			// Look for active sessions that locked this worktree
			relPath := filepath.Join(".worktrees", taskID)
			query := `
			SELECT COUNT(1) FROM agent_working_files w
			JOIN agent_sessions s ON w.session_id = s.id
			WHERE w.file_path = ? AND s.status = 'active'
			`
			var count int
			_ = w.DB.QueryRow(query, relPath).Scan(&count)
			if count > 0 {
				isActive = true
			}
		}

		if !isActive {
			// Orphan found. Prune it.
			if err := w.Prune(taskID); err != nil {
				fmt.Fprintf(os.Stderr, "failed to prune orphan worktree %s: %v\n", taskID, err)
			}
		}
	}

	return nil
}
