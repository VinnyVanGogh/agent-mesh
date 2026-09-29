package telemetry

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"
)

type rawStateJSON struct {
	Quotas   map[string]map[string]interface{} `json:"quotas"`
	Lockouts map[string]interface{}            `json:"lockouts"`
}

func PollQuotas(ctx context.Context) {
	slog.Info("Polling quotas...")

	fetchers := []quota.Fetcher{
		quota.NewClaudeFetcher(),
		quota.NewGeminiFetcher(),
	}

	home, _ := os.UserHomeDir()
	statePath := filepath.Join(home, ".config", "rate-limits", "state.json")

	// Read existing state
	var state rawStateJSON
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &state)
	}

	if state.Quotas == nil {
		state.Quotas = make(map[string]map[string]interface{})
	}
	if state.Lockouts == nil {
		state.Lockouts = make(map[string]interface{})
	}

	for _, f := range fetchers {
		snap, err := f.Fetch(ctx)
		if err != nil {
			slog.Debug("Fetch failed", slog.String("provider", f.Provider()), slog.Any("error", err))
			continue
		}

		provKey := "Claude (Personal)"
		if f.Provider() == "gemini" {
			provKey = "Gemini"
		} else if f.Provider() == "claude" {
			provKey = "Claude (Personal)"
		}

		entry := state.Quotas[provKey]
		if entry == nil {
			entry = make(map[string]interface{})
		}

		entry["last_updated"] = snap.FetchedAt.Format(time.RFC3339)

		if snap.FiveHour != nil {
			entry["five_hour_used"] = snap.FiveHour.Utilization
			entry["five_hour_remaining"] = 100.0 - snap.FiveHour.Utilization
			if !snap.FiveHour.ResetsAt.IsZero() {
				entry["five_hour_resets_at"] = float64(snap.FiveHour.ResetsAt.Unix())
			}
		}
		if snap.Weekly != nil {
			entry["weekly_used"] = snap.Weekly.Utilization
			entry["weekly_remaining"] = 100.0 - snap.Weekly.Utilization
			if !snap.Weekly.ResetsAt.IsZero() {
				entry["weekly_resets_at"] = float64(snap.Weekly.ResetsAt.Unix())
			}
		}

		state.Quotas[provKey] = entry
	}

	// Write back
	if out, err := json.MarshalIndent(state, "", "  "); err == nil {
		tmpPath := statePath + ".tmp"
		if err := os.WriteFile(tmpPath, out, 0644); err == nil {
			_ = os.Rename(tmpPath, statePath)
		}
	}
}
