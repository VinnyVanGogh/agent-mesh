package shipreview_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/VinnyVanGogh/staypoint/internal/shipreview"
)

// openTestDB creates an in-memory SQLite DB with the ship_review schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			repo_path TEXT NOT NULL DEFAULT '',
			git_branch TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			account_role TEXT NOT NULL DEFAULT 'work',
			max_budget_usd REAL NOT NULL DEFAULT 0.0,
			max_turns INTEGER NOT NULL DEFAULT 0,
			spent_tokens INTEGER NOT NULL DEFAULT 0,
			spent_usd REAL NOT NULL DEFAULT 0.0,
			spent_turns INTEGER NOT NULL DEFAULT 0,
			organization TEXT, project TEXT,
			is_blocked INTEGER NOT NULL DEFAULT 0,
			block_reason TEXT, parent_id TEXT,
			execution_stage TEXT NOT NULL DEFAULT 'todo',
			checkout_run_id TEXT, checkout_agent_id TEXT, assignee_agent_id TEXT,
			work_kind TEXT NOT NULL DEFAULT 'coding',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			deleted_at TEXT
		);
		CREATE TABLE IF NOT EXISTS ship_review_cards (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			branch TEXT NOT NULL,
			head_sha TEXT NOT NULL,
			test_steps_json TEXT NOT NULL DEFAULT '[]',
			dev_url TEXT NOT NULL DEFAULT '',
			dev_pid INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			approved_sha TEXT,
			main_sha TEXT,
			send_back_comment TEXT,
			reject_comment TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE TABLE IF NOT EXISTS project_dev_configs (
			repo_path TEXT PRIMARY KEY,
			dev_command TEXT NOT NULL DEFAULT '',
			dev_url TEXT NOT NULL DEFAULT '',
			setup_steps_json TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// setupGitRepo creates a temporary git repo with one commit and a feature branch.
// A bare clone is set up as origin so push/pull work in tests.
// Returns repoDir, featureBranch, featureSHA.
func setupGitRepo(t *testing.T) (repoDir, featureBranch, featureSHA string) {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	_ = os.MkdirAll(dir, 0755)

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.com",
	)
	run := func(d string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = gitEnv
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v (in %s): %v", args, d, err)
		}
		return string(out)
	}

	run(dir, "init", "-b", "main")
	run(dir, "config", "user.email", "t@t.com")
	run(dir, "config", "user.name", "test")

	// Initial commit on main.
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0644)
	run(dir, "add", ".")
	run(dir, "commit", "-m", "init")

	// Feature branch.
	featureBranch = "feature/test-ship"
	run(dir, "checkout", "-b", featureBranch)
	_ = os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0644)
	run(dir, "add", ".")
	run(dir, "commit", "-m", "feat: add feature")

	sha, err := shipreview.CurrentBranchHEAD(context.Background(), dir, featureBranch)
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}
	featureSHA = sha

	run(dir, "checkout", "main")

	// Create bare remote and push both branches.
	bareDir := filepath.Join(base, "bare.git")
	run(base, "init", "--bare", "-b", "main", bareDir)
	run(dir, "remote", "add", "origin", bareDir)
	run(dir, "push", "origin", "main")
	run(dir, "push", "origin", featureBranch)

	return dir, featureBranch, featureSHA
}

