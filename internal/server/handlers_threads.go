package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/conversation"
)

type ThreadsHandler struct {
	db    *sql.DB
	store *conversation.ConversationStore
	hub   *EventHub
}

func NewThreadsHandler(db *sql.DB, hub *EventHub) *ThreadsHandler {
	return &ThreadsHandler{
		db:    db,
		store: conversation.NewStore(db),
		hub:   hub,
	}
}

// ListThreads handles GET /api/threads
func (h *ThreadsHandler) ListThreads(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	sessions, err := h.store.ListSessions(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list threads: "+err.Error())
		return
	}
	if sessions == nil {
		sessions = []*conversation.SessionSummary{}
	}

	var total int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM chat_sessions`).Scan(&total)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"threads":  sessions,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
		"has_more": offset+len(sessions) < total,
	})
}

// CreateThread handles POST /api/threads
func (h *ThreadsHandler) CreateThread(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title        string `json:"title"`
		RepoPath     string `json:"repo_path"`
		SystemPrompt string `json:"system_prompt"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	conv := &conversation.Conversation{
		Title:        strings.TrimSpace(req.Title),
		RepoPath:     strings.TrimSpace(req.RepoPath),
		SystemPrompt: strings.TrimSpace(req.SystemPrompt),
	}

	if err := h.store.CreateSession(r.Context(), conv); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create thread: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("thread_created", map[string]string{
			"id":    conv.ID,
			"title": conv.Title,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(conv)
}

// GetThread handles GET /api/threads/{id}
func (h *ThreadsHandler) GetThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "thread id is required")
		return
	}

	conv, err := h.store.GetSession(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(conv)
}

// GetMessages handles GET /api/threads/{id}/messages with pagination for long threads.
func (h *ThreadsHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "thread id is required")
		return
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			if l > 200 {
				limit = 200
			} else {
				limit = l
			}
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	if _, err := h.store.GetSession(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var total int
	err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM chat_messages WHERE session_id = ?`, id).Scan(&total)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count messages: "+err.Error())
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, sequence_num, role, content, token_count, created_at, metadata_json
		FROM chat_messages
		WHERE session_id = ?
		ORDER BY sequence_num ASC
		LIMIT ? OFFSET ?;
	`, id, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query messages: "+err.Error())
		return
	}
	defer rows.Close()

	var messages []conversation.Message
	for rows.Next() {
		var m conversation.Message
		var roleStr, createdAtStr string
		var metaJSON sql.NullString
		if err := rows.Scan(&m.ID, &m.SequenceNum, &roleStr, &m.Content, &m.TokenCount, &createdAtStr, &metaJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan message: "+err.Error())
			return
		}
		m.SessionID = id
		m.Role = conversation.Role(roleStr)
		if t, err := time.Parse(time.RFC3339Nano, createdAtStr); err == nil {
			m.CreatedAt = t
		}
		if metaJSON.Valid && metaJSON.String != "" {
			var mm map[string]interface{}
			if err := json.Unmarshal([]byte(metaJSON.String), &mm); err == nil {
				m.Metadata = mm
			}
		}
		messages = append(messages, m)
	}

	if messages == nil {
		messages = []conversation.Message{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": id,
		"messages":   messages,
		"total":      total,
		"limit":      limit,
		"offset":     offset,
		"has_more":   offset+len(messages) < total,
	})
}

// AppendMessage handles POST /api/threads/{id}/messages
func (h *ThreadsHandler) AppendMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "thread id is required")
		return
	}

	var req struct {
		Role       string                 `json:"role"`
		Content    string                 `json:"content"`
		TokenCount int                    `json:"token_count"`
		Metadata   map[string]interface{} `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "message content is required")
		return
	}

	if req.Role == "" {
		req.Role = "user"
	}

	switch conversation.Role(req.Role) {
	case conversation.RoleSystem, conversation.RoleUser, conversation.RoleAssistant, conversation.RoleTool:
	default:
		writeError(w, http.StatusBadRequest, "invalid role: must be system, user, assistant, or tool")
		return
	}

	msg := &conversation.Message{
		SessionID:  id,
		Role:       conversation.Role(req.Role),
		Content:    req.Content,
		TokenCount: req.TokenCount,
		Metadata:   req.Metadata,
	}

	if _, err := h.store.GetSession(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := h.store.AppendMessage(r.Context(), id, msg); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to append message: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("thread_message_appended", map[string]any{
			"session_id": id,
			"message":    msg,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(msg)
}
