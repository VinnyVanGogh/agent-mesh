package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/VinnyVanGogh/staypoint/internal/bridge"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
	"github.com/VinnyVanGogh/staypoint/internal/router"
	"github.com/VinnyVanGogh/staypoint/internal/telemetry"
	"github.com/VinnyVanGogh/staypoint/internal/wire"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Staypoint lifecycle and prompt hooks for Claude Code and Antigravity",
}

var hookPromptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Claude Code UserPromptSubmit hook: monitors 5h limit & stages handoff",
	Run: func(cmd *cobra.Command, args []string) {
		handleHookPrompt()
	},
}

var hookPromptFormat string

func handleHookPrompt() {
	var rawInput []byte
	stat, err := os.Stdin.Stat()
	if err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		rawInput, _ = io.ReadAll(os.Stdin)
	}

	var promptText string
	var sessionID string
	var payload map[string]interface{}
	isAntigravity := false

	if len(rawInput) > 0 {
		if err := json.Unmarshal(rawInput, &payload); err == nil {
			if p, ok := payload["prompt"].(string); ok {
				promptText = p
			}
			if s, ok := payload["sessionId"].(string); ok && s != "" {
				sessionID = s
			} else if s, ok := payload["session_id"].(string); ok && s != "" {
				sessionID = s
			} else if c, ok := payload["conversationId"].(string); ok && c != "" {
				sessionID = c
			}
			if _, ok := payload["conversationId"]; ok {
				isAntigravity = true
			}
			if _, ok := payload["invocationNum"]; ok {
				isAntigravity = true
			}
			if _, ok := payload["workspacePaths"]; ok {
				isAntigravity = true
			}
		} else {
			promptText = string(rawInput)
		}
	}

	if hookPromptFormat == "gemini" {
		isAntigravity = true
	} else if hookPromptFormat == "claude" {
		isAntigravity = false
	}

	if sessionID == "" {
		if s := os.Getenv("CLAUDE_SESSION_ID"); s != "" {
			sessionID = s
		} else if s := os.Getenv("GEMINI_SESSION_ID"); s != "" {
			sessionID = s
		} else if s := os.Getenv("STAYPOINT_SESSION_ID"); s != "" {
			sessionID = s
		} else if s := os.Getenv("MESH_SESSION_ID"); s != "" {
			sessionID = s
		} else {
			sessionID = fmt.Sprintf("session-pid-%d", os.Getppid())
		}
	}

	cwd, _ := os.Getwd()
	if payload != nil {
		if wsPaths, ok := payload["workspacePaths"].([]interface{}); ok && len(wsPaths) > 0 {
			if firstPath, ok := wsPaths[0].(string); ok && firstPath != "" {
				cwd = firstPath
			}
		}
	}
	var notices []string

	// Open staypoint database
	var dbConn *sql.DB
	if cfg != nil && cfg.DBPath != "" {
		if store, err := db.Open(cfg.DBPath); err == nil {
			dbConn = store.DB()
			defer store.Close()
		}
	}

	if dbConn != nil {
		// A. Register / Heartbeat Session
		branch := meshContext.GetCurrentGitBranch(cwd)
		_ = telemetry.HeartbeatSession(dbConn, telemetry.AgentSession{
			ID:        sessionID,
			AgentType: "claude",
			RepoPath:  cwd,
			GitBranch: branch,
			PID:       os.Getppid(),
		})

		// Asynchronously update base handoff as turn begins
		go func() {
			maxKeep := 3
			if cfg != nil && cfg.MaxHandoffsPerRepo > 0 {
				maxKeep = cfg.MaxHandoffsPerRepo
			}
			dataDir := ""
			if cfg != nil {
				dataDir = cfg.DataDir
			}
			_, _ = meshContext.AutoGenerateHandoffForSession(sessionID, cwd, "active_session", dbConn, dataDir, maxKeep)
		}()

		// B. Inspect working tree for multi-agent collision detection
		gitCtx := meshContext.GatherGitContext(cwd)
		var dirtyFiles []string
		for _, f := range gitCtx.ModifiedFiles {
			cleanF := cleanGitStatusFile(f)
			if cleanF != "" {
				dirtyFiles = append(dirtyFiles, cleanF)
				_ = telemetry.RecordWorkingFile(dbConn, sessionID, cwd, cleanF, "write", 15*time.Minute)
			}
		}

		if len(dirtyFiles) > 0 {
			collisions, _ := telemetry.CheckCollisions(dbConn, sessionID, cwd, dirtyFiles)
			if len(collisions) > 0 {
				var collLines []string
				for _, c := range collisions {
					ago := time.Since(c.LastTouchedAt).Round(time.Second)
					collLines = append(collLines, fmt.Sprintf("  • %s (touched %s ago by agent %s [session: %s, PID: %d])", c.FilePath, ago, c.OtherAgent, c.OtherSessionID, c.OtherPID))
				}
				notices = append(notices, fmt.Sprintf("⚠️ [STAYPOINT MULTI-AGENT COLLISION WARNING]: Another agent session is actively editing overlapping files in this repository:\n%s\nCoordinate with the user or wait for peer completion before editing or committing these files to prevent conflicts.", strings.Join(collLines, "\n")))
			}
		}

		// C. Agent Circuit Breaker check
		cb, _ := telemetry.GetCircuitBreaker(dbConn, sessionID)
		if cb == nil {
			cbs, _ := telemetry.ListCircuitBreakers(dbConn, cwd, true)
			if len(cbs) > 0 {
				cb = &cbs[0]
			}
		}
		if cb != nil && cb.IsTripped {
			notices = append(notices, fmt.Sprintf("🚨 [STAYPOINT CIRCUIT BREAKER ACTIVE]: Execution pause active because an agent loop was detected (%s on %s).\nLast error: %s\nTo reset and proceed, run: staypoint breaker reset %s", cb.FailingTool, cb.FailingCommand, cb.LastError, cb.SessionID))
		}

		// D. Task Budget Evaluation
		activeTask, _ := meshContext.GetActiveTaskForRepo(dbConn, cwd)
		if activeTask != nil {
			eval := meshContext.EvaluateTaskBudget(activeTask)
			if eval.IsBlocked {
				fmt.Fprintf(os.Stderr, "❌ [STAYPOINT TASK BUDGET EXCEEDED]\nTask %q budget limit reached: %s\nSpent: $%.2f / $%.2f (%d / %d turns)\nHalting execution to prevent runaway costs.\nTo increase budget, run: staypoint task budget %s --usd <limit>\n", activeTask.Name, eval.Reason, activeTask.SpentUSD, activeTask.MaxBudgetUSD, activeTask.SpentTurns, activeTask.MaxTurns, activeTask.ID)
				os.Exit(2)
			} else if eval.IsWarning {
				notices = append(notices, fmt.Sprintf("⚠️ [STAYPOINT TASK BUDGET WARNING]: Task %q is at %.1f%% of budget ($%.2f / $%.2f max, %d / %d turns). %s", activeTask.Name, eval.PctBudget, activeTask.SpentUSD, activeTask.MaxBudgetUSD, activeTask.SpentTurns, activeTask.MaxTurns, eval.Reason))
			}
		}

		// E. Check unread Mesh Wire broadcasts
		unreadMsgs, _ := wire.GetUnread(dbConn, sessionID, cwd)
		if len(unreadMsgs) > 0 {
			var wireLines []string
			for _, m := range unreadMsgs {
				wireLines = append(wireLines, fmt.Sprintf("  • [%s] <%s>: %s", m.Channel, m.Author, m.Content))
			}
			notices = append(notices, fmt.Sprintf("📡 [STAYPOINT WIRE :: PEER AGENT BROADCASTS]:\n%s", strings.Join(wireLines, "\n")))
		}

		// F. Check for previous session handoffs in repo to present proactive pickup banner
		baseHandoffsDir := meshContext.GetHandoffsDir(cfg.DataDir)
		if latestMan, err := meshContext.GetLatestManifest(baseHandoffsDir, cwd); err == nil && latestMan != nil {
			if latestMan.SessionID != sessionID && time.Since(latestMan.CreatedAt) < 4*time.Hour {
				cleanSess := strings.ReplaceAll(sessionID, "/", "_")
				pickupDebounce := filepath.Join(os.TempDir(), fmt.Sprintf("staypoint-pickup-warned-%s.ts", cleanSess))
				if _, err := os.Stat(pickupDebounce); os.IsNotExist(err) {
					_ = os.WriteFile(pickupDebounce, []byte(fmt.Sprintf("%d", time.Now().Unix())), 0644)
					age := time.Since(latestMan.CreatedAt).Round(time.Minute)
					branchInfo := latestMan.GitBranch
					if branchInfo != "" {
						branchInfo = fmt.Sprintf(" on branch [%s]", branchInfo)
					}
					goalInfo := latestMan.Goal
					if goalInfo == "" {
						goalInfo = latestMan.Title
					}
					notices = append(notices, fmt.Sprintf("📋 [STAYPOINT PREVIOUS CONTEXT AVAILABLE]: Recent handoff from session %s (%s ago%s) found.\nGoal: %s\nTo inspect or adopt this context, view: %s or run: staypoint handoff show %s", latestMan.SessionID, age, branchInfo, goalInfo, latestMan.HandoffFile, latestMan.SessionID))
				}
			}
		}
	}

	// 1. Programmatically inspect prompt for client machine paths and auto-fetch them
	if promptText != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		guidance, _, _ := bridge.ProcessPromptForClientPaths(ctx, promptText)
		cancel()
		if guidance != "" {
			notices = append(notices, guidance)
		}
	}

	// 2. Quota notice check
	pacerState, err := router.LoadPacerState()
	if err == nil {
		poolPersonal := pacerState.Pools[router.PoolPersonalClaude]
		pool3P := pacerState.Pools[router.Pool3PClaude]
		poolWork := pacerState.Pools[router.PoolWorkClaude]

		var triggeredPool *router.QuotaPool
		var warningReason string

		checkPool := func(pool *router.QuotaPool) bool {
			if pool == nil {
				return false
			}
			if pool.FiveHour.UsedPct >= 85.0 || (pool.FiveHour.RemainingPct > 0 && pool.FiveHour.RemainingPct <= 15.0) {
				resetStr := "soon"
				if !pool.FiveHour.ResetsAt.IsZero() {
					resetStr = pool.FiveHour.ResetsAt.Format("3:04pm")
				} else if !pool.LockoutUntil.IsZero() {
					resetStr = pool.LockoutUntil.Format("3:04pm")
				}
				warningReason = fmt.Sprintf("5-hour session quota is at %.0f%% (~%.0f%% left, resets @%s)", pool.FiveHour.UsedPct, pool.FiveHour.RemainingPct, resetStr)
				return true
			}
			if pool.Weekly.UsedPct >= 85.0 || (pool.Weekly.RemainingPct > 0 && pool.Weekly.RemainingPct <= 15.0) {
				resetStr := "soon"
				if !pool.Weekly.ResetsAt.IsZero() {
					resetStr = pool.Weekly.ResetsAt.Format("Mon 3:04pm")
				}
				warningReason = fmt.Sprintf("weekly quota is at %.0f%% (only %.0f%% remaining, resets @%s)", pool.Weekly.UsedPct, pool.Weekly.RemainingPct, resetStr)
				return true
			}
			if pool.IsLocked {
				warningReason = fmt.Sprintf("quota is currently locked (%s)", pool.LockoutReason)
				return true
			}
			return false
		}

		if checkPool(poolPersonal) {
			triggeredPool = poolPersonal
		} else if checkPool(pool3P) {
			triggeredPool = pool3P
		} else if checkPool(poolWork) {
			triggeredPool = poolWork
		}

		if triggeredPool != nil {
			debounceFile := filepath.Join(os.TempDir(), fmt.Sprintf("staypoint-prelock-warned-u%d.ts", os.Getuid()))
			shouldNotify := true
			if stat, err := os.Stat(debounceFile); err == nil {
				if time.Since(stat.ModTime()) < 15*time.Minute {
					shouldNotify = false
				}
			}

			if shouldNotify {
				_ = os.WriteFile(debounceFile, []byte(fmt.Sprintf("%d", time.Now().Unix())), 0644)
				_, _ = meshContext.GenerateHandoff(meshContext.HandoffOptions{
					TargetModel:       "gemini",
					ImmediateNextStep: fmt.Sprintf("Approaching quota limit: %s. Resume session seamlessly in Gemini.", warningReason),
					Directory:         cwd,
					DB:                dbConn,
				})

				telemetry.SendNotification(
					"[Staypoint] Quota Limit Warning (15% left)",
					fmt.Sprintf("%s %s. Handoff staged in clipboard. Switch to Gemini (/model gemini-3.8-flash-high or open agy and paste).", triggeredPool.Name, warningReason),
				)
			}

			notices = append(notices, fmt.Sprintf("⚠️ [STAYPOINT QUOTA NOTICE]: %s %s. Staypoint has pre-staged a zero-token context handoff snapshot in your system clipboard and /tmp/ai-handoff.md. Remind the user to prepare to switch to Gemini (/model gemini-3.8-flash-high or open Antigravity 'agy' and paste) before running out of turns.", triggeredPool.Name, warningReason))
		}
	}

	// 3. Drain pending code reviews from Claude Code for Gemini
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		pendingGeminiDir := filepath.Join(homeDir, ".claude", "reviews", "pending-gemini")
		if entries, err := os.ReadDir(pendingGeminiDir); err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") {
					filePath := filepath.Join(pendingGeminiDir, e.Name())
					if data, err := os.ReadFile(filePath); err == nil {
						var rev struct {
							SHA      string `json:"sha"`
							Repo     string `json:"repo"`
							Verdict  string `json:"verdict"`
							Review   string `json:"review"`
							Reviewer string `json:"reviewer"`
						}
						if json.Unmarshal(data, &rev) == nil {
							if rev.Repo == "" || strings.EqualFold(rev.Repo, filepath.Base(cwd)) {
								notices = append(notices, fmt.Sprintf("⚖️ [CODE REVIEW FROM CLAUDE CODE on commit %s : VERDICT %s]:\n%s", rev.SHA, rev.Verdict, rev.Review))
								_ = os.Remove(filePath)
							}
						}
					}
				}
			}
		}
	}

	if isAntigravity {
		if len(notices) > 0 {
			type InjectedStep struct {
				EphemeralMessage string `json:"ephemeralMessage,omitempty"`
			}
			type HookResp struct {
				InjectSteps []InjectedStep `json:"injectSteps"`
			}
			resp := HookResp{
				InjectSteps: []InjectedStep{
					{
						EphemeralMessage: strings.Join(notices, "\n\n"),
					},
				},
			}
			out, _ := json.Marshal(resp)
			fmt.Println(string(out))
			return
		}
		fmt.Println("{}")
		return
	}

	if len(notices) > 0 {
		resp := map[string]string{
			"additionalContext": strings.Join(notices, "\n\n"),
		}
		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
		return
	}

	fmt.Println("{}")
}

var hookInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Antigravity and Claude Code lifecycle hooks for bidirectional review",
	Run: func(cmd *cobra.Command, args []string) {
		installHooks()
	},
}

func init() {
	hookPromptCmd.Flags().StringVar(&hookPromptFormat, "format", "auto", "Output format: auto, gemini, or claude")
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookPromptCmd)
	hookCmd.AddCommand(hookInstallCmd)
}
