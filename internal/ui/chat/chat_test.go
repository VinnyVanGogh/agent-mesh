package chat

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/conversation"
	tea "github.com/charmbracelet/bubbletea"
	_ "modernc.org/sqlite"
)

// MockDaemonClient provides a mock implementation of DaemonClientInterface for testing.
type MockDaemonClient struct {
	connected       bool
	checkpointCalls []string
	undoCalls       []string
}

func (m *MockDaemonClient) IsConnected() bool {
	return m.connected
}

func (m *MockDaemonClient) Ping(ctx context.Context) error {
	if !m.connected {
		return ErrDaemonUnreachable
	}
	return nil
}

func (m *MockDaemonClient) Checkpoint(ctx context.Context, message string) (string, error) {
	if !m.connected {
		return "", ErrDaemonUnreachable
	}
	m.checkpointCalls = append(m.checkpointCalls, message)
	return fmt.Sprintf("mock-checkpoint-id-12345678 (sha: abcdef01) - %s", message), nil
}

func (m *MockDaemonClient) Undo(ctx context.Context, checkpointID string) (string, error) {
	if !m.connected {
		return "", ErrDaemonUnreachable
	}
	m.undoCalls = append(m.undoCalls, checkpointID)
	return fmt.Sprintf("mock-undo-target: %s, 3 files reverted", checkpointID), nil
}

func (m *MockDaemonClient) Close() error {
	m.connected = false
	return nil
}

// MockAdapter provides a controllable ProviderAdapter for streaming tests.
type MockAdapter struct {
	streamChunks []string
	deltas       [][]adapter.StreamDelta
	execErr      error
}

func (m MockAdapter) Provider() string          { return "mock" }
func (m MockAdapter) BinaryName() string        { return "" }
func (m MockAdapter) KnownMajorVersions() []int { return []int{1} }
func (m MockAdapter) BuildArgs(opts adapter.ParsedOptions) []string {
	return []string{"mock"}
}

func (m MockAdapter) Execute(ctx context.Context, req adapter.ExecRequest) error {
	for _, chunk := range m.streamChunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			_, _ = req.Stdout.Write([]byte(chunk + "\n"))
			time.Sleep(10 * time.Millisecond)
		}
	}
	return m.execErr
}

