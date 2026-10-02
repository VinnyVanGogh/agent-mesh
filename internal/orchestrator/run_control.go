package orchestrator

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// GlobalRunControl is the process-wide RunControl instance, shared between the
// harness (which reads flags) and HTTP handlers (which write flags).
var GlobalRunControl *RunControl

func init() {
	GlobalRunControl = NewRunControl(nil)
}

// RunControl holds per-task pause/stop flags and a pending-message queue.
// It is safe for concurrent use.
type RunControl struct {
	db *sql.DB

	mu         sync.Mutex
	notify     map[string]chan struct{} // closed + replaced on any state change (pause, resume, stop)
	stopNotify map[string]chan struct{} // closed + replaced only on SetStop
}

// NewRunControl creates a RunControl. db may be nil (no persistence).
func NewRunControl(db *sql.DB) *RunControl {
	return &RunControl{
		db:         db,
		notify:     make(map[string]chan struct{}),
		stopNotify: make(map[string]chan struct{}),
	}
}

// SetDB wires up the DB after the store is open.
func (rc *RunControl) SetDB(db *sql.DB) {
	rc.mu.Lock()
	rc.db = db
	rc.mu.Unlock()
}

// ClearForRun resets all flags for a task at run start so stale signals don't
// bleed into the new run.
func (rc *RunControl) ClearForRun(taskID string) {
	if rc.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = rc.db.ExecContext(ctx,
		`INSERT INTO run_control (task_id, pause_after_step, stop_requested)
		 VALUES (?, 0, 0)
		 ON CONFLICT(task_id) DO UPDATE SET pause_after_step=0, stop_requested=0,
		     updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		taskID,
	)
	_, _ = rc.db.ExecContext(ctx,
		`DELETE FROM run_pending_messages WHERE task_id=?`, taskID,
	)
}

// SetPause sets or clears the pause-after-step flag. Signals all waiters.
func (rc *RunControl) SetPause(taskID string, paused bool) error {
	if rc.db != nil {
		v := 0
		if paused {
			v = 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := rc.db.ExecContext(ctx,
			`INSERT INTO run_control (task_id, pause_after_step, stop_requested)
			 VALUES (?, ?, 0)
			 ON CONFLICT(task_id) DO UPDATE SET pause_after_step=?,
			     updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
			taskID, v, v,
		)
		if err != nil {
			return err
		}
	}
	rc.signal(taskID)
	return nil
}

// SetStop marks the task for immediate stop. Signals all waiters.
func (rc *RunControl) SetStop(taskID string) error {
	if rc.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := rc.db.ExecContext(ctx,
			`INSERT INTO run_control (task_id, pause_after_step, stop_requested)
			 VALUES (?, 0, 1)
			 ON CONFLICT(task_id) DO UPDATE SET stop_requested=1,
			     updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
			taskID,
		)
		if err != nil {
			return err
		}
	}
	rc.signal(taskID)
	rc.signalStop(taskID)
	return nil
}

// InjectMessage appends a message to the pending queue for the task.
func (rc *RunControl) InjectMessage(taskID, text string) error {
	if rc.db == nil || text == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := rc.db.ExecContext(ctx,
		`INSERT INTO run_pending_messages (task_id, message) VALUES (?, ?)`,
		taskID, text,
	)
	return err
}

// DequeuePendingMessages atomically reads and removes all pending messages.
func (rc *RunControl) DequeuePendingMessages(taskID string) []string {
	if rc.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := rc.db.QueryContext(ctx,
		`SELECT id, message FROM run_pending_messages WHERE task_id=? ORDER BY id`, taskID,
	)
	if err != nil {
		slog.Warn("run_control: dequeue query failed", slog.Any("err", err))
		return nil
	}
	defer rows.Close()

	var ids []int64
	var msgs []string
	for rows.Next() {
		var id int64
		var msg string
		if err := rows.Scan(&id, &msg); err == nil {
			ids = append(ids, id)
			msgs = append(msgs, msg)
		}
	}

	for _, id := range ids {
		_, _ = rc.db.ExecContext(ctx,
			`DELETE FROM run_pending_messages WHERE id=?`, id,
		)
	}
	return msgs
}

// IsPaused reports whether the pause-after-step flag is set.
func (rc *RunControl) IsPaused(taskID string) bool {
	p, _ := rc.getFlags(taskID)
	return p
}

// IsStopRequested reports whether the stop flag is set.
func (rc *RunControl) IsStopRequested(taskID string) bool {
	_, s := rc.getFlags(taskID)
	return s
}

// StopChan returns a channel that is closed only when stop is requested.
// Pause signals do NOT close this channel. Callers should capture it once and
// select on it alongside turnCtx.Done() to cancel a turn only on hard stop.
func (rc *RunControl) StopChan(taskID string) <-chan struct{} {
	return rc.chanStopFor(taskID)
}

// WaitForResume blocks until the pause flag is cleared or stop is requested.
// Returns true if stopped, false if resumed.
func (rc *RunControl) WaitForResume(ctx context.Context, taskID string) bool {
	for {
		ch := rc.chanFor(taskID)
		select {
		case <-ctx.Done():
			return true
		case <-ch:
			_, stop := rc.getFlags(taskID)
			if stop {
				return true
			}
			pause, _ := rc.getFlags(taskID)
			if !pause {
				return false
			}
			// Still paused — wait on next signal.
		}
	}
}

// getFlags reads pause and stop flags from the DB.
func (rc *RunControl) getFlags(taskID string) (pause, stop bool) {
	if rc.db == nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var p, s int
	if err := rc.db.QueryRowContext(ctx,
		`SELECT pause_after_step, stop_requested FROM run_control WHERE task_id=?`, taskID,
	).Scan(&p, &s); err != nil {
		return false, false
	}
	return p != 0, s != 0
}

// chanFor returns (or creates) the current notification channel for a task.
func (rc *RunControl) chanFor(taskID string) <-chan struct{} {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.notify[taskID] == nil {
		rc.notify[taskID] = make(chan struct{})
	}
	return rc.notify[taskID]
}

// chanStopFor returns (or creates) the stop-only notification channel for a task.
func (rc *RunControl) chanStopFor(taskID string) <-chan struct{} {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.stopNotify[taskID] == nil {
		rc.stopNotify[taskID] = make(chan struct{})
	}
	return rc.stopNotify[taskID]
}

// signal closes the current notify channel for the task (waking all waiters) and
// creates a fresh one for the next wait.
func (rc *RunControl) signal(taskID string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if ch := rc.notify[taskID]; ch != nil {
		close(ch)
	}
	rc.notify[taskID] = make(chan struct{})
}

// signalStop closes the stop-only channel, waking any turn goroutine watching for
// hard-stop. Called exclusively by SetStop so pause does not trigger it.
func (rc *RunControl) signalStop(taskID string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if ch := rc.stopNotify[taskID]; ch != nil {
		close(ch)
	}
	rc.stopNotify[taskID] = make(chan struct{})
}
