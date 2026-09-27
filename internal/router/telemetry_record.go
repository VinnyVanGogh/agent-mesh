package router

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RecordClaudeStatuslineTelemetry persists the live rate limits and token usage
// piped into statusline by Claude Code into:
// 1. ~/.config/token-telemetry/statusline-samples.ndjson
// 2. ~/.config/rate-limits/state.json
func RecordClaudeStatuslineTelemetry(payload *StatuslinePayload, isWork bool) {
	if payload == nil {
		return
	}

	// Only record if rate limits or usage are present
	has5h := payload.RateLimits.FiveHour.UsedPercentage != nil
	has7d := payload.RateLimits.SevenDay.UsedPercentage != nil
	if !has5h && !has7d && payload.ContextWindow.UsedPercentage == nil {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	now := time.Now()
	nowISO := now.Format(time.RFC3339)

	accountEmail := payload.Email
	if isWork && accountEmail == "" {
		accountEmail = "vvasile@managedsolution.com"
	} else if accountEmail == "" {
		accountEmail = "stylesbyvinny@gmail.com"
	}

	// 1. Append sample to statusline-samples.ndjson
	sample := map[string]interface{}{
		"ts":            nowISO,
		"session_id":    payload.SessionID,
		"model_id":      payload.Model.ID,
		"account_email": accountEmail,
	}

	var fiveUsed, fiveRem, fiveResets float64
	var weekUsed, weekRem, weekResets float64

	if has5h {
		fiveUsed = *payload.RateLimits.FiveHour.UsedPercentage
		fiveRem = math.Max(0, 100.0-fiveUsed)
		sample["five_hour_pct"] = fiveUsed
		if payload.RateLimits.FiveHour.ResetsAt != nil && *payload.RateLimits.FiveHour.ResetsAt > 0 {
			fiveResets = *payload.RateLimits.FiveHour.ResetsAt
			sample["five_hour_resets_at"] = fiveResets
		}
	}

	if has7d {
		weekUsed = *payload.RateLimits.SevenDay.UsedPercentage
		weekRem = math.Max(0, 100.0-weekUsed)
		sample["seven_day_pct"] = weekUsed
		if payload.RateLimits.SevenDay.ResetsAt != nil && *payload.RateLimits.SevenDay.ResetsAt > 0 {
			weekResets = *payload.RateLimits.SevenDay.ResetsAt
			sample["seven_day_resets_at"] = weekResets
		}
	}

	if payload.ContextWindow.UsedPercentage != nil {
		sample["ctx_pct"] = *payload.ContextWindow.UsedPercentage
	}
	if payload.ContextWindow.RemainingTokens != nil {
		sample["ctx_remaining"] = *payload.ContextWindow.RemainingTokens
	}
	if payload.Cost.TotalCostUSD != nil {
		sample["cum_cost_usd"] = *payload.Cost.TotalCostUSD
	}

	samplesDir := filepath.Join(home, ".config", "token-telemetry")
	_ = os.MkdirAll(samplesDir, 0755)
	samplesFile := filepath.Join(samplesDir, "statusline-samples.ndjson")
	if sampleBytes, err := json.Marshal(sample); err == nil {
		if f, err := os.OpenFile(samplesFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			_, _ = f.Write(append(sampleBytes, '\n'))
			_ = f.Close()
		}
	}

	// 2. Update ~/.config/rate-limits/state.json
	stateDir := filepath.Join(home, ".config", "rate-limits")
	_ = os.MkdirAll(stateDir, 0755)
	stateFile := filepath.Join(stateDir, "state.json")

	stateData := make(map[string]interface{})
	if raw, err := os.ReadFile(stateFile); err == nil {
		_ = json.Unmarshal(raw, &stateData)
	}

	quotas, ok := stateData["quotas"].(map[string]interface{})
	if !ok {
		quotas = make(map[string]interface{})
		stateData["quotas"] = quotas
	}

	lockouts, ok := stateData["lockouts"].(map[string]interface{})
	if !ok {
		lockouts = make(map[string]interface{})
		stateData["lockouts"] = lockouts
	}

	providerKey := "Claude (Personal)"
	if isWork || strings.Contains(strings.ToLower(accountEmail), "managedsolution.com") {
		providerKey = "Claude (Work)"
	}

	quotaRecord := map[string]interface{}{
		"five_hour_used":      math.Round(fiveUsed*10) / 10,
		"five_hour_remaining": math.Round(fiveRem*10) / 10,
		"five_hour_resets_at": fiveResets,
		"weekly_used":         math.Round(weekUsed*10) / 10,
		"weekly_remaining":    math.Round(weekRem*10) / 10,
		"weekly_resets_at":    weekResets,
		"last_updated":        nowISO,
	}

	quotas[providerKey] = quotaRecord
	quotas["Claude"] = quotaRecord

	// Check lockout
	isLocked := (fiveUsed >= 100.0 && fiveResets > float64(now.Unix())) || (weekUsed >= 100.0 && weekResets > float64(now.Unix()))
	resetTimeStr := ""
	if fiveResets > 0 {
		resetTimeStr = strings.ToLower(time.Unix(int64(fiveResets), 0).Format("3:04pm"))
	}

	lockoutRecord := map[string]interface{}{
		"provider":       providerKey,
		"locked":         isLocked,
		"resets_at":      fiveResets,
		"reset_time_str": resetTimeStr,
		"reset_time_iso": time.Unix(int64(fiveResets), 0).Format(time.RFC3339),
		"last_updated":   nowISO,
	}
	lockouts[providerKey] = lockoutRecord
	lockouts["Claude"] = lockoutRecord

	// Write atomically via tmp file
	if finalJSON, err := json.MarshalIndent(stateData, "", "  "); err == nil {
		tmpFile := stateFile + ".tmp"
		if err := os.WriteFile(tmpFile, finalJSON, 0644); err == nil {
			_ = os.Rename(tmpFile, stateFile)
		}
	}
}
