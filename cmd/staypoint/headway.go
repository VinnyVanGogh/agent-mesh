package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/router"
)

var (
	headwayProvider string
	headwayJSON     bool
)

var headwayCmd = &cobra.Command{
	Use:   "headway",
	Short: "Inspect dynamic burn-rate pacing and quota headway across AI model pools",
	Long: `Evaluate whether AI model pools have sufficient weekly burn-rate headway
to permit unattended autonomous execution (--dangerously-skip-permissions) or if quota
is tight/overpaced, requiring per-task confirmation.`,
	Run: runHeadway,
}

func init() {
	headwayCmd.Flags().StringVarP(&headwayProvider, "provider", "p", "any", "Filter provider: any, gemini, claude, claude_3p, work")
	headwayCmd.Flags().BoolVar(&headwayJSON, "json", false, "Output results in JSON format")
	rootCmd.AddCommand(headwayCmd)
}

func runHeadway(cmd *cobra.Command, args []string) {
	pacerState, err := router.LoadPacerState()
	if err != nil || pacerState == nil {
		if headwayJSON {
			fmt.Println(`{"error": "failed to load quota state"}`)
		} else {
			fmt.Printf("Error: failed to load quota state: %v\n", err)
		}
		os.Exit(1)
	}

	summary := pacerState.GetHeadwaySummary()

	gemPool := pacerState.Pools[router.PoolGeminiNative]
	persPool := pacerState.Pools[router.PoolPersonalClaude]
	workPool := pacerState.Pools[router.PoolWorkClaude]
	p3Pool := pacerState.Pools[router.Pool3PClaude]

	if headwayJSON {
		output := map[string]interface{}{
			"summary": map[string]interface{}{
				"gemini_headway":    summary.GeminiHeadway,
				"claude_headway":    summary.ClaudeHeadway,
				"any_headway":       summary.AnyHeadway,
				"all_headway":       summary.AllHeadway,
				"suggested_adapter": summary.SuggestedAdapter,
				"execution_posture": summary.ExecutionPosture,
			},
		}

		if gemPool != nil {
			output["gemini"] = map[string]interface{}{
				"weekly_remaining": gemPool.Weekly.RemainingPct,
				"five_hour_rem":    gemPool.FiveHour.RemainingPct,
				"burn_rate":        gemPool.BurnRatePerDay,
				"budget":           gemPool.SustainableBudget,
				"on_pace":          gemPool.IsOnPace,
				"locked":           gemPool.IsLocked,
				"has_headway":      gemPool.HasHeadway,
			}
		}

		if persPool != nil {
			output["claude_personal"] = map[string]interface{}{
				"weekly_remaining": persPool.Weekly.RemainingPct,
				"five_hour_rem":    persPool.FiveHour.RemainingPct,
				"burn_rate":        persPool.BurnRatePerDay,
				"budget":           persPool.SustainableBudget,
				"on_pace":          persPool.IsOnPace,
				"locked":           persPool.IsLocked,
				"has_headway":      persPool.HasHeadway,
			}
		}

		if workPool != nil {
			output["claude_work"] = map[string]interface{}{
				"weekly_remaining": workPool.Weekly.RemainingPct,
				"five_hour_rem":    workPool.FiveHour.RemainingPct,
				"burn_rate":        workPool.BurnRatePerDay,
				"budget":           workPool.SustainableBudget,
				"on_pace":          workPool.IsOnPace,
				"locked":           workPool.IsLocked,
				"has_headway":      workPool.HasHeadway,
			}
		}

		if p3Pool != nil {
			output["claude_3p"] = map[string]interface{}{
				"weekly_remaining": p3Pool.Weekly.RemainingPct,
				"five_hour_rem":    p3Pool.FiveHour.RemainingPct,
				"burn_rate":        p3Pool.BurnRatePerDay,
				"budget":           p3Pool.SustainableBudget,
				"on_pace":          p3Pool.IsOnPace,
				"locked":           p3Pool.IsLocked,
				"has_headway":      p3Pool.HasHeadway,
			}
		}

		jsonBytes, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(jsonBytes))

		// Check exit code based on filter
		if !checkProviderSuccess(headwayProvider, summary, pacerState) {
			os.Exit(1)
		}
		return
	}

	// Human-readable CLI formatting
	fmt.Println("\033[1;36m[Staypoint :: Quota Headway & Fleet Pacing Engine]\033[0m")
	fmt.Printf("  • Time:                    %s\n", time.Now().Format("03:04 PM MST"))

	postureText := "\033[1;32m✔ Autonomous / Unattended Execution Active\033[0m"
	if summary.ExecutionPosture == "restricted" {
		postureText = "\033[1;31m⚠ Restricted (Confirmation Required Before Action)\033[0m"
	}
	fmt.Printf("  • Fleet Execution Posture: %s\n", postureText)
	fmt.Printf("  • Suggested Adapter:       \033[1;32m%s\033[0m\n", summary.SuggestedAdapter)
	fmt.Println()
	fmt.Println("Model Pool Headway Breakdown:")

	renderPoolRow := func(label string, pool *router.QuotaPool) {
		if pool == nil {
			return
		}
		status := "\033[1;32m✔ Headway Available\033[0m"
		if pool.IsLocked {
			status = "\033[1;31m✖ Locked Out\033[0m"
		} else if !pool.IsOnPace {
			status = "\033[1;33m⚠ Tight / Overpaced\033[0m"
		}

		pacingDetail := fmt.Sprintf("Burn: %.1f%%/d <= Budget: %.1f%%/d", pool.BurnRatePerDay, pool.SustainableBudget)
		if !pool.IsOnPace {
			pacingDetail = fmt.Sprintf("Burn: %.1f%%/d > Budget: %.1f%%/d", pool.BurnRatePerDay, pool.SustainableBudget)
		}

		fmt.Printf("  • %-24s %s (%s | Week Left: %.1f%% | 5h Left: %.1f%%)\n",
			label+":", status, pacingDetail, pool.Weekly.RemainingPct, pool.FiveHour.RemainingPct)
	}

	renderPoolRow("Gemini Native", gemPool)
	renderPoolRow("Personal Claude Code", persPool)
	renderPoolRow("Enterprise (Work)", workPool)
	renderPoolRow("Claude / 3P", p3Pool)

	if !checkProviderSuccess(headwayProvider, summary, pacerState) {
		os.Exit(1)
	}
}

func checkProviderSuccess(provider string, summary router.HeadwaySummary, state *router.PacerState) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "gemini", "gemini_local":
		return summary.GeminiHeadway
	case "claude", "claude_local", "personal":
		pool := state.Pools[router.PoolPersonalClaude]
		return pool != nil && pool.HasHeadway
	case "work", "claude_work":
		pool := state.Pools[router.PoolWorkClaude]
		return pool != nil && pool.HasHeadway
	case "claude_3p", "3p":
		pool := state.Pools[router.Pool3PClaude]
		return pool != nil && pool.HasHeadway
	default:
		return summary.AnyHeadway
	}
}
