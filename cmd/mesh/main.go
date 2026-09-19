package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vincevasile/agent-mesh/internal/bridge"
	"github.com/vincevasile/agent-mesh/internal/config"
	meshContext "github.com/vincevasile/agent-mesh/internal/context"
	"github.com/vincevasile/agent-mesh/internal/db"
	"github.com/vincevasile/agent-mesh/internal/reporting"
	"github.com/vincevasile/agent-mesh/internal/router"
)

var (
	cfg     *config.Config
	rootCmd = &cobra.Command{
		Use:   "mesh",
		Short: "Agent-Mesh: Autonomous AI Agent Ops, Quota Pacing & Context Platform",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			cfg, err = config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return nil
		},
	}
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display real-time quota meters, active task context, and routing recommendations",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("\033[1;36m[Agent-Mesh :: Fleet Status & Pacing Engine]\033[0m")
		fmt.Printf("  • Time:                    %s\n", time.Now().Format("03:04 PM MST"))

		pacerState, _ := router.LoadPacerState()
		cwd, _ := os.Getwd()
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		remoteHost := "mansol-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			remoteHost = cfg.RemoteHost
		}

		decision, _ := router.Route(ctx, cwd, pacerState, router.RouteOptions{
			CheckSSH:   false,
			RemoteHost: remoteHost,
		})

		if pacerState != nil {
			workPool := pacerState.Pools[router.PoolWorkClaude]
			persPool := pacerState.Pools[router.PoolPersonalClaude]
			geminiPool := pacerState.Pools[router.PoolGeminiNative]
			pool3p := pacerState.Pools[router.Pool3PClaude]

			if workPool != nil {
				workName := "Managed Solution (Work)"
				if cfg != nil && cfg.CompanyName != "" {
					workName = fmt.Sprintf("%s (Work)", cfg.CompanyName)
				}
				fmt.Printf("  • %-26s \033[1;32m✔ Highest Priority\033[0m (routes via %s | Week Left: %.0f%% | 5h Left: %.0f%%)\n",
					workName+":", remoteHost, workPool.Weekly.RemainingPct, workPool.FiveHour.RemainingPct)
			}
			if persPool != nil {
				persColor := "\033[1;32m✔ Available\033[0m"
				if persPool.IsLocked {
					persColor = "\033[1;31m✖ Locked\033[0m"
				} else if persPool.Weekly.RemainingPct < 10 {
					persColor = fmt.Sprintf("\033[1;33m⚠ %.0f%% Used\033[0m", persPool.Weekly.UsedPct)
				}
				fmt.Printf("  • Personal Claude Code:    %s | Week Left: %.0f%% | 5h Left: %.0f%%\n",
					persColor, persPool.Weekly.RemainingPct, persPool.FiveHour.RemainingPct)
			}
			if geminiPool != nil {
				gemColor := "\033[1;32m✔ Available\033[0m"
				if geminiPool.IsLocked {
					gemColor = "\033[1;31m✖ Locked\033[0m"
				}
				fmt.Printf("  • Gemini Native Quota:     %s | Week Left: %.1f%% | 5h Left: %.1f%%\n",
					gemColor, geminiPool.Weekly.RemainingPct, geminiPool.FiveHour.RemainingPct)
			}
			if pool3p != nil {
				p3pColor := "\033[1;32m✔ Available\033[0m"
				if pool3p.IsLocked || pool3p.Weekly.RemainingPct <= 0 {
					p3pColor = "\033[1;31m✖ Locked (0%)\033[0m"
				}
				fmt.Printf("  • Claude / 3P Quota:       %s | Week Left: %.1f%%\n",
					p3pColor, pool3p.Weekly.RemainingPct)
			}
		}

		if decision != nil {
			fmt.Printf("  • Recommended Route:       \033[1;32m%s\033[0m (via \033[1m%s\033[0m in %s)\n",
				decision.Model, decision.Tool, decision.Workspace)
		}
		if cfg != nil {
			fmt.Printf("  • Database:                %s (WAL Active)\n", cfg.DBPath)
		}
	},
}

