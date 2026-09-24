package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/bridge"
)

var bridgeCmd = &cobra.Command{
	Use:   "bridge",
	Short: "Work bridge and remote host connectivity manager",
}

var bridgeCheckCmd = &cobra.Command{
	Use:   "check [dir]",
	Short: "Check whether directory is a work repo and probe remote node",
	Run: func(cmd *cobra.Command, args []string) {
		targetDir := "."
		if len(args) > 0 {
			targetDir = args[0]
		}
		host := "company-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		res, err := bridge.Check(ctx, targetDir, host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bridge check error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;36m[Staypoint WorkBridge]\033[0m\n")
		fmt.Printf("  • Local Path:   %s\n", res.LocalPath)
		fmt.Printf("  • Is Work Repo: %v\n", res.IsWorkRepo)
		fmt.Printf("  • Remote Host:  %s\n", res.RemoteHost)
		fmt.Printf("  • Remote Path:  %s\n", res.RemotePath)
		if res.Probe.Reachable {
			fmt.Printf("  • SSH Status:   \033[1;32m✔ Online\033[0m (%v latency)\n", res.Probe.Latency)
			fmt.Printf("  • Decision:     \033[1;32mRoute to remote session\033[0m\n")
		} else {
			fmt.Printf("  • SSH Status:   \033[1;33m✖ Offline\033[0m (%s)\n", res.Probe.Error)
			fmt.Printf("  • Decision:     \033[1;33mFallback to local work session\033[0m\n")
		}
	},
}

