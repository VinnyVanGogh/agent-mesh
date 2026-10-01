package speedtest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// Raw stream event schemas for Claude Code output-format stream-json.
type claudeRawContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type claudeRawMessage struct {
	Model   string                  `json:"model"`
	Role    string                  `json:"role"`
	Content []claudeRawContentBlock `json:"content"`
	Usage   *claudeRawUsage         `json:"usage,omitempty"`
}

type claudeRawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokensDetails      struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type claudeRawEvent struct {
	Type          string            `json:"type"`
	Subtype       string            `json:"subtype,omitempty"`
	Message       *claudeRawMessage `json:"message,omitempty"`
	Result        string            `json:"result,omitempty"`
	IsError       bool              `json:"is_error,omitempty"`
	DurationMs    int64             `json:"duration_ms,omitempty"`
	DurationAPIMs int64             `json:"duration_api_ms,omitempty"`
	TTFTMs        int64             `json:"ttft_ms,omitempty"`
	TotalCostUSD  float64           `json:"total_cost_usd,omitempty"`
	Usage         *claudeRawUsage   `json:"usage,omitempty"`
}

// RunProbe executes a single command run and captures high-precision telemetry.
func RunProbe(ctx context.Context, cfg RunConfig) (*RunTelemetry, error) {
	if cfg.Mock {
		return runMockProbe(ctx, cfg)
	}

	runID := "probe_" + uuid.New().String()[:8]
	telemetry := &RunTelemetry{
		RunID:     runID,
		Scenario:  cfg.Scenario,
		Lane:      cfg.Lane,
		Effort:    cfg.Effort,
		Iteration: cfg.Iteration,
		Timestamp: time.Now(),
		ToolCalls: make([]ToolCallEvent, 0),
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		claudePath = "/Users/vincevasile/.local/bin/claude"
	}

	args := []string{
		"-p", cfg.Prompt,
		"--output-format", "stream-json",
		"--verbose",
	}

	if cfg.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	if cfg.Effort != "" {
		args = append(args, "--effort", string(cfg.Effort))
	}

	if cfg.SystemPromptFile != "" {
		args = append(args, "--append-system-prompt-file", cfg.SystemPromptFile)
	}

	if cfg.MCPConfigFile != "" {
		args = append(args, "--mcp-config", cfg.MCPConfigFile)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, claudePath, args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// Environment configuration
	cmd.Env = os.Environ()
	if len(cfg.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, cfg.ExtraEnv...)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("probe stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("probe spawn failed: %w", err)
	}

	var t1 time.Time
	var t2 time.Time
	openTools := make(map[string]*ToolCallEvent)

	scanner := bufio.NewScanner(stdoutPipe)
	// Buffer up to 1MB per line for large tool returns or payloads
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		now := time.Now()
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		if t1.IsZero() {
			t1 = now
			telemetry.SpawnLatency = t1.Sub(t0)
		}

		var ev claudeRawEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "assistant":
			if ev.Message != nil {
				for _, block := range ev.Message.Content {
					if block.Type == "text" || block.Type == "thinking" {
						if t2.IsZero() {
							t2 = now
							telemetry.TTFTLatency = t2.Sub(t0)
						}
					}
					if block.Type == "tool_use" {
						toolEv := &ToolCallEvent{
							ToolName:  block.Name,
							ToolID:    block.ID,
							StartedAt: now,
						}
						openTools[block.ID] = toolEv
					}
				}
			}

		case "user":
			if ev.Message != nil {
				for _, block := range ev.Message.Content {
					if block.Type == "tool_result" {
						if toolEv, ok := openTools[block.ToolUseID]; ok {
							toolEv.EndedAt = now
							toolEv.Duration = now.Sub(toolEv.StartedAt)
							toolEv.IsError = block.IsError
							telemetry.ToolCalls = append(telemetry.ToolCalls, *toolEv)
							telemetry.ToolDurationTotal += toolEv.Duration
							delete(openTools, block.ToolUseID)
						}
					}
				}
			}

		case "result":
			telemetry.ResultText = ev.Result
			telemetry.DurationMs = ev.DurationMs
			telemetry.DurationApiMs = ev.DurationAPIMs
			telemetry.TTFTMs = ev.TTFTMs
			telemetry.CostUSD = ev.TotalCostUSD
			if ev.Usage != nil {
				telemetry.InputTokens = ev.Usage.InputTokens
				telemetry.OutputTokens = ev.Usage.OutputTokens
				telemetry.CacheReadTokens = ev.Usage.CacheReadInputTokens
				telemetry.CacheCreationTokens = ev.Usage.CacheCreationInputTokens
				telemetry.ThinkingTokens = ev.Usage.OutputTokensDetails.ThinkingTokens
			}
		}
	}

	waitErr := cmd.Wait()
	t5 := time.Now()
	telemetry.TotalDuration = t5.Sub(t0)

	// Close any unclosed tool calls (e.g. if stream ended abruptly)
	for _, toolEv := range openTools {
		toolEv.EndedAt = t5
		toolEv.Duration = t5.Sub(toolEv.StartedAt)
		telemetry.ToolCalls = append(telemetry.ToolCalls, *toolEv)
		telemetry.ToolDurationTotal += toolEv.Duration
	}

	if t1.IsZero() {
		telemetry.SpawnLatency = telemetry.TotalDuration
	}
	if t2.IsZero() {
		telemetry.TTFTLatency = telemetry.TotalDuration
	}

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			telemetry.ExitCode = exitErr.ExitCode()
		} else {
			telemetry.ExitCode = 1
		}
		telemetry.Success = false
		telemetry.ErrorMessage = fmt.Sprintf("%v: %s", waitErr, strings.TrimSpace(stderrBuf.String()))
	} else {
		telemetry.ExitCode = 0
		telemetry.Success = true
	}

	// Capture child process peak RSS
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			if runtime.GOOS == "darwin" {
				telemetry.PeakRSSBytes = ru.Maxrss
			} else {
				telemetry.PeakRSSBytes = ru.Maxrss * 1024
			}
		}
	}

	return telemetry, nil
}

