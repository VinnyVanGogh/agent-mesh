// Package governance implements the StayPoint task governance engine:
// reviewers, approvers, update-triggered watchdogs, state gating, and audit trail.
package governance

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotAssigned is returned when a review or vote comes from an actor who is
// not assigned to the task as a reviewer or approver.
var ErrNotAssigned = errors.New("governance: actor is not assigned to this task")

// Config is the per-task governance configuration.
type Config struct {
	TaskID            string  `json:"task_id"`
	ApprovalThreshold int     `json:"approval_threshold"`
	RequireReview     bool    `json:"require_review"`
	WatchdogAgentID   string  `json:"watchdog_agent_id,omitempty"`
	WatchdogPrompt    string  `json:"watchdog_prompt,omitempty"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

// Reviewer is a reviewer assigned to a task.
type Reviewer struct {
	ID           int64  `json:"id"`
	TaskID       string `json:"task_id"`
	ReviewerID   string `json:"reviewer_id"`
	ReviewerType string `json:"reviewer_type"`
	AssignedAt   string `json:"assigned_at"`
}

// Approver is an approver assigned to a task.
type Approver struct {
	ID           int64  `json:"id"`
	TaskID       string `json:"task_id"`
	ApproverID   string `json:"approver_id"`
	ApproverType string `json:"approver_type"`
	AssignedAt   string `json:"assigned_at"`
}

// ApprovalVote is a recorded approval vote.
type ApprovalVote struct {
	ID         int64  `json:"id"`
	TaskID     string `json:"task_id"`
	ApproverID string `json:"approver_id"`
	Vote       string `json:"vote"`
	Reason     string `json:"reason,omitempty"`
	VotedAt    string `json:"voted_at"`
}

// ReviewDecision is a recorded review decision.
type ReviewDecision struct {
	ID         int64  `json:"id"`
	TaskID     string `json:"task_id"`
	ReviewerID string `json:"reviewer_id"`
	Decision   string `json:"decision"`
	Notes      string `json:"notes,omitempty"`
	DecidedAt  string `json:"decided_at"`
}

// GovernanceSnapshot is the full governance state for a task.
type GovernanceSnapshot struct {
	Config    *Config          `json:"config,omitempty"`
	Reviewers []Reviewer       `json:"reviewers"`
	Approvers []Approver       `json:"approvers"`
	Reviews   []ReviewDecision `json:"reviews"`
	Votes     []ApprovalVote   `json:"votes"`
}

// GetOrCreateConfig returns the governance config for a task, creating defaults if absent.
func GetOrCreateConfig(db *sql.DB, taskID string) (*Config, error) {
	cfg, err := GetGovernanceConfig(db, taskID)
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		return cfg, nil
	}
	_, err = db.Exec(
		`INSERT OR IGNORE INTO task_governance (task_id, approval_threshold, require_review) VALUES (?, 1, 0)`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("governance: create default config: %w", err)
	}
	return GetGovernanceConfig(db, taskID)
}

// GetGovernanceConfig reads the governance config for a task; returns nil if not set.
func GetGovernanceConfig(db *sql.DB, taskID string) (*Config, error) {
	var c Config
	var watchdogAgentID, watchdogPrompt sql.NullString
	var requireReview int
	err := db.QueryRow(
		`SELECT task_id, approval_threshold, require_review, watchdog_agent_id, watchdog_prompt, created_at, updated_at
		 FROM task_governance WHERE task_id = ?`,
		taskID,
	).Scan(&c.TaskID, &c.ApprovalThreshold, &requireReview, &watchdogAgentID, &watchdogPrompt, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.RequireReview = requireReview == 1
	if watchdogAgentID.Valid {
		c.WatchdogAgentID = watchdogAgentID.String
	}
	if watchdogPrompt.Valid {
		c.WatchdogPrompt = watchdogPrompt.String
	}
	return &c, nil
}

// SetGovernanceConfig upserts the governance config for a task.
func SetGovernanceConfig(db *sql.DB, taskID, actorID string, threshold int, requireReview bool, watchdogAgentID, watchdogPrompt string) (*Config, error) {
	var waid, wp interface{}
	if watchdogAgentID != "" {
		waid = watchdogAgentID
	}
	if watchdogPrompt != "" {
		wp = watchdogPrompt
	}

	_, err := db.Exec(
		`INSERT INTO task_governance (task_id, approval_threshold, require_review, watchdog_agent_id, watchdog_prompt)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(task_id) DO UPDATE SET
		   approval_threshold = excluded.approval_threshold,
		   require_review      = excluded.require_review,
		   watchdog_agent_id   = excluded.watchdog_agent_id,
		   watchdog_prompt     = excluded.watchdog_prompt,
		   updated_at          = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		taskID, threshold, boolToInt(requireReview), waid, wp,
	)
	if err != nil {
		return nil, fmt.Errorf("governance: upsert config: %w", err)
	}
	_ = LogEvent(db, taskID, actorID, AuditGovernanceUpdated, nil, nil, map[string]any{
		"approval_threshold": threshold,
		"require_review":     requireReview,
		"watchdog_agent_id":  watchdogAgentID,
	})
	return GetGovernanceConfig(db, taskID)
}

