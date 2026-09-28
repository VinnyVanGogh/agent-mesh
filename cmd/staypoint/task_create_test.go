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

func TestTaskCreate_BareNaturalLanguage_AssignsChiefOfStaff(t *testing.T) {
	var receivedReq paperclip.CreateIssueRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/issues") && r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&receivedReq)
			_ = json.NewEncoder(w).Encode(paperclip.IssueResponse{
				ID:          "mock-cos-issue",
				Identifier:  "STA-101",
				Title:       receivedReq.Title,
				Description: receivedReq.Description,
				Status:      "todo",
				Priority:    receivedReq.Priority,
				CompanyID:   "sta-comp-id",
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agents") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]paperclip.AgentResponse{
				{ID: "agent-cos-id-123", Name: "Chief of Staff", Role: "ceo"},
				{ID: "agent-qa-id-456", Name: "QA Engineer", Role: "qa"},
			})
			return
		}
		if r.URL.Path == "/api/companies" && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]paperclip.CompanyResponse{
				{ID: "sta-comp-id", Name: "StayPoint", IssuePrefix: "STA", Status: "active"},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/companies/sta-comp-id") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(paperclip.CompanyResponse{
				ID:          "sta-comp-id",
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
	os.Setenv("PAPERCLIP_COMPANY_ID", "sta-comp-id")
	os.Setenv("PAPERCLIP_API_KEY", "test-token")
	defer os.Unsetenv("PAPERCLIP_API_URL")
	defer os.Unsetenv("PAPERCLIP_COMPANY_ID")
	defer os.Unsetenv("PAPERCLIP_API_KEY")

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "false")
	_ = taskCreateCmd.Flags().Set("role", "")
	_ = taskCreateCmd.Flags().Set("priority", "")
	_ = taskCreateCmd.Flags().Set("company", "")
	_ = taskCreateCmd.Flags().Set("project", "")
	rootCmd.SetArgs([]string{
		"task", "create",
		"In StayPoint we need to implement distributed quota synchronization in the wire router",
	})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task create failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "STA-101") {
		t.Errorf("expected issue STA-101 in output, got: %s", out)
	}
	if !strings.Contains(out, "Chief of Staff") {
		t.Errorf("expected Chief of Staff in output, got: %s", out)
	}
	if receivedReq.AssigneeAgentId != "agent-cos-id-123" {
		t.Errorf("expected AssigneeAgentId to be 'agent-cos-id-123', got %q", receivedReq.AssigneeAgentId)
	}
}

func TestTaskCreate_ManualOverrideFlags(t *testing.T) {
	var receivedReq paperclip.CreateIssueRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/issues") && r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&receivedReq)
			_ = json.NewEncoder(w).Encode(paperclip.IssueResponse{
				ID:          "mock-override-issue",
				Identifier:  "STA-102",
				Title:       receivedReq.Title,
				Description: receivedReq.Description,
				Status:      "todo",
				Priority:    receivedReq.Priority,
				CompanyID:   "custom-comp-id",
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agents") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]paperclip.AgentResponse{
				{ID: "cos-id", Name: "Chief of Staff", Role: "ceo"},
				{ID: "qa-id", Name: "QA & Automated Test Engineer", Role: "qa"},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/companies/custom-comp-id") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(paperclip.CompanyResponse{
				ID:          "custom-comp-id",
				Name:        "Custom Company",
				IssuePrefix: "STA",
				Status:      "active",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	os.Setenv("PAPERCLIP_API_URL", mockServer.URL)
	os.Setenv("PAPERCLIP_API_KEY", "test-token")
	defer os.Unsetenv("PAPERCLIP_API_URL")
	defer os.Unsetenv("PAPERCLIP_API_KEY")

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "false")
	rootCmd.SetArgs([]string{
		"task", "create",
		"Fix typo in documentation comment",
		"--company", "custom-comp-id",
		"--project", "custom-proj-id",
		"--priority", "urgent",
		"--role", "qa",
	})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task create with overrides failed: %v", err)
	}

	if receivedReq.Priority != "critical" {
		t.Errorf("expected priority override 'critical' (normalized from urgent), got %q", receivedReq.Priority)
	}
	if receivedReq.ProjectId != "custom-proj-id" {
		t.Errorf("expected project override 'custom-proj-id', got %q", receivedReq.ProjectId)
	}
	if receivedReq.AssigneeAgentId != "qa-id" {
		t.Errorf("expected role override agent 'qa-id', got %q", receivedReq.AssigneeAgentId)
	}
}

