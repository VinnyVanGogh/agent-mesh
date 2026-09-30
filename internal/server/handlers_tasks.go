package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/context"
)

type TasksHandler struct {
	db  *sql.DB
	hub *EventHub
}

func NewTasksHandler(db *sql.DB, hub *EventHub) *TasksHandler {
	return &TasksHandler{db: db, hub: hub}
}

// ListTasks handles GET /api/tasks
func (h *TasksHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			if l > 1000 {
				l = 1000
			}
			limit = l
		}
	}
	offset := 0
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	tasks, err := context.ListTasks(h.db, status == "all" || status == "done" || status == "soft_deleted")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter by specific status if requested and not "all"
	var filtered []context.Task
	for _, t := range tasks {
		if status == "" || status == "all" || strings.EqualFold(t.Status, status) {
			filtered = append(filtered, t)
		}
	}

	total := len(filtered)
	// Apply offset and limit
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	slice := filtered[start:end]

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tasks":    slice,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
		"has_more": end < total,
	})
}

// GetTask handles GET /api/tasks/{id}
func (h *TasksHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	task, err := context.GetTask(h.db, id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	comments, _ := context.GetTaskComments(h.db, task.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"task":     task,
		"comments": comments,
	})
}

// CreateTask handles POST /api/tasks
func (h *TasksHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string  `json:"name"`
		RepoPath     string  `json:"repo_path"`
		GitBranch    string  `json:"git_branch"`
		AccountRole  string  `json:"account_role"`
		MaxBudgetUSD float64 `json:"max_budget_usd"`
		MaxTurns     int     `json:"max_turns"`
		Organization string  `json:"organization"`
		Project      string  `json:"project"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "task name is required")
		return
	}

	opts := context.TaskCreateOptions{
		Name:         req.Name,
		RepoPath:     req.RepoPath,
		GitBranch:    req.GitBranch,
		AccountRole:  req.AccountRole,
		MaxBudgetUSD: req.MaxBudgetUSD,
		MaxTurns:     req.MaxTurns,
		Organization: req.Organization,
		Project:      req.Project,
	}

	task, err := context.CreateTaskWithOptions(h.db, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_created", task)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(task)
}

// GetComments handles GET /api/tasks/{id}/comments
func (h *TasksHandler) GetComments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	comments, err := context.GetTaskComments(h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"comments": comments,
	})
}

// AddComment handles POST /api/tasks/{id}/comments
func (h *TasksHandler) AddComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	var req struct {
		Author  string `json:"author"`
		Message string `json:"message"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Message == "" && req.Body != "" {
		req.Message = req.Body
	}

	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "comment message is required")
		return
	}
	if req.Author == "" {
		req.Author = "user"
	}

	if err := context.AddTaskComment(h.db, id, req.Author, req.Message); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add comment: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_comment_added", map[string]string{
			"task_id": id,
			"author":  req.Author,
			"message": req.Message,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// MarkDone handles POST /api/tasks/{id}/done
func (h *TasksHandler) MarkDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	if err := context.MarkTaskDone(h.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark task done: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_done", map[string]string{"task_id": id})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// BlockTask handles POST /api/tasks/{id}/block
func (h *TasksHandler) BlockTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := context.BlockTask(h.db, id, req.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to block task: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_blocked", map[string]string{"task_id": id, "reason": req.Reason})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// UnblockTask handles POST /api/tasks/{id}/unblock
func (h *TasksHandler) UnblockTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	if err := context.UnblockTask(h.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unblock task: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_unblocked", map[string]string{"task_id": id})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// SetStage handles POST /api/tasks/{id}/stage
func (h *TasksHandler) SetStage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	var req struct {
		Stage string `json:"stage"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	req.Stage = strings.TrimSpace(req.Stage)
	if req.Stage == "" {
		writeError(w, http.StatusBadRequest, "stage is required")
		return
	}

	switch req.Stage {
	case "todo", "in_progress", "in_review", "done":
	default:
		writeError(w, http.StatusBadRequest, "invalid stage: must be todo, in_progress, in_review, or done")
		return
	}

	if err := context.SetTaskExecutionStage(h.db, id, req.Stage); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update task stage: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Publish("task_stage_changed", map[string]string{
			"task_id": id,
			"stage":   req.Stage,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"task_id": id,
		"stage":   req.Stage,
	})
}

