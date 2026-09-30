package conversation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Store defines operations on the conversation storage layer.
type Store interface {
	CreateSession(ctx context.Context, conv *Conversation) error
	GetSession(ctx context.Context, id string) (*Conversation, error)
	UpdateSession(ctx context.Context, conv *Conversation) error
	DeleteSession(ctx context.Context, id string) error
	ListSessions(ctx context.Context, limit, offset int) ([]*SessionSummary, error)
	AppendMessage(ctx context.Context, sessionID string, msg *Message) error
	SetProviderHandle(ctx context.Context, handle *ProviderHandle) error
	GetProviderHandle(ctx context.Context, sessionID, provider string) (*ProviderHandle, error)
	ListProviderHandles(ctx context.Context, sessionID string) ([]*ProviderHandle, error)
}

// ConversationStore implements Store backed by a SQLite database.
type ConversationStore struct {
	db *sql.DB
}

// NewStore creates a new ConversationStore backed by db.
func NewStore(db *sql.DB) *ConversationStore {
	return &ConversationStore{db: db}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *ConversationStore) CreateSession(ctx context.Context, conv *Conversation) error {
	if conv.ID == "" {
		conv.ID = newID()
	}
	now := time.Now().UTC()
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	if conv.UpdatedAt.IsZero() {
		conv.UpdatedAt = now
	}

	meta := conv.Metadata
	if meta == nil {
		meta = make(map[string]interface{})
	}
	if conv.SystemPrompt != "" {
		meta["system_prompt"] = conv.SystemPrompt
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal conversation metadata: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO chat_sessions (id, title, repo_path, created_at, updated_at, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?);
	`, conv.ID, conv.Title, conv.RepoPath, conv.CreatedAt.Format(time.RFC3339Nano), conv.UpdatedAt.Format(time.RFC3339Nano), string(metaJSON))
	if err != nil {
		return fmt.Errorf("failed to insert chat_sessions: %w", err)
	}

	for i := range conv.Messages {
		msg := &conv.Messages[i]
		msg.SessionID = conv.ID
		msg.SequenceNum = i
		if msg.ID == "" {
			msg.ID = newID()
		}
		if msg.CreatedAt.IsZero() {
			msg.CreatedAt = now
		}

		msgMeta := msg.Metadata
		if msgMeta == nil {
			msgMeta = make(map[string]interface{})
		}
		if len(msg.ToolResults) > 0 {
			msgMeta["tool_results"] = msg.ToolResults
		}
		msgMetaJSON, err := json.Marshal(msgMeta)
		if err != nil {
			return fmt.Errorf("failed to marshal message metadata: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO chat_messages (id, session_id, sequence_num, role, content, token_count, created_at, metadata_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?);
		`, msg.ID, conv.ID, msg.SequenceNum, string(msg.Role), msg.Content, msg.TokenCount, msg.CreatedAt.Format(time.RFC3339Nano), string(msgMetaJSON))
		if err != nil {
			return fmt.Errorf("failed to insert chat_messages: %w", err)
		}

		for tcIdx, tc := range msg.ToolCalls {
			if tc.ID == "" {
				tc.ID = newID()
				msg.ToolCalls[tcIdx].ID = tc.ID
			}
			args := tc.Arguments
			if args == "" {
				args = "{}"
			}
			_, err = tx.ExecContext(ctx, `
				INSERT INTO chat_tool_calls (id, message_id, sequence_num, name, arguments, created_at)
				VALUES (?, ?, ?, ?, ?, ?);
			`, tc.ID, msg.ID, tcIdx, tc.Name, args, msg.CreatedAt.Format(time.RFC3339Nano))
			if err != nil {
				return fmt.Errorf("failed to insert chat_tool_calls: %w", err)
			}
		}

		for _, tr := range msg.ToolResults {
			if tr.ToolCallID != "" {
				isErr := 0
				if tr.IsError {
					isErr = 1
				}
				_, _ = tx.ExecContext(ctx, `
					UPDATE chat_tool_calls SET result = ?, is_error = ? WHERE id = ?;
				`, tr.Content, isErr, tr.ToolCallID)
			}
		}
	}

	for _, handle := range conv.ProviderHandles {
		handle.SessionID = conv.ID
		if handle.CreatedAt.IsZero() {
			handle.CreatedAt = now
		}
		if handle.UpdatedAt.IsZero() {
			handle.UpdatedAt = now
		}
		hMetaJSON, err := json.Marshal(handle.Metadata)
		if err != nil {
			hMetaJSON = []byte("{}")
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO session_provider_handles (session_id, provider, handle, model, created_at, updated_at, metadata_json)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, provider) DO UPDATE SET
				handle = excluded.handle,
				model = excluded.model,
				updated_at = excluded.updated_at,
				metadata_json = excluded.metadata_json;
		`, conv.ID, handle.Provider, handle.Handle, handle.Model, handle.CreatedAt.Format(time.RFC3339Nano), handle.UpdatedAt.Format(time.RFC3339Nano), string(hMetaJSON))
		if err != nil {
			return fmt.Errorf("failed to insert session_provider_handles: %w", err)
		}
	}

	return tx.Commit()
}

func (s *ConversationStore) GetSession(ctx context.Context, id string) (*Conversation, error) {
	conv := &Conversation{
		ID:              id,
		ProviderHandles: make(map[string]*ProviderHandle),
	}

	var title, repoPath, createdAtStr, updatedAtStr string
	var metaJSON sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT title, repo_path, created_at, updated_at, metadata_json
		FROM chat_sessions WHERE id = ?;
	`, id).Scan(&title, &repoPath, &createdAtStr, &updatedAtStr, &metaJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("chat session not found: %s", id)
		}
		return nil, fmt.Errorf("failed to query chat_sessions: %w", err)
	}

	conv.Title = title
	conv.RepoPath = repoPath
	if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
		conv.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
		conv.UpdatedAt = t
	}

	if metaJSON.Valid && metaJSON.String != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(metaJSON.String), &meta); err == nil {
			conv.Metadata = meta
			if sp, ok := meta["system_prompt"].(string); ok {
				conv.SystemPrompt = sp
			}
		}
	}

	// Fetch messages
	msgRows, err := s.db.QueryContext(ctx, `
		SELECT id, sequence_num, role, content, token_count, created_at, metadata_json
		FROM chat_messages WHERE session_id = ? ORDER BY sequence_num ASC;
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query chat_messages: %w", err)
	}
	defer msgRows.Close()

	for msgRows.Next() {
		var m Message
		var roleStr, msgCreatedAtStr string
		var mMetaJSON sql.NullString
		if err := msgRows.Scan(&m.ID, &m.SequenceNum, &roleStr, &m.Content, &m.TokenCount, &msgCreatedAtStr, &mMetaJSON); err != nil {
			return nil, fmt.Errorf("failed to scan chat_messages: %w", err)
		}
		m.SessionID = id
		m.Role = Role(roleStr)
		if t, err := time.Parse(time.RFC3339Nano, msgCreatedAtStr); err == nil {
			m.CreatedAt = t
		}
		if mMetaJSON.Valid && mMetaJSON.String != "" {
			var mm map[string]interface{}
			if err := json.Unmarshal([]byte(mMetaJSON.String), &mm); err == nil {
				m.Metadata = mm
				if rawTR, ok := mm["tool_results"]; ok {
					trBytes, _ := json.Marshal(rawTR)
					var trs []ToolResult
					if err := json.Unmarshal(trBytes, &trs); err == nil {
						m.ToolResults = trs
					}
				}
			}
		}
		conv.Messages = append(conv.Messages, m)
	}

	msgIndex := make(map[string]int)
	for i := range conv.Messages {
		msgIndex[conv.Messages[i].ID] = i
	}

	// Fetch tool calls
	tcRows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.message_id, c.sequence_num, c.name, c.arguments, c.result, c.is_error
		FROM chat_tool_calls c
		JOIN chat_messages m ON c.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.sequence_num ASC, c.sequence_num ASC;
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query chat_tool_calls: %w", err)
	}
	defer tcRows.Close()

	for tcRows.Next() {
		var tcID, messageID, name, args string
		var seqNum, isErrInt int
		var res sql.NullString
		if err := tcRows.Scan(&tcID, &messageID, &seqNum, &name, &args, &res, &isErrInt); err != nil {
			return nil, fmt.Errorf("failed to scan chat_tool_calls: %w", err)
		}
		if idx, ok := msgIndex[messageID]; ok {
			conv.Messages[idx].ToolCalls = append(conv.Messages[idx].ToolCalls, ToolCall{
				ID:        tcID,
				Name:      name,
				Arguments: args,
			})
		}
	}

	// Fetch provider handles
	hRows, err := s.db.QueryContext(ctx, `
		SELECT provider, handle, model, created_at, updated_at, metadata_json
		FROM session_provider_handles WHERE session_id = ?;
	`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query session_provider_handles: %w", err)
	}
	defer hRows.Close()

	for hRows.Next() {
		var ph ProviderHandle
		var hCreatedAtStr, hUpdatedAtStr string
		var modelStr, hMetaJSON sql.NullString
		if err := hRows.Scan(&ph.Provider, &ph.Handle, &modelStr, &hCreatedAtStr, &hUpdatedAtStr, &hMetaJSON); err != nil {
			return nil, fmt.Errorf("failed to scan session_provider_handles: %w", err)
		}
		ph.SessionID = id
		if modelStr.Valid {
			ph.Model = modelStr.String
		}
		if t, err := time.Parse(time.RFC3339Nano, hCreatedAtStr); err == nil {
			ph.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, hUpdatedAtStr); err == nil {
			ph.UpdatedAt = t
		}
		if hMetaJSON.Valid && hMetaJSON.String != "" {
			var hm map[string]interface{}
			if err := json.Unmarshal([]byte(hMetaJSON.String), &hm); err == nil {
				ph.Metadata = hm
			}
		}
		conv.ProviderHandles[ph.Provider] = &ph
	}

	return conv, nil
}

func (s *ConversationStore) UpdateSession(ctx context.Context, conv *Conversation) error {
	conv.UpdatedAt = time.Now().UTC()
	meta := conv.Metadata
	if meta == nil {
		meta = make(map[string]interface{})
	}
	if conv.SystemPrompt != "" {
		meta["system_prompt"] = conv.SystemPrompt
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE chat_sessions
		SET title = ?, repo_path = ?, updated_at = ?, metadata_json = ?
		WHERE id = ?;
	`, conv.Title, conv.RepoPath, conv.UpdatedAt.Format(time.RFC3339Nano), string(metaJSON), conv.ID)
	return err
}

