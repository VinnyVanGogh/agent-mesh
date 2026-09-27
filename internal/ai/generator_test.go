package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildPrompt(t *testing.T) {
	prompt := BuildPrompt("Add speech-to-text safety to textarea")
	if !strings.Contains(prompt, "Add speech-to-text safety to textarea") {
		t.Errorf("expected prompt to contain input comment")
	}
	if !strings.Contains(prompt, "schema") || !strings.Contains(prompt, "organization") {
		t.Errorf("expected prompt to specify schema requirements")
	}
}

func TestExtractTaskJSON(t *testing.T) {
	// Case 1: Plain JSON
	raw1 := `{
		"organization": "StayPoint",
		"project": "StayPoint Core Engine & Telemetry Fleet",
		"title": "Implement CLI Task Generator",
		"description": "## Objectives\nAdd new task command",
		"priority": "high",
		"labels": ["cli", "tui"],
		"assigneeRole": "CLI & Statusline Presentation Specialist"
	}`
	task1, err := ExtractTaskJSON(raw1)
	if err != nil {
		t.Fatalf("unexpected error parsing plain JSON: %v", err)
	}
	if task1.Title != "Implement CLI Task Generator" || task1.Priority != "high" {
		t.Errorf("unexpected task fields: %+v", task1)
	}

	// Case 2: Markdown fenced JSON with commentary
	raw2 := "Here is the structured task:\n```json\n" + raw1 + "\n```\nLet me know if you need changes."
	task2, err := ExtractTaskJSON(raw2)
	if err != nil {
		t.Fatalf("unexpected error parsing fenced JSON: %v", err)
	}
	if task2.Title != "Implement CLI Task Generator" {
		t.Errorf("unexpected task title: %s", task2.Title)
	}
}

func TestCalculateCost(t *testing.T) {
	geminiCost := CalculateCost("gemini-3.8-flash", 1000, 2000, 500)
	if geminiCost <= 0.0 {
		t.Errorf("expected positive gemini cost, got %f", geminiCost)
	}

	claudeCost := CalculateCost("claude-sonnet-4-6", 1000, 2000, 500)
	if claudeCost <= 0.0 || claudeCost <= geminiCost {
		t.Errorf("expected claude cost to exceed gemini cost, got %f vs %f", claudeCost, geminiCost)
	}
}

func TestGenerator_GeminiPrimarySuccess(t *testing.T) {
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"candidates": []map[string]interface{}{
				{
					"content": map[string]interface{}{
						"parts": []map[string]interface{}{
							{
								"text": `{
									"organization": "StayPoint",
									"project": "StayPoint Core Engine",
									"title": "Implement Gemini Primary Inference",
									"description": "## Objectives\nRun Gemini inference",
									"priority": "high",
									"labels": ["ai", "gemini"],
									"assigneeRole": "CLI & Statusline Presentation Specialist"
								}`,
							},
						},
					},
				},
			},
			"usageMetadata": map[string]interface{}{
				"promptTokenCount":        150,
				"candidatesTokenCount":    220,
				"cachedContentTokenCount": 0,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer geminiServer.Close()

	cfg := GeneratorConfig{
		GeminiAPIKey:  "test-gemini-key",
		GeminiModel:   "gemini-3.8-flash",
		GeminiBaseURL: geminiServer.URL,
		HTTPClient:    geminiServer.Client(),
	}

	gen := NewGenerator(cfg)
	res, err := gen.GenerateTask(context.Background(), "Implement Gemini primary inference")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.FallbackUsed {
		t.Errorf("expected primary model to be used, not fallback")
	}
	if res.Model != "gemini-3.8-flash" {
		t.Errorf("expected model gemini-3.8-flash, got %s", res.Model)
	}
	if res.Task.Title != "Implement Gemini Primary Inference" {
		t.Errorf("unexpected task title: %s", res.Task.Title)
	}
	if res.InputTokens != 150 || res.OutputTokens != 220 {
		t.Errorf("unexpected token counts: in=%d, out=%d", res.InputTokens, res.OutputTokens)
	}
}

func TestGenerator_FallbackToClaudeOnGeminiFailure(t *testing.T) {
	// Gemini server returns 429 Too Many Requests
	geminiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":429,"message":"Resource exhausted"}}`, http.StatusTooManyRequests)
	}))
	defer geminiServer.Close()

	// Claude server succeeds
	claudeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": `{
						"organization": "StayPoint",
						"project": "StayPoint Core Engine",
						"title": "Fallback Issue Synthesized by Claude",
						"description": "## Objectives\nHandled via Sonnet fallback",
						"priority": "urgent",
						"labels": ["claude", "fallback"],
						"assigneeRole": "Senior PR Reviewer"
					}`,
				},
			},
			"usage": map[string]interface{}{
				"input_tokens":             180,
				"output_tokens":            250,
				"cache_read_input_tokens": 10,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer claudeServer.Close()

	cfg := GeneratorConfig{
		GeminiAPIKey:     "test-gemini-key",
		GeminiModel:      "gemini-3.8-flash",
		GeminiBaseURL:    geminiServer.URL,
		AnthropicAPIKey:  "test-claude-key",
		AnthropicModel:   "claude-sonnet-4-6",
		AnthropicBaseURL: claudeServer.URL,
		HTTPClient:       http.DefaultClient,
	}

	gen := NewGenerator(cfg)
	res, err := gen.GenerateTask(context.Background(), "Urgent bug in rate limiter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.FallbackUsed {
		t.Errorf("expected FallbackUsed to be true")
	}
	if res.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model claude-sonnet-4-6, got %s", res.Model)
	}
	if res.Task.Title != "Fallback Issue Synthesized by Claude" {
		t.Errorf("unexpected title: %s", res.Task.Title)
	}
	if res.Task.Priority != "urgent" {
		t.Errorf("expected priority urgent, got %s", res.Task.Priority)
	}
}

func TestGenerator_HeuristicFallbackWhenOffline(t *testing.T) {
	cfg := GeneratorConfig{
		GeminiAPIKey:    "",
		AnthropicAPIKey: "",
	}
	gen := NewGenerator(cfg)
	res, err := gen.GenerateTask(context.Background(), "Fix critical race condition in watcher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !res.FallbackUsed {
		t.Errorf("expected FallbackUsed to be true for offline heuristic")
	}
	if res.Model != "heuristic-fallback" {
		t.Errorf("expected model heuristic-fallback, got %s", res.Model)
	}
	if !strings.Contains(res.Task.Title, "race condition in watcher") {
		t.Errorf("unexpected task title: %s", res.Task.Title)
	}
	if res.Task.Priority != "urgent" {
		t.Errorf("expected urgent priority for 'critical', got %s", res.Task.Priority)
	}
}
