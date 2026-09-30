package orchestrator

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"
)

func TestQueueRunner_SequentialPriorityOrder(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now().UTC()
	insertTask := func(id, name, priority string, offset int) {
		ts := now.Add(time.Duration(offset) * time.Second).Format(time.RFC3339Nano)
		_, err := db.Exec(`
			INSERT INTO tasks (id, name, priority, execution_stage, created_at, updated_at)
			VALUES (?, ?, ?, 'todo', ?, ?)
		`, id, name, priority, ts, ts)
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}

	insertTask("task-med", "Medium feature", PriorityMedium, 1)
	insertTask("task-crit", "Critical bug fix", PriorityCritical, 2)
	insertTask("task-high", "High deliverable", PriorityHigh, 3)

	runner := NewQueueRunner(db, t.TempDir(), config.DefaultConfig())

	// Mock WorktreeManager and Interceptor to avoid disk git dependencies in unit test
	mockWM := &mockWorktreeManager{dir: t.TempDir()}
	runner.Harness.WM = mockWM

	var executionOrder []string
	adapterRunner := func(ctx context.Context, cwd, prov string, rawArgs, extraEnv []string, stdout, stderr io.Writer) error {
		_, _ = stdout.Write([]byte("[[TASK_COMPLETE]]\n"))
		return nil
	}

	opts := RunQueueOptions{
		RunConfig: RunConfig{
			RunAdapter: adapterRunner,
			AgentID:    "test-runner",
		},
		OnTaskStart: func(task *QueuedTask, assessment *PacingAssessment) {
			executionOrder = append(executionOrder, task.ID)
		},
	}

	results, err := runner.RunQueue(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunQueue error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Order MUST be: task-crit -> task-high -> task-med
	expected := []string{"task-crit", "task-high", "task-med"}
	for i, exp := range expected {
		if executionOrder[i] != exp {
			t.Errorf("at step %d: expected %s, executed %s", i, exp, executionOrder[i])
		}
	}
}

func TestQueueRunner_PacingDelayApplied(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(2 * time.Hour)

	// Save quota snapshot in caution band (80% used)
	store := quota.Store{DB: db}
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 80.0, ResetsAt: resetsAt},
		FetchedAt: now,
	})

	_, _ = db.Exec(`INSERT INTO tasks (id, name, priority, execution_stage) VALUES ('task-pacing', 'Paced task', 'medium', 'todo')`)

	runner := NewQueueRunner(db, t.TempDir(), config.DefaultConfig())
	runner.Harness.WM = &mockWorktreeManager{dir: t.TempDir()}
	runner.Pacer.Config.NowFunc = func() time.Time { return now }

	var sleptDuration time.Duration
	runner.SleepFunc = func(d time.Duration) {
		sleptDuration += d
	}

	opts := RunQueueOptions{
		Provider: "claude",
		DryRun:   true,
	}

	res, err := runner.RunNext(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunNext error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}

	if sleptDuration < 5*time.Second {
		t.Errorf("expected pacing delay to be applied (>=5s), got %v", sleptDuration)
	}
}

func TestQueueRunner_SafeguardCappingPreventsExecution(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert task that has exceeded budget
	_, _ = db.Exec(`
		INSERT INTO tasks (id, name, priority, max_budget_usd, spent_usd, execution_stage)
		VALUES ('task-over-budget', 'Budget blown', 'critical', 10.0, 15.0, 'todo')
	`)
	_, _ = db.Exec(`
		INSERT INTO tasks (id, name, priority, execution_stage)
		VALUES ('task-valid', 'Valid task', 'medium', 'todo')
	`)

	runner := NewQueueRunner(db, t.TempDir(), config.DefaultConfig())
	runner.Harness.WM = &mockWorktreeManager{dir: t.TempDir()}

	opts := RunQueueOptions{
		DryRun: true,
	}

	// First RunNext: task-over-budget should be capped
	res1, err := runner.RunNext(context.Background(), opts)
	if err != nil {
		t.Fatalf("run 1 error: %v", err)
	}
	if res1.TaskID != "task-over-budget" {
		t.Errorf("expected task-over-budget, got %s", res1.TaskID)
	}
	if res1.Disposition != "capped" {
		t.Errorf("expected disposition capped, got %s", res1.Disposition)
	}

	// Verify task in DB is capped
	var stage string
	_ = db.QueryRow(`SELECT execution_stage FROM tasks WHERE id = 'task-over-budget'`).Scan(&stage)
	if stage != "capped" {
		t.Errorf("expected DB execution_stage = capped, got %s", stage)
	}

	// Second RunNext: next valid task runs
	res2, err := runner.RunNext(context.Background(), opts)
	if err != nil {
		t.Fatalf("run 2 error: %v", err)
	}
	if res2.TaskID != "task-valid" {
		t.Errorf("expected task-valid, got %s", res2.TaskID)
	}
}

type mockWorktreeManager struct {
	dir string
}

func (m *mockWorktreeManager) CreateContext(ctx context.Context, taskID, sessionID string) (string, error) {
	return m.dir, nil
}

func (m *mockWorktreeManager) PruneContext(ctx context.Context, taskID string) error {
	return nil
}
