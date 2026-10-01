package speedtest

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// EnsureFixtures generates all necessary test fixtures for the 4 benchmark scenarios.
func EnsureFixtures(fixturesDir string) error {
	if err := os.MkdirAll(fixturesDir, 0755); err != nil {
		return fmt.Errorf("create fixtures dir: %w", err)
	}

	// 1. sample_numbers.txt (for Scenario 3: median & std dev computation)
	numbersPath := filepath.Join(fixturesDir, "sample_numbers.txt")
	if _, err := os.Stat(numbersPath); os.IsNotExist(err) {
		var sb strings.Builder
		r := rand.New(rand.NewSource(42))
		for i := 0; i < 100; i++ {
			sb.WriteString(fmt.Sprintf("%d\n", r.Intn(1000)+1))
		}
		if err := os.WriteFile(numbersPath, []byte(sb.String()), 0644); err != nil {
			return fmt.Errorf("write sample_numbers.txt: %w", err)
		}
	}

	// 2. service.go (for Scenario 4: CounterStruct editing & test updating)
	serviceGoPath := filepath.Join(fixturesDir, "service.go")
	if err := writeServiceGoFixture(serviceGoPath); err != nil {
		return err
	}

	// 3. service_test.go
	serviceTestGoPath := filepath.Join(fixturesDir, "service_test.go")
	if err := writeServiceTestGoFixture(serviceTestGoPath); err != nil {
		return err
	}

	// 4. Lean Prompt
	leanPromptPath := filepath.Join(fixturesDir, "lean_prompt.md")
	leanContent := `# Baseline Autonomous Agent Instructions
You are an autonomous engineering agent. Complete the requested task precisely and concisely.
Respond with exact, verified output.`
	if err := os.WriteFile(leanPromptPath, []byte(leanContent), 0644); err != nil {
		return fmt.Errorf("write lean_prompt.md: %w", err)
	}

	// 5. StayPoint Condensed Prompt Diet (~3k tokens)
	staypointPromptPath := filepath.Join(fixturesDir, "staypoint_condensed_prompt.md")
	if err := writeStaypointCondensedPrompt(staypointPromptPath); err != nil {
		return err
	}

	// 6. Paperclip Heavy Prompt (~30k tokens with agent instructions, board contracts, and skill manifests)
	paperclipPromptPath := filepath.Join(fixturesDir, "paperclip_heavy_prompt.md")
	if err := writePaperclipHeavyPrompt(paperclipPromptPath); err != nil {
		return err
	}

	// 7. StayPoint MCP Config
	staypointMcpPath := filepath.Join(fixturesDir, "staypoint_mcp.json")
	staypointBin, _ := os.Executable()
	if !strings.Contains(staypointBin, "staypoint") {
		staypointBin = "/Users/vincevasile/.local/bin/staypoint"
	}
	staypointMcpContent := fmt.Sprintf(`{
  "mcpServers": {
    "staypoint": {
      "command": "%s",
      "args": ["mcp"]
    }
  }
}`, staypointBin)
	if err := os.WriteFile(staypointMcpPath, []byte(staypointMcpContent), 0644); err != nil {
		return fmt.Errorf("write staypoint_mcp.json: %w", err)
	}

	// 8. Paperclip Reconstructed MCP Config
	paperclipMcpPath := filepath.Join(fixturesDir, "paperclip_mcp.json")
	paperclipMcpContent := `{
  "mcpServers": {
    "Paperclip projects": {
      "type": "http",
      "url": "http://127.0.0.1:3100/api/mcp/project-tools",
      "headers": {
        "Authorization": "Bearer mock-paperclip-token"
      }
    }
  }
}`
	if err := os.WriteFile(paperclipMcpPath, []byte(paperclipMcpContent), 0644); err != nil {
		return fmt.Errorf("write paperclip_mcp.json: %w", err)
	}

	return nil
}

// ResetScenarioFixtures restores mutable fixture files to clean state.
func ResetScenarioFixtures(fixturesDir string) error {
	statsJson := filepath.Join(fixturesDir, "stats.json")
	_ = os.Remove(statsJson)

	serviceGoPath := filepath.Join(fixturesDir, "service.go")
	if err := writeServiceGoFixture(serviceGoPath); err != nil {
		return err
	}

	serviceTestGoPath := filepath.Join(fixturesDir, "service_test.go")
	return writeServiceTestGoFixture(serviceTestGoPath)
}

