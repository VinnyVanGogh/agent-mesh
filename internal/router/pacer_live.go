package router

import (
	"database/sql"
	"math"
	"os"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"

	_ "modernc.org/sqlite"
)

// livePools maps a quota provider to the pacer pool it feeds. The provider
// fetchers read whichever account the local CLI is logged into; Claude is
// attributed to the personal pool, as the retired state.json poller did.
var livePools = map[string]PoolID{
	"claude": PoolPersonalClaude,
	"gemini": PoolGeminiNative,
}

// applyLiveQuotas overlays fresh rows from quota_windows onto the pools. Any
// failure (no DB, unreadable, stale rows) leaves the pools untouched so routing
// keeps working on local estimates.
func applyLiveQuotas(state *PacerState, now time.Time) {
	conn := openQuotaDB()
	if conn == nil {
		return
	}
	defer conn.Close()
	store := quota.Store{DB: conn}
	for provider, id := range livePools {
		rows, err := store.Load(provider)
		if err != nil {
			continue
		}
		applyQuotaRows(state.Pools[id], rows, now)
	}
}

func applyQuotaRows(p *QuotaPool, rows []quota.Row, now time.Time) {
	for _, r := range rows {
		if r.UpdatedAt.IsZero() || now.Sub(r.UpdatedAt) > quota.StaleAfter {
			continue
		}
		var w *QuotaWindow
		switch r.WindowType {
		case quota.WindowFiveHour:
			w = &p.FiveHour
		case quota.WindowWeekly:
			w = &p.Weekly
		default:
			continue
		}
		w.UsedPct, w.RemainingPct, w.Known = r.UsedPct, math.Max(0, 100-r.UsedPct), true
		w.ResetsAt = r.ResetsAt
		if r.UpdatedAt.After(p.LastUpdated) {
			p.LastUpdated = r.UpdatedAt
		}
	}
}

// openQuotaDB opens the StayPoint database read-only, or returns nil when it
// does not exist yet. It never creates the file as a side effect.
func openQuotaDB() *sql.DB {
	cfg, err := config.LoadConfig()
	if err != nil || cfg.DBPath == "" {
		return nil
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return nil
	}
	conn, err := sql.Open("sqlite", "file:"+cfg.DBPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil
	}
	conn.SetMaxOpenConns(1)
	return conn
}
