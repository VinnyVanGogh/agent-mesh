package context

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
)

// Task represents an engineering task tracked within SQLite mesh.db.
type Task struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	RepoPath    string  `json:"repo_path"`
	GitBranch   string  `json:"git_branch"`
	Status      string  `json:"status"` // active, done, soft_deleted
	AccountRole string  `json:"account_role"` // work, personal, other
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	DeletedAt   *string `json:"deleted_at,omitempty"`
}

// GetCurrentGitBranch returns the current active git branch for a directory.
func GetCurrentGitBranch(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "main"
	}
	return branch
}

// CreateTask inserts a new active task into the database.
func CreateTask(db *sql.DB, name, repoPath, gitBranch, role string) (*Task, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("task name cannot be empty")
	}

	if repoPath == "" {
		var err error
		repoPath, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err == nil {
		repoPath = absRepoPath
	}

	if gitBranch == "" {
		gitBranch = GetCurrentGitBranch(repoPath)
	}

	if role == "" {
		if bridge.IsWorkRepo(repoPath) {
			role = "work"
		} else {
			role = "personal"
		}
	}

	taskID := fmt.Sprintf("task-%s", uuid.New().String()[:8])

	query := `
		INSERT INTO tasks (id, name, repo_path, git_branch, status, account_role, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`

	if _, err := db.Exec(query, taskID, name, repoPath, gitBranch, role); err != nil {
		return nil, fmt.Errorf("failed to insert task: %w", err)
	}

	return GetTask(db, taskID)
}

// ListTasks queries active tasks, or all non-deleted tasks if includeAll is true.
func ListTasks(db *sql.DB, includeAll bool) ([]Task, error) {
	var query string
	if includeAll {
		query = `
			SELECT id, name, repo_path, git_branch, status, account_role, created_at, updated_at, deleted_at
			FROM tasks
			WHERE status != 'soft_deleted'
			ORDER BY created_at DESC
		`
	} else {
		query = `
			SELECT id, name, repo_path, git_branch, status, account_role, created_at, updated_at, deleted_at
			FROM tasks
			WHERE status = 'active'
			ORDER BY created_at DESC
		`
	}

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var deletedAt sql.NullString
		if err := rows.Scan(
			&t.ID,
			&t.Name,
			&t.RepoPath,
			&t.GitBranch,
			&t.Status,
			&t.AccountRole,
			&t.CreatedAt,
			&t.UpdatedAt,
			&deletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan task row: %w", err)
		}
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// GetTask fetches a single task by exact ID or ID prefix.
func GetTask(db *sql.DB, id string) (*Task, error) {
	id = strings.TrimSpace(id)
	query := `
		SELECT id, name, repo_path, git_branch, status, account_role, created_at, updated_at, deleted_at
		FROM tasks
		WHERE id = ? OR id = ? OR id LIKE ?
		ORDER BY created_at DESC
		LIMIT 1
	`
	prefixMatch := id + "%"
	fullID := id
	if !strings.HasPrefix(fullID, "task-") {
		fullID = "task-" + id
	}

	row := db.QueryRow(query, id, fullID, prefixMatch)
	var t Task
	var deletedAt sql.NullString
	if err := row.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if deletedAt.Valid {
		t.DeletedAt = &deletedAt.String
	}
	return &t, nil
}

// GetActiveTaskForRepo finds the most recently updated active task for a repo (or globally).
func GetActiveTaskForRepo(db *sql.DB, repoPath string) (*Task, error) {
	cleanPath := filepath.Clean(repoPath)

	// First try exact or prefix match on repo_path
	query := `
		SELECT id, name, repo_path, git_branch, status, account_role, created_at, updated_at, deleted_at
		FROM tasks
		WHERE status = 'active' AND (repo_path = ? OR repo_path LIKE ?)
		ORDER BY updated_at DESC
		LIMIT 1
	`
	row := db.QueryRow(query, cleanPath, cleanPath+"/%")
	var t Task
	var deletedAt sql.NullString
	err := row.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	)
	if err == nil {
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		return &t, nil
	}

	// Fallback to most recent active task in any repo
	fallbackQuery := `
		SELECT id, name, repo_path, git_branch, status, account_role, created_at, updated_at, deleted_at
		FROM tasks
		WHERE status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1
	`
	fbRow := db.QueryRow(fallbackQuery)
	err = fbRow.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	)
	if err == nil {
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		return &t, nil
	}

	return nil, fmt.Errorf("no active tasks found")
}

// MarkTaskDone updates the task's status to 'done'.
func MarkTaskDone(db *sql.DB, id string) error {
	task, err := GetTask(db, id)
	if err != nil {
		return err
	}

	query := `
		UPDATE tasks
		SET status = 'done', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	res, err := db.Exec(query, task.ID)
	if err != nil {
		return fmt.Errorf("failed to mark task as done: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

// DeleteTask marks a task as soft_deleted.
func DeleteTask(db *sql.DB, id string) error {
	task, err := GetTask(db, id)
	if err != nil {
		return err
	}

	query := `
		UPDATE tasks
		SET status = 'soft_deleted', deleted_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	res, err := db.Exec(query, task.ID)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}
