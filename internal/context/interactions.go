package context

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Supported interaction kinds
const (
	KindAskUserQuestions   = "ask_user_questions"
	KindRequestConfirmation = "request_confirmation"
	KindSuggestTasks        = "suggest_tasks"
)

// Interaction status values
const (
	InteractionStatusPending    = "pending"
	InteractionStatusAccepted   = "accepted"
	InteractionStatusRejected   = "rejected"
	InteractionStatusSuperseded = "superseded"
	InteractionStatusCancelled  = "cancelled"
)

var (
	ErrStaleDocumentRevision  = errors.New("stale document revision")
	ErrDocumentNotFound       = errors.New("target document not found")
	ErrInvalidInteractionKind = errors.New("invalid interaction kind")
	ErrInvalidPayload         = errors.New("invalid interaction payload")
	ErrInteractionNotFound    = errors.New("interaction not found")
)

// TaskInteraction represents a user-facing interactive card or prompt.
type TaskInteraction struct {
	ID                 int     `json:"id"`
	TaskID             string  `json:"task_id"`
	InteractionKind    string  `json:"interaction_kind"`
	Payload            string  `json:"payload"`
	Status             string  `json:"status"`
	IdempotencyKey     string  `json:"idempotency_key,omitempty"`
	SupersedeOnComment bool    `json:"supersede_on_comment"`
	Response           string  `json:"response,omitempty"`
	CreatedAt          string  `json:"created_at"`
	ResolvedAt         *string `json:"resolved_at,omitempty"`
}

// QuestionItem defines a single question in ask_user_questions.
type QuestionItem struct {
	ID            string   `json:"id,omitempty"`
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	IsMultiSelect bool     `json:"is_multi_select,omitempty"`
}

// AskUserQuestionsPayload defines the payload for ask_user_questions.
type AskUserQuestionsPayload struct {
	Questions              []QuestionItem `json:"questions"`
	SupersedeOnUserComment *bool          `json:"supersedeOnUserComment,omitempty"`
}

// ConfirmationTarget defines the target bound to a confirmation request.
type ConfirmationTarget struct {
	Type       string `json:"type"` // e.g. "issue_document"
	Key        string `json:"key"`  // e.g. "plan"
	RevisionId int    `json:"revisionId"`
}

// RequestConfirmationPayload defines the payload for request_confirmation.
type RequestConfirmationPayload struct {
	Target                 ConfirmationTarget `json:"target"`
	Prompt                 string             `json:"prompt"`
	IdempotencyKey         string             `json:"idempotencyKey,omitempty"`
	SupersedeOnUserComment *bool              `json:"supersedeOnUserComment,omitempty"`
}

// SuggestedTaskItem defines an item in suggest_tasks.
type SuggestedTaskItem struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// SuggestTasksPayload defines the payload for suggest_tasks.
type SuggestTasksPayload struct {
	Tasks                  []SuggestedTaskItem `json:"tasks"`
	SupersedeOnUserComment *bool               `json:"supersedeOnUserComment,omitempty"`
}

// QuestionAnswer holds the response for a question.
type QuestionAnswer struct {
	QuestionID      string   `json:"question_id,omitempty"`
	SelectedOptions []string `json:"selected_options,omitempty"`
	CustomAnswer    string   `json:"custom_answer,omitempty"`
}

// AskQuestionsResponse represents answered questions.
type AskQuestionsResponse struct {
	Answers []QuestionAnswer `json:"answers"`
}

// ConfirmationResponse represents user confirmation disposition.
type ConfirmationResponse struct {
	Confirmed bool   `json:"confirmed"`
	Feedback  string `json:"feedback,omitempty"`
}

// SuggestTasksResponse represents accepted suggested tasks.
type SuggestTasksResponse struct {
	AcceptedTasks []SuggestedTaskItem `json:"accepted_tasks"`
}

