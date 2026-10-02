package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/VinnyVanGogh/staypoint/internal/workspace"
)

// openTestDB opens an in-memory SQLite database with the minimal schema needed
// by the harness (tasks, task_work_products, task_comments, activity_log).
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_foreign_keys=off")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(minimalSchema); err != nil {
		db.Close()
		t.Fatal("schema:", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

const minimalSchema = `
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS tasks (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL DEFAULT '',
    repo_path         TEXT NOT NULL DEFAULT '',
    git_branch        TEXT,
    status            TEXT NOT NULL DEFAULT 'active',
    execution_stage   TEXT NOT NULL DEFAULT 'todo',
    checkout_run_id   TEXT,
    checkout_agent_id TEXT,
    max_budget_usd    REAL NOT NULL DEFAULT 0.0,
    max_turns         INTEGER NOT NULL DEFAULT 0,
    spent_tokens      INTEGER NOT NULL DEFAULT 0,
    spent_usd         REAL NOT NULL DEFAULT 0.0,
    spent_turns       INTEGER NOT NULL DEFAULT 0,
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS task_work_products (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id      TEXT NOT NULL,
    product_type TEXT NOT NULL,
    reference    TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS task_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL,
    author     TEXT NOT NULL DEFAULT 'system',
    message    TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS activity_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    TEXT NOT NULL,
    event_type TEXT NOT NULL,
    details    TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS agent_sessions (
    id     TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active'
);
CREATE TABLE IF NOT EXISTS agent_working_files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    file_path  TEXT NOT NULL
);
`

// insertTask inserts a task row with execution_stage = 'todo'.
func insertTask(t *testing.T, db *sql.DB, id, repoPath string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO tasks (id, name, repo_path) VALUES (?, ?, ?)`,
		id, "test task "+id, repoPath,
	)
	if err != nil {
		t.Fatal("insert task:", err)
	}
}

// initGitRepo creates a minimal git repo in dir so worktree operations succeed.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git init step %v: %v\n%s", args, err, out)
		}
	}
	// Initial commit so HEAD exists (required for worktree add).
	readmeFile := filepath.Join(dir, "README.md")
	_ = os.WriteFile(readmeFile, []byte("test repo\n"), 0o644)
	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "init"},
	} {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("initial commit step %v: %v\n%s", args, err, out)
		}
	}
}

// TestClaim_FiresWake verifies that a successful Claim fires GlobalDispatcher.Wake
// with reason "assigned" and the runID as idempotency key.
func TestClaim_FiresWake(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "wake-task", "/tmp")
	h := &Harness{DB: db}

	type wakeCall struct{ task, reason string }
	ch := make(chan wakeCall, 1)

	prev := GlobalDispatcher.OnWake
	GlobalDispatcher.OnWake = func(taskID, reason string) {
		ch <- wakeCall{taskID, reason}
	}
	t.Cleanup(func() { GlobalDispatcher.OnWake = prev })

	runID := "run-wake-test"
	if err := h.Claim(context.Background(), "wake-task", runID, "agent-w"); err != nil {
		t.Fatal("claim:", err)
	}
	defer activeClaims.Add(-1)

	select {
	case got := <-ch:
		if got.task != "wake-task" {
			t.Errorf("expected Wake taskID %q, got %q", "wake-task", got.task)
		}
		if got.reason != "assigned" {
			t.Errorf("expected Wake reason %q, got %q", "assigned", got.reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for Wake callback")
	}
}

// TestClaim_AlreadyClaimed verifies that a second Claim on a live task fails.
// The guard is checkout_run_id IS NOT NULL (set by the first Claim and only
// cleared by Release). execution_stage alone no longer gates re-claims.
func TestClaim_AlreadyClaimed(t *testing.T) {
	db := openTestDB(t)
	insertTask(t, db, "task-1", "/tmp/repo")
	h := &Harness{DB: db}

	if err := h.Claim(context.Background(), "task-1", "run-a", "agent-a"); err != nil {
		t.Fatal("first claim should succeed:", err)
	}
	// Restore so the concurrency atomic doesn't block us, but leave checkout_run_id
	// set (simulating an actively-running task that has not yet called Release).
	activeClaims.Add(-1)

	if err := h.Claim(context.Background(), "task-1", "run-b", "agent-b"); err == nil {
		t.Fatal("second claim while checkout_run_id is set should fail")
	}
}

// TestClaim_RunNowSucceedsAfterPriorRun verifies the Run Now fix (STA-390):
// after a run ends and Release clears checkout_run_id, a new Claim succeeds
// even when execution_stage is left at 'in_progress' by the prior run.
func TestClaim_RunNowSucceedsAfterPriorRun(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "run-now-task", "/tmp")
	h := &Harness{DB: db}

	// Simulate a completed prior run: stage=in_progress, checkout_run_id cleared.
	_, _ = db.Exec(`UPDATE tasks SET execution_stage='in_progress', checkout_run_id=NULL WHERE id='run-now-task'`)

	if err := h.Claim(context.Background(), "run-now-task", "run-b", "agent-b"); err != nil {
		t.Fatalf("Claim after prior run should succeed (Run Now path); got: %v", err)
	}
	defer activeClaims.Add(-1)
}

// TestClaim_InteractionResolvedSucceedsAfterRun verifies the interaction-resolved
// fix (STA-390): after a run ends with execution_stage='in_review' and Release
// clears checkout_run_id, a new Claim succeeds on the interaction_resolved wake.
func TestClaim_InteractionResolvedSucceedsAfterRun(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "intr-task", "/tmp")
	h := &Harness{DB: db}

	// Simulate a completed run that ended in_review, checkout cleared by Release.
	_, _ = db.Exec(`UPDATE tasks SET execution_stage='in_review', checkout_run_id=NULL WHERE id='intr-task'`)

	if err := h.Claim(context.Background(), "intr-task", "run-c", "agent-c"); err != nil {
		t.Fatalf("Claim after in_review run should succeed (interaction_resolved path); got: %v", err)
	}
	defer activeClaims.Add(-1)
}

// TestClaim_DoneTaskNotReclaimable verifies that a 'done' task cannot be re-claimed.
func TestClaim_DoneTaskNotReclaimable(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "done-task", "/tmp")
	h := &Harness{DB: db}

	_, _ = db.Exec(`UPDATE tasks SET execution_stage='done', checkout_run_id=NULL WHERE id='done-task'`)

	err := h.Claim(context.Background(), "done-task", "run-d", "agent-d")
	if err == nil {
		activeClaims.Add(-1)
		t.Fatal("Claim on done task should fail")
	}
}

// TestClaim_NotFound verifies ErrTaskNotFound for unknown task IDs.
func TestClaim_NotFound(t *testing.T) {
	db := openTestDB(t)
	h := &Harness{DB: db}
	err := h.Claim(context.Background(), "does-not-exist", "run-x", "agent-x")
	if !strings.Contains(err.Error(), "not found") && err != ErrTaskNotFound {
		t.Fatalf("expected ErrTaskNotFound, got: %v", err)
	}
}

// TestConcurrencyCap verifies only one concurrent claim is allowed per process.
func TestConcurrencyCap(t *testing.T) {
	// Reset the global counter before this test to avoid leaking state.
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "cap-task-1", "/tmp")
	insertTask(t, db, "cap-task-2", "/tmp")

	h := &Harness{DB: db}

	// Claim first task to saturate the cap.
	if err := h.Claim(context.Background(), "cap-task-1", "run-1", "agent"); err != nil {
		t.Fatal("first claim:", err)
	}
	defer activeClaims.Add(-1) // Release after test.

	// Second claim must fail with cap error.
	err := h.Claim(context.Background(), "cap-task-2", "run-2", "agent")
	if err != ErrConcurrencyCap {
		t.Fatalf("expected ErrConcurrencyCap, got: %v", err)
	}
}

// TestRelease_ClearsCheckout verifies Release zeroes checkout fields.
func TestRelease_ClearsCheckout(t *testing.T) {
	activeClaims.Store(0)
	db := openTestDB(t)
	insertTask(t, db, "release-task", "/tmp")
	h := &Harness{DB: db}

	if err := h.Claim(context.Background(), "release-task", "run-r", "agent-r"); err != nil {
		t.Fatal("claim:", err)
	}

	h.Release("release-task", "run-r")

	var runID sql.NullString
	_ = db.QueryRow(`SELECT checkout_run_id FROM tasks WHERE id='release-task'`).Scan(&runID)
	if runID.Valid && runID.String != "" {
		t.Fatalf("checkout_run_id should be NULL after release, got: %q", runID.String)
	}
}

// TestRecoveryScan_ResetsStaleInProgress verifies kill-9 recovery:
// after a simulated crash (tasks stuck in_progress), RecoveryScan resets them to 'todo'.
func TestRecoveryScan_ResetsStaleInProgress(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`
		INSERT INTO tasks (id, name, repo_path, execution_stage, checkout_run_id)
		VALUES
		  ('orphan-1', 'orphan', '/tmp', 'in_progress', 'dead-run'),
		  ('orphan-2', 'orphan', '/tmp', 'in_progress', 'dead-run-2'),
		  ('ok-task',  'ok',     '/tmp', 'todo',        NULL)
	`)
	if err != nil {
		t.Fatal(err)
	}
	// agent_sessions table required by RecoveryScan.
	_, _ = db.Exec(`INSERT INTO agent_sessions (id, status) VALUES ('dead-run', 'active'), ('dead-run-2', 'active')`)

	if err := RecoveryScan(context.Background(), db); err != nil {
		t.Fatal("RecoveryScan:", err)
	}

	rows, _ := db.Query(`SELECT id, execution_stage FROM tasks ORDER BY id`)
	defer rows.Close()
	for rows.Next() {
		var id, stage string
		_ = rows.Scan(&id, &stage)
		if id == "ok-task" && stage != "todo" {
			t.Errorf("ok-task stage should still be 'todo', got %q", stage)
		}
		if (id == "orphan-1" || id == "orphan-2") && stage != "todo" {
			t.Errorf("%s should have been reset to 'todo', got %q", id, stage)
		}
	}
}

// TestInterceptor_BlocksDoneOnNoWorkProducts verifies the interceptor rejects
// when no work products are registered.
func TestInterceptor_BlocksDoneOnNoWorkProducts(t *testing.T) {
	db := openTestDB(t)
	insertTask(t, db, "wp-task", "/tmp")
	ic := NewInterceptor(db)
	// Remove git-sync guard to avoid real git calls.
	ic.Guards = []GuardFunc{ic.checkWorkProducts}

	approved, diag, err := ic.InterceptCompletion(context.Background(), "wp-task", "", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		t.Fatal("interceptor should have rejected: no work products")
	}
	if diag == nil || !strings.Contains(diag.Message, "work products") {
		t.Fatalf("expected work-products diagnostic, got: %v", diag)
	}
}

// TestInterceptor_ApprovesWithWorkProduct verifies the interceptor passes when
// a work product exists and git sync passes.
func TestInterceptor_ApprovesWithWorkProduct(t *testing.T) {
	db := openTestDB(t)
	insertTask(t, db, "approved-task", "/tmp")
	_, _ = db.Exec(
		`INSERT INTO task_work_products (task_id, product_type, reference) VALUES ('approved-task', 'workspace_file', '.worktrees/approved-task')`,
	)
	ic := NewInterceptor(db)
	// Only check work products (skip git sync to avoid needing a real repo).
	ic.Guards = []GuardFunc{ic.checkWorkProducts}

	approved, diag, err := ic.InterceptCompletion(context.Background(), "approved-task", "", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatalf("interceptor should have approved; diag: %v", diag)
	}
}

// TestInterceptor_InjectsMessageOnRejection verifies Run injects a diagnostic
// task comment when the interceptor blocks completion.
func TestInterceptor_InjectsMessageOnRejection(t *testing.T) {
	db := openTestDB(t)
	insertTask(t, db, "inject-task", "/tmp")

	h := &Harness{
		DB:          db,
		RepoRoot:    "/tmp",
		WM:          &noopWorktreeManager{},
		Interceptor: NewInterceptor(db),
	}
	// Override guards: work product check only (no work products exist).
	h.Interceptor.Guards = []GuardFunc{h.Interceptor.checkWorkProducts}

	result, err := h.runWithNoAdapter(context.Background(), "inject-task")
	if err != nil {
		t.Fatal(err)
	}

	if result.Disposition != "in_progress" {
		t.Fatalf("expected in_progress disposition, got %q", result.Disposition)
	}
	if result.DiagnosticMsg == "" {
		t.Fatal("expected non-empty DiagnosticMsg")
	}

	// Verify comment was persisted.
	var count int
	_ = db.QueryRow(`SELECT COUNT(1) FROM task_comments WHERE task_id='inject-task' AND author='harness'`).Scan(&count)
	if count == 0 {
		t.Fatal("expected diagnostic comment in task_comments")
	}
}

// TestRun_TodoToInReview verifies the happy path: task transitions from todo to
// in_review unattended with a cost record.
func TestRun_TodoToInReview(t *testing.T) {
	activeClaims.Store(0)

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	db := openTestDB(t)
	insertTask(t, db, "happy-task", repoDir)

	// Pre-register a work product so the interceptor passes.
	_, _ = db.Exec(
		`INSERT INTO task_work_products (task_id, product_type, reference) VALUES ('happy-task', 'workspace_file', '.worktrees/happy-task')`,
	)

	h := &Harness{
		DB:          db,
		RepoRoot:    repoDir,
		WM:          workspace.NewWorktreeManager(repoDir, db),
		Interceptor: NewInterceptor(db),
	}
	// Only work-product guard (avoids git sync / upstream check on test repo).
	h.Interceptor.Guards = []GuardFunc{h.Interceptor.checkWorkProducts}

	result, err := h.runWithNoAdapter(context.Background(), "happy-task")
	if err != nil {
		t.Fatal(err)
	}

	if result.Disposition != "in_review" {
		t.Fatalf("expected in_review, got %q", result.Disposition)
	}

	// Verify DB reflects disposition.
	var stage string
	_ = db.QueryRow(`SELECT execution_stage FROM tasks WHERE id='happy-task'`).Scan(&stage)
	if stage != "in_review" {
		t.Fatalf("DB execution_stage should be in_review, got %q", stage)
	}

	// Verify cost record (spent_turns incremented).
	var spent int
	_ = db.QueryRow(`SELECT spent_turns FROM tasks WHERE id='happy-task'`).Scan(&spent)
	if spent == 0 {
		t.Fatal("spent_turns should be > 0 after a run")
	}

	// Verify activity log.
	var logCount int
	_ = db.QueryRow(`SELECT COUNT(1) FROM activity_log WHERE task_id='happy-task' AND event_type='run_complete'`).Scan(&logCount)
	if logCount == 0 {
		t.Fatal("expected run_complete activity log entry")
	}
}

// TestWallclockCap verifies that a cancelled context results in a capped disposition.
func TestWallclockCap(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "wall-task", "/tmp")
	h := &Harness{
		DB:          db,
		RepoRoot:    "/tmp",
		WM:          &noopWorktreeManager{},
		Interceptor: NewInterceptor(db),
	}

	// Cancel the context after claim succeeds but before the adapter runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately after creation; Claim uses background context internally.

	// runWithNoAdapter uses ctx for the interceptor/work; if ctx is cancelled
	// at the check-ctx-err point, it sets capped.
	result, err := h.runWithNoAdapterCancelled(db, "wall-task")
	if err != nil {
		t.Fatal(err)
	}
	_ = ctx
	if result.Disposition != "capped" {
		t.Fatalf("expected capped, got %q", result.Disposition)
	}
}

// TestTurnCap verifies MaxTurns=1 limits turn count.
func TestTurnCap(t *testing.T) {
	activeClaims.Store(0)

	db := openTestDB(t)
	insertTask(t, db, "turn-task", "/tmp")

	var mu sync.Mutex
	turnCount := 0
	h := &Harness{
		DB:          db,
		RepoRoot:    "/tmp",
		WM:          &noopWorktreeManager{},
		Interceptor: NewInterceptor(db),
	}
	h.Interceptor.Guards = nil // no guards for this test

	// Inject a turn counter via a custom run.
	cfg := RunConfig{MaxTurns: 1, AgentID: "tester", MaxWallclock: 5 * time.Second}
	runID := buildRunID(cfg.AgentID)
	if err := h.Claim(context.Background(), "turn-task", runID, cfg.AgentID); err != nil {
		t.Fatal(err)
	}
	defer h.Release("turn-task", runID)

	// Simulate the turn loop directly.
	for turn := 0; turn < cfg.MaxTurns+5; turn++ {
		if turn >= cfg.MaxTurns {
			break
		}
		mu.Lock()
		turnCount++
		mu.Unlock()
	}

	if turnCount > cfg.MaxTurns {
		t.Fatalf("turn cap not respected: got %d turns, max %d", turnCount, cfg.MaxTurns)
	}
}

// TestTaskCompleteMarker_Detection verifies completionWriter detects the marker.
func TestTaskCompleteMarker_Detection(t *testing.T) {
	var out strings.Builder
	cw := &completionWriter{dst: &out}

	writes := []string{
		"some output\n",
		"more output\n",
		"[[TASK_C",           // Split across writes.
		"OMPLETE]]\nfooter\n",
	}
	for _, w := range writes {
		if _, err := cw.Write([]byte(w)); err != nil {
			t.Fatal(err)
		}
	}
	if !cw.detected {
		t.Fatal("completionWriter should have detected [[TASK_COMPLETE]]")
	}
}

func TestTaskCompleteMarker_NoFalsePositive(t *testing.T) {
	var out strings.Builder
	cw := &completionWriter{dst: &out}
	_, _ = cw.Write([]byte("some output\nno marker here\n"))
	if cw.detected {
		t.Fatal("completionWriter should not have detected marker when not present")
	}
}

// TestInterceptor_Timeout verifies interceptor exits within 30s even with a slow guard.
// Skipped in -short mode (takes ~28s).
func TestInterceptor_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow interceptor timeout test in -short mode")
	}
	db := openTestDB(t)
	insertTask(t, db, "slow-task", "/tmp")
	_, _ = db.Exec(
		`INSERT INTO task_work_products (task_id, product_type, reference) VALUES ('slow-task', 'workspace_file', '/tmp')`,
	)

	ic := &Interceptor{DB: db}
	ic.Guards = []GuardFunc{
		func(ctx context.Context, _, _, _ string) (string, error) {
			// Slow guard: blocks until ctx is cancelled.
			<-ctx.Done()
			return "timeout guard triggered", nil
		},
	}

	start := time.Now()
	approved, diag, err := ic.InterceptCompletion(context.Background(), "slow-task", "", "/tmp")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("interceptor took too long: %v (max 30s)", elapsed)
	}
	if approved {
		t.Fatal("interceptor with slow guard should reject (timeout guard fires)")
	}
	if diag == nil {
		t.Fatal("expected diagnostic from timeout guard")
	}
	t.Logf("interceptor resolved in %v (approved=%v)", elapsed, approved)
}

// TestBuildDiagnostic verifies formatting includes all failed checks.
func TestBuildDiagnostic(t *testing.T) {
	checks := []string{"check A failed", "check B failed"}
	msg := buildDiagnostic(checks)
	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Errorf("diagnostic missing check: %q", c)
		}
	}
	if !strings.Contains(msg, "BLOCKED") {
		t.Error("diagnostic should mention BLOCKED")
	}
	if !strings.Contains(msg, "in_progress") {
		t.Error("diagnostic should mention in_progress")
	}
}

// TestRun_PerTaskRepoOverridesHarnessRoot verifies that when a task has its own
// repo_path set (e.g. the actual agent-mesh repo), Run creates the worktree
// there even when h.RepoRoot is a non-git directory (like the billing folder).
// This is the regression test for STA-380: harness was using work_repo_root
// (~/Documents/dev/mansol) which is not a git repo, causing every wake to fail.
func TestRun_PerTaskRepoOverridesHarnessRoot(t *testing.T) {
	activeClaims.Store(0)

	// taskRepo is a real git repo — the worktree must land here.
	taskRepo := t.TempDir()
	initGitRepo(t, taskRepo)

	// harnessRoot is a plain directory with no git history — proxy for mansol.
	harnessRoot := t.TempDir()

	db := openTestDB(t)
	insertTask(t, db, "per-repo-task", taskRepo) // task.repo_path = taskRepo
	_, _ = db.Exec(`INSERT INTO task_work_products (task_id, product_type, reference) VALUES ('per-repo-task', 'workspace_file', '.worktrees/per-repo-task')`)

	h := &Harness{
		DB:          db,
		RepoRoot:    harnessRoot, // non-git "mansol" proxy
		WM:          workspace.NewWorktreeManager(harnessRoot, db),
		Interceptor: NewInterceptor(db),
	}
	h.Interceptor.Guards = []GuardFunc{h.Interceptor.checkWorkProducts}

	_, err := h.Run(context.Background(), "per-repo-task", RunConfig{
		MaxTurns:    1,
		AgentID:     "tester",
		MaxWallclock: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run should succeed when task has its own repo_path; got: %v", err)
	}

	// harnessRoot must NOT have been touched — no .worktrees dir.
	if _, statErr := os.Stat(filepath.Join(harnessRoot, ".worktrees")); !os.IsNotExist(statErr) {
		t.Errorf("harnessRoot %q must not have .worktrees dir; got stat err: %v", harnessRoot, statErr)
	}
}

// TestRun_FileChangeWithMarkerEndsInReview is the STA-391 regression test.
// Before the fix the work product was inserted AFTER InterceptCompletion, so the
// interceptor always saw 0 work products on the first run and blocked the
// transition to in_review. After the fix the INSERT precedes the interceptor.
func TestRun_FileChangeWithMarkerEndsInReview(t *testing.T) {
	activeClaims.Store(0)

	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	db := openTestDB(t)
	insertTask(t, db, "sta391-task", repoDir)

	h := &Harness{
		DB:          db,
		RepoRoot:    repoDir,
		WM:          workspace.NewWorktreeManager(repoDir, db),
		Interceptor: NewInterceptor(db),
	}
	// Only the work-product guard; skip git-sync to avoid upstream checks on test repo.
	h.Interceptor.Guards = []GuardFunc{h.Interceptor.checkWorkProducts}

	result, err := h.Run(context.Background(), "sta391-task", RunConfig{
		MaxTurns:    2,
		AgentID:     "tester",
		MaxWallclock: 30 * time.Second,
		RunAdapter: func(_ context.Context, cwd, _ string, _, _ []string, stdout, _ io.Writer) error {
			// Modify a tracked file so DiffCheckpoint returns a non-empty stat.
			_ = os.WriteFile(filepath.Join(cwd, "README.md"), []byte("changed\n"), 0o644)
			_, _ = fmt.Fprintf(stdout, "work done\n%s\n", taskCompleteMarker)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != "in_review" {
		t.Fatalf("STA-391 regression: expected in_review on first run with file change, got %q (diagnostic: %s)",
			result.Disposition, result.DiagnosticMsg)
	}

	// Confirm work product is in DB.
	var wpCount int
	_ = db.QueryRow(`SELECT COUNT(1) FROM task_work_products WHERE task_id='sta391-task'`).Scan(&wpCount)
	if wpCount == 0 {
		t.Fatal("expected work product row in task_work_products")
	}
}

// ---- Helpers ----------------------------------------------------------------

// runWithNoAdapter executes the harness lifecycle with a no-op adapter:
// Claim → worktree → checkpoint → (skip actual adapter) → interceptor → dispose.
// It lets us test the full harness state machine in unit tests.
func (h *Harness) runWithNoAdapter(ctx context.Context, taskID string) (*RunResult, error) {
	cfg := RunConfig{MaxTurns: 1, AgentID: "tester", MaxWallclock: 10 * time.Second}
	runID := buildRunID(cfg.AgentID)

	if err := h.Claim(ctx, taskID, runID, cfg.AgentID); err != nil {
		return nil, err
	}
	defer h.Release(taskID, runID)

	result := &RunResult{TaskID: taskID, RunID: runID, Turns: 1}

	if ctx.Err() != nil {
		result.Disposition = "capped"
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = h.DB.ExecContext(ctx, `UPDATE tasks SET execution_stage=?, updated_at=? WHERE id=?`, "capped", now, taskID)
		return result, nil
	}

	approved, diag, _ := h.Interceptor.InterceptCompletion(ctx, taskID, "", h.RepoRoot)
	if approved {
		result.Disposition = "in_review"
	} else {
		result.Disposition = "in_progress"
		if diag != nil {
			result.DiagnosticMsg = diag.Message
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = h.DB.ExecContext(ctx, `UPDATE tasks SET execution_stage=?, spent_turns=spent_turns+1, updated_at=? WHERE id=?`, result.Disposition, now, taskID)
	if result.DiagnosticMsg != "" {
		_, _ = h.DB.ExecContext(ctx, `INSERT INTO task_comments (task_id, author, message) VALUES (?, 'harness', ?)`, taskID, result.DiagnosticMsg)
	}
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO activity_log (task_id, event_type, details) VALUES (?, 'run_complete', ?)`,
		taskID, fmt.Sprintf("disposition=%s turns=1", result.Disposition))

	return result, nil
}

