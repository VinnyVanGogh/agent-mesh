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

func TestBlockerGraphAndRationales(t *testing.T) {
	database := setupTestDB(t)

	// Create 3 tasks: Task 1 (DB migration), Task 2 (Auth API), Task 3 (Frontend UI)
	t1, err := CreateTask(database, "Database Migration", ".", "feature/db", "work")
	if err != nil {
		t.Fatalf("failed to create task 1: %v", err)
	}
	t2, err := CreateTask(database, "Auth API Endpoints", ".", "feature/auth", "work")
	if err != nil {
		t.Fatalf("failed to create task 2: %v", err)
	}
	t3, err := CreateTask(database, "Frontend Dashboard", ".", "feature/ui", "work")
	if err != nil {
		t.Fatalf("failed to create task 3: %v", err)
	}

	// Block Task 3 on Task 1 and Task 2 with explicit rationales
	blockers := []BlockerInput{
		{ID: t1.ID, Rationale: "Requires database schema migration to complete"},
		{ID: t2.ID, Rationale: "Requires OAuth JWT verification endpoints"},
	}
	if err := BlockTaskWithBlockers(database, t3.ID, "Waiting on backend services", blockers); err != nil {
		t.Fatalf("BlockTaskWithBlockers failed: %v", err)
	}

	// Verify Task 3 is blocked and has BlockedBy relations with rationales
	task3, err := GetTask(database, t3.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if !task3.IsBlocked {
		t.Fatalf("expected task 3 to be blocked")
	}
	if task3.ExecutionStage != "blocked" {
		t.Errorf("expected execution stage 'blocked', got %s", task3.ExecutionStage)
	}
	if len(task3.BlockedBy) != 2 {
		t.Fatalf("expected 2 BlockedBy relations, got %d", len(task3.BlockedBy))
	}
	if task3.BlockedBy[0].Rationale == "" || task3.BlockedBy[1].Rationale == "" {
		t.Errorf("expected non-empty rationales for blockers, got: %+v", task3.BlockedBy)
	}

	// Verify Task 1 has Task 3 in Blocks
	task1, err := GetTask(database, t1.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if len(task1.Blocks) != 1 || task1.Blocks[0].ID != t3.ID {
		t.Fatalf("expected task 1 to block task 3, got: %+v", task1.Blocks)
	}

	// Test GetTaskDependencyGraph
	graph, err := GetTaskDependencyGraph(database, t3.ID)
	if err != nil {
		t.Fatalf("GetTaskDependencyGraph failed: %v", err)
	}
	if len(graph.BlockedBy) != 2 {
		t.Errorf("expected graph.BlockedBy to have 2 items, got %d", len(graph.BlockedBy))
	}
	if graph.UpstreamTree == nil || len(graph.UpstreamTree.Children) != 2 {
		t.Errorf("expected upstream tree with 2 children")
	}

	// Test Unblocking from one blocker (Task 1)
	if err := RemoveTaskBlocker(database, t3.ID, t1.ID); err != nil {
		t.Fatalf("RemoveTaskBlocker failed: %v", err)
	}
	task3After1, err := GetTask(database, t3.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	// Task 3 should still be blocked because Task 2 is still blocking it
	if !task3After1.IsBlocked {
		t.Errorf("expected task 3 to still be blocked by task 2")
	}
	if len(task3After1.BlockedBy) != 1 {
		t.Fatalf("expected 1 remaining blocker, got %d", len(task3After1.BlockedBy))
	}
	if task3After1.BlockedBy[0].ID != t2.ID {
		t.Errorf("expected remaining blocker to be task 2, got %s", task3After1.BlockedBy[0].ID)
	}

	// Now complete Task 2 (MarkTaskDone) -> should automatically unblock Task 3
	if err := AddWorkProduct(database, t2.ID, "commit", "sha256:abc123"); err != nil {
		t.Fatalf("AddWorkProduct failed: %v", err)
	}
	if err := MarkTaskDone(database, t2.ID); err != nil {
		t.Fatalf("MarkTaskDone failed: %v", err)
	}
	task3Unblocked, err := GetTask(database, t3.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task3Unblocked.IsBlocked {
		t.Errorf("expected task 3 to be unblocked after task 2 completed")
	}
	if len(task3Unblocked.BlockedBy) != 0 {
		t.Errorf("expected 0 remaining blockers, got %d", len(task3Unblocked.BlockedBy))
	}
	if task3Unblocked.ExecutionStage != "todo" {
		t.Errorf("expected execution stage restored to 'todo', got %s", task3Unblocked.ExecutionStage)
	}
}

