package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

type TelemetryHandler struct {
	db  *sql.DB
	hub *EventHub
}

func NewTelemetryHandler(db *sql.DB, hub *EventHub) *TelemetryHandler {
	return &TelemetryHandler{db: db, hub: hub}
}

type QuotaWindowRecord struct {
	PoolKey      string    `json:"pool_key"`
	WindowType   string    `json:"window_type"`
	UsedPercent  float64   `json:"used_percent"`
	RemainingPct float64   `json:"remaining_pct"`
	IsLocked     bool      `json:"is_locked"`
	ResetsAt     *string   `json:"resets_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type QuotaFetchRecord struct {
	Provider      string  `json:"provider"`
	LastAttemptAt string  `json:"last_attempt_at"`
	NextAttemptAt string  `json:"next_attempt_at"`
	LastSuccessAt *string `json:"last_success_at,omitempty"`
	LastStatus    string  `json:"last_status"`
}

type TaskSpendSummary struct {
	TotalSpentUSD    float64 `json:"total_spent_usd"`
	TotalSpentTokens int64   `json:"total_spent_tokens"`
	TotalTurns       int     `json:"total_turns"`
	ActiveTasks      int     `json:"active_tasks"`
	DoneTasks        int     `json:"done_tasks"`
}

// GetTelemetry handles GET /api/telemetry
func (h *TelemetryHandler) GetTelemetry(w http.ResponseWriter, r *http.Request) {
	// 1. Quota windows
	var windows []QuotaWindowRecord
	qRows, err := h.db.QueryContext(r.Context(), `
		SELECT pool_key, window_type, used_percent, remaining_pct, is_locked, resets_at, updated_at
		FROM quota_windows
		ORDER BY pool_key, window_type;
	`)
	if err == nil {
		defer qRows.Close()
		for qRows.Next() {
			var wRec QuotaWindowRecord
			var isLockedInt int
			var resetsAt sql.NullString
			var updatedAtStr string
			if err := qRows.Scan(&wRec.PoolKey, &wRec.WindowType, &wRec.UsedPercent, &wRec.RemainingPct, &isLockedInt, &resetsAt, &updatedAtStr); err == nil {
				wRec.IsLocked = isLockedInt != 0
				if resetsAt.Valid {
					wRec.ResetsAt = &resetsAt.String
				}
				if t, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
					wRec.UpdatedAt = t
				}
				windows = append(windows, wRec)
			}
		}
	}

	// 2. Quota fetch states
	var fetchStates []QuotaFetchRecord
	fRows, err := h.db.QueryContext(r.Context(), `
		SELECT provider, last_attempt_at, next_attempt_at, last_success_at, last_status
		FROM quota_fetch_state;
	`)
	if err == nil {
		defer fRows.Close()
		for fRows.Next() {
			var fRec QuotaFetchRecord
			var lastSuccess sql.NullString
			if err := fRows.Scan(&fRec.Provider, &fRec.LastAttemptAt, &fRec.NextAttemptAt, &lastSuccess, &fRec.LastStatus); err == nil {
				if lastSuccess.Valid {
					fRec.LastSuccessAt = &lastSuccess.String
				}
				fetchStates = append(fetchStates, fRec)
			}
		}
	}

	// 3. Task spend summary
	var spend TaskSpendSummary
	row := h.db.QueryRowContext(r.Context(), `
		SELECT
			COALESCE(SUM(spent_usd), 0),
			COALESCE(SUM(spent_tokens), 0),
			COALESCE(SUM(spent_turns), 0),
			COALESCE(SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)
		FROM tasks;
	`)
	_ = row.Scan(&spend.TotalSpentUSD, &spend.TotalSpentTokens, &spend.TotalTurns, &spend.ActiveTasks, &spend.DoneTasks)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"timestamp":    time.Now().UTC(),
		"quota_pools":  windows,
		"fetch_states": fetchStates,
		"task_spend":   spend,
	})
}
