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

	// Verify cool-project subdirectory was created
	repoSubdir := filepath.Join(handoffsDir, "cool-project")
	if fi, err := os.Stat(repoSubdir); err != nil || !fi.IsDir() {
		t.Fatalf("expected repo subdirectory %s to exist", repoSubdir)
	}

	// Verify latest.md and latest.json symlinks exist and resolve to session-test-4
	latestMd := filepath.Join(repoSubdir, "latest.md")
	latestJson := filepath.Join(repoSubdir, "latest.json")
	if target, err := os.Readlink(latestMd); err != nil || !strings.HasPrefix(target, "session-test-4") {
		t.Errorf("expected latest.md to resolve to session-test-4, got %s (err: %v)", target, err)
	}
	if target, err := os.Readlink(latestJson); err != nil || !strings.HasPrefix(target, "session-test-4") {
		t.Errorf("expected latest.json to resolve to session-test-4, got %s (err: %v)", target, err)
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

func TestManualPinningAndSymlinks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-pinning-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	repoPath := "/Users/dev/auth-service"

	// 1. User creates a deliberate manual handoff
	manualManifest := HandoffManifest{
		SessionID:       "manual-session-important",
		Title:           "Critical Auth Architecture",
		Goal:            "Preserve OAuth2 PKCE design",
		RepoPath:        repoPath,
		RepoName:        "auth-service",
		GitBranch:       "feat/oauth",
		AgentType:       "claude",
		CreatedAt:       time.Now().UTC().Add(-10 * time.Minute),
		Trigger:         "manual",
	}
	_, err = SaveHandoffWithManifest(tempDir, manualManifest, "# Important Manual Handoff", 3)
	if err != nil {
		t.Fatalf("failed to save manual handoff: %v", err)
	}

	// 2. Automated daemon records 3 background turns
	for i := 1; i <= 3; i++ {
		autoManifest := HandoffManifest{
			SessionID:       "auto-session-" + string(rune('0'+i)),
			Title:           "Auto turn " + string(rune('0'+i)),
			Goal:            "Background execution",
			RepoPath:        repoPath,
			RepoName:        "auth-service",
			GitBranch:       "feat/oauth",
			AgentType:       "gemini",
			CreatedAt:       time.Now().UTC().Add(time.Duration(i) * time.Minute),
			Trigger:         "auto_daemon",
		}
		_, err := SaveHandoffWithManifest(tempDir, autoManifest, "# Auto Turn", 3)
		if err != nil {
			t.Fatalf("failed to save auto turn %d: %v", i, err)
		}
	}

	handoffsDir := filepath.Join(tempDir, "handoffs")
	manifests, err := ListManifests(handoffsDir, repoPath)
	if err != nil {
		t.Fatalf("failed to list manifests: %v", err)
	}

	// Total should be max 3
	if len(manifests) != 3 {
		t.Fatalf("expected 3 manifests, got %d", len(manifests))
	}

	// CRITICAL CHECK: manual-session-important MUST STILL BE PRESENT because auto_daemon was pruned first!
	foundManual := false
	for _, m := range manifests {
		if m.SessionID == "manual-session-important" {
			foundManual = true
			break
		}
	}
	if !foundManual {
		t.Errorf("manual handoff was evicted by auto_daemon snapshots! Manual pinning failed.")
	}
}

func TestListBranchesWithHandoffs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-branches-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	repoPath := "/Users/dev/multi-branch-repo"

	// 2 handoffs on main
	_, _ = SaveHandoffWithManifest(tempDir, HandoffManifest{
		SessionID: "sess-main-1",
		Goal:      "Main branch stability",
		RepoPath:  repoPath,
		RepoName:  "multi-branch-repo",
		GitBranch: "main",
		CreatedAt: time.Now().UTC().Add(-20 * time.Minute),
	}, "# Main 1", 5)

	_, _ = SaveHandoffWithManifest(tempDir, HandoffManifest{
		SessionID: "sess-main-2",
		Goal:      "Main release v1.0",
		RepoPath:  repoPath,
		RepoName:  "multi-branch-repo",
		GitBranch: "main",
		CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
	}, "# Main 2", 5)

	// 1 handoff on feat/billing
	_, _ = SaveHandoffWithManifest(tempDir, HandoffManifest{
		SessionID: "sess-billing-1",
		Goal:      "Stripe checkout flow",
		RepoPath:  repoPath,
		RepoName:  "multi-branch-repo",
		GitBranch: "feat/billing",
		CreatedAt: time.Now().UTC().Add(-5 * time.Minute),
	}, "# Billing 1", 5)

	handoffsDir := filepath.Join(tempDir, "handoffs")
	summaries, err := ListBranchesWithHandoffs(handoffsDir, repoPath)
	if err != nil {
		t.Fatalf("ListBranchesWithHandoffs failed: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("expected 2 branch summaries, got %d", len(summaries))
	}

	// feat/billing was updated most recently (-5m vs -10m)
	if summaries[0].Branch != "feat/billing" {
		t.Errorf("expected newest active branch to be feat/billing, got %s", summaries[0].Branch)
	}
	if summaries[0].HandoffCount != 1 {
		t.Errorf("expected 1 handoff for billing, got %d", summaries[0].HandoffCount)
	}

	if summaries[1].Branch != "main" {
		t.Errorf("expected second branch to be main, got %s", summaries[1].Branch)
	}
	if summaries[1].HandoffCount != 2 {
		t.Errorf("expected 2 handoffs for main, got %d", summaries[1].HandoffCount)
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
