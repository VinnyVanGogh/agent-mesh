package chat

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/conversation"
	tea "github.com/charmbracelet/bubbletea"
)

// TrackingAdapter records executions and returns customizable responses.
type TrackingAdapter struct {
	mu           sync.Mutex
	providerName string
	binaryName   string
	execCalls    []adapter.ExecRequest
	responseFn   func(req adapter.ExecRequest) (string, string) // returns response text, sessionID
}

func (a *TrackingAdapter) Provider() string          { return a.providerName }
func (a *TrackingAdapter) BinaryName() string        { return a.binaryName }
func (a *TrackingAdapter) KnownMajorVersions() []int { return []int{1, 2} }
func (a *TrackingAdapter) BuildArgs(opts adapter.ParsedOptions) []string {
	return []string{a.providerName}
}

func (a *TrackingAdapter) Execute(ctx context.Context, req adapter.ExecRequest) error {
	a.mu.Lock()
	a.execCalls = append(a.execCalls, req)
	respText, sessID := "", ""
	if a.responseFn != nil {
		respText, sessID = a.responseFn(req)
	}
	a.mu.Unlock()

	// Write simulated stream-json lines
	if sessID != "" {
		initLine := fmt.Sprintf(`{"type":"system","subtype":"init","session_id":"%s"}`, sessID)
		_, _ = req.Stdout.Write([]byte(initLine + "\n"))
	}
	textLine := fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}`, respText)
	_, _ = req.Stdout.Write([]byte(textLine + "\n"))

	resultLine := fmt.Sprintf(`{"type":"result","subtype":"success","session_id":"%s","result":%q,"usage":{"output_tokens":10}}`, sessID, respText)
	_, _ = req.Stdout.Write([]byte(resultLine + "\n"))
	return nil
}

func (a *TrackingAdapter) ParseStreamDelta(line []byte) ([]adapter.StreamDelta, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	lineStr := string(line)
	if strings.Contains(lineStr, `"subtype":"init"`) {
		sessID := ""
		if strings.Contains(lineStr, `"session_id":"`) {
			parts := strings.Split(lineStr, `"session_id":"`)
			if len(parts) > 1 {
				sessID = strings.Split(parts[1], `"`)[0]
			}
		}
		return []adapter.StreamDelta{{Kind: adapter.DeltaInit, SessionID: sessID}}, nil
	}
	if strings.Contains(lineStr, `"type":"assistant"`) {
		text := ""
		if strings.Contains(lineStr, `"text":"`) {
			parts := strings.Split(lineStr, `"text":"`)
			if len(parts) > 1 {
				text = strings.Split(parts[1], `"`)[0]
			}
		}
		return []adapter.StreamDelta{{Kind: adapter.DeltaText, Text: text}}, nil
	}
	if strings.Contains(lineStr, `"type":"result"`) {
		sessID := ""
		if strings.Contains(lineStr, `"session_id":"`) {
			parts := strings.Split(lineStr, `"session_id":"`)
			if len(parts) > 1 {
				sessID = strings.Split(parts[1], `"`)[0]
			}
		}
		return []adapter.StreamDelta{{
			Kind:      adapter.DeltaResult,
			SessionID: sessID,
			Usage:     &adapter.Usage{OutputTokens: 10},
		}}, nil
	}
	return nil, nil
}

// MultiProviderMockResolver routes to provider-specific TrackingAdapters.
type MultiProviderMockResolver struct {
	claudeTracker *TrackingAdapter
	geminiTracker *TrackingAdapter
	localTracker  *TrackingAdapter
}

func (r *MultiProviderMockResolver) Resolve(prov string) adapter.ProviderAdapter {
	switch ProviderFamily(prov) {
	case "claude":
		return r.claudeTracker
	case "gemini":
		return r.geminiTracker
	case "local":
		return r.localTracker
	default:
		return r.geminiTracker
	}
}

