package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveHandoffWithManifestAndPruning(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-manifest-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	repoPath := "/Users/dev/cool-project"

	// Create 4 handoffs for the same repo with maxKeep = 3
	for i := 1; i <= 4; i++ {
		sessID := "session-test-" + string(rune('0'+i))
		manifest := HandoffManifest{
			SessionID:       sessID,
			Title:           "Feature Milestone " + string(rune('0'+i)),
			Goal:            "Implement core feature " + string(rune('0'+i)),
			RepoPath:        repoPath,
			RepoName:        "cool-project",
			GitBranch:       "main",
			AgentType:       "claude",
			CreatedAt:       time.Now().UTC().Add(time.Duration(i) * time.Minute),
			TotalUserTurns:  i * 2,
			DirectivesCount: i,
			Trigger:         "manual",
		}
		md := "# Handoff " + sessID

		_, err := SaveHandoffWithManifest(tempDir, manifest, md, 3)
		if err != nil {
			t.Fatalf("failed to save handoff %d: %v", i, err)
		}
	}

	handoffsDir := filepath.Join(tempDir, "handoffs")
	manifests, err := ListManifests(handoffsDir, repoPath)
	if err != nil {
		t.Fatalf("failed to list manifests: %v", err)
	}

	// Should be pruned to exactly 3!
	if len(manifests) != 3 {
		t.Fatalf("expected 3 manifests after pruning, got %d", len(manifests))
	}

	// Newest should be #4, oldest should be #2 (#1 was pruned)
	if manifests[0].SessionID != "session-test-4" {
		t.Errorf("expected newest to be session-test-4, got %s", manifests[0].SessionID)
	}
	if manifests[2].SessionID != "session-test-2" {
		t.Errorf("expected oldest remaining to be session-test-2, got %s", manifests[2].SessionID)
	}

	// Verify session-test-1 files are deleted
	if _, err := os.Stat(filepath.Join(handoffsDir, "session-test-1-manifest.json")); !os.IsNotExist(err) {
		t.Errorf("expected session-test-1-manifest.json to be deleted")
	}
	if _, err := os.Stat(filepath.Join(handoffsDir, "handoff-session-test-1.md")); !os.IsNotExist(err) {
		t.Errorf("expected handoff-session-test-1.md to be deleted")
	}

	// Search tests
	searchResults, err := SearchManifests(handoffsDir, repoPath, "Milestone 4")
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].SessionID != "session-test-4" {
		t.Errorf("search failed to find Milestone 4: %v", searchResults)
	}

	// Load Markdown test
	md, err := LoadHandoffMarkdown(handoffsDir, "session-test-4")
	if err != nil {
		t.Fatalf("LoadHandoffMarkdown error: %v", err)
	}
	if md != "# Handoff session-test-4" {
		t.Errorf("unexpected loaded markdown: %s", md)
	}
}

func TestAutoGenerateHandoffForSession(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-auto-handoff-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sessID := "crash-session-abc-123"
	repoPath := "/Users/dev/crash-project"

	manifest, err := AutoGenerateHandoffForSession(sessID, repoPath, "crash", nil, tempDir, 3)
	if err != nil {
		t.Fatalf("AutoGenerateHandoffForSession failed: %v", err)
	}

	if manifest == nil {
		t.Fatalf("expected non-nil manifest")
	}
	if manifest.SessionID != sessID {
		t.Errorf("expected session ID %s, got %s", sessID, manifest.SessionID)
	}
	if manifest.Trigger != "crash" {
		t.Errorf("expected trigger 'crash', got %s", manifest.Trigger)
	}

	handoffsDir := filepath.Join(tempDir, "handoffs")
	loaded, err := LoadManifest(handoffsDir, sessID)
	if err != nil {
		t.Fatalf("failed to load saved manifest: %v", err)
	}
	if loaded.Trigger != "crash" {
		t.Errorf("expected loaded trigger 'crash', got %s", loaded.Trigger)
	}

	md, err := LoadHandoffMarkdown(handoffsDir, sessID)
	if err != nil {
		t.Fatalf("failed to load saved handoff markdown: %v", err)
	}
	if !strings.Contains(md, "AGENT-MESH CONTEXT HANDOFF") {
		t.Errorf("missing handoff header in auto-generated markdown")
	}
}

