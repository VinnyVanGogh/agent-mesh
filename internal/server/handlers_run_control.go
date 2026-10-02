package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
)

var validRunControlActions = map[string]bool{
	"pause":   true,
	"resume":  true,
	"stop":    true,
	"message": true,
}

// RunControl handles POST /api/tasks/{id}/run-control
//
//	body: {"action":"pause"|"resume"|"stop"|"message","text":"..."}
func (h *TasksHandler) RunControl(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	task, err := context.GetTask(h.db, id)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	var req struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	if !validRunControlActions[req.Action] {
		writeError(w, http.StatusBadRequest, "action must be one of: pause, resume, stop, message")
		return
	}

	// Only allow control actions on in-progress or paused tasks.
	stage := task.ExecutionStage
	if stage == "" {
		stage = task.Status
	}
	if req.Action != "stop" {
		switch stage {
		case "in_progress", "paused":
			// allowed
		default:
			writeError(w, http.StatusConflict, "task is not in_progress or paused")
			return
		}
	}

	rc := orchestrator.GlobalRunControl

	switch req.Action {
	case "pause":
		if err := rc.SetPause(id, true); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to set pause: "+err.Error())
			return
		}
	case "resume":
		if err := rc.SetPause(id, false); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resume: "+err.Error())
			return
		}
	case "stop":
		if err := rc.SetStop(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop: "+err.Error())
			return
		}
	case "message":
		if strings.TrimSpace(req.Text) == "" {
			writeError(w, http.StatusBadRequest, "text is required for message action")
			return
		}
		if err := rc.InjectMessage(id, req.Text); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to inject message: "+err.Error())
			return
		}
	}

	if h.hub != nil {
		h.hub.Publish("run_control", map[string]string{
			"task_id": id,
			"action":  req.Action,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "action": req.Action})
}