// TestMidChatModelSwitching_Claude_Gemini_Local verifies Acceptance Criterion 1:
// One session runs Claude, then Gemini, then a local model with context intact.
func TestMidChatModelSwitching_Claude_Gemini_Local(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	store := conversation.NewStore(dbConn)

	claudeTracker := &TrackingAdapter{
		providerName: "claude",
		responseFn: func(req adapter.ExecRequest) (string, string) {
			return "Claude: Confirmed, project codename is PROJECT_PHOENIX.", "claude-session-uuid-1"
		},
	}
	geminiTracker := &TrackingAdapter{
		providerName: "gemini",
		responseFn: func(req adapter.ExecRequest) (string, string) {
			// Verify that previous turn context (PROJECT_PHOENIX) is present in rehydrated prompt
			if !strings.Contains(req.Opts.Prompt, "PROJECT_PHOENIX") {
				return "Gemini Error: Context missing!", "gemini-session-uuid-2"
			}
			return "Gemini: Understood, your project codename is PROJECT_PHOENIX.", "gemini-session-uuid-2"
		},
	}
	localTracker := &TrackingAdapter{
		providerName: "local",
		responseFn: func(req adapter.ExecRequest) (string, string) {
			// Verify that previous turns context is present in rehydrated prompt or history
			if !strings.Contains(req.Opts.Prompt, "PROJECT_PHOENIX") {
				return "Local Error: Context missing!", ""
			}
			return "Local: Fully verified, project codename remains PROJECT_PHOENIX.", ""
		},
	}

	mockResolver := &MultiProviderMockResolver{
		claudeTracker: claudeTracker,
		geminiTracker: geminiTracker,
		localTracker:  localTracker,
	}

	cfg := Config{
		Model:         "claude-3-5-sonnet",
		InProcess:     true,
		DB:            dbConn,
		Store:         store,
		CustomAdapter: claudeTracker,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newM.(*Model)

	sessID := m.session.ID

	// ==========================================
	// Turn 1: Run Claude
	// ==========================================
	turn1Input := "The secret project codename is PROJECT_PHOENIX."
	m.submitUserMessage(turn1Input)
	execCmd := m.startAdapterExecution(turn1Input)
	doneMsg := execCmd().(streamDoneMsg)
	newM, _ = m.Update(doneMsg)
	m = newM.(*Model)

	claudeTracker.mu.Lock()
	if len(claudeTracker.execCalls) != 1 {
		t.Fatalf("expected 1 Claude exec call, got %d", len(claudeTracker.execCalls))
	}
	claudeCall := claudeTracker.execCalls[0]
	claudeTracker.mu.Unlock()

	if !strings.Contains(claudeCall.Opts.Prompt, "PROJECT_PHOENIX") {
		t.Errorf("expected Claude prompt to contain user input, got: %s", claudeCall.Opts.Prompt)
	}

	// Verify provider handle captured
	if m.session.ProviderHandles["claude"] == nil || m.session.ProviderHandles["claude"].Handle != "claude-session-uuid-1" {
		t.Errorf("expected claude session handle captured, got: %+v", m.session.ProviderHandles["claude"])
	}
	if m.lastExecutedFamily != "claude" {
		t.Fatalf("expected lastExecutedFamily to be claude, got %s", m.lastExecutedFamily)
	}

	// ==========================================
	// Switch to Gemini: /model gemini-2.5-flash
	// ==========================================
	cmd := m.handleSlashCommand("/model gemini-2.5-flash")
	msg := cmd().(slashResultMsg)

	// Verify status bar warning and result message for family switch
	if !strings.Contains(m.statusMessage, "Model family switch (claude ➔ gemini)") {
		t.Errorf("expected statusMessage to warn about family switch, got: %q", m.statusMessage)
	}
	if !strings.Contains(msg.content, "Family switch (claude ➔ gemini)") {
		t.Errorf("expected slash command result to note family switch, got: %q", msg.content)
	}

	// Switch custom adapter to gemini tracker
	m.customAdapter = mockResolver.Resolve("gemini")

	// ==========================================
	// Turn 2: Run Gemini
	// ==========================================
	turn2Input := "What is my secret project codename?"
	m.submitUserMessage(turn2Input)
	execCmd = m.startAdapterExecution(turn2Input)
	doneMsg = execCmd().(streamDoneMsg)
	newM, _ = m.Update(doneMsg)
	m = newM.(*Model)

	geminiTracker.mu.Lock()
	if len(geminiTracker.execCalls) != 1 {
		t.Fatalf("expected 1 Gemini exec call, got %d", len(geminiTracker.execCalls))
	}
	geminiCall := geminiTracker.execCalls[0]
	geminiTracker.mu.Unlock()

	// Verify rehydration: Gemini was provided the Turn 1 context from SQLite chat_messages!
	if !strings.Contains(geminiCall.Opts.Prompt, "PROJECT_PHOENIX") {
		t.Errorf("expected Gemini rehydrated prompt to contain prior context PROJECT_PHOENIX, got:\n%s", geminiCall.Opts.Prompt)
	}
	if !strings.Contains(geminiCall.Opts.Prompt, "[Conversation History]") {
		t.Errorf("expected Gemini prompt to have [Conversation History] header, got:\n%s", geminiCall.Opts.Prompt)
	}
	// Verify Gemini was NOT passed Claude's session ID
	if geminiCall.Opts.ConversationID == "claude-session-uuid-1" {
		t.Errorf("cross-family switch must not pass Claude session ID to Gemini")
	}

	// Verify Gemini provider handle captured
	if m.session.ProviderHandles["gemini"] == nil || m.session.ProviderHandles["gemini"].Handle != "gemini-session-uuid-2" {
		t.Errorf("expected gemini handle captured, got: %+v", m.session.ProviderHandles["gemini"])
	}
	if m.lastExecutedFamily != "gemini" {
		t.Fatalf("expected lastExecutedFamily to be gemini, got %s", m.lastExecutedFamily)
	}

	// ==========================================
	// Switch to Local: /model local
	// ==========================================
	cmd = m.handleSlashCommand("/model local")
	msg = cmd().(slashResultMsg)

	// Verify status bar warning and context window downshift notification
	if !strings.Contains(m.statusMessage, "Model family switch (gemini ➔ local)") {
		t.Errorf("expected statusMessage to warn about family switch to local, got: %q", m.statusMessage)
	}
	if !strings.Contains(msg.content, "Downshifting context window") {
		t.Errorf("expected slash command result to mention downshifting context window, got: %q", msg.content)
	}

	// Switch custom adapter to local tracker
	m.customAdapter = mockResolver.Resolve("local")

	// ==========================================
	// Turn 3: Run Local Model
	// ==========================================
	turn3Input := "Please confirm the secret codename once more."
	m.submitUserMessage(turn3Input)
	execCmd = m.startAdapterExecution(turn3Input)
	doneMsg = execCmd().(streamDoneMsg)
	newM, _ = m.Update(doneMsg)
	m = newM.(*Model)

	localTracker.mu.Lock()
	if len(localTracker.execCalls) != 1 {
		t.Fatalf("expected 1 Local exec call, got %d", len(localTracker.execCalls))
	}
	localCall := localTracker.execCalls[0]
	localTracker.mu.Unlock()

	// Verify rehydration and context retention in Local model
	if !strings.Contains(localCall.Opts.Prompt, "PROJECT_PHOENIX") {
		t.Errorf("expected Local model prompt to contain PROJECT_PHOENIX, got:\n%s", localCall.Opts.Prompt)
	}
	if len(localCall.Opts.History) == 0 {
		t.Errorf("expected Local model to receive multi-turn structured History")
	}

	// Verify complete session in SQLite store
	conv, err := store.GetSession(context.Background(), sessID)
	if err != nil {
		t.Fatalf("failed to retrieve session from store: %v", err)
	}

	// 3 user messages + 3 assistant messages = 6 total turns
	if len(conv.Messages) != 6 {
		t.Fatalf("expected 6 messages in store, got %d", len(conv.Messages))
	}

	// Verify context intact across all three turns
	allText := ""
	for _, msg := range conv.Messages {
		allText += msg.Content + "\n"
	}
	if !strings.Contains(allText, "Claude: Confirmed") {
		t.Errorf("store missing Claude turn")
	}
	if !strings.Contains(allText, "Gemini: Understood") {
		t.Errorf("store missing Gemini turn")
	}
	if !strings.Contains(allText, "Local: Fully verified") {
		t.Errorf("store missing Local turn")
	}
}

// TestMidChatModelSwitching_ResumeAfterRestart verifies Acceptance Criterion 2:
// Resume after restart works with conversation context and provider handles intact.
func TestMidChatModelSwitching_ResumeAfterRestart(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	store := conversation.NewStore(dbConn)

	tracker := &TrackingAdapter{
		providerName: "gemini",
		responseFn: func(req adapter.ExecRequest) (string, string) {
			return "Gemini resumed answer: 42 is the answer.", "gemini-sess-resumed"
		},
	}

	// 1. Create and populate an initial session
	sessID := "persisted-session-1234"
	ctx := context.Background()
	conv := &conversation.Conversation{
		ID:        sessID,
		Title:     "Restart Test Session",
		CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
		UpdatedAt: time.Now().UTC(),
		Metadata: map[string]interface{}{
			"model":    "gemini-2.5-flash",
			"provider": "gemini",
			"family":   "gemini",
		},
	}
	if err := store.CreateSession(ctx, conv); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Add user turn
	if err := store.AppendMessage(ctx, sessID, &conversation.Message{
		ID:        "msg-1",
		SessionID: sessID,
		Role:      conversation.RoleUser,
		Content:   "What is the ultimate question of life, the universe, and everything?",
		CreatedAt: time.Now().UTC().Add(-9 * time.Minute),
	}); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Add assistant turn
	if err := store.AppendMessage(ctx, sessID, &conversation.Message{
		ID:        "msg-2",
		SessionID: sessID,
		Role:      conversation.RoleAssistant,
		Content:   "The answer is 42.",
		CreatedAt: time.Now().UTC().Add(-8 * time.Minute),
		Metadata: map[string]interface{}{
			"model":    "gemini-2.5-flash",
			"provider": "gemini",
		},
	}); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Save provider handle
	handle := &conversation.ProviderHandle{
		SessionID: sessID,
		Provider:  "gemini",
		Handle:    "gemini-persisted-handle-99",
		Model:     "gemini-2.5-flash",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SetProviderHandle(ctx, handle); err != nil {
		t.Fatalf("failed to set provider handle: %v", err)
	}

	// 2. Simulate restarting the process by launching a brand new Model with SessionID
	cfg := Config{
		SessionID:     sessID,
		InProcess:     true,
		DB:            dbConn,
		Store:         store,
		CustomAdapter: tracker,
	}

	resumedModel, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to resume model from session ID: %v", err)
	}

	newM, _ := resumedModel.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	resumedModel = newM.(*Model)

	// Verify messages were restored
	if len(resumedModel.messages) != 2 {
		t.Fatalf("expected 2 messages loaded on resume, got %d", len(resumedModel.messages))
	}
	if resumedModel.messages[0].Content != "What is the ultimate question of life, the universe, and everything?" {
		t.Errorf("unexpected first message: %s", resumedModel.messages[0].Content)
	}

	// Verify provider handle was loaded
	if resumedModel.session.ProviderHandles["gemini"] == nil || resumedModel.session.ProviderHandles["gemini"].Handle != "gemini-persisted-handle-99" {
		t.Errorf("expected gemini handle to be restored, got: %+v", resumedModel.session.ProviderHandles)
	}

	// Verify status message reflects resume
	if !strings.Contains(resumedModel.statusMessage, "Resumed session") {
		t.Errorf("expected status message to indicate session resume, got: %s", resumedModel.statusMessage)
	}

	// 3. Send a follow-up turn in the resumed session
	followUp := "Why 42?"
	resumedModel.submitUserMessage(followUp)
	execCmd := resumedModel.startAdapterExecution(followUp)
	doneMsg := execCmd().(streamDoneMsg)
	newM, _ = resumedModel.Update(doneMsg)
	resumedModel = newM.(*Model)

	tracker.mu.Lock()
	if len(tracker.execCalls) != 1 {
		t.Fatalf("expected 1 exec call after resume, got %d", len(tracker.execCalls))
	}
	call := tracker.execCalls[0]
	tracker.mu.Unlock()

	// Since same provider family and native handle exists: native resume should be used!
	if call.Opts.ConversationID != "gemini-persisted-handle-99" {
		t.Errorf("expected native handle gemini-persisted-handle-99 to be passed, got: %s", call.Opts.ConversationID)
	}

	// Verify 4 total messages in SQLite
	updatedConv, err := store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("failed to get updated session: %v", err)
	}
	if len(updatedConv.Messages) != 4 {
		t.Fatalf("expected 4 messages in SQLite after follow-up turn, got %d", len(updatedConv.Messages))
	}
}

