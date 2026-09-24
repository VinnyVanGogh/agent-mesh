package wire

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
)

func setupTestDB(t *testing.T) *db.Store {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mesh.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return store
}

func TestWirePostAndGetUnread(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// 1. Post 3 messages: 2 global, 1 repo-scoped
	m1, err := Post(store.DB(), "global", "claude", "", "Starting refactor of auth module", 3600)
	if err != nil {
		t.Fatalf("failed to post m1: %v", err)
	}
	if m1.ID == 0 {
		t.Errorf("expected valid ID")
	}

	_, err = Post(store.DB(), "review", "agy", "/repo/sub", "PR #12 is ready for review", 3600)
	if err != nil {
		t.Fatalf("failed to post m2: %v", err)
	}

	_, err = Post(store.DB(), "global", "claude", "", "Auth module refactored successfully", 3600)
	if err != nil {
		t.Fatalf("failed to post m3: %v", err)
	}

	// 2. Consumer A in /repo/sub: should see all 3 messages
	unreadsA, err := GetUnread(store.DB(), "session-A", "/repo/sub")
	if err != nil {
		t.Fatalf("failed to get unread for session-A: %v", err)
	}
	if len(unreadsA) != 3 {
		t.Errorf("expected 3 unread messages for session-A, got %d", len(unreadsA))
	}

	// 3. Second call for Consumer A: watermark advanced, should see 0 messages
	unreadsA2, err := GetUnread(store.DB(), "session-A", "/repo/sub")
	if err != nil {
		t.Fatalf("failed to get second unread for session-A: %v", err)
	}
	if len(unreadsA2) != 0 {
		t.Errorf("expected 0 unread messages for session-A after watermark, got %d", len(unreadsA2))
	}

	// 4. Consumer B in /other/repo: should only see the 2 global messages (not /repo/sub)
	unreadsB, err := GetUnread(store.DB(), "session-B", "/other/repo")
	if err != nil {
		t.Fatalf("failed to get unread for session-B: %v", err)
	}
	if len(unreadsB) != 2 {
		t.Errorf("expected 2 unread messages for session-B, got %d", len(unreadsB))
	}

	// 5. Test List
	list, err := List(store.DB(), "global", 10)
	if err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 global messages in list, got %d", len(list))
	}
}

func TestWirePrune(t *testing.T) {
	store := setupTestDB(t)
	defer store.Close()

	// Post message with 1 second TTL
	_, err := Post(store.DB(), "temp", "agent", "", "Ephemeral message", 1)
	if err != nil {
		t.Fatalf("failed to post message: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	pruned, err := Prune(store.DB())
	if err != nil {
		t.Fatalf("failed to prune messages: %v", err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 pruned message, got %d", pruned)
	}
}
