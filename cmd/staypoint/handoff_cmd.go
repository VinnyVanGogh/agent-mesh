package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	meshSync "github.com/VinnyVanGogh/staypoint/internal/sync"
)

var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "Synthesize zero-clarification handoff prompt and copy to clipboard",
	Run: func(cmd *cobra.Command, args []string) {
		pullHost, _ := cmd.Flags().GetString("pull")
		pushHost, _ := cmd.Flags().GetString("push")

		// If --pull is requested, fetch handoff from remote machine
		if cmd.Flags().Changed("pull") {
			if pullHost == "" {
				pullHost = cfg.RemoteHost
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			rec, err := meshSync.PullHandoff(ctx, pullHost)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error pulling handoff from %s: %v\n", pullHost, err)
				os.Exit(1)
			}
			_ = rec
			fmt.Printf("\033[1;32m✔ Handoff context pulled from %s and copied to clipboard!\033[0m\n", pullHost)
			fmt.Printf("  • Prompt saved: /tmp/ai-handoff.md\n")
			fmt.Printf("  • Ready to paste (Cmd+V) in current session.\n")
			return
		}

		targetModel, _ := cmd.Flags().GetString("to")
		nextStep, _ := cmd.Flags().GetString("step")
		cwd, _ := os.Getwd()

		var storeDB *sql.DB
		if cfg != nil && cfg.DBPath != "" {
			if store, err := db.Open(cfg.DBPath); err == nil {
				storeDB = store.DB()
				defer store.Close()
			}
		}

		maxKeep := 3
		if cfg != nil && cfg.MaxHandoffsPerRepo > 0 {
			maxKeep = cfg.MaxHandoffsPerRepo
		}
		dataDir := ""
		if cfg != nil {
			dataDir = cfg.DataDir
		}

		record, err := meshContext.GenerateHandoff(meshContext.HandoffOptions{
			Directory:         cwd,
			TargetModel:       targetModel,
			ImmediateNextStep: nextStep,
			DB:                storeDB,
			DataDir:           dataDir,
			MaxKeepPerRepo:    maxKeep,
			Trigger:           "manual",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Handoff error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Continuation prompt copied to clipboard!\033[0m Press Cmd+V in session.\n")
		fmt.Printf("  • Target Model: \033[1;36m%s\033[0m\n", record.TargetModel)
		fmt.Printf("  • Project:      %s (branch: %s)\n", record.RepoName, record.GitBranch)
		fmt.Printf("  • Files:        %d modified\n", len(record.ModifiedFiles))
		fmt.Printf("  • Manifest:     ~/.staypoint/handoffs/<session_id>-manifest.json\n")
		fmt.Printf("  • Prompt saved: /tmp/ai-handoff.md\n")

		// If --push is requested, push to remote host
		if cmd.Flags().Changed("push") {
			if pushHost == "" {
				pushHost = cfg.RemoteHost
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := meshSync.PushHandoff(ctx, pushHost, record); err != nil {
				fmt.Fprintf(os.Stderr, "\033[1;31m✖ Failed to push handoff to %s: %v\033[0m\n", pushHost, err)
			} else {
				fmt.Printf("\033[1;32m✔ Pushed active handoff directly to remote host: %s (remote clipboard armed)!\033[0m\n", pushHost)
			}
		}
	},
}

func renderManifestsTable(manifests []meshContext.HandoffManifest, currentBranch string) {
	if len(manifests) == 0 {
		fmt.Println("  No saved handoffs found.")
		return
	}
	for i, m := range manifests {
		toolBadge := "\033[1;35m🟣 Claude\033[0m"
		if m.AgentType == "gemini" {
			toolBadge = "\033[1;34m🔵 Antigravity\033[0m"
		}
		age := time.Since(m.CreatedAt).Round(time.Minute)
		ageStr := fmt.Sprintf("%v ago", age)
		if age < time.Minute {
			ageStr = "just now"
		}
		shortID := m.SessionID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		title := m.Title
		if title == "" {
			title = m.Goal
		}
		if len(title) > 60 {
			title = title[:60] + "..."
		}
		branchBadge := ""
		if m.GitBranch != "" {
			if currentBranch != "" && m.GitBranch != currentBranch {
				branchBadge = fmt.Sprintf("\033[1;33m[%s (current: %s)]\033[0m", m.GitBranch, currentBranch)
			} else {
				branchBadge = fmt.Sprintf("\033[1;32m[%s]\033[0m", m.GitBranch)
			}
		}
		triggerBadge := fmt.Sprintf("(trigger: %s)", m.Trigger)
		fmt.Printf("  %d. %s  \033[1;33m%s\033[0m  (%s) %s %s\n", i+1, toolBadge, shortID, ageStr, branchBadge, triggerBadge)
		fmt.Printf("     \033[1m%q\033[0m  •  Turns: %d, Directives: %d\n", title, m.TotalUserTurns, m.DirectivesCount)
		fmt.Printf("     File: \033[0;36m%s\033[0m\n\n", m.HandoffFile)
	}
}

var handoffListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List saved session handoffs and manifests for current repo (or all)",
	Run: func(cmd *cobra.Command, args []string) {
		all, _ := cmd.Flags().GetBool("all")
		limit, _ := cmd.Flags().GetInt("limit")
		jsonFlag, _ := cmd.Flags().GetBool("json")
		branchFilter, _ := cmd.Flags().GetString("branch")

		cwd, _ := os.Getwd()
		filter := cwd
		if all {
			filter = ""
		}

		handoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		manifests, err := meshContext.ListManifests(handoffsDir, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing handoffs: %v\n", err)
			os.Exit(1)
		}

		if branchFilter != "" {
			var filtered []meshContext.HandoffManifest
			for _, m := range manifests {
				if m.GitBranch == branchFilter {
					filtered = append(filtered, m)
				}
			}
			manifests = filtered
		}

		if limit > 0 && len(manifests) > limit {
			manifests = manifests[:limit]
		}

		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(manifests)
			return
		}

		currentBranch := meshContext.GetCurrentGitBranch(cwd)
		header := fmt.Sprintf("Saved Handoffs in %s", filepath.Base(cwd))
		if branchFilter != "" {
			header = fmt.Sprintf("Saved Handoffs in %s (branch: %s)", filepath.Base(cwd), branchFilter)
		} else if all {
			header = "All Saved Handoffs"
		}
		fmt.Printf("\n\033[1;36m[Staypoint :: %s]\033[0m\n", header)
		renderManifestsTable(manifests, currentBranch)
	},
}

var handoffSearchCmd = &cobra.Command{
	Use:     "search [query]",
	Aliases: []string{"find"},
	Short:   "Search saved handoff manifests by goal, title, session ID, or branch",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		all, _ := cmd.Flags().GetBool("all")
		jsonFlag, _ := cmd.Flags().GetBool("json")
		branchFilter, _ := cmd.Flags().GetString("branch")
		query := strings.Join(args, " ")

		cwd, _ := os.Getwd()
		filter := cwd
		if all {
			filter = ""
		}

		handoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		manifests, err := meshContext.SearchManifests(handoffsDir, filter, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error searching handoffs: %v\n", err)
			os.Exit(1)
		}

		if branchFilter != "" {
			var filtered []meshContext.HandoffManifest
			for _, m := range manifests {
				if m.GitBranch == branchFilter {
					filtered = append(filtered, m)
				}
			}
			manifests = filtered
		}

		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(manifests)
			return
		}

		currentBranch := meshContext.GetCurrentGitBranch(cwd)
		fmt.Printf("\n\033[1;36m[Staypoint :: Search Handoffs Matching %q]\033[0m\n", query)
		renderManifestsTable(manifests, currentBranch)
	},
}

