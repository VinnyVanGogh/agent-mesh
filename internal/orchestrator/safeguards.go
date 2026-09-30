package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

// SafeguardStatus captures the outcome of anti-overuse verification.
type SafeguardStatus struct {
	Allowed   bool   `json:"allowed"`
	Violated  bool   `json:"violated"`
	TripType  string `json:"trip_type,omitempty"` // "budget", "turn_limit", "provider_ceiling", "circuit_breaker", "runaway_loop"
	Reason    string `json:"reason"`
	ShouldCap bool   `json:"should_cap"`
}

// HourlyUsage tracks aggregate spend and turns across all tasks in a sliding 1-hour window.
type HourlyUsage struct {
	SpendUSD float64
	Turns    int
	Window   time.Time
}

// SafeguardsManager enforces per-task limits, provider ceilings, and runaway loop protections.
type SafeguardsManager struct {
	mu          sync.Mutex
	DB          *sql.DB
	appCfg      *config.Config
	hourlySpend map[string][]usageEntry // provider -> recent usage records
	NowFunc     func() time.Time
}

type usageEntry struct {
	timestamp time.Time
	spendUSD  float64
	turns     int
}

// NewSafeguardsManager creates an anti-overuse safeguard enforcement manager.
func NewSafeguardsManager(dbConn *sql.DB, appCfg *config.Config) *SafeguardsManager {
	return &SafeguardsManager{
		DB:          dbConn,
		appCfg:      appCfg,
		hourlySpend: make(map[string][]usageEntry),
		NowFunc:     time.Now,
	}
}

func (s *SafeguardsManager) now() time.Time {
	if s.NowFunc != nil {
		return s.NowFunc()
	}
	return time.Now()
}

// RecordRun records usage from an executed run to enforce hourly rate and spend ceilings.
func (s *SafeguardsManager) RecordRun(provider string, turns int, spendUSD float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prov := strings.ToLower(provider)
	now := s.now()
	s.hourlySpend[prov] = append(s.hourlySpend[prov], usageEntry{
		timestamp: now,
		spendUSD:  spendUSD,
		turns:     turns,
	})
	s.pruneOldUsageLocked(prov, now)
}

func (s *SafeguardsManager) pruneOldUsageLocked(provider string, now time.Time) {
	cutoff := now.Add(-1 * time.Hour)
	entries := s.hourlySpend[provider]
	var active []usageEntry
	for _, e := range entries {
		if e.timestamp.After(cutoff) {
			active = append(active, e)
		}
	}
	s.hourlySpend[provider] = active
}

// GetHourlyStats returns total spend and turns consumed on a provider in the last hour.
func (s *SafeguardsManager) GetHourlyStats(provider string) (totalSpend float64, totalTurns int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prov := strings.ToLower(provider)
	s.pruneOldUsageLocked(prov, s.now())
	for _, e := range s.hourlySpend[prov] {
		totalSpend += e.spendUSD
		totalTurns += e.turns
	}
	return totalSpend, totalTurns
}

