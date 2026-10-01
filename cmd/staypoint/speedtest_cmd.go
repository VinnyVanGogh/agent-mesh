package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/benchmark/speedtest"
	"github.com/spf13/cobra"
)

var (
	speedtestAll        bool
	speedtestScenarios  string
	speedtestLanes      string
	speedtestEffort     string
	speedtestIterations int
	speedtestMock       bool
	speedtestQuick      bool
	speedtestOutputJSON string
	speedtestOutputMD   string
	speedtestModel      string
	speedtestTimeout    time.Duration
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run automated test suites and benchmarks",
}

var speedCmd = &cobra.Command{
	Use:     "speed",
	Aliases: []string{"benchmark", "speedtest"},
	Short:   "Benchmark StayPoint vs. Paperclip Claude Code execution latency",
	Long: `Automated speed test benchmark comparing Claude Code execution latency
between Paperclip (reconstructed heavy prompt + HTTP MCP) and StayPoint (condensed prompt diet + pure-Go stdio MCP).

Executes across 4 scenarios:
  1. Single Response Latency
  2. Single Tool Call Overhead
  3. Multi-Chain Response (Sequential file read -> compute -> write)
  4. File Editing & Workspace Mutation (with StayPoint micro-checkpoints)`,
	RunE: runSpeedTest,
}

var speedtestCmd = &cobra.Command{
	Use:   "speedtest",
	Short: "Benchmark StayPoint vs. Paperclip Claude Code execution latency (alias for 'test speed')",
	RunE:  runSpeedTest,
}

func runSpeedTest(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	opts := speedtest.BenchmarkOptions{
		Iterations: speedtestIterations,
		Model:      speedtestModel,
		Timeout:    speedtestTimeout,
		Mock:       speedtestMock,
	}

	if speedtestQuick {
		opts.Iterations = 1
	}

	// Parse Scenarios
	if speedtestAll || speedtestScenarios == "all" || speedtestScenarios == "" {
		opts.Scenarios = []speedtest.ScenarioID{
			speedtest.Scenario1SingleResponse,
			speedtest.Scenario2ToolCall,
			speedtest.Scenario3MultiChain,
			speedtest.Scenario4FileMutation,
		}
	} else {
		for _, part := range strings.Split(speedtestScenarios, ",") {
			part = strings.TrimSpace(part)
			switch part {
			case "1", "single", string(speedtest.Scenario1SingleResponse):
				opts.Scenarios = append(opts.Scenarios, speedtest.Scenario1SingleResponse)
			case "2", "tool", string(speedtest.Scenario2ToolCall):
				opts.Scenarios = append(opts.Scenarios, speedtest.Scenario2ToolCall)
			case "3", "multichain", string(speedtest.Scenario3MultiChain):
				opts.Scenarios = append(opts.Scenarios, speedtest.Scenario3MultiChain)
			case "4", "file", string(speedtest.Scenario4FileMutation):
				opts.Scenarios = append(opts.Scenarios, speedtest.Scenario4FileMutation)
			default:
				if n, err := strconv.Atoi(part); err == nil {
					switch n {
					case 1:
						opts.Scenarios = append(opts.Scenarios, speedtest.Scenario1SingleResponse)
					case 2:
						opts.Scenarios = append(opts.Scenarios, speedtest.Scenario2ToolCall)
					case 3:
						opts.Scenarios = append(opts.Scenarios, speedtest.Scenario3MultiChain)
					case 4:
						opts.Scenarios = append(opts.Scenarios, speedtest.Scenario4FileMutation)
					}
				}
			}
		}
	}

	// Parse Lanes
	if speedtestLanes == "" || speedtestLanes == "all" {
		opts.Lanes = []speedtest.Lane{
			speedtest.LaneBaseline,
			speedtest.LanePaperclip,
			speedtest.LaneStayPoint,
		}
	} else {
		for _, part := range strings.Split(speedtestLanes, ",") {
			part = strings.TrimSpace(strings.ToLower(part))
			switch part {
			case "baseline", "base":
				opts.Lanes = append(opts.Lanes, speedtest.LaneBaseline)
			case "paperclip", "pclip":
				opts.Lanes = append(opts.Lanes, speedtest.LanePaperclip)
			case "staypoint", "sp":
				opts.Lanes = append(opts.Lanes, speedtest.LaneStayPoint)
			}
		}
	}

	// Parse Effort
	if speedtestEffort == "" || speedtestEffort == "all" {
		opts.EffortLevels = []speedtest.EffortLevel{
			speedtest.EffortLow,
			speedtest.EffortHigh,
		}
	} else {
		for _, part := range strings.Split(speedtestEffort, ",") {
			part = strings.TrimSpace(strings.ToLower(part))
			switch part {
			case "low", "none":
				opts.EffortLevels = append(opts.EffortLevels, speedtest.EffortLow)
			case "high":
				opts.EffortLevels = append(opts.EffortLevels, speedtest.EffortHigh)
			}
		}
	}

	outputDir := "benchmarks/results"
	opts.OutputDir = outputDir

	fmt.Println("\033[1;35m======================================================================\033[0m")
	fmt.Println("\033[1;35m  🏎️ StayPoint Speed Test Benchmark Harness (STA-200 / STA-201)\033[0m")
	fmt.Println("\033[1;35m======================================================================\033[0m")
	fmt.Printf("• Scenarios:    %d scenarios\n", len(opts.Scenarios))
	fmt.Printf("• Lanes:        %s\n", formatLanes(opts.Lanes))
	fmt.Printf("• Effort:       %s\n", formatEfforts(opts.EffortLevels))
	fmt.Printf("• Iterations:   %d per cell\n", opts.Iterations)
	fmt.Printf("• Mode:         %s\n", map[bool]string{true: "MOCK / Deterministic", false: "LIVE Claude Code"}[opts.Mock])
	fmt.Println("----------------------------------------------------------------------")

	report, err := speedtest.RunMatrix(ctx, opts)
	if err != nil {
		return fmt.Errorf("benchmark execution error: %w", err)
	}

	if speedtestOutputJSON != "" {
		_ = os.MkdirAll(filepath.Dir(speedtestOutputJSON), 0755)
		data, _ := os.ReadFile(filepath.Join(outputDir, "benchmark_report.json"))
		_ = os.WriteFile(speedtestOutputJSON, data, 0644)
	}
	if speedtestOutputMD != "" {
		_ = os.MkdirAll(filepath.Dir(speedtestOutputMD), 0755)
		data, _ := os.ReadFile(filepath.Join(outputDir, "benchmark_report.md"))
		_ = os.WriteFile(speedtestOutputMD, data, 0644)
	}

	fmt.Println("\n" + speedtest.FormatMarkdownReport(report))
	fmt.Println("\033[1;32m✔ Benchmark suite completed successfully.\033[0m")
	fmt.Printf("• Report written to: %s\n", filepath.Join(outputDir, "benchmark_report.md"))
	fmt.Printf("• Raw JSON saved to: %s\n\n", filepath.Join(outputDir, "benchmark_report.json"))

	return nil
}

