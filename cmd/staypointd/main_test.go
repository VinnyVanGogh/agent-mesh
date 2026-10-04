package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
	"github.com/VinnyVanGogh/staypoint/internal/server"
)

// openTestStore opens a real SQLite store in a temp dir with the full schema.
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// stubWM implements WorktreeManagerIface using a pre-existing directory so
// tests do not require a real git repository.
type stubWM struct{ dir string }

func (s *stubWM) CreateContext(_ context.Context, _, _ string) (string, error) {
	return s.dir, nil
}
func (s *stubWM) PruneContext(_ context.Context, _ string) error            { return nil }
func (s *stubWM) PruneWorktreeDirContext(_ context.Context, _ string) error { return nil }

func TestWireOnWake_SetsOnWake(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()
	if orchestrator.GlobalDispatcher.OnWake != nil {
		t.Fatal("pre-condition: OnWake should be nil before wiring")
	}
	wireOnWake(openTestStore(t), t.TempDir(), nil, nil)
	if orchestrator.GlobalDispatcher.OnWake == nil {
		t.Fatal("wireOnWake did not set GlobalDispatcher.OnWake")
	}
}

func TestWireOnWake_AdapterCalledOnWake(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()

	store := openTestStore(t)
	dir := t.TempDir()

	taskID := "wire-wake-test-abc1"
	if _, err := store.DB().Exec(
		`INSERT INTO tasks (id, name, repo_path, execution_stage, assignee_agent_id) VALUES (?, 'test task', '', 'todo', 'agent-test')`,
		taskID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	var adapterCalls atomic.Int32
	stub := func(_ context.Context, _ string, _ string, _ []string, _ []string, stdout, _ io.Writer) error {
		adapterCalls.Add(1)
		// Signal completion so the harness exits after one turn.
		fmt.Fprintln(stdout, "[[TASK_COMPLETE]]")
		return nil
	}

	wireOnWake(store, dir, nil, stub, &stubWM{dir: dir})

	orchestrator.GlobalDispatcher.Wake(taskID, "test_wake", "test:"+taskID)

	// Drain waits for the goroutine spawned by Wake to return.
	drainDone := make(chan struct{})
	go func() { orchestrator.GlobalDispatcher.Drain(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(15 * time.Second):
		t.Fatal("dispatcher did not drain within timeout")
	}

	if adapterCalls.Load() == 0 {
		t.Fatal("stub adapter was never called; harness did not run")
	}

	var stage string
	if err := store.DB().QueryRow(
		"SELECT execution_stage FROM tasks WHERE id=?", taskID,
	).Scan(&stage); err != nil {
		t.Fatalf("query task stage: %v", err)
	}
	if stage == "todo" {
		t.Errorf("task execution_stage still 'todo' after wake; harness did not claim it")
	}
}

func TestWireOnWake_MissingTaskSkipsRun(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()
	store := openTestStore(t)
	dir := t.TempDir()

	var adapterCalls atomic.Int32
	stub := func(_ context.Context, _ string, _ string, _ []string, _ []string, _ io.Writer, _ io.Writer) error {
		adapterCalls.Add(1)
		return nil
	}

	wireOnWake(store, dir, nil, stub, &stubWM{dir: dir})

	// Wake a task that does not exist in the DB.
	orchestrator.GlobalDispatcher.Wake("nonexistent-task-id", "test", "test:nonexistent")

	drainDone := make(chan struct{})
	go func() { orchestrator.GlobalDispatcher.Drain(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not drain")
	}

	if adapterCalls.Load() != 0 {
		t.Error("adapter should not be called for a missing task")
	}
}

// TestWireOnWake_ParseDeltaUsesClaudeAdapter asserts that parseDelta uses the
// Claude adapter (not the Agy/Gemini fallback) so tool steps from a Claude
// stream-json transcript are recorded in run_steps.
//
// Regression guard for: parseDelta called adapter.AdapterFor("") which
// returned AgyAdapter{} and silently dropped all Claude stream events.
func TestWireOnWake_ParseDeltaUsesClaudeAdapter(t *testing.T) {
	orchestrator.GlobalDispatcher = orchestrator.NewDispatcher()

	store := openTestStore(t)
	dir := t.TempDir()

	taskID := "wire-wake-claude-parse-01"
	if _, err := store.DB().Exec(
		`INSERT INTO tasks (id, name, repo_path, execution_stage, assignee_agent_id) VALUES (?, 'claude parse test', '', 'todo', 'agent-claude')`,
		taskID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	// Read the captured Claude stream-json fixture.  The fixture contains a
	// Bash tool_use + tool_result pair which must produce at least one non-wake
	// step when parsed by ClaudeAdapter.
	fixturePath := filepath.Join("..", "..", "internal", "adapter", "testdata", "claude_stream.ndjson")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read claude fixture: %v", err)
	}

	stub := func(_ context.Context, _ string, prov string, _ []string, _ []string, stdout io.Writer, _ io.Writer) error {
		// Emit the captured Claude stream-json lines so parseDelta can process them.
		if _, werr := stdout.Write(fixtureBytes); werr != nil {
			return werr
		}
		// Signal harness completion.
		fmt.Fprintln(stdout, "[[TASK_COMPLETE]]")
		return nil
	}

	wireOnWake(store, dir, nil, stub, &stubWM{dir: dir})
	orchestrator.GlobalDispatcher.Wake(taskID, "test_claude_parse", "test:"+taskID)

	drainDone := make(chan struct{})
	go func() { orchestrator.GlobalDispatcher.Drain(); close(drainDone) }()
	select {
	case <-drainDone:
	case <-time.After(15 * time.Second):
		t.Fatal("dispatcher did not drain within timeout")
	}

	// Query run_steps for steps that are not wake/state/checkpoint bookkeeping.
	// With the correct ClaudeAdapter, the tool_use line in the fixture must produce
	// at least one such step (kind = 'run', 'read', 'edit', or similar tool step).
	rows, err := store.DB().QueryContext(context.Background(),
		`SELECT kind FROM run_steps WHERE task_id = ? AND kind NOT IN ('wake','state','checkpoint')`,
		taskID,
	)
	if err != nil {
		t.Fatalf("query run_steps: %v", err)
	}
	defer rows.Close()

	var toolSteps []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		toolSteps = append(toolSteps, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	if len(toolSteps) == 0 {
		t.Error("no tool/text steps recorded in run_steps; ClaudeAdapter stream parsing is broken (AdapterFor may be using wrong provider)")
	}
}

// TestStaypointd_BoardToken_PersistedAndUsable is an E2E test on the staypointd
// options path (not just the apitest server). It verifies:
//  1. When BoardTokenPath is set (as it now is in runDaemon), the board_token
//     file is written to DataDir at server startup.
//  2. A Board session bootstrapped with the persisted board_token can call a
//     WrapBoardAction-protected endpoint and receives 200 (not 403).
//  3. An agent-only request (no staypoint_board cookie) to the same endpoint
//     receives 403, confirming agents cannot self-approve Board actions.
func TestStaypointd_BoardToken_PersistedAndUsable(t *testing.T) {
	dataDir := t.TempDir()
	boardTokenPath := filepath.Join(dataDir, "board_token")

	store := openTestStore(t)

	// Mirror the server.Options now used by runDaemon.
	srv, err := server.New(server.Options{
		BindHost:       "127.0.0.1",
		Port:           0, // ephemeral
		TokenPath:      filepath.Join(dataDir, "auth_token"),
		BoardTokenPath: boardTokenPath,
		DB:             store.DB(),
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("srv.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	// 1. board_token file must have been written to DataDir.
	data, err := os.ReadFile(boardTokenPath)
	if err != nil {
		t.Fatalf("board_token file not created at %s: %v", boardTokenPath, err)
	}
	boardToken := strings.TrimSpace(string(data))
	if len(boardToken) < 16 {
		t.Fatalf("board_token too short: %q", boardToken)
	}
	if boardToken != srv.BoardToken() {
		t.Errorf("persisted board_token %q != srv.BoardToken() %q", boardToken, srv.BoardToken())
	}

	authToken := srv.Token()
	base := srv.URL()

	// Bootstrap a board session using the persisted board_token.
	// The middleware sets the staypoint_board cookie on the /?token=…&board_token=… redirect.
	jar := newTestCookieJar()
	client := &http.Client{
		Jar: jar,
		// Follow redirects so the cookie is captured.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}
	bootstrapURL := fmt.Sprintf("%s/?token=%s&board_token=%s", base, authToken, boardToken)
	resp, err := client.Get(bootstrapURL)
	if err != nil {
		t.Fatalf("bootstrap GET: %v", err)
	}
	resp.Body.Close()

	// Confirm staypoint_board cookie was set.
	var gotBoardCookie bool
	for _, c := range jar.cookies {
		if c.Name == "staypoint_board" {
			gotBoardCookie = true
			break
		}
	}
	if !gotBoardCookie {
		t.Fatal("staypoint_board cookie was not set after bootstrap with valid board_token")
	}

	// 2. Board session → POST /api/settings/security-gate must return something other than 403.
	// (It may return 400/422 due to missing body, but not 403 — the board gate is passed.)
	req, _ := http.NewRequest("POST", base+"/api/settings/security-gate", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range jar.cookies {
		req.AddCookie(c)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("board-session POST: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusForbidden {
		t.Errorf("board session got 403 Forbidden on /api/settings/security-gate; board cookie gate is broken")
	}

	// 3. Agent-only (no board cookie) → must get 403.
	req3, _ := http.NewRequest("POST", base+"/api/settings/security-gate", strings.NewReader(`{}`))
	req3.Header.Set("Authorization", "Bearer "+authToken)
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("agent-only POST: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("agent-only request expected 403, got %d", resp3.StatusCode)
	}
}

// testCookieJar is a minimal http.CookieJar for tests.
type testCookieJar struct{ cookies []*http.Cookie }

func newTestCookieJar() *testCookieJar { return &testCookieJar{} }

func (j *testCookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	j.cookies = append(j.cookies, cookies...)
}
func (j *testCookieJar) Cookies(_ *url.URL) []*http.Cookie { return j.cookies }
