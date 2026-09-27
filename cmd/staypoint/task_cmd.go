package main

import (
	"fmt"
	"os"

	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage development tasks and context in staypoint.db",
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
		fmt.Println("\033[1;36m[Staypoint Tasks]\033[0m")
		if len(tasks) == 0 {
			fmt.Println("  No active tasks found.")
			return
		}
		for _, t := range tasks {
			statusColor := "\033[1;32m"
			if t.Status == "done" {
				statusColor = "\033[0;37m"
			}
			budgetInfo := ""
			if t.MaxBudgetUSD > 0 || t.MaxTurns > 0 {
				pct := 0.0
				if t.MaxBudgetUSD > 0 {
					pct = (t.SpentUSD / t.MaxBudgetUSD) * 100.0
				}
				budgetInfo = fmt.Sprintf(" [budget: $%.2f/$%.2f (%.0f%%), %d/%d turns]", t.SpentUSD, t.MaxBudgetUSD, pct, t.SpentTurns, t.MaxTurns)
			}
			fmt.Printf("  • %s[%s]\033[0m \033[1m%s\033[0m (branch: %s, role: %s)%s\n",
				statusColor, t.Status, t.Name, t.GitBranch, t.AccountRole, budgetInfo)
		}
	},
}

var taskAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add a new active task (or dynamic task creation with --tui / --ai)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tuiFlag, _ := cmd.Flags().GetBool("tui")
		aiFlag, _ := cmd.Flags().GetBool("ai")
		if tuiFlag || aiFlag || len(args) == 0 {
			if err := runTaskCreate(cmd, args); err != nil {
				fmt.Fprintf(os.Stderr, "Error creating task: %v\n", err)
				os.Exit(1)
			}
			return
		}

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
		budget, _ := cmd.Flags().GetFloat64("budget")
		maxTurns, _ := cmd.Flags().GetInt("max-turns")
		t, err := meshContext.CreateTaskWithOptions(store.DB(), meshContext.TaskCreateOptions{
			Name:         args[0],
			RepoPath:     cwd,
			GitBranch:    branch,
			AccountRole:  role,
			MaxBudgetUSD: budget,
			MaxTurns:     maxTurns,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding task: %v\n", err)
			os.Exit(1)
		}
		budgetDesc := ""
		if budget > 0 || maxTurns > 0 {
			budgetDesc = fmt.Sprintf(" [budget: $%.2f, max turns: %d]", budget, maxTurns)
		}
		fmt.Printf("\033[1;32m✔ Task created:\033[0m %s (id: %s, role: %s)%s\n", t.Name, t.ID, t.AccountRole, budgetDesc)
	},
}

var taskDoneCmd = &cobra.Command{
	Use:   "done [id|name]",
	Short: "Mark a task as completed",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		if err := meshContext.MarkTaskDone(store.DB(), args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating task: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Task %q marked as done\033[0m\n", args[0])
	},
}

var taskBudgetCmd = &cobra.Command{
	Use:   "budget [id|name]",
	Short: "Set or update dollar/turn budget limits for a task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		budget, _ := cmd.Flags().GetFloat64("usd")
		turns, _ := cmd.Flags().GetInt("turns")
		if err := meshContext.UpdateTaskBudget(store.DB(), args[0], budget, turns); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating budget: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Task %q budget updated to $%.2f USD / %d turns\033[0m\n", args[0], budget, turns)
	},
}

func init() {
	rootCmd.AddCommand(taskCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskAddCmd)
	taskCmd.AddCommand(taskDoneCmd)
	taskCmd.AddCommand(taskBudgetCmd)
	taskListCmd.Flags().BoolP("all", "a", false, "Include done and soft-deleted tasks")
	taskAddCmd.Flags().Float64("budget", 0.0, "Maximum budget limit in USD")
	taskAddCmd.Flags().Int("max-turns", 0, "Maximum allowed turns")
	taskAddCmd.Flags().Bool("tui", false, "Launch Bubble Tea TUI interactive textarea")
	taskAddCmd.Flags().Bool("ai", false, "Force dynamic AI inference")
	taskAddCmd.Flags().Bool("dry-run", false, "Preview generated task without dispatching to Paperclip")
	taskBudgetCmd.Flags().Float64("usd", 0.0, "Budget limit in USD")
	taskBudgetCmd.Flags().Int("turns", 0, "Maximum allowed turns")
}
