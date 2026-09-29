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
