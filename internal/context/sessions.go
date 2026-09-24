package context

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SessionInfo represents a discovered agent session from Claude Code or Antigravity
type SessionInfo struct {
	ID             string    `json:"id"`
	AgentType      string    `json:"agent_type"` // "claude" or "gemini"
	RepoPath       string    `json:"repo_path"`
	GitBranch      string    `json:"git_branch,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	RootGoal       string    `json:"root_goal"`
	TotalUserTurns int       `json:"total_user_turns"`
	UserDirectives []string  `json:"user_directives"`
	LastUserPrompt string    `json:"last_user_prompt"`
	LastAssistant  string    `json:"last_assistant"`
	TranscriptPath string    `json:"transcript_path"`
}

var lowSignalPrompts = map[string]bool{
	"yes": true, "y": true, "ok": true, "okay": true, "sure": true,
	"continue": true, "proceed": true, "go": true, "go ahead": true,
	"looks good": true, "lgtm": true, "thanks": true, "thank you": true,
	"do it": true, "yes please": true, "yes, please": true,
	"yep": true, "yeah": true, "please do that": true, "done": true,
	"start": true, "start it": true, "next": true,
}

var metadataRegex = regexp.MustCompile(`(?si)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
var tagRegex = regexp.MustCompile(`(?si)</?[a-zA-Z0-9_-]+>`)

// CleanRawPrompt strips internal metadata wrappers and XML tags from prompts
func CleanRawPrompt(p string) string {
	clean := metadataRegex.ReplaceAllString(p, "")
	clean = tagRegex.ReplaceAllString(clean, "")
	return strings.TrimSpace(clean)
}

// IsSubstantiveUserPrompt filters out noise, single-word approvals, and greetings
func IsSubstantiveUserPrompt(p string) bool {
	clean := strings.ToLower(CleanRawPrompt(p))
	if clean == "" || lowSignalPrompts[clean] {
		return false
	}
	// Directives should have at least 12 chars and a space (multi-word instruction)
	return len(clean) >= 12 && strings.Contains(clean, " ")
}

// SanitizeSummary condenses a prompt into a clean single-line summary
func SanitizeSummary(p string, maxLen int) string {
	clean := CleanRawPrompt(p)
	clean = strings.Join(strings.Fields(clean), " ")
	if maxLen > 0 && len(clean) > maxLen {
		return clean[:maxLen] + "..."
	}
	return clean
}

// DiscoverSessions finds recent sessions matching a repo path across Claude Code and Antigravity
func DiscoverSessions(repoPath string, limit int, meshDB *sql.DB) ([]SessionInfo, error) {
	if limit <= 0 {
		limit = 10
	}

	cleanRepo := ""
	if repoPath != "" {
		if abs, err := filepath.Abs(repoPath); err == nil {
			cleanRepo = filepath.Clean(abs)
		}
	}

	home, _ := os.UserHomeDir()
	var sessions []SessionInfo
	seenIDs := make(map[string]bool)

	// 1. Scan Claude Code projects
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")
	if entries, err := os.ReadDir(claudeProjectsDir); err == nil {
		for _, projDir := range entries {
			if !projDir.IsDir() {
				continue
			}
			fullProjDir := filepath.Join(claudeProjectsDir, projDir.Name())

			// Fast slug match or inspect jsonl inside
			jsonlFiles, err := filepath.Glob(filepath.Join(fullProjDir, "*.jsonl"))
			if err != nil || len(jsonlFiles) == 0 {
				continue
			}

			for _, jf := range jsonlFiles {
				fi, err := os.Stat(jf)
				if err != nil {
					continue
				}
				// Skip if older than 14 days
				if time.Since(fi.ModTime()) > 14*24*time.Hour {
					continue
				}

				sess := parseClaudeSession(jf, fi.ModTime())
				if sess == nil {
					continue
				}

				if cleanRepo != "" && sess.RepoPath != "" && sess.RepoPath != cleanRepo && !strings.HasPrefix(cleanRepo, sess.RepoPath) && !strings.HasPrefix(sess.RepoPath, cleanRepo) {
					continue
				}

				if !seenIDs[sess.ID] {
					seenIDs[sess.ID] = true
					sessions = append(sessions, *sess)
				}
			}
		}
	}

	// 2. Scan Antigravity / Gemini Brain Transcripts
	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	if entries, err := os.ReadDir(brainDir); err == nil {
		for _, b := range entries {
			if !b.IsDir() {
				continue
			}
			transcriptPath := filepath.Join(brainDir, b.Name(), ".system_generated", "logs", "transcript.jsonl")
			fi, err := os.Stat(transcriptPath)
			if err != nil {
				continue
			}
			if time.Since(fi.ModTime()) > 14*24*time.Hour {
				continue
			}

			sess := parseAntigravitySession(b.Name(), transcriptPath, fi.ModTime())
			if sess == nil {
				continue
			}

			if cleanRepo != "" && sess.RepoPath != "" && sess.RepoPath != cleanRepo && !strings.HasPrefix(cleanRepo, sess.RepoPath) && !strings.HasPrefix(sess.RepoPath, cleanRepo) {
				continue
			}

			if !seenIDs[sess.ID] {
				seenIDs[sess.ID] = true
				sessions = append(sessions, *sess)
			}
		}
	}

	// 3. Enrich with mesh.db metadata if available
	if meshDB != nil {
		for i := range sessions {
			var branch sql.NullString
			_ = meshDB.QueryRow("SELECT git_branch FROM agent_sessions WHERE id = ?", sessions[i].ID).Scan(&branch)
			if branch.Valid && branch.String != "" {
				sessions[i].GitBranch = branch.String
			}
		}
	}

	// Sort newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	return sessions, nil
}

// GetLatestSession returns the most recent session for a given repo
func GetLatestSession(repoPath string, meshDB *sql.DB) (*SessionInfo, error) {
	sessions, err := DiscoverSessions(repoPath, 1, meshDB)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no recent session found for %s", repoPath)
	}
	return &sessions[0], nil
}

