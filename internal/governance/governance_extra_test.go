package governance_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/governance"
)

func taskStage(t *testing.T, conn *sql.DB, id string) string {
	t.Helper()
	var stage string
	if err := conn.QueryRow(`SELECT execution_stage FROM tasks WHERE id = ?`, id).Scan(&stage); err != nil {
		t.Fatalf("taskStage: %v", err)
	}
	return stage
}

func taskBlock(t *testing.T, conn *sql.DB, id string) (bool, string) {
	t.Helper()
	var blocked int
	var reason string
	if err := conn.QueryRow(`SELECT is_blocked, COALESCE(block_reason,'') FROM tasks WHERE id = ?`, id).Scan(&blocked, &reason); err != nil {
		t.Fatalf("taskBlock: %v", err)
	}
	return blocked == 1, reason
}

func countEvents(t *testing.T, conn *sql.DB, taskID, eventType string) int {
	t.Helper()
	entries, err := governance.GetAuditLog(conn, taskID)
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

func TestGetOrCreateConfig_CreatesDefaultsOnce(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-goc")

	cfg, err := governance.GetGovernanceConfig(conn, "task-goc")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatalf("want nil config before creation, got %+v", cfg)
	}

	cfg, err = governance.GetOrCreateConfig(conn, "task-goc")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("want default config, got nil")
	}
	if cfg.ApprovalThreshold != 1 || cfg.RequireReview || cfg.WatchdogAgentID != "" || cfg.WatchdogPrompt != "" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	// A second call must return the existing row, not overwrite it.
	if _, err := governance.SetGovernanceConfig(conn, "task-goc", "a", 3, true, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, err = governance.GetOrCreateConfig(conn, "task-goc")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApprovalThreshold != 3 || !cfg.RequireReview {
		t.Errorf("GetOrCreateConfig clobbered existing config: %+v", cfg)
	}
}

func TestSetGovernanceConfig_EmptyStringsClearWatchdog(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-clear")

	if _, err := governance.SetGovernanceConfig(conn, "task-clear", "a", 1, false, "wd", "requires: x"); err != nil {
		t.Fatal(err)
	}
	cfg, err := governance.SetGovernanceConfig(conn, "task-clear", "a", 0, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WatchdogAgentID != "" || cfg.WatchdogPrompt != "" {
		t.Errorf("want watchdog cleared, got agent=%q prompt=%q", cfg.WatchdogAgentID, cfg.WatchdogPrompt)
	}
	if cfg.ApprovalThreshold != 0 {
		t.Errorf("want threshold 0, got %d", cfg.ApprovalThreshold)
	}
	if got := countEvents(t, conn, "task-clear", governance.AuditGovernanceUpdated); got != 2 {
		t.Errorf("want 2 governance_updated events, got %d", got)
	}
}

func TestAssign_DefaultTypeAndIdempotent(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-idem")

	for i := 0; i < 2; i++ {
		if err := governance.AssignReviewer(conn, "task-idem", "rev-1", "", "a"); err != nil {
			t.Fatal(err)
		}
		if err := governance.AssignApprover(conn, "task-idem", "app-1", "", "a"); err != nil {
			t.Fatal(err)
		}
	}

	reviewers, err := governance.ListReviewers(conn, "task-idem")
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewers) != 1 || reviewers[0].ReviewerType != "agent" {
		t.Errorf("want 1 reviewer of type agent, got %+v", reviewers)
	}
	approvers, err := governance.ListApprovers(conn, "task-idem")
	if err != nil {
		t.Fatal(err)
	}
	if len(approvers) != 1 || approvers[0].ApproverType != "agent" {
		t.Errorf("want 1 approver of type agent, got %+v", approvers)
	}
}