var handoffBranchesCmd = &cobra.Command{
	Use:     "branches",
	Aliases: []string{"branch"},
	Short:   "List branches in this repository that have saved handoffs",
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		baseHandoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		summaries, err := meshContext.ListBranchesWithHandoffs(baseHandoffsDir, cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing branches: %v\n", err)
			os.Exit(1)
		}
		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(summaries)
			return
		}
		currentBranch := meshContext.GetCurrentGitBranch(cwd)
		fmt.Printf("\n\033[1;36m[Staypoint :: Branches with Saved Handoffs in %s]\033[0m\n", filepath.Base(cwd))
		if len(summaries) == 0 {
			fmt.Println("  No branches with saved handoffs found.")
			return
		}
		for _, s := range summaries {
			badge := "\033[1;34m"
			suffix := ""
			if s.Branch == currentBranch {
				badge = "\033[1;32m✔ "
				suffix = " \033[0;32m(current branch)\033[0m"
			}
			age := time.Since(s.LatestAt).Round(time.Minute)
			fmt.Printf("  • %s[%s]\033[0m%s  •  %d handoffs (latest: %v ago)\n", badge, s.Branch, suffix, s.HandoffCount, age)
			title := s.LatestTitle
			if title == "" {
				title = s.LatestGoal
			}
			if len(title) > 60 {
				title = title[:60] + "..."
			}
			fmt.Printf("    Latest: \033[1m%q\033[0m\n", title)
			fmt.Printf("    Filter via: \033[0;33mstaypoint handoff list -b %s\033[0m\n\n", s.Branch)
		}
	},
}

