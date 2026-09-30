package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/adapter"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/orchestrator"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <task-id>",
	Short: "Run a task autonomously in an isolated worktree (one agent at a time)",
	Long: `Claim the given task and drive it to completion unattended.

The harness:
  1. Atomically claims the task (fails if already in_progress or concurrency cap reached).
  2. Creates an isolated git worktree.
  3. Checkpoints before each adapter turn.
  4. Runs the adapter (sanitized env; --dangerously-skip-permissions only with --skip-perms).
  5. Intercepts [[TASK_COMPLETE]] and verifies work products + git sync before transitioning.
  6. Sets disposition (in_review on success; in_progress + diagnostic on failure).
  7. Prunes the worktree on exit.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		maxTurns, _ := cmd.Flags().GetInt("max-turns")
		maxBudget, _ := cmd.Flags().GetFloat64("max-budget")
		maxWall, _ := cmd.Flags().GetDuration("max-wallclock")
		skipPerms, _ := cmd.Flags().GetBool("skip-perms")
		agentID, _ := cmd.Flags().GetString("agent-id")
		provider, _ := cmd.Flags().GetString("provider")
		repoRoot, _ := cmd.Flags().GetString("repo")

		if skipPerms {
			slog.Warn("CAUTION: --skip-perms passed; adapter will skip permission prompts")
		}

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

		h := orchestrator.NewHarness(store.DB(), repoRoot)

		// Wire adapter.RunAdapter as the injected turn runner.
		// The harness package cannot import adapter (import cycle), so the CLI bridges them.
		adapterFn := func(ctx context.Context, cwd, prov string, rawArgs, extraEnv []string, stdout, stderr io.Writer) error {
			// Prepend extraEnv to the OS env for the child process.
			// adapter.RunAdapter reads env from os.Environ(); we pass our sanitized set
			// via ctx so it takes precedence. A thin override context key is cleaner than
			// patching os.Environ which is process-global.
			//
			// Inject sanitized extraEnv via context so RunAdapter forwards it to the child
			// without changing its public signature.
			if len(extraEnv) > 0 {
				ctx = adapter.WithExtraEnv(ctx, extraEnv)
			}
			return adapter.RunAdapter(ctx, cwd, nil, prov, rawArgs, nil, stdout, stderr)
		}

		runCfg := orchestrator.RunConfig{
			MaxTurns:        maxTurns,
			MaxBudgetUSD:    maxBudget,
			MaxWallclock:    maxWall,
			SkipPermissions: skipPerms,
			AgentID:         agentID,
			Provider:        provider,
			RunAdapter:      adapterFn,
		}

		result, err := h.Run(context.Background(), taskID, runCfg)
		if err != nil {
			return fmt.Errorf("run %s: %w", taskID, err)
		}

		fmt.Printf("task %s: disposition=%s turns=%d\n", result.TaskID, result.Disposition, result.Turns)
		if result.DiagnosticMsg != "" {
			fmt.Printf("\n[Interceptor diagnostic injected]\n%s\n", result.DiagnosticMsg)
		}
		if result.DiffStat != "" {
			fmt.Printf("\nDiff stat:\n%s\n", result.DiffStat)
		}
		if result.Disposition == "in_progress" {
			return fmt.Errorf("interceptor blocked completion; task remains in_progress")
		}
		return nil
	},
}

func init() {
	runCmd.Flags().Int("max-turns", 50, "Maximum adapter turns before capping")
	runCmd.Flags().Float64("max-budget", 0, "Maximum spend in USD before capping (0 = unlimited)")
	runCmd.Flags().Duration("max-wallclock", 30*time.Minute, "Maximum wall-clock time before capping")
	runCmd.Flags().Bool("skip-perms", false, "Opt-in: pass --dangerously-skip-permissions to the adapter")
	runCmd.Flags().String("agent-id", "local", "Agent identifier for checkout audit")
	runCmd.Flags().String("provider", "", "Adapter provider override (e.g. claude, gemini)")
	runCmd.Flags().String("repo", "", "Repository root (defaults to cwd)")
	rootCmd.AddCommand(runCmd)
}