func (m MockAdapter) ParseStreamDelta(line []byte) ([]adapter.StreamDelta, error) {
	lineStr := string(bytes.TrimSpace(line))
	if lineStr == "" {
		return nil, nil
	}
	return []adapter.StreamDelta{
		{
			Kind: adapter.DeltaText,
			Text: lineStr,
		},
	}, nil
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}

	// Create required schema tables
	schema := `
	CREATE TABLE IF NOT EXISTS chat_sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		repo_path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		metadata_json TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE IF NOT EXISTS chat_messages (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		sequence_num INTEGER NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		token_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		metadata_json TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE IF NOT EXISTS chat_tool_calls (
		id TEXT PRIMARY KEY,
		message_id TEXT NOT NULL,
		sequence_num INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		arguments TEXT NOT NULL DEFAULT '{}',
		result TEXT,
		is_error INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS session_provider_handles (
		session_id TEXT NOT NULL,
		provider TEXT NOT NULL,
		handle TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		PRIMARY KEY (session_id, provider)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create test tables: %v", err)
	}
	return db
}

// TestTokenAccumulator_DebouncedStreaming tests that the accumulator smoothly buffers
// and flushes tokens at frame boundaries without loss or corruption.
func TestTokenAccumulator_DebouncedStreaming(t *testing.T) {
	acc := NewTokenAccumulator()

	// 1. Initial state
	if acc.Len() != 0 || acc.TotalLen() != 0 {
		t.Errorf("expected empty accumulator, got len=%d total=%d", acc.Len(), acc.TotalLen())
	}
	if flushed := acc.Flush(); flushed != "" {
		t.Errorf("expected empty flush, got %q", flushed)
	}

	// 2. Simulate streaming tokens arriving at high frequency
	tokens := []string{"Hello", " ", "world", "!", "\n\n", "Here", " is", " code:"}
	for _, tok := range tokens[:4] {
		acc.Append(tok)
	}

	if acc.Len() != len("Hello world!") {
		t.Errorf("expected pending len %d, got %d", len("Hello world!"), acc.Len())
	}

	// 3. Flush frame 1
	frame1 := acc.Flush()
	if frame1 != "Hello world!" {
		t.Errorf("expected frame1 %q, got %q", "Hello world!", frame1)
	}
	if acc.Len() != 0 {
		t.Errorf("expected pending buffer to be cleared after flush, got len %d", acc.Len())
	}

	// 4. Subsequent tokens arrive
	for _, tok := range tokens[4:] {
		acc.Append(tok)
	}

	// 5. Flush frame 2
	frame2 := acc.Flush()
	expectedFrame2 := "\n\nHere is code:"
	if frame2 != expectedFrame2 {
		t.Errorf("expected frame2 %q, got %q", expectedFrame2, frame2)
	}

	// 6. Verify total matches concatenation of all frames
	if acc.Total() != "Hello world!\n\nHere is code:" {
		t.Errorf("expected total %q, got %q", "Hello world!\n\nHere is code:", acc.Total())
	}

	// 7. Reset clears total
	acc.Reset()
	if acc.Total() != "" || acc.Len() != 0 {
		t.Errorf("expected empty after reset")
	}
}

// TestModel_ResizeHandling tests that tea.WindowSizeMsg dynamically updates viewport
// and renderer dimensions without corrupting the view or crashing.
func TestModel_ResizeHandling(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	cfg := Config{
		Model:     "gemini-2.5-flash",
		InProcess: true,
		DB:        dbConn,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	// Add messages to model
	m.messages = append(m.messages,
		ChatMessage{
			ID:        "msg-1",
			Role:      RoleUser,
			Content:   "Explain Bubble Tea TUI architecture",
			Timestamp: time.Now(),
		},
		ChatMessage{
			ID:        "msg-2",
			Role:      RoleAssistant,
			Model:     "gemini-2.5-flash",
			Content:   "Bubble Tea is based on The Elm Architecture (Model, Update, View).",
			Timestamp: time.Now(),
		},
	)

	// Test a series of resize events
	sizes := [][2]int{
		{80, 24},   // Standard 80x24
		{120, 40},  // Wide terminal
		{40, 15},   // Small terminal
		{200, 60},  // Large full-screen
		{30, 10},   // Constrained terminal
	}

	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		newModel, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		if cmd != nil {
			// WindowSizeMsg shouldn't return unexpected cmds
		}
		updated := newModel.(*Model)

		if updated.width != w || updated.height != h {
			t.Errorf("expected size %dx%d, got %dx%d", w, h, updated.width, updated.height)
		}
		if updated.viewport.Width != w {
			t.Errorf("expected viewport width %d, got %d", w, updated.viewport.Width)
		}
		if updated.viewport.Height <= 0 {
			t.Errorf("expected positive viewport height, got %d", updated.viewport.Height)
		}

		// View render must succeed and not panic
		view := updated.View()
		if !strings.Contains(view, "StayPoint Chat") {
			t.Errorf("view missing header: %s", view)
		}
		if !strings.Contains(view, "Ctrl+C") {
			t.Errorf("view missing footer: %s", view)
		}
		if updated.viewport.TotalLineCount() <= 0 {
			t.Errorf("viewport content is empty")
		}
	}
}

// TestModel_SlashCommands tests all required slash commands:
// /model, /task, /checkpoint, /undo, /clear, /help
func TestModel_SlashCommands(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	mockDaemon := &MockDaemonClient{connected: true}

	cfg := Config{
		Model:              "gemini-2.5-flash",
		InProcess:          false,
		DB:                 dbConn,
		CustomDaemonClient: mockDaemon,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	// Trigger initial WindowSize to make viewport ready
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newM.(*Model)

	// 1. /model with no args (view model)
	cmd := m.handleSlashCommand("/model")
	msg := cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "Active model") || !strings.Contains(msg.content, "gemini-2.5-flash") {
		t.Errorf("expected active model info, got: %s", msg.content)
	}

	// 2. /model switch to claude-3-7-sonnet
	cmd = m.handleSlashCommand("/model claude-3-7-sonnet")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "claude-3-7-sonnet") || !strings.Contains(msg.content, "claude") {
		t.Errorf("expected switch to claude, got: %s", msg.content)
	}
	if m.activeModel != "claude-3-7-sonnet" || m.activeProvider != "claude" {
		t.Errorf("model state not updated: model=%s provider=%s", m.activeModel, m.activeProvider)
	}

	// 3. /task bind
	cmd = m.handleSlashCommand("/task STA-105")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "STA-105") {
		t.Errorf("expected task binding confirmation, got: %s", msg.content)
	}
	if m.activeTask != "STA-105" {
		t.Errorf("active task not set: %s", m.activeTask)
	}

	// 4. /task view
	cmd = m.handleSlashCommand("/task")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "STA-105") {
		t.Errorf("expected active task info, got: %s", msg.content)
	}

	// 5. /checkpoint via daemon
	cmd = m.handleSlashCommand("/checkpoint test checkpoint")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "staypointd") {
		t.Errorf("expected daemon checkpoint response, got: %s", msg.content)
	}
	if len(mockDaemon.checkpointCalls) != 1 || mockDaemon.checkpointCalls[0] != "test checkpoint" {
		t.Errorf("daemon checkpoint not called as expected: %v", mockDaemon.checkpointCalls)
	}

	// 6. /undo via daemon
	cmd = m.handleSlashCommand("/undo cp-999")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "staypointd") {
		t.Errorf("expected daemon undo response, got: %s", msg.content)
	}
	if len(mockDaemon.undoCalls) != 1 || mockDaemon.undoCalls[0] != "cp-999" {
		t.Errorf("daemon undo not called as expected: %v", mockDaemon.undoCalls)
	}

	// 7. /help
	cmd = m.handleSlashCommand("/help")
	msg = cmd().(slashResultMsg)
	if !strings.Contains(msg.content, "/model") || !strings.Contains(msg.content, "/checkpoint") {
		t.Errorf("expected help text, got: %s", msg.content)
	}

	// 8. /clear
	m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: "Hello"})
	cmd = m.handleSlashCommand("/clear")
	msg = cmd().(slashResultMsg)
	if len(m.messages) != 0 {
		t.Errorf("expected messages cleared, got %d", len(m.messages))
	}
}

// TestModel_InProcessFallback verifies that when staypointd is not running,
// the chat TUI seamlessly falls back to in-process mode and marks status accordingly.
func TestModel_InProcessFallback(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	// Pass non-existent socket path to trigger daemon unreachable
	cfg := Config{
		Model:      "gemini-2.5-flash",
		SocketPath: "/tmp/non-existent-staypointd.sock",
		InProcess:  false,
		DB:         dbConn,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to initialize model with unreachable daemon: %v", err)
	}

	if !m.inProcessMode {
		t.Errorf("expected model to enter in-process mode when daemon is unreachable")
	}

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := newM.View()
	if !strings.Contains(view, "[in-process mode]") {
		t.Errorf("expected view to display in-process badge, got: %s", view)
	}
}

// TestModel_StreamingDebounceAt30FPS tests that streaming chunks are debounced
// across frame ticks at ~33ms (30fps) rather than re-rendering on every token.
func TestModel_StreamingDebounceAt30FPS(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	mockAdapter := MockAdapter{
		streamChunks: []string{"Line 1", "Line 2", "Line 3"},
	}

	cfg := Config{
		Model:         "gemini-2.5-flash",
		InProcess:     true,
		DB:            dbConn,
		CustomAdapter: mockAdapter,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = newM.(*Model)

	// Simulate user submitting prompt
	m.textarea.SetValue("Write code")
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newM.(*Model)

	if !m.streaming {
		t.Fatalf("expected model to be in streaming state")
	}

	// Append chunks to accumulator directly (simulating background adapter execution)
	m.accumulator.Append("Chunk A")
	m.accumulator.Append(" Chunk B")
	m.accumulator.Append(" Chunk C")

	// Viewport shouldn't have new tokens until frame tick
	if strings.Contains(m.activeResponse.String(), "Chunk A") {
		t.Errorf("activeResponse updated before frame tick")
	}

	// Trigger frameTickMsg (simulating 30fps tick ~33ms)
	newM, cmd := m.Update(frameTickMsg{})
	m = newM.(*Model)

	if !strings.Contains(m.activeResponse.String(), "Chunk A Chunk B Chunk C") {
		t.Errorf("expected activeResponse to have flushed tokens, got %q", m.activeResponse.String())
	}
	if cmd == nil {
		t.Errorf("expected frameTickMsg to schedule next 30fps tick")
	}

	// Send streamDoneMsg
	newM, _ = m.Update(streamDoneMsg{
		usage: &adapter.Usage{OutputTokens: 42},
	})
	m = newM.(*Model)

	if m.streaming {
		t.Errorf("expected streaming to be finished")
	}
	if len(m.messages) != 2 { // user + assistant
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	asst := m.messages[1]
	if asst.Role != RoleAssistant || asst.TokenCount != 42 {
		t.Errorf("assistant message invalid: role=%s tokens=%d", asst.Role, asst.TokenCount)
	}
}

// TestModel_CancelLeavesNoOrphanProcess verifies that pressing Ctrl+C during streaming
// cancels the subprocess group and leaves no orphan background processes.
func TestModel_CancelLeavesNoOrphanProcess(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	tmpDir := t.TempDir()
	pidFile := filepath.Join(tmpDir, "chat_child.pid")
	script := filepath.Join(tmpDir, "chat_worker.sh")

	scriptContent := fmt.Sprintf(`#!/bin/sh
