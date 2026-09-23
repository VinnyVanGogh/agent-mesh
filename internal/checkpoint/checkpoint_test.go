package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestGitRepo(t *testing.T) string {
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
		}
	}

	run("init")
	run("config", "user.email", "test@agentmesh.dev")
	run("config", "user.name", "Agent Mesh Test")

	// Create initial file & commit
	fileA := filepath.Join(dir, "file_a.txt")
	if err := os.WriteFile(fileA, []byte("version 1"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "file_a.txt")
	run("commit", "-m", "initial commit")

	return dir
}

func TestCheckpointAndUndo(t *testing.T) {
	dir := setupTestGitRepo(t)
	ctx := context.Background()

	// 1. Modify file_a.txt and create untracked file_b.txt
	fileA := filepath.Join(dir, "file_a.txt")
	fileB := filepath.Join(dir, "file_b.txt")
	if err := os.WriteFile(fileA, []byte("version 2 (modified)"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("untracked content"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Capture baseline HEAD commit
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	headOut, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	headSHA := strings.TrimSpace(string(headOut))

	// 3. Create Checkpoint
	cp, err := CreateCheckpoint(ctx, CreateOptions{
		WorkDir:   dir,
		SessionID: "test-sess",
		Message:   "checkpoint before experiment",
	})
	if err != nil {
		t.Fatalf("CreateCheckpoint failed: %v", err)
	}

	if cp.CommitSHA == "" {
		t.Errorf("expected valid commit SHA, got empty")
	}

	// Assert HEAD did NOT change
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	headAfter, _ := cmd.Output()
	if strings.TrimSpace(string(headAfter)) != headSHA {
		t.Errorf("HEAD moved! Expected %s, got %s", headSHA, string(headAfter))
	}

	// 4. Corrupt working tree: modify fileA, delete fileB, create garbage fileC
	fileC := filepath.Join(dir, "file_c.txt")
	_ = os.WriteFile(fileA, []byte("corrupted version 3"), 0644)
	_ = os.Remove(fileB)
	_ = os.WriteFile(fileC, []byte("garbage untracked file"), 0644)

	// 5. Test Dry-Run Undo
	dryRes, err := Undo(ctx, UndoOptions{
		WorkDir:      dir,
		CheckpointID: cp.ID,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry run undo failed: %v", err)
	}
	if len(dryRes.FilesReverted) == 0 {
		t.Errorf("expected dry-run to detect file_a.txt modification")
	}

	// 6. Execute Actual Undo
	undoRes, err := Undo(ctx, UndoOptions{
		WorkDir:      dir,
		CheckpointID: cp.ID,
	})
	if err != nil {
		t.Fatalf("Undo failed: %v", err)
	}

	if undoRes.SafetyCP == nil {
		t.Errorf("expected pre-undo safety checkpoint to be created")
	}

	// Verify fileA restored to "version 2 (modified)"
	contentA, _ := os.ReadFile(fileA)
	if string(contentA) != "version 2 (modified)" {
		t.Errorf("expected file_a to be 'version 2 (modified)', got %s", string(contentA))
	}

	// Verify fileB restored
	contentB, err := os.ReadFile(fileB)
	if err != nil || string(contentB) != "untracked content" {
		t.Errorf("expected file_b to be restored, err: %v, content: %s", err, string(contentB))
	}

	// Verify garbage fileC removed
	if _, err := os.Stat(fileC); !os.IsNotExist(err) {
		t.Errorf("expected file_c to be cleaned up, but it still exists")
	}

	// 7. Test Redo
	redoRes, err := Redo(ctx, dir, "test-sess")
	if err != nil {
		t.Fatalf("Redo failed: %v", err)
	}
	if redoRes == nil {
		t.Fatal("expected redo result")
	}

	// Verify fileA is back to corrupted version 3
	contentRedoA, _ := os.ReadFile(fileA)
	if string(contentRedoA) != "corrupted version 3" {
		t.Errorf("expected redo to restore 'corrupted version 3', got %s", string(contentRedoA))
	}
}

func TestListAndPruneCheckpoints(t *testing.T) {
	dir := setupTestGitRepo(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		f := filepath.Join(dir, "file_a.txt")
		_ = os.WriteFile(f, []byte(string(rune('0'+i))), 0644)
		_, err := CreateCheckpoint(ctx, CreateOptions{
			WorkDir:   dir,
			SessionID: "sess-1",
			Message:   "iteration",
		})
		if err != nil {
			t.Fatalf("CreateCheckpoint %d failed: %v", i, err)
		}
	}

	cps, err := ListCheckpoints(ctx, dir, 0)
	if err != nil {
		t.Fatalf("ListCheckpoints failed: %v", err)
	}
	if len(cps) < 5 {
		t.Errorf("expected at least 5 checkpoints, got %d", len(cps))
	}

	// Prune keeping 2
	pruned, err := PruneCheckpoints(ctx, dir, 2)
	if err != nil {
		t.Fatalf("PruneCheckpoints failed: %v", err)
	}
	if pruned == 0 {
		t.Errorf("expected >0 checkpoints pruned")
	}
}
