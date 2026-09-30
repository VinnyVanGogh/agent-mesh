package conversation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
)

func setupTestStore(t *testing.T) (*ConversationStore, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "conv_test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	convStore := NewStore(store.DB())
	cleanup := func() {
		store.Close()
	}
	return convStore, cleanup
}

func TestStore_CreateAndGetSession(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	conv := &Conversation{
		ID:           "sess_test_1",
		Title:        "Weather Consultation",
		RepoPath:     "/test/repo",
		SystemPrompt: "You are a weather assistant.",
		Messages: []Message{
			{
				Role:       RoleUser,
				Content:    "What is the weather?",
				TokenCount: 5,
			},
			{
				Role:       RoleAssistant,
				Content:    "Let me check.",
				TokenCount: 15,
				ToolCalls: []ToolCall{
					{
						ID:        "tc_1",
						Name:      "get_weather",
						Arguments: `{"city":"Austin"}`,
					},
				},
			},
			{
				Role: RoleTool,
				ToolResults: []ToolResult{
					{
						ToolCallID: "tc_1",
						Name:       "get_weather",
						Content:    "75F and clear",
						IsError:    false,
					},
				},
			},
			{
				Role:       RoleAssistant,
				Content:    "It is 75F and clear in Austin.",
				TokenCount: 10,
			},
		},
		ProviderHandles: map[string]*ProviderHandle{
			"anthropic": {
				Provider: "anthropic",
				Handle:   "msg_12345",
				Model:    "claude-3-5-sonnet",
			},
			"openai": {
				Provider: "openai",
				Handle:   "thread_abcde",
				Model:    "gpt-4o",
			},
		},
		Metadata: map[string]interface{}{
			"environment": "staging",
		},
	}

	if err := s.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	got, err := s.GetSession(ctx, "sess_test_1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if got.ID != conv.ID {
		t.Errorf("ID mismatch: %s vs %s", got.ID, conv.ID)
	}
	if got.Title != conv.Title {
		t.Errorf("Title mismatch: %s vs %s", got.Title, conv.Title)
	}
	if got.RepoPath != conv.RepoPath {
		t.Errorf("RepoPath mismatch: %s vs %s", got.RepoPath, conv.RepoPath)
	}
	if got.SystemPrompt != conv.SystemPrompt {
		t.Errorf("SystemPrompt mismatch: %s vs %s", got.SystemPrompt, conv.SystemPrompt)
	}
	if len(got.Messages) != len(conv.Messages) {
		t.Fatalf("Messages length mismatch: %d vs %d", len(got.Messages), len(conv.Messages))
	}

	// Verify tool calls on assistant message
	if len(got.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call on msg 1, got %d", len(got.Messages[1].ToolCalls))
	}
	tc := got.Messages[1].ToolCalls[0]
	if tc.ID != "tc_1" || tc.Name != "get_weather" || tc.Arguments != `{"city":"Austin"}` {
		t.Errorf("unexpected tool call: %+v", tc)
	}

	// Verify tool results
	if len(got.Messages[2].ToolResults) != 1 {
		t.Fatalf("expected 1 tool result on msg 2, got %d", len(got.Messages[2].ToolResults))
	}
	tr := got.Messages[2].ToolResults[0]
	if tr.ToolCallID != "tc_1" || tr.Content != "75F and clear" {
		t.Errorf("unexpected tool result: %+v", tr)
	}

	// Verify provider handles
	if len(got.ProviderHandles) != 2 {
		t.Fatalf("expected 2 provider handles, got %d", len(got.ProviderHandles))
	}
	if got.ProviderHandles["anthropic"].Handle != "msg_12345" {
		t.Errorf("anthropic handle mismatch: %+v", got.ProviderHandles["anthropic"])
	}
}

