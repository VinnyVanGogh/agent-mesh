package router

import (
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"

	_ "modernc.org/sqlite"
)

// applyLiveQuotas overlays fresh rows from quota_windows onto the pools. Any
// failure (no DB, unreadable, stale rows) leaves the pools untouched so routing
// keeps working on local estimates.
//
// The "claude" provider rows come from whichever Claude Code account holds the
// Keychain credential. They go to one pool, chosen from that account's email,
// and are applied before the seat-specific rows so a dedicated seat reading
// always wins. Order is fixed: ranging a map here made the winner random per
// load, which flipped the dashboard between seats (STA-283).
func applyLiveQuotas(state *PacerState, now time.Time) {
	conn := openQuotaDB()
	if conn == nil {
		return
	}
	defer conn.Close()
	store := quota.Store{DB: conn}
	pools := []struct {
		provider string
		id       PoolID
	}{
		{"claude", resolveClaudeLivePool()},
		{"claude_personal", PoolPersonalClaude},
		{"claude_work", PoolWorkClaude},
		{"gemini", PoolGeminiNative},
	}
	for _, e := range pools {
		rows, err := store.Load(e.provider)
		if err != nil || len(rows) == 0 {
			continue
		}
		if p, ok := state.Pools[e.id]; ok && p != nil {
			applyQuotaRows(p, rows, now)
		}
	}
}

// resolveClaudeLivePool returns the pool that should receive quota data from the
// in-process Claude fetcher, based on the account Claude Code is logged into
// (~/.claude.json oauthAccount, the owner of the Keychain credential). It used
// to peek at the latest statusline sample, but both seats write samples, so the
// answer alternated whenever they interleaved (STA-283). Unknown accounts
// default to the personal seat.
func resolveClaudeLivePool() PoolID {
	home, err := os.UserHomeDir()
	if err != nil {
		return PoolPersonalClaude
	}
	email := strings.ToLower(claudeAccountEmail(home))
	if email != "" && !strings.Contains(email, "gmail.com") && !strings.Contains(email, "personal") {
		return PoolWorkClaude
	}
	return PoolPersonalClaude
}

var claudeAccountCache struct {
	sync.Mutex
	path  string
	mtime time.Time
	email string
}

// claudeAccountEmail returns oauthAccount.emailAddress from ~/.claude.json,
// cached on the file's mtime since the file is large and read on every load.
func claudeAccountEmail(home string) string {
	path := filepath.Join(home, ".claude.json")
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	c := &claudeAccountCache
	c.Lock()
	defer c.Unlock()
	if c.path == path && c.mtime.Equal(fi.ModTime()) {
		return c.email
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	c.path, c.mtime, c.email = path, fi.ModTime(), cfg.OAuthAccount.EmailAddress
	return c.email
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
