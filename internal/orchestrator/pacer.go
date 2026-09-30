package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"
)

// PacingState indicates the rate-limit health of a provider.
type PacingState string

const (
	PacingOptimal   PacingState = "optimal"   // < 70% utilization: full throughput, no artificial delays
	PacingCaution   PacingState = "caution"   // 70%-85% utilization: dynamic pacing active, spacing turns
	PacingThrottled PacingState = "throttled" // 85%-95% utilization: aggressive throttling, only critical/urgent allowed
	PacingExhausted PacingState = "exhausted" // >= 95% or locked: hard stop, trigger failover or pause
)

// PacerConfig tunes the 5-hour rate limit pacing thresholds.
type PacerConfig struct {
	WarningThresholdPct  float64       `json:"warning_threshold_pct"`  // default 70.0%
	ThrottleThresholdPct float64       `json:"throttle_threshold_pct"` // default 85.0%
	CeilingThresholdPct  float64       `json:"ceiling_threshold_pct"`  // default 95.0%
	MinPacingDelay       time.Duration `json:"min_pacing_delay"`       // default 5s
	MaxPacingDelay       time.Duration `json:"max_pacing_delay"`       // default 60s
	NowFunc              func() time.Time
}

// DefaultPacerConfig returns the standard pacing configuration.
func DefaultPacerConfig() PacerConfig {
	return PacerConfig{
		WarningThresholdPct:  70.0,
		ThrottleThresholdPct: 85.0,
		CeilingThresholdPct:  95.0,
		MinPacingDelay:       5 * time.Second,
		MaxPacingDelay:       60 * time.Second,
		NowFunc:              time.Now,
	}
}

// PacingAssessment summarizes the pacing evaluation for a given task and provider.
type PacingAssessment struct {
	Provider         string        `json:"provider"`
	State            PacingState   `json:"state"`
	FiveHourUsedPct  float64       `json:"five_hour_used_pct"`
	FiveHourLeftPct  float64       `json:"five_hour_left_pct"`
	WeeklyUsedPct    float64       `json:"weekly_used_pct"`
	WeeklyLeftPct    float64       `json:"weekly_left_pct"`
	ResetsAt         time.Time     `json:"resets_at"`
	TimeUntilReset   time.Duration `json:"time_until_reset"`
	PacingDelay      time.Duration `json:"pacing_delay"`
	CanExecute       bool          `json:"can_execute"`
	SuggestedFailover string       `json:"suggested_failover,omitempty"`
	Reason           string        `json:"reason"`
}

// ExecutionPacer tracks provider quotas and throttles execution to prevent 5-hour lockouts.
type ExecutionPacer struct {
	mu     sync.Mutex
	DB     *sql.DB
	Config PacerConfig
	appCfg *config.Config
}

// NewExecutionPacer creates a rate-limit-aware pacer.
func NewExecutionPacer(dbConn *sql.DB, pacerCfg PacerConfig, appCfg *config.Config) *ExecutionPacer {
	if pacerCfg.WarningThresholdPct <= 0 {
		pacerCfg.WarningThresholdPct = 70.0
	}
	if pacerCfg.ThrottleThresholdPct <= 0 {
		pacerCfg.ThrottleThresholdPct = 85.0
	}
	if pacerCfg.CeilingThresholdPct <= 0 {
		pacerCfg.CeilingThresholdPct = 95.0
	}
	if pacerCfg.MinPacingDelay <= 0 {
		pacerCfg.MinPacingDelay = 5 * time.Second
	}
	if pacerCfg.MaxPacingDelay <= 0 {
		pacerCfg.MaxPacingDelay = 60 * time.Second
	}
	if pacerCfg.NowFunc == nil {
		pacerCfg.NowFunc = time.Now
	}

	return &ExecutionPacer{
		DB:     dbConn,
		Config: pacerCfg,
		appCfg: appCfg,
	}
}

// now returns the current time using the configured clock.
func (p *ExecutionPacer) now() time.Time {
	if p.Config.NowFunc != nil {
		return p.Config.NowFunc()
	}
	return time.Now()
}

