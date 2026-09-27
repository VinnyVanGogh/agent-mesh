package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/ai"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
)

func TestSanitizeComment(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{`"hello world"`, `hello world`},
		{`'single quotes'`, `single quotes`},
		{`  " leading and trailing spaces "  `, `leading and trailing spaces`},
		{`inner "quotes" preserved`, `inner "quotes" preserved`},
		{`speech-to-text 'nested "quotes"' dictation`, `speech-to-text 'nested "quotes"' dictation`},
	}

	for _, c := range cases {
		out := sanitizeComment(c.input)
		if out != c.expected {
			t.Errorf("sanitizeComment(%q) = %q, expected %q", c.input, out, c.expected)
		}
	}
}

func TestRenderMarkdownSummary(t *testing.T) {
	task := ai.InferredTask{
		Organization: "StayPoint",
		Project:      "StayPoint Core Engine & Telemetry Fleet",
		Title:        "Implement TUI Task Generator",
		Description:  "## Objectives\nAdd Bubble Tea interactive input\n\n## Next Steps\n- Run tests",
		Priority:     "high",
		Labels:       []string{"tui", "cli"},
		AssigneeRole: "CLI & Statusline Presentation Specialist",
	}

	rendered := renderMarkdownSummary(task)
	// Strip ANSI sequences for clean text assertions
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	clean := ansiRegex.ReplaceAllString(rendered, "")
	normalized := strings.Join(strings.Fields(clean), " ")

	if !strings.Contains(normalized, "Implement TUI Task Generator") {
		t.Errorf("expected rendered card to contain title, got: %s", clean)
	}
	if !strings.Contains(normalized, "HIGH") {
		t.Errorf("expected rendered card to contain uppercase priority, got: %s", clean)
	}
	if !strings.Contains(normalized, "CLI & Statusline Presentation Specialist") {
		t.Errorf("expected rendered card to contain assignee role, got: %s", clean)
	}
}

func TestTaskCreate_DryRunExecution(t *testing.T) {
	// Test CLI mode with dry-run
	homeDir := t.TempDir()

	_ = os.Setenv("HOME", homeDir)
	defer os.Unsetenv("HOME")

	cfg = nil // reload config

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "true")
	rootCmd.SetArgs([]string{"task", "create", "Add automated health check endpoint", "--dry-run"})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("unexpected command failure: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Automated health check") && !strings.Contains(out, "automated health check") && !strings.Contains(out, "Objectives") {
		t.Errorf("expected generated task in output, got: %s", out)
	}
	if !strings.Contains(out, "Inference Telemetry & Cost Engine") {
		t.Errorf("expected telemetry output, got: %s", out)
	}
	if !strings.Contains(out, "Dry Run Mode") {
		t.Errorf("expected dry run notice in output, got: %s", out)
	}
}

func TestTaskCreate_DispatchToMockPaperclip(t *testing.T) {
	// Mock Paperclip API Server
	var receivedReq paperclip.CreateIssueRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/issues") && r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&receivedReq)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(paperclip.IssueResponse{
				ID:          "mock-issue-id-123",
				Identifier:  "STA-99",
				Title:       receivedReq.Title,
				Description: receivedReq.Description,
				Status:      "todo",
				Priority:    receivedReq.Priority,
				CompanyID:   "mock-company-id",
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/companies/") && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(paperclip.CompanyResponse{
				ID:          "mock-company-id",
				Name:        "StayPoint",
				IssuePrefix: "STA",
				Status:      "active",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	os.Setenv("PAPERCLIP_API_URL", mockServer.URL)
	os.Setenv("PAPERCLIP_COMPANY_ID", "mock-company-id")
	os.Setenv("PAPERCLIP_API_KEY", "test-token")
	defer os.Unsetenv("PAPERCLIP_API_URL")
	defer os.Unsetenv("PAPERCLIP_COMPANY_ID")
	defer os.Unsetenv("PAPERCLIP_API_KEY")

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "false")
	rootCmd.SetArgs([]string{
		"task", "create",
		"Fix quota window reset bug in router",
		"--company", "mock-company-id",
		"--priority", "high",
	})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task create failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "STA-99") {
		t.Errorf("expected issue identifier STA-99 in output, got: %s", out)
	}
	if !strings.Contains(out, mockServer.URL+"/STA/issues/mock-issue-id-123") {
		t.Errorf("expected clickable issue URL, got: %s", out)
	}
	if receivedReq.Priority != "high" {
		t.Errorf("expected priority high, got: %s", receivedReq.Priority)
	}
}
