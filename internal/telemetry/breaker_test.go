package telemetry

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	_ "modernc.org/sqlite"
)

func setupTestMeshDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "mesh-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tempDir, "mesh.db")
	store, err := db.Open(dbPath)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to open mesh db: %v", err)
	}

	return store.DB(), func() {
		store.Close()
		os.RemoveAll(tempDir)
	}
}

func TestCircuitBreakerConsecutiveLoop(t *testing.T) {
	meshDB, cleanup := setupTestMeshDB(t)
	defer cleanup()

	tracker := NewBreakerTracker()
	sessionID := "sess-loop-1"
	repoPath := "/Users/dev/project"
	tool := "run_command"
	cmd := "npm run build"
	errText := "Error: TS2307: Cannot find module '@types/node'"

	// First failure - should not trip
	tripped, _, err := tracker.RecordFailure(meshDB, sessionID, repoPath, "claude", tool, cmd, errText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tripped {
		t.Errorf("expected not tripped on 1st failure")
	}

	// Second failure - should not trip
	tripped, _, err = tracker.RecordFailure(meshDB, sessionID, repoPath, "claude", tool, cmd, errText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tripped {
		t.Errorf("expected not tripped on 2nd failure")
	}

	// Third identical failure - MUST TRIP!
	tripped, reason, err := tracker.RecordFailure(meshDB, sessionID, repoPath, "claude", tool, cmd, errText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tripped {
		t.Fatalf("expected breaker to trip on 3rd identical failure")
	}
	if reason == "" {
		t.Errorf("expected trip reason")
	}

	// Verify persistence in DB
	cb, err := GetCircuitBreaker(meshDB, sessionID)
	if err != nil {
		t.Fatalf("failed to query breaker: %v", err)
	}
	if cb == nil || !cb.IsTripped {
		t.Fatalf("expected circuit breaker record in DB to be tripped")
	}
	if cb.FailingTool != tool || cb.TripCount != 1 {
		t.Errorf("mismatch cb fields: tool=%s tripCount=%d", cb.FailingTool, cb.TripCount)
	}

	// Reset breaker
	if err := tracker.ResetCircuitBreaker(meshDB, sessionID); err != nil {
		t.Fatalf("failed to reset breaker: %v", err)
	}

	cbAfter, err := GetCircuitBreaker(meshDB, sessionID)
	if err != nil || cbAfter == nil {
		t.Fatalf("failed to query breaker after reset: %v", err)
	}
	if cbAfter.IsTripped {
		t.Errorf("expected breaker to be untripped after reset")
	}
}

func TestCircuitBreakerBurstSpiral(t *testing.T) {
	meshDB, cleanup := setupTestMeshDB(t)
	defer cleanup()

	tracker := NewBreakerTracker()
	sessionID := "sess-burst-1"
	repoPath := "/Users/dev/project"

	// Trigger 5 different failures
	var tripped bool
	for i := 1; i <= 5; i++ {
		var err error
		tripped, _, err = tracker.RecordFailure(meshDB, sessionID, repoPath, "gemini", "bash", "cmd-"+string(rune('a'+i)), "err")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if !tripped {
		t.Errorf("expected breaker to trip on 5-failure burst")
	}

	list, err := ListCircuitBreakers(meshDB, repoPath, true)
	if err != nil {
		t.Fatalf("failed to list circuit breakers: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 active circuit breaker, got %d", len(list))
	}
}

func TestCircuitBreakerDistinguishVariedFromIdentical(t *testing.T) {
	meshDB, cleanup := setupTestMeshDB(t)
	defer cleanup()

	tracker := NewBreakerTracker()
	sessionID := "sess-distinguish-1"
	repoPath := "/Users/dev/project"

	// 1. Send 4 distinct, varied errors.
	// Consecutive identical threshold is 3, but because each failure has a different tool/command,
	// none of the first 4 failures must trip the breaker.
	variedFailures := []struct {
		tool string
		cmd  string
		err  string
	}{
		{"git", "git status", "fatal: not a git repo"},
		{"npm", "npm test", "ERR! missing script: test"},
		{"curl", "curl localhost:8080", "connection refused"},
		{"python", "python3 main.py", "ModuleNotFoundError: no module named 'flask'"},
	}

	for i, f := range variedFailures {
		tripped, reason, err := tracker.RecordFailure(meshDB, sessionID, repoPath, "claude", f.tool, f.cmd, f.err)
		if err != nil {
			t.Fatalf("failure %d encountered unexpected error: %v", i+1, err)
		}
		if tripped {
			t.Fatalf("failure %d (varied error) tripped the breaker unexpectedly: %s", i+1, reason)
		}
	}

	// 2. The 5th varied failure hits the rolling 5-minute threshold and MUST trip.
	tripped, reason, err := tracker.RecordFailure(meshDB, sessionID, repoPath, "claude", "go", "go build ./...", "cannot find package")
	if err != nil {
		t.Fatalf("5th failure error: %v", err)
	}
	if !tripped {
		t.Fatalf("expected 5th varied error to trip rolling 5-minute spiral breaker")
	}
	if !strings.Contains(reason, "Failure spiral: 5 errors") {
		t.Errorf("expected reason to cite 'Failure spiral: 5 errors', got: %s", reason)
	}

	// 3. Reset the breaker and verify consecutive identical failures trip at 3 (not 5).
	if err := tracker.ResetCircuitBreaker(meshDB, sessionID); err != nil {
		t.Fatalf("failed to reset breaker: %v", err)
	}

	// Create a new session for clean testing
	sess2 := "sess-identical-1"
	tripped, _, _ = tracker.RecordFailure(meshDB, sess2, repoPath, "claude", "cargo", "cargo build", "error[E0432]")
	if tripped {
		t.Fatalf("1st identical failure should not trip")
	}
	tripped, _, _ = tracker.RecordFailure(meshDB, sess2, repoPath, "claude", "cargo", "cargo build", "error[E0432]")
	if tripped {
		t.Fatalf("2nd identical failure should not trip")
	}
	// 3rd identical failure MUST trip immediately
	tripped, reason2, _ := tracker.RecordFailure(meshDB, sess2, repoPath, "claude", "cargo", "cargo build", "error[E0432]")
	if !tripped {
		t.Fatalf("3rd identical failure must trip consecutive failure breaker")
	}
	if !strings.Contains(reason2, "Repeating failure loop: 3 consecutive") {
		t.Errorf("expected reason to cite 'Repeating failure loop: 3 consecutive', got: %s", reason2)
	}
}
