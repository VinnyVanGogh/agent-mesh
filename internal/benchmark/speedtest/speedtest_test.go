package speedtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureFixtures(t *testing.T) {
	tempDir := t.TempDir()
	if err := EnsureFixtures(tempDir); err != nil {
		t.Fatalf("EnsureFixtures failed: %v", err)
	}

	requiredFiles := []string{
		"sample_numbers.txt",
		"service.go",
		"service_test.go",
		"lean_prompt.md",
		"staypoint_condensed_prompt.md",
		"paperclip_heavy_prompt.md",
		"staypoint_mcp.json",
		"paperclip_mcp.json",
	}

	for _, file := range requiredFiles {
		path := filepath.Join(tempDir, file)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected fixture file %s does not exist", file)
		}
	}
}

func TestMockProbeExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := RunConfig{
		Scenario: Scenario1SingleResponse,
		Lane:     LaneStayPoint,
		Effort:   EffortLow,
		Mock:     true,
	}

	telem, err := RunProbe(ctx, cfg)
	if err != nil {
		t.Fatalf("RunProbe failed: %v", err)
	}

	if !telem.Success {
		t.Errorf("expected success, got error: %s", telem.ErrorMessage)
	}
	if telem.SpawnLatency <= 0 {
		t.Errorf("expected positive spawn latency, got %v", telem.SpawnLatency)
	}
	if telem.TTFTLatency <= 0 {
		t.Errorf("expected positive TTFT latency, got %v", telem.TTFTLatency)
	}
	if telem.TotalDuration <= 0 {
		t.Errorf("expected positive total duration, got %v", telem.TotalDuration)
	}
}

func TestRunMatrixMock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tempDir := t.TempDir()

	opts := BenchmarkOptions{
		Scenarios: []ScenarioID{
			Scenario1SingleResponse,
			Scenario2ToolCall,
			Scenario3MultiChain,
			Scenario4FileMutation,
		},
		Lanes: []Lane{
			LaneBaseline,
			LanePaperclip,
			LaneStayPoint,
		},
		EffortLevels: []EffortLevel{
			EffortLow,
			EffortHigh,
		},
		Iterations: 1,
		Mock:       true,
		OutputDir:  tempDir,
		Quiet:      true,
	}

	report, err := RunMatrix(ctx, opts)
	if err != nil {
		t.Fatalf("RunMatrix failed: %v", err)
	}

	expectedRuns := 4 * 3 * 2 * 1
	if len(report.Runs) != expectedRuns {
		t.Fatalf("expected %d runs, got %d", expectedRuns, len(report.Runs))
	}

	if len(report.Stats) != 4*3*2 {
		t.Errorf("expected 24 stat entries, got %d", len(report.Stats))
	}

	// Verify report files were written
	jsonReport := filepath.Join(tempDir, "benchmark_report.json")
	if _, err := os.Stat(jsonReport); os.IsNotExist(err) {
		t.Errorf("expected benchmark_report.json to exist")
	}

	mdReport := filepath.Join(tempDir, "benchmark_report.md")
	if _, err := os.Stat(mdReport); os.IsNotExist(err) {
		t.Errorf("expected benchmark_report.md to exist")
	}
}
