package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	if len(historyResp.History) != 1 || historyResp.History[0].Status != "pass" {
		t.Fatalf("unexpected history entries: %+v", historyResp.History)
	}
}
