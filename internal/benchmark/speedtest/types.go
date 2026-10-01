package speedtest

import (
	"time"
)

// ScenarioID represents a specific benchmark test case.
type ScenarioID string

const (
	Scenario1SingleResponse ScenarioID = "scenario_1_single_response"
	Scenario2ToolCall       ScenarioID = "scenario_2_tool_call"
	Scenario3MultiChain     ScenarioID = "scenario_3_multi_chain"
	Scenario4FileMutation   ScenarioID = "scenario_4_file_mutation"
)

// Lane represents the execution harness configuration.
type Lane string

const (
	LaneBaseline  Lane = "baseline"  // Direct Claude CLI, lean prompt
	LanePaperclip Lane = "paperclip" // Reconstructed Paperclip: heavy prompt + HTTP MCP
	LaneStayPoint Lane = "staypoint" // StayPoint: condensed prompt diet + pure-Go stdio MCP
)

// EffortLevel controls Claude reasoning effort.
type EffortLevel string

const (
	EffortLow  EffortLevel = "low"
	EffortHigh EffortLevel = "high"
)

// ToolCallEvent records a single tool invocation within a stream.
type ToolCallEvent struct {
	ToolName  string        `json:"tool_name"`
	ToolID    string        `json:"tool_id"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   time.Time     `json:"ended_at"`
	Duration  time.Duration `json:"duration_ns"`
	IsError   bool          `json:"is_error"`
}

// RunConfig contains parameters for a single benchmark execution.
type RunConfig struct {
	Scenario                   ScenarioID    `json:"scenario"`
	Lane                       Lane          `json:"lane"`
	Effort                     EffortLevel   `json:"effort"`
	Model                      string        `json:"model"`
	Prompt                     string        `json:"prompt"`
	SystemPromptFile           string        `json:"system_prompt_file,omitempty"`
	MCPConfigFile              string        `json:"mcp_config_file,omitempty"`
	WorkDir                    string        `json:"work_dir"`
	Timeout                    time.Duration `json:"timeout"`
	Iteration                  int           `json:"iteration"`
	Mock                       bool          `json:"mock"`
	DangerouslySkipPermissions bool          `json:"dangerously_skip_permissions"`
	ExtraEnv                   []string      `json:"extra_env,omitempty"`
}

// RunTelemetry captures high-precision timing and resource telemetry.
type RunTelemetry struct {
	RunID               string          `json:"run_id"`
	Scenario            ScenarioID      `json:"scenario"`
	Lane                Lane            `json:"lane"`
	Effort              EffortLevel     `json:"effort"`
	Iteration           int             `json:"iteration"`
	Timestamp           time.Time       `json:"timestamp"`
	SpawnLatency        time.Duration   `json:"spawn_latency_ns"` // t1 - t0: spawn to first stdout line
	TTFTLatency         time.Duration   `json:"ttft_latency_ns"`  // t2 - t0: spawn to first assistant token
	TotalDuration       time.Duration   `json:"total_duration_ns"` // t5 - t0: spawn to process exit
	ToolCalls           []ToolCallEvent `json:"tool_calls"`
	ToolDurationTotal   time.Duration   `json:"tool_duration_total_ns"`
	PeakRSSBytes        int64           `json:"peak_rss_bytes"`
	ExitCode            int             `json:"exit_code"`
	Success             bool            `json:"success"`
	ErrorMessage        string          `json:"error_message,omitempty"`
	InputTokens         int64           `json:"input_tokens"`
	OutputTokens        int64           `json:"output_tokens"`
	CacheReadTokens     int64           `json:"cache_read_tokens"`
	CacheCreationTokens int64           `json:"cache_creation_tokens"`
	ThinkingTokens      int64           `json:"thinking_tokens"`
	DurationApiMs       int64           `json:"duration_api_ms"`
	DurationMs          int64           `json:"duration_ms"`
	TTFTMs              int64           `json:"ttft_ms"`
	CostUSD             float64         `json:"cost_usd"`
	ResultText          string          `json:"result_text,omitempty"`
}

// ScenarioStats holds statistical aggregates for a scenario/lane/effort slice.
type ScenarioStats struct {
	Scenario           ScenarioID  `json:"scenario"`
	Lane               Lane        `json:"lane"`
	Effort             EffortLevel `json:"effort"`
	SampleCount        int         `json:"sample_count"`
	MeanTotalMs        float64     `json:"mean_total_ms"`
	P50TotalMs         float64     `json:"p50_total_ms"`
	P95TotalMs         float64     `json:"p95_total_ms"`
	StdDevTotalMs      float64     `json:"std_dev_total_ms"`
	MeanTTFTMs         float64     `json:"mean_ttft_ms"`
	MeanSpawnMs        float64     `json:"mean_spawn_ms"`
	MeanToolMs         float64     `json:"mean_tool_ms"`
	MeanThinkingTokens float64     `json:"mean_thinking_tokens"`
	MeanPeakRSSMB      float64     `json:"mean_peak_rss_mb"`
	MeanCostUSD        float64     `json:"mean_cost_usd"`
}

// BenchmarkReport encapsulates full benchmark output and findings.
type BenchmarkReport struct {
	GeneratedAt      time.Time                `json:"generated_at"`
	Environment      string                   `json:"environment"`
	Scenarios        []ScenarioID             `json:"scenarios"`
	Lanes            []Lane                   `json:"lanes"`
	EffortLevels     []EffortLevel            `json:"effort_levels"`
	Runs             []RunTelemetry           `json:"runs"`
	Stats            map[string]ScenarioStats `json:"stats"`
	ExecutiveSummary string                   `json:"executive_summary"`
}

// BenchmarkOptions configures an automated benchmark suite execution.
type BenchmarkOptions struct {
	Scenarios    []ScenarioID  `json:"scenarios"`
	Lanes        []Lane        `json:"lanes"`
	EffortLevels []EffortLevel `json:"effort_levels"`
	Iterations   int           `json:"iterations"`
	Model        string        `json:"model"`
	Timeout      time.Duration `json:"timeout"`
	Mock         bool          `json:"mock"`
	OutputDir    string        `json:"output_dir"`
	Quiet        bool          `json:"quiet"`
}
