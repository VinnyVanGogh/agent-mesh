package paperclip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CreateIssueRequest represents the JSON payload dispatched to Paperclip.
type CreateIssueRequest struct {
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Priority        string   `json:"priority,omitempty"` // low, medium, high, urgent, critical
	ProjectId       string   `json:"projectId,omitempty"`
	AssigneeAgentId string   `json:"assigneeAgentId,omitempty"`
	Labels          []string `json:"labels,omitempty"`
}

// IssueResponse represents the issue returned by the Paperclip API.
type IssueResponse struct {
	ID          string   `json:"id"`
	Identifier  string   `json:"identifier"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	CompanyID   string   `json:"companyId"`
	ProjectID   string   `json:"projectId"`
	IssueNumber int      `json:"issueNumber"`
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"createdAt"`
}

// CompanyResponse represents company metadata returned by /api/companies/:id.
type CompanyResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IssuePrefix string `json:"issuePrefix"`
	Status      string `json:"status"`
}

// Client wraps HTTP communication with the Paperclip API control plane.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient creates a Paperclip client with environment variable fallbacks.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv("PAPERCLIP_API_URL")
		if baseURL == "" {
			baseURL = "http://127.0.0.1:3100"
		}
	}
	if apiKey == "" {
		apiKey = os.Getenv("PAPERCLIP_API_KEY")
	}

	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// NormalizePriority maps priority levels (including "urgent") to valid Paperclip enum values.
func NormalizePriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "urgent", "critical", "crit":
		return "critical"
	case "high":
		return "high"
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	default:
		return "medium"
	}
}

