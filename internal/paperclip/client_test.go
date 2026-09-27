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