// TestMidChatModelSwitching_SameFamilyNativeResume verifies that within the same provider family,
// the native session flag is used rather than re-rehydrating all prior history.
func TestMidChatModelSwitching_SameFamilyNativeResume(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	store := conversation.NewStore(dbConn)

	claudeTracker := &TrackingAdapter{
		providerName: "claude",
		responseFn: func(req adapter.ExecRequest) (string, string) {
			return "Claude reply", "claude-native-sess-abc"
		},
	}

	cfg := Config{
		Model:         "claude-3-5-sonnet",
		InProcess:     true,
		DB:            dbConn,
		Store:         store,
		CustomAdapter: claudeTracker,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newM.(*Model)

	// Turn 1
	turn1 := "Turn 1 prompt"
	m.submitUserMessage(turn1)
	execCmd := m.startAdapterExecution(turn1)
	doneMsg := execCmd().(streamDoneMsg)
	newM, _ = m.Update(doneMsg)
	m = newM.(*Model)

	// Switch to another model in the SAME family: claude-3-7-sonnet
	cmd := m.handleSlashCommand("/model claude-3-7-sonnet")
	msg := cmd().(slashResultMsg)

	// Status message confirms same family native resume
	if !strings.Contains(msg.content, "Same family (claude)") {
		t.Errorf("expected same family message, got: %s", msg.content)
	}
	if !strings.Contains(m.statusMessage, "Retained claude provider family") {
		t.Errorf("expected retained claude family status, got: %s", m.statusMessage)
	}

	// Turn 2
	turn2 := "Turn 2 prompt"
	m.submitUserMessage(turn2)
	execCmd = m.startAdapterExecution(turn2)
	doneMsg = execCmd().(streamDoneMsg)
	newM, _ = m.Update(doneMsg)
	m = newM.(*Model)

	claudeTracker.mu.Lock()
	if len(claudeTracker.execCalls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(claudeTracker.execCalls))
	}
	call2 := claudeTracker.execCalls[1]
	claudeTracker.mu.Unlock()

	// Verify native resume handle passed!
	if call2.Opts.ConversationID != "claude-native-sess-abc" {
		t.Errorf("expected native conversation ID claude-native-sess-abc, got: %s", call2.Opts.ConversationID)
	}
	// Verify prompt was NOT re-rehydrated with full history, since native resume was used
	if strings.Contains(call2.Opts.Prompt, "[Conversation History]") {
		t.Errorf("native resume in same family should not prepend full [Conversation History]")
	}
}

