package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/fleet"
)

func TestPrintFleetDashboard(t *testing.T) {
	overview := &fleet.FleetOverview{
		Timestamp: time.Now(),
		Organizations: []fleet.OrgFleetSummary{
			{
				ID:          "org-1",
				Name:        "StayPoint",
				IssuePrefix: "STA",
				TaskCounts: fleet.TaskStatusCounts{
					Total:   10,
					Running: 2,
					Active:  5,
					Blocked: 1,
					Done:    2,
				},
				ActiveAgents: 3,
				ActiveAgentsByProvider: map[string]int{
					"claude": 2,
					"gemini": 1,
				},
				SpentUSD:    120.50,
				SpentTokens: 500000,
			},
			{
				ID:          "org-2",
				Name:        "Managed Solution",
				IssuePrefix: "MAN",
				TaskCounts: fleet.TaskStatusCounts{
					Total:   8,
					Running: 1,
					Active:  6,
					Blocked: 0,
					Done:    1,
				},
				ActiveAgents: 2,
				ActiveAgentsByProvider: map[string]int{
					"claude": 2,
				},
				SpentUSD:    350.00,
				SpentTokens: 1200000,
			},
		},
		GlobalTasks: fleet.TaskStatusCounts{
			Total:   18,
			Running: 3,
			Active:  11,
			Blocked: 1,
			Done:    3,
		},
		GlobalAgents: fleet.GlobalAgentMetrics{
			Total:         5,
			ActiveRunning: 5,
			ByProvider: map[string]int{
				"claude": 4,
				"gemini": 1,
			},
		},
		TokenTelemetry: fleet.TokenSummary{
			TotalTokens:     1700000,
			InputTokens:     1200000,
			OutputTokens:    500000,
			CacheReadTokens: 100000,
			TotalCostUSD:    470.50,
		},
		ProviderQuotas: map[string]*fleet.ProviderQuotaGauge{
			"gemini": {
				Provider:             "gemini",
				DisplayName:          "Google Gemini",
				FiveHourRemainingPct: 85.0,
				FiveHourUsedPct:      15.0,
				BurnRate5h:           1.5,
				WeeklyRemainingPct:   40.0,
				ProjectionStatus:     "on_track",
				ProjectionMessage:    "On Track: healthy headroom",
				RunwayTurns:          56,
			},
			"claude": {
				Provider:             "claude",
				DisplayName:          "Anthropic Claude",
				FiveHourRemainingPct: 45.0,
				FiveHourUsedPct:      55.0,
				BurnRate5h:           5.6,
				WeeklyRemainingPct:   80.0,
				ProjectionStatus:     "on_track",
				ProjectionMessage:    "On Track: sustainable burn pace",
				RunwayTurns:          15,
			},
			"openai": {
				Provider:             "openai",
				DisplayName:          "OpenAI / Codex",
				FiveHourRemainingPct: 100.0,
				FiveHourUsedPct:      0.0,
				BurnRate5h:           4.0,
				WeeklyRemainingPct:   100.0,
				ProjectionStatus:     "on_track",
				ProjectionMessage:    "On Track: within weekly budget",
				RunwayTurns:          25,
			},
		},
		Tasks: []fleet.TaskItem{
			{
				ID:           "t-1",
				Identifier:   "STA-146",
				Title:        "Multi-Org Fleet Dashboard",
				Organization: "StayPoint",
				Status:       "running",
				SpentUSD:     25.0,
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printFleetDashboard(overview)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify key sections are present
	checks := []string{
		"StayPoint :: Multi-Organization Fleet Dashboard",
		"5-Hour Rolling Quotas & Lockout Indicators",
		"Google Gemini",
		"Anthropic Claude",
		"OpenAI / Codex",
		"Active Agents & Organization Roster",
		"Token Telemetry & Cost Accounting",
		"Global Task Status Overview",
		"STA-146",
		"Multi-Org Fleet Dashboard",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected dashboard output to contain %q", check)
		}
	}
}
