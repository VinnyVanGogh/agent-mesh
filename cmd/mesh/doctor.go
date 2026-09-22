package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/VinnyVanGogh/agent-mesh/internal/doctor"
	"github.com/spf13/cobra"
)

var (
	doctorFast   bool
	doctorJSON   bool
	doctorClaude bool
	doctorGemini bool
	doctorRemote bool
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run comprehensive fleet health diagnostics across local & remote agent environments",
	Long: `Fleet Doctor Engine performs automated diagnostics across 4 critical subsystems:
  1. Agent-Mesh Infrastructure (version parity, SSH probe, reverse tunnel, instruction parity, DB & quota)
  2. Claude Code Health (CLI, doctor check, auth credentials, prompt hooks)
  3. AGY / Gemini Health (config dir, MCP server configs, Google AI Ultra quota, caveman mode)
  4. Remote Node Health (remote Claude settings, prompt hook, mapped scan-repos)

Examples:
  mesh doctor              # full fleet diagnostic
  mesh doctor --fast       # offline check skipping remote network round-trips
  mesh doctor --json       # export structured report for automation
  mesh doctor --claude     # Claude Code health only
  mesh doctor --gemini     # AGY / Gemini health only
  mesh doctor --remote     # Remote node health only`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		opts := doctor.DoctorOptions{
			Fast:       doctorFast,
			ClaudeOnly: doctorClaude,
			GeminiOnly: doctorGemini,
			RemoteOnly: doctorRemote,
			Config:     cfg,
		}

		doc := doctor.NewFleetDoctor(opts)
		doc.SetLocalVersion(version)

		report, err := doc.Run(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Fleet doctor execution error:\033[0m %v\n", err)
			os.Exit(1)
		}

		if doctorJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(report); err != nil {
				fmt.Fprintf(os.Stderr, "\033[1;31m✖ JSON encode error:\033[0m %v\n", err)
				os.Exit(1)
			}
			return
		}

		fmt.Print(doc.FormatReport(report))
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFast, "fast", false, "Skip remote network probes for rapid offline check")
	doctorCmd.Flags().BoolVarP(&doctorJSON, "json", "j", false, "Output diagnostic report in JSON format")
	doctorCmd.Flags().BoolVarP(&doctorClaude, "claude", "c", false, "Run Claude Code health checks only")
	doctorCmd.Flags().BoolVarP(&doctorGemini, "gemini", "g", false, "Run AGY / Gemini health checks only")
	doctorCmd.Flags().BoolVarP(&doctorRemote, "remote", "r", false, "Run Remote Node health checks only")

	rootCmd.AddCommand(doctorCmd)
}
