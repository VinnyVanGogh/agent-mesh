package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/router"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display real-time quota meters, active task context, and routing recommendations",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("\033[1;36m[Staypoint :: Fleet Status & Pacing Engine]\033[0m")
		fmt.Printf("  • Time:                    %s\n", time.Now().Format("03:04 PM MST"))

		pacerState, _ := router.LoadPacerState()
		cwd, _ := os.Getwd()
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		remoteHost := "company-mbp"
		if cfg != nil && cfg.RemoteHost != "" {
			remoteHost = cfg.RemoteHost
		}

		decision, _ := router.Route(ctx, cwd, pacerState, router.RouteOptions{
			CheckSSH:   false,
			RemoteHost: remoteHost,
		})

		if pacerState != nil {
			workPool := pacerState.Pools[router.PoolWorkClaude]
			persPool := pacerState.Pools[router.PoolPersonalClaude]
			geminiPool := pacerState.Pools[router.PoolGeminiNative]
			pool3p := pacerState.Pools[router.Pool3PClaude]

			if workPool != nil {
				workName := "Enterprise (Work)"
				if cfg != nil && cfg.CompanyName != "" {
					workName = fmt.Sprintf("%s (Work)", cfg.CompanyName)
				}
				fmt.Printf("  • %-26s \033[1;32m✔ Highest Priority\033[0m | %s | Week Left: %.0f%% | 5h Left: %.0f%%\n",
					workName+":", formatHeadwayBadge(workPool), workPool.Weekly.RemainingPct, workPool.FiveHour.RemainingPct)
			}
			if persPool != nil {
				persColor := "\033[1;32m✔ Available\033[0m"
				if persPool.IsLocked {
					persColor = "\033[1;31m✖ Locked\033[0m"
				} else if !persPool.IsOnPace {
					persColor = "\033[1;33m⚠ Tight\033[0m"
				}
				fmt.Printf("  • Personal Claude Code:    %s | %s | Week Left: %.0f%% | 5h Left: %.0f%%\n",
					persColor, formatHeadwayBadge(persPool), persPool.Weekly.RemainingPct, persPool.FiveHour.RemainingPct)
			}
			if geminiPool != nil {
				gemColor := "\033[1;32m✔ Available\033[0m"
				if geminiPool.IsLocked {
					gemColor = "\033[1;31m✖ Locked\033[0m"
				} else if !geminiPool.IsOnPace {
					gemColor = "\033[1;33m⚠ Tight\033[0m"
				}
				fmt.Printf("  • Gemini Native Quota:     %s | %s | Week Left: %.1f%% | 5h Left: %.1f%%\n",
					gemColor, formatHeadwayBadge(geminiPool), geminiPool.Weekly.RemainingPct, geminiPool.FiveHour.RemainingPct)
			}
			if pool3p != nil {
				p3pColor := "\033[1;32m✔ Available\033[0m"
				if pool3p.IsLocked || pool3p.Weekly.RemainingPct <= 0 {
					p3pColor = "\033[1;31m✖ Locked (0%)\033[0m"
				} else if !pool3p.IsOnPace {
					p3pColor = "\033[1;33m⚠ Tight\033[0m"
				}
				fmt.Printf("  • Claude / 3P Quota:       %s | %s | Week Left: %.1f%%\n",
					p3pColor, formatHeadwayBadge(pool3p), pool3p.Weekly.RemainingPct)
			}

			summary := pacerState.GetHeadwaySummary()
			postureColor := "\033[1;32m✔ Autonomous / Unattended\033[0m"
			if summary.ExecutionPosture == "restricted" {
				postureColor = "\033[1;31m⚠ Restricted (Permission Required)\033[0m"
			}
			fmt.Printf("  • Fleet Execution Posture: %s (Adapter: %s)\n",
				postureColor, summary.SuggestedAdapter)
		}

		if decision != nil {
			fmt.Printf("  • Recommended Route:       \033[1;32m%s\033[0m (via \033[1m%s\033[0m in %s)\n",
				decision.Model, decision.Tool, decision.Workspace)
		}
		if cfg != nil {
			fmt.Printf("  • Database:                %s (WAL Active)\n", cfg.DBPath)
		}
	},
}

func formatHeadwayBadge(pool *router.QuotaPool) string {
	if pool == nil {
		return ""
	}
	if pool.IsLocked {
		return "\033[1;31m✖ Locked\033[0m"
	}
	if pool.HasHeadway {
		if pool.BurnRatePerDay > 0 {
			return fmt.Sprintf("\033[1;32m✔ On Pace\033[0m (burn: %.1f%%/d <= budget: %.1f%%/d)", pool.BurnRatePerDay, pool.SustainableBudget)
		}
		return "\033[1;32m✔ Headway Available\033[0m"
	}
	return fmt.Sprintf("\033[1;33m⚠ Overpaced / Tight\033[0m (burn: %.1f%%/d > budget: %.1f%%/d)", pool.BurnRatePerDay, pool.SustainableBudget)
}


var statuslineCmd = &cobra.Command{
	Use:   "statusline",
	Short: "Render instantaneous statusline widget (<2ms) for prompt/tmux integration",
	Run: func(cmd *cobra.Command, args []string) {
		if err := router.RenderStatusline(os.Stdout, os.Stdin); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(statuslineCmd)
}
