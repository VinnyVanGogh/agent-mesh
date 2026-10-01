package checklist_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
)

func TestResolveItemCommit(t *testing.T) {
	// 1. Explicit item commit hash takes precedence
	it1 := checklist.Item{
		Section:    "02. Table Sorting & Deep Content Search (STA-191)",
		Contract:   `{"type":"command","command":"echo hi","commit_hash":"1111111"}`,
		CommitHash: "2222222",
	}
	if got := checklist.ResolveItemCommit(it1); got != "2222222" {
		t.Fatalf("expected 2222222, got %q", got)
	}

	// 2. Fall back to contract JSON commit_hash
	it2 := checklist.Item{
		Section:  "02. Table Sorting & Deep Content Search (STA-191)",
		Contract: `{"type":"command","command":"echo hi","commit_hash":"1111111"}`,
	}
	if got := checklist.ResolveItemCommit(it2); got != "1111111" {
		t.Fatalf("expected 1111111, got %q", got)
	}

	// 3. Fall back to section mapping
	it3 := checklist.Item{
		Section: "02. Table Sorting & Deep Content Search (STA-191)",
	}
	if got := checklist.ResolveItemCommit(it3); got != "4697e36" {
		t.Fatalf("expected 4697e36, got %q", got)
	}
}

func setupTestGitRepo(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	// Commit 1 on main
	exec.Command("touch", filepath.Join(dir, "f1")).Run()
	run("add", ".")
	run("commit", "-m", "commit 1 on main")
	c1 := run("rev-parse", "--short", "HEAD")

	// Commit 2 on branch (unmerged)
	run("checkout", "-b", "feature-branch")
	exec.Command("touch", filepath.Join(dir, "f2")).Run()
	run("add", ".")
	run("commit", "-m", "commit 2 on branch")
	c2 := run("rev-parse", "--short", "HEAD")

	// Commit 3 on main
	run("checkout", "main")
	exec.Command("touch", filepath.Join(dir, "f3")).Run()
	run("add", ".")
	run("commit", "-m", "commit 3 on main")
	_ = run("rev-parse", "--short", "HEAD")

	return dir, c1, c2 // dir, merged c1, unmerged c2 (c3 is main HEAD)
}

func TestVerifyCommits(t *testing.T) {
	ctx := context.Background()
	repoDir, c1, c2 := setupTestGitRepo(t)

	// Case 1: Commit c1 is in main and binary is at c1 -> All verified
	items1 := []checklist.Item{
		{
			ID:         "item-1",
			Section:    "Section A",
			Title:      "Feature 1",
			CommitHash: c1,
		},
	}
	res1, err := checklist.VerifyCommits(ctx, repoDir, c1, items1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res1.Verified {
		t.Fatalf("expected res1 to be verified, got: %+v", res1)
	}
	if len(res1.MissingCommits) != 0 || len(res1.BlockedItemIDs) != 0 {
		t.Fatalf("expected zero missing commits, got: %+v", res1)
	}

	// Case 2: Commit c2 is on an unmerged branch -> Missing from main
	items2 := []checklist.Item{
		{
			ID:         "item-2",
			Section:    "Section B",
			Title:      "Unmerged Feature",
			CommitHash: c2,
		},
	}
	res2, err := checklist.VerifyCommits(ctx, repoDir, c1, items2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.Verified {
		t.Fatal("expected res2 to NOT be verified")
	}
	if len(res2.MissingCommits) != 1 || !res2.MissingCommits[0].MissingFromMain {
		t.Fatalf("expected missing from main, got: %+v", res2.MissingCommits)
	}
	if len(res2.BlockedItemIDs) != 1 || res2.BlockedItemIDs[0] != "item-2" {
		t.Fatalf("expected item-2 to be blocked, got: %+v", res2.BlockedItemIDs)
	}

	// Case 3: Binary is 'none' / unrebuilt -> Missing from binary
	res3, err := checklist.VerifyCommits(ctx, repoDir, "none", items1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res3.Verified {
		t.Fatal("expected res3 to fail when binary commit is 'none'")
	}
	if len(res3.MissingCommits) != 1 || !res3.MissingCommits[0].MissingFromBinary {
		t.Fatalf("expected missing from binary, got: %+v", res3.MissingCommits)
	}

	// Case 4: Nonexistent commit
	items4 := []checklist.Item{
		{
			ID:         "item-4",
			Section:    "Section D",
			Title:      "Bogus Commit",
			CommitHash: "0000000",
		},
	}
	res4, err := checklist.VerifyCommits(ctx, repoDir, c1, items4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res4.Verified {
		t.Fatal("expected res4 to fail for nonexistent commit")
	}
	if len(res4.MissingCommits) != 1 || !strings.Contains(res4.MissingCommits[0].Reason, "does not exist") {
		t.Fatalf("expected nonexistent reason, got: %+v", res4.MissingCommits)
	}
}
