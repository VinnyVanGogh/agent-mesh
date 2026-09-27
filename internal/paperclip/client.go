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
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    string   `json:"priority,omitempty"` // low, medium, high, urgent, critical
	ProjectId   string   `json:"projectId,omitempty"`
	Labels      []string `json:"labels,omitempty"`
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

// CreateIssue sends a POST request to /api/companies/:companyId/issues.
func (c *Client) CreateIssue(ctx context.Context, companyID string, req CreateIssueRequest) (*IssueResponse, error) {
	if companyID == "" {
		companyID = os.Getenv("PAPERCLIP_COMPANY_ID")
		if companyID == "" {
			return nil, errors.New("companyID is required (specify via flag or PAPERCLIP_COMPANY_ID)")
		}
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
