package context

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"github.com/vincevasile/agent-mesh/internal/db"
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
	task1, err := CreateTask(database, "Implement bridge package", "/Users/vincevasile/Documents/dev/agent-mesh", "feature/bridge", "personal")
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

	task2, err := CreateTask(database, "Fix partner center API", "/Users/vincevasile/Documents/dev/mansol/python_projects/partner-center-api", "main", "work")
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
	foundActive, err := GetActiveTaskForRepo(database, "/Users/vincevasile/Documents/dev/mansol/python_projects/partner-center-api")
	if err != nil {
		t.Fatalf("GetActiveTaskForRepo failed: %v", err)
	}
	if foundActive.ID != task2.ID {
		t.Errorf("expected task2 ID %s, got %s", task2.ID, foundActive.ID)
	}

	// 4. Mark Task Done
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

	_, err := CreateTask(database, "Test Handoff Flow", "/Users/vincevasile/Documents/dev/agent-mesh", "main", "personal")
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
