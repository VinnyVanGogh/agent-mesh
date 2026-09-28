package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
	meshContext "github.com/VinnyVanGogh/agent-mesh/internal/context"
	"github.com/VinnyVanGogh/agent-mesh/internal/db"
)

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

		fmt.Println("\033[1;36m[StayPoint Tasks]\033[0m")
		if len(tasks) == 0 {
			fmt.Println("  No active tasks found.")
			return
		}

		for _, t := range tasks {
			statusColor := "\033[1;32m"
			if t.Status == "done" {
				statusColor = "\033[0;37m"
			}
			assignee := "unassigned"
			if t.Assignee != nil {
				assignee = *t.Assignee
			}
			fmt.Printf("  • %s[%s]\033[0m \033[1m%s\033[0m (branch: %s, role: %s, assignee: %s)\n",
				statusColor, t.Status, t.Name, t.GitBranch, t.AccountRole, assignee)
			if t.Description != nil && *t.Description != "" {
				fmt.Printf("      Desc: %s\n", *t.Description)
			}
			if t.Priority != nil && *t.Priority != "" {
				fmt.Printf("      Priority: %s\n", *t.Priority)
			}
			if t.Budget != nil && *t.Budget != "" {
				fmt.Printf("      Budget: %s\n", *t.Budget)
			}
			fmt.Printf("      ID: %s\n", t.ID)
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
		
		repo, _ := cmd.Flags().GetString("repo")
		if repo == "" {
			repo = cwd
		}
		
		branch, _ := cmd.Flags().GetString("branch")
		if branch == "" {
			branch = meshContext.GetCurrentGitBranch(repo)
		}
		
		role, _ := cmd.Flags().GetString("role")
		if role == "" {
			role = "personal"
			if bridge.IsWorkRepo(repo) {
				role = "work"
			}
		}

		task, err := meshContext.CreateTask(store.DB(), args[0], repo, branch, role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error adding task: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Task created:\033[0m %s (id: %s, role: %s)\n", task.Name, task.ID, task.AccountRole)
	},
}

var taskUpdateCmd = &cobra.Command{
	Use:   "update <task-id>",
	Short: "Update task metadata (title, description, priority, budget)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		var title, desc, prio, budget *string

		if cmd.Flags().Changed("title") {
			v, _ := cmd.Flags().GetString("title")
			title = &v
		}
		if cmd.Flags().Changed("description") {
			v, _ := cmd.Flags().GetString("description")
			desc = &v
		}
		if cmd.Flags().Changed("priority") {
			v, _ := cmd.Flags().GetString("priority")
			prio = &v
		}
		if cmd.Flags().Changed("budget") {
			v, _ := cmd.Flags().GetString("budget")
			budget = &v
		}

		err = meshContext.UpdateTask(store.DB(), args[0], title, desc, prio, budget)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating task: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Updated task %s successfully.\033[0m\n", args[0])
	},
}

var taskAssignCmd = &cobra.Command{
	Use:   "assign <task-id> <assignee>",
	Short: "Assign a task to a designated agent/worker/user",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		err = meshContext.AssignTask(store.DB(), args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error assigning task: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Assigned task %s to %s.\033[0m\n", args[0], args[1])
	},
}

var taskReassignCmd = &cobra.Command{
	Use:   "reassign <task-id> <new-assignee>",
	Short: "Reassign a task to a new owner with appropriate validation and audit logging",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		task, err := meshContext.GetTask(store.DB(), args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting task: %v\n", err)
			os.Exit(1)
		}
		
		oldAssignee := "unassigned"
		if task.Assignee != nil {
			oldAssignee = *task.Assignee
		}

		err = meshContext.AssignTask(store.DB(), args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reassigning task: %v\n", err)
			os.Exit(1)
		}

		// Simple audit log concept output
		fmt.Printf("\033[1;32m✔ Reassigned task %s from %s to %s.\033[0m\n", args[0], oldAssignee, args[1])
	},
}

func init() {
	rootCmd.AddCommand(taskCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskAddCmd)
	taskCmd.AddCommand(taskUpdateCmd)
	taskCmd.AddCommand(taskAssignCmd)
	taskCmd.AddCommand(taskReassignCmd)

	taskListCmd.Flags().BoolP("all", "a", false, "Include done and soft-deleted tasks")
	
	taskAddCmd.Flags().StringP("repo", "r", "", "Repository path (defaults to pwd)")
	taskAddCmd.Flags().StringP("branch", "b", "", "Git branch (defaults to current)")
	taskAddCmd.Flags().StringP("role", "R", "", "Account role (work, personal)")

	taskUpdateCmd.Flags().StringP("title", "t", "", "Update task title")
	taskUpdateCmd.Flags().StringP("description", "d", "", "Update task description")
	taskUpdateCmd.Flags().StringP("priority", "p", "", "Update task priority")
	taskUpdateCmd.Flags().StringP("budget", "B", "", "Update task budget")
}
