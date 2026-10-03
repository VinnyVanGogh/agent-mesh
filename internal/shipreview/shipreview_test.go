package shipreview_test

import (
	"context"
	"database/sql"
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
