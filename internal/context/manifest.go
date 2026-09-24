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
	Trigger         string    `json:"trigger"` // "manual", "auto_daemon", "active_session", "crash", "exit", "breaker", "quota_warning"
	HandoffFile     string    `json:"handoff_file"`
	ManifestFile    string    `json:"manifest_file,omitempty"`
}

// BranchHandoffSummary summarizes saved handoffs per branch in a repository.
type BranchHandoffSummary struct {
	Branch       string    `json:"branch"`
	RepoSlug     string    `json:"repo_slug"`
	HandoffCount int       `json:"handoff_count"`
	LatestAt     time.Time `json:"latest_at"`
	LatestGoal   string    `json:"latest_goal"`
	LatestTitle  string    `json:"latest_title"`
	LatestFile   string    `json:"latest_file"`
}

// GetHandoffsDir returns the base directory where handoffs and manifests are stored.
func GetHandoffsDir(dataDir string) string {
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		newDir := filepath.Join(home, ".staypoint")
		oldDir := filepath.Join(home, ".agent-mesh")
		if _, err := os.Stat(newDir); err == nil {
			dataDir = newDir
		} else if _, err := os.Stat(oldDir); err == nil {
			dataDir = oldDir
		} else {
			dataDir = newDir
		}
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
// maintains atomic latest.md and latest.json symlinks, writes /tmp/ai-handoff.md,
// and prunes older handoffs protecting manual handoffs from auto eviction.
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

	handoffFilename := fileBase + ".md"
	manifestFilename := fileBase + ".json"

	handoffPath := filepath.Join(repoDir, handoffFilename)
	manifestPath := filepath.Join(repoDir, manifestFilename)

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

	// 3. Maintain latest.md and latest.json symlinks in the repo subdirectory
	latestMdPath := filepath.Join(repoDir, "latest.md")
	latestJsonPath := filepath.Join(repoDir, "latest.json")
	_ = os.Remove(latestMdPath)
	_ = os.Remove(latestJsonPath)
	_ = os.Symlink(handoffFilename, latestMdPath)
	_ = os.Symlink(manifestFilename, latestJsonPath)

	// 4. Write /tmp/ai-handoff.md for quick access
	_ = os.WriteFile("/tmp/ai-handoff.md", []byte(handoffMarkdown), 0644)

	// 5. Prune older handoffs in this repository subdirectory with manual protection
	_, _ = PruneHandoffs(baseHandoffsDir, manifest.RepoPath, maxKeepPerRepo)

	return &manifest, nil
}

// PruneHandoffs removes older manifests and markdown files for repoPath if exceeding maxKeep.
// Implements "Manual Pinning": always protects manual user-created handoffs from being
// evicted by automated daemon or active session snapshots.
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

	// Partition into automated snapshots and manual/critical handoffs
	var autoList []HandoffManifest
	var manualList []HandoffManifest

	for _, m := range manifests {
		if m.Trigger == "auto_daemon" || m.Trigger == "active_session" {
			autoList = append(autoList, m)
		} else {
			manualList = append(manualList, m)
		}
	}

	// Sort oldest first in both lists
	sort.Slice(autoList, func(i, j int) bool {
		return autoList[i].CreatedAt.Before(autoList[j].CreatedAt)
	})
	sort.Slice(manualList, func(i, j int) bool {
		return manualList[i].CreatedAt.Before(manualList[j].CreatedAt)
	})

	excess := len(manifests) - maxKeep
	pruned := 0

	// Step 1: Evict oldest auto snapshots first
	for i := 0; i < len(autoList) && excess > 0; i++ {
		m := autoList[i]
		if m.ManifestFile != "" {
			_ = os.Remove(m.ManifestFile)
		}
		if m.HandoffFile != "" {
			_ = os.Remove(m.HandoffFile)
		}
		pruned++
		excess--
	}

	// Step 2: If still excess, evict oldest manual handoffs
	for i := 0; i < len(manualList) && excess > 0; i++ {
		m := manualList[i]
		if m.ManifestFile != "" {
			_ = os.Remove(m.ManifestFile)
		}
		if m.HandoffFile != "" {
			_ = os.Remove(m.HandoffFile)
		}
		pruned++
		excess--
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

		name := info.Name()
		// Ignore latest.json and latest.md symlinks to prevent duplicate accounting
		if name == "latest.json" || name == "latest.md" {
			return nil
		}

		if !strings.HasSuffix(name, ".json") {
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

// ListBranchesWithHandoffs groups all handoffs in a repo by branch and returns summaries.
func ListBranchesWithHandoffs(baseHandoffsDir, repoFilter string) ([]BranchHandoffSummary, error) {
	manifests, err := ListManifests(baseHandoffsDir, repoFilter)
	if err != nil {
		return nil, err
	}

	groups := make(map[string][]HandoffManifest)
	for _, m := range manifests {
		branch := m.GitBranch
		if branch == "" {
			branch = "unknown"
		}
		groups[branch] = append(groups[branch], m)
	}

	var summaries []BranchHandoffSummary
	for branch, list := range groups {
		if len(list) == 0 {
			continue
		}
		// Sort newest first
		sort.Slice(list, func(i, j int) bool {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		})
		latest := list[0]
		summaries = append(summaries, BranchHandoffSummary{
			Branch:       branch,
			RepoSlug:     CleanRepoSlug(latest.RepoName, latest.RepoPath),
			HandoffCount: len(list),
			LatestAt:     latest.CreatedAt,
			LatestGoal:   latest.Goal,
			LatestTitle:  latest.Title,
			LatestFile:   latest.HandoffFile,
		})
	}

	// Sort branches with most recently active first
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].LatestAt.After(summaries[j].LatestAt)
	})

	return summaries, nil
}

// GetLatestManifest fetches the latest manifest for a repository via latest.json or newest manifest.
func GetLatestManifest(baseHandoffsDir, repoPath string) (*HandoffManifest, error) {
	repoSlug := CleanRepoSlug(filepath.Base(repoPath), repoPath)
	latestJsonPath := filepath.Join(baseHandoffsDir, repoSlug, "latest.json")

	if data, err := os.ReadFile(latestJsonPath); err == nil {
		var m HandoffManifest
		if err := json.Unmarshal(data, &m); err == nil {
			return &m, nil
		}
	}

	// Fallback to ListManifests
	manifests, err := ListManifests(baseHandoffsDir, repoPath)
	if err == nil && len(manifests) > 0 {
		return &manifests[0], nil
	}

	return nil, fmt.Errorf("no handoff found for %s", repoPath)
}

// LoadManifest reads a specific manifest by sessionID (prefix match supported).
func LoadManifest(baseHandoffsDir, sessionID string) (*HandoffManifest, error) {
	if sessionID == "" || sessionID == "latest" {
		return nil, fmt.Errorf("empty session ID")
	}

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
		if info.Name() == "latest.md" {
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
