package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage development tasks and context in staypoint.db and Paperclip",
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all active tasks (Paperclip and local Staypoint)",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pclipClient := paperclip.NewClient("", "")
		var pclipIssues []paperclip.IssueResponse

		// Try to fetch active issues from Paperclip
		companyID := os.Getenv("PAPERCLIP_COMPANY_ID")
		if companyID != "" {
			if issues, err := pclipClient.ListActiveIssues(ctx, companyID); err == nil {
				pclipIssues = issues
			}
		} else {
			if companies, err := pclipClient.ListCompanies(ctx); err == nil {
				for _, c := range companies {
					if issues, err := pclipClient.ListActiveIssues(ctx, c.ID); err == nil && len(issues) > 0 {
						pclipIssues = append(pclipIssues, issues...)
					}
				}
			}
		}

		if len(pclipIssues) > 0 {
			fmt.Println("\033[1;36m[Paperclip Active Issues]\033[0m")
			for _, iss := range pclipIssues {
				statusColor := "\033[1;32m"
				if iss.Status == "done" || iss.Status == "cancelled" {
					statusColor = "\033[0;37m"
				} else if iss.Status == "in_progress" {
					statusColor = "\033[1;33m"
				}
				fmt.Printf("  • %s[%s]\033[0m \033[1m%s\033[0m (%s, priority: %s)\n",
					statusColor, iss.Identifier, iss.Title, iss.Status, iss.Priority)
			}
			fmt.Println()
		}

		fmt.Println("\033[1;36m[Staypoint Tasks]\033[0m")
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			if len(pclipIssues) == 0 {
				fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
				os.Exit(1)
			}
			return
		}
		defer store.Close()
		all, _ := cmd.Flags().GetBool("all")
		tasks, err := meshContext.ListTasks(store.DB(), all)
		if err != nil {
			if len(pclipIssues) == 0 {
				fmt.Fprintf(os.Stderr, "Error listing tasks: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if len(tasks) == 0 {
			if len(pclipIssues) == 0 {
				fmt.Println("  No active tasks found.")
			} else {
				fmt.Println("  No local mesh tasks.")
			}
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
	Use:     "add [comment]",
	Aliases: []string{"new"},
	Short:   "Create and dispatch a structured engineering task using Gemini & Claude with TUI or CLI input",
	RunE:    runTaskCreate,
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
	taskAddCmd.Flags().Bool("ai", true, "Force dynamic AI inference")
	taskAddCmd.Flags().Bool("dry-run", false, "Preview generated task without dispatching to Paperclip")
	taskAddCmd.Flags().String("company", "", "Target Paperclip company ID (defaults to PAPERCLIP_COMPANY_ID)")
	taskAddCmd.Flags().String("project", "", "Target project ID (defaults to current project)")
	taskAddCmd.Flags().String("priority", "", "Override priority (low, medium, high, urgent)")
	taskBudgetCmd.Flags().Float64("usd", 0.0, "Budget limit in USD")
	taskBudgetCmd.Flags().Int("turns", 0, "Maximum allowed turns")
}

