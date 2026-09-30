package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
	"github.com/spf13/cobra"
)

var queueCmd = &cobra.Command{
	Use:     "queue",
	Aliases: []string{"pq"},
	Short:   "Intelligent priority queue and rate-limit-aware execution pacer",
	Long: `Manages task execution sequencing to maximize quota usage without exceeding
rolling 5-hour rate limits or triggering lockouts.

Features:
  1. Sequential Priority Dispatch:
     Executes tasks in strict priority order (Critical > Urgent > High > Medium > Low).
  2. 5-Hour Rate Limit Pacing:
     Dynamically paces execution when approaching quota ceilings to prevent lockouts.
  3. Anti-Overuse Safeguards:
     Enforces per-task limits, provider ceilings, and runaway loop protection.`,
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List queued runnable tasks in priority order",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer store.Close()

		qm := orchestrator.NewQueueManager(store.DB())
		tasks, err := qm.FetchRunnableTasks(cmd.Context())
		if err != nil {
			return fmt.Errorf("fetch queued tasks: %w", err)
		}

		fmt.Println("\033[1;36m[StayPoint Intelligent Priority Queue]\033[0m")
		if len(tasks) == 0 {
			fmt.Println("  No runnable tasks currently in queue.")
			return nil
		}

		pacer := orchestrator.NewExecutionPacer(store.DB(), orchestrator.DefaultPacerConfig(), cfg)

		for i, t := range tasks {
			badge := formatPriorityBadge(t.Priority)
			assessment, _ := pacer.EvaluatePacing(cmd.Context(), "claude", t.Priority)

			pacingNote := "\033[1;32moptimal\033[0m"
			if assessment != nil {
				switch assessment.State {
				case orchestrator.PacingCaution:
					pacingNote = fmt.Sprintf("\033[1;33mcaution (paced: %v)\033[0m", assessment.PacingDelay.Round(time.Second))
				case orchestrator.PacingThrottled:
					if assessment.CanExecute {
						pacingNote = fmt.Sprintf("\033[1;35mthrottled (priority override, delay: %v)\033[0m", assessment.PacingDelay.Round(time.Second))
					} else {
						pacingNote = "\033[1;31mthrottled (deferred)\033[0m"
					}
				case orchestrator.PacingExhausted:
					pacingNote = "\033[1;31mexhausted / locked\033[0m"
				}
			}

			fmt.Printf("  %d. %s \033[1m%s\033[0m (%s) - pacing: %s\n",
				i+1, badge, t.Name, t.ID, pacingNote)
			if t.MaxBudgetUSD > 0 || t.MaxTurns > 0 {
				fmt.Printf("     Budget: $%.2f/$%.2f | Turns: %d/%d\n",
					t.SpentUSD, t.MaxBudgetUSD, t.SpentTurns, t.MaxTurns)
			}
		}

		return nil
	},
}

var queueStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show queue depth, 5-hour quota headroom, and pacing state",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer store.Close()

		qm := orchestrator.NewQueueManager(store.DB())
		summary, err := qm.GetQueueSummary(cmd.Context())
		if err != nil {
			return fmt.Errorf("queue summary: %w", err)
		}

		pacer := orchestrator.NewExecutionPacer(store.DB(), orchestrator.DefaultPacerConfig(), cfg)

		fmt.Println("\033[1;36m[Priority Queue & Rate Pacer Status]\033[0m")
		fmt.Printf("  • Total Pending Deliverables: %d\n", summary.TotalPending)
		fmt.Printf("  • Breakdown: Critical: %d | Urgent: %d | High: %d | Medium: %d | Low: %d\n",
			summary.ByPriority[orchestrator.PriorityCritical],
			summary.ByPriority[orchestrator.PriorityUrgent],
			summary.ByPriority[orchestrator.PriorityHigh],
			summary.ByPriority[orchestrator.PriorityMedium],
			summary.ByPriority[orchestrator.PriorityLow],
		)
		fmt.Printf("  • Dependency Blocked Tasks:   %d\n", summary.BlockedCount)
		fmt.Printf("  • Active In-Process Claims:   %d\n", summary.ActiveClaims)

		if summary.NextTask != nil {
			fmt.Printf("  • Next Deliverable in Line:   %s [%s] %s\n",
				formatPriorityBadge(summary.NextTask.Priority), summary.NextTask.ID, summary.NextTask.Name)
		}

		fmt.Println("\n\033[1;36m[5-Hour Rolling Rate Limit Headroom]\033[0m")
		for _, prov := range []string{"claude", "gemini", "codex"} {
			assessment, _ := pacer.EvaluatePacing(cmd.Context(), prov, orchestrator.PriorityMedium)
			if assessment != nil {
				stateColor := "\033[1;32m"
				if assessment.State == orchestrator.PacingCaution {
					stateColor = "\033[1;33m"
				} else if assessment.State == orchestrator.PacingThrottled || assessment.State == orchestrator.PacingExhausted {
					stateColor = "\033[1;31m"
				}
				fmt.Printf("  • %-8s: %s[%s]\033[0m 5h Used: %.1f%% (%.1f%% left) | Resets in: %v\n",
					prov, stateColor, strings.ToUpper(string(assessment.State)),
					assessment.FiveHourUsedPct, assessment.FiveHourLeftPct,
					assessment.TimeUntilReset.Round(time.Minute))
			}
		}

		if len(cfg.ProviderCeilings) > 0 {
			fmt.Println("\n\033[1;36m[Configured Anti-Overuse Ceilings]\033[0m")
			for prov, c := range cfg.ProviderCeilings {
				fmt.Printf("  • %-8s: Max spend: $%.2f/hr | Max turns: %d/hr | Max util: %.1f%%\n",
					prov, c.MaxSpendPerHour, c.MaxTurnsPerHour, c.MaxUtilizationPct)
			}
		}

		return nil
	},
}

var queueNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Execute the single highest-priority ready task with rate limit pacing",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runQueueExecution(cmd, 1)
	},
}

var queueRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Drain and execute tasks from the priority queue with rate-limit pacing",
	RunE: func(cmd *cobra.Command, args []string) error {
		maxTasks, _ := cmd.Flags().GetInt("max-tasks")
		return runQueueExecution(cmd, maxTasks)
	},
}

func runQueueExecution(cmd *cobra.Command, maxTasks int) error {
	provider, _ := cmd.Flags().GetString("provider")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	maxTurns, _ := cmd.Flags().GetInt("max-turns")
	maxBudget, _ := cmd.Flags().GetFloat64("max-budget")
	skipPerms, _ := cmd.Flags().GetBool("skip-perms")
	agentID, _ := cmd.Flags().GetString("agent-id")
	repoRoot, _ := cmd.Flags().GetString("repo")

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if repoRoot == "" {
		repoRoot = cfg.WorkRepoRoot
		if wd, err := os.Getwd(); err == nil {
			repoRoot = wd
		}
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer store.Close()

	runner := orchestrator.NewQueueRunner(store.DB(), repoRoot, cfg)

	adapterFn := func(ctx context.Context, cwd, prov string, rawArgs, extraEnv []string, stdout, stderr io.Writer) error {
		if len(extraEnv) > 0 {
			ctx = adapter.WithExtraEnv(ctx, extraEnv)
		}
		return adapter.RunAdapter(ctx, cwd, nil, prov, rawArgs, nil, stdout, stderr)
	}

	opts := orchestrator.RunQueueOptions{
		MaxTasks: maxTasks,
		Provider: provider,
		DryRun:   dryRun,
		RunConfig: orchestrator.RunConfig{
			MaxTurns:        maxTurns,
			MaxBudgetUSD:    maxBudget,
			SkipPermissions: skipPerms,
			AgentID:         agentID,
			RunAdapter:      adapterFn,
		},
		OnTaskStart: func(task *orchestrator.QueuedTask, assessment *orchestrator.PacingAssessment) {
			badge := formatPriorityBadge(task.Priority)
			fmt.Printf("\n\033[1;34m▶ Dispatching %s %s (%s)\033[0m\n", badge, task.Name, task.ID)
			fmt.Printf("  Provider: %s | Pacing: %s\n", assessment.Provider, assessment.Reason)
		},
		OnTaskDone: func(task *orchestrator.QueuedTask, res *orchestrator.RunResult) {
			statusColor := "\033[1;32m"
			if res.Disposition == "capped" || res.Disposition == "in_progress" {
				statusColor = "\033[1;33m"
			}
			fmt.Printf("✔ Completed %s: disposition=%s%s\033[0m (turns: %d, spend: $%.2f)\n",
				task.ID, statusColor, res.Disposition, res.Turns, res.SpentUSD)
			if res.DiagnosticMsg != "" {
				fmt.Printf("  Note: %s\n", res.DiagnosticMsg)
			}
		},
	}

	results, err := runner.RunQueue(cmd.Context(), opts)
	if err != nil {
		return fmt.Errorf("queue execution: %w", err)
	}

	fmt.Printf("\n\033[1;32mDone: Processed %d deliverables from priority queue.\033[0m\n", len(results))
	return nil
}

func formatPriorityBadge(priority string) string {
	switch strings.ToLower(priority) {
	case orchestrator.PriorityCritical:
		return "\033[1;41;37m[CRITICAL]\033[0m"
	case orchestrator.PriorityUrgent:
		return "\033[1;31m[URGENT]\033[0m"
	case orchestrator.PriorityHigh:
		return "\033[1;33m[HIGH]\033[0m"
	case orchestrator.PriorityMedium:
		return "\033[1;34m[MEDIUM]\033[0m"
	case orchestrator.PriorityLow:
		return "\033[0;37m[LOW]\033[0m"
	default:
		return "\033[1;34m[MEDIUM]\033[0m"
	}
}

func init() {
	rootCmd.AddCommand(queueCmd)
	queueCmd.AddCommand(queueListCmd)
	queueCmd.AddCommand(queueStatusCmd)
	queueCmd.AddCommand(queueNextCmd)
	queueCmd.AddCommand(queueRunCmd)

	for _, c := range []*cobra.Command{queueNextCmd, queueRunCmd} {
		c.Flags().String("provider", "", "Target provider override (claude, gemini, codex)")
		c.Flags().Bool("dry-run", false, "Preview priority dispatch and pacing without running adapter")
		c.Flags().Int("max-turns", 50, "Maximum turns allowed per task before capping")
		c.Flags().Float64("max-budget", 0, "Maximum spend in USD per task before capping (0 = unlimited)")
		c.Flags().Bool("skip-perms", false, "Pass --dangerously-skip-permissions to the adapter")
		c.Flags().String("agent-id", "local", "Agent identifier for checkout audit")
		c.Flags().String("repo", "", "Repository root (defaults to current directory)")
	}

	queueRunCmd.Flags().Int("max-tasks", 0, "Maximum number of queued deliverables to process (0 = all)")
}