func TestRemoveReviewerAndApprover(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-rm")

	_ = governance.AssignReviewer(conn, "task-rm", "rev-1", "agent", "a")
	_ = governance.AssignReviewer(conn, "task-rm", "rev-2", "user", "a")
	_ = governance.AssignApprover(conn, "task-rm", "app-1", "agent", "a")

	if err := governance.RemoveReviewer(conn, "task-rm", "rev-1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := governance.RemoveApprover(conn, "task-rm", "app-1", "a"); err != nil {
		t.Fatal(err)
	}
	// Removing an absent entry is a no-op, not an error.
	if err := governance.RemoveReviewer(conn, "task-rm", "ghost", "a"); err != nil {
		t.Errorf("remove absent reviewer: %v", err)
	}
	if err := governance.RemoveApprover(conn, "task-rm", "ghost", "a"); err != nil {
		t.Errorf("remove absent approver: %v", err)
	}

	reviewers, _ := governance.ListReviewers(conn, "task-rm")
	if len(reviewers) != 1 || reviewers[0].ReviewerID != "rev-2" {
		t.Errorf("want only rev-2 left, got %+v", reviewers)
	}
	approvers, _ := governance.ListApprovers(conn, "task-rm")
	if len(approvers) != 0 {
		t.Errorf("want no approvers left, got %+v", approvers)
	}
	if got := countEvents(t, conn, "task-rm", governance.AuditReviewerRemoved); got != 2 {
		t.Errorf("want 2 reviewer_removed events, got %d", got)
	}
	if got := countEvents(t, conn, "task-rm", governance.AuditApproverRemoved); got != 2 {
		t.Errorf("want 2 approver_removed events, got %d", got)
	}
}

func TestSubmitReviewAndVote_RejectInvalidValues(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-inv")

	if _, err := governance.SubmitReview(conn, "task-inv", "r", "lgtm", "", "r"); err == nil {
		t.Error("want error for invalid review decision")
	}
	if _, err := governance.SubmitApprovalVote(conn, "task-inv", "a", "yes", "", "a"); err == nil {
		t.Error("want error for invalid vote")
	}

	snap, err := governance.GetSnapshot(conn, "task-inv")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Reviews) != 0 || len(snap.Votes) != 0 {
		t.Errorf("invalid submissions were persisted: %+v", snap)
	}
}

func TestSubmitReview_ReturnsPersistedDecision(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-rd")

	rd, err := governance.SubmitReview(conn, "task-rd", "rev-1", "rejected", "missing tests", "rev-1")
	if err != nil {
		t.Fatal(err)
	}
	if rd.ID == 0 || rd.TaskID != "task-rd" || rd.Decision != "rejected" || rd.Notes != "missing tests" || rd.DecidedAt == "" {
		t.Errorf("unexpected review decision: %+v", rd)
	}

	av, err := governance.SubmitApprovalVote(conn, "task-rd", "app-1", "abstain", "conflict", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if av.ID == 0 || av.Vote != "abstain" || av.Reason != "conflict" || av.VotedAt == "" {
		t.Errorf("unexpected vote: %+v", av)
	}
}

func TestSubmitReview_ChangesRequestedRevertsInReview(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-cr")
	_, _ = conn.Exec(`UPDATE tasks SET execution_stage = 'in_review' WHERE id = 'task-cr'`)

	if _, err := governance.SubmitReview(conn, "task-cr", "rev-1", "changes_requested", "fix it", "rev-1"); err != nil {
		t.Fatal(err)
	}
	if got := taskStage(t, conn, "task-cr"); got != governance.StageInProgress {
		t.Errorf("want stage in_progress after changes_requested, got %q", got)
	}
	if got := countEvents(t, conn, "task-cr", governance.AuditStateTransition); got != 1 {
		t.Errorf("want 1 state_transition event, got %d", got)
	}
}

func TestSubmitReview_ChangesRequestedLeavesOtherStages(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-cr2") // stage = todo

	if _, err := governance.SubmitReview(conn, "task-cr2", "rev-1", "changes_requested", "", "rev-1"); err != nil {
		t.Fatal(err)
	}
	if got := taskStage(t, conn, "task-cr2"); got != governance.StageTodo {
		t.Errorf("want stage unchanged (todo), got %q", got)
	}
}

func TestExecuteTransition_UpdatesStageAndAudits(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-ex")

	if err := governance.ExecuteTransition(conn, "task-ex", governance.StageTodo, governance.StageInProgress, "actor-x"); err != nil {
		t.Fatal(err)
	}
	if got := taskStage(t, conn, "task-ex"); got != governance.StageInProgress {
		t.Errorf("want in_progress, got %q", got)
	}

	entries, err := governance.GetAuditLog(conn, "task-ex")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.EventType != governance.AuditStateTransition {
			continue
		}
		found = true
		if e.ActorID != "actor-x" || e.FromState == nil || *e.FromState != "todo" || e.ToState == nil || *e.ToState != "in_progress" {
			t.Errorf("unexpected transition audit entry: %+v", e)
		}
	}
	if !found {
		t.Error("no state_transition audit entry")
	}
}