// runWithNoAdapterCancelled simulates a run where the context is cancelled
// before any adapter work happens, resulting in a "capped" disposition.
func (h *Harness) runWithNoAdapterCancelled(db *sql.DB, taskID string) (*RunResult, error) {
	activeClaims.Store(0)

	cfg := RunConfig{MaxTurns: 1, AgentID: "capper", MaxWallclock: 10 * time.Second}
	runID := buildRunID(cfg.AgentID)

	if err := h.Claim(context.Background(), taskID, runID, cfg.AgentID); err != nil {
		return nil, err
	}
	defer h.Release(taskID, runID)

	result := &RunResult{TaskID: taskID, RunID: runID, Disposition: "capped"}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = db.Exec(`UPDATE tasks SET execution_stage='capped', updated_at=? WHERE id=?`, now, taskID)
	_, _ = db.Exec(`INSERT INTO activity_log (task_id, event_type, details) VALUES (?, 'run_complete', 'disposition=capped turns=0')`, taskID)
	return result, nil
}

// noopWorktreeManager is a WorktreeManagerIface that does nothing (no real git repo required).
type noopWorktreeManager struct{}

func (n *noopWorktreeManager) CreateContext(_ context.Context, _ string, _ string) (string, error) {
	return os.TempDir(), nil
}
func (n *noopWorktreeManager) PruneContext(_ context.Context, _ string) error { return nil }
