package router

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkStatusline(b *testing.B) {
	payload := `{"model":{"display_name":"Claude 3.7 Sonnet"},"workspace":{"current_dir":"/path/to/project-mesh"},"cost":{"total_cost_usd":0.43},"context_window":{"used_percentage":18,"remaining_tokens":164000}}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader([]byte(payload))
		if err := RenderStatusline(io.Discard, r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacerLoad(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := LoadPacerState(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoute(b *testing.B) {
	ctx := context.Background()
	pacerState, err := LoadPacerState()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Route(ctx, "/path/to/project-mesh", pacerState, RouteOptions{
			CheckSSH: false,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestPacer_ClaudeKeyFallback(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rateLimitsDir := filepath.Join(tmpHome, ".config", "rate-limits")
	if err := os.MkdirAll(rateLimitsDir, 0755); err != nil {
		t.Fatal(err)
	}

	stateJSON := `{
		"quotas": {
			"Claude": {
				"five_hour_used": 15.0,
				"five_hour_remaining": 85.0,
				"weekly_used": 20.0,
				"weekly_remaining": 80.0
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(rateLimitsDir, "state.json"), []byte(stateJSON), 0644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadPacerState()
	if err != nil {
		t.Fatalf("LoadPacerState failed: %v", err)
	}

	personalPool := state.Pools[PoolPersonalClaude]
	if personalPool.FiveHour.RemainingPct != 85.0 {
		t.Errorf("expected 85.0%% 5h remaining from Claude key fallback, got %f", personalPool.FiveHour.RemainingPct)
	}
	if personalPool.Weekly.RemainingPct != 80.0 {
		t.Errorf("expected 80.0%% weekly remaining from Claude key fallback, got %f", personalPool.Weekly.RemainingPct)
	}
}