func TestExecuteTransition_InvalidEdge(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-bad")

	err := governance.ExecuteTransition(conn, "task-bad", governance.StageTodo, governance.StageDone, "a")
	var te *governance.TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("want *TransitionError, got %v", err)
	}
	if te.From != "todo" || te.To != "done" {
		t.Errorf("unexpected TransitionError fields: %+v", te)
	}
	if !strings.Contains(err.Error(), "todo→done") || !strings.Contains(err.Error(), "not in allowed set") {
		t.Errorf("unexpected message: %q", err.Error())
	}
	if got := taskStage(t, conn, "task-bad"); got != governance.StageTodo {
		t.Errorf("stage mutated on rejected transition: %q", got)
	}
	if got := countEvents(t, conn, "task-bad", governance.AuditStateTransition); got != 0 {
		t.Errorf("rejected transition was audited %d times", got)
	}
}

func TestExecuteTransition_GateBlocksDone(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-gate")
	_, _ = conn.Exec(`UPDATE tasks SET execution_stage = 'in_review' WHERE id = 'task-gate'`)
	_, _ = governance.SetGovernanceConfig(conn, "task-gate", "a", 1, false, "", "")

	err := governance.ExecuteTransition(conn, "task-gate", governance.StageInReview, governance.StageDone, "a")
	var te *governance.TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("want *TransitionError, got %v", err)
	}
	if !strings.Contains(te.Reason, "requires 1 approval votes, only 0 received") {
		t.Errorf("unexpected reason: %q", te.Reason)
	}
	if got := taskStage(t, conn, "task-gate"); got != governance.StageInReview {
		t.Errorf("stage mutated despite gate: %q", got)
	}

	_, _ = governance.SubmitApprovalVote(conn, "task-gate", "app-1", "approved", "", "app-1")
	if err := governance.ExecuteTransition(conn, "task-gate", governance.StageInReview, governance.StageDone, "a"); err != nil {
		t.Fatalf("want transition after approval, got %v", err)
	}
	if got := taskStage(t, conn, "task-gate"); got != governance.StageDone {
		t.Errorf("want done, got %q", got)
	}
}

func TestExecuteTransition_UnknownTask(t *testing.T) {
	conn := openTestDB(t)

	err := governance.ExecuteTransition(conn, "no-such-task", governance.StageTodo, governance.StageInProgress, "a")
	if err == nil || !strings.Contains(err.Error(), "task not found") {
		t.Errorf("want task not found error, got %v", err)
	}
}

func TestApprovalGate_OnlyApprovedVotesCount(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-votes")
	_, _ = governance.SetGovernanceConfig(conn, "task-votes", "a", 2, false, "", "")

	_, _ = governance.SubmitApprovalVote(conn, "task-votes", "app-1", "approved", "", "app-1")
	_, _ = governance.SubmitApprovalVote(conn, "task-votes", "app-2", "rejected", "", "app-2")
	_, _ = governance.SubmitApprovalVote(conn, "task-votes", "app-3", "abstain", "", "app-3")

	err := governance.CheckGates(conn, "task-votes", governance.StageInReview, governance.StageDone, "a")
	if err == nil || !strings.Contains(err.Error(), "only 1 received") {
		t.Errorf("want gate to count only approved votes, got %v", err)
	}
}

