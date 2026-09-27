package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/checkpoint"
	"github.com/spf13/cobra"
)

var checkpointCmd = &cobra.Command{
	Use:   "checkpoint [message]",
	Short: "Create an ephemeral micro-checkpoint of working tree without moving HEAD",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		msg := ""
		if len(args) > 0 {
			msg = strings.Join(args, " ")
		}
		sessID, _ := cmd.Flags().GetString("session")
		jsonFlag, _ := cmd.Flags().GetBool("json")

		cp, err := checkpoint.CreateCheckpoint(cmd.Context(), checkpoint.CreateOptions{
			WorkDir:   cwd,
			SessionID: sessID,
			Message:   msg,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Checkpoint failed:\033[0m %v\n", err)
			os.Exit(1)
		}

		if jsonFlag {
			enc, _ := json.MarshalIndent(cp, "", "  ")
			fmt.Println(string(enc))
			return
		}

		fmt.Printf("\033[1;32m✔ Micro-checkpoint created in %s:\033[0m \033[1m%s\033[0m\n", cp.Duration.Round(time.Millisecond), cp.ID)
		fmt.Printf("  • Commit:  %s\n", cp.CommitSHA[:10])
		fmt.Printf("  • Ref:     %s\n", cp.Ref)
		if cp.Message != "" {
			fmt.Printf("  • Message: %s\n", cp.Message)
		}
		fmt.Printf("  • Restore: run \033[1;36mstaypoint undo %s\033[0m or \033[1;36mstaypoint undo\033[0m anytime.\n", cp.ID)
	},
}

var checkpointMigrateCmd = &cobra.Command{
	Use:   "migrate-legacy-refs",
	Short: "Migrate legacy git refs from refs/mesh/checkpoints to refs/staypoint/checkpoints",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		count, err := checkpoint.MigrateLegacyRefs(cmd.Context(), cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Migration failed:\033[0m %v\n", err)
			os.Exit(1)
		}
		if count == 0 {
			fmt.Println("No legacy refs/mesh/checkpoints found to migrate.")
			return
		}
		fmt.Printf("\033[1;32m✔ Migrated %d legacy checkpoint refs to refs/staypoint/checkpoints/\033[0m\n", count)
	},
}

var undoCmd = &cobra.Command{
	Use:   "undo [checkpoint-id]",
	Short: "Restore working tree to prior micro-checkpoint (safe pre-undo snapshot taken automatically)",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		cpID := ""
		if len(args) > 0 {
			cpID = args[0]
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		keepUntracked, _ := cmd.Flags().GetBool("keep-untracked")
		cleanIgnored, _ := cmd.Flags().GetBool("clean-ignored")

		res, err := checkpoint.Undo(cmd.Context(), checkpoint.UndoOptions{
			WorkDir:       cwd,
			CheckpointID:  cpID,
			DryRun:        dryRun,
			KeepUntracked: keepUntracked,
			CleanIgnored:  cleanIgnored,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Undo failed:\033[0m %v\n", err)
			os.Exit(1)
		}

		if dryRun {
			fmt.Printf("\033[1;33m[Dry Run] Preview of undo to %s:\033[0m\n", res.RestoredTo.CommitSHA[:10])
			if len(res.FilesReverted) > 0 {
				fmt.Printf("  • Reverted files (%d):\n", len(res.FilesReverted))
				for _, f := range res.FilesReverted {
					fmt.Printf("      M %s\n", f)
				}
			}
			if len(res.FilesRemoved) > 0 {
				fmt.Printf("  • Removed untracked files (%d):\n", len(res.FilesRemoved))
				for _, f := range res.FilesRemoved {
					fmt.Printf("      D %s\n", f)
				}
			}
			if len(res.FilesIgnoredRemoved) > 0 {
				fmt.Printf("  • Removed ignored files (%d):\n", len(res.FilesIgnoredRemoved))
				for _, f := range res.FilesIgnoredRemoved {
					fmt.Printf("      ! %s\n", f)
				}
			}
			return
		}

		fmt.Printf("\033[1;32m✔ Working tree rolled back to checkpoint:\033[0m \033[1m%s\033[0m (%s)\n", res.RestoredTo.ID, res.RestoredTo.CommitSHA[:10])
		if res.SafetyCP != nil {
			fmt.Printf("  • Safety snapshot saved: %s (run \033[1;36mstaypoint redo\033[0m to reverse)\n", res.SafetyCP.ID)
		}
		if len(res.FilesReverted) > 0 {
			fmt.Printf("  • Reverted: %d files\n", len(res.FilesReverted))
		}
		if len(res.FilesRemoved) > 0 {
			fmt.Printf("  • Cleaned:  %d untracked files\n", len(res.FilesRemoved))
		}
		if len(res.FilesIgnoredRemoved) > 0 {
			fmt.Printf("  • Cleaned:  %d ignored files\n", len(res.FilesIgnoredRemoved))
		}
	},
}

var redoCmd = &cobra.Command{
	Use:   "redo",
	Short: "Reverse the previous undo operation using the pre-undo safety snapshot",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		res, err := checkpoint.Redo(cmd.Context(), cwd, "redo")
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Redo failed:\033[0m %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Working tree restored via redo snapshot!\033[0m\n")
		if len(res.FilesReverted) > 0 {
			fmt.Printf("  • Restored: %d files\n", len(res.FilesReverted))
		}
	},
}

var checkpointsListCmd = &cobra.Command{
	Use:     "checkpoints",
	Aliases: []string{"cps"},
	Short:   "List ephemeral micro-checkpoints for current repository",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		cps, err := checkpoint.ListCheckpoints(cmd.Context(), cwd, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing checkpoints: %v\n", err)
			os.Exit(1)
		}
		if len(cps) == 0 {
			fmt.Println("No checkpoints found. Create one with `staypoint checkpoint`.")
			return
		}
		fmt.Printf("\033[1;36m📍 [Staypoint :: Micro-Checkpoints (Time Machine)]\033[0m\n")
		for _, cp := range cps {
			fmt.Printf("  • \033[1;32m%s\033[0m (%s) - %s\n", cp.ID, cp.CommitSHA[:10], cp.Message)
		}
	},
}

func init() {
	rootCmd.AddCommand(checkpointCmd)
	rootCmd.AddCommand(undoCmd)
	rootCmd.AddCommand(redoCmd)
	rootCmd.AddCommand(checkpointsListCmd)
	rootCmd.AddCommand(checkpointMigrateCmd)

	checkpointCmd.Flags().StringP("session", "s", "", "Agent session ID")
	checkpointCmd.Flags().BoolP("json", "j", false, "Output checkpoint metadata as JSON")
	undoCmd.Flags().BoolP("dry-run", "n", false, "Preview files to be reverted without changing disk")
	undoCmd.Flags().BoolP("keep-untracked", "k", false, "Do not delete untracked files created after checkpoint")
	undoCmd.Flags().Bool("clean-ignored", false, "Remove untracked ignored files and directories")
}
