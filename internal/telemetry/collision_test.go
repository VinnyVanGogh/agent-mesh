package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VinnyVanGogh/agent-mesh/internal/db"
	_ "modernc.org/sqlite"
)

func TestCollisionDetection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-collision-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := db.Open(filepath.Join(tempDir, "mesh.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer store.Close()
	meshDB := store.DB()

	repo := "/Users/dev/agent-mesh"

	// 1. Session A (Claude Code) heartbeats
	sessA := AgentSession{
		ID:        "sess-claude-a",
		AgentType: "claude",
		RepoPath:  repo,
		GitBranch: "feature/auth",
		PID:       12345,
	}
	if err := HeartbeatSession(meshDB, sessA); err != nil {
		t.Fatalf("heartbeat A failed: %v", err)
	}

	// 2. Session A marks main.go and auth.go as working files
	if err := RecordWorkingFile(meshDB, sessA.ID, repo, "cmd/main.go", "write", 15*time.Minute); err != nil {
		t.Fatalf("record working file failed: %v", err)
	}
	if err := RecordWorkingFile(meshDB, sessA.ID, repo, "internal/auth/auth.go", "write", 15*time.Minute); err != nil {
		t.Fatalf("record working file failed: %v", err)
	}

	// 3. Session B (Antigravity) heartbeats
	sessB := AgentSession{
		ID:        "sess-gemini-b",
		AgentType: "gemini",
		RepoPath:  repo,
		GitBranch: "feature/auth",
		PID:       54321,
	}
	if err := HeartbeatSession(meshDB, sessB); err != nil {
		t.Fatalf("heartbeat B failed: %v", err)
	}

	// 4. Session B checks collisions before editing "cmd/main.go"
	collisions, err := CheckCollisions(meshDB, sessB.ID, repo, []string{"cmd/main.go"})
	if err != nil {
		t.Fatalf("check collisions failed: %v", err)
	}

	if len(collisions) != 1 {
		t.Fatalf("expected 1 collision for cmd/main.go, got %d", len(collisions))
	}
	if collisions[0].OtherSessionID != sessA.ID || collisions[0].FilePath != "cmd/main.go" {
		t.Errorf("collision mismatch: %+v", collisions[0])
	}

	// 5. Session B checks unrelated file "internal/wire/wire.go" -> no collision
	cleanCollisions, err := CheckCollisions(meshDB, sessB.ID, repo, []string{"internal/wire/wire.go"})
	if err != nil {
		t.Fatalf("check collisions failed: %v", err)
	}
	if len(cleanCollisions) != 0 {
		t.Errorf("expected 0 collisions, got %d", len(cleanCollisions))
	}

	// 6. Close session A -> collision should clear
	if err := CloseSession(meshDB, sessA.ID); err != nil {
		t.Fatalf("close session failed: %v", err)
	}
	postCloseCollisions, err := CheckCollisions(meshDB, sessB.ID, repo, []string{"cmd/main.go"})
	if err != nil {
		t.Fatalf("check collisions post-close failed: %v", err)
	}
	if len(postCloseCollisions) != 0 {
		t.Errorf("expected 0 collisions after session closed, got %d", len(postCloseCollisions))
	}
}
