package telemetry

import (
	"context"
	"log/slog"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"
)

// PollQuotas refreshes cached provider quota in the StayPoint database. It is
// safe to call as often as convenient: the quota.Poller enforces a persisted
// 15-minute per-provider floor, and every failure fails open (the pacer falls
// back to local estimates). It never returns an error.
func PollQuotas(ctx context.Context) {
	cfg, err := config.LoadConfig()
	if err != nil || cfg.DBPath == "" {
		return
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		slog.Debug("quota poll: open db failed", slog.Any("error", err))
		return
	}
	defer store.Close()
	if fetched := quota.NewPoller(quota.Store{DB: store.DB()}).PollOnce(ctx); len(fetched) > 0 {
		slog.Debug("quota polled", slog.Any("providers", fetched))
	}
}
