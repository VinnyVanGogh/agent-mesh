package governance_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/governance"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	store, err := db.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store.DB()
}

func insertTask(t *testing.T, conn *sql.DB, id string) {
	t.Helper()
	_, err := conn.Exec(
		`INSERT INTO tasks (id, name, repo_path, status, account_role, execution_stage)
		 VALUES (?, 'test task', '/tmp', 'active', 'personal', 'todo')`,
		id,
	)
	if err != nil {
		t.Fatalf("insertTask: %v", err)
	}
}

func TestSetAndGetConfig(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-001")

	cfg, err := governance.SetGovernanceConfig(conn, "task-001", "actor-1", 2, true, "agent-watchdog", "requires: tests")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApprovalThreshold != 2 {
		t.Errorf("want threshold 2, got %d", cfg.ApprovalThreshold)
	}
	if !cfg.RequireReview {
		t.Errorf("want require_review true")
	}
	if cfg.WatchdogAgentID != "agent-watchdog" {
		t.Errorf("want watchdog agent-watchdog, got %q", cfg.WatchdogAgentID)
	}
}

func TestAssignReviewerAndApprover(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-002")

	if err := governance.AssignReviewer(conn, "task-002", "rev-1", "agent", "actor-1"); err != nil {
		t.Fatal(err)
	}
	reviewers, err := governance.ListReviewers(conn, "task-002")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewers) != 1 || reviewers[0].ReviewerID != "rev-1" {
		t.Errorf("unexpected reviewers: %v", reviewers)
	}

	if err := governance.AssignApprover(conn, "task-002", "app-1", "agent", "actor-1"); err != nil {
		t.Fatal(err)
	}
	approvers, err := governance.ListApprovers(conn, "task-002")
	if err != nil {
		t.Fatal(err)
	}
	if len(approvers) != 1 || approvers[0].ApproverID != "app-1" {
		t.Errorf("unexpected approvers: %v", approvers)
	}
}

func TestStateMachineValidTransitions(t *testing.T) {
	valid := [][2]string{
		{"backlog", "todo"},
		{"todo", "in_progress"},
		{"in_progress", "in_review"},
		{"in_review", "in_progress"},
		{"in_progress", "blocked"},
	}
	for _, pair := range valid {
		if !governance.IsValidTransition(pair[0], pair[1]) {
			t.Errorf("expected valid: %s→%s", pair[0], pair[1])
		}
	}

	invalid := [][2]string{
		{"done", "todo"},
		{"backlog", "done"},
		{"todo", "in_review"},
	}
	for _, pair := range invalid {
		if governance.IsValidTransition(pair[0], pair[1]) {
			t.Errorf("expected invalid: %s→%s", pair[0], pair[1])
		}
	}
}

func TestGatedTransition_InReviewToDone(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-003")
	// stage = in_review
	_, _ = conn.Exec(`UPDATE tasks SET execution_stage = 'in_review' WHERE id = 'task-003'`)

	// No governance config: gate passes (no require_review).
	if err := governance.CheckGates(conn, "task-003", governance.StageInReview, governance.StageDone, "actor"); err != nil {
		t.Errorf("unexpected gate failure without config: %v", err)
	}

	// Set require_review = true with threshold 2.
	_, _ = governance.SetGovernanceConfig(conn, "task-003", "actor", 2, true, "", "")

	// No reviews yet — should fail.
	if err := governance.CheckGates(conn, "task-003", governance.StageInReview, governance.StageDone, "actor"); err == nil {
		t.Errorf("expected gate failure: no reviewer approvals")
	}

	// Add a review approval.
	_, _ = governance.SubmitReview(conn, "task-003", "rev-1", "approved", "", "rev-1")

	// Still needs 2 approval votes — should fail.
	if err := governance.CheckGates(conn, "task-003", governance.StageInReview, governance.StageDone, "actor"); err == nil {
		t.Errorf("expected gate failure: not enough approval votes")
	}

	// Cast 2 approval votes.
	_, _ = governance.SubmitApprovalVote(conn, "task-003", "app-1", "approved", "", "app-1")
	_, _ = governance.SubmitApprovalVote(conn, "task-003", "app-2", "approved", "", "app-2")

	// Now should pass.
	if err := governance.CheckGates(conn, "task-003", governance.StageInReview, governance.StageDone, "actor"); err != nil {
		t.Errorf("unexpected gate failure after sufficient votes: %v", err)
	}
}

func TestWatchdogBlocksOnMissingContent(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-004")

	// Set watchdog requiring "tests" keyword.
	_, _ = governance.SetGovernanceConfig(conn, "task-004", "actor", 1, false, "agent-w", "requires: tests, docs")

	// No documents exist — watchdog should block.
	if err := governance.TriggerWatchdogEval(conn, "task-004", "comment"); err != nil {
		t.Fatal(err)
	}

	var isBlocked int
	_ = conn.QueryRow(`SELECT is_blocked FROM tasks WHERE id = 'task-004'`).Scan(&isBlocked)
	if isBlocked != 1 {
		t.Errorf("expected task to be blocked by watchdog")
	}
}

func TestAuditLogRecordsEvents(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-005")

	_, _ = governance.SetGovernanceConfig(conn, "task-005", "actor-1", 1, true, "", "")
	_ = governance.AssignReviewer(conn, "task-005", "rev-1", "agent", "actor-1")
	_, _ = governance.SubmitReview(conn, "task-005", "rev-1", "approved", "looks good", "rev-1")

	entries, err := governance.GetAuditLog(conn, "task-005")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 3 {
		t.Errorf("expected ≥3 audit entries, got %d", len(entries))
	}
}

func TestSnapshotReturnsAllRoles(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-006")

	_, _ = governance.SetGovernanceConfig(conn, "task-006", "a", 1, true, "wdog", "requires: x")
	_ = governance.AssignReviewer(conn, "task-006", "r1", "agent", "a")
	_ = governance.AssignApprover(conn, "task-006", "ap1", "agent", "a")
	_, _ = governance.SubmitApprovalVote(conn, "task-006", "ap1", "approved", "", "ap1")

	snap, err := governance.GetSnapshot(conn, "task-006")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Config == nil {
		t.Fatal("expected config")
	}
	if len(snap.Reviewers) != 1 {
		t.Errorf("expected 1 reviewer, got %d", len(snap.Reviewers))
	}
	if len(snap.Approvers) != 1 {
		t.Errorf("expected 1 approver, got %d", len(snap.Approvers))
	}
	if len(snap.Votes) != 1 {
		t.Errorf("expected 1 vote, got %d", len(snap.Votes))
	}
}
