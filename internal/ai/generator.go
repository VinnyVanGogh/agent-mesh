package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// InferredTask represents the structured engineering issue inferred by the AI engine.
type InferredTask struct {
	Organization string   `json:"organization"`
	Project      string   `json:"project"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Priority     string   `json:"priority"` // low, medium, high, urgent
	Labels       []string `json:"labels"`
	AssigneeRole string   `json:"assigneeRole"`
}

// GenerationResult holds the task and generation telemetry.
type GenerationResult struct {
	Task             InferredTask
	Model            string
	InputTokens      int
	OutputTokens     int
	CachedTokens     int
	EstimatedCostUSD float64
	FallbackUsed     bool
	RawResponse      string
}

// GeneratorConfig configures model endpoints, keys, and timeout behaviors.
type GeneratorConfig struct {
	GeminiAPIKey     string
	GeminiModel      string
	GeminiBaseURL    string
	AnthropicAPIKey  string
	AnthropicModel   string
	AnthropicBaseURL string
	HTTPClient       *http.Client
}

type TaskGenerator struct {
	cfg GeneratorConfig
}

// DefaultGeneratorConfig resolves configuration from environment variables with sensible defaults.
func DefaultGeneratorConfig() GeneratorConfig {
	geminiKey := os.Getenv("GEMINI_API_KEY")
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")

	geminiModel := os.Getenv("STAYPOINT_GEMINI_MODEL")
	if geminiModel == "" {
		geminiModel = "gemini-3.8-flash"
	}

	anthropicModel := os.Getenv("STAYPOINT_ANTHROPIC_MODEL")
	if anthropicModel == "" {
		anthropicModel = "claude-sonnet-4-6"
	}

	return GeneratorConfig{
		GeminiAPIKey:     geminiKey,
		GeminiModel:      geminiModel,
		GeminiBaseURL:    "https://generativelanguage.googleapis.com",
		AnthropicAPIKey:  anthropicKey,
		AnthropicModel:   anthropicModel,
		AnthropicBaseURL: "https://api.anthropic.com",
		HTTPClient:       &http.Client{Timeout: 30 * time.Second},
	}
}

// NewGenerator returns a new TaskGenerator.
func NewGenerator(cfg GeneratorConfig) *TaskGenerator {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.GeminiModel == "" {
		cfg.GeminiModel = "gemini-3.8-flash"
	}
	if cfg.AnthropicModel == "" {
		cfg.AnthropicModel = "claude-sonnet-4-6"
	}
	if cfg.GeminiBaseURL == "" {
		cfg.GeminiBaseURL = "https://generativelanguage.googleapis.com"
	}
	if cfg.AnthropicBaseURL == "" {
		cfg.AnthropicBaseURL = "https://api.anthropic.com"
	}
	return &TaskGenerator{cfg: cfg}
}

// BuildPrompt creates the system instruction and context wrapper for the raw comment.
func BuildPrompt(comment string) string {
	return fmt.Sprintf(`You are the CTO's autonomous AI parsing engine. A user has dictated or written a raw thought, complaint, or request.

Task Input:
"""
%s
"""

Your job is to read this raw input and convert it into a highly structured, professional engineering issue.
1. DO NOT just copy and paste the input as the title or description. You MUST synthesize a crisp, concise title in imperative mood (e.g. "Implement dynamic quota router", "Fix layout bug on settings page").
2. Carefully infer the target Organization (e.g. StayPoint, Managed Solution, RuneLite, Research) and Project from context clues in the text.
3. Write a professional markdown description that includes:
   ## Objectives (what needs to be achieved based on the user's intent)
   ## Core Specs (technical details, constraints, questions asked by user)
   ## Next Steps (concrete actions to take)

Respond ONLY with a valid JSON object matching the requested schema. No markdown wrapping.`, strings.TrimSpace(comment))
}

// CalculateCost estimates the USD cost for a generation run based on token counts.
func CalculateCost(model string, inputTokens, outputTokens, cachedTokens int) float64 {
	switch {
	case strings.Contains(strings.ToLower(model), "gemini"):
		// Gemini 3.8 Flash rates: $0.15/1M in, $0.60/1M out, $0.0375/1M cached
		inCost := (float64(inputTokens) / 1000000.0) * 0.15
		outCost := (float64(outputTokens) / 1000000.0) * 0.60
		cachedCost := (float64(cachedTokens) / 1000000.0) * 0.0375
		return inCost + outCost + cachedCost

	case strings.Contains(strings.ToLower(model), "claude") || strings.Contains(strings.ToLower(model), "sonnet"):
		// Claude Sonnet rates: $3.00/1M in, $15.00/1M out, $0.30/1M cached
		inCost := (float64(inputTokens) / 1000000.0) * 3.00
		outCost := (float64(outputTokens) / 1000000.0) * 15.00
		cachedCost := (float64(cachedTokens) / 1000000.0) * 0.30
		return inCost + outCost + cachedCost

	default:
		return 0.0
	}
}

// ExtractTaskJSON extracts and parses the InferredTask from a raw model string response.
func ExtractTaskJSON(raw string) (InferredTask, error) {
	var task InferredTask
	trimmed := strings.TrimSpace(raw)

	// Strip markdown code fences if present
	fenceRegex := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
	if matches := fenceRegex.FindStringSubmatch(trimmed); len(matches) > 1 {
		trimmed = strings.TrimSpace(matches[1])
	}

	// Find enclosing brackets
	firstBrace := strings.Index(trimmed, "{")
	lastBrace := strings.LastIndex(trimmed, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		trimmed = trimmed[firstBrace : lastBrace+1]
	}

	if err := json.Unmarshal([]byte(trimmed), &task); err != nil {
		return task, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	// Normalize defaults
	if task.Title == "" {
		task.Title = "Untitled Task"
	}
	if task.Priority == "" {
		task.Priority = "medium"
	}
	task.Priority = strings.ToLower(task.Priority)
	if task.Organization == "" {
		task.Organization = "StayPoint"
	}
	if task.Project == "" {
		task.Project = "StayPoint Core Engine & Telemetry Fleet"
	}
	if task.AssigneeRole == "" {
		task.AssigneeRole = "CLI & Statusline Presentation Specialist"
	}
	if task.Description == "" {
		task.Description = fmt.Sprintf("## Objectives\n%s\n\n## Next Steps\n- Implement task requirements.", task.Title)
	}

	return task, nil
}

// GenerateTask orchestrates inference with Gemini 3.8 Flash as primary, falling back to Claude Sonnet.
func (g *TaskGenerator) GenerateTask(ctx context.Context, comment string) (*GenerationResult, error) {
	prompt := BuildPrompt(comment)

	// 1. Try Gemini (Primary) if key configured
	var geminiErr error
	if g.cfg.GeminiAPIKey != "" {
		res, err := g.CallGemini(ctx, prompt)
		if err == nil {
			return res, nil
		}
		geminiErr = err
	} else {
		geminiErr = errors.New("GEMINI_API_KEY not configured")
	}

	// 2. Downshift to Claude Sonnet (Fallback)
	var claudeErr error
	if g.cfg.AnthropicAPIKey != "" {
		res, err := g.CallClaude(ctx, prompt)
		if err == nil {
			res.FallbackUsed = true
			return res, nil
		}
		claudeErr = err
	} else {
		claudeErr = errors.New("ANTHROPIC_API_KEY not configured")
	}

	// 3. If both remote APIs fail or are unconfigured, use deterministic heuristic generator
	heuristicTask := g.GenerateHeuristicTask(comment)
	return &GenerationResult{
		Task:             heuristicTask,
		Model:            "heuristic-fallback",
		InputTokens:      len(strings.Fields(comment)),
		OutputTokens:     120,
		CachedTokens:     0,
		EstimatedCostUSD: 0.0,
		FallbackUsed:     true,
		RawResponse:      "Synthesized via StayPoint local heuristic engine (remote APIs unavailable: Gemini: " + geminiErr.Error() + "; Claude: " + claudeErr.Error() + ")",
	}, nil
}

// CallGemini executes an HTTP call to the Google Gemini generateContent endpoint.
func (g *TaskGenerator) CallGemini(ctx context.Context, prompt string) (*GenerationResult, error) {
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(g.cfg.GeminiBaseURL, "/"),
		g.cfg.GeminiModel,
		g.cfg.GeminiAPIKey,
	)

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": prompt},
				},
			},
		},
		"systemInstruction": map[string]interface{}{
			"parts": []map[string]interface{}{
				{"text": "You are an expert autonomous software engineer and task coordinator for the Paperclip & StayPoint ecosystem. Analyze the following natural language task request or dictated comment and synthesize a structured engineering issue."},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":      0.2,
			"responseMimeType": "application/json",
			"responseSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"organization": map[string]interface{}{
						"type":        "string",
						"description": "Target Organization (e.g. StayPoint, Managed Solution, RuneLite, Maintenance, Research)",
					},
					"project": map[string]interface{}{
						"type":        "string",
						"description": "Target Project name (e.g. StayPoint Core Engine & Telemetry Fleet)",
					},
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Crisp, concise issue title in imperative mood (e.g. 'Implement dynamic quota router')",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Structured Markdown description with sections: ## Objectives, ## Core Specs, ## Next Steps",
					},
					"priority": map[string]interface{}{
						"type": "string",
						"enum": []string{"low", "medium", "high", "urgent"},
					},
					"labels": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "array of lowercase tags",
					},
					"assigneeRole": map[string]interface{}{
						"type":        "string",
						"description": "Recommended assignee role (e.g. CLI & Statusline Presentation Specialist, Architecture Lead, Senior PR Reviewer)",
					},
				},
				"required": []string{"organization", "project", "title", "description", "priority", "labels", "assigneeRole"},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode gemini request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini network error: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read gemini response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API error (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(respBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini response JSON: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, errors.New("empty candidates returned from gemini")
	}

	rawText := geminiResp.Candidates[0].Content.Parts[0].Text
	task, err := ExtractTaskJSON(rawText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse structured task from gemini response: %w", err)
	}

	inTokens := geminiResp.UsageMetadata.PromptTokenCount
	outTokens := geminiResp.UsageMetadata.CandidatesTokenCount
	cachedTokens := geminiResp.UsageMetadata.CachedContentTokenCount
	cost := CalculateCost(g.cfg.GeminiModel, inTokens, outTokens, cachedTokens)

	return &GenerationResult{
		Task:             task,
		Model:            g.cfg.GeminiModel,
		InputTokens:      inTokens,
		OutputTokens:     outTokens,
		CachedTokens:     cachedTokens,
		EstimatedCostUSD: cost,
		FallbackUsed:     false,
		RawResponse:      rawText,
	}, nil
}

// CallClaude executes an HTTP call to the Anthropic Messages endpoint.
func (g *TaskGenerator) CallClaude(ctx context.Context, prompt string) (*GenerationResult, error) {
	url := fmt.Sprintf("%s/v1/messages", strings.TrimRight(g.cfg.AnthropicBaseURL, "/"))

	reqBody := map[string]interface{}{
		"model":      g.cfg.AnthropicModel,
		"max_tokens": 4096,
		"system":     "You are an expert autonomous software engineer and task coordinator. Return ONLY a valid JSON object matching the requested schema.",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to encode claude request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create claude request: %w", err)
	}
	req.Header.Set("x-api-key", g.cfg.AnthropicAPIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude network error: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read claude response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude API error (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	var claudeResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBytes, &claudeResp); err != nil {
		return nil, fmt.Errorf("failed to decode claude response JSON: %w", err)
	}

	if len(claudeResp.Content) == 0 {
		return nil, errors.New("empty content returned from claude")
	}

	rawText := claudeResp.Content[0].Text
	task, err := ExtractTaskJSON(rawText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse structured task from claude response: %w", err)
	}

	inTokens := claudeResp.Usage.InputTokens
	outTokens := claudeResp.Usage.OutputTokens
	cachedTokens := claudeResp.Usage.CacheReadInputTokens
	cost := CalculateCost(g.cfg.AnthropicModel, inTokens, outTokens, cachedTokens)

	return &GenerationResult{
		Task:             task,
		Model:            g.cfg.AnthropicModel,
		InputTokens:      inTokens,
		OutputTokens:     outTokens,
		CachedTokens:     cachedTokens,
		EstimatedCostUSD: cost,
		FallbackUsed:     true,
		RawResponse:      rawText,
	}, nil
}

// GenerateHeuristicTask provides a deterministic fallback task synthesis when remote AI APIs are offline.
func (g *TaskGenerator) GenerateHeuristicTask(comment string) InferredTask {
	cleanComment := strings.TrimSpace(comment)

	// Correct common phonetic STT artifacts
	replacer := strings.NewReplacer(
		"sharepoint", "StayPoint",
		"SharePoint", "StayPoint",
		"share point", "StayPoint",
		"grab ", "grep ",
		"length ", "lint ",
		"batch ", "bash ",
	)
	normalized := replacer.Replace(cleanComment)
	lowerNorm := strings.ToLower(normalized)

	// 1. Infer Organization
	org := "StayPoint"
	switch {
	case strings.Contains(lowerNorm, "managed solution") || strings.Contains(lowerNorm, "mansol") ||
		strings.Contains(lowerNorm, "azure") || strings.Contains(lowerNorm, "m365") ||
		strings.Contains(lowerNorm, "client portal") || strings.Contains(lowerNorm, "client acme") ||
		strings.Contains(lowerNorm, "msp"):
		org = "Managed Solution"
	case strings.Contains(lowerNorm, "runelite") || strings.Contains(lowerNorm, "osrs") ||
		strings.Contains(lowerNorm, "runescape") || strings.Contains(lowerNorm, "prayer flick") ||
		strings.Contains(lowerNorm, "tile indicator"):
		org = "RuneLite"
	case strings.Contains(lowerNorm, "dotfile") || strings.Contains(lowerNorm, "zshrc") ||
		strings.Contains(lowerNorm, "homebrew") || strings.Contains(lowerNorm, "prune") ||
		strings.Contains(lowerNorm, "maintenance") || strings.Contains(lowerNorm, "cleanup my") ||
		strings.Contains(lowerNorm, "clean up my"):
		org = "Maintenance"
	case strings.Contains(lowerNorm, "arxiv") || strings.Contains(lowerNorm, "paper") ||
		strings.Contains(lowerNorm, "benchmark") || strings.Contains(lowerNorm, "research") ||
		strings.Contains(lowerNorm, "eval") || strings.Contains(lowerNorm, "needle retrieval"):
		org = "Research"
	}

	// 2. Infer Project
	project := "StayPoint Core Engine & Telemetry Fleet"
	switch org {
	case "Managed Solution":
		if strings.Contains(lowerNorm, "migration") || strings.Contains(lowerNorm, "cloud") {
			project = "Managed Solution Cloud Migration"
		} else {
			project = "Managed Solution Client Services"
		}
	case "RuneLite":
		project = "RuneLite Plugin Suite"
	case "Maintenance":
		project = "System Maintenance & Infrastructure"
	case "Research":
		if strings.Contains(lowerNorm, "tooling") || strings.Contains(lowerNorm, "observability") ||
			strings.Contains(lowerNorm, "github") || strings.Contains(lowerNorm, "copilot") ||
			strings.Contains(lowerNorm, "appsec") {
			project = "Tooling, AppSec & Observability Intelligence"
		} else {
			project = "AI Model Benchmarking & Research"
		}
	case "StayPoint":
		if strings.Contains(lowerNorm, "statusline") || strings.Contains(lowerNorm, "tui") ||
			strings.Contains(lowerNorm, "bubbletea") || strings.Contains(lowerNorm, "textarea") ||
			strings.Contains(lowerNorm, "glamour") {
			project = "StayPoint Statusline & TUI Presentation"
		} else if strings.Contains(lowerNorm, "wire") || strings.Contains(lowerNorm, "daemon") ||
			strings.Contains(lowerNorm, "socket") || strings.Contains(lowerNorm, "ipc") {
			project = "StayPoint Wire Protocol & Daemon"
		} else if strings.Contains(lowerNorm, "quota") || strings.Contains(lowerNorm, "pacing") ||
			strings.Contains(lowerNorm, "rate limit") {
			project = "StayPoint Dynamic Quota & Fleet Engine"
		}
	}

	// 3. Infer Priority
	priority := "medium"
	if strings.Contains(lowerNorm, "urgent") || strings.Contains(lowerNorm, "asap") ||
		strings.Contains(lowerNorm, "critical") || strings.Contains(lowerNorm, "blocker") ||
		strings.Contains(lowerNorm, "outage") {
		priority = "urgent"
	} else if strings.Contains(lowerNorm, "bug") || strings.Contains(lowerNorm, "broken") ||
		strings.Contains(lowerNorm, "fix") || strings.Contains(lowerNorm, "failure") ||
		strings.Contains(lowerNorm, "race condition") || strings.Contains(lowerNorm, "leak") {
		priority = "high"
	} else if strings.Contains(lowerNorm, "doc") || strings.Contains(lowerNorm, "readme") ||
		strings.Contains(lowerNorm, "typo") || strings.Contains(lowerNorm, "minor") {
		priority = "low"
	}

	// 4. Synthesize Crisp Imperative Title (strip conversational filler)
	title := cleanImperativeTitle(normalized, org)

	// 5. Infer Labels
	labels := []string{"cli", "task"}
	switch org {
	case "Managed Solution":
		labels = []string{"managed-solution", "client"}
		if strings.Contains(lowerNorm, "azure") {
			labels = append(labels, "azure")
		}
		if strings.Contains(lowerNorm, "portal") {
			labels = append(labels, "portal")
		}
	case "RuneLite":
		labels = []string{"runelite", "plugin", "osrs"}
	case "Maintenance":
		labels = []string{"maintenance", "infrastructure"}
		if strings.Contains(lowerNorm, "dotfile") || strings.Contains(lowerNorm, "zshrc") {
			labels = append(labels, "dotfiles")
		}
	case "Research":
		labels = []string{"research", "ai"}
		if strings.Contains(lowerNorm, "github") || strings.Contains(lowerNorm, "copilot") {
			labels = append(labels, "github", "copilot", "agents")
		} else {
			labels = append(labels, "benchmark")
		}
	case "StayPoint":
		labels = []string{"staypoint"}
		if strings.Contains(lowerNorm, "tui") || strings.Contains(lowerNorm, "bubbletea") {
			labels = append(labels, "tui")
		}
		if strings.Contains(lowerNorm, "wire") || strings.Contains(lowerNorm, "daemon") {
			labels = append(labels, "wire")
		}
		if strings.Contains(lowerNorm, "quota") || strings.Contains(lowerNorm, "pacing") {
			labels = append(labels, "pacing")
		}
	}
	if priority == "urgent" || priority == "high" {
		labels = append(labels, "bug")
	}

	// 6. Assignee Role
	role := "CLI & Statusline Presentation Specialist"
	switch {
	case strings.Contains(lowerNorm, "security") || strings.Contains(lowerNorm, "auth") || strings.Contains(lowerNorm, "secret"):
		role = "Security & Deep Remediation Fixer"
	case strings.Contains(lowerNorm, "review") || strings.Contains(lowerNorm, "audit"):
		role = "Senior PR Reviewer"
	case strings.Contains(lowerNorm, "research") || strings.Contains(lowerNorm, "copilot") || strings.Contains(lowerNorm, "eval"):
		role = "Research & Architecture Specialist"
	case strings.Contains(lowerNorm, "wire") || strings.Contains(lowerNorm, "architecture") || strings.Contains(lowerNorm, "rfc") || strings.Contains(lowerNorm, "daemon"):
		role = "Architecture Lead"
	case strings.Contains(lowerNorm, "ci") || strings.Contains(lowerNorm, "lint") || strings.Contains(lowerNorm, "hook") || strings.Contains(lowerNorm, "test"):
		role = "CI/CD Engineer"
	case strings.Contains(lowerNorm, "release") || strings.Contains(lowerNorm, "deploy") || strings.Contains(lowerNorm, "package") || strings.Contains(lowerNorm, "homebrew"):
		role = "DevOps & Release Engineer"
	}

	// Extract questions if present
	var questions []string
	reQuestion := regexp.MustCompile(`(?m)^\s*(\d+\.|\*|-)\s*(.+)`)
	for _, match := range reQuestion.FindAllStringSubmatch(cleanComment, -1) {
		if len(match) > 2 {
			q := strings.TrimSpace(match[2])
			if q != "" {
				questions = append(questions, fmt.Sprintf("%s %s", match[1], q))
			}
		}
	}

	questionBlock := ""
	if len(questions) > 0 {
		questionBlock = fmt.Sprintf("\n\n## Key Inquiries & Questions\n%s", strings.Join(questions, "\n"))
	}

	// 7. Synthesize Markdown Description
	description := fmt.Sprintf(`## Objectives
- Synthesize requirements and deliver engineering solution for %s.
- Address specifications and prevent regressions.

## Core Specs
- **Target Organization:** %s
- **Target Project:** %s
- **Priority Tier:** %s
- **Scope Summary:** %s%s

## Next Steps
1. Triage codebase and inspect relevant source files.
2. Implement solution following architectural specifications.
3. Validate against StayPoint Definition of Done under race detection.

## Original Request
> %s`, title, org, project, strings.ToUpper(priority), title, questionBlock, cleanComment)

	return InferredTask{
		Organization: org,
		Project:      project,
		Title:        title,
		Description:  description,
		Priority:     priority,
		Labels:       labels,
		AssigneeRole: role,
	}
}

// cleanImperativeTitle strips conversational dictation filler and ensures imperative mood under 72 chars.
func cleanImperativeTitle(input, org string) string {
	raw := strings.TrimSpace(input)

	// Repeatedly strip conversational and meta-dictation preambles from beginning
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(?:can\s+you\s+(?:please\s+)?)?(?:open|create|add|make|file)\s+(?:a\s+)?(?:new\s+)?task\s+(?:under|for|in|titled|about)\s+[^\n\?]+[\?\.]?\s*`),
		regexp.MustCompile(`(?i)^(?:hey|yo|hi|hello)\s+(?:vinny\s+|there\s+|assistant\s+)?`),
		regexp.MustCompile(`(?i)^(?:so\s+)?basically\s+`),
		regexp.MustCompile(`(?i)^(?:can\s+you|could\s+you|would\s+you)\s+(?:please\s+)?`),
		regexp.MustCompile(`(?i)^please\s+`),
		regexp.MustCompile(`(?i)^(?:um+|uh+|er+|ah+)\s+`),
		regexp.MustCompile(`(?i)^(?:in|for)\s+(?:sharepoint|staypoint|runelite|managed\s+solution|research)\s+`),
		regexp.MustCompile(`(?i)^(?:we\s+(?:need\s+to|gotta|should|have\s+to)|i\s+(?:need\s+to|want\s+to|would\s+like\s+to))\s+`),
		regexp.MustCompile(`(?i)^(?:the\s+)?task\s+(?:is\s+to|should\s+be\s+to|is)\s+`),
	}

	stripped := raw
	changed := true
	for changed {
		changed = false
		for _, re := range patterns {
			if loc := re.FindStringIndex(stripped); loc != nil && loc[0] == 0 {
				stripped = strings.TrimSpace(stripped[loc[1]:])
				changed = true
			}
		}
	}

	// Use first substantive line or sentence
	if idx := strings.Index(stripped, "\n"); idx != -1 {
		stripped = strings.TrimSpace(stripped[:idx])
	}
	if idx := strings.Index(stripped, ". "); idx != -1 && idx > 20 {
		stripped = strings.TrimSpace(stripped[:idx])
	}

	// Strip conversational trailing clauses
	if idx := strings.Index(strings.ToLower(stripped), " because "); idx != -1 {
		stripped = strings.TrimSpace(stripped[:idx])
	}
	reTrailing := regexp.MustCompile(`(?i)\s+(?:asap|urgently|please|right now)$`)
	stripped = reTrailing.ReplaceAllString(stripped, "")
	stripped = strings.TrimSpace(stripped)

	// Capitalize first character
	if len(stripped) > 0 {
		stripped = strings.ToUpper(stripped[:1]) + stripped[1:]
	}

	// Ensure imperative verb prefix if missing
	verbs := []string{"Fix", "Implement", "Add", "Update", "Refactor", "Clean", "Prune", "Run", "Resolve", "Audit", "Remove", "Research", "Investigate", "Evaluate", "Benchmark", "Design"}
	hasVerb := false
	for _, v := range verbs {
		if strings.HasPrefix(stripped, v) {
			hasVerb = true
			break
		}
	}
	if !hasVerb {
		stripped = "Implement " + stripped
	}

	// Remove trailing punctuation
	stripped = strings.TrimRight(stripped, ".!? \t\r\n")

	// Limit to 72 characters
	if len(stripped) > 72 {
		stripped = strings.TrimSpace(stripped[:69]) + "..."
	}

	return stripped
}