// runMockProbe generates deterministic telemetry for offline verification and fast unit tests.
func runMockProbe(_ context.Context, cfg RunConfig) (*RunTelemetry, error) {
	now := time.Now()
	runID := "mock_" + uuid.New().String()[:8]

	// Simulate representative delays based on lane and effort
	spawnDelay := 50 * time.Millisecond
	ttftDelay := 450 * time.Millisecond
	totalDelay := 950 * time.Millisecond
	toolDelay := 25 * time.Millisecond
	peakRSS := int64(45 * 1024 * 1024) // 45 MB
	thinkingTokens := int64(0)
	inputTokens := int64(500)
	outputTokens := int64(80)

	if cfg.Lane == LanePaperclip {
		spawnDelay = 220 * time.Millisecond
		ttftDelay = 1200 * time.Millisecond
		totalDelay = 2400 * time.Millisecond
		toolDelay = 95 * time.Millisecond
		peakRSS = int64(180 * 1024 * 1024) // 180 MB Node overhead
		inputTokens = 32000                 // heavy prompt
	} else if cfg.Lane == LaneStayPoint {
		spawnDelay = 8 * time.Millisecond
		ttftDelay = 510 * time.Millisecond
		totalDelay = 1050 * time.Millisecond
		toolDelay = 12 * time.Millisecond  // pure Go stdio MCP
		peakRSS = int64(38 * 1024 * 1024)  // 38 MB Go
		inputTokens = 3100                 // condensed prompt
	}

	if cfg.Effort == EffortHigh {
		ttftDelay += 3500 * time.Millisecond
		totalDelay += 4200 * time.Millisecond
		thinkingTokens = 420
	}

	toolCalls := make([]ToolCallEvent, 0)
	if cfg.Scenario == Scenario2ToolCall || cfg.Scenario == Scenario3MultiChain || cfg.Scenario == Scenario4FileMutation {
		toolCalls = append(toolCalls, ToolCallEvent{
			ToolName:  "Bash",
			ToolID:    "tool_mock_1",
			StartedAt: now.Add(ttftDelay),
			EndedAt:   now.Add(ttftDelay + toolDelay),
			Duration:  toolDelay,
		})
	}
	if cfg.Scenario == Scenario3MultiChain {
		toolCalls = append(toolCalls, ToolCallEvent{
			ToolName:  "Write",
			ToolID:    "tool_mock_2",
			StartedAt: now.Add(ttftDelay + toolDelay + 20*time.Millisecond),
			EndedAt:   now.Add(ttftDelay + 2*toolDelay + 20*time.Millisecond),
			Duration:  toolDelay,
		})
	}
	if cfg.Scenario == Scenario4FileMutation && cfg.Lane == LaneStayPoint {
		toolCalls = append(toolCalls, ToolCallEvent{
			ToolName:  "staypoint_checkpoint",
			ToolID:    "tool_mock_cp",
			StartedAt: now.Add(ttftDelay + toolDelay + 50*time.Millisecond),
			EndedAt:   now.Add(ttftDelay + toolDelay + 50*time.Millisecond + 14*time.Millisecond),
			Duration:  14 * time.Millisecond,
		})
	}

	return &RunTelemetry{
		RunID:               runID,
		Scenario:            cfg.Scenario,
		Lane:                cfg.Lane,
		Effort:              cfg.Effort,
		Iteration:           cfg.Iteration,
		Timestamp:           now,
		SpawnLatency:        spawnDelay,
		TTFTLatency:         ttftDelay,
		TotalDuration:       totalDelay,
		ToolCalls:           toolCalls,
		ToolDurationTotal:   time.Duration(len(toolCalls)) * toolDelay,
		PeakRSSBytes:        peakRSS,
		ExitCode:            0,
		Success:             true,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		CacheReadTokens:     inputTokens / 2,
		CacheCreationTokens: inputTokens / 4,
		ThinkingTokens:      thinkingTokens,
		DurationApiMs:       ttftDelay.Milliseconds(),
		DurationMs:          totalDelay.Milliseconds(),
		TTFTMs:              ttftDelay.Milliseconds(),
		CostUSD:             0.015,
		ResultText:          "Mock execution completed successfully.",
	}, nil
}