var bridgeLaunchCmd = &cobra.Command{
	Use:   "launch [dir] [args...]",
	Short: "Launch remote Claude Code session with local fallback",
	Run: func(cmd *cobra.Command, args []string) {
		targetDir := "."
		var passArgs []string
		if len(args) > 0 {
			targetDir = args[0]
			passArgs = args[1:]
		}
		host := "company-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}
		ctx := context.Background()
		err := bridge.Launch(ctx, bridge.LaunchOptions{
			TargetDir: targetDir,
			Host:      host,
			Args:      passArgs,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bridge launch error: %v\n", err)
			os.Exit(1)
		}
	},
}

var screenshotCmd = &cobra.Command{
	Use:     "screenshot [file]",
	Aliases: []string{"shot", "snap"},
	Short:   "Capture or transfer screenshot to/from remote Claude session via SCP",
	Run: func(cmd *cobra.Command, args []string) {
		interactive, _ := cmd.Flags().GetBool("interactive")
		clipboard, _ := cmd.Flags().GetBool("clipboard")
		pull, _ := cmd.Flags().GetBool("pull")
		outPath, _ := cmd.Flags().GetString("output")
		alsoCwd, _ := cmd.Flags().GetBool("cwd")
		host, _ := cmd.Flags().GetString("host")

		if host == "" && cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}

		ctx := context.Background()

		if pull {
			remoteSrc := outPath
			if len(args) > 0 {
				remoteSrc = args[0]
			}
			res, err := bridge.PullScreenshot(ctx, bridge.ScreenshotOptions{
				Host:          host,
				RemotePath:    remoteSrc,
				OpenAfterPull: true,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[1;31m✖ Screenshot pull error:\033[0m %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("\033[1;32m✔ Screenshot pulled from %s:%s\033[0m\n", res.Host, res.RemotePath)
			fmt.Printf("  • Local file: %s (%s)\n", res.LocalPath, formatBytes(res.FileSize))
			return
		}

		var localSrc string
		if len(args) > 0 {
			localSrc = args[0]
		}

		var remoteCwd string
		if alsoCwd {
			cwd, _ := os.Getwd()
			remoteCwd = bridge.ToRemotePath(cwd)
		}

		res, err := bridge.PushScreenshot(ctx, bridge.ScreenshotOptions{
			Host:          host,
			SourcePath:    localSrc,
			RemotePath:    outPath,
			Interactive:   interactive,
			FromClipboard: clipboard,
			RemoteCwd:     remoteCwd,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Screenshot push error:\033[0m %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\033[1;32m✔ Screenshot pushed to %s:%s\033[0m (%s in %v)\n",
			res.Host, res.RemotePath, formatBytes(res.FileSize), res.Duration.Round(time.Millisecond))
		fmt.Printf("  • Local file:  %s\n", res.LocalPath)
		fmt.Printf("  • Remote file: \033[1;33m%s\033[0m (copied to clipboard)\n", res.RemotePath)
		fmt.Printf("  • In Claude:   \033[1;36mlook at %s\033[0m (Cmd+V to paste)\n", res.RemotePath)
	},
}

var scpCmd = &cobra.Command{
	Use:   "scp [flags] <source> [destination]",
	Short: "Fast SCP file or directory transfer across the bridge with path mapping",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		pull, _ := cmd.Flags().GetBool("pull")
		recursive, _ := cmd.Flags().GetBool("recursive")
		host, _ := cmd.Flags().GetString("host")

		if host == "" && cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}

		src := args[0]
		dst := ""
		if len(args) > 1 {
			dst = args[1]
		}

		ctx := context.Background()
		res, err := bridge.Transfer(ctx, bridge.TransferOptions{
			Host:      host,
			Source:    src,
			Dest:      dst,
			Pull:      pull,
			Recursive: recursive,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ SCP transfer error:\033[0m %v\n", err)
			os.Exit(1)
		}

		if res.Action == "pulled" {
			fmt.Printf("\033[1;32m✔ Pulled from %s:%s -> %s\033[0m\n", res.Host, res.Source, res.Dest)
		} else {
			fmt.Printf("\033[1;32m✔ Pushed %s -> %s:%s\033[0m\n", res.Source, res.Host, res.Dest)
			fmt.Printf("  • Remote path copied to clipboard: \033[1;33m%s\033[0m\n", res.Dest)
		}
	},
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

var fetchCmd = &cobra.Command{
	Use:   "fetch <client-path> [dest]",
	Short: "Fetch a client file over the reverse bridge tunnel",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clientPath := args[0]
		dest := ""
		if len(args) > 1 {
			dest = args[1]
		}
		ctx := context.Background()
		cached, err := bridge.FetchClientFile(ctx, clientPath, dest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fetch error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\033[1;32m✔ Fetched client file:\033[0m %s -> \033[1;33m%s\033[0m\n", clientPath, cached)
	},
}


func init() {
	rootCmd.AddCommand(bridgeCmd)
	rootCmd.AddCommand(screenshotCmd)
	rootCmd.AddCommand(scpCmd)
	rootCmd.AddCommand(fetchCmd)

	bridgeCmd.AddCommand(bridgeCheckCmd)
	bridgeCmd.AddCommand(bridgeLaunchCmd)
	bridgeCmd.AddCommand(screenshotCmd)
	bridgeCmd.AddCommand(scpCmd)
	bridgeCmd.AddCommand(fetchCmd)

	screenshotCmd.Flags().BoolP("interactive", "i", false, "Trigger interactive screen area crop (screencapture -i)")
	screenshotCmd.Flags().BoolP("clipboard", "c", false, "Extract screenshot directly from system clipboard")
	screenshotCmd.Flags().BoolP("pull", "p", false, "Pull screenshot from remote host to local Mac and open it")
	screenshotCmd.Flags().StringP("output", "o", "", "Destination path (default: /tmp/screenshot.png)")
	screenshotCmd.Flags().Bool("cwd", true, "Also copy screenshot into remote working directory if in work repo")
	screenshotCmd.Flags().String("host", "", "Remote host (defaults to config remote_host)")

	scpCmd.Flags().BoolP("pull", "p", false, "Pull file or directory from remote host to local machine")
	scpCmd.Flags().BoolP("recursive", "r", false, "Copy directories recursively")
	scpCmd.Flags().String("host", "", "Remote host (defaults to config remote_host)")
}
