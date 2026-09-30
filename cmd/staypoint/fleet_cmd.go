package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/fleet"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
	"github.com/spf13/cobra"
)

var (
	fleetJSONFlag   bool
	fleetOrgFilter  string
	fleetStatusFlag string
)

var fleetCmd = &cobra.Command{
	Use:     "fleet",
	Aliases: []string{"dashboard", "multi-org"},
	Short:   "Display unified multi-organization fleet dashboard, token telemetry, and 5-hour quota gauges",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()

		var store *db.Store
		if cfg != nil && cfg.DBPath != "" {
			var err error
			store, err = db.Open(cfg.DBPath)
			if err != nil {
				// non-fatal: fallback to read-only or empty store
				store = nil
			} else {
				defer store.Close()
			}
		}

		pclipClient := paperclip.NewClient("", "")
		telemPath := ""
		if cfg != nil {
			telemPath = cfg.TelemetryDBPath
		}

		var database *sql.DB
		if store != nil {
			database = store.DB()
		}

		agg := fleet.NewAggregator(database, telemPath, pclipClient)
		overview, err := agg.Gather(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error gathering fleet overview: %v\n", err)
			os.Exit(1)
		}

		if fleetJSONFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(overview)
			return
		}

		printFleetDashboard(overview)
	},
}

