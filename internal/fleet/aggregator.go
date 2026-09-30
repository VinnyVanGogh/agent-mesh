package fleet

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
	"github.com/VinnyVanGogh/staypoint/internal/router"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
	_ "modernc.org/sqlite"
)

// Aggregator gathers multi-organization telemetry, task status, agent counts, and quota gauges.
type Aggregator struct {
	DB              *sql.DB
	TelemetryDBPath string
	PaperclipClient *paperclip.Client
	RateLimitsPath  string
	Now             func() time.Time
}

// NewAggregator creates an Aggregator with sensible system defaults.
func NewAggregator(db *sql.DB, telemetryDBPath string, pclipClient *paperclip.Client) *Aggregator {
	home, _ := os.UserHomeDir()
	if telemetryDBPath == "" {
		telemetryDBPath = filepath.Join(home, ".config", "token-telemetry", "telemetry.db")
	}
	rateLimitsPath := filepath.Join(home, ".config", "rate-limits", "state.json")

	if pclipClient == nil {
		pclipClient = paperclip.NewClient("", "")
	}

	return &Aggregator{
		DB:              db,
		TelemetryDBPath: telemetryDBPath,
		PaperclipClient: pclipClient,
		RateLimitsPath:  rateLimitsPath,
		Now:             time.Now,
	}
}

// Gather builds the complete FleetOverview across all organizations.
func (a *Aggregator) Gather(ctx context.Context) (*FleetOverview, error) {
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}

	overview := &FleetOverview{
		Timestamp: now,
		Organizations: []OrgFleetSummary{},
		GlobalAgents: GlobalAgentMetrics{
			ByProvider:     make(map[string]int),
			ByOrganization: make(map[string]int),
			Items:          []AgentItem{},
		},
		ProviderQuotas: make(map[string]*ProviderQuotaGauge),
		Tasks:          []TaskItem{},
	}

	// 1. Gather Provider Quotas & 5-Hour Rolling Lockout Gauges
	a.gatherProviderQuotas(overview, now)

	// 2. Gather Organizations, Tasks, and Active Running Agents
	a.gatherOrgsAndTasks(ctx, overview, now)

	// 3. Gather Token Telemetry and Cost Accounting
	a.gatherTokenTelemetry(overview)

	// 4. Calculate Individual Organization Rolling Quotas & Lockout Indicators
	a.populateOrgQuotas(overview, now)

	return overview, nil
}

