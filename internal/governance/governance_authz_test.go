package governance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/governance"
)

// STA-355: reviews and votes are accepted only from assigned actors.
func TestSubmit_RejectsUnassignedActors(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-authz")
	_ = governance.AssignReviewer(conn, "task-authz", "rev-1", "agent", "a")
	_ = governance.AssignApprover(conn, "task-authz", "app-1", "agent", "a")

	if _, err := governance.SubmitReview(conn, "task-authz", "stranger", "approved", "", "stranger"); !errors.Is(err, governance.ErrNotAssigned) {
		t.Errorf("review from stranger: want ErrNotAssigned, got %v", err)
	}
	if _, err := governance.SubmitApprovalVote(conn, "task-authz", "stranger", "approved", "", "stranger"); !errors.Is(err, governance.ErrNotAssigned) {
		t.Errorf("vote from stranger: want ErrNotAssigned, got %v", err)
	}
	// An approver is not a reviewer and vice versa.
	if _, err := governance.SubmitReview(conn, "task-authz", "app-1", "approved", "", "app-1"); !errors.Is(err, governance.ErrNotAssigned) {
		t.Errorf("review from approver: want ErrNotAssigned, got %v", err)
	}
	if _, err := governance.SubmitApprovalVote(conn, "task-authz", "rev-1", "approved", "", "rev-1"); !errors.Is(err, governance.ErrNotAssigned) {
		t.Errorf("vote from reviewer: want ErrNotAssigned, got %v", err)
	}
	// Assignment on another task does not carry over.
	insertTask(t, conn, "task-other")
	if _, err := governance.SubmitReview(conn, "task-other", "rev-1", "approved", "", "rev-1"); !errors.Is(err, governance.ErrNotAssigned) {
		t.Errorf("review on unassigned task: want ErrNotAssigned, got %v", err)
	}

	snap, err := governance.GetSnapshot(conn, "task-authz")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Reviews) != 0 || len(snap.Votes) != 0 {
		t.Errorf("rejected submissions were persisted: reviews=%+v votes=%+v", snap.Reviews, snap.Votes)
	}
	if got := countEvents(t, conn, "task-authz", governance.AuditReviewSubmitted) + countEvents(t, conn, "task-authz", governance.AuditApprovalVote); got != 0 {
		t.Errorf("rejected submissions were audited %d times", got)
	}
}

// STA-355: the in_review -> done gate counts only assigned reviewers and
// approvers, even if decision rows exist for anyone else.
func TestGate_CountsOnlyAssignedActors(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-gc")
	_, _ = conn.Exec(`UPDATE tasks SET execution_stage = 'in_review' WHERE id = 'task-gc'`)
	_, _ = governance.SetGovernanceConfig(conn, "task-gc", "a", 1, true, "", "")
	_ = governance.AssignReviewer(conn, "task-gc", "rev-1", "agent", "a")
	_ = governance.AssignApprover(conn, "task-gc", "app-1", "agent", "a")

	// Rows written behind the API's back, e.g. by an older daemon.
	if _, err := conn.Exec(`INSERT INTO task_review_decisions (task_id, reviewer_id, decision) VALUES ('task-gc', 'stranger', 'approved')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO task_approval_votes (task_id, approver_id, vote) VALUES ('task-gc', 'stranger', 'approved')`); err != nil {
		t.Fatal(err)
	}

	err := governance.ExecuteTransition(conn, "task-gc", governance.StageInReview, governance.StageDone, "a")
	if err == nil || !strings.Contains(err.Error(), "no reviewer has approved") {
		t.Fatalf("stranger review opened the gate: %v", err)
	}
	if _, err := governance.SubmitReview(conn, "task-gc", "rev-1", "approved", "", "rev-1"); err != nil {
		t.Fatal(err)
	}
	err = governance.ExecuteTransition(conn, "task-gc", governance.StageInReview, governance.StageDone, "a")
	if err == nil || !strings.Contains(err.Error(), "only 0 received") {
		t.Fatalf("stranger vote counted toward threshold: %v", err)
	}
	if _, err := governance.SubmitApprovalVote(conn, "task-gc", "app-1", "approved", "", "app-1"); err != nil {
		t.Fatal(err)
	}
	if err := governance.ExecuteTransition(conn, "task-gc", governance.StageInReview, governance.StageDone, "a"); err != nil {
		t.Fatalf("assigned review + vote should open the gate: %v", err)
	}
}

// Removing an assignee drops their earlier review or vote from the gate.
func TestGate_RemovedAssigneesStopCounting(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-rm2")
	_, _ = governance.SetGovernanceConfig(conn, "task-rm2", "a", 1, true, "", "")
	_ = governance.AssignReviewer(conn, "task-rm2", "rev-1", "agent", "a")
	_ = governance.AssignApprover(conn, "task-rm2", "app-1", "agent", "a")
	_, _ = governance.SubmitReview(conn, "task-rm2", "rev-1", "approved", "", "rev-1")
	_, _ = governance.SubmitApprovalVote(conn, "task-rm2", "app-1", "approved", "", "app-1")
	if err := governance.CheckGates(conn, "task-rm2", governance.StageInReview, governance.StageDone, "a"); err != nil {
		t.Fatalf("gate should be open: %v", err)
	}

	_ = governance.RemoveApprover(conn, "task-rm2", "app-1", "a")
	if err := governance.CheckGates(conn, "task-rm2", governance.StageInReview, governance.StageDone, "a"); err == nil {
		t.Error("removed approver's vote still counted")
	}
	_ = governance.AssignApprover(conn, "task-rm2", "app-1", "agent", "a")
	_ = governance.RemoveReviewer(conn, "task-rm2", "rev-1", "a")
	if err := governance.CheckGates(conn, "task-rm2", governance.StageInReview, governance.StageDone, "a"); err == nil {
		t.Error("removed reviewer's approval still counted")
	}
}