var handoffShowCmd = &cobra.Command{
	Use:   "show [session-id]",
	Short: "Display markdown contents of a saved handoff",
	Run: func(cmd *cobra.Command, args []string) {
		handoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		cwd, _ := os.Getwd()
		sessionID := ""
		if len(args) > 0 {
			sessionID = args[0]
		}

		if sessionID == "" || sessionID == "latest" {
			latestMan, err := meshContext.GetLatestManifest(handoffsDir, cwd)
			if err == nil && latestMan != nil {
				sessionID = latestMan.SessionID
			}
		}

		if sessionID == "" {
			fmt.Fprintf(os.Stderr, "No session ID specified and no recent handoff found in %s.\n", filepath.Base(cwd))
			os.Exit(1)
		}

		md, err := meshContext.LoadHandoffMarkdown(handoffsDir, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading handoff: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(md)
	},
}

var handoffCopyCmd = &cobra.Command{
	Use:   "copy [session-id]",
	Short: "Copy saved handoff prompt to system clipboard",
	Run: func(cmd *cobra.Command, args []string) {
		handoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		cwd, _ := os.Getwd()
		sessionID := ""
		if len(args) > 0 {
			sessionID = args[0]
		}

		if sessionID == "" || sessionID == "latest" {
			latestMan, err := meshContext.GetLatestManifest(handoffsDir, cwd)
			if err == nil && latestMan != nil {
				sessionID = latestMan.SessionID
			}
		}

		if sessionID == "" {
			fmt.Fprintf(os.Stderr, "No session ID specified and no recent handoff found in %s.\n", filepath.Base(cwd))
			os.Exit(1)
		}

		md, err := meshContext.LoadHandoffMarkdown(handoffsDir, sessionID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading handoff: %v\n", err)
			os.Exit(1)
		}

		_ = meshContext.CopyToClipboard(md)
		fmt.Printf("\033[1;32m✔ Handoff for session %s copied to clipboard!\033[0m\n", sessionID)
	},
}

var handoffsCmd = &cobra.Command{
	Use:   "handoffs",
	Short: "Alias for staypoint handoff list",
	Run: func(cmd *cobra.Command, args []string) {
		handoffListCmd.Run(cmd, args)
	},
}


func init() {
	rootCmd.AddCommand(handoffCmd)
	rootCmd.AddCommand(handoffsCmd)

	handoffCmd.AddCommand(handoffListCmd)
	handoffCmd.AddCommand(handoffSearchCmd)
	handoffCmd.AddCommand(handoffBranchesCmd)
	handoffCmd.AddCommand(handoffShowCmd)
	handoffCmd.AddCommand(handoffCopyCmd)

	handoffCmd.Flags().String("to", "gemini", "Target model family (gemini or claude)")
	handoffCmd.Flags().String("step", "", "Immediate next step description")
	handoffCmd.Flags().String("push", "", "Push active handoff context to remote host over SSH/Tailscale")
	handoffCmd.Flags().String("pull", "", "Pull active handoff context from remote host over SSH/Tailscale")

	handoffListCmd.Flags().BoolP("all", "a", false, "Include handoffs from all repositories")
	handoffListCmd.Flags().IntP("limit", "l", 10, "Maximum handoffs to list")
	handoffListCmd.Flags().BoolP("json", "j", false, "Output results as JSON")
	handoffListCmd.Flags().StringP("branch", "b", "", "Filter handoffs by git branch")

	handoffSearchCmd.Flags().BoolP("all", "a", false, "Search across all repositories")
	handoffSearchCmd.Flags().BoolP("json", "j", false, "Output results as JSON")
	handoffSearchCmd.Flags().StringP("branch", "b", "", "Filter handoffs by git branch")

	handoffBranchesCmd.Flags().BoolP("json", "j", false, "Output results as JSON")

	handoffsCmd.Flags().BoolP("all", "a", false, "Include handoffs from all repositories")
	handoffsCmd.Flags().IntP("limit", "l", 10, "Maximum handoffs to list")
	handoffsCmd.Flags().BoolP("json", "j", false, "Output results as JSON")
	handoffsCmd.Flags().StringP("branch", "b", "", "Filter handoffs by git branch")
}
