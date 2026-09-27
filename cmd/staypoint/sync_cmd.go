package main

import (
	"context"
	"fmt"
	"os"
	"time"

	meshSync "github.com/VinnyVanGogh/staypoint/internal/sync"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronize telemetry, transcripts, and handoff states across machines",
}

var syncPullCmd = &cobra.Command{
	Use:   "pull [remote_host]",
	Short: "Pull remote transcripts over SSH/rsync and ingest into local telemetry DB",
	Run: func(cmd *cobra.Command, args []string) {
		host := cfg.RemoteHost
		if len(args) > 0 && args[0] != "" {
			host = args[0]
		}
		if host == "" {
			host = "company-mbp"
		}

		fmt.Printf("\033[1;36m[sync]\033[0m Pulling transcripts from %s...\n", host)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := meshSync.PullTranscripts(ctx, host, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Sync pull failed: %v\033[0m\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Telemetry synchronization complete!\033[0m\n")
		fmt.Printf("  • Remote Host:      %s\n", res.Host)
		fmt.Printf("  • Ingested Records: %d new\n", res.RecordsIngested)
		fmt.Printf("  • Duration:         %s\n", res.Duration.Round(time.Millisecond))
	},
}

var syncExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export local telemetry records into a portable compressed bundle for air-gapped or non-SSH transfer",
	Run: func(cmd *cobra.Command, args []string) {
		outPath, _ := cmd.Flags().GetString("output")
		dest, count, err := meshSync.ExportBundle(outPath, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Export error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Telemetry bundle exported successfully!\033[0m\n")
		fmt.Printf("  • Archive:     %s\n", dest)
		fmt.Printf("  • Records:     %d\n", count)
		fmt.Printf("  • Machine:     %s\n", cfg.MachineRole)
		fmt.Printf("  • To import:   staypoint sync import %s\n", dest)
	},
}

var syncImportCmd = &cobra.Command{
	Use:   "import [bundle_path]",
	Short: "Import telemetry records from an exported bundle into local telemetry DB",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		bundlePath := args[0]
		count, err := meshSync.ImportBundle(bundlePath, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Import error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Successfully imported %d new records into telemetry database!\033[0m\n", count)
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
	syncCmd.AddCommand(syncPullCmd)
	syncCmd.AddCommand(syncExportCmd)
	syncCmd.AddCommand(syncImportCmd)
	syncExportCmd.Flags().StringP("output", "o", "", "Destination path for exported .tar.gz bundle")
}
