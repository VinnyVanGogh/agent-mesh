package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

type SessionsHandler struct {
	db  *sql.DB
	hub *EventHub
}

func NewSessionsHandler(db *sql.DB, hub *EventHub) *SessionsHandler {
	return &SessionsHandler{db: db, hub: hub}
}

// AgentSession represents a session record in agent_sessions
type AgentSession struct {
	ID              string    `json:"id"`
	AgentType       string    `json:"agent_type"`
	RepoPath        string    `json:"repo_path"`
	GitBranch       string    `json:"git_branch"`
	PID             int       `json:"pid"`
	Hostname        string    `json:"hostname"`
	Status          string    `json:"status"`
	StartedAt       time.Time `json:"started_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	MetadataJSON    string    `json:"metadata_json,omitempty"`
}

// ListSessions handles GET /api/sessions
func (h *SessionsHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	repoPath := r.URL.Query().Get("repo_path")

	query := `
		SELECT id, agent_type, repo_path, git_branch, COALESCE(pid, 0), hostname, status, started_at, last_heartbeat_at, COALESCE(metadata_json, '')
		FROM agent_sessions
		WHERE 1=1
	`
	var args []any
	if status != "" && status != "all" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if repoPath != "" {
		query += " AND repo_path = ?"
		args = append(args, repoPath)
	}
	query += " ORDER BY last_heartbeat_at DESC LIMIT 100"

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query sessions: "+err.Error())
		return
	}
	defer rows.Close()

	var sessions []AgentSession
	for rows.Next() {
		var s AgentSession
		var startedAtStr, heartbeatStr string
		if err := rows.Scan(&s.ID, &s.AgentType, &s.RepoPath, &s.GitBranch, &s.PID, &s.Hostname, &s.Status, &startedAtStr, &heartbeatStr, &s.MetadataJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan session: "+err.Error())
			return
		}
		if t, err := time.Parse(time.RFC3339Nano, startedAtStr); err == nil {
			s.StartedAt = t
		}
		if t, err := time.Parse(time.RFC3339Nano, heartbeatStr); err == nil {
			s.LastHeartbeatAt = t
		}
		sessions = append(sessions, s)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessions": sessions,
	})
}

// ListAgents handles GET /api/agents — returns active sessions as assignable agents for the UI.
func (h *SessionsHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, agent_type, repo_path, hostname, status, last_heartbeat_at
		FROM agent_sessions
		WHERE status IN ('active', 'idle')
		ORDER BY last_heartbeat_at DESC
		LIMIT 200
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query agents: "+err.Error())
		return
	}
	defer rows.Close()

	type Agent struct {
		ID              string `json:"id"`
		AgentType       string `json:"agent_type"`
		RepoPath        string `json:"repo_path"`
		Hostname        string `json:"hostname"`
		Status          string `json:"status"`
		LastHeartbeatAt string `json:"last_heartbeat_at"`
		Label           string `json:"label"`
	}

	agents := []Agent{}
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.AgentType, &a.RepoPath, &a.Hostname, &a.Status, &a.LastHeartbeatAt); err != nil {
			continue
		}
		a.Label = a.AgentType + " / " + a.ID[:min(8, len(a.ID))]
		agents = append(agents, a)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"agents": agents})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetSession handles GET /api/sessions/{id}
func (h *SessionsHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var s AgentSession
	var startedAtStr, heartbeatStr string
	query := `
		SELECT id, agent_type, repo_path, git_branch, COALESCE(pid, 0), hostname, status, started_at, last_heartbeat_at, COALESCE(metadata_json, '')
		FROM agent_sessions
		WHERE id = ?;
	`
	err := h.db.QueryRowContext(r.Context(), query, id).Scan(
		&s.ID, &s.AgentType, &s.RepoPath, &s.GitBranch, &s.PID, &s.Hostname, &s.Status, &startedAtStr, &heartbeatStr, &s.MetadataJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if t, err := time.Parse(time.RFC3339Nano, startedAtStr); err == nil {
		s.StartedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, heartbeatStr); err == nil {
		s.LastHeartbeatAt = t
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

// RegisterSession handles POST /api/sessions
func (h *SessionsHandler) RegisterSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		AgentType string `json:"agent_type"`
		RepoPath  string `json:"repo_path"`
		GitBranch string `json:"git_branch"`
		PID       int    `json:"pid"`
		Hostname  string `json:"hostname"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.ID == "" || req.AgentType == "" || req.RepoPath == "" {
		writeError(w, http.StatusBadRequest, "id, agent_type, and repo_path are required")
		return
	}

	if req.Hostname == "" {
		req.Hostname = "local"
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := `
		INSERT INTO agent_sessions (id, agent_type, repo_path, git_branch, pid, hostname, status, started_at, last_heartbeat_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			last_heartbeat_at = excluded.last_heartbeat_at,
			status = 'active';
	`
	_, err := h.db.ExecContext(r.Context(), query, req.ID, req.AgentType, req.RepoPath, req.GitBranch, req.PID, req.Hostname, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register session: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("session_registered", map[string]any{
			"id":         req.ID,
			"agent_type": req.AgentType,
			"repo_path":  req.RepoPath,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": req.ID})
}

// Heartbeat handles POST /api/sessions/{id}/heartbeat
func (h *SessionsHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := h.db.ExecContext(r.Context(), `UPDATE agent_sessions SET last_heartbeat_at = ? WHERE id = ?`, now, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to heartbeat: "+err.Error())
		return
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	if h.hub != nil {
		h.hub.Publish("session_heartbeat", map[string]string{"id": id, "timestamp": now})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// CloseSession handles POST /api/sessions/{id}/close
func (h *SessionsHandler) CloseSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	res, err := h.db.ExecContext(r.Context(), `UPDATE agent_sessions SET status = 'closed' WHERE id = ?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close session: "+err.Error())
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	if h.hub != nil {
		h.hub.Publish("session_closed", map[string]string{"id": id})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
