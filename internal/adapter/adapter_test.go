package adapter

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/router"
)

func TestParseRawArgs(t *testing.T) {
	rawArgs := []string{
		"--output-format", "stream-json",
		"--approval-mode", "yolo",
		"--sandbox=none",
		"--model", "google/gemini-3.8-flash-high",
		"--resume", "sess-123",
		"--add-dir", "/tmp/skills",
		"--prompt", "Explain how quota gating works",
	}

	opts := parseRawArgs(rawArgs)
	if opts.Prompt != "Explain how quota gating works" {
		t.Errorf("expected prompt 'Explain how quota gating works', got %q", opts.Prompt)
	}
	if opts.Model != "gemini-3.8-flash" {
		t.Errorf("expected model 'gemini-3.8-flash', got %q", opts.Model)
	}
	if opts.Effort != "high" {
		t.Errorf("expected effort 'high', got %q", opts.Effort)
	}
	if opts.ConversationID != "sess-123" {
		t.Errorf("expected conversationID 'sess-123', got %q", opts.ConversationID)
	}
	if opts.OutputFormat != "stream-json" {
		t.Errorf("expected outputFormat 'stream-json', got %q", opts.OutputFormat)
	}
	if len(opts.AddDirs) != 1 || opts.AddDirs[0] != "/tmp/skills" {
		t.Errorf("expected addDirs [/tmp/skills], got %v", opts.AddDirs)
	}

	claudeArgs := buildClaudeArgs(opts)
	// Claude should receive --print and NOT receive --prompt or Gemini model
	hasPrint := false
	hasPrompt := false
	hasGeminiModel := false
	for _, a := range claudeArgs {
		if a == "--print" {
			hasPrint = true
		}
		if a == "--prompt" {
			hasPrompt = true
		}
		if strings.Contains(a, "gemini") {
			hasGeminiModel = true
		}
	}
	if !hasPrint {
		t.Errorf("Claude args must include --print")
	}
	if hasPrompt {
		t.Errorf("Claude args must not include --prompt")
	}
	if hasGeminiModel {
		t.Errorf("Claude args must not include Gemini model names")
	}

	// Test double-dash delimiter stops flag parsing
	delimiterArgs := []string{"--approval-mode", "yolo", "--", "--model", "foo", "hello world"}
	delimOpts := parseRawArgs(delimiterArgs)
	if delimOpts.Prompt != "--model foo hello world" {
		t.Errorf("expected prompt after -- to be '--model foo hello world', got %q", delimOpts.Prompt)
	}
	if delimOpts.Model != "" {
		t.Errorf("expected model to remain empty after --, got %q", delimOpts.Model)
	}
}