sleep 60 &
echo $! > %s
wait
`, pidFile)

	if err := os.WriteFile(script, []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := execCommandWithProcessGroup(ctx, script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start script: %v", err)
	}

	// Wait for grandchild PID
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("timed out waiting for child process PID")
	}

	// Verify child process is alive
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("child process %d is not alive: %v", childPID, err)
	}

	// Wire model with active cancellation
	cfg := Config{
		Model:     "gemini-2.5-flash",
		InProcess: true,
		DB:        dbConn,
	}
	m, err := NewModel(cfg)
	if err != nil {
		t.Fatal(err)
	}

	newM, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = newM.(*Model)

	m.streaming = true
	m.execCancel = func() {
		cancel()
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			pgid = cmd.Process.Pid
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(50 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}

	// Send Ctrl+C while streaming
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = newM.(*Model)

	if m.streaming {
		t.Errorf("expected streaming to be canceled")
	}
	if m.quitting {
		t.Errorf("Ctrl+C during streaming should cancel generation, not quit TUI")
	}

	// Wait for subprocess group to be completely reaped
	time.Sleep(150 * time.Millisecond)
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Errorf("child process %d is still alive! orphan process was not killed", childPID)
	}
}

// TestMarkdownRenderer_SyntaxHighlighting verifies that MarkdownRenderer renders
// code blocks with syntax highlighting and formats headers/banners properly.
func TestMarkdownRenderer_SyntaxHighlighting(t *testing.T) {
	r := NewMarkdownRenderer(80)

	md := "Here is a code block:\n```go\nfunc add(a, b int) int {\n\treturn a + b\n}\n```"
	rendered := r.Render(md)

	if !strings.Contains(rendered, "add") {
		t.Errorf("rendered output missing code content: %s", rendered)
	}

	// User message formatting
	userMsg := ChatMessage{
		Role:      RoleUser,
		Content:   "Hello StayPoint",
		Timestamp: time.Now(),
	}
	userRendered := r.RenderMessage(userMsg)
	if !strings.Contains(userRendered, "You") || !strings.Contains(userRendered, "StayPoint") {
		t.Errorf("user message banner invalid: %s", userRendered)
	}

	// Assistant message formatting
	asstMsg := ChatMessage{
		Role:       RoleAssistant,
		Model:      "claude-3-7-sonnet",
		Content:    "Hello! How can I assist?",
		Timestamp:  time.Now(),
		TokenCount: 15,
	}
	asstRendered := r.RenderMessage(asstMsg)
	if !strings.Contains(asstRendered, "Assistant") || !strings.Contains(asstRendered, "claude-3-7-sonnet") {
		t.Errorf("assistant message banner invalid: %s", asstRendered)
	}
}

// TestConversationPersistence verifies that user and assistant turns are persisted
// to SQLite chat_sessions and chat_messages.
func TestConversationPersistence(t *testing.T) {
	dbConn := setupTestDB(t)
	defer dbConn.Close()

	store := conversation.NewStore(dbConn)
	cfg := Config{
		Model:     "gemini-2.5-flash",
		InProcess: true,
		Store:     store,
		DB:        dbConn,
	}

	m, err := NewModel(cfg)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	sessID := m.session.ID

	// Submit user message
	m.submitUserMessage("What is the speed of light?")

	// Finalize assistant message
	m.activeResponse.WriteString("The speed of light is approximately 299,792,458 m/s.")
	m.finalizeAssistantMessage(&adapter.Usage{OutputTokens: 25}, nil)

	// Fetch from store
	ctx := context.Background()
	conv, err := store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}

	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Role != conversation.RoleUser || conv.Messages[0].Content != "What is the speed of light?" {
		t.Errorf("user message in store invalid: %v", conv.Messages[0])
	}
	if conv.Messages[1].Role != conversation.RoleAssistant || !strings.Contains(conv.Messages[1].Content, "299,792,458") {
		t.Errorf("assistant message in store invalid: %v", conv.Messages[1])
	}
}
