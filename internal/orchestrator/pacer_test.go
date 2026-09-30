package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"
)

func TestPacer_OptimalHeadroom(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(4 * time.Hour)

	store := quota.Store{DB: db}
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 25.0, ResetsAt: resetsAt},
		Weekly:    &quota.Window{Utilization: 10.0, ResetsAt: resetsAt.Add(7 * 24 * time.Hour)},
		FetchedAt: now,
	})

	cfg := DefaultPacerConfig()
	cfg.NowFunc = func() time.Time { return now }
	pacer := NewExecutionPacer(db, cfg, config.DefaultConfig())

	assessment, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityMedium)
	if err != nil {
		t.Fatalf("EvaluatePacing error: %v", err)
	}

	if assessment.State != PacingOptimal {
		t.Errorf("expected State = PacingOptimal, got %s", assessment.State)
	}
	if !assessment.CanExecute {
		t.Errorf("expected CanExecute = true")
	}
	if assessment.PacingDelay != 0 {
		t.Errorf("expected PacingDelay = 0 in optimal state, got %v", assessment.PacingDelay)
	}
	if assessment.FiveHourUsedPct != 25.0 {
		t.Errorf("expected 25.0%% used, got %f", assessment.FiveHourUsedPct)
	}
}

func TestPacer_CautionHeadroom(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(2 * time.Hour) // 120 minutes left

	store := quota.Store{DB: db}
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 78.0, ResetsAt: resetsAt},
		Weekly:    &quota.Window{Utilization: 40.0},
		FetchedAt: now,
	})

	cfg := DefaultPacerConfig()
	cfg.NowFunc = func() time.Time { return now }
	pacer := NewExecutionPacer(db, cfg, config.DefaultConfig())

	assessment, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityMedium)
	if err != nil {
		t.Fatalf("EvaluatePacing error: %v", err)
	}

	if assessment.State != PacingCaution {
		t.Errorf("expected State = PacingCaution, got %s", assessment.State)
	}
	if !assessment.CanExecute {
		t.Errorf("expected CanExecute = true in caution state")
	}
	// Pacing delay should be applied between Min (5s) and Max (60s)
	if assessment.PacingDelay < 5*time.Second || assessment.PacingDelay > 60*time.Second {
		t.Errorf("expected pacing delay in [5s, 60s], got %v", assessment.PacingDelay)
	}
}

func TestPacer_Throttled_PriorityGating(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(1 * time.Hour)

	store := quota.Store{DB: db}
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 90.0, ResetsAt: resetsAt},
		FetchedAt: now,
	})

	cfg := DefaultPacerConfig()
	cfg.NowFunc = func() time.Time { return now }
	pacer := NewExecutionPacer(db, cfg, config.DefaultConfig())

	// 1. Critical task: Must be allowed through despite 90% utilization
	critAssess, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityCritical)
	if err != nil {
		t.Fatalf("critical eval error: %v", err)
	}
	if critAssess.State != PacingThrottled {
		t.Errorf("expected PacingThrottled, got %s", critAssess.State)
	}
	if !critAssess.CanExecute {
		t.Errorf("expected critical task to execute under priority override")
	}

	// 2. Urgent task: Must be allowed through
	urgAssess, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityUrgent)
	if err != nil {
		t.Fatalf("urgent eval error: %v", err)
	}
	if !urgAssess.CanExecute {
		t.Errorf("expected urgent task to execute under priority override")
	}

	// 3. Medium task: Must be deferred to protect 5h quota
	medAssess, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityMedium)
	if err != nil {
		t.Fatalf("medium eval error: %v", err)
	}
	if medAssess.CanExecute {
		t.Errorf("expected medium task to be deferred when 5h quota is throttled at 90%%")
	}

	// 4. Low task: Must be deferred
	lowAssess, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityLow)
	if err != nil {
		t.Fatalf("low eval error: %v", err)
	}
	if lowAssess.CanExecute {
		t.Errorf("expected low task to be deferred when 5h quota is throttled at 90%%")
	}
}

func TestPacer_ExhaustedAndFailover(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := now.Add(45 * time.Minute)

	store := quota.Store{DB: db}
	// Claude is exhausted (98% used)
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 98.0, ResetsAt: resetsAt},
		FetchedAt: now,
	})

	// Gemini has ample headroom (15% used)
	_ = store.Save(&quota.Snapshot{
		Provider:  "gemini",
		FiveHour:  &quota.Window{Utilization: 15.0, ResetsAt: now.Add(3 * time.Hour)},
		FetchedAt: now,
	})

	cfg := DefaultPacerConfig()
	cfg.NowFunc = func() time.Time { return now }
	pacer := NewExecutionPacer(db, cfg, config.DefaultConfig())

	assessment, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityCritical)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}

	if assessment.State != PacingExhausted {
		t.Errorf("expected State = PacingExhausted, got %s", assessment.State)
	}
	if assessment.CanExecute {
		t.Errorf("expected CanExecute = false on exhausted provider")
	}
	if assessment.SuggestedFailover != "gemini" {
		t.Errorf("expected suggested failover = gemini, got %q", assessment.SuggestedFailover)
	}
}

func TestPacer_AutoResetAfterTimestamp(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	snapshotTime := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	resetsAt := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	store := quota.Store{DB: db}
	_ = store.Save(&quota.Snapshot{
		Provider:  "claude",
		FiveHour:  &quota.Window{Utilization: 99.0, ResetsAt: resetsAt},
		FetchedAt: snapshotTime,
	})

	// Simulate clock advancing past resetsAt to 12:05 PM
	now := time.Date(2026, 9, 30, 12, 5, 0, 0, time.UTC)

	cfg := DefaultPacerConfig()
	cfg.NowFunc = func() time.Time { return now }
	pacer := NewExecutionPacer(db, cfg, config.DefaultConfig())

	assessment, err := pacer.EvaluatePacing(context.Background(), "claude", PriorityMedium)
	if err != nil {
		t.Fatalf("eval error: %v", err)
	}

	// Should auto-reset to optimal
	if assessment.State != PacingOptimal {
		t.Errorf("expected State = PacingOptimal after reset time passed, got %s (used: %.1f%%)",
			assessment.State, assessment.FiveHourUsedPct)
	}
	if !assessment.CanExecute {
		t.Errorf("expected CanExecute = true after reset")
	}
}
