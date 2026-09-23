package telemetry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AgentSession struct {
	ID              string    `json:"id"`
	AgentType       string    `json:"agent_type"` // claude, gemini, codex, other
	RepoPath        string    `json:"repo_path"`
	GitBranch       string    `json:"git_branch"`
	PID             int       `json:"pid"`
	Hostname        string    `json:"hostname"`
	Status          string    `json:"status"` // active, idle, closed
	StartedAt       time.Time `json:"started_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	MetadataJSON    string    `json:"metadata_json,omitempty"`
}

type CollisionWarning struct {
	FilePath       string    `json:"file_path"`
	OtherSessionID string    `json:"other_session_id"`
	OtherAgent     string    `json:"other_agent"`
	OtherPID       int       `json:"other_pid"`
	AccessType     string    `json:"access_type"`
	LastTouchedAt  time.Time `json:"last_touched_at"`
}

// HeartbeatSession registers or refreshes an active agent session in mesh.db
func HeartbeatSession(meshDB *sql.DB, sess AgentSession) error {
	if meshDB == nil {
		return nil
	}

	if sess.Hostname == "" {
		sess.Hostname, _ = os.Hostname()
		if sess.Hostname == "" {
			sess.Hostname = "local"
		}
	}
	if sess.Status == "" {
		sess.Status = "active"
	}
	if sess.GitBranch == "" {
		sess.GitBranch = "main"
	}
	if sess.AgentType == "" {
		sess.AgentType = "other"
	}

	nowStr := time.Now().UTC().Format(time.RFC3339Nano)

	query := `
	INSERT INTO agent_sessions (
		id, agent_type, repo_path, git_branch, pid, hostname, status, started_at, last_heartbeat_at, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		last_heartbeat_at = excluded.last_heartbeat_at,
		status = excluded.status,
		git_branch = excluded.git_branch,
		pid = excluded.pid;
	`
	_, err := meshDB.Exec(
		query,
		sess.ID,
		sess.AgentType,
		sess.RepoPath,
		sess.GitBranch,
		sess.PID,
		sess.Hostname,
		sess.Status,
		nowStr,
		nowStr,
		sess.MetadataJSON,
	)
	return err
}

// CloseSession marks an agent session as closed and removes its working files
func CloseSession(meshDB *sql.DB, sessionID string) error {
	if meshDB == nil {
		return nil
	}

	_, _ = meshDB.Exec(`UPDATE agent_sessions SET status = 'closed' WHERE id = ?;`, sessionID)
	_, err := meshDB.Exec(`DELETE FROM agent_working_files WHERE session_id = ?;`, sessionID)
	return err
}

// RecordWorkingFile marks a file as actively read, written, or locked by a session
func RecordWorkingFile(meshDB *sql.DB, sessionID, repoPath, filePath, accessType string, ttl time.Duration) error {
	if meshDB == nil || sessionID == "" || filePath == "" {
		return nil
	}

	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if accessType == "" {
		accessType = "write"
	}

	// Normalize file path relative to repoPath if possible
	cleanFile := filepath.Clean(filePath)
	if filepath.IsAbs(cleanFile) && strings.HasPrefix(cleanFile, repoPath) {
		if rel, err := filepath.Rel(repoPath, cleanFile); err == nil {
			cleanFile = rel
		}
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	expiresStr := now.Add(ttl).Format(time.RFC3339Nano)

	query := `
	INSERT INTO agent_working_files (
		session_id, repo_path, file_path, access_type, first_touched_at, last_touched_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(session_id, file_path) DO UPDATE SET
		last_touched_at = excluded.last_touched_at,
		expires_at = excluded.expires_at,
		access_type = excluded.access_type;
	`
	_, err := meshDB.Exec(query, sessionID, repoPath, cleanFile, accessType, nowStr, nowStr, expiresStr)
	return err
}

// CheckCollisions inspects if any other active agent sessions are currently working
// on the same files or directory within this repository.
func CheckCollisions(meshDB *sql.DB, currentSessionID, repoPath string, filePaths []string) ([]CollisionWarning, error) {
	if meshDB == nil {
		return nil, nil
	}

	nowStr := time.Now().UTC().Format(time.RFC3339Nano)

	baseQuery := `
	SELECT w.file_path, w.session_id, s.agent_type, s.pid, w.access_type, w.last_touched_at
	FROM agent_working_files w
	JOIN agent_sessions s ON w.session_id = s.id
	WHERE w.session_id != ?
	  AND (w.repo_path = ? OR ? LIKE w.repo_path || '%')
	  AND w.expires_at > ?
	  AND s.status = 'active'
	  AND s.last_heartbeat_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-15 minutes')
	`

	var args []interface{}
	args = append(args, currentSessionID, repoPath, repoPath, nowStr)

	// If specific files given, filter by them
	if len(filePaths) > 0 {
		var cleanFiles []string
		for _, fp := range filePaths {
			cf := filepath.Clean(fp)
			if filepath.IsAbs(cf) && strings.HasPrefix(cf, repoPath) {
				if rel, err := filepath.Rel(repoPath, cf); err == nil {
					cf = rel
				}
			}
			cleanFiles = append(cleanFiles, cf)
		}

		placeholders := make([]string, len(cleanFiles))
		for i, cf := range cleanFiles {
			placeholders[i] = "?"
			args = append(args, cf)
		}
		baseQuery += fmt.Sprintf(" AND w.file_path IN (%s)", strings.Join(placeholders, ","))
	}

	baseQuery += " ORDER BY w.last_touched_at DESC;"

	rows, err := meshDB.Query(baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var warnings []CollisionWarning
	for rows.Next() {
		var cw CollisionWarning
		var touchedAtStr string
		if err := rows.Scan(&cw.FilePath, &cw.OtherSessionID, &cw.OtherAgent, &cw.OtherPID, &cw.AccessType, &touchedAtStr); err != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, touchedAtStr); err == nil {
			cw.LastTouchedAt = t
		}
		warnings = append(warnings, cw)
	}

	return warnings, nil
}

// PruneWorkingFiles cleans expired files from the registry
func PruneWorkingFiles(meshDB *sql.DB) error {
	if meshDB == nil {
		return nil
	}
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := meshDB.Exec("DELETE FROM agent_working_files WHERE expires_at <= ?;", nowStr)
	return err
}
