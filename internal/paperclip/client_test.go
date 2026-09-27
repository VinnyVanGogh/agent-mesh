package paperclip

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_CreateIssue(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Authorization header with token")
		}
		if r.URL.Path != "/api/companies/comp-123/issues" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req CreateIssueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Title != "Test Issue" {
			t.Errorf("expected title 'Test Issue', got %q", req.Title)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := IssueResponse{
			ID:          "issue-xyz-789",
			Identifier:  "STA-22",
			Title:       req.Title,
			Description: req.Description,
			Status:      "todo",
			Priority:    req.Priority,
			CompanyID:   "comp-123",
			IssueNumber: 22,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-api-key")
	res, err := client.CreateIssue(context.Background(), "comp-123", CreateIssueRequest{
		Title:       "Test Issue",
		Description: "Detailed description",
		Priority:    "high",
		Labels:      []string{"test", "cli"},
	})
	if err != nil {
		t.Fatalf("unexpected error creating issue: %v", err)
	}

	if res.ID != "issue-xyz-789" || res.Identifier != "STA-22" {
		t.Errorf("unexpected issue response: %+v", res)
	}

	url := client.IssueURL("STA", res.ID)
	expectedURL := ts.URL + "/STA/issues/issue-xyz-789"
	if url != expectedURL {
		t.Errorf("expected issue URL %s, got %s", expectedURL, url)
	}
}

func TestClient_GetCompany(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CompanyResponse{
			ID:          "comp-123",
			Name:        "StayPoint",
			IssuePrefix: "STA",
			Status:      "active",
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-key")
	comp, err := client.GetCompany(context.Background(), "comp-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comp.Name != "StayPoint" || comp.IssuePrefix != "STA" {
		t.Errorf("unexpected company response: %+v", comp)
	}
}

func TestClient_ListCompaniesAndResolve(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		comps := []CompanyResponse{
			{ID: "sta-1", Name: "StayPoint", IssuePrefix: "STA", Status: "active"},
			{ID: "res-1", Name: "Research & Intelligence", IssuePrefix: "RES", Status: "active"},
			{ID: "man-1", Name: "Managed Solution", IssuePrefix: "MAN", Status: "active"},
		}
		_ = json.NewEncoder(w).Encode(comps)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-key")
	companies, err := client.ListCompanies(context.Background())
	if err != nil {
		t.Fatalf("unexpected error listing companies: %v", err)
	}
	if len(companies) != 3 {
		t.Fatalf("expected 3 companies, got %d", len(companies))
	}

	// Resolve Research
	comp, err := client.ResolveCompany(context.Background(), "Research")
	if err != nil {
		t.Fatalf("unexpected error resolving Research: %v", err)
	}
	if comp.ID != "res-1" || comp.IssuePrefix != "RES" {
		t.Errorf("expected res-1 (RES), got %+v", comp)
	}

	// Resolve StayPoint
	comp, err = client.ResolveCompany(context.Background(), "StayPoint")
	if err != nil {
		t.Fatalf("unexpected error resolving StayPoint: %v", err)
	}
	if comp.ID != "sta-1" || comp.IssuePrefix != "STA" {
		t.Errorf("expected sta-1 (STA), got %+v", comp)
	}
}

func TestClient_ListActiveIssues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		issues := []IssueResponse{
			{ID: "issue-1", Identifier: "STA-21", Title: "Dynamic task generator", Status: "in_progress", Priority: "high"},
			{ID: "issue-2", Identifier: "STA-20", Title: "Architecture RFC", Status: "done", Priority: "medium"},
		}
		_ = json.NewEncoder(w).Encode(issues)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-key")
	issues, err := client.ListActiveIssues(context.Background(), "comp-123")
	if err != nil {
		t.Fatalf("unexpected error listing issues: %v", err)
	}
	if len(issues) != 2 || issues[0].Identifier != "STA-21" {
		t.Errorf("unexpected issues: %+v", issues)
	}
}