// ValidateInteractionPayload checks the structural validity of an interaction payload.
func ValidateInteractionPayload(kind string, payloadJSON string) error {
	if strings.TrimSpace(payloadJSON) == "" {
		return fmt.Errorf("%w: payload cannot be empty", ErrInvalidPayload)
	}

	switch kind {
	case KindAskUserQuestions:
		var p AskUserQuestionsPayload
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			return fmt.Errorf("%w: failed to parse ask_user_questions payload: %w", ErrInvalidPayload, err)
		}
		if len(p.Questions) == 0 {
			return fmt.Errorf("%w: questions array cannot be empty", ErrInvalidPayload)
		}
		for i, q := range p.Questions {
			if strings.TrimSpace(q.Question) == "" {
				return fmt.Errorf("%w: question %d has empty question text", ErrInvalidPayload, i+1)
			}
			if len(q.Options) < 2 {
				return fmt.Errorf("%w: question %d must provide at least 2 options", ErrInvalidPayload, i+1)
			}
		}
		return nil

	case KindRequestConfirmation:
		var p RequestConfirmationPayload
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			return fmt.Errorf("%w: failed to parse request_confirmation payload: %w", ErrInvalidPayload, err)
		}
		if strings.TrimSpace(p.Prompt) == "" {
			return fmt.Errorf("%w: confirmation prompt cannot be empty", ErrInvalidPayload)
		}
		if p.Target.Key != "" && p.Target.RevisionId < 1 {
			return fmt.Errorf("%w: target revisionId must be >= 1", ErrInvalidPayload)
		}
		return nil

	case KindSuggestTasks:
		var p SuggestTasksPayload
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			return fmt.Errorf("%w: failed to parse suggest_tasks payload: %w", ErrInvalidPayload, err)
		}
		if len(p.Tasks) == 0 {
			return fmt.Errorf("%w: tasks array cannot be empty", ErrInvalidPayload)
		}
		for i, t := range p.Tasks {
			if strings.TrimSpace(t.Title) == "" {
				return fmt.Errorf("%w: suggested task %d has empty title", ErrInvalidPayload, i+1)
			}
		}
		return nil

	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidInteractionKind, kind)
	}
}

