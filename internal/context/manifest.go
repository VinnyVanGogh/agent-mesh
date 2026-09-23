package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HandoffManifest contains structured metadata for a session handoff.
type HandoffManifest struct {
	SessionID       string    `json:"session_id"`
	Title           string    `json:"title"`
	Goal            string    `json:"goal"`
	RepoPath        string    `json:"repo_path"`
	RepoName        string    `json:"repo_name"`
	GitBranch       string    `json:"git_branch"`
	AgentType       string    `json:"agent_type"` // "claude" or "gemini"
	CreatedAt       time.Time `json:"created_at"`
	TotalUserTurns  int       `json:"total_user_turns"`
	DirectivesCount int       `json:"directives_count"`
	ActiveTaskID    string    `json:"active_task_id,omitempty"`
	ActiveTaskName  string    `json:"active_task_name,omitempty"`
	ModifiedFiles   []string  `json:"modified_files,omitempty"`
	Trigger         string    `json:"trigger"` // "manual", "auto_daemon", "crash", "exit", "breaker", "quota_warning"
	HandoffFile     string    `json:"handoff_file"`
}

// GetHandoffsDir returns the directory where handoffs and manifests are stored.
func GetHandoffsDir(dataDir string) string {
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataDir = filepath.Join(home, ".agent-mesh")
	}
	return filepath.Join(dataDir, "handoffs")
}

// CleanSessionFilename produces a safe file basename from a session ID.
func CleanSessionFilename(sessionID string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, sessionID)
	clean = strings.Trim(clean, "_-")
	if clean == "" {
		clean = "session"
	}
	return clean
}

// SaveHandoffWithManifest persists both the markdown handoff and its metadata manifest,
// copies to /tmp/ai-handoff.md, and prunes older handoffs for the repo beyond maxKeepPerRepo.
func SaveHandoffWithManifest(dataDir string, manifest HandoffManifest, handoffMarkdown string, maxKeepPerRepo int) (*HandoffManifest, error) {
	if maxKeepPerRepo <= 0 {
		maxKeepPerRepo = 3
	}

	handoffsDir := GetHandoffsDir(dataDir)
	if err := os.MkdirAll(handoffsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create handoffs directory: %w", err)
	}

	safeID := CleanSessionFilename(manifest.SessionID)
	if safeID == "" {
		safeID = fmt.Sprintf("session-%d", time.Now().Unix())
	}
	manifest.SessionID = safeID

	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}

	handoffFilename := fmt.Sprintf("handoff-%s.md", safeID)
	manifestFilename := fmt.Sprintf("%s-manifest.json", safeID)

	handoffPath := filepath.Join(handoffsDir, handoffFilename)
	manifestPath := filepath.Join(handoffsDir, manifestFilename)

	manifest.HandoffFile = handoffPath

	// 1. Write Markdown file
	if err := os.WriteFile(handoffPath, []byte(handoffMarkdown), 0644); err != nil {
		return nil, fmt.Errorf("failed to write handoff file %s: %w", handoffPath, err)
	}

	// 2. Write Manifest JSON file
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write manifest file %s: %w", manifestPath, err)
	}

	// 3. Write /tmp/ai-handoff.md for quick access
	_ = os.WriteFile("/tmp/ai-handoff.md", []byte(handoffMarkdown), 0644)

	// 4. Prune older handoffs for this repo
	if manifest.RepoPath != "" {
		_, _ = PruneHandoffs(handoffsDir, manifest.RepoPath, maxKeepPerRepo)
	}

	return &manifest, nil
}

// PruneHandoffs removes older manifests and markdown files for repoPath if exceeding maxKeep.
func PruneHandoffs(handoffsDir, repoPath string, maxKeep int) (int, error) {
	if maxKeep <= 0 {
		maxKeep = 3
	}

	manifests, err := ListManifests(handoffsDir, repoPath)
	if err != nil {
		return 0, err
	}

	if len(manifests) <= maxKeep {
		return 0, nil
	}

	// Sort oldest first
	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].CreatedAt.Before(manifests[j].CreatedAt)
	})

	excess := len(manifests) - maxKeep
	pruned := 0

	for i := 0; i < excess; i++ {
		m := manifests[i]
		safeID := CleanSessionFilename(m.SessionID)
		manifestPath := filepath.Join(handoffsDir, fmt.Sprintf("%s-manifest.json", safeID))
		handoffPath := m.HandoffFile
		if handoffPath == "" {
			handoffPath = filepath.Join(handoffsDir, fmt.Sprintf("handoff-%s.md", safeID))
		}

		_ = os.Remove(manifestPath)
		_ = os.Remove(handoffPath)
		pruned++
	}

	return pruned, nil
}

// ListManifests returns all manifests matching repoFilter (or all if repoFilter is empty),
// sorted newest first.
func ListManifests(handoffsDir string, repoFilter string) ([]HandoffManifest, error) {
	entries, err := os.ReadDir(handoffsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []HandoffManifest{}, nil
		}
		return nil, err
	}

	var results []HandoffManifest
	repoFilter = strings.TrimSpace(repoFilter)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-manifest.json") {
			continue
		}

		manifestPath := filepath.Join(handoffsDir, entry.Name())
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		var m HandoffManifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}

		if repoFilter != "" {
			if m.RepoPath != repoFilter && m.RepoName != filepath.Base(repoFilter) {
				continue
			}
		}

		results = append(results, m)
	}

	// Sort newest first
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	return results, nil
}

// SearchManifests searches manifests in handoffsDir by query across title, goal, session_id,
// and task name, filtered optionally by repoFilter.
func SearchManifests(handoffsDir, repoFilter, query string) ([]HandoffManifest, error) {
	manifests, err := ListManifests(handoffsDir, repoFilter)
	if err != nil {
		return nil, err
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return manifests, nil
	}

	var matched []HandoffManifest
	for _, m := range manifests {
		if strings.Contains(strings.ToLower(m.SessionID), query) ||
			strings.Contains(strings.ToLower(m.Title), query) ||
			strings.Contains(strings.ToLower(m.Goal), query) ||
			strings.Contains(strings.ToLower(m.ActiveTaskName), query) ||
			strings.Contains(strings.ToLower(m.RepoName), query) ||
			strings.Contains(strings.ToLower(m.GitBranch), query) {
			matched = append(matched, m)
		}
	}

	return matched, nil
}

// LoadManifest reads a specific manifest by sessionID.
func LoadManifest(handoffsDir, sessionID string) (*HandoffManifest, error) {
	safeID := CleanSessionFilename(sessionID)
	manifestPath := filepath.Join(handoffsDir, fmt.Sprintf("%s-manifest.json", safeID))
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("manifest not found for session %s: %w", sessionID, err)
	}

	var m HandoffManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}
	return &m, nil
}

// LoadHandoffMarkdown reads the markdown content for a given sessionID.
func LoadHandoffMarkdown(handoffsDir, sessionID string) (string, error) {
	safeID := CleanSessionFilename(sessionID)
	// Try manifest first to get exact file path
	if m, err := LoadManifest(handoffsDir, sessionID); err == nil && m.HandoffFile != "" {
		if data, err := os.ReadFile(m.HandoffFile); err == nil {
			return string(data), nil
		}
	}

	// Fallback to default name
	handoffPath := filepath.Join(handoffsDir, fmt.Sprintf("handoff-%s.md", safeID))
	data, err := os.ReadFile(handoffPath)
	if err != nil {
		return "", fmt.Errorf("handoff file not found for session %s: %w", sessionID, err)
	}
	return string(data), nil
}
