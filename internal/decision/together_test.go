package decision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testOptions = []Option{
	{Key: "proceed", Letter: "A", Label: "Proceed"},
	{Key: "hold", Letter: "B", Label: "Hold"},
}

var testReq = DecisionRequest{
	State:    "stage=in_progress task_id=t1",
	Question: "Should we transition to in_review?",
	Options:  testOptions,
}

// golden fixture: well-formed response selecting option A.
const fixtureOptionA = `{"choices":[{"message":{"content":"A"}}]}`

// golden fixture: well-formed response selecting option B.
const fixtureOptionB = `{"choices":[{"message":{"content":"B. hold\n"}}]}`

// golden fixture: response with no matching letter.
const fixtureUnknown = `{"choices":[{"message":{"content":"Z"}}]}`

// golden fixture: empty choices slice.
const fixtureEmpty = `{"choices":[]}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *TogetherDecisionClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &TogetherDecisionClient{
		baseURL:    srv.URL,
		model:      "tev1-4b",
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func TestDecide_ValidOptionA(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixtureOptionA))
	})
	res, err := c.Decide(context.Background(), testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedKey != "proceed" {
		t.Errorf("got key=%q, want proceed", res.SelectedKey)
	}
	if res.SelectedLetter != "A" {
		t.Errorf("got letter=%q, want A", res.SelectedLetter)
	}
}

func TestDecide_ValidOptionB(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixtureOptionB))
	})
	res, err := c.Decide(context.Background(), testReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.SelectedKey != "hold" {
		t.Errorf("got key=%q, want hold", res.SelectedKey)
	}
}

func TestDecide_UnknownLetter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixtureUnknown))
	})
	_, err := c.Decide(context.Background(), testReq)
	if err == nil {
		t.Fatal("expected error for unknown letter, got nil")
	}
}

func TestDecide_EmptyChoices(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixtureEmpty))
	})
	_, err := c.Decide(context.Background(), testReq)
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
}

func TestDecide_NonOKStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})
	_, err := c.Decide(context.Background(), testReq)
	if err == nil {
		t.Fatal("expected error for 500 status, got nil")
	}
}

func TestDecide_ContextCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Write([]byte(fixtureOptionA))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Decide(ctx, testReq)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
}

func TestNewTogether_NoKey(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "")
	_, err := NewTogether()
	if err == nil {
		t.Fatal("expected ErrNoKey, got nil")
	}
	if err != ErrNoKey {
		t.Errorf("got %v, want ErrNoKey", err)
	}
}

func TestNew_DefaultsToLocal(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "")
	t.Setenv("DECISION_BASE_URL", "")
	t.Setenv("TOGETHER_BASE_URL", "")
	t.Setenv("DECISION_MODEL", "")

	c, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.baseURL != defaultLocalURL {
		t.Errorf("baseURL=%q, want %q", c.baseURL, defaultLocalURL)
	}
	if c.model != defaultLocalModel {
		t.Errorf("model=%q, want %q", c.model, defaultLocalModel)
	}
	if c.apiKey != "" {
		t.Errorf("apiKey should be empty for local mode, got %q", c.apiKey)
	}
}

func TestNew_TogetherWhenKeySet(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "sk-test")
	t.Setenv("DECISION_BASE_URL", "")
	t.Setenv("TOGETHER_BASE_URL", "")
	t.Setenv("DECISION_MODEL", "")

	c, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.baseURL != defaultTogetherURL {
		t.Errorf("baseURL=%q, want %q", c.baseURL, defaultTogetherURL)
	}
	if c.apiKey != "sk-test" {
		t.Errorf("apiKey=%q, want sk-test", c.apiKey)
	}
}

func TestBuildPrompt(t *testing.T) {
	prompt := buildPrompt(testReq)
	for _, opt := range testOptions {
		if !strings.Contains(prompt, opt.Letter) {
			t.Errorf("prompt missing option letter %q", opt.Letter)
		}
		if !strings.Contains(prompt, opt.Label) {
			t.Errorf("prompt missing option label %q", opt.Label)
		}
	}
	if !strings.Contains(prompt, testReq.Question) {
		t.Errorf("prompt missing question")
	}
}
