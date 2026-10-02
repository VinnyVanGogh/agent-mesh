package orchestrator_test

import (
	"path/filepath"
	"sync"
	"testing"

	ctx "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

// TestStepRecorder_PersistRoundTrip verifies that StepRecorder.persist writes rows
// that ListRunStepsByTask can read back, covering the schema contract (TEXT id,
// status column) that previously caused every insert to silently fail.
func TestStepRecorder_PersistRoundTrip(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}

	var mu sync.Mutex
	var published []string
	pub := func(eventType string, _ any) {
		mu.Lock()
		published = append(published, eventType)
		mu.Unlock()
	}

	// Seed a task so the FK on run_steps.task_id is satisfied.
	task, err := ctx.CreateTask(store.DB(), "test-task", "/repo", "main", "coder")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := orchestrator.NewStepRecorder(store.DB(), pub, "run-abc", task.ID)

	// Emit a Bash tool step; tool_use+tool_result triggers persist().
	r.Feed(orchestrator.StepDelta{Kind: orchestrator.StepDeltaToolUse, ToolName: "Bash", ToolID: "tool-1"})
	r.Feed(orchestrator.StepDelta{Kind: orchestrator.StepDeltaToolResult, ToolID: "tool-1"})
	r.Close()

	steps, err := ctx.ListRunStepsByTask(store.DB(), task.ID)
	if err != nil {
		t.Fatalf("ListRunStepsByTask: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected at least one run_step, got none — insert likely failed (schema mismatch?)")
	}
	s := steps[0]
	if s.ID == "" {
		t.Error("step ID must be a non-empty TEXT uuid")
	}
	if s.RunID != "run-abc" {
		t.Errorf("RunID = %q, want run-abc", s.RunID)
	}
	if s.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", s.TaskID, task.ID)
	}
	if s.Status == "" {
		t.Error("Status must be non-empty (done or error)")
	}
}
