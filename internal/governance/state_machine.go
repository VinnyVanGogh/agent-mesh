package governance

import (
	"database/sql"
	"fmt"
)

// Execution stages for the full governance state machine.
const (
	StageBacklog    = "backlog"
	StageTodo       = "todo"
	StageInProgress = "in_progress"
	StageInReview   = "in_review"
	StageDone       = "done"
	StageBlocked    = "blocked"
	StageRejected   = "rejected"
)

// validTransitions defines which transitions are structurally allowed.
var validTransitions = map[string][]string{
	StageBacklog:    {StageTodo},
	StageTodo:       {StageInProgress, StageBlocked},
	StageInProgress: {StageInReview, StageBlocked, StageTodo},
	StageInReview:   {StageDone, StageInProgress, StageBlocked, StageRejected},
	StageBlocked:    {StageTodo, StageInProgress, StageInReview},
	StageDone:       {},
	StageRejected:   {},
}

// TransitionError is returned when a gate blocks a transition.
type TransitionError struct {
	From   string
	To     string
	Reason string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("governance: cannot transition %s→%s: %s", e.From, e.To, e.Reason)
}

// IsValidTransition returns true if the structural edge exists.
func IsValidTransition(from, to string) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// CheckGates validates governance gates for a transition and returns an error if blocked.
// actorID is the agent or user requesting the transition.
func CheckGates(db *sql.DB, taskID, from, to, actorID string) error {
	if !IsValidTransition(from, to) {
		return &TransitionError{From: from, To: to, Reason: "transition not in allowed set"}
	}

	// Gate: in_review → done requires review approval + sufficient approval votes.
	if from == StageInReview && to == StageDone {
		if err := checkReviewGate(db, taskID); err != nil {
			return err
		}
		if err := checkApprovalGate(db, taskID); err != nil {
			return err
		}
	}

	// Gate: in_progress → in_review requires at least one reviewer or approver assigned
	// when governance is configured (soft gate — just a best-effort hint, not a hard block).
	return nil
}

// ExecuteTransition applies the state change, enforces gates, and writes the audit log.
func ExecuteTransition(db *sql.DB, taskID, from, to, actorID string) error {
	if err := CheckGates(db, taskID, from, to, actorID); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	res, err := tx.Exec(
		`UPDATE tasks SET execution_stage = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		to, taskID,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("governance: update execution_stage: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		tx.Rollback()
		return fmt.Errorf("governance: task not found: %s", taskID)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	fromCopy, toCopy := from, to
	_ = LogEvent(db, taskID, actorID, AuditStateTransition, &fromCopy, &toCopy, nil)
	return nil
}

// checkReviewGate ensures at least one reviewer has approved when require_review is set.
func checkReviewGate(db *sql.DB, taskID string) error {
	var requireReview int
	err := db.QueryRow(`SELECT require_review FROM task_governance WHERE task_id = ?`, taskID).Scan(&requireReview)
	if err == sql.ErrNoRows || requireReview == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("governance: read governance config: %w", err)
	}

	var approved int
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM task_review_decisions WHERE task_id = ? AND decision = 'approved'`,
		taskID,
	).Scan(&approved)

	if approved == 0 {
		return &TransitionError{From: StageInReview, To: StageDone, Reason: "require_review=true but no reviewer has approved"}
	}
	return nil
}

// checkApprovalGate ensures enough approval votes have been cast.
func checkApprovalGate(db *sql.DB, taskID string) error {
	var threshold int
	err := db.QueryRow(`SELECT approval_threshold FROM task_governance WHERE task_id = ?`, taskID).Scan(&threshold)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("governance: read approval threshold: %w", err)
	}
	if threshold <= 0 {
		return nil
	}

	var approvedCount int
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM task_approval_votes WHERE task_id = ? AND vote = 'approved'`,
		taskID,
	).Scan(&approvedCount)

	if approvedCount < threshold {
		return &TransitionError{
			From:   StageInReview,
			To:     StageDone,
			Reason: fmt.Sprintf("requires %d approval votes, only %d received", threshold, approvedCount),
		}
	}
	return nil
}
