package governance

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// AuditEvent types for the governance_audit_log.
const (
	AuditReviewerAssigned   = "reviewer_assigned"
	AuditReviewerRemoved    = "reviewer_removed"
	AuditApproverAssigned   = "approver_assigned"
	AuditApproverRemoved    = "approver_removed"
	AuditWatchdogAssigned   = "watchdog_assigned"
	AuditReviewSubmitted    = "review_submitted"
	AuditApprovalVote       = "approval_vote"
	AuditWatchdogEval       = "watchdog_eval"
	AuditStateTransition    = "state_transition"
	AuditGovernanceUpdated  = "governance_updated"
)

// AuditEntry is a single row from governance_audit_log.
type AuditEntry struct {
	ID        int64   `json:"id"`
	TaskID    string  `json:"task_id"`
	ActorID   string  `json:"actor_id"`
	EventType string  `json:"event_type"`
	FromState *string `json:"from_state,omitempty"`
	ToState   *string `json:"to_state,omitempty"`
	Payload   any     `json:"payload,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// LogEvent writes a governance event to the durable audit log.
func LogEvent(db *sql.DB, taskID, actorID, eventType string, fromState, toState *string, payload any) error {
	var payloadJSON *string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("governance: marshal audit payload: %w", err)
		}
		s := string(b)
		payloadJSON = &s
	}
	_, err := db.Exec(
		`INSERT INTO governance_audit_log (task_id, actor_id, event_type, from_state, to_state, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, actorID, eventType, fromState, toState, payloadJSON,
	)
	return err
}

// GetAuditLog returns the full governance audit trail for a task, newest first.
func GetAuditLog(db *sql.DB, taskID string) ([]AuditEntry, error) {
	rows, err := db.Query(
		`SELECT id, task_id, actor_id, event_type, from_state, to_state, payload, created_at
		 FROM governance_audit_log
		 WHERE task_id = ?
		 ORDER BY created_at DESC, id DESC`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var fromState, toState, payloadStr sql.NullString
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ActorID, &e.EventType, &fromState, &toState, &payloadStr, &e.CreatedAt); err != nil {
			return nil, err
		}
		if fromState.Valid {
			e.FromState = &fromState.String
		}
		if toState.Valid {
			e.ToState = &toState.String
		}
		if payloadStr.Valid {
			var v any
			if err := json.Unmarshal([]byte(payloadStr.String), &v); err == nil {
				e.Payload = v
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
