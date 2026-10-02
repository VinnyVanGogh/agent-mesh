package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

// openTestStore opens a real SQLite store in a temp dir with the full schema.
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// stubWM implements WorktreeManagerIface using a pre-existing directory so
// tests do not require a real git repository.
type stubWM struct{ dir string }

func (s *stubWM) CreateContext(_ context.Context, _, _ string) (string, error) {
	return s.dir, nil
}
func (s *stubWM) PruneContext(_ context.Context, _ string) error { return nil }

func TestWireOnWake_SetsOnWake(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()
	if orchestrator.GlobalDispatcher.OnWake != nil {
		t.Fatal("pre-condition: OnWake should be nil before wiring")
	}
	wireOnWake(openTestStore(t), t.TempDir(), nil, nil)
	if orchestrator.GlobalDispatcher.OnWake == nil {
		t.Fatal("wireOnWake did not set GlobalDispatcher.OnWake")
	}
}

func TestWireOnWake_AdapterCalledOnWake(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()

	store := openTestStore(t)
	dir := t.TempDir()

	taskID := "wire-wake-test-abc1"
	if _, err := store.DB().Exec(
		`INSERT INTO tasks (id, name, repo_path, execution_stage, assignee_agent_id) VALUES (?, 'test task', '', 'todo', 'agent-test')`,
		taskID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	var adapterCalls atomic.Int32
	stub := func(_ context.Context, _ string, _ string, _ []string, _ []string, stdout, _ io.Writer) error {
		adapterCalls.Add(1)
		// Signal completion so the harness exits after one turn.
		fmt.Fprintln(stdout, "[[TASK_COMPLETE]]")
		return nil
	}

	wireOnWake(store, dir, nil, stub, &stubWM{dir: dir})

	orchestrator.GlobalDispatcher.Wake(taskID, "test_wake", "test:"+taskID)

	// Drain waits for the goroutine spawned by Wake to return.
	drainDone := make(chan struct{})
	go func() { orchestrator.GlobalDispatcher.Drain(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(15 * time.Second):
		t.Fatal("dispatcher did not drain within timeout")
	}

	if adapterCalls.Load() == 0 {
		t.Fatal("stub adapter was never called; harness did not run")
	}

	var stage string
	if err := store.DB().QueryRow(
		"SELECT execution_stage FROM tasks WHERE id=?", taskID,
	).Scan(&stage); err != nil {
		t.Fatalf("query task stage: %v", err)
	}
	if stage == "todo" {
		t.Errorf("task execution_stage still 'todo' after wake; harness did not claim it")
	}
}

func TestWireOnWake_MissingTaskSkipsRun(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()
	store := openTestStore(t)
	dir := t.TempDir()

	var adapterCalls atomic.Int32
	stub := func(_ context.Context, _ string, _ string, _ []string, _ []string, _ io.Writer, _ io.Writer) error {
		adapterCalls.Add(1)
		return nil
	}

	wireOnWake(store, dir, nil, stub, &stubWM{dir: dir})

	// Wake a task that does not exist in the DB.
	orchestrator.GlobalDispatcher.Wake("nonexistent-task-id", "test", "test:nonexistent")

	drainDone := make(chan struct{})
	go func() { orchestrator.GlobalDispatcher.Drain(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not drain")
	}

	if adapterCalls.Load() != 0 {
		t.Error("adapter should not be called for a missing task")
	}
}