func writeServiceGoFixture(path string) error {
	var sb strings.Builder
	sb.WriteString(`package fixtures

import (
	"sync"
	"time"
)

// CounterStruct tracks event counts across multiple workers.
type CounterStruct struct {
	mu    sync.RWMutex
	count int64
	tags  map[string]int64
}

// NewCounterStruct initializes a new thread-safe counter.
func NewCounterStruct() *CounterStruct {
	return &CounterStruct{
		tags: make(map[string]int64),
	}
}

// Get returns the current counter value.
func (c *CounterStruct) Get() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.count
}

// Reset clears the counter and tags.
func (c *CounterStruct) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = 0
	c.tags = make(map[string]int64)
}

// ServiceMetrics holds high-throughput telemetry counters.
type ServiceMetrics struct {
	StartTime time.Time
	Requests  CounterStruct
	Errors    CounterStruct
}

// NewServiceMetrics initializes service metrics.
func NewServiceMetrics() *ServiceMetrics {
	return &ServiceMetrics{
		StartTime: time.Now(),
	}
}
`)

	// Pad service file with realistic utility structs to simulate a real ~500-line service
	for i := 1; i <= 35; i++ {
		sb.WriteString(fmt.Sprintf(`
// WorkerPoolSegment%d manages chunk %d processing.
type WorkerPoolSegment%d struct {
	SegmentID int
	Active    bool
}

// Process handles segment task execution for batch %d.
func (w *WorkerPoolSegment%d) Process() bool {
	return w.Active
}
`, i, i, i, i, i))
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func writeServiceTestGoFixture(path string) error {
	content := `package fixtures

import (
	"testing"
)

func TestCounterStruct(t *testing.T) {
	c := NewCounterStruct()
	if val := c.Get(); val != 0 {
		t.Fatalf("expected 0, got %d", val)
	}
}
`
	return os.WriteFile(path, []byte(content), 0644)
}

func writeStaypointCondensedPrompt(path string) error {
	content := `# StayPoint Condensed Agent Execution Contract (Diet Mode)
🦴 CAVEMAN: Concise, factual, minimal tokens.
Role: Autonomous Platform Engineer
Engine: StayPoint Native (Sub-15ms Micro-Checkpointing & Quota Pacing)

Guidelines:
- Execute changes with atomic precision.
- Run tests before signaling completion.
- Micro-checkpoint workspace state before major mutations via staypoint_checkpoint.
- Complete task in single heartbeat where possible.
`
	// Pad to ~3k tokens (approx 12KB)
	var sb strings.Builder
	sb.WriteString(content)
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("\n## Guideline Rule %d\nEnsure invariant %d is strictly preserved across all operations. Avoid superfluous comments.\n", i, i))
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func writePaperclipHeavyPrompt(path string) error {
	var sb strings.Builder
	sb.WriteString(`# Chief Technology Officer (CTO) - Paperclip Autonomous Control Plane

You are the CTO of StayPoint. You hold ultimate technical strategy and escalation authority, reporting directly to the Board of Directors.

## Complete Corporate Governance & Multi-Division Directives
`)
	// Emulate Paperclip's massive 30k token prompt bundle:
	// Includes full Base Instructions, Claude instructions, skill manifests, board contracts, and agent hierarchy.
	for i := 1; i <= 150; i++ {
		sb.WriteString(fmt.Sprintf(`
### Enterprise Policy Section %d: Operational Mandates & Verification Gates
All agents executing under Paperclip orchestration must verify preconditions, postconditions, and intermediate state transitions.
1. Rule %d.A: Enforce multi-step verification contracts across every heartbeat.
2. Rule %d.B: Maintain idempotent transaction boundaries in Postgres.
3. Rule %d.C: Record comprehensive audit logs and JSON-RPC dispatch events.
4. Rule %d.D: Retain full conversation history and historical tool execution traces.
5. Rule %d.E: Skills directory manifest: [skill-a%d, skill-b%d, skill-c%d]. Each tool invocation requires strict schema validation.
`, i, i, i, i, i, i, i, i, i))
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}