func TestStore_AppendMessage(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	conv := &Conversation{
		ID:    "sess_append",
		Title: "Append Test",
		Messages: []Message{
			{Role: RoleUser, Content: "First message"},
		},
	}
	if err := s.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	newMsg := &Message{
		Role:    RoleAssistant,
		Content: "Second message",
	}
	if err := s.AppendMessage(ctx, conv.ID, newMsg); err != nil {
		t.Fatalf("AppendMessage failed: %v", err)
	}

	got, err := s.GetSession(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if got.Messages[0].SequenceNum != 0 || got.Messages[1].SequenceNum != 1 {
		t.Errorf("unexpected sequence numbers: %d, %d", got.Messages[0].SequenceNum, got.Messages[1].SequenceNum)
	}
	if got.Messages[1].Content != "Second message" {
		t.Errorf("unexpected content: %s", got.Messages[1].Content)
	}
}

func TestStore_UpdateSession(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	conv := &Conversation{
		ID:    "sess_update",
		Title: "Initial Title",
	}
	if err := s.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	conv.Title = "Updated Title"
	conv.RepoPath = "/new/path"
	if err := s.UpdateSession(ctx, conv); err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	got, err := s.GetSession(ctx, conv.ID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.Title != "Updated Title" || got.RepoPath != "/new/path" {
		t.Errorf("unexpected updated session: %+v", got)
	}
}

func TestStore_ListSessions(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		c := &Conversation{
			ID:    newID(),
			Title: "Session " + string(rune('0'+i)),
			Messages: []Message{
				{Role: RoleUser, Content: "Hello"},
			},
		}
		if err := s.CreateSession(ctx, c); err != nil {
			t.Fatalf("failed to create session %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	list, err := s.ListSessions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
	}
	if list[0].MessageCount != 1 {
		t.Errorf("expected message count 1, got %d", list[0].MessageCount)
	}
}

func TestStore_ProviderHandles(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	conv := &Conversation{ID: "sess_ph", Title: "Handles Test"}
	if err := s.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	h := &ProviderHandle{
		SessionID: conv.ID,
		Provider:  "gemini",
		Handle:    "gemini_turn_99",
		Model:     "gemini-2.0-flash",
	}
	if err := s.SetProviderHandle(ctx, h); err != nil {
		t.Fatalf("SetProviderHandle failed: %v", err)
	}

	got, err := s.GetProviderHandle(ctx, conv.ID, "gemini")
	if err != nil {
		t.Fatalf("GetProviderHandle failed: %v", err)
	}
	if got == nil || got.Handle != "gemini_turn_99" || got.Model != "gemini-2.0-flash" {
		t.Fatalf("unexpected handle retrieved: %+v", got)
	}

	// Update handle
	h.Handle = "gemini_turn_100"
	if err := s.SetProviderHandle(ctx, h); err != nil {
		t.Fatalf("SetProviderHandle update failed: %v", err)
	}

	gotUpdated, err := s.GetProviderHandle(ctx, conv.ID, "gemini")
	if err != nil || gotUpdated.Handle != "gemini_turn_100" {
		t.Fatalf("expected updated handle gemini_turn_100, got: %+v", gotUpdated)
	}

	all, err := s.ListProviderHandles(ctx, conv.ID)
	if err != nil {
		t.Fatalf("ListProviderHandles failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 handle, got %d", len(all))
	}
}

func TestStore_CascadeDelete(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	conv := &Conversation{
		ID:    "sess_cascade",
		Title: "Cascade Test",
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: "Hello",
			},
			{
				Role:    RoleAssistant,
				Content: "Tool time",
				ToolCalls: []ToolCall{
					{ID: "tc_casc", Name: "test_tool", Arguments: "{}"},
				},
			},
		},
		ProviderHandles: map[string]*ProviderHandle{
			"anthropic": {Provider: "anthropic", Handle: "h_casc"},
		},
	}

	if err := s.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if err := s.DeleteSession(ctx, conv.ID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Verify session is gone
	_, err := s.GetSession(ctx, conv.ID)
	if err == nil {
		t.Fatalf("expected error getting deleted session")
	}

	// Check child tables directly
	var msgCount, tcCount, phCount int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM chat_messages WHERE session_id = 'sess_cascade';").Scan(&msgCount)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM chat_tool_calls WHERE id = 'tc_casc';").Scan(&tcCount)
	_ = s.db.QueryRow("SELECT COUNT(*) FROM session_provider_handles WHERE session_id = 'sess_cascade';").Scan(&phCount)

	if msgCount != 0 || tcCount != 0 || phCount != 0 {
		t.Errorf("cascade delete failed: msgCount=%d, tcCount=%d, phCount=%d", msgCount, tcCount, phCount)
	}
}

func TestStore_PersistenceAcrossReopen(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "reopen.db")

	store1, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	ctx := context.Background()
	convStore1 := NewStore(store1.DB())

	conv := &Conversation{
		ID:           "sess_reopen",
		Title:        "Persist Test",
		SystemPrompt: "Keep this across re-opens.",
		Messages: []Message{
			{Role: RoleUser, Content: "Persisted question"},
		},
	}
	if err := convStore1.CreateSession(ctx, conv); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	store1.Close()

	// Reopen
	store2, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer store2.Close()

	convStore2 := NewStore(store2.DB())
	got, err := convStore2.GetSession(ctx, "sess_reopen")
	if err != nil {
		t.Fatalf("failed to get session after reopen: %v", err)
	}
	if got.Title != "Persist Test" || got.SystemPrompt != "Keep this across re-opens." || len(got.Messages) != 1 {
		t.Errorf("data mismatch after reopen: %+v", got)
	}
}
