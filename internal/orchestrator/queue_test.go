package orchestrator

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	schema := `
	CREATE TABLE tasks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		repo_path TEXT NOT NULL DEFAULT '',
		git_branch TEXT NOT NULL DEFAULT 'main',
		status TEXT NOT NULL DEFAULT 'active',
		account_role TEXT NOT NULL DEFAULT 'work',
		priority TEXT NOT NULL DEFAULT 'medium',
		max_budget_usd REAL NOT NULL DEFAULT 0.0,
		max_turns INTEGER NOT NULL DEFAULT 0,
		spent_tokens INTEGER NOT NULL DEFAULT 0,
		spent_usd REAL NOT NULL DEFAULT 0.0,
		spent_turns INTEGER NOT NULL DEFAULT 0,
		organization TEXT,
		project TEXT,
		parent_id TEXT,
		execution_stage TEXT NOT NULL DEFAULT 'todo',
		checkout_run_id TEXT,
		checkout_agent_id TEXT,
		is_blocked INTEGER NOT NULL DEFAULT 0,
		block_reason TEXT,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	);

	CREATE TABLE task_relations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL REFERENCES tasks(id),
		blocks_id TEXT NOT NULL REFERENCES tasks(id),
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
		UNIQUE(task_id, blocks_id)
	);

	CREATE TABLE activity_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		details TEXT,
		created_at TEXT NOT NULL
	);

	CREATE TABLE agent_circuit_breakers (
		session_id TEXT PRIMARY KEY,
		repo_path TEXT NOT NULL,
		agent_type TEXT NOT NULL,
		is_tripped INTEGER NOT NULL DEFAULT 0,
		trip_count INTEGER NOT NULL DEFAULT 0,
		failure_signature TEXT,
		failing_tool TEXT,
		failing_command TEXT,
		last_error TEXT,
		tripped_at TEXT,
		cleared_at TEXT
	);

	CREATE TABLE quota_windows (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		pool_key TEXT NOT NULL,
		window_type TEXT NOT NULL,
		used_percent REAL NOT NULL DEFAULT 0.0,
		remaining_pct REAL NOT NULL DEFAULT 100.0,
		is_locked INTEGER NOT NULL DEFAULT 0,
		resets_at TEXT,
		updated_at TEXT NOT NULL,
		UNIQUE(pool_key, window_type)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("exec schema: %v", err)
	}

	return db
}

func TestPriorityNormalizationAndWeights(t *testing.T) {
	tests := []struct {
		input       string
		expected    string
		expectWeight int
	}{
		{"CRITICAL", PriorityCritical, 400},
		{"critical", PriorityCritical, 400},
		{"urgent", PriorityUrgent, 300},
		{"high", PriorityHigh, 200},
		{"HIGH", PriorityHigh, 200},
		{"medium", PriorityMedium, 100},
		{"normal", PriorityMedium, 100},
		{"standard", PriorityMedium, 100},
		{"low", PriorityLow, 0},
		{"LOW", PriorityLow, 0},
		{"unknown", PriorityMedium, 100},
		{"", PriorityMedium, 100},
	}

	for _, tc := range tests {
		got := NormalizePriority(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizePriority(%q) = %q; want %q", tc.input, got, tc.expected)
		}
		weight := PriorityWeight(tc.input)
		if weight != tc.expectWeight {
			t.Errorf("PriorityWeight(%q) = %d; want %d", tc.input, weight, tc.expectWeight)
		}
	}
}

func TestTaskPriorityQueue_Heap(t *testing.T) {
	now := time.Now()
	tasks := []*QueuedTask{
		{ID: "t-low", Priority: PriorityLow, PriorityWeight: 0, CreatedAt: now.Add(1 * time.Minute)},
		{ID: "t-med", Priority: PriorityMedium, PriorityWeight: 100, CreatedAt: now.Add(2 * time.Minute)},
		{ID: "t-crit", Priority: PriorityCritical, PriorityWeight: 400, CreatedAt: now.Add(3 * time.Minute)},
		{ID: "t-high", Priority: PriorityHigh, PriorityWeight: 200, CreatedAt: now.Add(4 * time.Minute)},
		{ID: "t-urgent", Priority: PriorityUrgent, PriorityWeight: 300, CreatedAt: now.Add(5 * time.Minute)},
	}

	pq := BuildPriorityQueue(tasks)
	if pq.Len() != 5 {
		t.Fatalf("expected len 5, got %d", pq.Len())
	}

	// First popped must be Critical
	first := pq.PopTask()
	if first.ID != "t-crit" {
		t.Errorf("expected t-crit, got %s", first.ID)
	}

	// Second popped must be Urgent
	second := pq.PopTask()
	if second.ID != "t-urgent" {
		t.Errorf("expected t-urgent, got %s", second.ID)
	}

	// Third popped must be High
	third := pq.PopTask()
	if third.ID != "t-high" {
		t.Errorf("expected t-high, got %s", third.ID)
	}

	// Fourth must be Medium
	fourth := pq.PopTask()
	if fourth.ID != "t-med" {
		t.Errorf("expected t-med, got %s", fourth.ID)
	}

	// Last must be Low
	fifth := pq.PopTask()
	if fifth.ID != "t-low" {
		t.Errorf("expected t-low, got %s", fifth.ID)
	}
}

func TestQueueManager_FetchRunnableTasks_PriorityOrderAndBlockers(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert tasks with various priorities and timestamps
	insertTask := func(id, name, priority, stage string, blocked int, offsetSeconds int) {
		ts := now.Add(time.Duration(offsetSeconds) * time.Second).Format(time.RFC3339Nano)
		_, err := db.Exec(`
			INSERT INTO tasks (id, name, priority, execution_stage, is_blocked, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, id, name, priority, stage, blocked, ts, ts)
		if err != nil {
			t.Fatalf("insert task %s: %v", id, err)
		}
	}

	insertTask("task-low", "Low deliverable", "low", "todo", 0, 1)
	insertTask("task-med-1", "Medium deliverable 1", "medium", "todo", 0, 2)
	insertTask("task-med-2", "Medium deliverable 2", "medium", "todo", 0, 3)
	insertTask("task-high", "High deliverable", "high", "todo", 0, 4)
	insertTask("task-crit", "Critical deliverable", "critical", "todo", 0, 5)
	insertTask("task-blocked", "Blocked critical", "critical", "todo", 1, 6)
	insertTask("task-in-prog", "Already in progress", "critical", "in_progress", 0, 7)

	// Block task-high with a dependency on task-dep
	insertTask("task-dep", "Dependency for high", "low", "todo", 0, 8)
	_, err := db.Exec(`INSERT INTO task_relations (task_id, blocks_id) VALUES ('task-dep', 'task-high')`)
	if err != nil {
		t.Fatalf("insert task relation: %v", err)
	}

	qm := NewQueueManager(db)
	tasks, err := qm.FetchRunnableTasks(ctx)
	if err != nil {
		t.Fatalf("fetch runnable: %v", err)
	}

	// Expected order:
	// 1. task-crit (critical, unblocked)
	// 2. task-med-1 (medium, earlier timestamp)
	// 3. task-med-2 (medium, later timestamp)
	// 4. task-low (low, earlier timestamp)
	// 5. task-dep (low, later timestamp)
	// Note: task-high is BLOCKED by task-dep, task-blocked has is_blocked=1, task-in-prog is in_progress
	if len(tasks) != 5 {
		t.Fatalf("expected 5 runnable tasks, got %d", len(tasks))
	}

	expectedOrder := []string{"task-crit", "task-med-1", "task-med-2", "task-low", "task-dep"}
	for i, exp := range expectedOrder {
		if tasks[i].ID != exp {
			t.Errorf("at position %d: expected task %s, got %s (priority: %s)", i, exp, tasks[i].ID, tasks[i].Priority)
		}
	}

	// Now complete task-dep and verify task-high unblocks into 2nd position (above mediums)
	_, err = db.Exec(`UPDATE tasks SET status = 'done', execution_stage = 'done' WHERE id = 'task-dep'`)
	if err != nil {
		t.Fatalf("mark task-dep done: %v", err)
	}

	tasksAfterUnblock, err := qm.FetchRunnableTasks(ctx)
	if err != nil {
		t.Fatalf("fetch runnable after unblock: %v", err)
	}

	// Now task-high is runnable, so it should rank behind task-crit but ahead of all mediums
	if len(tasksAfterUnblock) != 5 {
		t.Fatalf("expected 5 tasks after unblock, got %d", len(tasksAfterUnblock))
	}
	if tasksAfterUnblock[0].ID != "task-crit" {
		t.Errorf("expected task-crit first, got %s", tasksAfterUnblock[0].ID)
	}
	if tasksAfterUnblock[1].ID != "task-high" {
		t.Errorf("expected task-high second, got %s", tasksAfterUnblock[1].ID)
	}

	// Verify QueueSummary
	summary, err := qm.GetQueueSummary(ctx)
	if err != nil {
		t.Fatalf("summary error: %v", err)
	}
	if summary.TotalPending != 5 {
		t.Errorf("summary total pending = %d; want 5", summary.TotalPending)
	}
	if summary.NextTask == nil || summary.NextTask.ID != "task-crit" {
		t.Errorf("summary next task = %v; want task-crit", summary.NextTask)
	}
}