// AssignReviewer adds a reviewer to a task.
func AssignReviewer(db *sql.DB, taskID, reviewerID, reviewerType, actorID string) error {
	if reviewerType == "" {
		reviewerType = "agent"
	}
	_, err := db.Exec(
		`INSERT OR IGNORE INTO task_reviewers (task_id, reviewer_id, reviewer_type) VALUES (?, ?, ?)`,
		taskID, reviewerID, reviewerType,
	)
	if err != nil {
		return fmt.Errorf("governance: assign reviewer: %w", err)
	}
	_ = LogEvent(db, taskID, actorID, AuditReviewerAssigned, nil, nil, map[string]string{"reviewer_id": reviewerID, "reviewer_type": reviewerType})
	return nil
}

// RemoveReviewer removes a reviewer from a task.
func RemoveReviewer(db *sql.DB, taskID, reviewerID, actorID string) error {
	_, err := db.Exec(`DELETE FROM task_reviewers WHERE task_id = ? AND reviewer_id = ?`, taskID, reviewerID)
	if err != nil {
		return fmt.Errorf("governance: remove reviewer: %w", err)
	}
	_ = LogEvent(db, taskID, actorID, AuditReviewerRemoved, nil, nil, map[string]string{"reviewer_id": reviewerID})
	return nil
}

// ListReviewers returns all reviewers for a task.
func ListReviewers(db *sql.DB, taskID string) ([]Reviewer, error) {
	rows, err := db.Query(
		`SELECT id, task_id, reviewer_id, reviewer_type, assigned_at FROM task_reviewers WHERE task_id = ? ORDER BY assigned_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviewers := []Reviewer{}
	for rows.Next() {
		var r Reviewer
		if err := rows.Scan(&r.ID, &r.TaskID, &r.ReviewerID, &r.ReviewerType, &r.AssignedAt); err != nil {
			return nil, err
		}
		reviewers = append(reviewers, r)
	}
	return reviewers, rows.Err()
}

// AssignApprover adds an approver to a task.
func AssignApprover(db *sql.DB, taskID, approverID, approverType, actorID string) error {
	if approverType == "" {
		approverType = "agent"
	}
	_, err := db.Exec(
		`INSERT OR IGNORE INTO task_approvers (task_id, approver_id, approver_type) VALUES (?, ?, ?)`,
		taskID, approverID, approverType,
	)
	if err != nil {
		return fmt.Errorf("governance: assign approver: %w", err)
	}
	_ = LogEvent(db, taskID, actorID, AuditApproverAssigned, nil, nil, map[string]string{"approver_id": approverID, "approver_type": approverType})
	return nil
}

// RemoveApprover removes an approver from a task.
func RemoveApprover(db *sql.DB, taskID, approverID, actorID string) error {
	_, err := db.Exec(`DELETE FROM task_approvers WHERE task_id = ? AND approver_id = ?`, taskID, approverID)
	if err != nil {
		return fmt.Errorf("governance: remove approver: %w", err)
	}
	_ = LogEvent(db, taskID, actorID, AuditApproverRemoved, nil, nil, map[string]string{"approver_id": approverID})
	return nil
}

// ListApprovers returns all approvers for a task.
func ListApprovers(db *sql.DB, taskID string) ([]Approver, error) {
	rows, err := db.Query(
		`SELECT id, task_id, approver_id, approver_type, assigned_at FROM task_approvers WHERE task_id = ? ORDER BY assigned_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	approvers := []Approver{}
	for rows.Next() {
		var a Approver
		if err := rows.Scan(&a.ID, &a.TaskID, &a.ApproverID, &a.ApproverType, &a.AssignedAt); err != nil {
			return nil, err
		}
		approvers = append(approvers, a)
	}
	return approvers, rows.Err()
}

// SubmitReview records a reviewer's decision and fires notifications.
func SubmitReview(db *sql.DB, taskID, reviewerID, decision, notes, actorID string) (*ReviewDecision, error) {
	valid := map[string]bool{"approved": true, "rejected": true, "changes_requested": true}
	if !valid[decision] {
		return nil, fmt.Errorf("governance: invalid review decision %q", decision)
	}
	if ok, err := isAssigned(db, "task_reviewers", "reviewer_id", taskID, reviewerID); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %q is not a reviewer", ErrNotAssigned, reviewerID)
	}

	res, err := db.Exec(
		`INSERT INTO task_review_decisions (task_id, reviewer_id, decision, notes) VALUES (?, ?, ?, ?)`,
		taskID, reviewerID, decision, notes,
	)
	if err != nil {
		return nil, fmt.Errorf("governance: submit review: %w", err)
	}

	id, _ := res.LastInsertId()
	_ = LogEvent(db, taskID, actorID, AuditReviewSubmitted, nil, nil, map[string]string{
		"reviewer_id": reviewerID,
		"decision":    decision,
	})

	// If changes requested, revert stage to in_progress automatically.
	if decision == "changes_requested" {
		var currentStage string
		_ = db.QueryRow(`SELECT execution_stage FROM tasks WHERE id = ?`, taskID).Scan(&currentStage)
		if currentStage == StageInReview {
			_ = ExecuteTransition(db, taskID, StageInReview, StageInProgress, actorID)
		}
	}

	var rd ReviewDecision
	_ = db.QueryRow(
		`SELECT id, task_id, reviewer_id, decision, COALESCE(notes,''), decided_at FROM task_review_decisions WHERE id = ?`,
		id,
	).Scan(&rd.ID, &rd.TaskID, &rd.ReviewerID, &rd.Decision, &rd.Notes, &rd.DecidedAt)
	return &rd, nil
}