func parseClaudeSession(filePath string, modTime time.Time) *SessionInfo {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
	sess := &SessionInfo{
		ID:             sessionID,
		AgentType:      "claude",
		UpdatedAt:      modTime,
		TranscriptPath: filePath,
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 128*1024)
	scanner.Buffer(buf, 1024*1024)

	var userPrompts []string
	var lastAssistant string
	var repoPath string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}

		// Check working directory snapshot
		if repoPath == "" {
			if snap, ok := record["snapshot"].(map[string]interface{}); ok {
				if wd, ok := snap["workingDirectory"].(string); ok && wd != "" {
					repoPath = wd
				}
			}
			if att, ok := record["attachment"].(map[string]interface{}); ok {
				if snap, ok := att["snapshot"].(map[string]interface{}); ok {
					if wd, ok := snap["workingDirectory"].(string); ok && wd != "" {
						repoPath = wd
					}
				}
			}
			if cwd, ok := record["cwd"].(string); ok && cwd != "" {
				repoPath = cwd
			}
		}

		// User prompt
		recType, _ := record["type"].(string)
		if recType == "user" {
			if msg, ok := record["message"].(map[string]interface{}); ok {
				if contentStr, ok := msg["content"].(string); ok && contentStr != "" {
					userPrompts = append(userPrompts, contentStr)
				}
			}
		} else if recType == "queue-operation" {
			if op, _ := record["operation"].(string); op == "enqueue" {
				if content, ok := record["content"].(string); ok && content != "" {
					userPrompts = append(userPrompts, content)
				}
			}
		}

		// Assistant message
		if recType == "assistant" {
			if msg, ok := record["message"].(map[string]interface{}); ok {
				if contentSlice, ok := msg["content"].([]interface{}); ok && len(contentSlice) > 0 {
					for _, item := range contentSlice {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if text, ok := itemMap["text"].(string); ok && text != "" {
								lastAssistant = text
							}
						}
					}
				}
			}
		}
	}

	if repoPath != "" {
		sess.RepoPath = repoPath
	} else {
		// Infer from parent folder name
		parent := filepath.Base(filepath.Dir(filePath))
		if strings.HasPrefix(parent, "-") {
			sess.RepoPath = strings.ReplaceAll(parent, "-", "/")
		}
	}

	sess.TotalUserTurns = len(userPrompts)
	if len(userPrompts) > 0 {
		sess.RootGoal = SanitizeSummary(userPrompts[0], 250)
		sess.LastUserPrompt = SanitizeSummary(userPrompts[len(userPrompts)-1], 300)

		for _, p := range userPrompts {
			if IsSubstantiveUserPrompt(p) {
				clean := SanitizeSummary(p, 200)
				sess.UserDirectives = append(sess.UserDirectives, clean)
			}
		}
		// Cap directives to last 6
		if len(sess.UserDirectives) > 6 {
			sess.UserDirectives = sess.UserDirectives[len(sess.UserDirectives)-6:]
		}
	}

	sess.LastAssistant = SanitizeSummary(lastAssistant, 250)
	return sess
}