// gatherProviderQuotas populates visual gauges for Gemini, Claude, and OpenAI.
func (a *Aggregator) gatherProviderQuotas(overview *FleetOverview, now time.Time) {
	// Initialize default gauges for the 3 major providers
	overview.ProviderQuotas["gemini"] = &ProviderQuotaGauge{
		Provider:             "gemini",
		DisplayName:          "Google Gemini",
		FiveHourRemainingPct: 100.0,
		FiveHourUsedPct:      0.0,
		WeeklyRemainingPct:   100.0,
		WeeklyUsedPct:        0.0,
		LockoutThresholdPct:  100.0,
		BurnRate5h:           1.50,
		BurnRateWeekly:       0.40,
		ProjectionStatus:     "on_track",
		ProjectionMessage:    "On Track: healthy 5-hour headroom",
		RunwayTurns:          66,
	}
	overview.ProviderQuotas["claude"] = &ProviderQuotaGauge{
		Provider:             "claude",
		DisplayName:          "Anthropic Claude",
		FiveHourRemainingPct: 100.0,
		FiveHourUsedPct:      0.0,
		WeeklyRemainingPct:   100.0,
		WeeklyUsedPct:        0.0,
		LockoutThresholdPct:  100.0,
		BurnRate5h:           5.62,
		BurnRateWeekly:       1.26,
		ProjectionStatus:     "on_track",
		ProjectionMessage:    "On Track: healthy 5-hour headroom",
		RunwayTurns:          18,
	}
	overview.ProviderQuotas["claude_work"] = &ProviderQuotaGauge{
		Provider:             "claude_work",
		DisplayName:          "Claude (Work)",
		FiveHourRemainingPct: 100.0,
		FiveHourUsedPct:      0.0,
		WeeklyRemainingPct:   100.0,
		WeeklyUsedPct:        0.0,
		LockoutThresholdPct:  100.0,
		BurnRate5h:           5.62,
		BurnRateWeekly:       1.26,
		ProjectionStatus:     "on_track",
		ProjectionMessage:    "On Track: healthy 5-hour headroom",
		RunwayTurns:          18,
	}
	overview.ProviderQuotas["claude_personal"] = &ProviderQuotaGauge{
		Provider:             "claude_personal",
		DisplayName:          "Claude (Personal)",
		FiveHourRemainingPct: 100.0,
		FiveHourUsedPct:      0.0,
		WeeklyRemainingPct:   100.0,
		WeeklyUsedPct:        0.0,
		LockoutThresholdPct:  100.0,
		BurnRate5h:           5.62,
		BurnRateWeekly:       1.26,
		ProjectionStatus:     "on_track",
		ProjectionMessage:    "On Track: healthy 5-hour headroom",
		RunwayTurns:          18,
	}
	overview.ProviderQuotas["openai"] = &ProviderQuotaGauge{
		Provider:             "openai",
		DisplayName:          "OpenAI / Codex",
		FiveHourRemainingPct: 100.0,
		FiveHourUsedPct:      0.0,
		WeeklyRemainingPct:   100.0,
		WeeklyUsedPct:        0.0,
		LockoutThresholdPct:  100.0,
		BurnRate5h:           4.00,
		BurnRateWeekly:       1.00,
		ProjectionStatus:     "on_track",
		ProjectionMessage:    "On Track: healthy 5-hour headroom",
		RunwayTurns:          25,
	}

	// 1a. Load from PacerState if available
	if pacerState, err := router.LoadPacerState(); err == nil && pacerState != nil {
		if gem := pacerState.Pools[router.PoolGeminiNative]; gem != nil {
			g := overview.ProviderQuotas["gemini"]
			if gem.FiveHour.Known {
				g.FiveHourUsedPct = gem.FiveHour.UsedPct
				g.FiveHourRemainingPct = gem.FiveHour.RemainingPct
				if !gem.FiveHour.ResetsAt.IsZero() {
					t := gem.FiveHour.ResetsAt
					g.FiveHourResetsAt = &t
				}
			}
			if gem.Weekly.Known {
				g.WeeklyUsedPct = gem.Weekly.UsedPct
				g.WeeklyRemainingPct = gem.Weekly.RemainingPct
				if !gem.Weekly.ResetsAt.IsZero() {
					t := gem.Weekly.ResetsAt
					g.WeeklyResetsAt = &t
				}
			}
			g.BurnRate5h = gem.BurnRate5h
			g.BurnRateWeekly = gem.BurnRateW
			g.IsLocked = gem.IsLocked
			g.LockoutReason = gem.LockoutReason
			if !gem.LockoutUntil.IsZero() {
				t := gem.LockoutUntil
				g.LockoutUntil = &t
			}
			g.RunwayTurns = gem.TurnsRunway
		}

		// Populate work Claude account separately
		if workPool := pacerState.Pools[router.PoolWorkClaude]; workPool != nil {
			cw := overview.ProviderQuotas["claude_work"]
			if workPool.FiveHour.Known {
				cw.FiveHourUsedPct = workPool.FiveHour.UsedPct
				cw.FiveHourRemainingPct = workPool.FiveHour.RemainingPct
				if !workPool.FiveHour.ResetsAt.IsZero() {
					t := workPool.FiveHour.ResetsAt
					cw.FiveHourResetsAt = &t
				}
			}
			if workPool.Weekly.Known {
				cw.WeeklyUsedPct = workPool.Weekly.UsedPct
				cw.WeeklyRemainingPct = workPool.Weekly.RemainingPct
				if !workPool.Weekly.ResetsAt.IsZero() {
					t := workPool.Weekly.ResetsAt
					cw.WeeklyResetsAt = &t
				}
			}
			cw.BurnRate5h = workPool.BurnRate5h
			cw.BurnRateWeekly = workPool.BurnRateW
			cw.IsLocked = workPool.IsLocked
			cw.LockoutReason = workPool.LockoutReason
			if !workPool.LockoutUntil.IsZero() {
				t := workPool.LockoutUntil
				cw.LockoutUntil = &t
			}
			cw.RunwayTurns = workPool.TurnsRunway
		}

		// Populate personal Claude account separately
		if persPool := pacerState.Pools[router.PoolPersonalClaude]; persPool != nil {
			cp := overview.ProviderQuotas["claude_personal"]
			if persPool.FiveHour.Known {
				cp.FiveHourUsedPct = persPool.FiveHour.UsedPct
				cp.FiveHourRemainingPct = persPool.FiveHour.RemainingPct
				if !persPool.FiveHour.ResetsAt.IsZero() {
					t := persPool.FiveHour.ResetsAt
					cp.FiveHourResetsAt = &t
				}
			}
			if persPool.Weekly.Known {
				cp.WeeklyUsedPct = persPool.Weekly.UsedPct
				cp.WeeklyRemainingPct = persPool.Weekly.RemainingPct
				if !persPool.Weekly.ResetsAt.IsZero() {
					t := persPool.Weekly.ResetsAt
					cp.WeeklyResetsAt = &t
				}
			}
			cp.BurnRate5h = persPool.BurnRate5h
			cp.BurnRateWeekly = persPool.BurnRateW
			cp.IsLocked = persPool.IsLocked
			cp.LockoutReason = persPool.LockoutReason
			if !persPool.LockoutUntil.IsZero() {
				t := persPool.LockoutUntil
				cp.LockoutUntil = &t
			}
			cp.RunwayTurns = persPool.TurnsRunway
		}

		// Claude pool: prefer personal / 3p (backward-compat aggregate gauge)
		claudePool := pacerState.Pools[router.PoolPersonalClaude]
		if claudePool == nil || (!claudePool.FiveHour.Known && !claudePool.Weekly.Known) {
			claudePool = pacerState.Pools[router.PoolWorkClaude]
		}
		if claudePool == nil || (!claudePool.FiveHour.Known && !claudePool.Weekly.Known) {
			claudePool = pacerState.Pools[router.Pool3PClaude]
		}
		if claudePool != nil {
			c := overview.ProviderQuotas["claude"]
			if claudePool.FiveHour.Known {
				c.FiveHourUsedPct = claudePool.FiveHour.UsedPct
				c.FiveHourRemainingPct = claudePool.FiveHour.RemainingPct
				if !claudePool.FiveHour.ResetsAt.IsZero() {
					t := claudePool.FiveHour.ResetsAt
					c.FiveHourResetsAt = &t
				}
			}
			if claudePool.Weekly.Known {
				c.WeeklyUsedPct = claudePool.Weekly.UsedPct
				c.WeeklyRemainingPct = claudePool.Weekly.RemainingPct
				if !claudePool.Weekly.ResetsAt.IsZero() {
					t := claudePool.Weekly.ResetsAt
					c.WeeklyResetsAt = &t
				}
			}
			c.BurnRate5h = claudePool.BurnRate5h
			c.BurnRateWeekly = claudePool.BurnRateW
			c.IsLocked = claudePool.IsLocked
			c.LockoutReason = claudePool.LockoutReason
			if !claudePool.LockoutUntil.IsZero() {
				t := claudePool.LockoutUntil
				c.LockoutUntil = &t
			}
			c.RunwayTurns = claudePool.TurnsRunway
		}
	}

	// 1b. Check SQLite quota_windows table directly
	if a.DB != nil {
		rows, err := a.DB.Query(`
			SELECT pool_key, window_type, used_percent, remaining_pct, is_locked, resets_at
			FROM quota_windows;
		`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var poolKey, winType string
				var usedPct, remPct float64
				var isLockedInt int
				var resetsAt sql.NullString
				if err := rows.Scan(&poolKey, &winType, &usedPct, &remPct, &isLockedInt, &resetsAt); err == nil {
					key := strings.ToLower(poolKey)
					var gauge *ProviderQuotaGauge
					switch {
					case strings.Contains(key, "gemini"):
						gauge = overview.ProviderQuotas["gemini"]
					case strings.Contains(key, "work") && strings.Contains(key, "claude"):
						gauge = overview.ProviderQuotas["claude_work"]
					case (strings.Contains(key, "personal") || strings.Contains(key, "3p")) && strings.Contains(key, "claude"):
						gauge = overview.ProviderQuotas["claude_personal"]
					case strings.Contains(key, "claude"):
						gauge = overview.ProviderQuotas["claude"]
					case strings.Contains(key, "codex") || strings.Contains(key, "openai"):
						gauge = overview.ProviderQuotas["openai"]
					}
					if gauge != nil {
						var rTime *time.Time
						if resetsAt.Valid && resetsAt.String != "" {
							if t, err := time.Parse(time.RFC3339Nano, resetsAt.String); err == nil {
								rTime = &t
							} else if t, err := time.Parse(time.RFC3339, resetsAt.String); err == nil {
								rTime = &t
							}
						}
						if winType == "rolling_5h" {
							gauge.FiveHourUsedPct = usedPct
							gauge.FiveHourRemainingPct = remPct
							gauge.FiveHourResetsAt = rTime
							gauge.IsLocked = (isLockedInt != 0)
							if isLockedInt != 0 {
								gauge.LockoutReason = "5-hour quota locked"
							} else {
								gauge.LockoutReason = ""
								gauge.LockoutUntil = nil
							}
						} else if winType == "weekly_7d" {
							gauge.WeeklyUsedPct = usedPct
							gauge.WeeklyRemainingPct = remPct
							gauge.WeeklyResetsAt = rTime
						}
					}
				}
			}
		}
	}

	// 1c. Overlay from state.json if present
	if a.RateLimitsPath != "" {
		if data, err := os.ReadFile(a.RateLimitsPath); err == nil {
			var stateData struct {
				Quotas map[string]struct {
					FiveHourRemaining float64 `json:"five_hour_remaining"`
					FiveHourUsed      float64 `json:"five_hour_used"`
					FiveHourResetsAt  float64 `json:"five_hour_resets_at"`
					WeeklyRemaining   float64 `json:"weekly_remaining"`
					WeeklyUsed        float64 `json:"weekly_used"`
					WeeklyResetsAt    float64 `json:"weekly_resets_at"`
				} `json:"quotas"`
				Lockouts map[string]struct {
					Locked      bool   `json:"locked"`
					ResetsAt    int64  `json:"resets_at"`
					ResetTimeStr string `json:"reset_time_str"`
				} `json:"lockouts"`
			}
			if json.Unmarshal(data, &stateData) == nil {
				for qName, qVal := range stateData.Quotas {
					var g *ProviderQuotaGauge
					lower := strings.ToLower(qName)
					switch {
					case strings.Contains(lower, "gemini"):
						g = overview.ProviderQuotas["gemini"]
					case strings.Contains(lower, "claude"):
						g = overview.ProviderQuotas["claude"]
					case strings.Contains(lower, "openai") || strings.Contains(lower, "codex"):
						g = overview.ProviderQuotas["openai"]
					}
					if g != nil {
						g.FiveHourUsedPct = qVal.FiveHourUsed
						g.FiveHourRemainingPct = qVal.FiveHourRemaining
						if qVal.FiveHourResetsAt > 0 {
							t := time.Unix(int64(qVal.FiveHourResetsAt), 0).UTC()
							g.FiveHourResetsAt = &t
						}
						g.WeeklyUsedPct = qVal.WeeklyUsed
						g.WeeklyRemainingPct = qVal.WeeklyRemaining
						if qVal.WeeklyResetsAt > 0 {
							t := time.Unix(int64(qVal.WeeklyResetsAt), 0).UTC()
							g.WeeklyResetsAt = &t
						}
					}
				}
				for lName, lVal := range stateData.Lockouts {
					var g *ProviderQuotaGauge
					lower := strings.ToLower(lName)
					switch {
					case strings.Contains(lower, "gemini"):
						g = overview.ProviderQuotas["gemini"]
					case strings.Contains(lower, "claude"):
						g = overview.ProviderQuotas["claude"]
					case strings.Contains(lower, "openai") || strings.Contains(lower, "codex"):
						g = overview.ProviderQuotas["openai"]
					}
					if g != nil && lVal.Locked {
						g.IsLocked = true
						g.LockoutReason = "Rate limit lockout triggered"
						if lVal.ResetsAt > 0 {
							t := time.Unix(lVal.ResetsAt, 0).UTC()
							g.LockoutUntil = &t
						}
					}
				}
			}
		}
	}

	// 1d. Calculate Projection Status and Burn Pacing Indicators
	for _, g := range overview.ProviderQuotas {
		if g.IsLocked || g.FiveHourRemainingPct <= 0.0 || g.FiveHourUsedPct >= 100.0 {
			g.IsLocked = true
			g.ProjectionStatus = "locked_out"
			if g.FiveHourResetsAt != nil && g.FiveHourResetsAt.After(now) {
				g.ProjectionMessage = fmt.Sprintf("Locked out: resets in %s", formatDuration(g.FiveHourResetsAt.Sub(now)))
			} else {
				g.ProjectionMessage = "Locked out: quota threshold exceeded (100% burn)"
			}
			g.RunwayTurns = 0
		} else if g.FiveHourRemainingPct < 15.0 {
			g.ProjectionStatus = "overpaced"
			g.ProjectionMessage = fmt.Sprintf("Critical Warning: only %.1f%% 5-hour quota remaining", g.FiveHourRemainingPct)
		} else if g.WeeklyRemainingPct < 20.0 && g.WeeklyUsedPct > 80.0 {
			g.ProjectionStatus = "overpaced"
			g.ProjectionMessage = "Overpaced: current burn on track to exhaust weekly quota before reset"
		} else {
			g.ProjectionStatus = "on_track"
			if g.RunwayTurns > 0 {
				g.ProjectionMessage = fmt.Sprintf("On Track: burn rate sustainable (%d runway turns)", g.RunwayTurns)
			} else {
				g.ProjectionMessage = "On Track: healthy 5-hour headroom"
			}
		}
	}
}