func (s *ConversationStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = ?;`, id)
	return err
}

func (s *ConversationStore) ListSessions(ctx context.Context, limit, offset int) ([]*SessionSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.repo_path, s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM chat_messages m WHERE m.session_id = s.id) as msg_count
		FROM chat_sessions s
		ORDER BY s.updated_at DESC
		LIMIT ? OFFSET ?;
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list chat_sessions: %w", err)
	}
	defer rows.Close()

	var summaries []*SessionSummary
	for rows.Next() {
		var sum SessionSummary
		var createdAtStr, updatedAtStr string
		var repoPath sql.NullString
		if err := rows.Scan(&sum.ID, &sum.Title, &repoPath, &createdAtStr, &updatedAtStr, &sum.MessageCount); err != nil {
			return nil, fmt.Errorf("failed to scan session summary: %w", err)
		}
		if repoPath.Valid {
			sum.RepoPath = repoPath.String
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			sum.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
			sum.UpdatedAt = t
		}
		summaries = append(summaries, &sum)
	}
	return summaries, nil
}

func (s *ConversationStore) AppendMessage(ctx context.Context, sessionID string, msg *Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if msg.ID == "" {
		msg.ID = newID()
	}
	msg.SessionID = sessionID
	now := time.Now().UTC()
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = now
	}

	var nextSeq int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence_num) + 1, 0) FROM chat_messages WHERE session_id = ?;
	`, sessionID).Scan(&nextSeq)
	if err != nil {
		return fmt.Errorf("failed to calculate next sequence_num: %w", err)
	}
	msg.SequenceNum = nextSeq

	msgMeta := msg.Metadata
	if msgMeta == nil {
		msgMeta = make(map[string]interface{})
	}
	if len(msg.ToolResults) > 0 {
		msgMeta["tool_results"] = msg.ToolResults
	}
	msgMetaJSON, err := json.Marshal(msgMeta)
	if err != nil {
		return fmt.Errorf("failed to marshal message metadata: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO chat_messages (id, session_id, sequence_num, role, content, token_count, created_at, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?);
	`, msg.ID, sessionID, msg.SequenceNum, string(msg.Role), msg.Content, msg.TokenCount, msg.CreatedAt.Format(time.RFC3339Nano), string(msgMetaJSON))
	if err != nil {
		return fmt.Errorf("failed to insert chat_message: %w", err)
	}

	for tcIdx, tc := range msg.ToolCalls {
		if tc.ID == "" {
			tc.ID = newID()
			msg.ToolCalls[tcIdx].ID = tc.ID
		}
		args := tc.Arguments
		if args == "" {
			args = "{}"
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO chat_tool_calls (id, message_id, sequence_num, name, arguments, created_at)
			VALUES (?, ?, ?, ?, ?, ?);
		`, tc.ID, msg.ID, tcIdx, tc.Name, args, msg.CreatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("failed to insert chat_tool_call: %w", err)
		}
	}

	for _, tr := range msg.ToolResults {
		if tr.ToolCallID != "" {
			isErr := 0
			if tr.IsError {
				isErr = 1
			}
			_, _ = tx.ExecContext(ctx, `
				UPDATE chat_tool_calls SET result = ?, is_error = ? WHERE id = ?;
			`, tr.Content, isErr, tr.ToolCallID)
		}
	}

	_, _ = tx.ExecContext(ctx, `UPDATE chat_sessions SET updated_at = ? WHERE id = ?;`, now.Format(time.RFC3339Nano), sessionID)

	return tx.Commit()
}