func formatLanes(lanes []speedtest.Lane) string {
	var names []string
	for _, l := range lanes {
		names = append(names, string(l))
	}
	return strings.Join(names, ", ")
}

func formatEfforts(efforts []speedtest.EffortLevel) string {
	var names []string
	for _, e := range efforts {
		names = append(names, string(e))
	}
	return strings.Join(names, ", ")
}

func init() {
	flags := []struct {
		cmd *cobra.Command
	}{
		{testCmd},
		{speedCmd},
		{speedtestCmd},
	}

	for _, entry := range flags {
		entry.cmd.Flags().BoolVar(&speedtestAll, "all", false, "Execute all 4 benchmark scenarios")
		entry.cmd.Flags().StringVar(&speedtestScenarios, "scenarios", "all", "Comma-separated scenarios: 1,2,3,4")
		entry.cmd.Flags().StringVar(&speedtestLanes, "lanes", "all", "Comma-separated lanes: baseline,paperclip,staypoint")
		entry.cmd.Flags().StringVar(&speedtestEffort, "effort", "all", "Comma-separated effort levels: low,high")
		entry.cmd.Flags().IntVar(&speedtestIterations, "iterations", 1, "Number of runs per scenario/lane/effort")
		entry.cmd.Flags().BoolVar(&speedtestMock, "mock", false, "Run in fast mock mode for offline test verification")
		entry.cmd.Flags().BoolVar(&speedtestQuick, "quick", false, "Quick run (single iteration)")
		entry.cmd.Flags().StringVar(&speedtestOutputJSON, "output-json", "", "Optional destination for JSON report")
		entry.cmd.Flags().StringVar(&speedtestOutputMD, "output-md", "", "Optional destination for Markdown report")
		entry.cmd.Flags().StringVar(&speedtestModel, "model", "", "Model override (e.g. claude-sonnet-4-6)")
		entry.cmd.Flags().DurationVar(&speedtestTimeout, "timeout", 5*time.Minute, "Timeout per execution turn")
	}

	testCmd.AddCommand(speedCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(speedtestCmd)
}