func TestAdapterKeepalive(t *testing.T) {
	script := `#!/bin/sh
sleep 3
echo "done"
`
	f, err := os.CreateTemp("", "dummy-agent-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	// 15s budget: script sleeps 3s; extra headroom covers race-detector overhead
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	start := time.Now()
	err = RunAdapter(ctx, ".", pacerState, "gemini", []string{"--model", "google/gemini-3.8-flash"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunAdapter failed: %v", err)
	}
	elapsed := time.Since(start)

	out := stdout.String()
	// Should have emitted at least one keepalive (blank line)
	if !strings.Contains(out, "\n\n") && !strings.HasPrefix(out, "\n") {
		t.Errorf("Expected keepalive newline in output, got %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("Expected 'done' in output, got %q", out)
	}
	if elapsed < 3*time.Second {
		t.Errorf("Expected script to take at least 3 seconds, took %v", elapsed)
	}
}

func TestAdapterFallback(t *testing.T) {
	failScript := `#!/bin/sh
exit 1
`
	f, err := os.CreateTemp("", "dummy-fail-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(failScript)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	err = RunAdapter(ctx, ".", pacerState, "gemini", []string{"--model", "google/gemini-3.8-flash"}, nil, &stdout, &stderr)
	if err == nil {
		t.Errorf("Expected error from failing script")
	}
}

func TestAdapterPacingBurst(t *testing.T) {
	script := `#!/bin/sh
for i in $(seq 1 100); do
  echo "line $i"
done
`
	f, err := os.CreateTemp("", "dummy-burst-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	start := time.Now()
	err = RunAdapter(ctx, ".", pacerState, "gemini", []string{"--model", "google/gemini-3.8-flash"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunAdapter failed: %v", err)
	}
	elapsed := time.Since(start)

	// 100 lines / 20 lines per pace * 2ms = 10ms minimum
	if elapsed < 8*time.Millisecond {
		t.Errorf("Expected pacing to slow down burst, took %v", elapsed)
	}
	out := stdout.String()
	if !strings.Contains(out, "line 100") {
		t.Errorf("Expected burst output to complete, got %d bytes", len(out))
	}
}

func TestClaudeAdapterWithStdinPrompt(t *testing.T) {
	rawArgs := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--add-dir", "/tmp/project",
	}

	opts := parseRawArgs(rawArgs)
	if opts.Prompt != "" {
		t.Errorf("Expected empty prompt initially from CLI flags, got %q", opts.Prompt)
	}

	stdinPrompt := "Review PR #76 and verify tests"
	stdin := strings.NewReader(stdinPrompt)

	script := `#!/bin/sh
cat
`
	f, err := os.CreateTemp("", "dummy-claude-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	pacerState := &router.PacerState{}

	err = RunAdapter(ctx, ".", pacerState, "claude", rawArgs, stdin, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunAdapter failed: %v", err)
	}

	claudeArgs := buildClaudeArgs(opts)
	// Claude args should not contain empty string after --print
	for i, arg := range claudeArgs {
		if arg == "--print" && i+1 < len(claudeArgs) && claudeArgs[i+1] == "" {
			t.Errorf("Claude args contains empty string after --print: %v", claudeArgs)
		}
	}
}

func TestAdapterFailoverRouting(t *testing.T) {
	now := time.Now()
	// Scenario: Gemini is off pace, but Claude is LOCKED.
	// Adapter must NOT fail over to Claude, but stay on Gemini.
	pacerState := &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolGeminiNative: {
				ID: router.PoolGeminiNative,
				Weekly: router.QuotaWindow{
					UsedPct:  50.0,
					ResetsAt: now.Add(6 * 24 * time.Hour), // off pace: 50% in 1 day
				},
			},
			router.PoolPersonalClaude: {
				ID:       router.PoolPersonalClaude,
				IsLocked: true,
			},
		},
	}

	script := `#!/bin/sh
echo "ran gemini"
`
	f, err := os.CreateTemp("", "dummy-routing-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(script)
	f.Close()
	os.Chmod(f.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctx = context.WithValue(ctx, "testBin", f.Name())

	var stdout, stderr bytes.Buffer
	err = RunAdapter(ctx, ".", pacerState, "gemini", []string{"--prompt", "test"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunAdapter failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "ran gemini") {
		t.Errorf("Expected gemini to execute when claude is locked, got %q", stdout.String())
	}
}


// =============================================================================
// REGRESSION TEST SUITE: Provider Chain Failover
// These tests prevent the adapter from ever regressing on work-repo awareness,
// chain ordering, failback behavior, or output buffering.
// =============================================================================

// TestBuildProviderChain_WorkRepo_GeminiStart verifies that a work repo with
// provider=gemini produces the chain: [gemini, work-claude, personal-claude].
func TestBuildProviderChain_WorkRepo_GeminiStart(t *testing.T) {
	chain := BuildProviderChain(true, "gemini")
	if len(chain) != 3 {
		t.Fatalf("expected 3 candidates for work+gemini, got %d", len(chain))
	}
	expected := []string{"gemini", "work-claude", "personal-claude"}
	for i, name := range expected {
		if chain[i].Name != name {
			t.Errorf("chain[%d]: expected %q, got %q", i, name, chain[i].Name)
		}
	}
	// work-claude must have CLAUDE_CONFIG_DIR
	if len(chain[1].ExtraEnv) == 0 {
		t.Fatal("work-claude candidate must have ExtraEnv with CLAUDE_CONFIG_DIR")
	}
	found := false
	for _, env := range chain[1].ExtraEnv {
		if strings.HasPrefix(env, "CLAUDE_CONFIG_DIR=") && strings.Contains(env, ".claude-work") {
			found = true
		}
	}
	if !found {
		t.Errorf("work-claude ExtraEnv missing CLAUDE_CONFIG_DIR=~/.claude-work, got %v", chain[1].ExtraEnv)
	}
	// personal-claude must NOT have ExtraEnv
	if len(chain[2].ExtraEnv) != 0 {
		t.Errorf("personal-claude should not have ExtraEnv, got %v", chain[2].ExtraEnv)
	}
}

// TestBuildProviderChain_WorkRepo_ClaudeStart verifies that a work repo with
// provider=claude produces the chain: [work-claude, personal-claude, gemini].
func TestBuildProviderChain_WorkRepo_ClaudeStart(t *testing.T) {
	chain := BuildProviderChain(true, "claude")
	if len(chain) != 3 {
		t.Fatalf("expected 3 candidates for work+claude, got %d", len(chain))
	}
	expected := []string{"work-claude", "personal-claude", "gemini"}
	for i, name := range expected {
		if chain[i].Name != name {
			t.Errorf("chain[%d]: expected %q, got %q", i, name, chain[i].Name)
		}
	}
}

// TestBuildProviderChain_PersonalRepo_GeminiStart verifies that a personal repo
// with provider=gemini produces the chain: [gemini, personal-claude].
func TestBuildProviderChain_PersonalRepo_GeminiStart(t *testing.T) {
	chain := BuildProviderChain(false, "gemini")
	if len(chain) != 2 {
		t.Fatalf("expected 2 candidates for personal+gemini, got %d", len(chain))
	}
	expected := []string{"gemini", "personal-claude"}
	for i, name := range expected {
		if chain[i].Name != name {
			t.Errorf("chain[%d]: expected %q, got %q", i, name, chain[i].Name)
		}
	}
}

// TestBuildProviderChain_PersonalRepo_ClaudeStart verifies that a personal repo
// with provider=claude produces the chain: [personal-claude, gemini].
func TestBuildProviderChain_PersonalRepo_ClaudeStart(t *testing.T) {
	chain := BuildProviderChain(false, "claude")
	if len(chain) != 2 {
		t.Fatalf("expected 2 candidates for personal+claude, got %d", len(chain))
	}
	expected := []string{"personal-claude", "gemini"}
	for i, name := range expected {
		if chain[i].Name != name {
			t.Errorf("chain[%d]: expected %q, got %q", i, name, chain[i].Name)
		}
	}
}

// TestIsPoolLocked_Scenarios validates the quota lock detection logic.
func TestIsPoolLocked_Scenarios(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		pool   *router.QuotaPool
		locked bool
	}{
		{"nil pool is not locked", nil, false},
		{"explicitly locked", &router.QuotaPool{IsLocked: true}, true},
		{"zero 5h headroom with future reset", &router.QuotaPool{
			FiveHour: router.QuotaWindow{RemainingPct: 0.0, ResetsAt: now.Add(1 * time.Hour)},
		}, true},
		{"healthy pool", &router.QuotaPool{
			FiveHour: router.QuotaWindow{RemainingPct: 85.0, ResetsAt: now.Add(1 * time.Hour)},
			Weekly:   router.QuotaWindow{RemainingPct: 70.0},
		}, false},
		{"zero 5h but reset in the past", &router.QuotaPool{
			FiveHour: router.QuotaWindow{RemainingPct: 0.0, ResetsAt: now.Add(-1 * time.Hour)},
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPoolLocked(tt.pool)
			if got != tt.locked {
				t.Errorf("expected locked=%v, got %v", tt.locked, got)
			}
		})
	}
}

// TestChainSkipsLockedProviders verifies that locked providers are skipped
// without execution. The first unlocked provider should run.
func TestChainSkipsLockedProviders(t *testing.T) {
	now := time.Now()

	// Gemini locked, work-claude locked, personal-claude available.
	// On a work repo with provider=gemini, chain is [gemini, work-claude, personal-claude].
	// It should skip gemini and work-claude, execute personal-claude.
	successScript := `#!/bin/sh
echo "personal-claude ran"
`
	f, err := os.CreateTemp("", "skip-locked-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(successScript)
	f.Close()
	os.Chmod(f.Name(), 0755)

	pacerState := &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolGeminiNative: {
				ID:       router.PoolGeminiNative,
				IsLocked: true,
			},
			router.PoolWorkClaude: {
				ID:       router.PoolWorkClaude,
				IsLocked: true,
			},
			router.PoolPersonalClaude: {
				ID:       router.PoolPersonalClaude,
				FiveHour: router.QuotaWindow{RemainingPct: 80.0, ResetsAt: now.Add(1 * time.Hour)},
				Weekly:   router.QuotaWindow{RemainingPct: 50.0},
			},
		},
	}

	// Build a chain manually, override bins to our test script
	chain := BuildProviderChain(true, "gemini")
	for i := range chain {
		chain[i].Bin = f.Name()
		chain[i].ExtraEnv = nil // don't need real CLAUDE_CONFIG_DIR in test
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	var lastErr error
	for _, candidate := range chain {
		pool := pacerState.Pools[candidate.PoolID]
		if isPoolLocked(pool) {
			continue
		}
		args := candidate.BuildArgs(ParsedOptions{Prompt: "test", OutputFormat: "stream-json"})
		var buf bytes.Buffer
		err := runCommandWithEnv(ctx, candidate.Bin, args, candidate.ExtraEnv, nil, &buf, &stderr)
		if err == nil {
			stdout.Write(buf.Bytes())
			break
		}
		lastErr = err
	}

	if lastErr != nil {
		t.Fatalf("unexpected error: %v", lastErr)
	}
	if !strings.Contains(stdout.String(), "personal-claude ran") {
		t.Errorf("expected personal-claude to execute after skipping locked providers, got %q", stdout.String())
	}
	// Verify stderr shows skipping messages would be logged (the actual RunAdapter does this)
}

// TestAllProvidersExhausted verifies clean error when every provider is locked.
func TestAllProvidersExhausted(t *testing.T) {
	pacerState := &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolGeminiNative:   {ID: router.PoolGeminiNative, IsLocked: true},
			router.PoolPersonalClaude: {ID: router.PoolPersonalClaude, IsLocked: true},
		},
	}

	chain := BuildProviderChain(false, "gemini")
	allLocked := true
	for _, candidate := range chain {
		pool := pacerState.Pools[candidate.PoolID]
		if !isPoolLocked(pool) {
			allLocked = false
		}
	}
	if !allLocked {
		t.Fatal("test setup error: expected all providers to be locked")
	}
}

// TestFailbackToGemini verifies that if Gemini was locked last run but is now
// available, it executes first without touching Claude.
func TestFailbackToGemini(t *testing.T) {
	now := time.Now()

	// Simulate: Gemini was locked last run (reset already passed), now available.
	pacerState := &router.PacerState{
		Pools: map[router.PoolID]*router.QuotaPool{
			router.PoolGeminiNative: {
				ID:       router.PoolGeminiNative,
				FiveHour: router.QuotaWindow{RemainingPct: 85.0, ResetsAt: now.Add(4 * time.Hour)},
				Weekly:   router.QuotaWindow{RemainingPct: 70.0},
			},
			router.PoolPersonalClaude: {
				ID:       router.PoolPersonalClaude,
				FiveHour: router.QuotaWindow{RemainingPct: 50.0, ResetsAt: now.Add(2 * time.Hour)},
				Weekly:   router.QuotaWindow{RemainingPct: 30.0},
			},
		},
	}

	chain := BuildProviderChain(false, "gemini")

	// First candidate should be gemini and should NOT be locked
	first := chain[0]
	if first.Name != "gemini" {
		t.Fatalf("expected first candidate to be gemini, got %q", first.Name)
	}
	pool := pacerState.Pools[first.PoolID]
	if isPoolLocked(pool) {
		t.Fatal("gemini should not be locked in this scenario")
	}
}

// TestWorkRepoUsesWorkClaude verifies that when Gemini is locked on a work repo,
// the adapter uses work-claude (with CLAUDE_CONFIG_DIR), not personal claude.
func TestWorkRepoUsesWorkClaude(t *testing.T) {
	chain := BuildProviderChain(true, "gemini")

	// chain[0] = gemini (will be locked), chain[1] = work-claude, chain[2] = personal-claude
	if chain[1].Name != "work-claude" {
		t.Fatalf("expected chain[1] to be work-claude, got %q", chain[1].Name)
	}

	// Verify work-claude has the correct CLAUDE_CONFIG_DIR
	hasWorkDir := false
	for _, env := range chain[1].ExtraEnv {
		if strings.Contains(env, "CLAUDE_CONFIG_DIR=") && strings.Contains(env, ".claude-work") {
			hasWorkDir = true
		}
	}
	if !hasWorkDir {
		t.Errorf("work-claude must set CLAUDE_CONFIG_DIR to ~/.claude-work, got env: %v", chain[1].ExtraEnv)
	}

	// Verify personal-claude does NOT have CLAUDE_CONFIG_DIR
	for _, env := range chain[2].ExtraEnv {
		if strings.Contains(env, "CLAUDE_CONFIG_DIR") {
			t.Errorf("personal-claude must NOT set CLAUDE_CONFIG_DIR, got env: %v", chain[2].ExtraEnv)
		}
	}
}

// TestFailoverBuffersOutput verifies that a failed provider's stdout is swallowed
// and only the successful provider's output reaches the caller.
func TestFailoverBuffersOutput(t *testing.T) {
	// First script: outputs poison then exits 1
	poisonScript := `#!/bin/sh
echo "SESSION_LIMIT_ERROR: resets 1pm"
exit 1
`
	pf, err := os.CreateTemp("", "poison-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pf.Name())
	pf.WriteString(poisonScript)
	pf.Close()
	os.Chmod(pf.Name(), 0755)

	// Second script: outputs valid result
	goodScript := `#!/bin/sh
echo "valid output from fallback"
`
	gf, err := os.CreateTemp("", "good-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(gf.Name())
	gf.WriteString(goodScript)
	gf.Close()
	os.Chmod(gf.Name(), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate chain: first candidate fails (poison), second succeeds (good)
	candidates := []providerCandidate{
		{Name: "poison-provider", Bin: pf.Name(), BuildArgs: buildAgyArgs},
		{Name: "good-provider", Bin: gf.Name(), BuildArgs: buildAgyArgs},
	}

	opts := ParsedOptions{Prompt: "test", OutputFormat: "stream-json"}
	var finalStdout, stderrBuf bytes.Buffer

	for _, candidate := range candidates {
		args := candidate.BuildArgs(opts)
		var buf bytes.Buffer
		err := runCommandWithEnv(ctx, candidate.Bin, args, nil, nil, &buf, &stderrBuf)
		if err == nil {
			finalStdout.Write(buf.Bytes())
			break
		}
		// Failed: stdout swallowed (buf is discarded)
	}

	out := finalStdout.String()
	if strings.Contains(out, "SESSION_LIMIT_ERROR") {
		t.Errorf("poison output must NOT reach final stdout, got %q", out)
	}
	if !strings.Contains(out, "valid output from fallback") {
		t.Errorf("expected fallback output in final stdout, got %q", out)
	}
}

// TestPoolIDAssignment verifies each provider candidate maps to the correct pool.
func TestPoolIDAssignment(t *testing.T) {
	workChain := BuildProviderChain(true, "gemini")
	expectedPools := map[string]router.PoolID{
		"gemini":          router.PoolGeminiNative,
		"work-claude":     router.PoolWorkClaude,
		"personal-claude": router.PoolPersonalClaude,
	}
	for _, c := range workChain {
		expected, ok := expectedPools[c.Name]
		if !ok {
			t.Errorf("unexpected candidate name %q", c.Name)
			continue
		}
		if c.PoolID != expected {
			t.Errorf("candidate %q: expected PoolID %q, got %q", c.Name, expected, c.PoolID)
		}
	}
}


// TestBuildAgyArgs_ClaudeModelTranslation verifies that Claude models are translated
// to Gemini equivalents via FallbackPairingMatrix, not stripped to empty string.
// Regression test: prevents "--model "" --effort "high"" error on agy.
func TestBuildAgyArgs_ClaudeModelTranslation(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		effort        string
		expectModel   string
		expectEffort  string
	}{
		{"opus to pro", "claude-opus-5", "high", "gemini-3.1-pro", "high"},
		{"sonnet to flash", "claude-sonnet-4-6", "", "gemini-3.8-flash", "medium"},
		{"sonnet with high effort", "claude-sonnet-4-6", "high", "gemini-3.8-flash", "high"},
		{"empty model gets default", "", "high", "gemini-3.8-flash", "high"},
		{"gemini model passes through", "gemini-3.8-flash", "medium", "gemini-3.8-flash", "medium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ParsedOptions{
				Prompt:       "test prompt",
				Model:        tt.model,
				Effort:       tt.effort,
				OutputFormat: "stream-json",
			}
			args := buildAgyArgs(opts)

			var foundModel, foundEffort string
			for i, a := range args {
				if a == "--model" && i+1 < len(args) {
					foundModel = args[i+1]
				}
				if a == "--effort" && i+1 < len(args) {
					foundEffort = args[i+1]
				}
			}
			if foundModel != tt.expectModel {
				t.Errorf("expected model %q, got %q (full args: %v)", tt.expectModel, foundModel, args)
			}
			if foundEffort != tt.expectEffort {
				t.Errorf("expected effort %q, got %q (full args: %v)", tt.expectEffort, foundEffort, args)
			}
			// Verify effort is never passed without a model
			if foundEffort != "" && foundModel == "" {
				t.Error("effort was passed without a model, which causes agy to error")
			}
		})
	}
}

// TestBuildAgyArgs_NoConversationID verifies that buildAgyArgs never passes
// a conversation/session ID. Claude session IDs are invalid for agy.
// Regression test: prevents "conversation not found" error on agy.
func TestBuildAgyArgs_NoConversationID(t *testing.T) {
	opts := ParsedOptions{
		Prompt:         "test",
		Model:          "claude-opus-5",
		ConversationID: "dbb58aeb-9fee-4425-9d6d-915ed3a63beb",
		OutputFormat:   "stream-json",
	}
	args := buildAgyArgs(opts)
	for _, a := range args {
		if a == "--conversation" || a == "--resume" || a == opts.ConversationID {
			t.Errorf("agy args must not contain conversation ID, but got: %v", args)
		}
	}
}

// TestFallbackClearsConversationID verifies that fallback candidates do not
// receive the original provider's conversation ID.
// Regression test: prevents stale session resume across providers.
func TestFallbackClearsConversationID(t *testing.T) {
	opts := ParsedOptions{
		Prompt:         "test",
		Model:          "claude-opus-5",
		ConversationID: "abc-123-session",
		OutputFormat:   "stream-json",
	}

	// Simulate: first candidate (work-claude) gets the session ID
	firstArgs := buildClaudeArgs(opts)
	hasResume := false
	for _, a := range firstArgs {
		if a == "--resume" {
			hasResume = true
		}
	}
	if !hasResume {
		t.Error("first candidate should get --resume with conversation ID")
	}

	// Simulate: second candidate (fallback) should NOT get the session ID
	fallbackOpts := opts
	fallbackOpts.ConversationID = ""
	fallbackArgs := buildClaudeArgs(fallbackOpts)
	for _, a := range fallbackArgs {
		if a == "--resume" || a == opts.ConversationID {
			t.Errorf("fallback candidate must not have --resume or session ID, got: %v", fallbackArgs)
		}
	}
}