// CreateIssue sends a POST request to /api/companies/:companyId/issues.
func (c *Client) CreateIssue(ctx context.Context, companyID string, req CreateIssueRequest) (*IssueResponse, error) {
	if companyID == "" {
		companyID = os.Getenv("PAPERCLIP_COMPANY_ID")
		if companyID == "" {
			return nil, errors.New("companyID is required (specify via flag or PAPERCLIP_COMPANY_ID)")
		}
	}

	if req.Priority != "" {
		req.Priority = NormalizePriority(req.Priority)
	}

	url := fmt.Sprintf("%s/api/companies/%s/issues", c.BaseURL, companyID)

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode issue request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paperclip connection error: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && c.APIKey != "" {
		// Retry without API key for local board instance / cross-company dispatch
		retryReq, errRetry := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
		if errRetry == nil {
			retryReq.Header.Set("Content-Type", "application/json")
			if retryResp, errDo := c.HTTPClient.Do(retryReq); errDo == nil {
				defer retryResp.Body.Close()
				if retryBytes, errRead := io.ReadAll(retryResp.Body); errRead == nil && retryResp.StatusCode >= 200 && retryResp.StatusCode < 300 {
					var created IssueResponse
					if err := json.Unmarshal(retryBytes, &created); err == nil {
						return &created, nil
					}
				}
			}
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("paperclip API error (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	var created IssueResponse
	if err := json.Unmarshal(respBytes, &created); err != nil {
		return nil, fmt.Errorf("failed to parse created issue: %w", err)
	}

	return &created, nil
}

// GetCompany fetches company metadata (including issue prefix).
func (c *Client) GetCompany(ctx context.Context, companyID string) (*CompanyResponse, error) {
	if companyID == "" {
		companyID = os.Getenv("PAPERCLIP_COMPANY_ID")
		if companyID == "" {
			return nil, errors.New("companyID is required")
		}
	}

	url := fmt.Sprintf("%s/api/companies/%s", c.BaseURL, companyID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var comp CompanyResponse
	if err := json.NewDecoder(resp.Body).Decode(&comp); err != nil {
		return nil, err
	}
	return &comp, nil
}

// IssueURL generates a clickable web link for an issue matching RFC-001 format:
// http://127.0.0.1:3100/<PREFIX>/issues/<ID>
func (c *Client) IssueURL(companyPrefix string, issueID string) string {
	prefix := companyPrefix
	if prefix == "" {
		prefix = "issues"
	}
	return fmt.Sprintf("%s/%s/issues/%s", c.BaseURL, prefix, issueID)
}

// ProjectResponse represents project metadata returned by /api/companies/:id/projects.
type ProjectResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListCompanies fetches all companies from /api/companies.
func (c *Client) ListCompanies(ctx context.Context) ([]CompanyResponse, error) {
	url := fmt.Sprintf("%s/api/companies", c.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paperclip connection error: %w", err)
	}
	defer resp.Body.Close()

	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && c.APIKey != "" {
		// Retry without agent key (local board mode)
		retryReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		if retryResp, errRetry := c.HTTPClient.Do(retryReq); errRetry == nil {
			defer retryResp.Body.Close()
			if retryResp.StatusCode == http.StatusOK {
				var companies []CompanyResponse
				if errDec := json.NewDecoder(retryResp.Body).Decode(&companies); errDec == nil {
					return companies, nil
				}
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var companies []CompanyResponse
	if err := json.NewDecoder(resp.Body).Decode(&companies); err != nil {
		return nil, fmt.Errorf("failed to decode companies: %w", err)
	}
	return companies, nil
}

// ResolveCompany attempts to find a company matching the organization name or prefix.
func (c *Client) ResolveCompany(ctx context.Context, orgName string) (*CompanyResponse, error) {
	companies, err := c.ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	if len(companies) == 0 {
		return nil, errors.New("no companies found in Paperclip")
	}

	normalized := strings.ToLower(strings.TrimSpace(orgName))

	// 1. Exact or prefix match
	for i := range companies {
		if strings.EqualFold(companies[i].IssuePrefix, orgName) || strings.EqualFold(companies[i].Name, orgName) {
			return &companies[i], nil
		}
	}

	// 2. Fuzzy / substring match
	for i := range companies {
		compNameLower := strings.ToLower(companies[i].Name)
		if strings.Contains(compNameLower, normalized) || strings.Contains(normalized, compNameLower) {
			return &companies[i], nil
		}
	}

	// 3. Domain keyword mapping
	for i := range companies {
		compNameLower := strings.ToLower(companies[i].Name)
		if strings.Contains(normalized, "research") && strings.Contains(compNameLower, "research") {
			return &companies[i], nil
		}
		if (strings.Contains(normalized, "staypoint") || strings.Contains(normalized, "stay point")) && strings.Contains(compNameLower, "staypoint") {
			return &companies[i], nil
		}
		if (strings.Contains(normalized, "managed solution") || strings.Contains(normalized, "mansol")) && strings.Contains(compNameLower, "managed solution") {
			return &companies[i], nil
		}
		if (strings.Contains(normalized, "runelite") || strings.Contains(normalized, "osrs")) && strings.Contains(compNameLower, "runelite") {
			return &companies[i], nil
		}
		if strings.Contains(normalized, "maintenance") && (strings.Contains(compNameLower, "maintenance") || strings.EqualFold(companies[i].IssuePrefix, "PER")) {
			return &companies[i], nil
		}
	}

	// Fallback to first company
	return &companies[0], nil
}

// ListProjects fetches all projects for a company from /api/companies/:id/projects.
func (c *Client) ListProjects(ctx context.Context, companyID string) ([]ProjectResponse, error) {
	url := fmt.Sprintf("%s/api/companies/%s/projects", c.BaseURL, companyID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paperclip connection error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var projects []ProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, fmt.Errorf("failed to decode projects: %w", err)
	}
	return projects, nil
}

// ResolveProject attempts to find a project matching the project name.
func (c *Client) ResolveProject(ctx context.Context, companyID, projectName string) (string, error) {
	projects, err := c.ListProjects(ctx, companyID)
	if err != nil || len(projects) == 0 {
		return "", err
	}
	norm := strings.ToLower(strings.TrimSpace(projectName))
	for _, p := range projects {
		if strings.EqualFold(p.Name, projectName) {
			return p.ID, nil
		}
	}
	for _, p := range projects {
		pLower := strings.ToLower(p.Name)
		if strings.Contains(pLower, norm) || strings.Contains(norm, pLower) {
			return p.ID, nil
		}
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	return "", nil
}

// ListActiveIssues fetches active issues for a company.
func (c *Client) ListActiveIssues(ctx context.Context, companyID string) ([]IssueResponse, error) {
	url := fmt.Sprintf("%s/api/companies/%s/issues?status=in_progress,todo,backlog", c.BaseURL, companyID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paperclip connection error: %w", err)
	}
	defer resp.Body.Close()

	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) && c.APIKey != "" {
		retryReq, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		if retryResp, errRetry := c.HTTPClient.Do(retryReq); errRetry == nil {
			defer retryResp.Body.Close()
			if retryResp.StatusCode == http.StatusOK {
				var issues []IssueResponse
				if errDec := json.NewDecoder(retryResp.Body).Decode(&issues); errDec == nil {
					return issues, nil
				}
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var issues []IssueResponse
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return nil, fmt.Errorf("failed to decode issues: %w", err)
	}
	return issues, nil
}

// AgentResponse represents an agent returned by the Paperclip API.
type AgentResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// ListAgents fetches all agents for a company.
func (c *Client) ListAgents(ctx context.Context, companyID string) ([]AgentResponse, error) {
	url := fmt.Sprintf("%s/api/companies/%s/agents", c.BaseURL, companyID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("paperclip connection error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var agents []AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, fmt.Errorf("failed to decode agents: %w", err)
	}
	return agents, nil
}
