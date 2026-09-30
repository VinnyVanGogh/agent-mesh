package governance

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// WatchdogEval is a single watchdog evaluation record.
type WatchdogEval struct {
	ID           int64  `json:"id"`
	TaskID       string `json:"task_id"`
	TriggerEvent string `json:"trigger_event"`
	Passed       bool   `json:"passed"`
	Verdict      string `json:"verdict"`
	EvaluatedAt  string `json:"evaluated_at"`
}

// TriggerWatchdogEval runs the watchdog for a task on a given event.
// Called from AddTaskComment and any deliverable-status change. Zero polling.
func TriggerWatchdogEval(db *sql.DB, taskID, triggerEvent string) error {
	cfg, err := GetGovernanceConfig(db, taskID)
	if err != nil || cfg == nil || cfg.WatchdogPrompt == "" {
		return nil // no watchdog configured
	}

	// Evaluate the task's deliverables against the watchdog prompt/criteria.
	passed, verdict := evaluateCriteria(db, taskID, cfg.WatchdogPrompt)

	// Persist the evaluation record.
	_, err = db.Exec(
		`INSERT INTO task_watchdog_evals (task_id, trigger_event, passed, verdict) VALUES (?, ?, ?, ?)`,
		taskID, triggerEvent, boolToInt(passed), verdict,
	)
	if err != nil {
		return fmt.Errorf("governance: persist watchdog eval: %w", err)
	}

	actorID := cfg.WatchdogAgentID
	if actorID == "" {
		actorID = "watchdog"
	}
	_ = LogEvent(db, taskID, actorID, AuditWatchdogEval, nil, nil, map[string]any{
		"trigger_event": triggerEvent,
		"passed":        passed,
		"verdict":       verdict,
	})

	if !passed {
		// Block the task so agents cannot mark it done prematurely.
		_, _ = db.Exec(
			`UPDATE tasks SET is_blocked = 1, block_reason = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
			"watchdog: "+verdict, taskID,
		)
		slog.Warn("watchdog blocked task", slog.String("task", taskID), slog.String("verdict", verdict))
	} else {
		// Clear watchdog-set block if criteria now pass.
		var blockReason string
		_ = db.QueryRow(`SELECT COALESCE(block_reason,'') FROM tasks WHERE id = ?`, taskID).Scan(&blockReason)
		if strings.HasPrefix(blockReason, "watchdog:") {
			_, _ = db.Exec(
				`UPDATE tasks SET is_blocked = 0, block_reason = '', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
				taskID,
			)
		}
	}

	return nil
}

// evaluateCriteria checks the task's work products and documents against the watchdog prompt.
// This is a structural/keyword evaluator; a production version would call an LLM.
func evaluateCriteria(db *sql.DB, taskID, prompt string) (passed bool, verdict string) {
	// Collect deliverable evidence: work products + latest docs.
	var workProductCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM task_work_products WHERE task_id = ?`, taskID).Scan(&workProductCount)

	var latestDoc string
	_ = db.QueryRow(
		`SELECT COALESCE(content,'') FROM task_documents WHERE task_id = ? ORDER BY version DESC LIMIT 1`,
		taskID,
	).Scan(&latestDoc)

	promptLower := strings.ToLower(prompt)
	evidence := strings.ToLower(latestDoc)

	// Simple keyword gate: watchdog prompt declares required keywords/sections.
	// Format: "requires: <keyword1>, <keyword2>" triggers a keyword check.
	const requiresPrefix = "requires:"
	idx := strings.Index(promptLower, requiresPrefix)
	if idx >= 0 {
		requiredPart := promptLower[idx+len(requiresPrefix):]
		// Take up to the next newline or end.
		if nl := strings.Index(requiredPart, "\n"); nl >= 0 {
			requiredPart = requiredPart[:nl]
		}
		keywords := strings.Split(requiredPart, ",")
		missing := []string{}
		for _, kw := range keywords {
			kw = strings.TrimSpace(kw)
			if kw == "" {
				continue
			}
			if !strings.Contains(evidence, kw) {
				missing = append(missing, kw)
			}
		}
		if len(missing) > 0 {
			return false, fmt.Sprintf("missing required content: %s", strings.Join(missing, ", "))
		}
	}

	// Gate: if prompt demands a work product and none exist, block.
	if strings.Contains(promptLower, "work_product") && workProductCount == 0 {
		return false, "no work product registered; deliverable required"
	}

	return true, "criteria satisfied"
}

// GetWatchdogEvals returns recent watchdog evaluations for a task.
func GetWatchdogEvals(db *sql.DB, taskID string, limit int) ([]WatchdogEval, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(
		`SELECT id, task_id, trigger_event, passed, verdict, evaluated_at
		 FROM task_watchdog_evals WHERE task_id = ? ORDER BY evaluated_at DESC LIMIT ?`,
		taskID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var evals []WatchdogEval
	for rows.Next() {
		var e WatchdogEval
		var passed int
		if err := rows.Scan(&e.ID, &e.TaskID, &e.TriggerEvent, &passed, &e.Verdict, &e.EvaluatedAt); err != nil {
			return nil, err
		}
		e.Passed = passed == 1
		evals = append(evals, e)
	}
	return evals, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