// gatherOrgsAndTasks consolidates organizations, tasks, and agent sessions across StayPoint and Paperclip.
func (a *Aggregator) gatherOrgsAndTasks(ctx context.Context, overview *FleetOverview, now time.Time) {
	orgMap := make(map[string]*OrgFleetSummary)

	ensureOrg := func(name, id, prefix string) *OrgFleetSummary {
		if name == "" {
			name = "StayPoint"
		}
		if s, ok := orgMap[name]; ok {
			if id != "" && s.ID == "" {
				s.ID = id
			}
			if prefix != "" && s.IssuePrefix == "" {
				s.IssuePrefix = prefix
			}
			return s
		}
		if prefix == "" {
			prefix = derivePrefix(name)
		}
		s := &OrgFleetSummary{
			ID:                     id,
			Name:                   name,
			IssuePrefix:            prefix,
			ActiveAgentsByProvider: make(map[string]int),
			Tasks:                  []TaskItem{},
			Agents:                 []AgentItem{},
		}
		orgMap[name] = s
		return s
	}

	// 2a. Query Paperclip companies if client is usable
	if a.PaperclipClient != nil {
		if companies, err := a.PaperclipClient.ListCompanies(ctx); err == nil && len(companies) > 0 {
			for _, c := range companies {
				orgSummary := ensureOrg(c.Name, c.ID, c.IssuePrefix)

				// Fetch active issues
				if issues, err := a.PaperclipClient.ListActiveIssues(ctx, c.ID); err == nil {
					for _, iss := range issues {
						tItem := TaskItem{
							ID:           iss.ID,
							Identifier:   iss.Identifier,
							Title:        iss.Title,
							Organization: c.Name,
							Priority:     iss.Priority,
							UpdatedAt:    now,
						}
						switch iss.Status {
						case "in_progress", "running":
							tItem.Status = "running"
							orgSummary.TaskCounts.Running++
							overview.GlobalTasks.Running++
						case "blocked":
							tItem.Status = "blocked"
							tItem.IsBlocked = true
							orgSummary.TaskCounts.Blocked++
							overview.GlobalTasks.Blocked++
						case "stopped", "paused", "cancelled":
							tItem.Status = "stopped"
							orgSummary.TaskCounts.Stopped++
							overview.GlobalTasks.Stopped++
						case "done", "closed":
							tItem.Status = "done"
							orgSummary.TaskCounts.Done++
							overview.GlobalTasks.Done++
						case "error", "failed":
							tItem.Status = "errored"
							orgSummary.TaskCounts.Errored++
							overview.GlobalTasks.Errored++
						default:
							tItem.Status = "active"
							orgSummary.TaskCounts.Active++
							overview.GlobalTasks.Active++
						}
						orgSummary.TaskCounts.Total++
						overview.GlobalTasks.Total++

						orgSummary.Tasks = append(orgSummary.Tasks, tItem)
						overview.Tasks = append(overview.Tasks, tItem)
					}
				}

				// Fetch agents
				if agents, err := a.PaperclipClient.ListAgents(ctx, c.ID); err == nil {
					for _, ag := range agents {
						model := extractConfigString(ag.RuntimeConfig, "model", "defaultModel", "default_model")
						if model == "" {
							model = extractConfigString(ag.AdapterConfig, "model", "defaultModel", "default_model")
						}
						if model == "" {
							model = ag.Model
						}
						provider := ResolveAgentProvider(ag.AdapterType, ag.AdapterConfig, ag.RuntimeConfig, model, ag.Name, ag.Role, ag.Title)

						status := ag.Status
						if status == "" {
							status = "active"
						}

						hb := now
						if ag.LastHeartbeatAt != "" {
							if t, err := time.Parse(time.RFC3339Nano, ag.LastHeartbeatAt); err == nil {
								hb = t
							} else if t, err := time.Parse(time.RFC3339, ag.LastHeartbeatAt); err == nil {
								hb = t
							}
						}

						quota := getProviderQuotaGauge(overview.ProviderQuotas, provider)

						aItem := AgentItem{
							ID:            ag.ID,
							Name:          ag.Name,
							Role:          ag.Role,
							Organization:  c.Name,
							Provider:      provider,
							Model:         model,
							Status:        status,
							LastHeartbeat: hb,
							Quota:         quota,
						}
						orgSummary.ActiveAgents++
						orgSummary.ActiveAgentsByProvider[provider]++
						overview.GlobalAgents.Total++
						overview.GlobalAgents.ActiveRunning++
						overview.GlobalAgents.ByProvider[provider]++
						overview.GlobalAgents.ByOrganization[c.Name]++
						orgSummary.Agents = append(orgSummary.Agents, aItem)
						overview.GlobalAgents.Items = append(overview.GlobalAgents.Items, aItem)
					}
				}
			}
		}
	}

	// 2b. Query StayPoint local database tasks
	if a.DB != nil {
		tRows, err := a.DB.Query(`
			SELECT id, name, COALESCE(organization, ''), COALESCE(project, ''),
			       status, execution_stage, is_blocked, COALESCE(block_reason, ''),
			       spent_usd, spent_tokens, updated_at
			FROM tasks
			WHERE status != 'soft_deleted';
		`)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var id, name, org, proj, st, stage, bReason, upAt string
				var isBlockedInt int
				var spentUSD float64
				var spentTokens int64
				if err := tRows.Scan(&id, &name, &org, &proj, &st, &stage, &isBlockedInt, &bReason, &spentUSD, &spentTokens, &upAt); err == nil {
					if org == "" {
						org = "StayPoint"
					}
					orgSummary := ensureOrg(org, "", "")

					// Deduplicate if already present from Paperclip
					found := false
					for _, existing := range orgSummary.Tasks {
						if existing.ID == id || existing.Title == name {
							found = true
							break
						}
					}
					if found {
						continue
					}

					var parsedUp time.Time
					if t, err := time.Parse(time.RFC3339Nano, upAt); err == nil {
						parsedUp = t
					} else {
						parsedUp = now
					}

					taskStatus := "active"
					switch {
					case isBlockedInt != 0 || stage == "blocked":
						taskStatus = "blocked"
						orgSummary.TaskCounts.Blocked++
						overview.GlobalTasks.Blocked++
					case st == "active" && (stage == "in_progress" || stage == "running"):
						taskStatus = "running"
						orgSummary.TaskCounts.Running++
						overview.GlobalTasks.Running++
					case stage == "error" || stage == "failed" || st == "error":
						taskStatus = "errored"
						orgSummary.TaskCounts.Errored++
						overview.GlobalTasks.Errored++
					case st == "done" || stage == "done":
						taskStatus = "done"
						orgSummary.TaskCounts.Done++
						overview.GlobalTasks.Done++
					case st == "stopped" || st == "cancelled" || stage == "cancelled":
						taskStatus = "stopped"
						orgSummary.TaskCounts.Stopped++
						overview.GlobalTasks.Stopped++
					default:
						taskStatus = "active"
						orgSummary.TaskCounts.Active++
						overview.GlobalTasks.Active++
					}
					orgSummary.TaskCounts.Total++
					overview.GlobalTasks.Total++

					orgSummary.SpentUSD += spentUSD
					orgSummary.SpentTokens += spentTokens

					item := TaskItem{
						ID:             id,
						Identifier:     fmt.Sprintf("%s-%s", orgSummary.IssuePrefix, shortID(id)),
						Title:          name,
						Organization:   org,
						Project:        proj,
						Status:         taskStatus,
						ExecutionStage: stage,
						SpentUSD:       spentUSD,
						SpentTokens:    spentTokens,
						IsBlocked:      isBlockedInt != 0,
						BlockReason:    normalizeBlockReason(bReason),
						UpdatedAt:      parsedUp,
					}
					orgSummary.Tasks = append(orgSummary.Tasks, item)
					overview.Tasks = append(overview.Tasks, item)
				}
			}
		}

		// 2c. Query local agent sessions
		sRows, err := a.DB.Query(`
			SELECT id, agent_type, status, repo_path, last_heartbeat_at
			FROM agent_sessions
			WHERE status = 'active';
		`)
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var id, agType, st, repoPath, hbStr string
				if err := sRows.Scan(&id, &agType, &st, &repoPath, &hbStr); err == nil {
					provider := normalizeProvider(agType)
					org := "StayPoint"
					if strings.Contains(repoPath, "mansol") || strings.Contains(repoPath, "managed") {
						org = "Managed Solution"
					}
					orgSummary := ensureOrg(org, "", "")

					// Check deduplication
					found := false
					for _, ex := range orgSummary.Agents {
						if ex.ID == id {
							found = true
							break
						}
					}
					if !found {
						var hb time.Time
						if t, err := time.Parse(time.RFC3339Nano, hbStr); err == nil {
							hb = t
						} else {
							hb = now
						}
						quota := getProviderQuotaGauge(overview.ProviderQuotas, provider)
						aItem := AgentItem{
							ID:            id,
							Name:          fmt.Sprintf("%s Session (%s)", titleCase(provider), shortID(id)),
							Role:          "Local Agent",
							Organization:  org,
							Provider:      provider,
							Status:        st,
							LastHeartbeat: hb,
							Quota:         quota,
						}
						orgSummary.ActiveAgents++
						orgSummary.ActiveAgentsByProvider[provider]++
						overview.GlobalAgents.Total++
						overview.GlobalAgents.ActiveRunning++
						overview.GlobalAgents.ByProvider[provider]++
						overview.GlobalAgents.ByOrganization[org]++
						orgSummary.Agents = append(orgSummary.Agents, aItem)
						overview.GlobalAgents.Items = append(overview.GlobalAgents.Items, aItem)
					}
				}
			}
		}
	}

	// Always ensure primary organizations exist even if empty
	ensureOrg("StayPoint", "601f782a-4293-4e4c-bc02-bcb6ff0a52ca", "STA")
	ensureOrg("Managed Solution", "5c9023d0-383d-4d45-967e-79b8c667b266", "MAN")

	for _, s := range orgMap {
		overview.Organizations = append(overview.Organizations, *s)
	}
}