// SubmitApprovalVote records an approver's vote.
func SubmitApprovalVote(db *sql.DB, taskID, approverID, vote, reason, actorID string) (*ApprovalVote, error) {
	valid := map[string]bool{"approved": true, "rejected": true, "abstain": true}
	if !valid[vote] {
		return nil, fmt.Errorf("governance: invalid vote %q", vote)
	}
	if ok, err := isAssigned(db, "task_approvers", "approver_id", taskID, approverID); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %q is not an approver", ErrNotAssigned, approverID)
	}

	res, err := db.Exec(
		`INSERT INTO task_approval_votes (task_id, approver_id, vote, reason) VALUES (?, ?, ?, ?)`,
		taskID, approverID, vote, reason,
	)
	if err != nil {
		return nil, fmt.Errorf("governance: submit approval vote: %w", err)
	}

	id, _ := res.LastInsertId()
	_ = LogEvent(db, taskID, actorID, AuditApprovalVote, nil, nil, map[string]string{
		"approver_id": approverID,
		"vote":        vote,
	})

	var av ApprovalVote
	_ = db.QueryRow(
		`SELECT id, task_id, approver_id, vote, COALESCE(reason,''), voted_at FROM task_approval_votes WHERE id = ?`,
		id,
	).Scan(&av.ID, &av.TaskID, &av.ApproverID, &av.Vote, &av.Reason, &av.VotedAt)
	return &av, nil
}

// isAssigned reports whether actorID appears in table for the task. table and
// column are package constants, never caller input.
func isAssigned(db *sql.DB, table, column, taskID, actorID string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM `+table+` WHERE task_id = ? AND `+column+` = ?`,
		taskID, actorID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("governance: check assignment: %w", err)
	}
	return n > 0, nil
}

// GetSnapshot returns the full governance state for a task.
func GetSnapshot(db *sql.DB, taskID string) (*GovernanceSnapshot, error) {
	cfg, err := GetGovernanceConfig(db, taskID)
	if err != nil {
		return nil, err
	}
	reviewers, err := ListReviewers(db, taskID)
	if err != nil {
		return nil, err
	}
	approvers, err := ListApprovers(db, taskID)
	if err != nil {
		return nil, err
	}

	// Latest review decisions.
	reviewRows, err := db.Query(
		`SELECT id, task_id, reviewer_id, decision, COALESCE(notes,''), decided_at FROM task_review_decisions WHERE task_id = ? ORDER BY decided_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer reviewRows.Close()
	reviews := []ReviewDecision{}
	for reviewRows.Next() {
		var r ReviewDecision
		if err := reviewRows.Scan(&r.ID, &r.TaskID, &r.ReviewerID, &r.Decision, &r.Notes, &r.DecidedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}

	// All approval votes.
	voteRows, err := db.Query(
		`SELECT id, task_id, approver_id, vote, COALESCE(reason,''), voted_at FROM task_approval_votes WHERE task_id = ? ORDER BY voted_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer voteRows.Close()
	votes := []ApprovalVote{}
	for voteRows.Next() {
		var v ApprovalVote
		if err := voteRows.Scan(&v.ID, &v.TaskID, &v.ApproverID, &v.Vote, &v.Reason, &v.VotedAt); err != nil {
			return nil, err
		}
		votes = append(votes, v)
	}

	return &GovernanceSnapshot{
		Config:    cfg,
		Reviewers: reviewers,
		Approvers: approvers,
		Reviews:   reviews,
		Votes:     votes,
	}, nil
}