func parseAntigravitySession(brainID, filePath string, modTime time.Time) *SessionInfo {
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	sess := &SessionInfo{
		ID:             brainID,
		AgentType:      "gemini",
		UpdatedAt:      modTime,
		TranscriptPath: filePath,
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 128*1024)
	scanner.Buffer(buf, 1024*1024)

	var userPrompts []string
	var lastAssistant string
	var repoPath string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record map[string]interface{}
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}

		// Detect repo path from tool call args or content
		if repoPath == "" {
			if toolCalls, ok := record["tool_calls"].([]interface{}); ok {
				for _, tc := range toolCalls {
					if tcMap, ok := tc.(map[string]interface{}); ok {
						if args, ok := tcMap["args"].(map[string]interface{}); ok {
							for _, key := range []string{"AbsolutePath", "TargetFile", "Cwd"} {
								if pathStr, ok := args[key].(string); ok && pathStr != "" {
									if filepath.IsAbs(pathStr) {
										// find toplevel or dir
										repoPath = filepath.Dir(pathStr)
										break
									}
								}
							}
						}
					}
					if repoPath != "" {
						break
					}
				}
			}
		}

		// User input
		stepType, _ := record["type"].(string)
		if stepType == "USER_INPUT" {
			if content, ok := record["content"].(string); ok && content != "" {
				userPrompts = append(userPrompts, content)
			}
		}

		// Assistant output
		if stepType == "PLANNER_RESPONSE" || stepType == "GENERIC" {
			if content, ok := record["content"].(string); ok && content != "" {
				lastAssistant = content
			}
		}
	}

	sess.RepoPath = repoPath

	sess.TotalUserTurns = len(userPrompts)
	if len(userPrompts) > 0 {
		sess.RootGoal = SanitizeSummary(userPrompts[0], 250)
		sess.LastUserPrompt = SanitizeSummary(userPrompts[len(userPrompts)-1], 300)

		for _, p := range userPrompts {
			if IsSubstantiveUserPrompt(p) {
				clean := SanitizeSummary(p, 200)
				sess.UserDirectives = append(sess.UserDirectives, clean)
			}
		}
		if len(sess.UserDirectives) > 6 {
			sess.UserDirectives = sess.UserDirectives[len(sess.UserDirectives)-6:]
		}
	}

	sess.LastAssistant = SanitizeSummary(lastAssistant, 250)
	return sess
}

// SessionHandoffOptions configures session handoff generation and persistence.
type SessionHandoffOptions struct {
	Trigger        string // "manual", "auto_daemon", "crash", "exit", "breaker", "quota_warning"
	DataDir        string
	MaxKeepPerRepo int
	SkipClipboard  bool
}

// GenerateSessionHandoff synthesizes a 5-anchor context handoff prompt from a session
func GenerateSessionHandoff(sess *SessionInfo, dbConn *sql.DB) (string, error) {
	prompt, _, err := GenerateSessionHandoffWithOptions(sess, dbConn, SessionHandoffOptions{
		Trigger: "manual",
	})
	return prompt, err
}