var routeCmd = &cobra.Command{
	Use:   "route [cwd]",
	Short: "Dynamic routing recommendation and quota-aware dispatch engine",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		evalFlag, _ := cmd.Flags().GetBool("eval")
		jsonFlag, _ := cmd.Flags().GetBool("json")
		noSSH, _ := cmd.Flags().GetBool("no-ssh")

		cwd := ""
		if len(args) > 0 {
			cwd = args[0]
		}
		if cwd == "" {
			cwd, _ = os.Getwd()
		}

		remoteHost := "mansol-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			remoteHost = cfg.RemoteHost
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		pacerState, err := router.LoadPacerState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading pacer state: %v\n", err)
			os.Exit(1)
		}

		decision, err := router.Route(ctx, cwd, pacerState, router.RouteOptions{
			CheckSSH:   !noSSH,
			RemoteHost: remoteHost,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Routing error: %v\n", err)
			os.Exit(1)
		}

		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(decision)
			return
		}

		if evalFlag {
			fmt.Printf("export MESH_ROUTE_TARGET=%q\n", decision.Target)
			fmt.Printf("export MESH_ROUTE_TOOL=%q\n", decision.Tool)
			fmt.Printf("export MESH_ROUTE_MODEL=%q\n", decision.Model)
			fmt.Printf("export MESH_ROUTE_COMMAND=%q\n", decision.Command)
			fmt.Printf("export MESH_ROUTE_WORKSPACE=%q\n", decision.Workspace)
			fmt.Printf("export MESH_ROUTE_ACCOUNT=%q\n", decision.AccountRole)
			fmt.Printf("export MESH_ROUTE_EMAIL=%q\n", decision.AccountEmail)
			fmt.Printf("export MESH_ROUTE_IS_WORK=%t\n", decision.IsWorkRepo)
			fmt.Printf("export MESH_ROUTE_REASON=%q\n", decision.Reason)
			return
		}

		// Human-readable output
		fmt.Println("\033[1;36m[Agent-Mesh :: Dynamic Router]\033[0m")
		fmt.Printf("  • Workspace:          %s\n", decision.Workspace)
		if decision.IsWorkRepo {
			fmt.Printf("  • Context Type:       \033[1;32mManaged Solution Work Repo\033[0m (%s)\n", decision.WorkRepoSource)
			if decision.SSHReachable {
				fmt.Printf("  • Node Reachability:  \033[1;32m✔ %s is reachable via SSH\033[0m\n", decision.RemoteHost)
			} else if !noSSH {
				fmt.Printf("  • Node Reachability:  \033[1;31m✖ %s unreachable via SSH\033[0m (using local fallback)\n", decision.RemoteHost)
			}
		} else {
			fmt.Printf("  • Context Type:       \033[1;34mPersonal Development\033[0m\n")
		}

		fmt.Printf("  • Recommended Target: \033[1;32m%s\033[0m (tool: \033[1m%s\033[0m, model: \033[1m%s\033[0m)\n",
			decision.Target, decision.Tool, decision.Model)
		fmt.Printf("  • Dispatch Command:   \033[1;33m%s\033[0m\n", decision.Command)
		fmt.Printf("  • Routing Rationale:  %s\n", decision.Reason)

		if len(decision.Warnings) > 0 {
			for _, w := range decision.Warnings {
				fmt.Printf("  • \033[1;33m⚠ Warning:\033[0m           %s\n", w)
			}
		}

		// Quota Headroom breakdown
		if pacerState != nil {
			fmt.Println("\n\033[1m[Quota Headroom & Turns Runway]\033[0m")
			for _, poolID := range []router.PoolID{router.PoolGeminiNative, router.PoolWorkClaude, router.PoolPersonalClaude, router.Pool3PClaude} {
				pool := pacerState.Pools[poolID]
				if pool == nil {
					continue
				}
				statusIcon := "\033[1;32m✔\033[0m"
				if pool.IsLocked {
					statusIcon = "\033[1;31m🔒\033[0m"
				} else if pool.Weekly.RemainingPct < 10 {
					statusIcon = "\033[1;33m⚠\033[0m"
				}
				fmt.Printf("  %s %-18s | 5h Left: %5.1f%% | Week Left: %5.1f%% | Runway: %3d turns",
					statusIcon, pool.Name, pool.FiveHour.RemainingPct, pool.Weekly.RemainingPct, pool.TurnsRunway)
				if pool.IsLocked {
					fmt.Printf(" (resets @%s)", pool.LockoutUntil.Format("03:04pm"))
				}
				fmt.Println()
			}
		}
	},
}

