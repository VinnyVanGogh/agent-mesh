package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
	"github.com/spf13/cobra"
)

var (
	checklistSprint      string
	checklistRepoDir     string
	checklistDowngrade   bool
	checklistNotifyPClip bool
)

var checklistCmd = &cobra.Command{
	Use:   "checklist",
	Short: "Manage and verify sprint verification checklists and machine contracts",
}

var checklistVerifyCmd = &cobra.Command{
	Use:     "verify",
	Aliases: []string{"eval", "check"},
	Short:   "Evaluate machine-verifiable contracts and auto-flag regressions/divergence",
	Long: `Evaluates all machine-verifiable contracts attached to checklist items in a sprint.
If a previously-verified item (status 'pass') fails its contract assertion,
it is automatically flagged as a divergence, downgraded to 'fail', and recorded in audit history.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\033[1;34m[StayPoint Checklist Verifier]\033[0m Evaluating sprint \033[1m%s\033[0m...\n", checklistSprint)

		if cfg == nil || cfg.DBPath == "" {
			fmt.Printf("\033[0;31mError: configuration or DBPath is not set\033[0m\n")
			os.Exit(1)
		}

		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Printf("\033[0;31mError opening database (%s): %v\033[0m\n", cfg.DBPath, err)
			os.Exit(1)
		}
		defer store.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		var pclipClient *paperclip.Client
		if checklistNotifyPClip {
			pclipClient = paperclip.NewClient("", "")
		}

		opts := checklist.EvaluateOptions{
			RepoRoot:           checklistRepoDir,
			Downgrade:          checklistDowngrade,
			NotifyPaperclip:    checklistNotifyPClip,
			PaperclipClient:    pclipClient,
			PaperclipCompanyID: os.Getenv("PAPERCLIP_COMPANY_ID"),
		}

		summary, err := checklist.EvaluateSprint(ctx, store.DB(), checklistSprint, opts)
		if err != nil {
			fmt.Printf("\033[0;31mError evaluating checklist sprint: %v\033[0m\n", err)
			os.Exit(1)
		}

		fmt.Printf("Git HEAD: \033[1m%s\033[0m | Total Contracts: %d | Passed: \033[1;32m%d\033[0m | Failed: \033[1;31m%d\033[0m | Divergences: \033[1;31m%d\033[0m\n\n",
			summary.CommitSHA, summary.Total, summary.Passed, summary.Failed, summary.Divergences)

		for _, r := range summary.Results {
			if r.Diverged {
				fmt.Printf(" \033[1;31m[DIVERGENCE / REGRESSION]\033[0m %s: \033[1m%s\033[0m\n", r.Section, r.Title)
				fmt.Printf("    • Previous Status: \033[1;32mpass\033[0m -> New Status: \033[1;31mfail\033[0m\n")
				fmt.Printf("    • Failure Reason:  %s\n", r.Reason)
				if r.Details != "" {
					fmt.Printf("    • Details:         %s\n", r.Details)
				}
				fmt.Println()
			} else if !r.Passed {
				fmt.Printf(" \033[0;31m[FAILED]\033[0m %s: \033[1m%s\033[0m (Status: %s)\n", r.Section, r.Title, r.PreviousStatus)
				fmt.Printf("    • Reason: %s\n", r.Reason)
				fmt.Println()
			} else {
				fmt.Printf(" \033[0;32m[PASS]\033[0m   %s: \033[1m%s\033[0m (Status: %s)\n", r.Section, r.Title, r.PreviousStatus)
			}
		}

		if summary.Divergences > 0 {
			fmt.Printf("\n\033[1;31m[Divergence Detected]\033[0m %d verified item(s) regressed! Automatically downgraded to 'fail'.\n", summary.Divergences)
			os.Exit(2)
		} else if summary.Failed > 0 {
			fmt.Printf("\n\033[0;33m[Checklist Incomplete]\033[0m %d contract(s) currently failing.\n", summary.Failed)
			os.Exit(1)
		} else {
			fmt.Printf("\n\033[1;32m[Checklist Verified]\033[0m All %d machine-verifiable contracts passed successfully!\n", summary.Total)
		}
	},
}

var checklistForceSeed bool

var checklistSeedCmd = &cobra.Command{
	Use:   "seed",
	Short: "Seed or update default checklist items and machine contracts for a sprint",
	Run: func(cmd *cobra.Command, args []string) {
		if cfg == nil || cfg.DBPath == "" {
			fmt.Printf("\033[0;31mError: configuration or DBPath is not set\033[0m\n")
			os.Exit(1)
		}
		store, err := db.Open(cfg.DBPath)
		if err != nil {
			fmt.Printf("\033[0;31mError opening database (%s): %v\033[0m\n", cfg.DBPath, err)
			os.Exit(1)
		}
		defer store.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		seeded, skipped, err := checklist.Seed(ctx, store.DB(), checklistSprint, checklistForceSeed)
		if err != nil {
			fmt.Printf("\033[0;31mSeed error: %v\033[0m\n", err)
			os.Exit(1)
		}
		if seeded > 0 {
			fmt.Printf("\033[1;32m[Checklist Seeded]\033[0m Successfully seeded %d items for sprint \033[1m%s\033[0m\n", seeded, checklistSprint)
		} else {
			fmt.Printf("\033[1;34m[Checklist Updated]\033[0m %d existing items already present for sprint \033[1m%s\033[0m (contracts updated)\n", skipped, checklistSprint)
		}
	},
}

func init() {
	checklistVerifyCmd.Flags().StringVar(&checklistSprint, "sprint", "STA-168", "Sprint identifier to evaluate")
	checklistVerifyCmd.Flags().StringVar(&checklistRepoDir, "repo-dir", ".", "Repository root directory for relative file assertions")
	checklistVerifyCmd.Flags().BoolVar(&checklistDowngrade, "downgrade", true, "Automatically downgrade regressed 'pass' items to 'fail'")
	checklistVerifyCmd.Flags().BoolVar(&checklistNotifyPClip, "notify-paperclip", false, "Create a Paperclip regression issue on divergence")

	checklistSeedCmd.Flags().StringVar(&checklistSprint, "sprint", "STA-168", "Sprint identifier to seed")
	checklistSeedCmd.Flags().BoolVar(&checklistForceSeed, "force", false, "Force re-seed and overwrite all items")

	checklistCmd.AddCommand(checklistVerifyCmd)
	checklistCmd.AddCommand(checklistSeedCmd)
	rootCmd.AddCommand(checklistCmd)
}
