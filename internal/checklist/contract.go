package checklist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ContractType represents the verification mechanism.
type ContractType string

const (
	ContractTypeFilePattern ContractType = "file_pattern"
	ContractTypeCommand     ContractType = "command"
	ContractTypeHTTP        ContractType = "http"
)

// Contract defines an automated assertion attached to a checklist item.
type Contract struct {
	Type ContractType `json:"type"` // "file_pattern", "command", "http"
	// file_pattern options
	FilePath       string   `json:"file_path,omitempty"`        // repo-relative file path
	MustContain    []string `json:"must_contain,omitempty"`     // substrings that MUST be present
	MustNotContain []string `json:"must_not_contain,omitempty"` // substrings that MUST NOT be present

	// command options
	Command string `json:"command,omitempty"` // shell command to execute

	// http options
	Method         string   `json:"method,omitempty"`          // GET, POST, etc. (default GET)
	Path           string   `json:"path,omitempty"`            // e.g. "/api/checklist/sprints"
	ExpectedStatus int      `json:"expected_status,omitempty"` // expected HTTP status code (default 200)
	BodyContains   []string `json:"body_contains,omitempty"`   // substrings that must appear in body
}

// EvaluationResult contains the outcome of evaluating a contract.
type EvaluationResult struct {
	Passed  bool   `json:"passed"`
	Reason  string `json:"reason,omitempty"`
	Details string `json:"details,omitempty"`
}

// EvaluateContract executes a contract assertion.
func EvaluateContract(ctx context.Context, c Contract, repoRoot string, httpClient *http.Client, baseURL string) EvaluationResult {
	if repoRoot == "" {
		repoRoot = "."
	}

	switch c.Type {
	case ContractTypeFilePattern:
		return evaluateFilePattern(c, repoRoot)
	case ContractTypeCommand:
		return evaluateCommand(ctx, c, repoRoot)
	case ContractTypeHTTP:
		return evaluateHTTP(ctx, c, httpClient, baseURL)
	default:
		return EvaluationResult{
			Passed: false,
			Reason: fmt.Sprintf("unsupported contract type: %q", c.Type),
		}
	}
}

func evaluateFilePattern(c Contract, repoRoot string) EvaluationResult {
	if c.FilePath == "" {
		return EvaluationResult{Passed: false, Reason: "missing file_path in contract"}
	}
	fullPath := filepath.Join(repoRoot, c.FilePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return EvaluationResult{
			Passed: false,
			Reason: fmt.Sprintf("failed to read file %s: %v", c.FilePath, err),
		}
	}
	contentStr := string(content)

	for _, pat := range c.MustContain {
		if !strings.Contains(contentStr, pat) {
			return EvaluationResult{
				Passed: false,
				Reason: fmt.Sprintf("file %s is missing required pattern %q", c.FilePath, pat),
			}
		}
	}

	for _, pat := range c.MustNotContain {
		if strings.Contains(contentStr, pat) {
			return EvaluationResult{
				Passed: false,
				Reason: fmt.Sprintf("file %s contains forbidden pattern %q", c.FilePath, pat),
			}
		}
	}

	return EvaluationResult{Passed: true}
}

func evaluateCommand(ctx context.Context, c Contract, repoRoot string) EvaluationResult {
	if strings.TrimSpace(c.Command) == "" {
		return EvaluationResult{Passed: false, Reason: "missing command in contract"}
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", c.Command)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	if err != nil {
		return EvaluationResult{
			Passed:  false,
			Reason:  fmt.Sprintf("command %q failed: %v", c.Command, err),
			Details: outStr,
		}
	}

	return EvaluationResult{
		Passed:  true,
		Details: outStr,
	}
}

func evaluateHTTP(ctx context.Context, c Contract, httpClient *http.Client, baseURL string) EvaluationResult {
	if baseURL == "" {
		return EvaluationResult{
			Passed: false,
			Reason: "baseURL required for HTTP contract evaluation",
		}
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	method := c.Method
	if method == "" {
		method = "GET"
	}
	expectedStatus := c.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}

	urlStr := strings.TrimRight(baseURL, "/") + c.Path
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return EvaluationResult{
			Passed: false,
			Reason: fmt.Sprintf("failed to create HTTP request to %s: %v", urlStr, err),
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return EvaluationResult{
			Passed: false,
			Reason: fmt.Sprintf("HTTP request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return EvaluationResult{
			Passed: false,
			Reason: fmt.Sprintf("failed to read response body: %v", err),
		}
	}
	bodyStr := string(bodyBytes)

	if resp.StatusCode != expectedStatus {
		return EvaluationResult{
			Passed:  false,
			Reason:  fmt.Sprintf("HTTP status mismatch: expected %d, got %d", expectedStatus, resp.StatusCode),
			Details: bodyStr,
		}
	}

	for _, pat := range c.BodyContains {
		if !strings.Contains(bodyStr, pat) {
			return EvaluationResult{
				Passed:  false,
				Reason:  fmt.Sprintf("HTTP response body missing expected pattern %q", pat),
				Details: bodyStr,
			}
		}
	}

	return EvaluationResult{
		Passed:  true,
		Details: bodyStr,
	}
}

// ParseContract parses a raw JSON contract string.
func ParseContract(raw string) (*Contract, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var c Contract
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("invalid contract json: %w", err)
	}
	return &c, nil
}