// GenerateSessionHandoffWithOptions synthesizes a 5-anchor context handoff prompt, saves the manifest,
// and conditionally copies to clipboard.
func GenerateSessionHandoffWithOptions(sess *SessionInfo, dbConn *sql.DB, opts SessionHandoffOptions) (string, *HandoffManifest, error) {
	if sess == nil {
		return "", nil, fmt.Errorf("session is nil")
	}

	dir := sess.RepoPath
	if dir == "" {
		dir, _ = os.Getwd()
	}
	gitCtx := GatherGitContext(dir)

	taskName := ""
	taskID := ""
	if dbConn != nil {
		if t, err := GetActiveTaskForRepo(dbConn, dir); err == nil && t != nil {
			taskID = t.ID
			taskName = t.Name
		}
	}

	var sb strings.Builder
	age := time.Since(sess.UpdatedAt).Round(time.Minute)
	ageStr := fmt.Sprintf("%v ago", age)
	if age < time.Minute {
		ageStr = "just now"
	}

	originName := "Claude Code"
	if sess.AgentType == "gemini" {
		originName = "Antigravity (Gemini)"
	}

	sb.WriteString("# ⚡ STAYPOINT CONTEXT HANDOFF\n")
	sb.WriteString(fmt.Sprintf("- **Origin Agent**: %s (%s)\n", originName, ageStr))
	sb.WriteString(fmt.Sprintf("- **Session ID**: `%s`\n", sess.ID))
	sb.WriteString(fmt.Sprintf("- **Repository Path**: `%s`\n", gitCtx.RepoRoot))
	sb.WriteString(fmt.Sprintf("- **Active Branch**: `%s`\n", gitCtx.Branch))
	if taskName != "" {
		sb.WriteString(fmt.Sprintf("- **Active Task**: %s\n", taskName))
	}
	sb.WriteString("\n")

	// 1. Root Goal
	sb.WriteString("## 🎯 Primary Goal\n")
	if sess.RootGoal != "" {
		sb.WriteString(fmt.Sprintf("%s\n\n", sess.RootGoal))
	} else {
		sb.WriteString("Continue ongoing development task in repository.\n\n")
	}

	// Extended session advisory: alert receiving agent to goal drift & multiple pivots
	if sess.TotalUserTurns >= 4 || len(sess.UserDirectives) >= 3 {
		sb.WriteString("> [!IMPORTANT]\n")
		sb.WriteString(fmt.Sprintf("> **Extended Multi-Turn Session Detected (%d user turns, %d key directives)**:\n", sess.TotalUserTurns, len(sess.UserDirectives)))
		sb.WriteString("> This conversation progressed through multiple iterations. Keep in mind:\n")
		sb.WriteString("> 1. **Goal Refinement & Clarification**: The initial goal above was likely broadened, corrected, or clarified in later turns.\n")
		sb.WriteString("> 2. **Multi-Goal Evolution**: The task evolved across multiple milestones or pivots. Treat the **User Directives Trail** below as the definitive chronological record of intent.\n")
		sb.WriteString("> 3. **Latest Directives Win**: If earlier instructions conflict with the latest turn, prioritize the latest user instructions.\n\n")
	}

	// 2. User Directives & Constraints Trail
	if len(sess.UserDirectives) > 0 {
		sb.WriteString("## 🗣️ User Directives & Constraints Trail\n")
		for i, d := range sess.UserDirectives {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, d))
		}
		sb.WriteString("\n")
	}

	// 3. Cutoff Point
	sb.WriteString("## 🛑 Cutoff Point (Latest Turn)\n")
	if sess.LastUserPrompt != "" {
		sb.WriteString(fmt.Sprintf("- **Last User Prompt**: %q\n", sess.LastUserPrompt))
	}
	if sess.LastAssistant != "" {
		sb.WriteString(fmt.Sprintf("- **Last Assistant State**: %s\n", sess.LastAssistant))
	}
	sb.WriteString("\n")

	// 4. Working Tree State
	sb.WriteString("## 📂 Working Tree Status (`git status --short`)\n")
	if len(gitCtx.ModifiedFiles) > 0 {
		sb.WriteString("```text\n")
		for _, f := range gitCtx.ModifiedFiles {
			sb.WriteString(fmt.Sprintf("%s\n", f))
		}
		sb.WriteString("```\n\n")
	} else {
		sb.WriteString("Working tree clean.\n\n")
	}

	if gitCtx.DiffStat != "" {
		sb.WriteString("## 📊 Diff Summary (`git diff --stat`)\n")
		sb.WriteString("```text\n")
		sb.WriteString(gitCtx.DiffStat)
		sb.WriteString("\n```\n\n")
	}

	// 5. Immediate Next Action
	sb.WriteString("## 🚀 Immediate Next Action Directive\n")
	sb.WriteString("Resume execution immediately from this exact cutoff state. Do NOT re-ask questions or re-introduce yourself. Fulfill the latest user directive right away.\n")

	promptText := sb.String()

	trigger := opts.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	maxKeep := opts.MaxKeepPerRepo
	if maxKeep <= 0 {
		maxKeep = 3
	}

	title := taskName
	if title == "" && len(sess.UserDirectives) > 0 {
		title = sess.UserDirectives[len(sess.UserDirectives)-1]
	}
	if title == "" && sess.RootGoal != "" {
		title = sess.RootGoal
	}
	if title == "" {
		shortID := sess.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		title = fmt.Sprintf("Session %s", shortID)
	}
	if len(title) > 80 {
		title = title[:80] + "..."
	}

	goal := sess.RootGoal
	if goal == "" {
		goal = sess.LastUserPrompt
	}
	if goal == "" {
		goal = "Ongoing development task"
	}

	manifest := HandoffManifest{
		SessionID:       sess.ID,
		Title:           title,
		Goal:            goal,
		RepoPath:        gitCtx.RepoRoot,
		RepoName:        gitCtx.RepoName,
		GitBranch:       gitCtx.Branch,
		AgentType:       sess.AgentType,
		CreatedAt:       time.Now().UTC(),
		TotalUserTurns:  sess.TotalUserTurns,
		DirectivesCount: len(sess.UserDirectives),
		ActiveTaskID:    taskID,
		ActiveTaskName:  taskName,
		ModifiedFiles:   gitCtx.ModifiedFiles,
		Trigger:         trigger,
	}

	savedManifest, _ := SaveHandoffWithManifest(opts.DataDir, manifest, promptText, maxKeep)

	// Copy to clipboard unless skipped
	if !opts.SkipClipboard {
		_ = CopyToClipboard(promptText)
	}

	return promptText, savedManifest, nil
}