// TestMidChatModelSwitching_CondenserOnDownshift verifies that internal/condenser
// is used when downshifting to a smaller context window.
func TestMidChatModelSwitching_CondenserOnDownshift(t *testing.T) {
	// Verify IsDownshift helper
	if !IsDownshift("gemini-2.5-flash", "local") {
		t.Errorf("expected Gemini -> Local to be a downshift")
	}
	if !IsDownshift("gemini-2.5-flash", "claude-3-5-sonnet") {
		t.Errorf("expected Gemini -> Claude to be a downshift")
	}
	if IsDownshift("local", "gemini-2.5-flash") {
		t.Errorf("expected Local -> Gemini to NOT be a downshift")
	}
	if IsDownshift("claude-3-5-sonnet", "claude-3-7-sonnet") {
		t.Errorf("expected Claude -> Claude to NOT be a downshift")
	}

	// Create messages with verbose repetitive logs to test condenser effect
	longLog := strings.Repeat("2026-09-29 ERROR main.go:42 failed connection\n", 50)
	rawMessages := []ChatMessage{
		{Role: RoleUser, Content: "Here are the server error logs:"},
		{Role: RoleAssistant, Content: longLog},
	}

	condensed := CondenseMessagesForDownshift(rawMessages, 8192)
	if len(condensed) != 2 {
		t.Fatalf("expected 2 condensed messages, got %d", len(condensed))
	}

	origLen := len(rawMessages[1].Content)
	condensedLen := len(condensed[1].Content)

	if condensedLen >= origLen {
		t.Errorf("expected condenser to reduce size, orig=%d condensed=%d", origLen, condensedLen)
	}
}
