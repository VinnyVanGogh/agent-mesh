package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/shipreview"
	"github.com/spf13/cobra"
)

var shipReviewCmd = &cobra.Command{
	Use:   "ship-review",
	Short: "Manage Ship Review cards",
}

var shipReviewCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create or refresh a Ship Review card for Board approval",
	Long: `Create a Ship Review card for the current task. The Board can then
approve (merging the pinned SHA), send back (requesting iteration), or reject the branch.

test_steps must be a JSON array of numbered strings, e.g.:
  '["1. Run go test ./...", "2. Open http://localhost:3000 and verify the dashboard loads"]'`,
	RunE: runShipReviewCreate,
}

var (
	srTaskID     string
	srTestSteps  string
	srDevURL     string
	srCheckRuns  string
)

func init() {
	shipReviewCreateCmd.Flags().StringVar(&srTaskID, "task-id", "", "Task ID (defaults to STAYPOINT_TASK_ID env var)")
	shipReviewCreateCmd.Flags().StringVar(&srTestSteps, "test-steps", "", "JSON array of test instructions (required)")
	shipReviewCreateCmd.Flags().StringVar(&srDevURL, "dev-url", "", "Optional dev server URL (e.g. http://localhost:3000)")
	shipReviewCreateCmd.Flags().StringVar(&srCheckRuns, "check-runs", "", "Optional JSON array of check run results")
	_ = shipReviewCreateCmd.MarkFlagRequired("test-steps")

	shipReviewCmd.AddCommand(shipReviewCreateCmd)
	rootCmd.AddCommand(shipReviewCmd)
}

func runShipReviewCreate(cmd *cobra.Command, _ []string) error {
	taskID := srTaskID
	if taskID == "" {
		taskID = os.Getenv("STAYPOINT_TASK_ID")
	}
	if taskID == "" {
		return fmt.Errorf("--task-id or STAYPOINT_TASK_ID is required")
	}

	var testSteps []string
	if err := json.Unmarshal([]byte(srTestSteps), &testSteps); err != nil {
		return fmt.Errorf("--test-steps must be a JSON array of strings: %w", err)
	}
	if len(testSteps) == 0 {
		return fmt.Errorf("--test-steps must not be empty")
	}

	var checkRuns []shipreview.CheckRun
	if srCheckRuns != "" {
		if err := json.Unmarshal([]byte(srCheckRuns), &checkRuns); err != nil {
			return fmt.Errorf("--check-runs must be a JSON array: %w", err)
		}
	}

	if srDevURL != "" {
		if err := shipreview.ValidateDevURL(srDevURL); err != nil {
			return fmt.Errorf("--dev-url invalid: %w", err)
		}
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer store.Close()
	dbConn := store.DB()

	task, err := context.GetTask(dbConn, taskID)
	if err != nil {
		return fmt.Errorf("task not found: %w", err)
	}

	// BuildAndStartCard always pins staypoint/<taskID>, never task.GitBranch
	// (which is the repo's branch at creation time, usually "main").
	card, err := shipreview.BuildAndStartCard(cmd.Context(), dbConn, task.ID, task.RepoPath, testSteps, srDevURL, checkRuns)
	if err != nil {
		return fmt.Errorf("create card: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(card)
	fmt.Fprintf(os.Stderr, "Ship Review card created (status: %s, branch: %s, sha: %s)\n",
		card.Status, card.Branch, card.HeadSHA[:min(12, len(card.HeadSHA))])
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
