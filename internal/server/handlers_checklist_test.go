package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServer_REST_Checklist(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	baseURL := srv.URL()

	client := &http.Client{}

	doReq := func(method, urlStr string, body []byte) (*http.Response, []byte) {
		t.Helper()
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, urlStr, bodyReader)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)
		return resp, respBytes
	}

	// 1. Seed checklist
	seedPayload := []byte(`{"sprint":"STA-168"}`)
	resp, body := doReq("POST", baseURL+"/api/checklist/seed", seedPayload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from seed, got %d: %s", resp.StatusCode, string(body))
	}

	// 2. List sprints
	resp, body = doReq("GET", baseURL+"/api/checklist/sprints", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from sprints, got %d: %s", resp.StatusCode, string(body))
	}
	var sprintsResp struct {
		Sprints []string `json:"sprints"`
	}
	if err := json.Unmarshal(body, &sprintsResp); err != nil {
		t.Fatalf("unmarshal sprints failed: %v", err)
	}
	if len(sprintsResp.Sprints) == 0 || sprintsResp.Sprints[0] != "STA-168" {
		t.Fatalf("expected sprints to contain STA-168, got %v", sprintsResp.Sprints)
	}

	// 3. List items
	resp, body = doReq("GET", baseURL+"/api/checklist?sprint=STA-168", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from list items, got %d: %s", resp.StatusCode, string(body))
	}
	var itemsResp struct {
		Items []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Status  string `json:"status"`
			Version int    `json:"version"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &itemsResp); err != nil {
		t.Fatalf("unmarshal items failed: %v", err)
	}
	if len(itemsResp.Items) == 0 {
		t.Fatalf("expected seeded items, got 0")
	}

	firstID := itemsResp.Items[0].ID

	// 4. Update item status & notes
	patchPayload := []byte(`{"status":"pass","notes":"Verified working flawlessly"}`)
	resp, body = doReq("PATCH", fmt.Sprintf("%s/api/checklist/%s", baseURL, firstID), patchPayload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from patch, got %d: %s", resp.StatusCode, string(body))
	}
	var patchedItem struct {
		Status  string `json:"status"`
		Notes   string `json:"notes"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(body, &patchedItem); err != nil {
		t.Fatalf("unmarshal patch response failed: %v", err)
	}
	if patchedItem.Status != "pass" || patchedItem.Notes != "Verified working flawlessly" || patchedItem.Version != 2 {
		t.Fatalf("unexpected patch result: %+v", patchedItem)
	}

	// 4b. Update with partial status (and in_between alias)
	patchPartialPayload := []byte(`{"status":"in_between","notes":"Partially implemented, needs work"}`)
	resp, body = doReq("PATCH", fmt.Sprintf("%s/api/checklist/%s", baseURL, firstID), patchPartialPayload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from partial patch, got %d: %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &patchedItem); err != nil {
		t.Fatalf("unmarshal patch response failed: %v", err)
	}
	if patchedItem.Status != "partial" || patchedItem.Notes != "Partially implemented, needs work" || patchedItem.Version != 3 {
		t.Fatalf("unexpected partial patch result: %+v", patchedItem)
	}

	// 5. Get history
	resp, body = doReq("GET", fmt.Sprintf("%s/api/checklist/%s/history", baseURL, firstID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from history, got %d: %s", resp.StatusCode, string(body))
	}
	var historyResp struct {
		History []struct {
			ItemID string `json:"item_id"`
			Status string `json:"status"`
			Notes  string `json:"notes"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &historyResp); err != nil {
		t.Fatalf("unmarshal history failed: %v", err)
	}
	if len(historyResp.History) != 2 || historyResp.History[0].Status != "partial" || historyResp.History[1].Status != "pass" {
		t.Fatalf("unexpected history entries: %+v", historyResp.History)
	}
}

func TestServer_REST_Checklist_Evaluate(t *testing.T) {
	database := setupTestDB(t)
	srv, token := startTestServer(t, database)
	baseURL := srv.URL()

	client := &http.Client{}
	doReq := func(method, urlStr string, body []byte) (*http.Response, []byte) {
		t.Helper()
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, urlStr, bodyReader)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)
		return resp, respBytes
	}

	// 1. Seed checklist
	resp, body := doReq("POST", baseURL+"/api/checklist/seed", []byte(`{"sprint":"STA-168"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from seed, got %d: %s", resp.StatusCode, string(body))
	}

	// 2. List items and check contract presence
	resp, body = doReq("GET", baseURL+"/api/checklist?sprint=STA-168", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var itemsResp struct {
		Items []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Contract string `json:"contract"`
			Status   string `json:"status"`
			Notes    string `json:"notes"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &itemsResp); err != nil {
		t.Fatalf("unmarshal items failed: %v", err)
	}

	contractsCount := 0
	var failingItemID string
	for _, it := range itemsResp.Items {
		if it.Contract != "" {
			contractsCount++
			if it.Title == "Web UI reads tasks from native StayPoint SQLite" {
				failingItemID = it.ID
			}
		}
	}

	if contractsCount < 3 {
		t.Fatalf("expected at least 3 items with contracts, found %d", contractsCount)
	}
	if failingItemID == "" {
		t.Fatal("could not find Architecture SQLite item")
	}

	// 3. Mark the failing item as "pass" manually to simulate human verification
	patchPayload := []byte(`{"status":"pass","notes":"Prematurely marked pass"}`)
	resp, body = doReq("PATCH", fmt.Sprintf("%s/api/checklist/%s", baseURL, failingItemID), patchPayload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to mark item pass: %s", string(body))
	}

	// 4. Trigger divergence evaluation
	resp, body = doReq("POST", baseURL+"/api/checklist/evaluate?sprint=STA-168&downgrade=true", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from evaluate, got %d: %s", resp.StatusCode, string(body))
	}

	var evalResp struct {
		Sprint      string `json:"sprint"`
		Total       int    `json:"total"`
		Passed      int    `json:"passed"`
		Failed      int    `json:"failed"`
		Divergences int    `json:"divergences"`
		Results     []struct {
			ItemID         string `json:"item_id"`
			PreviousStatus string `json:"previous_status"`
			NewStatus      string `json:"new_status"`
			Diverged       bool   `json:"diverged"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &evalResp); err != nil {
		t.Fatalf("unmarshal evaluate failed: %v", err)
	}

	if evalResp.Total < 3 {
		t.Fatalf("expected at least 3 evaluated contracts, got %d", evalResp.Total)
	}
	if evalResp.Divergences < 1 {
		t.Fatalf("expected at least 1 divergence detected, got %d", evalResp.Divergences)
	}

	// 5. Verify that the item was downgraded from pass to fail in DB
	resp, body = doReq("GET", baseURL+"/api/checklist?sprint=STA-168", nil)
	if err := json.Unmarshal(body, &itemsResp); err != nil {
		t.Fatal(err)
	}

	var checkedItem *struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Contract string `json:"contract"`
		Status   string `json:"status"`
		Notes    string `json:"notes"`
	}
	for i := range itemsResp.Items {
		if itemsResp.Items[i].ID == failingItemID {
			checkedItem = &itemsResp.Items[i]
			break
		}
	}
	if checkedItem == nil {
		t.Fatal("item not found after evaluate")
	}
	if checkedItem.Status != "fail" {
		t.Fatalf("expected status to be auto-downgraded to 'fail', got %s", checkedItem.Status)
	}
	if !strings.Contains(checkedItem.Notes, "REGRESSION DIVERGENCE") {
		t.Fatalf("expected notes to contain REGRESSION DIVERGENCE, got: %s", checkedItem.Notes)
	}

	// 6. Verify audit history record was inserted with changed_by="divergence-detector"
	resp, body = doReq("GET", fmt.Sprintf("%s/api/checklist/%s/history", baseURL, failingItemID), nil)
	var histResp struct {
		History []struct {
			Status    string `json:"status"`
			ChangedBy string `json:"changed_by"`
			Notes     string `json:"notes"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &histResp); err != nil {
		t.Fatal(err)
	}
	if len(histResp.History) == 0 || histResp.History[0].Status != "fail" || histResp.History[0].ChangedBy != "divergence-detector" {
		t.Fatalf("expected latest history entry by divergence-detector with fail, got %+v", histResp.History)
	}
}
