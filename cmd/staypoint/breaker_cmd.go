package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
	"github.com/spf13/cobra"
)

var breakerCmd = &cobra.Command{
	Use:   "breaker",
	Short: "Inspect and reset agent circuit breakers",
}

var breakerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active or past circuit breakers",
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		all, _ := cmd.Flags().GetBool("all")
		cwd, _ := os.Getwd()
		repoFilter := cwd
		if all {
			repoFilter = ""
		}

		cbs, err := telemetry.ListCircuitBreakers(store.DB(), repoFilter, !all)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error listing breakers: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\033[1;36m[Agent Circuit Breakers]\033[0m")
		if len(cbs) == 0 {
			fmt.Println("  All circuit breakers clear. No tripped agents.")
			return
		}
		for _, b := range cbs {
			status := "\033[1;32m[CLEAR]\033[0m"
			if b.IsTripped {
				status = "\033[1;31m[TRIPPED]\033[0m"
			}
			fmt.Printf("  • %s Session: %s (%s) | Trips: %d\n", status, b.SessionID, b.AgentType, b.TripCount)
			if b.IsTripped {
				fmt.Printf("    Failing tool: %s | Cmd: %s\n", b.FailingTool, b.FailingCommand)
				fmt.Printf("    Last error:   %s\n", b.LastError)
				fmt.Printf("    Reset via:    staypoint breaker reset %s\n", b.SessionID)
			}
		}
	},
}

var breakerResetCmd = &cobra.Command{
	Use:   "reset [session-id]",
	Short: "Reset a tripped circuit breaker to allow execution to resume",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()

		sessionID := args[0]
		tracker := telemetry.NewBreakerTracker()
		if err := tracker.ResetCircuitBreaker(store.DB(), sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "Error resetting breaker: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Circuit breaker reset for session %s\033[0m\n", sessionID)
	},
}

func cleanGitStatusFile(line string) string {
	line = strings.TrimSpace(line)
	if len(line) >= 3 {
		idx := strings.Index(line, " ")
		if idx != -1 {
			return strings.TrimSpace(line[idx:])
		}
	}
	return line
}

func init() {
	rootCmd.AddCommand(breakerCmd)
	breakerCmd.AddCommand(breakerListCmd)
	breakerCmd.AddCommand(breakerResetCmd)
	breakerListCmd.Flags().BoolP("all", "a", false, "Include cleared circuit breakers")
}