func TestTaskCreate_StdinPipeMode(t *testing.T) {
	homeDir := t.TempDir()
	_ = os.Setenv("HOME", homeDir)
	defer os.Unsetenv("HOME")

	cfg = nil

	// Pipe stdin with multiline text containing speech-to-text dictation, quotes, backticks
	pipeContent := "Here is a multiline brief:\n`git status` showed uncommitted changes.\n\"Ensure quotes\" and 'single quotes' work!"
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	_, _ = w.Write([]byte(pipeContent))
	_ = w.Close()
	defer func() { os.Stdin = oldStdin }()

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "true")
	_ = taskCreateCmd.Flags().Set("company", "")
	_ = taskCreateCmd.Flags().Set("project", "")
	_ = taskCreateCmd.Flags().Set("priority", "")
	_ = taskCreateCmd.Flags().Set("role", "")
	rootCmd.SetArgs([]string{"task", "create", "--dry-run"})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task create stdin pipe mode failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Inference Telemetry & Cost Engine") {
		t.Errorf("expected telemetry output for stdin piped task, got: %s", out)
	}
	if !strings.Contains(out, "Dry Run Mode") {
		t.Errorf("expected dry run notice, got: %s", out)
	}
}

func TestTaskAdd_AliasedCommand(t *testing.T) {
	homeDir := t.TempDir()
	_ = os.Setenv("HOME", homeDir)
	defer os.Unsetenv("HOME")

	cfg = nil

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskAddCmd.Flags().Set("dry-run", "true")
	_ = taskAddCmd.Flags().Set("priority", "high")
	_ = taskAddCmd.Flags().Set("role", "engineer")
	rootCmd.SetArgs([]string{"task", "add", "Implement telemetry buffering", "--dry-run", "--priority", "high", "--role", "engineer"})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task add failed: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Implement telemetry buffering") && !strings.Contains(out, "telemetry buffering") && !strings.Contains(out, "Objectives") {
		t.Errorf("expected task add output to contain generated task, got: %s", out)
	}
	if !strings.Contains(out, "Inference Telemetry & Cost Engine") {
		t.Errorf("expected telemetry in task add, got: %s", out)
	}
}

func TestRenderMarkdownSummary_WithDispositionAndClarification(t *testing.T) {
	task := ai.InferredTask{
		Organization:     "StayPoint",
		Project:          "StayPoint Core Engine",
		Title:            "Implement Clarification Flow",
		Description:      "## Objectives\nAdd modal.",
		Priority:         "high",
		Status:           "backlog",
		Labels:           []string{"tui", "ui"},
		AssigneeRole:     "CLI Specialist",
		AskClarification: "Should the modal be full screen or popup?",
	}

	rendered := renderMarkdownSummary(task)
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	clean := ansiRegex.ReplaceAllString(rendered, "")
	normalized := strings.Join(strings.Fields(clean), " ")

	if !strings.Contains(normalized, "BACKLOG") {
		t.Errorf("expected rendered card to contain BACKLOG disposition, got: %s", clean)
	}
	if !strings.Contains(normalized, "Clarification Requested") {
		t.Errorf("expected rendered card to contain Clarification Requested, got: %s", clean)
	}
	if !strings.Contains(normalized, "Should the modal be full screen or popup?") {
		t.Errorf("expected rendered card to contain clarification question, got: %s", clean)
	}
}

func TestTaskCreate_BacklogDisposition_DispatchesUnassigned(t *testing.T) {
	var receivedReq paperclip.CreateIssueRequest
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/issues") && r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&receivedReq)
			_ = json.NewEncoder(w).Encode(paperclip.IssueResponse{
				ID:          "mock-backlog-issue",
				Identifier:  "STA-200",
				Title:       receivedReq.Title,
				Description: receivedReq.Description,
				Status:      "backlog",
				Priority:    receivedReq.Priority,
				CompanyID:   "sta-comp-id",
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/agents") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode([]paperclip.AgentResponse{
				{ID: "agent-cos-123", Name: "Chief of Staff", Role: "ceo"},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/api/companies/sta-comp-id") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(paperclip.CompanyResponse{
				ID:          "sta-comp-id",
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
	os.Setenv("PAPERCLIP_COMPANY_ID", "sta-comp-id")
	os.Setenv("PAPERCLIP_API_KEY", "test-token")
	defer os.Unsetenv("PAPERCLIP_API_URL")
	defer os.Unsetenv("PAPERCLIP_COMPANY_ID")
	defer os.Unsetenv("PAPERCLIP_API_KEY")

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	_ = taskCreateCmd.Flags().Set("dry-run", "false")
	_ = taskCreateCmd.Flags().Set("yes", "true")
	rootCmd.SetArgs([]string{
		"task", "create",
		"Someday we might want to explore WebAssembly for statusline rendering",
		"--yes",
	})

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("task create failed: %v", err)
	}

	if receivedReq.Status == "backlog" {
		if receivedReq.AssigneeAgentId != "" {
			t.Errorf("expected backlog task to have empty AssigneeAgentId, got %q", receivedReq.AssigneeAgentId)
		}
	}
}