// CheckSafeguards evaluates whether a task can be safely dispatched without violating anti-overuse rules.
func (s *SafeguardsManager) CheckSafeguards(ctx context.Context, task *QueuedTask, provider string) (*SafeguardStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prov := strings.ToLower(provider)
	now := s.now()

	// 1. Task-Level Hard Limits: MaxBudgetUSD
	if task.MaxBudgetUSD > 0 && task.SpentUSD >= task.MaxBudgetUSD {
		return &SafeguardStatus{
			Allowed:   false,
			Violated:  true,
			TripType:  "budget",
			Reason:    fmt.Sprintf("task spend budget exceeded ($%.2f spent >= $%.2f limit)", task.SpentUSD, task.MaxBudgetUSD),
			ShouldCap: true,
		}, nil
	}

	// 2. Task-Level Hard Limits: MaxTurns
	if task.MaxTurns > 0 && task.SpentTurns >= task.MaxTurns {
		return &SafeguardStatus{
			Allowed:   false,
			Violated:  true,
			TripType:  "turn_limit",
			Reason:    fmt.Sprintf("task turn limit exceeded (%d turns spent >= %d limit)", task.SpentTurns, task.MaxTurns),
			ShouldCap: true,
		}, nil
	}

	// 3. Provider Ceilings from Configuration
	if s.appCfg != nil && len(s.appCfg.ProviderCeilings) > 0 {
		ceiling, hasCeiling := s.appCfg.ProviderCeilings[prov]
		if !hasCeiling {
			// Check standard alias
			if prov == "claude-work" || prov == "claude-personal" || prov == "3p-claude" {
				ceiling, hasCeiling = s.appCfg.ProviderCeilings["claude"]
			} else if prov == "gemini-native" {
				ceiling, hasCeiling = s.appCfg.ProviderCeilings["gemini"]
			}
		}

		if hasCeiling {
			s.pruneOldUsageLocked(prov, now)
			var hourlySpend float64
			var hourlyTurns int
			for _, e := range s.hourlySpend[prov] {
				hourlySpend += e.spendUSD
				hourlyTurns += e.turns
			}

			if ceiling.MaxSpendPerHour > 0 && hourlySpend >= ceiling.MaxSpendPerHour {
				return &SafeguardStatus{
					Allowed:   false,
					Violated:  true,
					TripType:  "provider_ceiling",
					Reason:    fmt.Sprintf("provider %s hourly spend ceiling reached ($%.2f/hr >= $%.2f/hr cap)", prov, hourlySpend, ceiling.MaxSpendPerHour),
					ShouldCap: false, // Provider throttled, don't cap the task permanently
				}, nil
			}

			if ceiling.MaxTurnsPerHour > 0 && hourlyTurns >= ceiling.MaxTurnsPerHour {
				return &SafeguardStatus{
					Allowed:   false,
					Violated:  true,
					TripType:  "provider_ceiling",
					Reason:    fmt.Sprintf("provider %s hourly turns ceiling reached (%d/hr >= %d/hr cap)", prov, hourlyTurns, ceiling.MaxTurnsPerHour),
					ShouldCap: false,
				}, nil
			}
		}
	}

	// 4. Runaway Agent Loop Safeguards: Circuit Breaker Inspection
	if s.DB != nil {
		var isTripped int
		var failingTool, lastError string
		err := s.DB.QueryRowContext(ctx, `
			SELECT is_tripped, COALESCE(failing_tool, ''), COALESCE(last_error, '')
			FROM agent_circuit_breakers
			WHERE session_id = ?
		`, task.ID).Scan(&isTripped, &failingTool, &lastError)
		if err == nil && isTripped == 1 {
			return &SafeguardStatus{
				Allowed:   false,
				Violated:  true,
				TripType:  "circuit_breaker",
				Reason:    fmt.Sprintf("runaway failure loop detected: circuit breaker tripped for task (%s: %s)", failingTool, lastError),
				ShouldCap: true,
			}, nil
		}
	}

	return &SafeguardStatus{
		Allowed:  true,
		Violated: false,
		Reason:   "safeguards clear; all usage within acceptable parameters",
	}, nil
}

// EnforceSafeguardCap caps a task in the database and records an activity log event.
func (s *SafeguardsManager) EnforceSafeguardCap(ctx context.Context, taskID, reason string) error {
	if s.DB == nil {
		return nil
	}

	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE tasks
		 SET execution_stage = 'capped', is_blocked = 1, block_reason = ?, updated_at = ?
		 WHERE id = ?`,
		reason, now, taskID,
	)
	if err != nil {
		return fmt.Errorf("cap task %s: %w", taskID, err)
	}

	// Record in activity log
	_, _ = s.DB.ExecContext(ctx,
		`INSERT INTO activity_log (task_id, event_type, details, created_at)
		 VALUES (?, 'safeguard_cap', ?, ?)`,
		taskID, reason, now,
	)

	return nil
}
