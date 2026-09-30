package router

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry/quota"

	_ "modernc.org/sqlite"
)

// applyLiveQuotas overlays fresh rows from quota_windows onto the pools. Any
// failure (no DB, unreadable, stale rows) leaves the pools untouched so routing
// keeps working on local estimates.
//
// The "claude" provider pool is resolved dynamically: the in-process fetcher
// reads whichever Claude Code account is primary in the local Keychain, which
// may be either the work or personal seat.
func applyLiveQuotas(state *PacerState, now time.Time) {
	conn := openQuotaDB()
	if conn == nil {
		return
	}
	defer conn.Close()
	store := quota.Store{DB: conn}
	pools := map[string]PoolID{
		"claude": resolveClaudeLivePool(),
		"gemini": PoolGeminiNative,
	}
	for provider, id := range pools {
		rows, err := store.Load(provider)
		if err != nil {
			continue
		}
		applyQuotaRows(state.Pools[id], rows, now)
	}
}

// resolveClaudeLivePool returns the pool that should receive quota data from the
// in-process Claude fetcher. It peeks at the latest statusline-samples.ndjson
// entry: if the account email looks like a work address (non-gmail, non-personal),
// the data belongs to the work pool; otherwise the personal pool.
func resolveClaudeLivePool() PoolID {
	home, err := os.UserHomeDir()
	if err != nil {
		return PoolPersonalClaude
	}
	email := peekLatestSampleEmail(home)
	if email == "" {
		return PoolPersonalClaude
	}
	lower := strings.ToLower(email)
	if !strings.Contains(lower, "gmail.com") && !strings.Contains(lower, "personal") {
		return PoolWorkClaude
	}
	return PoolPersonalClaude
}

// peekLatestSampleEmail reads the last non-empty line of statusline-samples.ndjson
// and returns the account_email field, or "" when unavailable.
func peekLatestSampleEmail(home string) string {
	path := filepath.Join(home, ".config", "token-telemetry", "statusline-samples.ndjson")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return ""
	}
	tail := int64(2048)
	if fi.Size() < tail {
		tail = fi.Size()
	}
	if _, err := f.Seek(fi.Size()-tail, io.SeekStart); err != nil {
		return ""
	}
	buf := make([]byte, tail)
	n, _ := io.ReadFull(f, buf)
	lines := bytes.Split(buf[:n], []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var s struct {
			Email string `json:"account_email"`
		}
		if json.Unmarshal(line, &s) == nil && s.Email != "" {
			return s.Email
		}
	}
	return ""
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
