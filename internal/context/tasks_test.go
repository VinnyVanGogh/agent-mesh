package context

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test-mesh.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})
	return store.DB()
}

func TestTaskCRUD(t *testing.T) {
	database := setupTestDB(t)

	// 1. Create Task
	task1, err := CreateTask(database, "Implement bridge package", "/path/to/personal/agent-mesh", "feature/bridge", "personal")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if task1.ID == "" {
		t.Fatalf("expected generated ID, got empty")
	}
	if task1.Name != "Implement bridge package" {
		t.Errorf("unexpected name: %s", task1.Name)
	}
	if task1.Status != "active" {
		t.Errorf("unexpected status: %s", task1.Status)
	}

	task2, err := CreateTask(database, "Fix API service", "/path/to/work/services/api-service", "main", "work")
	if err != nil {
		t.Fatalf("CreateTask 2 failed: %v", err)
	}

	// 2. List Tasks
	activeTasks, err := ListTasks(database, false)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(activeTasks) != 2 {
		t.Fatalf("expected 2 active tasks, got %d", len(activeTasks))
	}

	// 3. Get Active Task for Repo
	foundActive, err := GetActiveTaskForRepo(database, "/path/to/work/services/api-service")
	if err != nil {
		t.Fatalf("GetActiveTaskForRepo failed: %v", err)
	}
	if foundActive.ID != task2.ID {
		t.Errorf("expected task2 ID %s, got %s", task2.ID, foundActive.ID)
	}

	// 4. Mark Task Done
	if err := MarkTaskDone(database, task1.ID); err == nil {
		t.Fatalf("expected MarkTaskDone to fail without work product")
	}

	if err := AddWorkProduct(database, task1.ID, "pull_request", "https://github.com/org/repo/pull/1"); err != nil {
		t.Fatalf("failed to add work product: %v", err)
	}

	if err := MarkTaskDone(database, task1.ID); err != nil {
		t.Fatalf("MarkTaskDone failed: %v", err)
	}

	// Verify only 1 active task remains
	activeAfterDone, err := ListTasks(database, false)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(activeAfterDone) != 1 {
		t.Fatalf("expected 1 active task after done, got %d", len(activeAfterDone))
	}

	// Verify all tasks list still shows both
	allTasks, err := ListTasks(database, true)
	if err != nil {
		t.Fatalf("ListTasks(true) failed: %v", err)
	}
	if len(allTasks) != 2 {
		t.Fatalf("expected 2 total tasks, got %d", len(allTasks))
	}

	// 5. Delete Task
	if err := DeleteTask(database, task2.ID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	allAfterDelete, err := ListTasks(database, true)
	if err != nil {
		t.Fatalf("ListTasks after delete failed: %v", err)
	}
	if len(allAfterDelete) != 1 {
		t.Fatalf("expected 1 task after soft delete, got %d", len(allAfterDelete))
	}
}

func TestHandoffGeneration(t *testing.T) {
	database := setupTestDB(t)

	_, err := CreateTask(database, "Test Handoff Flow", ".", "main", "personal")
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	record, err := GenerateHandoff(HandoffOptions{
		TargetModel:       "gemini",
		ImmediateNextStep: "Verify handoff prompt generation",
		Directory:         ".",
		DB:                database,
	})
	if err != nil {
		t.Fatalf("GenerateHandoff failed: %v", err)
	}

	if record.TargetModel != "Gemini" {
		t.Errorf("expected TargetModel 'Gemini', got %s", record.TargetModel)
	}
	if record.SourceModel != "Claude" {
		t.Errorf("expected SourceModel 'Claude', got %s", record.SourceModel)
	}
	if record.ImmediateNextStep != "Verify handoff prompt generation" {
		t.Errorf("unexpected immediate next step: %s", record.ImmediateNextStep)
	}
	if len(record.HandoffPrompt) == 0 {
		t.Errorf("expected non-empty handoff prompt")
	}
}

func TestTaskBudget(t *testing.T) {
	database := setupTestDB(t)

	// 1. Create Task with Budget
	task, err := CreateTaskWithOptions(database, TaskCreateOptions{
		Name:         "Implement Auth Service",
		RepoPath:     "/path/to/repo",
		GitBranch:    "feat/auth",
		AccountRole:  "work",
		MaxBudgetUSD: 5.00,
		MaxTurns:     10,
	})
	if err != nil {
		t.Fatalf("CreateTaskWithOptions failed: %v", err)
	}

	if task.MaxBudgetUSD != 5.00 || task.MaxTurns != 10 {
		t.Fatalf("unexpected budget fields: $%.2f, %d turns", task.MaxBudgetUSD, task.MaxTurns)
	}

	// Initial evaluation: neither warning nor blocked
	eval := EvaluateTaskBudget(task)
	if eval.IsBlocked || eval.IsWarning {
		t.Errorf("expected no warning or block initially")
	}

	// 2. Record spend (within budget: $2.50, 5 turns)
	err = RecordTaskSpend(database, task.ID, 50000, 2.50, 5)
	if err != nil {
		t.Fatalf("RecordTaskSpend failed: %v", err)
	}

	task, _ = GetTask(database, task.ID)
	if task.SpentUSD != 2.50 || task.SpentTurns != 5 || task.SpentTokens != 50000 {
		t.Errorf("unexpected spend tracking: $%.2f, %d turns, %d tokens", task.SpentUSD, task.SpentTurns, task.SpentTokens)
	}

	eval = EvaluateTaskBudget(task)
	if eval.IsBlocked || eval.IsWarning {
		t.Errorf("expected no warning at 50%% spend")
	}

	// 3. Record spend to reach warning threshold (80%: $4.10 / 8 turns)
	_ = RecordTaskSpend(database, task.ID, 30000, 1.60, 3)
	task, _ = GetTask(database, task.ID)
	eval = EvaluateTaskBudget(task)
	if !eval.IsWarning {
		t.Errorf("expected warning at >=80%% spend, got false")
	}
	if eval.IsBlocked {
		t.Errorf("did not expect block at 82%% spend")
	}

	// 4. Record spend to exceed budget ($5.20 / 11 turns)
	_ = RecordTaskSpend(database, task.ID, 20000, 1.10, 3)
	task, _ = GetTask(database, task.ID)
	eval = EvaluateTaskBudget(task)
	if !eval.IsBlocked {
		t.Errorf("expected block at >=100%% spend")
	}

	// 5. Update Task Budget
	err = UpdateTaskBudget(database, task.ID, 10.00, 20)
	if err != nil {
		t.Fatalf("UpdateTaskBudget failed: %v", err)
	}
	task, _ = GetTask(database, task.ID)
	eval = EvaluateTaskBudget(task)
	if eval.IsBlocked {
		t.Errorf("expected unblocked after increasing budget to $10.00")
	}
}
