package quota

import (
	"database/sql"
	"errors"
	"time"
)

// Window types as stored in quota_windows.window_type.
const (
	WindowFiveHour = "rolling_5h"
	WindowWeekly   = "weekly_7d"
	WindowMonthly  = "monthly"
)

// StaleAfter is how old a cached row may be before consumers must ignore it and
// fall back to local estimation (four poll intervals).
const StaleAfter = 4 * MinInterval

const tsLayout = time.RFC3339

// Row is one cached window reading.
type Row struct {
	Provider   string
	WindowType string
	UsedPct    float64
	ResetsAt   time.Time
	UpdatedAt  time.Time
}

// Store persists snapshots in quota_windows and throttle state in
// quota_fetch_state.
type Store struct{ DB *sql.DB }

// Save replaces the cached windows for snap.Provider. Windows the provider did
// not report are removed so a stale value never outlives its source.
func (s Store) Save(snap *Snapshot) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM quota_windows WHERE pool_key = ?`, snap.Provider); err != nil {
		return err
	}
	ts := snap.FetchedAt.UTC().Format(tsLayout)
	for _, w := range []struct {
		typ string
		w   *Window
	}{{WindowFiveHour, snap.FiveHour}, {WindowWeekly, snap.Weekly}, {WindowMonthly, snap.Monthly}} {
		if w.w == nil {
			continue
		}
		var reset any
		if !w.w.ResetsAt.IsZero() {
			reset = w.w.ResetsAt.UTC().Format(tsLayout)
		}
		if _, err := tx.Exec(`INSERT INTO quota_windows (pool_key, window_type, used_percent, remaining_pct, is_locked, resets_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			snap.Provider, w.typ, w.w.Utilization, clampPct(100-w.w.Utilization), b2i(w.w.Utilization >= 100), reset, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Load returns cached rows for provider, including stale ones (callers apply
// StaleAfter themselves so the CLI can still show "stale").
func (s Store) Load(provider string) ([]Row, error) {
	rows, err := s.DB.Query(`SELECT window_type, used_percent, resets_at, updated_at FROM quota_windows WHERE pool_key = ?`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		r := Row{Provider: provider}
		var reset sql.NullString
		var updated string
		if err := rows.Scan(&r.WindowType, &r.UsedPct, &reset, &updated); err != nil {
			return nil, err
		}
		r.ResetsAt = parseTime(reset.String)
		r.UpdatedAt = parseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// State is the persisted fetch bookkeeping for one provider.
type State struct {
	LastAttempt time.Time
	NextAttempt time.Time
	LastSuccess time.Time
	Status      string
}

func (s Store) State(provider string) (State, bool, error) {
	var st State
	var la, na string
	var ls sql.NullString
	err := s.DB.QueryRow(`SELECT last_attempt_at, next_attempt_at, last_success_at, last_status FROM quota_fetch_state WHERE provider = ?`, provider).
		Scan(&la, &na, &ls, &st.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	st.LastAttempt, st.NextAttempt, st.LastSuccess = parseTime(la), parseTime(na), parseTime(ls.String)
	return st, true, nil
}

func (s Store) SetState(provider string, st State) error {
	var ls any
	if !st.LastSuccess.IsZero() {
		ls = st.LastSuccess.UTC().Format(tsLayout)
	}
	_, err := s.DB.Exec(`INSERT INTO quota_fetch_state (provider, last_attempt_at, next_attempt_at, last_success_at, last_status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET last_attempt_at=excluded.last_attempt_at, next_attempt_at=excluded.next_attempt_at,
			last_success_at=excluded.last_success_at, last_status=excluded.last_status`,
		provider, st.LastAttempt.UTC().Format(tsLayout), st.NextAttempt.UTC().Format(tsLayout), ls, st.Status)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