func printFleetDashboard(o *fleet.FleetOverview) {
	fmt.Println("\033[1;36m[StayPoint :: Multi-Organization Fleet Dashboard]\033[0m")
	fmt.Printf("  • Time:                    %s\n", time.Now().Format("03:04 PM MST"))
	fmt.Printf("  • Fleet Deployment:        \033[1m%d Organizations\033[0m · \033[1m%d Active Agents\033[0m · \033[1m%d Running Tasks\033[0m\n",
		len(o.Organizations), o.GlobalAgents.ActiveRunning, o.GlobalTasks.Running)

	// Pacing Status Banner
	pacingBanner := "\033[1;32m✔ FLEET PACING ON TRACK (Headway Available)\033[0m"
	for _, q := range o.ProviderQuotas {
		if q.IsLocked || q.ProjectionStatus == "locked_out" {
			pacingBanner = "\033[1;31m✖ CRITICAL: PROVIDER QUOTA LOCKOUT DETECTED\033[0m"
			break
		} else if q.ProjectionStatus == "overpaced" {
			pacingBanner = "\033[1;33m⚠ WARNING: ACCELERATED BURN RATE DETECTED (RESTRICTED MODE RECOMMENDED)\033[0m"
		}
	}
	fmt.Printf("  • Overall Pacing:          %s\n\n", pacingBanner)

	// 1. 5-Hour Rolling Quota & Lockout Indicators
	fmt.Println("\033[1;35m[5-Hour Rolling Quotas & Lockout Indicators]\033[0m")
	for _, key := range []string{"gemini", "claude", "openai"} {
		q, ok := o.ProviderQuotas[key]
		if !ok || q == nil {
			continue
		}

		statusColor := "\033[1;32m✔ Available\033[0m"
		if q.IsLocked || q.ProjectionStatus == "locked_out" {
			statusColor = "\033[1;31m✖ Locked Out\033[0m"
		} else if q.ProjectionStatus == "overpaced" {
			statusColor = "\033[1;33m⚠ Overpaced\033[0m"
		}

		resetInfo := ""
		if q.FiveHourResetsAt != nil && q.FiveHourResetsAt.After(time.Now()) {
			diff := q.FiveHourResetsAt.Sub(time.Now())
			hrs := int(diff.Hours())
			mins := int(diff.Minutes()) % 60
			resetInfo = fmt.Sprintf(", resets in %dh %dm", hrs, mins)
		}

		fmt.Printf("  • %-22s %s | 5h Left: \033[1m%.1f%%\033[0m (Used: %.1f%%%s) | Burn: %.2f%%/turn\n",
			q.DisplayName+":", statusColor, q.FiveHourRemainingPct, q.FiveHourUsedPct, resetInfo, q.BurnRate5h)
		fmt.Printf("    \033[2m↳ Projection: %s (Week Left: %.1f%%, Runway: %d turns)\033[0m\n",
			q.ProjectionMessage, q.WeeklyRemainingPct, q.RunwayTurns)
	}
	fmt.Println()

	// 2. Active Running Agents Overview
	fmt.Println("\033[1;35m[Active Agents & Organization Roster]\033[0m")
	provBreakdown := fmt.Sprintf("Claude: %d · Gemini: %d · OpenAI: %d",
		o.GlobalAgents.ByProvider["claude"], o.GlobalAgents.ByProvider["gemini"], o.GlobalAgents.ByProvider["openai"])
	fmt.Printf("  • Global Active Agents:    \033[1m%d online\033[0m (%s)\n", o.GlobalAgents.ActiveRunning, provBreakdown)

	for _, org := range o.Organizations {
		provs := org.ActiveAgentsByProvider
		fmt.Printf("  • %-22s \033[1m%d agents\033[0m (Running: %d, Blocked: %d, Done: %d) [%s]\n",
			org.Name+":", org.ActiveAgents, org.TaskCounts.Running, org.TaskCounts.Blocked, org.TaskCounts.Done,
			fmt.Sprintf("Claude: %d, Gemini: %d, OpenAI: %d", provs["claude"], provs["gemini"], provs["openai"]))
	}
	fmt.Println()

	// 3. Token Telemetry & Cost Accounting
	fmt.Println("\033[1;35m[Token Telemetry & Cost Accounting]\033[0m")
	fmt.Printf("  • Ingested Tokens:         \033[1m%s\033[0m (Input: %s · Output: %s · Cache: %s)\n",
		formatCompact(o.TokenTelemetry.TotalTokens), formatCompact(o.TokenTelemetry.InputTokens),
		formatCompact(o.TokenTelemetry.OutputTokens), formatCompact(o.TokenTelemetry.CacheReadTokens))
	fmt.Printf("  • Total Cost Equivalent:   \033[1;33m$%.2f\033[0m (API list-price ratecard valuation)\n",
		o.TokenTelemetry.TotalCostUSD)

	if len(o.ModelSpend) > 0 {
		fmt.Println("  • Spend by Model:")
		for i, m := range o.ModelSpend {
			if i >= 4 {
				break
			}
			fmt.Printf("    - %-22s $%.2f (%4.1f%%) · %s tokens\n",
				m.Model, m.CostUSD, m.Percentage, formatCompact(m.TotalTokens))
		}
	}

	if len(o.OrgSpend) > 0 {
		fmt.Println("  • Spend by Organization:")
		for _, org := range o.OrgSpend {
			fmt.Printf("    - %-22s $%.2f (%4.1f%%) · %s tokens (%d active tasks)\n",
				org.Organization, org.CostUSD, org.Percentage, formatCompact(org.TotalTokens), org.ActiveTasks)
		}
	}
	fmt.Println()

	// 4. Global Task Overview
	fmt.Println("\033[1;35m[Global Task Status Overview]\033[0m")
	fmt.Printf("  • Consolidated Tasks:      \033[1m%d Total\033[0m | Running: \033[1;36m%d\033[0m | Active: \033[1;34m%d\033[0m | Blocked: \033[1;31m%d\033[0m | Stopped: %d | Errored: %d | Done: \033[1;32m%d\033[0m\n",
		o.GlobalTasks.Total, o.GlobalTasks.Running, o.GlobalTasks.Active, o.GlobalTasks.Blocked,
		o.GlobalTasks.Stopped, o.GlobalTasks.Errored, o.GlobalTasks.Done)

	// Filter tasks if requested
	var displayedTasks []fleet.TaskItem
	for _, t := range o.Tasks {
		if fleetOrgFilter != "" && !strings.EqualFold(t.Organization, fleetOrgFilter) {
			continue
		}
		if fleetStatusFlag != "" && !strings.EqualFold(t.Status, fleetStatusFlag) {
			continue
		}
		displayedTasks = append(displayedTasks, t)
	}

	if len(displayedTasks) > 0 {
		fmt.Println("\n  Recent Consolidated Tasks:")
		limit := 10
		if len(displayedTasks) < limit {
			limit = len(displayedTasks)
		}
		for i := 0; i < limit; i++ {
			t := displayedTasks[i]
			statusColor := "\033[1;32m"
			switch t.Status {
			case "running":
				statusColor = "\033[1;36m"
			case "blocked":
				statusColor = "\033[1;31m"
			case "errored":
				statusColor = "\033[1;31m"
			case "stopped":
				statusColor = "\033[0;37m"
			case "done":
				statusColor = "\033[0;32m"
			default:
				statusColor = "\033[1;34m"
			}
			fmt.Printf("    • %s[%-7s]\033[0m \033[1m%-10s\033[0m %-32s (Org: %s | Spend: $%.2f)\n",
				statusColor, t.Status, t.Identifier, truncate(t.Title, 32), t.Organization, t.SpentUSD)
		}
		if len(displayedTasks) > limit {
			fmt.Printf("    \033[2m... and %d more tasks across fleet (use --json for full export)\033[0m\n", len(displayedTasks)-limit)
		}
	}
}

func formatCompact(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000.0)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000.0)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000.0)
	}
	return fmt.Sprintf("%d", n)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func init() {
	fleetCmd.Flags().BoolVar(&fleetJSONFlag, "json", false, "Output fleet telemetry and status in JSON format")
	fleetCmd.Flags().StringVar(&fleetOrgFilter, "org", "", "Filter tasks by organization name")
	fleetCmd.Flags().StringVar(&fleetStatusFlag, "status", "", "Filter tasks by status (running, active, blocked, stopped, errored, done)")
	rootCmd.AddCommand(fleetCmd)
}
