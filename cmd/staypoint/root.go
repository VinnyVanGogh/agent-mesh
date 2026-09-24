package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	"github.com/VinnyVanGogh/staypoint/internal/config"
	"github.com/VinnyVanGogh/staypoint/internal/router"
)

var (
	version = "0.1.0"
	commit  = "none"
	date    = "unknown"
	cfg     *config.Config
	rootCmd = &cobra.Command{
		Use:     "staypoint [command|args...]",
		Version: version,
		Short:   "Staypoint: Autonomous AI Agent Ops, Quota Pacing & Context Platform",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			cfg, err = config.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return nil
		},
		FParseErrWhitelist: cobra.FParseErrWhitelist{
			UnknownFlags: true,
		},
		Run: runSmartLaunch,
	}
)



func runSmartLaunch(cmd *cobra.Command, args []string) {
	statusFlag, _ := cmd.Flags().GetBool("status")
	if statusFlag {
		statusCmd.Run(cmd, args)
		return
	}

	continueFlag, _ := cmd.Flags().GetBool("continue")
	resumeFlag, _ := cmd.Flags().GetBool("resume")
	handoffFlag, _ := cmd.Flags().GetBool("handoff")

	if continueFlag {
		handleContinueFlow(cmd, handoffFlag)
		return
	}
	if resumeFlag {
		handleResumeFlow(cmd, args, handoffFlag)
		return
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	forceClaude, _ := cmd.Flags().GetBool("claude")
	forceGemini, _ := cmd.Flags().GetBool("gemini")
	noSSH, _ := cmd.Flags().GetBool("no-ssh")

	pacerState, _ := router.LoadPacerState()
	cwd, _ := os.Getwd()
	routeCtx, routeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer routeCancel()

	remoteHost := "company-mbp"
	if cfg != nil && cfg.RemoteHost != "" {
		remoteHost = cfg.RemoteHost
	}

	var targetTool string
	var targetModel string
	var isRemoteWork bool

	if forceClaude {
		targetTool = "claude"
		targetModel = "claude-sonnet-4-6"
	} else if forceGemini {
		targetTool = "agy"
		targetModel = "gemini-3.8-flash-high"
	} else {
		decision, err := router.Route(routeCtx, cwd, pacerState, router.RouteOptions{
			CheckSSH:   !noSSH,
			RemoteHost: remoteHost,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Routing error: %v\n", err)
			os.Exit(1)
		}
		targetTool = decision.Tool
		targetModel = decision.Model
		if decision.Target == router.TargetRemoteClaude {
			isRemoteWork = true
		}
	}

	// Always render the Tokyo Night statusline before launch
	_ = router.RenderStatusline(os.Stdout, nil)

	if dryRun {
		fmt.Printf("\n\033[1;36m[Staypoint :: Dry Run]\033[0m\n")
		fmt.Printf("  • Tool:        %s\n", targetTool)
		fmt.Printf("  • Model:       %s\n", targetModel)
		fmt.Printf("  • Remote Work: %t\n", isRemoteWork)
		fmt.Printf("  • Arguments:   %v\n", args)
		return
	}

	// If remote work session on remote host:
	if isRemoteWork {
		err := bridge.Launch(context.Background(), bridge.LaunchOptions{
			Host:       remoteHost,
			TargetDir:  cwd,
			Args:       args,
			ForceLocal: false,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Bridge launch error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Local session execution
	binName := targetTool
	if binName == "" {
		binName = "agy"
	}

	binPath, err := exec.LookPath(binName)
	if err != nil {
		altBin := "claude"
		if binName == "claude" {
			altBin = "agy"
		}
		binPath, err = exec.LookPath(altBin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: neither '%s' nor '%s' found in PATH.\n", binName, altBin)
			os.Exit(1)
		}
		binName = altBin
	}

	execArgs := append([]string{binName}, args...)
	if err := syscall.Exec(binPath, execArgs, os.Environ()); err != nil {
		// Fallback to exec.Command if syscall.Exec fails (e.g. on non-Unix)
		subCmd := exec.Command(binPath, args...)
		subCmd.Stdin = os.Stdin
		subCmd.Stdout = os.Stdout
		subCmd.Stderr = os.Stderr
		subCmd.Env = os.Environ()

		if err := subCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
	}
}


func init() {
	cfg = config.DefaultConfig()
	rootCmd.Flags().BoolP("claude", "C", false, "Force route to Claude Code")
	rootCmd.Flags().BoolP("gemini", "G", false, "Force route to Antigravity Gemini")
	rootCmd.Flags().BoolP("continue", "c", false, "Continue the most recent agent session in this repository")
	rootCmd.Flags().BoolP("resume", "r", false, "Show recent sessions across Claude and Antigravity to resume or hand off")
	rootCmd.Flags().BoolP("handoff", "H", false, "Synthesize zero-effort handoff prompt to clipboard instead of launching")
	rootCmd.Flags().BoolP("status", "s", false, "Display fleet status & quota table")
	rootCmd.Flags().BoolP("dry-run", "n", false, "Preview routed target without executing")
	rootCmd.Flags().Bool("no-ssh", false, "Bypass remote SSH probe")
}