func TestApprovalGate_ZeroThresholdPasses(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-zero")
	_, _ = governance.SetGovernanceConfig(conn, "task-zero", "a", 0, false, "", "")

	if err := governance.CheckGates(conn, "task-zero", governance.StageInReview, governance.StageDone, "a"); err != nil {
		t.Errorf("want zero threshold to pass, got %v", err)
	}
}

func TestReviewGate_RejectedReviewDoesNotSatisfy(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-rg")
	_, _ = governance.SetGovernanceConfig(conn, "task-rg", "a", 0, true, "", "")
	_, _ = governance.SubmitReview(conn, "task-rg", "rev-1", "rejected", "", "rev-1")

	err := governance.CheckGates(conn, "task-rg", governance.StageInReview, governance.StageDone, "a")
	if err == nil || !strings.Contains(err.Error(), "no reviewer has approved") {
		t.Errorf("want review gate failure, got %v", err)
	}
}

func TestIsValidTransition_TerminalAndUnknownStages(t *testing.T) {
	for _, to := range []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "rejected"} {
		if governance.IsValidTransition(governance.StageDone, to) {
			t.Errorf("done must be terminal, allowed done→%s", to)
		}
		if governance.IsValidTransition(governance.StageRejected, to) {
			t.Errorf("rejected must be terminal, allowed rejected→%s", to)
		}
	}
	if governance.IsValidTransition("nonsense", governance.StageTodo) {
		t.Error("unknown source stage must be invalid")
	}
}

func TestWatchdog_NoConfigIsNoop(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-nowd")

	if err := governance.TriggerWatchdogEval(conn, "task-nowd", "comment"); err != nil {
		t.Fatal(err)
	}
	_, _ = governance.SetGovernanceConfig(conn, "task-nowd", "a", 1, false, "wd", "")
	if err := governance.TriggerWatchdogEval(conn, "task-nowd", "comment"); err != nil {
		t.Fatal(err)
	}

	evals, err := governance.GetWatchdogEvals(conn, "task-nowd", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evals) != 0 {
		t.Errorf("want no evals without a watchdog prompt, got %+v", evals)
	}
	if blocked, _ := taskBlock(t, conn, "task-nowd"); blocked {
		t.Error("task blocked without a watchdog prompt")
	}
}

func TestWatchdog_PassClearsOwnBlock(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-wd")
	_, _ = governance.SetGovernanceConfig(conn, "task-wd", "a", 1, false, "", "Review carefully.\nRequires: Tests, Docs\nthanks")

	if err := governance.TriggerWatchdogEval(conn, "task-wd", "comment"); err != nil {
		t.Fatal(err)
	}
	blocked, reason := taskBlock(t, conn, "task-wd")
	if !blocked || !strings.HasPrefix(reason, "watchdog: missing required content: tests, docs") {
		t.Fatalf("want watchdog block listing tests, docs; got blocked=%v reason=%q", blocked, reason)
	}

	// Keyword match is case-insensitive and only the latest version counts.
	_, _ = conn.Exec(`INSERT INTO task_documents (task_id, doc_key, version, content) VALUES ('task-wd', 'plan', 1, 'nothing')`)
	_, _ = conn.Exec(`INSERT INTO task_documents (task_id, doc_key, version, content) VALUES ('task-wd', 'plan', 2, 'Has TESTS and DOCS')`)

	if err := governance.TriggerWatchdogEval(conn, "task-wd", "document_updated"); err != nil {
		t.Fatal(err)
	}
	if blocked, reason := taskBlock(t, conn, "task-wd"); blocked || reason != "" {
		t.Errorf("want watchdog block cleared, got blocked=%v reason=%q", blocked, reason)
	}

	evals, err := governance.GetWatchdogEvals(conn, "task-wd", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evals) != 2 {
		t.Fatalf("want 2 evals, got %d", len(evals))
	}
	var passes, fails int
	for _, e := range evals {
		if e.Passed {
			passes++
			if e.Verdict != "criteria satisfied" || e.TriggerEvent != "document_updated" {
				t.Errorf("unexpected passing eval: %+v", e)
			}
		} else {
			fails++
		}
	}
	if passes != 1 || fails != 1 {
		t.Errorf("want 1 pass and 1 fail, got %d/%d", passes, fails)
	}

	entries, _ := governance.GetAuditLog(conn, "task-wd")
	for _, e := range entries {
		if e.EventType == governance.AuditWatchdogEval && e.ActorID != "watchdog" {
			t.Errorf("want default actor 'watchdog', got %q", e.ActorID)
		}
	}
}

