package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage and export token-optimized LLM context payloads",
}

var contextPackCmd = &cobra.Command{
	Use:     "pack",
	Aliases: []string{"export"},
	Short:   "Packages active task objectives, uncommitted git diffs, touched files, and recent wire messages into a compact payload",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		cwd, _ := os.Getwd()

		gitCtx := meshContext.GatherGitContext(cwd)
		activeTask, _ := meshContext.GetActiveTaskForRepo(store.DB(), cwd)
		msgs, _ := wire.List(store.DB(), "global", 5)

		var sb strings.Builder
		sb.WriteString("## StayPoint Active Context\n\n")

		if activeTask != nil {
			sb.WriteString("### Task Objective\n")
			sb.WriteString(fmt.Sprintf("**ID:** %s\n", activeTask.ID))
			sb.WriteString(fmt.Sprintf("**Name:** %s\n", activeTask.Name))
			if activeTask.AccountRole != "" {
				sb.WriteString(fmt.Sprintf("**Role:** %s\n", activeTask.AccountRole))
			}
			sb.WriteString(fmt.Sprintf("**Status:** %s\n", activeTask.Status))
			sb.WriteString("\n")
		}

		sb.WriteString("### Git State\n")
		sb.WriteString(fmt.Sprintf("**Branch:** %s\n", gitCtx.Branch))
		sb.WriteString(fmt.Sprintf("**Modified Files:** %s\n", strings.Join(gitCtx.ModifiedFiles, ", ")))
		if gitCtx.DiffStat != "" {
			sb.WriteString(fmt.Sprintf("**Diff Stat:**\n```\n%s\n```\n", gitCtx.DiffStat))
		}
		sb.WriteString("\n")

		if len(msgs) > 0 {
			sb.WriteString("### Recent Wire Messages\n")
			for _, m := range msgs {
				ts := m.CreatedAt
				if t, err := time.Parse(time.RFC3339Nano, m.CreatedAt); err == nil {
					ts = t.Local().Format("15:04:05")
				}
				sb.WriteString(fmt.Sprintf("- [%s] <%s>: %s\n", ts, m.Author, m.Content))
			}
			sb.WriteString("\n")
		}

		out := sb.String()
		fmt.Println(out)
		_ = meshContext.CopyToClipboard(out)
		fmt.Fprintf(os.Stderr, "\033[1;32m✔ Context packed and copied to clipboard.\033[0m\n")
	},
}

func init() {
	rootCmd.AddCommand(contextCmd)
	contextCmd.AddCommand(contextPackCmd)
}
