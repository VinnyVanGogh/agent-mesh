package main

// eval-contracts subcommand: evaluate all checklist contracts and write a cache
// to ~/.staypoint/contract-eval-cache.json.
//
// Called by reinstall-daemon.sh BEFORE loading the launchd agent so the daemon
// can read cached results without touching ~/Documents, which is blocked by
// macOS TCC until the user grants Documents access to staypointd.
//
// Usage: staypointd eval-contracts [--sprint STA-236] [--repo-root /path] [--db /path/to/staypoint.db]

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
	_ "modernc.org/sqlite"
)

func runEvalContracts(args []string) error {
	fs := flag.NewFlagSet("eval-contracts", flag.ContinueOnError)
	sprint := fs.String("sprint", "STA-236", "checklist sprint identifier")
	repoRoot := fs.String("repo-root", "", "repository root (defaults to STAYPOINT_REPO_ROOT env or cwd)")
	dbPath := fs.String("db", "", "path to staypoint.db (defaults to ~/.staypoint/staypoint.db)")
	outPath := fs.String("out", "", "output cache path (defaults to ContractCachePath)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *repoRoot == "" {
		if v := os.Getenv("STAYPOINT_REPO_ROOT"); v != "" {
			*repoRoot = v
		} else {
			cwd, _ := os.Getwd()
			*repoRoot = cwd
		}
	}
	if *dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home dir: %w", err)
		}
		*dbPath = filepath.Join(home, ".staypoint", "staypoint.db")
	}
	if *outPath == "" {
		*outPath = checklist.ContractCachePath()
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("open db %s: %w", *dbPath, err)
	}
	defer db.Close()

	commit := checklist.GetGitCommitSHA(*repoRoot)

	fmt.Printf("→ Evaluating %s contracts (repo=%s, commit=%s) ...\n", *sprint, *repoRoot, commit)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cache, err := checklist.EvalContractsForSprint(ctx, db, *sprint, *repoRoot, commit)
	if err != nil {
		return fmt.Errorf("evaluate contracts: %w", err)
	}

	if len(cache.Entries) == 0 {
		fmt.Printf("  No contracts found for sprint %s in %s (DB may need seeding via daemon).\n", *sprint, *dbPath)
		fmt.Printf("  Skipping cache write — daemon will evaluate live.\n")
		return nil
	}

	passed := 0
	for _, e := range cache.Entries {
		if e.Passed {
			passed++
		}
	}
	fmt.Printf("  %d/%d contracts passed\n", passed, len(cache.Entries))

	if err := checklist.WriteContractCache(*outPath, cache); err != nil {
		return fmt.Errorf("write cache %s: %w", *outPath, err)
	}
	fmt.Printf("  Contract cache written: %s\n", *outPath)
	return nil
}