func TestWatchdog_PassDoesNotClearForeignBlock(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-fb")
	_, _ = conn.Exec(`UPDATE tasks SET is_blocked = 1, block_reason = 'waiting on board' WHERE id = 'task-fb'`)
	_, _ = governance.SetGovernanceConfig(conn, "task-fb", "a", 1, false, "wd-agent", "be thorough")

	if err := governance.TriggerWatchdogEval(conn, "task-fb", "comment"); err != nil {
		t.Fatal(err)
	}
	blocked, reason := taskBlock(t, conn, "task-fb")
	if !blocked || reason != "waiting on board" {
		t.Errorf("watchdog cleared a non-watchdog block: blocked=%v reason=%q", blocked, reason)
	}
	if got := countEvents(t, conn, "task-fb", governance.AuditWatchdogEval); got != 1 {
		t.Errorf("want 1 watchdog_eval event, got %d", got)
	}
}

func TestWatchdog_WorkProductRequirement(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-wp")
	_, _ = governance.SetGovernanceConfig(conn, "task-wp", "a", 1, false, "", "a work_product must be attached")

	_ = governance.TriggerWatchdogEval(conn, "task-wp", "status_change")
	if _, reason := taskBlock(t, conn, "task-wp"); !strings.Contains(reason, "no work product registered") {
		t.Errorf("want work product block, got %q", reason)
	}

	_, _ = conn.Exec(`INSERT INTO task_work_products (task_id, product_type, reference) VALUES ('task-wp', 'pull_request', '#1')`)
	_ = governance.TriggerWatchdogEval(conn, "task-wp", "status_change")
	if blocked, _ := taskBlock(t, conn, "task-wp"); blocked {
		t.Error("want block cleared once a work product exists")
	}
}

func TestGetWatchdogEvals_LimitAndDefault(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-lim")
	_, _ = governance.SetGovernanceConfig(conn, "task-lim", "a", 1, false, "", "requires: x")

	for i := 0; i < 25; i++ {
		if err := governance.TriggerWatchdogEval(conn, "task-lim", "comment"); err != nil {
			t.Fatal(err)
		}
	}

	evals, err := governance.GetWatchdogEvals(conn, "task-lim", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(evals) != 5 {
		t.Errorf("want 5 evals with limit 5, got %d", len(evals))
	}

	evals, err = governance.GetWatchdogEvals(conn, "task-lim", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(evals) != 20 {
		t.Errorf("want default limit 20, got %d", len(evals))
	}

	other, err := governance.GetWatchdogEvals(conn, "other-task", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("evals leaked across tasks: %+v", other)
	}
}

func TestLogEvent_UnmarshalablePayload(t *testing.T) {
	conn := openTestDB(t)
	insertTask(t, conn, "task-log")

	if err := governance.LogEvent(conn, "task-log", "a", "custom", nil, nil, make(chan int)); err == nil {
		t.Error("want marshal error for channel payload")
	}
	if entries, _ := governance.GetAuditLog(conn, "task-log"); len(entries) != 0 {
		t.Errorf("failed event was persisted: %+v", entries)
	}
}