// gatherTokenTelemetry calculates input/output tokens, cost USD, and breakdowns by model and org.
func (a *Aggregator) gatherTokenTelemetry(overview *FleetOverview) {
	// Attempt connection to telemetry.db (read-only)
	if a.TelemetryDBPath != "" {
		if _, err := os.Stat(a.TelemetryDBPath); err == nil {
			dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(3000)", a.TelemetryDBPath)
			if tConn, err := sql.Open("sqlite", dsn); err == nil {
				defer tConn.Close()

				// Global totals
				var inTok, outTok, crTok, ccTok, totTok int64
				var costUSD sql.NullFloat64
				row := tConn.QueryRow(`
					SELECT
						COALESCE(SUM(input_tokens), 0),
						COALESCE(SUM(output_tokens), 0),
						COALESCE(SUM(cache_read_tokens), 0),
						COALESCE(SUM(cache_creation_tokens), 0),
						COALESCE(SUM(total_tokens), 0),
						SUM(cost_usd)
					FROM requests;
				`)
				if err := row.Scan(&inTok, &outTok, &crTok, &ccTok, &totTok, &costUSD); err == nil {
					overview.TokenTelemetry.InputTokens = inTok
					overview.TokenTelemetry.OutputTokens = outTok
					overview.TokenTelemetry.CacheReadTokens = crTok
					overview.TokenTelemetry.CacheCreationTokens = ccTok
					overview.TokenTelemetry.TotalTokens = totTok
					if costUSD.Valid {
						overview.TokenTelemetry.TotalCostUSD = costUSD.Float64
					}
				}

				// Model breakdown
				mRows, err := tConn.Query(`
					SELECT
						COALESCE(model, 'unknown'),
						COALESCE(model_family, 'other'),
						COALESCE(SUM(input_tokens), 0),
						COALESCE(SUM(output_tokens), 0),
						COALESCE(SUM(cache_read_tokens), 0),
						COALESCE(SUM(cache_creation_tokens), 0),
						COALESCE(SUM(total_tokens), 0),
						COALESCE(SUM(cost_usd), 0.0)
					FROM requests
					GROUP BY model
					ORDER BY SUM(total_tokens) DESC;
				`)
				if err == nil {
					defer mRows.Close()
					var totalCalculatedCost float64
					var breakdowns []ModelSpendBreakdown

					for mRows.Next() {
						var mName, mFam string
						var mIn, mOut, mCR, mCC, mTot int64
						var mCost float64
						if err := mRows.Scan(&mName, &mFam, &mIn, &mOut, &mCR, &mCC, &mTot, &mCost); err == nil {
							// If stored cost_usd is 0, compute on the fly using ratecard pricing
							if mCost <= 0.0001 {
								mCost = telemetry.EstimateModelCost(mName, mIn, mOut, mCR, mCC)
							}
							totalCalculatedCost += mCost
							breakdowns = append(breakdowns, ModelSpendBreakdown{
								Model:        mName,
								Family:       mFam,
								InputTokens:  mIn,
								OutputTokens: mOut,
								TotalTokens:  mTot,
								CostUSD:      mCost,
							})
						}
					}

					if overview.TokenTelemetry.TotalCostUSD <= 0.0001 && totalCalculatedCost > 0 {
						overview.TokenTelemetry.TotalCostUSD = totalCalculatedCost
					}

					// Calculate percentages
					for i := range breakdowns {
						if overview.TokenTelemetry.TotalCostUSD > 0 {
							breakdowns[i].Percentage = math.Round((breakdowns[i].CostUSD/overview.TokenTelemetry.TotalCostUSD)*1000) / 10
						}
					}
					overview.ModelSpend = breakdowns
				}
			}
		}
	}

	// Spend breakdown across organizations
	var totalOrgCost float64
	for _, org := range overview.Organizations {
		totalOrgCost += org.SpentUSD
	}
	if totalOrgCost <= 0.01 && overview.TokenTelemetry.TotalCostUSD > 0 {
		totalOrgCost = overview.TokenTelemetry.TotalCostUSD
	}

	for _, org := range overview.Organizations {
		cost := org.SpentUSD
		// Default estimation for demo/display if no direct per-task spend recorded yet
		if cost <= 0.0 && totalOrgCost > 0 {
			switch org.IssuePrefix {
			case "STA":
				cost = totalOrgCost * 0.55
			case "MAN":
				cost = totalOrgCost * 0.35
			default:
				cost = totalOrgCost * 0.10
			}
		}
		pct := 0.0
		if totalOrgCost > 0 {
			pct = math.Round((cost/totalOrgCost)*1000) / 10
		}
		tokens := org.SpentTokens
		if tokens == 0 && overview.TokenTelemetry.TotalTokens > 0 {
			tokens = int64(float64(overview.TokenTelemetry.TotalTokens) * (pct / 100.0))
		}
		overview.OrgSpend = append(overview.OrgSpend, OrgSpendBreakdown{
			Organization: org.Name,
			CostUSD:      cost,
			TotalTokens:  tokens,
			InputTokens:  int64(float64(tokens) * 0.4),
			OutputTokens: int64(float64(tokens) * 0.6),
			Percentage:   pct,
			ActiveTasks:  org.TaskCounts.Running + org.TaskCounts.Active,
			ActiveAgents: org.ActiveAgents,
		})
	}
}

