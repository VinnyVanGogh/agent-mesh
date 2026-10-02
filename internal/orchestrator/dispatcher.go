package orchestrator

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// Dispatcher wakes agents without polling.
type Dispatcher struct {
	mu       sync.Mutex
	seenKeys map[string]time.Time
	OnWake   func(taskID string, reason string)
	wg       sync.WaitGroup // tracks in-flight OnWake goroutines
}

var GlobalDispatcher *Dispatcher

func init() {
	GlobalDispatcher = NewDispatcher()
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		seenKeys: make(map[string]time.Time),
	}
}

// Wake triggers an agent to wake up for a specific task.
// idempotencyKey ensures we don't wake multiple times for the same event.
func (d *Dispatcher) Wake(taskID, reason, idempotencyKey string) {
	if idempotencyKey != "" {
		d.mu.Lock()
		// Clean up old keys periodically (lazy)
		if len(d.seenKeys) > 1000 {
			now := time.Now()
			for k, v := range d.seenKeys {
				if now.Sub(v) > 24*time.Hour {
					delete(d.seenKeys, k)
				}
			}
		}

		if _, ok := d.seenKeys[idempotencyKey]; ok {
			d.mu.Unlock()
			slog.Debug("wake dispatcher: ignored duplicate event", slog.String("key", idempotencyKey), slog.String("task", taskID))
			return
		}
		d.seenKeys[idempotencyKey] = time.Now()
		d.mu.Unlock()
	}

	slog.Info("wake dispatcher: waking agent", slog.String("task", taskID), slog.String("reason", reason))
	if d.OnWake != nil {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.OnWake(taskID, reason)
		}()
	}
}

// Drain blocks until all in-flight OnWake goroutines have returned.
// Call during daemon shutdown to avoid killing in-progress harness runs.
func (d *Dispatcher) Drain() {
	d.wg.Wait()
}

// RecoveryScan runs once at daemon start to reset stale claims.
// A claim is stale if execution_stage = 'in_progress' but the daemon just started,
// since it's a single binary and all previous sessions are dead.
func RecoveryScan(ctx context.Context, dbConn *sql.DB) error {
	slog.Info("running startup recovery scan for stale task claims")

	// 1. Mark all active agent sessions as closed since the daemon restarted.
	_, err := dbConn.ExecContext(ctx, "UPDATE agent_sessions SET status = 'closed' WHERE status = 'active'")
	if err != nil {
		return err
	}

	// 2. Reset tasks that were 'in_progress' back to 'todo' and clear their checkout_run_id.
	res, err := dbConn.ExecContext(ctx, "UPDATE tasks SET execution_stage = 'todo', checkout_run_id = NULL, checkout_agent_id = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE execution_stage = 'in_progress'")
	if err != nil {
		return err
	}

	affected, _ := res.RowsAffected()
	if affected > 0 {
		slog.Info("recovered stale claims", slog.Int64("count", affected))
	}

	// 3. Reset capped tasks to todo so they can be retried.
	resCapped, err := dbConn.ExecContext(ctx,
		"UPDATE tasks SET execution_stage='todo', checkout_run_id=NULL, checkout_agent_id=NULL, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE execution_stage='capped'")
	if err != nil {
		return err
	}
	affectedCapped, _ := resCapped.RowsAffected()
	if affectedCapped > 0 {
		slog.Info("recovered capped tasks", slog.Int64("count", affectedCapped))
	}
	return nil
}