// EvaluatePacing evaluates the 5-hour quota and priority of a task to calculate pacing delay and admissibility.
func (p *ExecutionPacer) EvaluatePacing(ctx context.Context, provider string, taskPriority string) (*PacingAssessment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prov := strings.ToLower(strings.TrimSpace(provider))
	if prov == "" {
		prov = "claude"
	}

	now := p.now()
	assessment := &PacingAssessment{
		Provider:        prov,
		State:           PacingOptimal,
		FiveHourLeftPct: 100.0,
		WeeklyLeftPct:   100.0,
		CanExecute:      true,
	}

	if p.DB == nil {
		assessment.Reason = "no database connection; operating without pacing constraints"
		return assessment, nil
	}

	store := quota.Store{DB: p.DB}
	rows, err := store.Load(prov)
	if err != nil && err != sql.ErrNoRows {
		// Fail open if database read encounters an issue
		assessment.Reason = fmt.Sprintf("quota store read warning (%v); failing open with optimal throughput", err)
		return assessment, nil
	}

	var row5h, rowWeekly *quota.Row
	for i := range rows {
		r := &rows[i]
		if r.WindowType == quota.WindowFiveHour {
			row5h = r
		} else if r.WindowType == quota.WindowWeekly {
			rowWeekly = r
		}
	}

	// If no 5h window found for this provider, try generic claude or fail open
	if row5h != nil {
		// Auto-reset check if reset time passed
		if !row5h.ResetsAt.IsZero() && now.After(row5h.ResetsAt) && row5h.ResetsAt.After(row5h.UpdatedAt) {
			row5h.UsedPct = 0.0
		}

		assessment.FiveHourUsedPct = row5h.UsedPct
		assessment.FiveHourLeftPct = math.Max(0, 100.0-row5h.UsedPct)
		assessment.ResetsAt = row5h.ResetsAt
		if !row5h.ResetsAt.IsZero() && row5h.ResetsAt.After(now) {
			assessment.TimeUntilReset = row5h.ResetsAt.Sub(now)
		}
		if row5h.UsedPct >= 100.0 {
			assessment.State = PacingExhausted
		}
	}

	if rowWeekly != nil {
		assessment.WeeklyUsedPct = rowWeekly.UsedPct
		assessment.WeeklyLeftPct = math.Max(0, 100.0-rowWeekly.UsedPct)
		if rowWeekly.UsedPct >= 100.0 {
			assessment.State = PacingExhausted
		}
	}

	// Determine Pacing State based on 5-hour utilization
	used5h := assessment.FiveHourUsedPct
	if assessment.State != PacingExhausted {
		if used5h >= p.Config.CeilingThresholdPct {
			assessment.State = PacingExhausted
		} else if used5h >= p.Config.ThrottleThresholdPct {
			assessment.State = PacingThrottled
		} else if used5h >= p.Config.WarningThresholdPct {
			assessment.State = PacingCaution
		} else {
			assessment.State = PacingOptimal
		}
	}

	normPriority := NormalizePriority(taskPriority)

	switch assessment.State {
	case PacingOptimal:
		assessment.PacingDelay = 0
		assessment.CanExecute = true
		assessment.Reason = fmt.Sprintf("5-hour quota optimal (%.1f%% used, %.1f%% headroom); full throughput enabled",
			used5h, assessment.FiveHourLeftPct)

	case PacingCaution:
		// Calculate smooth turn spacing to prevent rushing into the 85% throttle band.
		// pacingDelay spreads turns over the remaining duration until reset.
		turnsLeft := assessment.FiveHourLeftPct / 5.0 // estimate ~5% per turn
		if turnsLeft < 1 {
			turnsLeft = 1
		}
		if assessment.TimeUntilReset > 0 {
			rawDelay := time.Duration(float64(assessment.TimeUntilReset) / (turnsLeft + 1))
			if rawDelay < p.Config.MinPacingDelay {
				assessment.PacingDelay = p.Config.MinPacingDelay
			} else if rawDelay > p.Config.MaxPacingDelay {
				assessment.PacingDelay = p.Config.MaxPacingDelay
			} else {
				assessment.PacingDelay = rawDelay
			}
		} else {
			assessment.PacingDelay = p.Config.MinPacingDelay
		}
		assessment.CanExecute = true
		assessment.Reason = fmt.Sprintf("5-hour quota caution (%.1f%% used); dynamic pacing delay of %v applied to prevent lockout",
			used5h, assessment.PacingDelay.Round(time.Second))

	case PacingThrottled:
		// In throttled mode (85%-95%), only critical and urgent tasks may run!
		if normPriority == PriorityCritical || normPriority == PriorityUrgent {
			assessment.CanExecute = true
			assessment.PacingDelay = p.Config.MaxPacingDelay
			assessment.Reason = fmt.Sprintf("5-hour quota heavily throttled (%.1f%% used); priority override granted for %s task with %v pacing delay",
				used5h, strings.ToUpper(normPriority), assessment.PacingDelay)
		} else {
			assessment.CanExecute = false
			assessment.PacingDelay = p.Config.MaxPacingDelay
			failover := p.checkFailover(prov, now)
			assessment.SuggestedFailover = failover
			if failover != "" {
				assessment.Reason = fmt.Sprintf("5-hour quota throttled (%.1f%% used); non-critical task deferred to avoid lockout. Dynamic failover available: %s",
					used5h, failover)
			} else {
				assessment.Reason = fmt.Sprintf("5-hour quota throttled (%.1f%% used); non-critical task deferred until quota window resets (in %v)",
					used5h, assessment.TimeUntilReset.Round(time.Minute))
			}
		}

	case PacingExhausted:
		assessment.CanExecute = false
		assessment.PacingDelay = p.Config.MaxPacingDelay
		failover := p.checkFailover(prov, now)
		assessment.SuggestedFailover = failover
		if failover != "" {
			assessment.Reason = fmt.Sprintf("5-hour quota exhausted (%.1f%% used, resets in %v); hard stop on %s. Dynamic failover available to %s",
				used5h, assessment.TimeUntilReset.Round(time.Minute), prov, failover)
		} else {
			assessment.Reason = fmt.Sprintf("5-hour quota exhausted (%.1f%% used); all providers locked until reset in %v",
				used5h, assessment.TimeUntilReset.Round(time.Minute))
		}
	}

	return assessment, nil
}

// checkFailover inspects alternate providers for available headroom.
func (p *ExecutionPacer) checkFailover(currentProvider string, now time.Time) string {
	store := quota.Store{DB: p.DB}
	candidates := []string{"gemini", "claude", "codex"}

	for _, cand := range candidates {
		if cand == currentProvider {
			continue
		}
		rows, err := store.Load(cand)
		if err != nil || len(rows) == 0 {
			continue
		}
		locked := false
		var left5h float64 = 100.0
		for _, r := range rows {
			if r.UsedPct >= 100.0 {
				locked = true
				break
			}
			if r.WindowType == quota.WindowFiveHour {
				left5h = math.Max(0, 100.0-r.UsedPct)
			}
		}
		if !locked && left5h > 20.0 {
			return cand
		}
	}
	return ""
}
