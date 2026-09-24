package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// BenchmarkTranscriptParsing benchmarks parsing a 100-turn Antigravity transcript from disk.
func BenchmarkTranscriptParsing(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "handoff-bench-parse-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	transcriptPath := filepath.Join(tmpDir, "transcript.jsonl")
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		sb.WriteString(fmt.Sprintf(`{"step_index":%d,"type":"USER_INPUT","source":"USER","content":"Turn %d: Refactor connection pool and ensure metrics export correctly","created_at":"2026-09-23T12:00:00Z"}`+"\n", i, i))
		sb.WriteString(fmt.Sprintf(`{"step_index":%d,"type":"PLANNER_RESPONSE","source":"MODEL","content":"Turn %d: Updated connection pool implementation and added unit tests in internal/db/pool_test.go","created_at":"2026-09-23T12:00:05Z"}`+"\n", i, i))
	}
	if err := os.WriteFile(transcriptPath, []byte(sb.String()), 0644); err != nil {
		b.Fatalf("failed to write transcript fixture: %v", err)
	}

	modTime := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess := parseAntigravitySession("bench-session", transcriptPath, modTime)
		if sess == nil || sess.TotalUserTurns != 100 {
			b.Fatalf("expected 100 user turns, got %v", sess)
		}
	}
}

// BenchmarkGenerateSessionHandoff benchmarks the complete 5-anchor context handoff synthesis
// from an in-memory parsed session (excluding clipboard pbcopy and git subprocesses).
func BenchmarkGenerateSessionHandoff(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "handoff-bench-gen-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sess := &SessionInfo{
		ID:             "bench-session-100-turns",
		AgentType:      "gemini",
		RepoPath:       tmpDir,
		GitBranch:      "remediation",
		StartedAt:      time.Now().Add(-2 * time.Hour),
		UpdatedAt:      time.Now(),
		RootGoal:       "Refactor connection pool and ensure metrics export correctly",
		TotalUserTurns: 100,
		UserDirectives: []string{
			"Directive 1: Add connection pooling metrics",
			"Directive 2: Integrate with Prometheus counters",
			"Directive 3: Write benchmark test for pool acquisition",
			"Directive 4: Limit max idle connections to 10",
			"Directive 5: Ensure thread-safe cursor cache",
			"Directive 6: Run race detector on connection pool tests",
		},
		LastUserPrompt: "Ensure all benchmarks pass and output reports no race conditions",
		LastAssistant:  "All benchmarks passed cleanly with 0 race conditions detected.",
		TranscriptPath: filepath.Join(tmpDir, "transcript.jsonl"),
	}

	opts := SessionHandoffOptions{
		Trigger:        "benchmark",
		DataDir:        tmpDir,
		MaxKeepPerRepo: 3,
		SkipClipboard:  true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prompt, _, err := GenerateSessionHandoffWithOptions(sess, nil, opts)
		if err != nil {
			b.Fatalf("GenerateSessionHandoffWithOptions failed: %v", err)
		}
		if len(prompt) == 0 {
			b.Fatalf("expected non-empty handoff prompt")
		}
	}
}
