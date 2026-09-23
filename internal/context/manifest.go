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
	ManifestFile    string    `json:"manifest_file,omitempty"`
}

// GetHandoffsDir returns the base directory where handoffs and manifests are stored.
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

// CleanRepoSlug produces a safe subdirectory name for the repository.
func CleanRepoSlug(repoName, repoPath string) string {
	name := repoName
	if name == "" && repoPath != "" {
		name = filepath.Base(repoPath)
	}
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
	clean = strings.Trim(clean, "_-.")
	if clean == "" {
		clean = "global"
	}
	return clean
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

// SaveHandoffWithManifest persists both the markdown handoff and its metadata manifest
// in a repository-specific subdirectory: handoffs/<repo_slug>/{id}-{timestamp}.md/json,
// writes /tmp/ai-handoff.md, and prunes older handoffs exceeding maxKeepPerRepo.
func SaveHandoffWithManifest(dataDir string, manifest HandoffManifest, handoffMarkdown string, maxKeepPerRepo int) (*HandoffManifest, error) {
	if maxKeepPerRepo <= 0 {
		maxKeepPerRepo = 3
	}

	baseHandoffsDir := GetHandoffsDir(dataDir)
	repoSlug := CleanRepoSlug(manifest.RepoName, manifest.RepoPath)
	repoDir := filepath.Join(baseHandoffsDir, repoSlug)

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create repo handoffs directory %s: %w", repoDir, err)
	}

	safeID := CleanSessionFilename(manifest.SessionID)
	if safeID == "" {
		safeID = fmt.Sprintf("session-%d", time.Now().Unix())
	}
	manifest.SessionID = safeID

	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}

	timestampStr := manifest.CreatedAt.UTC().Format("20060102-150405")
	fileBase := fmt.Sprintf("%s-%s", safeID, timestampStr)

	handoffPath := filepath.Join(repoDir, fileBase+".md")
	manifestPath := filepath.Join(repoDir, fileBase+".json")

	manifest.HandoffFile = handoffPath
	manifest.ManifestFile = manifestPath

	// 1. Write Markdown file: {id}-{timestamp}.md
	if err := os.WriteFile(handoffPath, []byte(handoffMarkdown), 0644); err != nil {
		return nil, fmt.Errorf("failed to write handoff file %s: %w", handoffPath, err)
	}

	// 2. Write Manifest JSON file: {id}-{timestamp}.json
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write manifest file %s: %w", manifestPath, err)
	}

	// 3. Write /tmp/ai-handoff.md for quick access
	_ = os.WriteFile("/tmp/ai-handoff.md", []byte(handoffMarkdown), 0644)

	// 4. Prune older handoffs in this repository subdirectory
	_, _ = PruneHandoffs(baseHandoffsDir, manifest.RepoPath, maxKeepPerRepo)

	return &manifest, nil
}

// PruneHandoffs removes older manifests and markdown files for repoPath if exceeding maxKeep.
func PruneHandoffs(baseHandoffsDir, repoPath string, maxKeep int) (int, error) {
	if maxKeep <= 0 {
		maxKeep = 3
	}

	manifests, err := ListManifests(baseHandoffsDir, repoPath)
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
		if m.ManifestFile != "" {
			_ = os.Remove(m.ManifestFile)
		}
		if m.HandoffFile != "" {
			_ = os.Remove(m.HandoffFile)
		}
		pruned++
	}

	return pruned, nil
}

// ListManifests returns all manifests matching repoFilter (or all if repoFilter is empty),
// sorted newest first. Recursively scans repository subdirectories.
func ListManifests(baseHandoffsDir string, repoFilter string) ([]HandoffManifest, error) {
	if _, err := os.Stat(baseHandoffsDir); os.IsNotExist(err) {
		return []HandoffManifest{}, nil
	}

	var results []HandoffManifest
	repoFilter = strings.TrimSpace(repoFilter)
	targetSlug := ""
	if repoFilter != "" {
		targetSlug = CleanRepoSlug(filepath.Base(repoFilter), repoFilter)
	}

	_ = filepath.Walk(baseHandoffsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Support both {id}-{timestamp}.json and legacy *-manifest.json
		if !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var m HandoffManifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil
		}

		if m.ManifestFile == "" {
			m.ManifestFile = path
		}
		if m.HandoffFile == "" {
			m.HandoffFile = strings.TrimSuffix(path, ".json") + ".md"
		}

		if repoFilter != "" {
			relDir := filepath.Base(filepath.Dir(path))
			if relDir != targetSlug &&
				m.RepoPath != repoFilter &&
				m.RepoName != filepath.Base(repoFilter) &&
				CleanRepoSlug(m.RepoName, m.RepoPath) != targetSlug {
				return nil
			}
		}

		results = append(results, m)
		return nil
	})

	// Sort newest first
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	return results, nil
}

// SearchManifests searches manifests in baseHandoffsDir by query across title, goal, session_id,
// task name, or branch, filtered optionally by repoFilter.
func SearchManifests(baseHandoffsDir, repoFilter, query string) ([]HandoffManifest, error) {
	manifests, err := ListManifests(baseHandoffsDir, repoFilter)
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

// LoadManifest reads a specific manifest by sessionID (prefix match supported).
func LoadManifest(baseHandoffsDir, sessionID string) (*HandoffManifest, error) {
	manifests, err := ListManifests(baseHandoffsDir, "")
	if err != nil {
		return nil, err
	}

	cleanID := CleanSessionFilename(sessionID)
	for _, m := range manifests {
		if m.SessionID == sessionID || m.SessionID == cleanID || strings.HasPrefix(m.SessionID, cleanID) {
			return &m, nil
		}
	}

	return nil, fmt.Errorf("manifest not found for session %s", sessionID)
}

// LoadHandoffMarkdown reads the markdown content for a given sessionID (prefix match supported).
func LoadHandoffMarkdown(baseHandoffsDir, sessionID string) (string, error) {
	m, err := LoadManifest(baseHandoffsDir, sessionID)
	if err == nil && m.HandoffFile != "" {
		if data, err := os.ReadFile(m.HandoffFile); err == nil {
			return string(data), nil
		}
	}

	cleanID := CleanSessionFilename(sessionID)
	var foundPath string
	_ = filepath.Walk(baseHandoffsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".md") && strings.HasPrefix(info.Name(), cleanID) {
			foundPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if foundPath != "" {
		data, err := os.ReadFile(foundPath)
		if err == nil {
			return string(data), nil
		}
	}

	return "", fmt.Errorf("handoff file not found for session %s", sessionID)
}