// CreateInteraction creates a new task interaction with revision locking and idempotency.
func CreateInteraction(db *sql.DB, in *TaskInteraction) (*TaskInteraction, error) {
	if in == nil {
		return nil, errors.New("interaction cannot be nil")
	}

	task, err := GetTask(db, in.TaskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}
	in.TaskID = task.ID

	if err := ValidateInteractionPayload(in.InteractionKind, in.Payload); err != nil {
		return nil, err
	}

	// Default supersede on comment is true unless explicitly set in payload
	supersedeOnComment := true

	// Check if idempotency key is specified or can be derived
	idempotencyKey := strings.TrimSpace(in.IdempotencyKey)

	switch in.InteractionKind {
	case KindRequestConfirmation:
		var p RequestConfirmationPayload
		if err := json.Unmarshal([]byte(in.Payload), &p); err == nil {
			if p.SupersedeOnUserComment != nil {
				supersedeOnComment = *p.SupersedeOnUserComment
			}
			if idempotencyKey == "" {
				if p.IdempotencyKey != "" {
					idempotencyKey = p.IdempotencyKey
				} else if p.Target.Key != "" && p.Target.RevisionId > 0 {
					idempotencyKey = fmt.Sprintf("confirmation:%s:%s:%d", in.TaskID, p.Target.Key, p.Target.RevisionId)
				}
			}

			// Validate plan revision locking against task_documents
			if p.Target.Key != "" {
				var maxVer sql.NullInt32
				err := db.QueryRow(`SELECT MAX(version) FROM task_documents WHERE task_id = ? AND doc_key = ?`, in.TaskID, p.Target.Key).Scan(&maxVer)
				if err != nil || !maxVer.Valid {
					return nil, fmt.Errorf("%w: document %q has no recorded revisions", ErrDocumentNotFound, p.Target.Key)
				}

				latestVer := int(maxVer.Int32)
				if p.Target.RevisionId < latestVer {
					return nil, fmt.Errorf("%w: target revision is %d, but latest is %d", ErrStaleDocumentRevision, p.Target.RevisionId, latestVer)
				}
				if p.Target.RevisionId > latestVer {
					return nil, fmt.Errorf("%w: target revision %d does not exist (latest is %d)", ErrDocumentNotFound, p.Target.RevisionId, latestVer)
				}
			}
		}

	case KindAskUserQuestions:
		var p AskUserQuestionsPayload
		if err := json.Unmarshal([]byte(in.Payload), &p); err == nil {
			if p.SupersedeOnUserComment != nil {
				supersedeOnComment = *p.SupersedeOnUserComment
			}
		}

	case KindSuggestTasks:
		var p SuggestTasksPayload
		if err := json.Unmarshal([]byte(in.Payload), &p); err == nil {
			if p.SupersedeOnUserComment != nil {
				supersedeOnComment = *p.SupersedeOnUserComment
			}
		}
	}

	// Duplicate idempotency keys are no-ops: return existing interaction if found
	if idempotencyKey != "" {
		existing, err := GetInteractionByIdempotencyKey(db, in.TaskID, idempotencyKey)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	in.IdempotencyKey = idempotencyKey
	in.SupersedeOnComment = supersedeOnComment

	query := `
		INSERT INTO task_interactions (
			task_id, interaction_kind, payload, status, idempotency_key, supersede_on_comment, created_at
		)
		VALUES (?, ?, ?, 'pending', ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	`
	supersedeInt := 0
	if in.SupersedeOnComment {
		supersedeInt = 1
	}

	res, err := db.Exec(query, in.TaskID, in.InteractionKind, in.Payload, in.IdempotencyKey, supersedeInt)
	if err != nil {
		// If race condition hit unique idempotency index, return the existing row
		if strings.Contains(err.Error(), "UNIQUE constraint failed") && idempotencyKey != "" {
			existing, getErr := GetInteractionByIdempotencyKey(db, in.TaskID, idempotencyKey)
			if getErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("failed to insert interaction: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	_ = LogActivity(db, in.TaskID, "interaction_created", fmt.Sprintf("Created interaction %s (#%d, idempotency: %s)", in.InteractionKind, id, in.IdempotencyKey))

	return GetInteraction(db, int(id))
}

// ResolveInteraction marks an interaction as accepted, rejected, cancelled, or superseded.
func ResolveInteraction(db *sql.DB, interactionID int, status string, response any) (*TaskInteraction, error) {
	switch status {
	case InteractionStatusAccepted, InteractionStatusRejected, InteractionStatusCancelled, InteractionStatusSuperseded:
	default:
		return nil, fmt.Errorf("invalid terminal status %q", status)
	}

	existing, err := GetInteraction(db, interactionID)
	if err != nil {
		return nil, err
	}

	if existing.Status != InteractionStatusPending {
		return nil, fmt.Errorf("interaction #%d already resolved (status: %s)", existing.ID, existing.Status)
	}

	var respJSON string
	if response != nil {
		switch r := response.(type) {
		case string:
			respJSON = r
		default:
			bytes, err := json.Marshal(r)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}
			respJSON = string(bytes)
		}
	}

	query := `
		UPDATE task_interactions
		SET status = ?, response = ?, resolved_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
	`
	if _, err := db.Exec(query, status, respJSON, existing.ID); err != nil {
		return nil, fmt.Errorf("failed to resolve interaction: %w", err)
	}

	_ = LogActivity(db, existing.TaskID, "interaction_resolved", fmt.Sprintf("Interaction #%d resolved with status %q", existing.ID, status))

	return GetInteraction(db, existing.ID)
}

// SupersedeInteractionsOnComment marks all pending interactions on a task as superseded if supersede_on_comment is enabled.
func SupersedeInteractionsOnComment(db *sql.DB, taskID string) (int, error) {
	query := `
		UPDATE task_interactions
		SET status = 'superseded', resolved_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE task_id = ? AND status = 'pending' AND supersede_on_comment = 1
	`
	res, err := db.Exec(query, taskID)
	if err != nil {
		return 0, fmt.Errorf("failed to supersede interactions: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	if affected > 0 {
		_ = LogActivity(db, taskID, "interaction_superseded", fmt.Sprintf("Superseded %d pending interaction(s) on new comment", affected))
	}

	return int(affected), nil
}

// GetInteraction retrieves an interaction by ID.
func GetInteraction(db *sql.DB, id int) (*TaskInteraction, error) {
	query := `
		SELECT id, task_id, interaction_kind, payload, status, COALESCE(idempotency_key, ''), supersede_on_comment, COALESCE(response, ''), created_at, resolved_at
		FROM task_interactions
		WHERE id = ?
	`
	var in TaskInteraction
	var supersedeInt int
	var resolvedAt sql.NullString

	err := db.QueryRow(query, id).Scan(
		&in.ID, &in.TaskID, &in.InteractionKind, &in.Payload, &in.Status,
		&in.IdempotencyKey, &supersedeInt, &in.Response, &in.CreatedAt, &resolvedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrInteractionNotFound
	}
	if err != nil {
		return nil, err
	}

	in.SupersedeOnComment = supersedeInt == 1
	if resolvedAt.Valid {
		val := resolvedAt.String
		in.ResolvedAt = &val
	}

	return &in, nil
}

// GetInteractionByIdempotencyKey retrieves an interaction by task ID and idempotency key.
func GetInteractionByIdempotencyKey(db *sql.DB, taskID, idempotencyKey string) (*TaskInteraction, error) {
	query := `
		SELECT id, task_id, interaction_kind, payload, status, COALESCE(idempotency_key, ''), supersede_on_comment, COALESCE(response, ''), created_at, resolved_at
		FROM task_interactions
		WHERE task_id = ? AND idempotency_key = ?
	`
	var in TaskInteraction
	var supersedeInt int
	var resolvedAt sql.NullString

	err := db.QueryRow(query, taskID, idempotencyKey).Scan(
		&in.ID, &in.TaskID, &in.InteractionKind, &in.Payload, &in.Status,
		&in.IdempotencyKey, &supersedeInt, &in.Response, &in.CreatedAt, &resolvedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrInteractionNotFound
	}
	if err != nil {
		return nil, err
	}

	in.SupersedeOnComment = supersedeInt == 1
	if resolvedAt.Valid {
		val := resolvedAt.String
		in.ResolvedAt = &val
	}

	return &in, nil
}

// GetPendingInteraction retrieves the first pending interaction for a task.
func GetPendingInteraction(db *sql.DB, taskID string) (*TaskInteraction, error) {
	query := `
		SELECT id, task_id, interaction_kind, payload, status, COALESCE(idempotency_key, ''), supersede_on_comment, COALESCE(response, ''), created_at, resolved_at
		FROM task_interactions
		WHERE task_id = ? AND status = 'pending'
		ORDER BY id ASC
		LIMIT 1
	`
	var in TaskInteraction
	var supersedeInt int
	var resolvedAt sql.NullString

	err := db.QueryRow(query, taskID).Scan(
		&in.ID, &in.TaskID, &in.InteractionKind, &in.Payload, &in.Status,
		&in.IdempotencyKey, &supersedeInt, &in.Response, &in.CreatedAt, &resolvedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	in.SupersedeOnComment = supersedeInt == 1
	if resolvedAt.Valid {
		val := resolvedAt.String
		in.ResolvedAt = &val
	}

	return &in, nil
}

// ListInteractions returns all interactions for a task ordered by creation.
func ListInteractions(db *sql.DB, taskID string) ([]TaskInteraction, error) {
	task, err := GetTask(db, taskID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, task_id, interaction_kind, payload, status, COALESCE(idempotency_key, ''), supersede_on_comment, COALESCE(response, ''), created_at, resolved_at
		FROM task_interactions
		WHERE task_id = ?
		ORDER BY id ASC
	`
	rows, err := db.Query(query, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TaskInteraction
	for rows.Next() {
		var in TaskInteraction
		var supersedeInt int
		var resolvedAt sql.NullString

		if err := rows.Scan(
			&in.ID, &in.TaskID, &in.InteractionKind, &in.Payload, &in.Status,
			&in.IdempotencyKey, &supersedeInt, &in.Response, &in.CreatedAt, &resolvedAt,
		); err != nil {
			return nil, err
		}

		in.SupersedeOnComment = supersedeInt == 1
		if resolvedAt.Valid {
			val := resolvedAt.String
			in.ResolvedAt = &val
		}
		list = append(list, in)
	}

	return list, rows.Err()
}