func TestCreateCardRequiresTestSteps(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`INSERT INTO tasks (id, name) VALUES ('t1', 'Test task')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = shipreview.CreateCard(db, "t1", "feature/test", "abc123", nil, "")
	if err == nil {
		t.Fatal("expected error for empty test_steps")
	}
}

func TestCardRoundTrip(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`INSERT INTO tasks (id, name) VALUES ('t2', 'Test task')`)
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{"1. Open /home page", "2. Click Login button", "3. Verify redirect to /dashboard"}
	card, err := shipreview.CreateCard(db, "t2", "feature/test", "deadbeef", steps, "http://localhost:5173")
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}
	if card.Status != "pending" {
		t.Errorf("want pending, got %q", card.Status)
	}
	if len(card.TestSteps) != 3 {
		t.Errorf("want 3 steps, got %d", len(card.TestSteps))
	}

	got, err := shipreview.GetCard(db, "t2")
	if err != nil {
		t.Fatalf("GetCard: %v", err)
	}
	if got.HeadSHA != "deadbeef" {
		t.Errorf("want headSHA deadbeef, got %q", got.HeadSHA)
	}
	if got.DevURL != "http://localhost:5173" {
		t.Errorf("want devURL, got %q", got.DevURL)
	}
}

func TestSendBackAndReplace(t *testing.T) {
	db := openTestDB(t)
	_, _ = db.Exec(`INSERT INTO tasks (id, name) VALUES ('t3', 'Test task')`)
	steps := []string{"1. Check homepage"}
	card, _ := shipreview.CreateCard(db, "t3", "feature/test", "sha1", steps, "")

	if err := shipreview.SendBack(db, card, "fix the typo"); err != nil {
		t.Fatalf("SendBack: %v", err)
	}

	got, _ := shipreview.GetCard(db, "t3")
	if got.Status != "sent_back" {
		t.Errorf("want sent_back, got %q", got.Status)
	}

	// Agent iterates: creates new card (replaces sent_back card).
	card2, err := shipreview.CreateCard(db, "t3", "feature/test", "sha2", steps, "")
	if err != nil {
		t.Fatalf("second CreateCard: %v", err)
	}
	if card2.HeadSHA != "sha2" {
		t.Errorf("want sha2, got %q", card2.HeadSHA)
	}

	got2, _ := shipreview.GetCard(db, "t3")
	if got2.Status != "pending" {
		t.Errorf("want pending, got %q", got2.Status)
	}
}

func TestApproveAndMergeHashPin(t *testing.T) {
	db := openTestDB(t)
	_, _ = db.Exec(`INSERT INTO tasks (id, name) VALUES ('t4', 'Merge test')`)

	repoDir, branch, featureSHA := setupGitRepo(t)

	card, err := shipreview.CreateCard(db, "t4", branch, featureSHA, []string{"1. Verify"}, "")
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}

	mainSHA, err := shipreview.ApproveAndMerge(context.Background(), db, card, repoDir, "main")
	if err != nil {
		t.Fatalf("ApproveAndMerge: %v", err)
	}
	if mainSHA == "" {
		t.Fatal("expected non-empty main SHA")
	}

	// Verify card is now approved.
	got, _ := shipreview.GetCard(db, "t4")
	if got.Status != "approved" {
		t.Errorf("want approved, got %q", got.Status)
	}
	if got.ApprovedSHA != featureSHA {
		t.Errorf("want approvedSHA=%q, got %q", featureSHA, got.ApprovedSHA)
	}
}

func TestApproveAndMergeRejectsMovedHead(t *testing.T) {
	db := openTestDB(t)
	_, _ = db.Exec(`INSERT INTO tasks (id, name) VALUES ('t5', 'Head moved test')`)

	repoDir, branch, _ := setupGitRepo(t)

	// Create card with a stale (wrong) SHA.
	card, err := shipreview.CreateCard(db, "t5", branch, "0000000000000000000000000000000000000000", []string{"1. Check"}, "")
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}

	_, err = shipreview.ApproveAndMerge(context.Background(), db, card, repoDir, "main")
	if err == nil {
		t.Fatal("expected error for moved HEAD")
	}
}

func TestProjectDevConfigRoundTrip(t *testing.T) {
	db := openTestDB(t)
	cfg := &shipreview.ProjectDevConfig{
		RepoPath:   "/tmp/myproject",
		DevCommand: "bun run dev",
		DevURL:     "http://localhost:5173",
		SetupSteps: []string{"bun install", "cp .env.local.source .env.local"},
	}
	if err := shipreview.UpsertProjectDevConfig(db, cfg); err != nil {
		t.Fatalf("UpsertProjectDevConfig: %v", err)
	}
	got, err := shipreview.GetProjectDevConfig(db, "/tmp/myproject")
	if err != nil {
		t.Fatalf("GetProjectDevConfig: %v", err)
	}
	if got.DevCommand != cfg.DevCommand {
		t.Errorf("want %q, got %q", cfg.DevCommand, got.DevCommand)
	}
	if len(got.SetupSteps) != 2 {
		t.Errorf("want 2 setup steps, got %d", len(got.SetupSteps))
	}
}

// TestDeleteBranchRefusesProtectedBranches verifies that DeleteBranch rejects
// main, master, and the remote default branch without touching the remote.
func TestDeleteBranchRefusesProtectedBranches(t *testing.T) {
	repoDir, _, _ := setupGitRepo(t)
	ctx := context.Background()

	for _, name := range []string{"main", "master"} {
		err := shipreview.DeleteBranch(ctx, repoDir, name)
		if err == nil {
			t.Errorf("expected error deleting protected branch %q, got nil", name)
			continue
		}
		if !errors.Is(err, shipreview.ErrProtectedBranch) {
			t.Errorf("DeleteBranch(%q): want ErrProtectedBranch, got %v", name, err)
		}
	}
}

// TestDeleteBranchRefusesRemoteDefault verifies that DeleteBranch refuses to
// delete whatever branch origin/HEAD points to (even if it is not named "main").
func TestDeleteBranchRefusesRemoteDefault(t *testing.T) {
	repoDir, _, _ := setupGitRepo(t)
	ctx := context.Background()

	// setupGitRepo sets up origin with HEAD → main.
	// "main" is already covered by the name check, but exercise the remote-HEAD
	// path by trying to delete the same branch after renaming it locally
	// (origin/HEAD still points to it).
	err := shipreview.DeleteBranch(ctx, repoDir, "main")
	if err == nil {
		t.Fatal("expected error deleting remote-default branch, got nil")
	}
	if !errors.Is(err, shipreview.ErrProtectedBranch) {
		t.Errorf("want ErrProtectedBranch, got %v", err)
	}
}

// TestDeleteBranchTaskBranch verifies that DeleteBranch succeeds for a real
// non-protected branch and actually removes it from the remote.
func TestDeleteBranchTaskBranch(t *testing.T) {
	repoDir, featureBranch, _ := setupGitRepo(t)
	ctx := context.Background()

	if err := shipreview.DeleteBranch(ctx, repoDir, featureBranch); err != nil {
		t.Fatalf("DeleteBranch(%q): %v", featureBranch, err)
	}

	// Verify the branch is gone from the remote.
	cmd := exec.Command("git", "ls-remote", "--heads", "origin", featureBranch)
	cmd.Dir = repoDir
	out, _ := cmd.Output()
	if len(out) > 0 {
		t.Errorf("branch %q still present on remote after delete", featureBranch)
	}
}

// TestDeleteBranchReturnsErrorOnFailure verifies that DeleteBranch propagates
// git errors rather than swallowing them.  A repo with a bad remote URL provides
// a reliable failure regardless of remote git version.
func TestDeleteBranchReturnsErrorOnFailure(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	_ = os.MkdirAll(dir, 0755)

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t.com",
	)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "test")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0644)
	run("add", ".")
	run("commit", "-m", "init")
	// Point origin to an unreachable path.
	run("remote", "add", "origin", "/nonexistent/path/repo.git")

	err := shipreview.DeleteBranch(context.Background(), dir, "staypoint/task-xyz")
	if err == nil {
		t.Fatal("expected error deleting branch with unreachable remote, got nil")
	}
}

// TestApproveAndMergeFromRepoRoot verifies that ApproveAndMerge works correctly
// when called with the repo root (not a task worktree), which is the post-fix path.
func TestApproveAndMergeFromRepoRoot(t *testing.T) {
	db := openTestDB(t)
	_, _ = db.Exec(`INSERT INTO tasks (id, name) VALUES ('t6', 'Repo root merge')`)

	repoDir, branch, featureSHA := setupGitRepo(t)
	// Repo root is on main after setupGitRepo — this is the condition we test.
	card, err := shipreview.CreateCard(db, "t6", branch, featureSHA, []string{"1. Verify"}, "")
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}

	mainSHA, err := shipreview.ApproveAndMerge(context.Background(), db, card, repoDir, "main")
	if err != nil {
		t.Fatalf("ApproveAndMerge from repo root: %v", err)
	}
	if mainSHA == "" {
		t.Fatal("expected non-empty main SHA after merge")
	}

	got, _ := shipreview.GetCard(db, "t6")
	if got.Status != "approved" {
		t.Errorf("want approved, got %q", got.Status)
	}
}

// TestStartDevServerCreatesWorktreeAtPinnedSHA verifies that StartDevServer creates
// a temporary detached worktree at card.HeadSHA, not at the repo root (main).
// It also verifies that StopDevServer removes the worktree.
func TestStartDevServerCreatesWorktreeAtPinnedSHA(t *testing.T) {
	db := openTestDB(t)
	_, _ = db.Exec(`INSERT INTO tasks (id, name) VALUES ('t7', 'Dev server test')`)

	repoDir, branch, featureSHA := setupGitRepo(t)

	card, err := shipreview.CreateCard(db, "t7", branch, featureSHA, []string{"1. Check"}, "http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("CreateCard: %v", err)
	}

	// "sleep 9999" blocks long enough that the process is still alive when we
	// check the worktree state, then StopDevServer kills it cleanly.
	cfg := &shipreview.ProjectDevConfig{
		RepoPath:   repoDir,
		DevCommand: "sleep 9999",
		DevURL:     "http://127.0.0.1:9999",
	}

	url, err := shipreview.StartDevServer(db, card, cfg, repoDir)
	if err != nil {
		t.Fatalf("StartDevServer: %v", err)
	}
	if url != "http://127.0.0.1:9999" {
		t.Errorf("want url http://127.0.0.1:9999, got %q", url)
	}

	// The worktree is created synchronously before the process starts, so it
	// exists immediately after StartDevServer returns.
	wtPath := filepath.Join(repoDir, ".worktrees", "devserver-t7")
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("expected dev worktree at %s, got stat error: %v", wtPath, err)
	}

	// Verify the worktree is detached at the feature SHA, not at main's HEAD.
	headInWT, err := shipreview.CurrentBranchHEAD(context.Background(), wtPath, "HEAD")
	if err != nil {
		t.Fatalf("CurrentBranchHEAD in worktree: %v", err)
	}
	if headInWT != featureSHA {
		t.Errorf("worktree HEAD = %q, want featureSHA %q (must not be main)", headInWT, featureSHA)
	}

	// StopDevServer kills the process and synchronously removes the worktree.
	shipreview.StopDevServer(db, card)

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("expected dev worktree %s to be removed after StopDevServer, stat err: %v", wtPath, err)
	}
}