var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Instantaneous Tokyo Night statusline generator (<5ms)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := router.RenderStatusline(os.Stdout, os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "statusline error: %v\n", err)
			os.Exit(1)
		}
	},
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate executive ROI briefings, value audits, and multi-AI reports",
	Run: func(cmd *cobra.Command, args []string) {
		pdfFlag, _ := cmd.Flags().GetBool("pdf")
		reportType, _ := cmd.Flags().GetString("type")
		outFlag, _ := cmd.Flags().GetString("output")

		if !pdfFlag {
			fmt.Println("Usage: mesh report --pdf [--type work|personal|gemini|combined] [--output <path>]")
			fmt.Println("  --type work        Executive Justification Memo (Boss Card)")
			fmt.Println("  --type personal    Personal Claude Code Value Audit (102k+ turns, $7,500+ value)")
			fmt.Println("  --type gemini      Antigravity & Gemini Native Report (Flash, Pro, Brain logs, Reviews)")
			fmt.Println("  --type combined    Unified Multi-AI Fleet Executive Report ($14,000+ total value)")
			return
		}

		if reportType == "" {
			reportType = "work"
		}

		home, _ := os.UserHomeDir()
		if outFlag == "" {
			switch strings.ToLower(reportType) {
			case "personal":
				outFlag = filepath.Join(home, "Desktop", "claude-code-personal-value-audit.pdf")
			case "gemini", "antigravity":
				outFlag = filepath.Join(home, "Desktop", "antigravity-gemini-native-report.pdf")
			case "combined", "fleet":
				outFlag = filepath.Join(home, "Desktop", "multi-ai-fleet-executive-report.pdf")
			default:
				if cfg != nil && cfg.CompanyName != "" {
					slug := strings.ToLower(strings.ReplaceAll(cfg.CompanyName, " ", "-"))
					outFlag = filepath.Join(home, "Desktop", fmt.Sprintf("%s-ai-justification.pdf", slug))
				} else {
					outFlag = filepath.Join(home, "Desktop", "managed-solution-ai-justification.pdf")
				}
			}
		}

		fmt.Printf("\033[1;36m[Agent-Mesh]\033[0m Rendering \033[1;33m%s\033[0m report via Chrome CDP...\n", reportType)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		if err := reporting.RenderReport(ctx, reportType, cfg, outFlag); err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31mError generating PDF:\033[0m %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Successfully generated PDF:\033[0m %s\n", outFlag)
		_ = exec.Command("open", outFlag).Start()
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize agent-mesh directories and SQLite storage engine",
	Run: func(cmd *cobra.Command, args []string) {
		if err := config.EnsureDataDir(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating data dir: %v\n", err)
			os.Exit(1)
		}

		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing mesh.db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		fmt.Printf("\033[1;32m✔ Agent-Mesh initialized at: %s\033[0m\n", cfg.DataDir)
		fmt.Printf("✔ SQLite database active with WAL mode: %s\n", cfg.DBPath)
	},
}

var bridgeCmd = &cobra.Command{
	Use:   "bridge",
	Short: "Work bridge and remote host connectivity manager",
}

var bridgeCheckCmd = &cobra.Command{
	Use:   "check [dir]",
	Short: "Check whether directory is a work repo and probe remote node",
	Run: func(cmd *cobra.Command, args []string) {
		targetDir := "."
		if len(args) > 0 {
			targetDir = args[0]
		}
		host := "mansol-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		res, err := bridge.Check(ctx, targetDir, host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bridge check error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;36m[Agent-Mesh WorkBridge]\033[0m\n")
		fmt.Printf("  • Local Path:   %s\n", res.LocalPath)
		fmt.Printf("  • Is Work Repo: %v\n", res.IsWorkRepo)
		fmt.Printf("  • Remote Host:  %s\n", res.RemoteHost)
		fmt.Printf("  • Remote Path:  %s\n", res.RemotePath)
		if res.Probe.Reachable {
			fmt.Printf("  • SSH Status:   \033[1;32m✔ Online\033[0m (%v latency)\n", res.Probe.Latency)
			fmt.Printf("  • Decision:     \033[1;32mRoute to remote session\033[0m\n")
		} else {
			fmt.Printf("  • SSH Status:   \033[1;33m✖ Offline\033[0m (%s)\n", res.Probe.Error)
			fmt.Printf("  • Decision:     \033[1;33mFallback to local work session\033[0m\n")
		}
	},
}

var bridgeLaunchCmd = &cobra.Command{
	Use:   "launch [dir] [args...]",
	Short: "Launch remote Claude Code session with local fallback",
	Run: func(cmd *cobra.Command, args []string) {
		targetDir := "."
		var passArgs []string
		if len(args) > 0 {
			targetDir = args[0]
			passArgs = args[1:]
		}
		host := "mansol-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}
		ctx := context.Background()
		err := bridge.Launch(ctx, bridge.LaunchOptions{
			TargetDir: targetDir,
			Host:      host,
			Args:      passArgs,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bridge launch error: %v\n", err)
			os.Exit(1)
		}
	},
}

var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "Synthesize zero-clarification handoff prompt and copy to clipboard",
	Run: func(cmd *cobra.Command, args []string) {
		targetModel, _ := cmd.Flags().GetString("to")
		nextStep, _ := cmd.Flags().GetString("step")
		cwd, _ := os.Getwd()
		record, err := meshContext.GenerateHandoff(meshContext.HandoffOptions{
			Directory:         cwd,
			TargetModel:       targetModel,
			ImmediateNextStep: nextStep,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Handoff error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Continuation prompt copied to clipboard!\033[0m Press Cmd+V in session.\n")
		fmt.Printf("  • Target Model: \033[1;36m%s\033[0m\n", record.TargetModel)
		fmt.Printf("  • Project:      %s (branch: %s)\n", record.RepoName, record.GitBranch)
		fmt.Printf("  • Files:        %d modified\n", len(record.ModifiedFiles))
		fmt.Printf("  • State saved:  ~/.agent-mesh/handoff.json\n")
		fmt.Printf("  • Prompt saved: /tmp/ai-handoff.md\n")
	},
}

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage development tasks and context in mesh.db",
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active tasks",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		all, _ := cmd.Flags().GetBool("all")
		tasks, err := meshContext.ListTasks(store.DB(), all)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing tasks: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("\033[1;36m[Agent-Mesh Tasks]\033[0m")
		if len(tasks) == 0 {
			fmt.Println("  No active tasks found.")
			return
		}
		for _, t := range tasks {
			statusColor := "\033[1;32m"
			if t.Status == "done" {
				statusColor = "\033[0;37m"
			}
			fmt.Printf("  • %s[%s]\033[0m \033[1m%s\033[0m (branch: %s, role: %s)\n",
				statusColor, t.Status, t.Name, t.GitBranch, t.AccountRole)
		}
	},
}

var taskAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new active task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		cwd, _ := os.Getwd()
		branch := meshContext.GetCurrentGitBranch(cwd)
		role := "personal"
		if bridge.IsWorkRepo(cwd) {
			role = "work"
		}
		t, err := meshContext.CreateTask(store.DB(), args[0], cwd, branch, role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Task created:\033[0m %s (id: %s, role: %s)\n", t.Name, t.ID, t.AccountRole)
	},
}

func init() {
	cfg = config.DefaultConfig()
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(routeCmd)
	rootCmd.AddCommand(statuslineCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(bridgeCmd)
	rootCmd.AddCommand(handoffCmd)
	rootCmd.AddCommand(taskCmd)
	rootCmd.AddCommand(initCmd)

	bridgeCmd.AddCommand(bridgeCheckCmd)
	bridgeCmd.AddCommand(bridgeLaunchCmd)

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskAddCmd)
	taskListCmd.Flags().BoolP("all", "a", false, "Include done and soft-deleted tasks")

	handoffCmd.Flags().String("to", "gemini", "Target model family (gemini or claude)")
	handoffCmd.Flags().String("step", "", "Immediate next step description")

	routeCmd.Flags().BoolP("eval", "e", false, "Output recommendation as shell environment variables for eval")
	routeCmd.Flags().BoolP("json", "j", false, "Output recommendation in JSON format")
	routeCmd.Flags().Bool("no-ssh", false, "Skip SSH connectivity probe for work repo routing")

	reportCmd.Flags().Bool("pdf", false, "Generate print-ready PDF report")
	reportCmd.Flags().StringP("type", "t", "work", "Report type: work, personal, gemini, combined (default: work)")
	reportCmd.Flags().StringP("output", "o", "", "Destination path for generated PDF")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
