package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/gitgate"
	"github.com/spf13/cobra"
)

var gateCmd = &cobra.Command{
	Use:   "gate <pre|post|main-contains> [sha...]",
	Short: "Git state gates: pre-flight, post-flight, and merged-to-main checks",
	Long: `staypoint gate runs hard git state checks.

  pre  <repo> <branch>   Fetch, dirty-check, and fast-forward before a run.
  post <repo> <branch>   Dirty-check, unpushed-commit check, merged-main report.
  main-contains <sha...> Exit non-zero if any SHA is not in origin/main.

main-contains uses the current directory as the repo root unless --repo is set.
Prints "NOT IN MAIN: <sha> <subject>" for each missing SHA, or
"ALL MERGED <origin/main sha>" when everything is present.`,
}

var gateCmdRepo string
var gateCmdBranch string

func init() {
	gateCmd.PersistentFlags().StringVar(&gateCmdRepo, "repo", ".", "Path to git repository")
	gateCmd.PersistentFlags().StringVar(&gateCmdBranch, "branch", "main", "Branch name for pre/post checks")

	gateCmd.AddCommand(gatePreCmd)
	gateCmd.AddCommand(gatePostCmd)
	gateCmd.AddCommand(gateMainContainsCmd)

	rootCmd.AddCommand(gateCmd)
}

var gatePreCmd = &cobra.Command{
	Use:   "pre",
	Short: "Run git pre-flight (fetch, dirty check, fast-forward)",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := gateCmdRepo
		branch := gateCmdBranch

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		r, err := gitgate.PreFlight(ctx, repo, branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "preflight error: %v\n", err)
			os.Exit(1)
		}
		for _, d := range r.Details {
			fmt.Println(d)
		}
		if !r.OK {
			fmt.Fprintln(os.Stderr, "PRE-FLIGHT FAILED")
			for _, e := range r.Errors {
				fmt.Fprintln(os.Stderr, "  ERROR:", e)
			}
			os.Exit(1)
		}
		fmt.Println("PRE-FLIGHT OK")
		return nil
	},
}

var gatePostCmd = &cobra.Command{
	Use:   "post",
	Short: "Run git post-flight (dirty check, unpushed commits, merged-main report)",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := gateCmdRepo
		branch := gateCmdBranch

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		r, err := gitgate.PostFlight(ctx, repo, branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "postflight error: %v\n", err)
			os.Exit(1)
		}
		for _, d := range r.Details {
			fmt.Println(d)
		}
		if !r.OK {
			fmt.Fprintln(os.Stderr, "POST-FLIGHT FAILED")
			for _, e := range r.Errors {
				fmt.Fprintln(os.Stderr, "  ERROR:", e)
			}
			os.Exit(1)
		}
		fmt.Println("POST-FLIGHT OK")
		return nil
	},
}

var gateMainContainsCmd = &cobra.Command{
	Use:   "main-contains <sha...>",
	Short: "Check that every SHA is in origin/main (ancestry or squash-equivalence)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := gateCmdRepo

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		missing, err := gitgate.MainContains(ctx, repo, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "main-contains error: %v\n", err)
			os.Exit(1)
		}

		if len(missing) > 0 {
			for _, sha := range missing {
				subject := gitSubject(ctx, repo, sha)
				fmt.Printf("NOT IN MAIN: %s %s\n", sha, subject)
			}
			os.Exit(1)
		}

		mainSHA := gitHead(ctx, repo, "origin/main")
		fmt.Printf("ALL MERGED %s\n", mainSHA)
		return nil
	},
}

func gitSubject(ctx context.Context, repo, sha string) string {
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, "git", "log", "--format=%s", "-1", sha)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "(unknown subject)"
	}
	return strings.TrimSpace(string(out))
}

func gitHead(ctx context.Context, repo, ref string) string {
	ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, "git", "rev-parse", "--short", ref)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
