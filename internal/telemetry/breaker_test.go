package telemetry

import (
	"database/sql"
	"os"
	"path/filepath"
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
