package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PoolID string

const (
	PoolWorkClaude     PoolID = "work-claude"
	PoolPersonalClaude PoolID = "personal-claude"
	PoolGeminiNative   PoolID = "gemini-native"
	Pool3PClaude       PoolID = "3p-claude"
)

type QuotaWindow struct {
	UsedPct      float64   `json:"used_pct"`
	RemainingPct float64   `json:"remaining_pct"`
	ResetsAt     time.Time `json:"resets_at"`
	IsLocked     bool      `json:"is_locked"`
}

type QuotaPool struct {
	ID            PoolID      `json:"id"`
	Name          string      `json:"name"`
	AccountEmail  string      `json:"account_email,omitempty"`
	FiveHour      QuotaWindow `json:"five_hour"`
	Weekly        QuotaWindow `json:"weekly"`
	IsLocked      bool        `json:"is_locked"`
	LockoutUntil  time.Time   `json:"lockout_until,omitempty"`
	LockoutReason string      `json:"lockout_reason,omitempty"`
	TurnsRunway   int         `json:"turns_runway"`
	Turns5h       int         `json:"turns_5h"`
	TurnsWeekly   int         `json:"turns_weekly"`
	BurnRate5h    float64     `json:"burn_rate_5h"` // % per turn
	BurnRateW     float64     `json:"burn_rate_w"`  // % per turn
	LastUpdated   time.Time   `json:"last_updated"`
}

type PacerState struct {
	Pools       map[PoolID]*QuotaPool `json:"pools"`
	LastUpdated time.Time             `json:"last_updated"`
}

type rawStateJSON struct {
	Lockouts map[string]struct {
		Provider     string  `json:"provider"`
		Locked       bool    `json:"locked"`
		ResetsAt     float64 `json:"resets_at"`
		ResetTimeStr string  `json:"reset_time_str"`
		ResetTimeISO string  `json:"reset_time_iso"`
		DetectedAt   string  `json:"detected_at"`
	} `json:"lockouts"`
	Quotas map[string]struct {
		FiveHourUsed      float64 `json:"five_hour_used"`
		FiveHourRemaining float64 `json:"five_hour_remaining"`
		FiveHourResetsAt  float64 `json:"five_hour_resets_at"`
		WeeklyUsed        float64 `json:"weekly_used"`
		WeeklyRemaining   float64 `json:"weekly_remaining"`
		WeeklyResetsAt    float64 `json:"weekly_resets_at"`
		LastUpdated       string  `json:"last_updated"`
	} `json:"quotas"`
}

type rawSampleJSON struct {
	Timestamp       string  `json:"ts"`
	SessionID       string  `json:"session_id"`
	ModelID         string  `json:"model_id"`
	FiveHourPct     float64 `json:"five_hour_pct"`
	SevenDayPct     float64 `json:"seven_day_pct"`
	FiveHourResets  float64 `json:"five_hour_resets_at"`
	SevenDayResets  float64 `json:"seven_day_resets_at"`
	AccountEmail    string  `json:"account_email"`
}

type rawEstimatesJSON struct {
	PctPerTurn struct {
		FiveHour struct {
			MeanPct float64 `json:"mean_pct"`
		} `json:"five_hour"`
		SevenDay struct {
			MeanPct float64 `json:"mean_pct"`
		} `json:"seven_day"`
	} `json:"pct_per_turn"`
}

