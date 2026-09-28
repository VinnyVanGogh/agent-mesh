package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/checkpoint"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show differences related to tasks or checkpoints",
}

func runDiffSinceCheckpoint() {
	cwd, _ := os.Getwd()
	ctx := context.Background()

	latest, err := checkpoint.GetLatestCheckpoint(ctx, cwd)
	if err != nil || latest == nil {
		fmt.Fprintf(os.Stderr, "No recent checkpoint found.\n")
		os.Exit(1)
	}

	diffOutput, err := checkpoint.DiffCheckpointFull(ctx, cwd, latest.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting diff: %v\n", err)
		os.Exit(1)
	}

	condensed := condenseDiff(diffOutput)
	fmt.Println(condensed)
}

func condenseDiff(rawDiff string) string {
	lines := strings.Split(rawDiff, "\n")
	var result []string

	for _, line := range lines {
		// Filter out standard diff boilerplate
		if strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}

		// Highlight interface changes
		if strings.HasPrefix(line, "+func") || strings.HasPrefix(line, "-func") ||
			strings.HasPrefix(line, "+type") || strings.HasPrefix(line, "-type") ||
			strings.HasPrefix(line, "+interface") || strings.HasPrefix(line, "-interface") ||
			strings.HasPrefix(line, "+class") || strings.HasPrefix(line, "-class") {
			result = append(result, line)
			continue
		}

		// Keep additions and deletions, but we might want to truncate long blocks
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "diff --git") {
			result = append(result, line)
		}
	}

	// Add a summary
	result = append([]string{"\033[1;36m[Semantic Diff since Micro-checkpoint]\033[0m"}, result...)
	return strings.Join(result, "\n")
}

func init() {
	rootCmd.AddCommand(diffCmd)
	diffCmd.Flags().Bool("since-checkpoint", false, "Generates a semantic, token-condensed diff of changes since the last micro-checkpoint")

	diffCmd.Run = func(cmd *cobra.Command, args []string) {
		since, _ := cmd.Flags().GetBool("since-checkpoint")
		if since {
			runDiffSinceCheckpoint()
			return
		}
		fmt.Println("Use --since-checkpoint to show checkpoint diffs")
	}
}