// AutoGenerateHandoffForSession automatically builds and persists a base handoff without AI tokens.
func AutoGenerateHandoffForSession(sessionID string, repoPath string, trigger string, dbConn *sql.DB, dataDir string, maxKeep int) (*HandoffManifest, error) {
	if trigger == "" {
		trigger = "auto_daemon"
	}
	var sess *SessionInfo
	if sessionID != "" {
		sess = FindSessionByID(sessionID)
	} else if repoPath != "" {
		sess, _ = GetLatestSession(repoPath, dbConn)
	}
	if sess == nil {
		sess = &SessionInfo{
			ID:        sessionID,
			RepoPath:  repoPath,
			UpdatedAt: time.Now(),
			AgentType: "agent",
		}
		if sess.ID == "" {
			sess.ID = fmt.Sprintf("auto-%d", time.Now().Unix())
		}
	}

	_, manifest, err := GenerateSessionHandoffWithOptions(sess, dbConn, SessionHandoffOptions{
		Trigger:        trigger,
		DataDir:        dataDir,
		MaxKeepPerRepo: maxKeep,
		SkipClipboard:  true,
	})
	return manifest, err
}

// FindSessionByID searches local Claude and Antigravity transcripts for a matching session ID.
func FindSessionByID(sessionID string) *SessionInfo {
	if sessionID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	// 1. Claude sessions: ~/.claude/projects/
	claudeDir := filepath.Join(home, ".claude", "projects")
	var foundClaude string
	_ = filepath.Walk(claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".jsonl") {
			base := strings.TrimSuffix(info.Name(), ".jsonl")
			if base == sessionID || strings.HasPrefix(base, sessionID) {
				foundClaude = path
				return filepath.SkipAll
			}
		}
		return nil
	})
	if foundClaude != "" {
		if fi, err := os.Stat(foundClaude); err == nil {
			return parseClaudeSession(foundClaude, fi.ModTime())
		}
	}

	// 2. Gemini / Antigravity brain sessions: ~/.gemini/antigravity-cli/brain/
	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	var foundGemini string
	_ = filepath.Walk(brainDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".jsonl") {
			parentDir := filepath.Dir(path)
			// check grand-parent or parent for brain ID
			brainID := filepath.Base(parentDir)
			if brainID == "logs" || brainID == ".system_generated" {
				brainID = filepath.Base(filepath.Dir(parentDir))
			}
			if brainID == ".system_generated" {
				brainID = filepath.Base(filepath.Dir(filepath.Dir(parentDir)))
			}
			if brainID == sessionID || strings.HasPrefix(brainID, sessionID) {
				foundGemini = path
				return filepath.SkipAll
			}
		}
		return nil
	})
	if foundGemini != "" {
		if fi, err := os.Stat(foundGemini); err == nil {
			parentDir := filepath.Dir(foundGemini)
			brainID := filepath.Base(parentDir)
			if brainID == "logs" || brainID == ".system_generated" {
				brainID = filepath.Base(filepath.Dir(parentDir))
			}
			if brainID == ".system_generated" {
				brainID = filepath.Base(filepath.Dir(filepath.Dir(parentDir)))
			}
			return parseAntigravitySession(brainID, foundGemini, fi.ModTime())
		}
	}

	return nil
}
