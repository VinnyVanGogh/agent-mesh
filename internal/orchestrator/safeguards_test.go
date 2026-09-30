package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

func TestSafeguards_TaskBudgetExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sm := NewSafeguardsManager(db, config.DefaultConfig())

	task := &QueuedTask{
		ID:           "task-budget-capped",
		MaxBudgetUSD: 10.00,
		SpentUSD:     10.50,
	}

	status, err := sm.CheckSafeguards(context.Background(), task, "claude")
	if err != nil {
		t.Fatalf("check error: %v", err)
	}

	if status.Allowed {
		t.Errorf("expected Allowed = false for exceeded budget")
	}
	if status.TripType != "budget" {
		t.Errorf("expected TripType = budget, got %s", status.TripType)
	}
	if !status.ShouldCap {
		t.Errorf("expected ShouldCap = true")
	}
}

func TestSafeguards_TaskTurnsExceeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	sm := NewSafeguardsManager(db, config.DefaultConfig())

	task := &QueuedTask{
		ID:         "task-turns-capped",
		MaxTurns:   25,
		SpentTurns: 25,
	}

	status, err := sm.CheckSafeguards(context.Background(), task, "claude")
	if err != nil {
		t.Fatalf("check error: %v", err)
	}

	if status.Allowed {
		t.Errorf("expected Allowed = false for exceeded turns")
	}
	if status.TripType != "turn_limit" {
		t.Errorf("expected TripType = turn_limit, got %s", status.TripType)
	}
	if !status.ShouldCap {
		t.Errorf("expected ShouldCap = true")
	}
}

func TestSafeguards_ProviderCeilings(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	appCfg := config.DefaultConfig()
	appCfg.ProviderCeilings = map[string]config.ProviderCeilingConfig{
		"claude": {
			MaxSpendPerHour: 15.0, // $15/hr cap
			MaxTurnsPerHour: 50,   // 50 turns/hr cap
		},
	}

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	sm := NewSafeguardsManager(db, appCfg)
	sm.NowFunc = func() time.Time { return now }

	task := &QueuedTask{ID: "task-safe", MaxBudgetUSD: 100.0, MaxTurns: 100}

	// 1. Initial check: allowed
	st1, err := sm.CheckSafeguards(context.Background(), task, "claude")
	if err != nil || !st1.Allowed {
		t.Fatalf("expected allowed initially, got %v (err: %v)", st1, err)
	}

	// 2. Consume $16 in recent runs
	sm.RecordRun("claude", 10, 8.0)
	sm.RecordRun("claude", 10, 8.50) // Total: $16.50 > $15.00 ceiling

	st2, err := sm.CheckSafeguards(context.Background(), task, "claude")
	if err != nil {
		t.Fatalf("check error: %v", err)
	}
	if st2.Allowed {
		t.Errorf("expected provider ceiling trip when spend exceeded $15/hr")
	}
	if st2.TripType != "provider_ceiling" {
		t.Errorf("expected TripType = provider_ceiling, got %s", st2.TripType)
	}

	// Task should not be permanently capped because it's a provider-wide rate throttle
	if st2.ShouldCap {
		t.Errorf("expected ShouldCap = false for provider ceiling throttle")
	}
}

func TestSafeguards_CircuitBreakerLoopDetection(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert tripped circuit breaker for task-loop
	_, err := db.Exec(`
		INSERT INTO agent_circuit_breakers (session_id, repo_path, agent_type, is_tripped, failing_tool, last_error)
		VALUES ('task-loop', '/path', 'claude', 1, 'git_commit', 'repeating merge conflict')
	`)
	if err != nil {
		t.Fatalf("insert circuit breaker: %v", err)
	}

	sm := NewSafeguardsManager(db, config.DefaultConfig())
	task := &QueuedTask{ID: "task-loop"}

	status, err := sm.CheckSafeguards(context.Background(), task, "claude")
	if err != nil {
		t.Fatalf("check error: %v", err)
	}

	if status.Allowed {
		t.Errorf("expected circuit breaker trip to block execution")
	}
	if status.TripType != "circuit_breaker" {
		t.Errorf("expected TripType = circuit_breaker, got %s", status.TripType)
	}
	if !status.ShouldCap {
		t.Errorf("expected ShouldCap = true to halt runaway loop")
	}

	// Test EnforceSafeguardCap
	err = sm.EnforceSafeguardCap(context.Background(), task.ID, status.Reason)
	if err != nil {
		t.Fatalf("enforce cap error: %v", err)
	}

	// Verify database record
	var stage string
	var blocked int
	var reason string
	err = db.QueryRow(`SELECT execution_stage, is_blocked, block_reason FROM tasks WHERE id = ?`, task.ID).Scan(&stage, &blocked, &reason)
	// Task row might not exist in tasks table yet; insert first and re-verify
	_, _ = db.Exec(`INSERT INTO tasks (id, name, execution_stage) VALUES ('task-loop', 'Loop task', 'todo')`)
	_ = sm.EnforceSafeguardCap(context.Background(), task.ID, status.Reason)

	err = db.QueryRow(`SELECT execution_stage, is_blocked, block_reason FROM tasks WHERE id = ?`, task.ID).Scan(&stage, &blocked, &reason)
	if err != nil {
		t.Fatalf("query capped task: %v", err)
	}
	if stage != "capped" {
		t.Errorf("expected execution_stage = capped, got %s", stage)
	}
	if blocked != 1 {
		t.Errorf("expected is_blocked = 1")
	}
}
