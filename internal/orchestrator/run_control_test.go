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
