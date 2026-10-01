package speedtest

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/checkpoint"
)

// ScenarioDefinition pairs prompt templates with scenario configuration.
type ScenarioDefinition struct {
	ID          ScenarioID
	Name        string
	Description string
	Prompt      string
}

// GetScenarioDefinitions returns specifications for the 4 approved scenarios.
func GetScenarioDefinitions(fixturesDir string) map[ScenarioID]ScenarioDefinition {
	numbersFile := filepath.Join(fixturesDir, "sample_numbers.txt")
	statsFile := filepath.Join(fixturesDir, "stats.json")
	serviceFile := filepath.Join(fixturesDir, "service.go")
	serviceTestFile := filepath.Join(fixturesDir, "service_test.go")

	return map[ScenarioID]ScenarioDefinition{
		Scenario1SingleResponse: {
			ID:          Scenario1SingleResponse,
			Name:        "Single Response Latency",
			Description: "Baseline generation measuring TTFT and raw stream generation without tools",
			Prompt:      "Explain the difference between optimistic and pessimistic locking in one concise paragraph.",
		},
		Scenario2ToolCall: {
			ID:          Scenario2ToolCall,
			Name:        "Single Tool Call Overhead",
			Description: "Single lightweight tool invocation measuring stdio/HTTP serialization roundtrip",
			Prompt:      "Check the git branch status and report the current branch name.",
		},
		Scenario3MultiChain: {
			ID:          Scenario3MultiChain,
			Name:        "Multi-Chain Response (Sequential Reasoning)",
			Description: "Multi-turn sequential chain: file read -> computation -> file write",
			Prompt: fmt.Sprintf(
				"Read the file %s, compute the median and standard deviation of the numbers, and write the JSON result into %s with keys median and std_dev.",
				numbersFile, statsFile,
			),
		},
		Scenario4FileMutation: {
			ID:          Scenario4FileMutation,
			Name:        "File Editing & Workspace Mutation",
			Description: "Modifying existing service struct, updating unit tests, and verifying state capture",
			Prompt: fmt.Sprintf(
				"In %s, add a thread-safe Inc() method to CounterStruct that increments count by 1. Then update %s to verify Inc(), and run the test.",
				serviceFile, serviceTestFile,
			),
		},
	}
}

// ExecuteScenarioRun runs a single iteration of a scenario under a given lane and effort.
func ExecuteScenarioRun(ctx context.Context, scenarioID ScenarioID, lane Lane, effort EffortLevel, iter int, fixturesDir string, opts BenchmarkOptions) (*RunTelemetry, error) {
	defs := GetScenarioDefinitions(fixturesDir)
	def, ok := defs[scenarioID]
	if !ok {
		return nil, fmt.Errorf("unknown scenario: %s", scenarioID)
	}

	// Reset any modified fixture files before running
	if !opts.Mock {
		_ = ResetScenarioFixtures(fixturesDir)
	}

	var promptFile string
	var mcpFile string

	switch lane {
	case LaneBaseline:
		promptFile = filepath.Join(fixturesDir, "lean_prompt.md")
		mcpFile = "" // no extra MCP
	case LanePaperclip:
		promptFile = filepath.Join(fixturesDir, "paperclip_heavy_prompt.md")
		mcpFile = filepath.Join(fixturesDir, "paperclip_mcp.json")
	case LaneStayPoint:
		promptFile = filepath.Join(fixturesDir, "staypoint_condensed_prompt.md")
		mcpFile = filepath.Join(fixturesDir, "staypoint_mcp.json")
	}

	cfg := RunConfig{
		Scenario:                   scenarioID,
		Lane:                       lane,
		Effort:                     effort,
		Model:                      opts.Model,
		Prompt:                     def.Prompt,
		SystemPromptFile:           promptFile,
		MCPConfigFile:              mcpFile,
		WorkDir:                    fixturesDir,
		Timeout:                    opts.Timeout,
		Iteration:                  iter,
		Mock:                       opts.Mock,
		DangerouslySkipPermissions: true,
	}

	// For Scenario 4 under StayPoint, pre-take a micro-checkpoint to measure native checkpointing
	var cpDuration time.Duration
	if scenarioID == Scenario4FileMutation && lane == LaneStayPoint && !opts.Mock {
		cpStart := time.Now()
		_, _ = checkpoint.CreateCheckpoint(ctx, checkpoint.CreateOptions{
			WorkDir:   fixturesDir,
			Message:   fmt.Sprintf("bench_pre_%s_%d", scenarioID, iter),
			SessionID: "bench-session",
		})
		cpDuration = time.Since(cpStart)
	}

	telem, err := RunProbe(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// If a pre-checkpoint was taken, add it to tool call records for transparency
	if cpDuration > 0 {
		telem.ToolCalls = append([]ToolCallEvent{
			{
				ToolName:  "staypoint_checkpoint",
				ToolID:    "sp_cp_init",
				StartedAt: telem.Timestamp,
				EndedAt:   telem.Timestamp.Add(cpDuration),
				Duration:  cpDuration,
				IsError:   false,
			},
		}, telem.ToolCalls...)
		telem.ToolDurationTotal += cpDuration
	}

	return telem, nil
}