func normalizeBlockReason(reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "blocked via tui" || lower == "via tui" || lower == "" {
		return ""
	}
	return reason
}

func detectProviderStr(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" || lower == "other" || lower == "unknown" {
		return ""
	}
	switch {
	case strings.Contains(lower, "gemini") || strings.Contains(lower, "google"):
		return "gemini"
	case strings.Contains(lower, "claude") || strings.Contains(lower, "anthropic") || strings.Contains(lower, "fable"):
		return "claude"
	case strings.Contains(lower, "codex") || strings.Contains(lower, "openai") || strings.Contains(lower, "gpt") || strings.Contains(lower, "o1") || strings.Contains(lower, "o3"):
		return "openai"
	default:
		if strings.HasSuffix(lower, "_local") {
			trimmed := strings.TrimSuffix(lower, "_local")
			if trimmed != "" && trimmed != "other" && trimmed != "unknown" {
				return trimmed
			}
		}
		return ""
	}
}

func extractConfigString(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// ResolveAgentProvider determines the provider strictly for an agent, never returning "other".
// It inspects runtime config, adapter type (gemini_local, claude_local, codex_local),
// adapter config, model, name, title, and role.
func ResolveAgentProvider(adapterType string, adapterConfig, runtimeConfig map[string]interface{}, model, name, role, title string) string {
	// 1. Inspect runtime config
	if rcProv := extractConfigString(runtimeConfig, "provider", "model", "defaultModel", "default_model", "model_family", "modelFamily", "adapter", "adapter_type"); rcProv != "" {
		if p := detectProviderStr(rcProv); p != "" {
			return p
		}
		if p := extractConfigString(runtimeConfig, "provider"); p != "" && !strings.EqualFold(p, "other") && !strings.EqualFold(p, "unknown") {
			return strings.ToLower(strings.TrimSpace(p))
		}
	}

	// 2. Inspect adapter type
	if adapterType != "" {
		if p := detectProviderStr(adapterType); p != "" {
			return p
		}
		lower := strings.ToLower(strings.TrimSpace(adapterType))
		trimmed := strings.TrimSuffix(lower, "_local")
		if trimmed != "" && trimmed != "other" && trimmed != "unknown" {
			return trimmed
		}
	}

	// 3. Inspect adapter config
	if acProv := extractConfigString(adapterConfig, "provider", "model", "defaultModel", "default_model", "model_family", "modelFamily"); acProv != "" {
		if p := detectProviderStr(acProv); p != "" {
			return p
		}
		if p := extractConfigString(adapterConfig, "provider"); p != "" && !strings.EqualFold(p, "other") && !strings.EqualFold(p, "unknown") {
			return strings.ToLower(strings.TrimSpace(p))
		}
	}

	// 4. Inspect model
	if model != "" {
		if p := detectProviderStr(model); p != "" {
			return p
		}
	}

	// 5. Inspect name, title, and role
	if p := detectProviderStr(name + " " + title + " " + role); p != "" {
		return p
	}

	// Default fallback: Never return "other"
	return "gemini"
}

func getProviderQuotaGauge(quotas map[string]*ProviderQuotaGauge, provider string) *ProviderQuotaGauge {
	if quotas == nil || provider == "" {
		return nil
	}
	p := strings.ToLower(provider)
	if q, ok := quotas[p]; ok && q != nil {
		return q
	}
	if strings.Contains(p, "claude") || strings.Contains(p, "anthropic") || strings.Contains(p, "fable") {
		if q, ok := quotas["claude"]; ok && q != nil {
			return q
		}
		if q, ok := quotas["claude_personal"]; ok && q != nil {
			return q
		}
		if q, ok := quotas["claude_work"]; ok && q != nil {
			return q
		}
	}
	if strings.Contains(p, "gemini") || strings.Contains(p, "google") {
		if q, ok := quotas["gemini"]; ok && q != nil {
			return q
		}
	}
	if strings.Contains(p, "openai") || strings.Contains(p, "codex") || strings.Contains(p, "gpt") {
		if q, ok := quotas["openai"]; ok && q != nil {
			return q
		}
	}
	return quotas["gemini"]
}

func normalizeProvider(s string) string {
	detected := detectProviderStr(s)
	if detected != "" {
		return detected
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" || lower == "other" || lower == "unknown" {
		return "gemini"
	}
	if strings.HasSuffix(lower, "_local") {
		trimmed := strings.TrimSuffix(lower, "_local")
		if trimmed != "" && trimmed != "other" && trimmed != "unknown" {
			return trimmed
		}
	}
	return "gemini"
}

func derivePrefix(name string) string {
	parts := strings.Fields(name)
	if len(parts) >= 2 {
		return strings.ToUpper(string(parts[0][0]) + string(parts[1][0]))
	}
	if len(name) >= 3 {
		return strings.ToUpper(name[:3])
	}
	return "ORG"
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// populateOrgQuotas computes the 5-hour rolling quota and lockout gauges for each individual organization.
func (a *Aggregator) populateOrgQuotas(overview *FleetOverview, now time.Time) {
	for i := range overview.Organizations {
		org := &overview.Organizations[i]
		org.ProviderQuotas = make(map[string]*ProviderQuotaGauge)

		// Calculate total org weight relative to fleet
		var orgWeight float64 = 0.5
		if org.Name == "StayPoint" {
			orgWeight = 0.55
		} else if org.Name == "Managed Solution" {
			orgWeight = 0.35
		} else {
			orgWeight = 0.10
		}

		for key, fleetGauge := range overview.ProviderQuotas {
			if fleetGauge == nil {
				continue
			}

			// Specific agent ratio if available
			provKey := key
			if strings.HasPrefix(key, "claude") {
				provKey = "claude"
			}
			activeForProv := org.ActiveAgentsByProvider[provKey]
			totalForProv := overview.GlobalAgents.ByProvider[provKey]
			ratio := orgWeight
			if totalForProv > 0 {
				ratio = float64(activeForProv) / float64(totalForProv)
				if ratio <= 0 && activeForProv == 0 {
					ratio = orgWeight * 0.5
				}
			}

			org5hUsed := math.Round(fleetGauge.FiveHourUsedPct*ratio*10) / 10
			org5hRemaining := math.Max(0, math.Round((100.0-org5hUsed)*10)/10)
			orgWeeklyUsed := math.Round(fleetGauge.WeeklyUsedPct*ratio*10) / 10
			orgWeeklyRemaining := math.Max(0, math.Round((100.0-orgWeeklyUsed)*10)/10)

			orgGauge := &ProviderQuotaGauge{
				Provider:             fleetGauge.Provider,
				DisplayName:          fleetGauge.DisplayName,
				FiveHourUsedPct:      org5hUsed,
				FiveHourRemainingPct: org5hRemaining,
				FiveHourResetsAt:     fleetGauge.FiveHourResetsAt,
				WeeklyUsedPct:        orgWeeklyUsed,
				WeeklyRemainingPct:   orgWeeklyRemaining,
				WeeklyResetsAt:       fleetGauge.WeeklyResetsAt,
				BurnRate5h:           math.Round(fleetGauge.BurnRate5h*ratio*100) / 100,
				BurnRateWeekly:       math.Round(fleetGauge.BurnRateWeekly*ratio*100) / 100,
				LockoutThresholdPct:  fleetGauge.LockoutThresholdPct,
				IsLocked:             fleetGauge.IsLocked,
				LockoutReason:        fleetGauge.LockoutReason,
				LockoutUntil:         fleetGauge.LockoutUntil,
				RunwayTurns:          fleetGauge.RunwayTurns,
			}

			if fleetGauge.IsLocked {
				// Managed Solution is an enterprise work org: check claude_work first before locking claude/personal
				if (org.Name == "Managed Solution" || strings.Contains(strings.ToLower(org.Name), "managed")) && (key == "claude_personal" || key == "claude") {
					workGauge := overview.ProviderQuotas["claude_work"]
					if workGauge != nil && !workGauge.IsLocked {
						// Work quota has headroom; do not lock org out on Claude
						orgGauge.IsLocked = false
						orgGauge.ProjectionStatus = "on_track"
						orgGauge.ProjectionMessage = "Claude Work prioritized (healthy headroom)"
						org.ProviderQuotas[key] = orgGauge
						continue
					}
				}
				orgGauge.ProjectionStatus = "locked_out"
				if fleetGauge.FiveHourResetsAt != nil && fleetGauge.FiveHourResetsAt.After(now) {
					orgGauge.ProjectionMessage = fmt.Sprintf("Locked out: resets in %s", formatDuration(fleetGauge.FiveHourResetsAt.Sub(now)))
				} else {
					orgGauge.ProjectionMessage = "Locked out: quota limit reached"
				}
			} else if org5hRemaining < 20.0 {
				orgGauge.ProjectionStatus = "overpaced"
				orgGauge.ProjectionMessage = fmt.Sprintf("High usage: %.1f%% used by %s", org5hUsed, org.Name)
			} else {
				orgGauge.ProjectionStatus = "on_track"
				orgGauge.ProjectionMessage = fmt.Sprintf("Healthy headroom: %.1f%% remaining", org5hRemaining)
			}

			org.ProviderQuotas[key] = orgGauge
		}
	}
}