// LoadPacerState reads live quotas from ~/.config/rate-limits/state.json,
// statusline samples from ~/.config/token-telemetry/statusline-samples.ndjson,
// and burn rates from ~/.config/token-telemetry/statusline-estimates.json.
func LoadPacerState() (*PacerState, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	state := &PacerState{
		Pools:       make(map[PoolID]*QuotaPool),
		LastUpdated: time.Now(),
	}

	// Default burn rates
	claudeBurn5h := 5.62
	claudeBurnW := 1.26
	geminiBurn5h := 1.50
	geminiBurnW := 0.40

	// Try reading statusline-estimates.json for dynamically learned burn rates
	estimatesPath := filepath.Join(home, ".config", "token-telemetry", "statusline-estimates.json")
	if estData, err := os.ReadFile(estimatesPath); err == nil {
		var est rawEstimatesJSON
		if json.Unmarshal(estData, &est) == nil {
			if est.PctPerTurn.FiveHour.MeanPct > 0.01 {
				claudeBurn5h = est.PctPerTurn.FiveHour.MeanPct
			}
			if est.PctPerTurn.SevenDay.MeanPct > 0.01 {
				claudeBurnW = est.PctPerTurn.SevenDay.MeanPct
			}
		}
	}

	// Initialize the 4 pools
	state.Pools[PoolWorkClaude] = &QuotaPool{
		ID:         PoolWorkClaude,
		Name:       "Claude (Work)",
		BurnRate5h: claudeBurn5h,
		BurnRateW:  claudeBurnW,
		FiveHour:   QuotaWindow{RemainingPct: 100},
		Weekly:     QuotaWindow{RemainingPct: 100},
	}
	state.Pools[PoolPersonalClaude] = &QuotaPool{
		ID:         PoolPersonalClaude,
		Name:       "Claude (Personal)",
		BurnRate5h: claudeBurn5h,
		BurnRateW:  claudeBurnW,
		FiveHour:   QuotaWindow{RemainingPct: 100},
		Weekly:     QuotaWindow{RemainingPct: 100},
	}
	state.Pools[PoolGeminiNative] = &QuotaPool{
		ID:          PoolGeminiNative,
		Name:        "Gemini Native",
		BurnRate5h:  geminiBurn5h,
		BurnRateW:   geminiBurnW,
		FiveHour:    QuotaWindow{RemainingPct: 100},
		Weekly:      QuotaWindow{RemainingPct: 100},
	}
	state.Pools[Pool3PClaude] = &QuotaPool{
		ID:          Pool3PClaude,
		Name:        "Antigravity 3P",
		BurnRate5h:  claudeBurn5h,
		BurnRateW:   claudeBurnW,
		FiveHour:    QuotaWindow{RemainingPct: 100},
		Weekly:      QuotaWindow{RemainingPct: 100},
	}

	// 1. Read state.json
	statePath := filepath.Join(home, ".config", "rate-limits", "state.json")
	var stateData rawStateJSON
	if raw, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(raw, &stateData)
	}

	// Apply quotas from state.json
	if q, ok := stateData.Quotas["Gemini"]; ok {
		applyStateQuota(state.Pools[PoolGeminiNative], q.FiveHourUsed, q.FiveHourRemaining, q.FiveHourResetsAt, q.WeeklyUsed, q.WeeklyRemaining, q.WeeklyResetsAt, q.LastUpdated)
	}
	if q, ok := stateData.Quotas["Antigravity 3P"]; ok {
		applyStateQuota(state.Pools[Pool3PClaude], q.FiveHourUsed, q.FiveHourRemaining, q.FiveHourResetsAt, q.WeeklyUsed, q.WeeklyRemaining, q.WeeklyResetsAt, q.LastUpdated)
	}
	if q, ok := stateData.Quotas["Claude (Personal)"]; ok {
		applyStateQuota(state.Pools[PoolPersonalClaude], q.FiveHourUsed, q.FiveHourRemaining, q.FiveHourResetsAt, q.WeeklyUsed, q.WeeklyRemaining, q.WeeklyResetsAt, q.LastUpdated)
	}
	if q, ok := stateData.Quotas["Claude (Work)"]; ok {
		applyStateQuota(state.Pools[PoolWorkClaude], q.FiveHourUsed, q.FiveHourRemaining, q.FiveHourResetsAt, q.WeeklyUsed, q.WeeklyRemaining, q.WeeklyResetsAt, q.LastUpdated)
	}

	// 2. Read latest statusline-samples.ndjson (fast tail read)
	samplesPath := filepath.Join(home, ".config", "token-telemetry", "statusline-samples.ndjson")
	readSamplesTail(samplesPath, state)

	// 3. Evaluate lockouts from state.json and thresholds
	now := time.Now()
	for _, pool := range state.Pools {
		// Check explicit lockout map in state.json
		var lockEntry *struct {
			Provider     string  `json:"provider"`
			Locked       bool    `json:"locked"`
			ResetsAt     float64 `json:"resets_at"`
			ResetTimeStr string  `json:"reset_time_str"`
			ResetTimeISO string  `json:"reset_time_iso"`
			DetectedAt   string  `json:"detected_at"`
		}

		switch pool.ID {
		case PoolGeminiNative:
			if l, ok := stateData.Lockouts["Gemini"]; ok {
				lockEntry = &l
			}
		case Pool3PClaude:
			if l, ok := stateData.Lockouts["Antigravity 3P"]; ok {
				lockEntry = &l
			}
		case PoolPersonalClaude:
			if l, ok := stateData.Lockouts["Claude (Personal)"]; ok {
				lockEntry = &l
			} else if l, ok := stateData.Lockouts["Claude"]; ok {
				lockEntry = &l
			}
		case PoolWorkClaude:
			if l, ok := stateData.Lockouts["Claude (Work)"]; ok {
				lockEntry = &l
			}
		}

		if lockEntry != nil && lockEntry.Locked && lockEntry.ResetsAt > float64(now.Unix()) {
			pool.IsLocked = true
			pool.LockoutUntil = time.Unix(int64(lockEntry.ResetsAt), 0)
			pool.LockoutReason = "Reported locked by rate-limit notifier"
			pool.FiveHour.IsLocked = true
		} else if pool.FiveHour.RemainingPct <= 0.0 || pool.FiveHour.UsedPct >= 100.0 {
			if pool.FiveHour.ResetsAt.After(now) {
				pool.IsLocked = true
				pool.FiveHour.IsLocked = true
				pool.LockoutUntil = pool.FiveHour.ResetsAt
				pool.LockoutReason = "5-hour quota exhausted (100% used)"
			}
		} else if pool.Weekly.RemainingPct <= 0.0 || pool.Weekly.UsedPct >= 100.0 {
			if pool.Weekly.ResetsAt.After(now) {
				pool.IsLocked = true
				pool.Weekly.IsLocked = true
				pool.LockoutUntil = pool.Weekly.ResetsAt
				pool.LockoutReason = "Weekly quota exhausted (100% used)"
			}
		}

		// Calculate turns runway
		if pool.IsLocked {
			pool.TurnsRunway = 0
			pool.Turns5h = 0
			pool.TurnsWeekly = 0
		} else {
			if pool.BurnRate5h > 0 {
				pool.Turns5h = int(pool.FiveHour.RemainingPct / pool.BurnRate5h)
			}
			if pool.BurnRateW > 0 {
				pool.TurnsWeekly = int(pool.Weekly.RemainingPct / pool.BurnRateW)
			}
			pool.TurnsRunway = pool.Turns5h
			if pool.TurnsWeekly < pool.TurnsRunway {
				pool.TurnsRunway = pool.TurnsWeekly
			}
			if pool.TurnsRunway < 0 {
				pool.TurnsRunway = 0
			}
		}
	}

	return state, nil
}