func (s *ConversationStore) SetProviderHandle(ctx context.Context, handle *ProviderHandle) error {
	now := time.Now().UTC()
	if handle.CreatedAt.IsZero() {
		handle.CreatedAt = now
	}
	handle.UpdatedAt = now
	hMetaJSON, err := json.Marshal(handle.Metadata)
	if err != nil {
		hMetaJSON = []byte("{}")
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO session_provider_handles (session_id, provider, handle, model, created_at, updated_at, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, provider) DO UPDATE SET
			handle = excluded.handle,
			model = excluded.model,
			updated_at = excluded.updated_at,
			metadata_json = excluded.metadata_json;
	`, handle.SessionID, handle.Provider, handle.Handle, handle.Model, handle.CreatedAt.Format(time.RFC3339Nano), handle.UpdatedAt.Format(time.RFC3339Nano), string(hMetaJSON))
	return err
}

func (s *ConversationStore) GetProviderHandle(ctx context.Context, sessionID, provider string) (*ProviderHandle, error) {
	var ph ProviderHandle
	var modelStr, hMetaJSON sql.NullString
	var createdAtStr, updatedAtStr string

	err := s.db.QueryRowContext(ctx, `
		SELECT handle, model, created_at, updated_at, metadata_json
		FROM session_provider_handles WHERE session_id = ? AND provider = ?;
	`, sessionID, provider).Scan(&ph.Handle, &modelStr, &createdAtStr, &updatedAtStr, &hMetaJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	ph.SessionID = sessionID
	ph.Provider = provider
	if modelStr.Valid {
		ph.Model = modelStr.String
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
		ph.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
		ph.UpdatedAt = t
	}
	if hMetaJSON.Valid && hMetaJSON.String != "" {
		_ = json.Unmarshal([]byte(hMetaJSON.String), &ph.Metadata)
	}
	return &ph, nil
}

func (s *ConversationStore) ListProviderHandles(ctx context.Context, sessionID string) ([]*ProviderHandle, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, handle, model, created_at, updated_at, metadata_json
		FROM session_provider_handles WHERE session_id = ? ORDER BY provider ASC;
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*ProviderHandle
	for rows.Next() {
		var ph ProviderHandle
		var modelStr, hMetaJSON sql.NullString
		var createdAtStr, updatedAtStr string
		if err := rows.Scan(&ph.Provider, &ph.Handle, &modelStr, &createdAtStr, &updatedAtStr, &hMetaJSON); err != nil {
			return nil, err
		}
		ph.SessionID = sessionID
		if modelStr.Valid {
			ph.Model = modelStr.String
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			ph.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, updatedAtStr); err == nil {
			ph.UpdatedAt = t
		}
		if hMetaJSON.Valid && hMetaJSON.String != "" {
			_ = json.Unmarshal([]byte(hMetaJSON.String), &ph.Metadata)
		}
		list = append(list, &ph)
	}
	return list, nil
}
