package context

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
	"github.com/google/uuid"
)

// Task represents an engineering task tracked within SQLite mesh.db.
type Task struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	RepoPath        string  `json:"repo_path"`
	GitBranch       string  `json:"git_branch"`
	Status          string  `json:"status"`       // active, done, soft_deleted
	AccountRole     string  `json:"account_role"` // work, personal, other
	Priority        string  `json:"priority"`     // critical, urgent, high, medium, low
	MaxBudgetUSD    float64 `json:"max_budget_usd"`
	MaxTurns        int     `json:"max_turns"`
	SpentTokens     int64   `json:"spent_tokens"`
	SpentUSD        float64 `json:"spent_usd"`
	SpentTurns      int     `json:"spent_turns"`
	Organization    string  `json:"organization,omitempty"`
	Project         string  `json:"project,omitempty"`
	ParentID        string  `json:"parent_id,omitempty"`
	ExecutionStage  string  `json:"execution_stage"`
	CheckoutRunID   string  `json:"checkout_run_id,omitempty"`
	CheckoutAgentID string  `json:"checkout_agent_id,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	DeletedAt       *string `json:"deleted_at,omitempty"`
	IsBlocked       bool    `json:"is_blocked"`
	BlockReason     string  `json:"block_reason"`
}

// TaskCreateOptions holds configuration for creating a task with budgets.
type TaskCreateOptions struct {
	Name         string
	RepoPath     string
	GitBranch    string
	AccountRole  string
	Priority     string
	MaxBudgetUSD float64
	MaxTurns     int
	Organization string
	Project      string
	ParentID     string
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
	return CreateTaskWithOptions(db, TaskCreateOptions{
		Name:        name,
		RepoPath:    repoPath,
		GitBranch:   gitBranch,
		AccountRole: role,
	})
}

// CreateTaskWithOptions inserts a new task with budget and turn limit configurations.
func CreateTaskWithOptions(db *sql.DB, opts TaskCreateOptions) (*Task, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return nil, fmt.Errorf("task name cannot be empty")
	}

	repoPath := opts.RepoPath
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

	gitBranch := opts.GitBranch
	if gitBranch == "" {
		gitBranch = GetCurrentGitBranch(repoPath)
	}

	role := opts.AccountRole
	if role == "" {
		if bridge.IsWorkRepo(repoPath) {
			role = "work"
		} else {
			role = "personal"
		}
	}

	priority := strings.ToLower(strings.TrimSpace(opts.Priority))
	if priority == "" {
		priority = "medium"
	}

	taskID := fmt.Sprintf("task-%s", uuid.New().String()[:8])

	query := `
		INSERT INTO tasks (
			id, name, repo_path, git_branch, status, account_role, priority,
			max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
			organization, project, parent_id,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, 0, 0.0, 0, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`

	var parentID interface{}
	if opts.ParentID != "" {
		parentID = opts.ParentID
	}

	if _, err := db.Exec(query, taskID, name, repoPath, gitBranch, role, priority, opts.MaxBudgetUSD, opts.MaxTurns, opts.Organization, opts.Project, parentID); err != nil {
		return nil, fmt.Errorf("failed to insert task: %w", err)
	}

	// Waking the agent as the task is ready for assignment/pickup
	_ = orchestrator.NotifyDaemon(taskID, "assignment", "")

	return GetTask(db, taskID)
}

// RecordTaskSpend updates the cumulative token, dollar, and turn spend on a task.
func RecordTaskSpend(db *sql.DB, taskID string, tokens int64, costUSD float64, turns int) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}

	query := `
		UPDATE tasks
		SET spent_tokens = spent_tokens + ?,
		    spent_usd = spent_usd + ?,
		    spent_turns = spent_turns + ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	res, err := db.Exec(query, tokens, costUSD, turns, task.ID)
	if err != nil {
		return fmt.Errorf("failed to record task spend: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// UpdateTaskBudget updates the budget limits for an existing task.
func UpdateTaskBudget(db *sql.DB, taskID string, maxUSD float64, maxTurns int) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}

	query := `
		UPDATE tasks
		SET max_budget_usd = ?, max_turns = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	res, err := db.Exec(query, maxUSD, maxTurns, task.ID)
	if err != nil {
		return fmt.Errorf("failed to update task budget: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// UpdateTaskPriority updates the priority level for an existing task.
func UpdateTaskPriority(db *sql.DB, taskID string, priority string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}

	norm := strings.ToLower(strings.TrimSpace(priority))
	if norm == "" {
		norm = "medium"
	}

	query := `
		UPDATE tasks
		SET priority = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	res, err := db.Exec(query, norm, task.ID)
	if err != nil {
		return fmt.Errorf("failed to update task priority: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}
	return nil
}

// BudgetEvaluation encapsulates the status of a task's spending limits.
type BudgetEvaluation struct {
	IsBlocked bool
	IsWarning bool
	Reason    string
	PctBudget float64
	PctTurns  float64
}

// EvaluateTaskBudget determines whether a task is approaching or has exceeded its limits.
func EvaluateTaskBudget(task *Task) BudgetEvaluation {
	var eval BudgetEvaluation
	if task == nil {
		return eval
	}

	if task.MaxBudgetUSD > 0 {
		eval.PctBudget = (task.SpentUSD / task.MaxBudgetUSD) * 100.0
		if task.SpentUSD >= task.MaxBudgetUSD {
			eval.IsBlocked = true
			eval.Reason = fmt.Sprintf("Dollar budget exhausted ($%.2f spent / $%.2f limit)", task.SpentUSD, task.MaxBudgetUSD)
			return eval
		} else if eval.PctBudget >= 80.0 {
			eval.IsWarning = true
			eval.Reason = fmt.Sprintf("Dollar budget at %.0f%% ($%.2f spent / $%.2f limit)", eval.PctBudget, task.SpentUSD, task.MaxBudgetUSD)
		}
	}

	if task.MaxTurns > 0 {
		eval.PctTurns = (float64(task.SpentTurns) / float64(task.MaxTurns)) * 100.0
		if task.SpentTurns >= task.MaxTurns {
			eval.IsBlocked = true
			eval.Reason = fmt.Sprintf("Turn limit exhausted (%d turns spent / %d turn limit)", task.SpentTurns, task.MaxTurns)
			return eval
		} else if eval.PctTurns >= 80.0 {
			eval.IsWarning = true
			eval.Reason = fmt.Sprintf("Turn limit at %.0f%% (%d turns spent / %d turn limit)", eval.PctTurns, task.SpentTurns, task.MaxTurns)
		}
	}

	return eval
}

// ListTasks queries active tasks, or all non-deleted tasks if includeAll is true.
func ListTasks(db *sql.DB, includeAll bool) ([]Task, error) {
	var query string
	if includeAll {
		query = `
			SELECT id, name, repo_path, git_branch, status, account_role, COALESCE(priority, 'medium'),
			       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
			       organization, project, parent_id, execution_stage, checkout_run_id, checkout_agent_id,
			       is_blocked, block_reason, created_at, updated_at, deleted_at
			FROM tasks
			WHERE status != 'soft_deleted'
			ORDER BY created_at DESC
		`
	} else {
		query = `
			SELECT id, name, repo_path, git_branch, status, account_role, COALESCE(priority, 'medium'),
			       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
			       organization, project, parent_id, execution_stage, checkout_run_id, checkout_agent_id,
			       is_blocked, block_reason, created_at, updated_at, deleted_at
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
		var priority sql.NullString
		var deletedAt, org, proj, blockReason, parentID, checkoutRunID, checkoutAgentID sql.NullString
		if err := rows.Scan(
			&t.ID,
			&t.Name,
			&t.RepoPath,
			&t.GitBranch,
			&t.Status,
			&t.AccountRole,
			&priority,
			&t.MaxBudgetUSD,
			&t.MaxTurns,
			&t.SpentTokens,
			&t.SpentUSD,
			&t.SpentTurns,
			&org,
			&proj,
			&parentID,
			&t.ExecutionStage,
			&checkoutRunID,
			&checkoutAgentID,
			&t.IsBlocked,
			&blockReason,
			&t.CreatedAt,
			&t.UpdatedAt,
			&deletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan task row: %w", err)
		}
		if priority.Valid && priority.String != "" {
			t.Priority = priority.String
		} else {
			t.Priority = "medium"
		}
		if blockReason.Valid {
			t.BlockReason = blockReason.String
		}
		if parentID.Valid {
			t.ParentID = parentID.String
		}
		if checkoutRunID.Valid {
			t.CheckoutRunID = checkoutRunID.String
		}
		if checkoutAgentID.Valid {
			t.CheckoutAgentID = checkoutAgentID.String
		}
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		if org.Valid {
			t.Organization = org.String
		}
		if proj.Valid {
			t.Project = proj.String
		}
		if org.Valid {
			t.Organization = org.String
		}
		if proj.Valid {
			t.Project = proj.String
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// GetTask fetches a single task by exact ID or ID prefix.
func GetTask(db *sql.DB, id string) (*Task, error) {
	id = strings.TrimSpace(id)
	query := `
		SELECT id, name, repo_path, git_branch, status, account_role, COALESCE(priority, 'medium'),
		       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
		       organization, project, parent_id, execution_stage, checkout_run_id, checkout_agent_id, is_blocked, block_reason, created_at, updated_at, deleted_at
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
	var priority sql.NullString
	var deletedAt, org, proj, blockReason, parentID, checkoutRunID, checkoutAgentID sql.NullString
	if err := row.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&priority,
		&t.MaxBudgetUSD,
		&t.MaxTurns,
		&t.SpentTokens,
		&t.SpentUSD,
		&t.SpentTurns,
		&org,
		&proj,
		&parentID,
		&t.ExecutionStage,
		&checkoutRunID,
		&checkoutAgentID,
		&t.IsBlocked,
		&blockReason,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %s", id)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	if priority.Valid && priority.String != "" {
		t.Priority = priority.String
	} else {
		t.Priority = "medium"
	}
	if blockReason.Valid {
		t.BlockReason = blockReason.String
	}
	if parentID.Valid {
		t.ParentID = parentID.String
	}
	if checkoutRunID.Valid {
		t.CheckoutRunID = checkoutRunID.String
	}
	if checkoutAgentID.Valid {
		t.CheckoutAgentID = checkoutAgentID.String
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
		SELECT id, name, repo_path, git_branch, status, account_role, COALESCE(priority, 'medium'),
		       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
		       organization, project, parent_id, execution_stage, checkout_run_id, checkout_agent_id, is_blocked, block_reason, created_at, updated_at, deleted_at
		FROM tasks
		WHERE status = 'active' AND (repo_path = ? OR repo_path LIKE ?)
		ORDER BY updated_at DESC
		LIMIT 1
	`
	row := db.QueryRow(query, cleanPath, cleanPath+"/%")
	var t Task
	var priority sql.NullString
	var deletedAt, org, proj, blockReason, parentID, checkoutRunID, checkoutAgentID sql.NullString
	err := row.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&priority,
		&t.MaxBudgetUSD,
		&t.MaxTurns,
		&t.SpentTokens,
		&t.SpentUSD,
		&t.SpentTurns,
		&org,
		&proj,
		&parentID,
		&t.ExecutionStage,
		&checkoutRunID,
		&checkoutAgentID,
		&t.IsBlocked,
		&blockReason,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	)
	if err == nil {
		if priority.Valid && priority.String != "" {
			t.Priority = priority.String
		} else {
			t.Priority = "medium"
		}
		if blockReason.Valid {
			t.BlockReason = blockReason.String
		}
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		if org.Valid {
			t.Organization = org.String
		}
		if proj.Valid {
			t.Project = proj.String
		}
		return &t, nil
	}

	// Fallback to most recent active task in any repo
	fallbackQuery := `
		SELECT id, name, repo_path, git_branch, status, account_role, COALESCE(priority, 'medium'),
		       max_budget_usd, max_turns, spent_tokens, spent_usd, spent_turns,
		       organization, project, parent_id, execution_stage, checkout_run_id, checkout_agent_id, is_blocked, block_reason, created_at, updated_at, deleted_at
		FROM tasks
		WHERE status = 'active'
		ORDER BY updated_at DESC
		LIMIT 1
	`
	fbRow := db.QueryRow(fallbackQuery)
	var fbPriority sql.NullString
	err = fbRow.Scan(
		&t.ID,
		&t.Name,
		&t.RepoPath,
		&t.GitBranch,
		&t.Status,
		&t.AccountRole,
		&fbPriority,
		&t.MaxBudgetUSD,
		&t.MaxTurns,
		&t.SpentTokens,
		&t.SpentUSD,
		&t.SpentTurns,
		&org,
		&proj,
		&parentID,
		&t.ExecutionStage,
		&checkoutRunID,
		&checkoutAgentID,
		&t.IsBlocked,
		&blockReason,
		&t.CreatedAt,
		&t.UpdatedAt,
		&deletedAt,
	)
	if err == nil {
		if fbPriority.Valid && fbPriority.String != "" {
			t.Priority = fbPriority.String
		} else {
			t.Priority = "medium"
		}
		if blockReason.Valid {
			t.BlockReason = blockReason.String
		}
		if deletedAt.Valid {
			t.DeletedAt = &deletedAt.String
		}
		if org.Valid {
			t.Organization = org.String
		}
		if proj.Valid {
			t.Project = proj.String
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

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_work_products WHERE task_id = ?`, task.ID).Scan(&count); err != nil {
		return fmt.Errorf("failed to check work products: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("cannot mark task as done without a registered work product")
	}

	query := `
		UPDATE tasks
		SET status = 'done', execution_stage = 'done', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
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

	// Unblock any tasks that were waiting on this one
	rows, err := db.Query(`SELECT blocks_id FROM task_relations WHERE task_id = ?`, task.ID)
	if err == nil {
		var blockedIDs []string
		for rows.Next() {
			var bid string
			if err := rows.Scan(&bid); err == nil {
				blockedIDs = append(blockedIDs, bid)
			}
		}
		rows.Close()

		for _, bid := range blockedIDs {
			_ = UnblockTask(db, bid, task.ID)
		}
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

type TaskComment struct {
	ID        int    `json:"id"`
	TaskID    string `json:"task_id"`
	Author    string `json:"author"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

func AddTaskComment(db *sql.DB, taskID, author, message string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}
	query := `INSERT INTO task_comments (task_id, author, message) VALUES (?, ?, ?)`
	if _, err := db.Exec(query, task.ID, author, message); err != nil {
		return err
	}

	// Any pending interactions configured to supersede on comment are superseded
	_, _ = SupersedeInteractionsOnComment(db, task.ID)
	_ = orchestrator.NotifyDaemon(task.ID, "comment", "")
	return nil
}

func GetTaskComments(db *sql.DB, taskID string) ([]TaskComment, error) {
	task, err := GetTask(db, taskID)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, task_id, author, message, created_at FROM task_comments WHERE task_id = ? ORDER BY created_at ASC`
	rows, err := db.Query(query, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []TaskComment
	for rows.Next() {
		var c TaskComment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Author, &c.Message, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

func BlockTask(db *sql.DB, taskID, reason string, blockedByIDs ...string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	query := `UPDATE tasks SET is_blocked = 1, block_reason = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`
	if _, err := tx.Exec(query, reason, task.ID); err != nil {
		tx.Rollback()
		return err
	}

	for _, blockedByID := range blockedByIDs {
		blockedByTask, err := GetTask(db, blockedByID)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`INSERT INTO task_relations (task_id, blocks_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, blockedByTask.ID, task.ID); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func UnblockTask(db *sql.DB, taskID string, unblockFromIDs ...string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	var actuallyUnblocked bool
	if len(unblockFromIDs) > 0 {
		for _, unblockFromID := range unblockFromIDs {
			unblockFromTask, err := GetTask(db, unblockFromID)
			if err != nil {
				tx.Rollback()
				return err
			}
			if _, err := tx.Exec(`DELETE FROM task_relations WHERE task_id = ? AND blocks_id = ?`, unblockFromTask.ID, task.ID); err != nil {
				tx.Rollback()
				return err
			}
		}

		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM task_relations WHERE blocks_id = ?`, task.ID).Scan(&count); err != nil {
			tx.Rollback()
			return err
		}

		if count == 0 {
			if _, err := tx.Exec(`UPDATE tasks SET is_blocked = 0, block_reason = '', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, task.ID); err != nil {
				tx.Rollback()
				return err
			}
			actuallyUnblocked = true
		}
	} else {
		if _, err := tx.Exec(`DELETE FROM task_relations WHERE blocks_id = ?`, task.ID); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET is_blocked = 0, block_reason = '', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, task.ID); err != nil {
			tx.Rollback()
			return err
		}
		actuallyUnblocked = true
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if actuallyUnblocked {
		_ = orchestrator.NotifyDaemon(task.ID, "blocker_cleared", "")
	}

	return nil
}

func ArchiveTask(db *sql.DB, taskID string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}
	// "cancel" / "archive" transitions to soft_deleted according to scope? Or maybe "cancelled" / "archived"?
	// scope says: "Transitions task to cancelled/archived state cleanly"
	// In the DB constraint: status IN ('active', 'done', 'soft_deleted').
	// Let's just use 'soft_deleted' and soft-delete it or change DB schema to allow 'cancelled' and 'archived'?
	// The CLI code says: if iss.Status == "done" || iss.Status == "cancelled"
	// Wait, the DB check constraint is: status IN ('active', 'done', 'soft_deleted'). So cancelled could just be 'soft_deleted'.
	query := `UPDATE tasks SET status = 'soft_deleted', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`
	_, err = db.Exec(query, task.ID)
	return err
}

func TouchTask(db *sql.DB, taskID string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}
	query := `UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`
	_, err = db.Exec(query, task.ID)
	return err
}

type TaskDocument struct {
	ID        int    `json:"id"`
	TaskID    string `json:"task_id"`
	DocKey    string `json:"doc_key"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type TaskWorkProduct struct {
	ID          int    `json:"id"`
	TaskID      string `json:"task_id"`
	ProductType string `json:"product_type"`
	Reference   string `json:"reference"`
	CreatedAt   string `json:"created_at"`
}

type ActivityLog struct {
	ID        int    `json:"id"`
	TaskID    string `json:"task_id"`
	EventType string `json:"event_type"`
	Details   string `json:"details"`
	CreatedAt string `json:"created_at"`
}

func AddTaskDocument(db *sql.DB, taskID, docKey, content string) error {
	var maxVer sql.NullInt32
	err := db.QueryRow(`SELECT MAX(version) FROM task_documents WHERE task_id = ? AND doc_key = ?`, taskID, docKey).Scan(&maxVer)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	v := 1
	if maxVer.Valid {
		v = int(maxVer.Int32) + 1
	}
	_, err = db.Exec(`INSERT INTO task_documents (task_id, doc_key, version, content) VALUES (?, ?, ?, ?)`, taskID, docKey, v, content)
	return err
}

func AddWorkProduct(db *sql.DB, taskID, productType, reference string) error {
	_, err := db.Exec(`INSERT INTO task_work_products (task_id, product_type, reference) VALUES (?, ?, ?)`, taskID, productType, reference)
	return err
}

func LogActivity(db *sql.DB, taskID, eventType, details string) error {
	_, err := db.Exec(`INSERT INTO activity_log (task_id, event_type, details) VALUES (?, ?, ?)`, taskID, eventType, details)
	return err
}

func GetLatestTaskDocument(db *sql.DB, taskID, docKey string) (*TaskDocument, error) {
	task, err := GetTask(db, taskID)
	if err != nil {
		return nil, err
	}
	var doc TaskDocument
	query := `SELECT id, task_id, doc_key, version, content, created_at FROM task_documents WHERE task_id = ? AND doc_key = ? ORDER BY version DESC LIMIT 1`
	err = db.QueryRow(query, task.ID, docKey).Scan(&doc.ID, &doc.TaskID, &doc.DocKey, &doc.Version, &doc.Content, &doc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func GetTaskDocumentRevision(db *sql.DB, taskID, docKey string, version int) (*TaskDocument, error) {
	task, err := GetTask(db, taskID)
	if err != nil {
		return nil, err
	}
	var doc TaskDocument
	query := `SELECT id, task_id, doc_key, version, content, created_at FROM task_documents WHERE task_id = ? AND doc_key = ? AND version = ?`
	err = db.QueryRow(query, task.ID, docKey, version).Scan(&doc.ID, &doc.TaskID, &doc.DocKey, &doc.Version, &doc.Content, &doc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func ListTaskDocuments(db *sql.DB, taskID string) ([]TaskDocument, error) {
	task, err := GetTask(db, taskID)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, task_id, doc_key, version, content, created_at FROM task_documents WHERE task_id = ? ORDER BY doc_key ASC, version DESC`
	rows, err := db.Query(query, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []TaskDocument
	for rows.Next() {
		var doc TaskDocument
		if err := rows.Scan(&doc.ID, &doc.TaskID, &doc.DocKey, &doc.Version, &doc.Content, &doc.CreatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// SetTaskExecutionStage updates the execution stage of a task and sets status accordingly.
func SetTaskExecutionStage(db *sql.DB, taskID, stage string) error {
	task, err := GetTask(db, taskID)
	if err != nil {
		return err
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	status := "active"
	if stage == "done" {
		status = "done"
	}
	query := `UPDATE tasks SET execution_stage = ?, status = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`
	if _, err := db.Exec(query, stage, status, task.ID); err != nil {
		return fmt.Errorf("failed to set task execution stage: %w", err)
	}
	_ = LogActivity(db, task.ID, "stage_change", fmt.Sprintf("execution stage set to %s", stage))
	return nil
}

// GetTaskWorkProducts retrieves all work products associated with a task.
func GetTaskWorkProducts(db *sql.DB, taskID string) ([]TaskWorkProduct, error) {
	query := `SELECT id, task_id, product_type, reference, created_at FROM task_work_products WHERE task_id = ? ORDER BY created_at ASC`
	rows, err := db.Query(query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []TaskWorkProduct
	for rows.Next() {
		var p TaskWorkProduct
		if err := rows.Scan(&p.ID, &p.TaskID, &p.ProductType, &p.Reference, &p.CreatedAt); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

// GetTaskActivityLog retrieves all activity log entries for a task.
func GetTaskActivityLog(db *sql.DB, taskID string) ([]ActivityLog, error) {
	query := `SELECT id, task_id, event_type, details, created_at FROM activity_log WHERE task_id = ? ORDER BY created_at ASC`
	rows, err := db.Query(query, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []ActivityLog
	for rows.Next() {
		var l ActivityLog
		if err := rows.Scan(&l.ID, &l.TaskID, &l.EventType, &l.Details, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

