package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
)

func TestRunControl_PauseResume(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/rc.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	conn := store.DB()

	rc := NewRunControl(conn)
	const taskID = "task-pause-test"

	// Seed tasks row so FK passes.
	_, err = conn.Exec(
		`INSERT INTO tasks (id, name, repo_path) VALUES (?, 'test', '/tmp')`, taskID,
	)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	rc.ClearForRun(taskID)

	if rc.IsPaused(taskID) {
		t.Fatal("should not be paused after clear")
	}

	if err := rc.SetPause(taskID, true); err != nil {
		t.Fatalf("SetPause: %v", err)
	}
	if !rc.IsPaused(taskID) {
		t.Fatal("should be paused after SetPause(true)")
	}

	// WaitForResume should unblock when resumed via goroutine.
	done := make(chan bool)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopped := rc.WaitForResume(ctx, taskID)
		done <- stopped
	}()

	time.Sleep(50 * time.Millisecond)
	if err := rc.SetPause(taskID, false); err != nil {
		t.Fatalf("SetPause resume: %v", err)
	}

	select {
	case stopped := <-done:
		if stopped {
			t.Fatal("WaitForResume should return false (resumed), not true (stopped)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForResume did not unblock after resume")
	}
}

func TestRunControl_Stop(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/rc_stop.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	conn := store.DB()
	rc := NewRunControl(conn)
	const taskID = "task-stop-test"

	_, _ = conn.Exec(`INSERT INTO tasks (id, name, repo_path) VALUES (?, 'test', '/tmp')`, taskID)
	rc.ClearForRun(taskID)

	// SetPause(true) then SetStop — WaitForResume should return stopped=true.
	if err := rc.SetPause(taskID, true); err != nil {
		t.Fatalf("SetPause: %v", err)
	}

	done := make(chan bool)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- rc.WaitForResume(ctx, taskID)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := rc.SetStop(taskID); err != nil {
		t.Fatalf("SetStop: %v", err)
	}

	select {
	case stopped := <-done:
		if !stopped {
			t.Fatal("WaitForResume should return true (stopped) after SetStop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForResume did not unblock after SetStop")
	}
}

// TestStopChan_PauseDoesNotCancel is the STA-458 regression test.
// StopChan must only fire on SetStop, never on SetPause.
// Before the fix, SetPause closed the shared notify channel (which StopChan
// returned), so the harness turn goroutine cancelled the adapter context on
// pause rather than letting the current turn finish.
func TestStopChan_PauseDoesNotCancel(t *testing.T) {
	rc := NewRunControl(nil) // channel behaviour is independent of DB
	const taskID = "task-stopchan-test"

	// Simulate the harness turn goroutine: capture the stop channel, watch it,
	// and record whether it fired.
	stopCh := rc.StopChan(taskID)

	adapterCancelled := make(chan struct{})
	turnCtx, turnCancel := context.WithCancel(context.Background())
	defer turnCancel()

	go func() {
		select {
		case <-turnCtx.Done():
		case <-stopCh:
			turnCancel()
			close(adapterCancelled)
		}
	}()

	// SetPause must NOT close StopChan — the running turn should continue.
	_ = rc.SetPause(taskID, true)
	select {
	case <-adapterCancelled:
		t.Fatal("STA-458 regression: SetPause must not cancel the adapter turn context")
	case <-time.After(150 * time.Millisecond):
		// expected: turn context still live
	}

	// SetStop MUST close StopChan — the running turn should be cancelled.
	_ = rc.SetStop(taskID)
	select {
	case <-adapterCancelled:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SetStop must cancel the adapter turn context via StopChan")
	}

	if turnCtx.Err() == nil {
		t.Fatal("turn context should be cancelled after SetStop")
	}
}

func TestRunControl_InjectMessage(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/rc_msg.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	conn := store.DB()
	rc := NewRunControl(conn)
	const taskID = "task-msg-test"

	_, _ = conn.Exec(`INSERT INTO tasks (id, name, repo_path) VALUES (?, 'test', '/tmp')`, taskID)

	if err := rc.InjectMessage(taskID, "hello agent"); err != nil {
		t.Fatalf("InjectMessage: %v", err)
	}
	if err := rc.InjectMessage(taskID, "second message"); err != nil {
		t.Fatalf("InjectMessage 2: %v", err)
	}

	msgs := rc.DequeuePendingMessages(taskID)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0] != "hello agent" || msgs[1] != "second message" {
		t.Fatalf("unexpected messages: %v", msgs)
	}

	// Second dequeue should return empty.
	msgs2 := rc.DequeuePendingMessages(taskID)
	if len(msgs2) != 0 {
		t.Fatalf("expected empty after dequeue, got %d", len(msgs2))
	}
}