func applyStateQuota(p *QuotaPool, used5h, rem5h, resets5h, usedW, remW, resetsW float64, updatedISO string) {
	p.FiveHour.UsedPct = used5h
	p.FiveHour.RemainingPct = rem5h
	if resets5h > 0 {
		p.FiveHour.ResetsAt = time.Unix(int64(resets5h), 0)
	}

	p.Weekly.UsedPct = usedW
	p.Weekly.RemainingPct = remW
	if resetsW > 0 {
		p.Weekly.ResetsAt = time.Unix(int64(resetsW), 0)
	}

	if updatedISO != "" {
		if t, err := time.Parse(time.RFC3339, updatedISO); err == nil {
			p.LastUpdated = t
		}
	}
}

// readSamplesTail performs a fast reverse-seek read on statusline-samples.ndjson
// to capture the most recent samples for work and personal Claude accounts.
func readSamplesTail(path string, state *PacerState) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.Size() == 0 {
		return
	}

	tailBytes := int64(128 * 1024)
	if stat.Size() < tailBytes {
		tailBytes = stat.Size()
	}

	offset := stat.Size() - tailBytes
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}

	buf := make([]byte, tailBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return
	}
	buf = buf[:n]

	// Split by newline and process backwards
	lines := bytes.Split(buf, []byte("\n"))
	workFound := false
	personalFound := false

	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}

		var sample rawSampleJSON
		if err := json.Unmarshal(line, &sample); err != nil {
			continue
		}

		email := strings.ToLower(sample.AccountEmail)
		isWork := !strings.Contains(email, "gmail.com") && !strings.Contains(email, "personal") && email != ""
		isPersonal := strings.Contains(email, "gmail.com") || (!isWork && email != "")

		if isWork && !workFound {
			p := state.Pools[PoolWorkClaude]
			sampleTime, _ := time.Parse(time.RFC3339, sample.Timestamp)
			if p.LastUpdated.IsZero() || sampleTime.After(p.LastUpdated) {
				p.FiveHour.UsedPct = sample.FiveHourPct
				p.FiveHour.RemainingPct = math.Max(0, 100.0-sample.FiveHourPct)
				if sample.FiveHourResets > 0 {
					p.FiveHour.ResetsAt = time.Unix(int64(sample.FiveHourResets), 0)
				}
				p.Weekly.UsedPct = sample.SevenDayPct
				p.Weekly.RemainingPct = math.Max(0, 100.0-sample.SevenDayPct)
				if sample.SevenDayResets > 0 {
					p.Weekly.ResetsAt = time.Unix(int64(sample.SevenDayResets), 0)
				}
				p.LastUpdated = sampleTime
			}
			workFound = true
		} else if isPersonal && !personalFound {
			p := state.Pools[PoolPersonalClaude]
			sampleTime, _ := time.Parse(time.RFC3339, sample.Timestamp)
			// Only override if newer or if state.json was missing data
			if p.LastUpdated.IsZero() || sampleTime.After(p.LastUpdated) {
				p.FiveHour.UsedPct = sample.FiveHourPct
				p.FiveHour.RemainingPct = math.Max(0, 100.0-sample.FiveHourPct)
				if sample.FiveHourResets > 0 {
					p.FiveHour.ResetsAt = time.Unix(int64(sample.FiveHourResets), 0)
				}
				p.Weekly.UsedPct = sample.SevenDayPct
				p.Weekly.RemainingPct = math.Max(0, 100.0-sample.SevenDayPct)
				if sample.SevenDayResets > 0 {
					p.Weekly.ResetsAt = time.Unix(int64(sample.SevenDayResets), 0)
				}
				p.LastUpdated = sampleTime
			}
			personalFound = true
		}

		if workFound && personalFound {
			break
		}
	}
}

// FormatDuration formats remaining duration nicely (e.g. "42m", "3h 12m", "2d 4h")
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
