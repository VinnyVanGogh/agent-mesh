package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/spf13/cobra"
)

var whereCmd = &cobra.Command{
	Use:     "where",
	Aliases: []string{"pickup"},
	Short:   "Show current active task, working tree status, and immediate next step",
	Run: func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Flags().GetBool("json")
		cwd, _ := os.Getwd()
		gitCtx := meshContext.GatherGitContext(cwd)

		store, err := db.Open(cfg.DBPath)
		var activeTask *meshContext.Task
		if err == nil {
			defer store.Close()
			activeTask, _ = meshContext.GetActiveTaskForRepo(store.DB(), cwd)
		}

		// Read handoff.json snapshot if present
		var handoffRec *meshContext.HandoffRecord
		home, _ := os.UserHomeDir()
		if home != "" {
			handoffFile := filepath.Join(home, ".staypoint", "handoff.json")
			if _, err := os.Stat(handoffFile); os.IsNotExist(err) {
				handoffFile = filepath.Join(home, ".agent-mesh", "handoff.json")
			}
			if data, err := os.ReadFile(handoffFile); err == nil {
				var rec meshContext.HandoffRecord
				if err := json.Unmarshal(data, &rec); err == nil {
					handoffRec = &rec
				}
			}
		}

		taskName := ""
		taskID := ""
		if activeTask != nil {
			taskID = activeTask.ID
			taskName = activeTask.Name
		} else if handoffRec != nil && handoffRec.ActiveTaskName != "" {
			taskID = handoffRec.ActiveTaskID
			taskName = handoffRec.ActiveTaskName
		}

		nextStep := ""
		if handoffRec != nil && handoffRec.ImmediateNextStep != "" {
			nextStep = handoffRec.ImmediateNextStep
		} else if activeTask != nil {
			nextStep = fmt.Sprintf("Continue implementation and verification of [%s]: %s", activeTask.ID, activeTask.Name)
		} else {
			nextStep = "Inspect working tree, run test suite, and proceed with pending implementation."
		}

		if jsonFlag {
			outData := map[string]interface{}{
				"repo_name":      gitCtx.RepoName,
				"repo_path":      gitCtx.RepoRoot,
				"git_branch":     gitCtx.Branch,
				"active_task_id": taskID,
				"active_task":    taskName,
				"next_step":      nextStep,
				"modified_files": gitCtx.ModifiedFiles,
				"recent_commits": gitCtx.RecentCommits,
			}
			enc, _ := json.MarshalIndent(outData, "", "  ")
			fmt.Println(string(enc))
			return
		}

		fmt.Printf("\033[1;36m📍 [Staypoint :: Context Resumption]\033[0m\n")
		fmt.Printf("  • Repository:   \033[1m%s\033[0m (branch: \033[1;33m%s\033[0m)\n", gitCtx.RepoName, gitCtx.Branch)
		if taskName != "" {
			if taskID != "" {
				fmt.Printf("  • Active Task:  \033[1;32m[%s]\033[0m %s\n", taskID, taskName)
			} else {
				fmt.Printf("  • Active Task:  \033[1;32m%s\033[0m\n", taskName)
			}
		} else {
			fmt.Printf("  • Active Task:  (No active task registered in staypoint.db)\n")
		}

		if activeTask != nil && activeTask.IsBlocked {
			fmt.Printf("  • Blocker:      \033[1;31mYES\033[0m (%s)\n", activeTask.BlockReason)
		}
		fmt.Printf("  • Next Step:    \033[1;35m%s\033[0m\n", nextStep)

		if len(gitCtx.ModifiedFiles) > 0 {
			fmt.Printf("  • Working Tree: %d modified files\n", len(gitCtx.ModifiedFiles))
			for i, f := range gitCtx.ModifiedFiles {
				if i < 8 {
					fmt.Printf("      %s\n", f)
				}
			}
			if len(gitCtx.ModifiedFiles) > 8 {
				fmt.Printf("      ... and %d more\n", len(gitCtx.ModifiedFiles)-8)
			}
		} else {
			fmt.Printf("  • Working Tree: Clean (no uncommitted changes)\n")
		}

		if len(gitCtx.RecentCommits) > 0 {
			fmt.Printf("  • Recent Log:\n")
			for _, c := range gitCtx.RecentCommits {
				fmt.Printf("      • %s\n", c)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(whereCmd)
	whereCmd.Flags().BoolP("json", "j", false, "Output context resumption in JSON format")
}
