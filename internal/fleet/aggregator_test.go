package fleet

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test in-memory db: %v", err)
	}

	// Insert test tasks across organizations
	_, err = store.DB().Exec(`
		INSERT INTO tasks (id, name, repo_path, organization, project, status, execution_stage, is_blocked, spent_usd, spent_tokens)
		VALUES
		('task-1', 'Build Fleet UI', '/repo/sta', 'StayPoint', 'Core', 'active', 'in_progress', 0, 12.50, 50000),
		('task-2', 'Review PR 122', '/repo/sta', 'StayPoint', 'Core', 'active', 'todo', 0, 0.0, 0),
		('task-3', 'Blocked Database Migration', '/repo/sta', 'StayPoint', 'Core', 'active', 'blocked', 1, 3.20, 12000),
		('task-4', 'Completed Auth Fix', '/repo/sta', 'StayPoint', 'Core', 'done', 'done', 0, 8.40, 30000),
		('task-5', 'Deploy Enterprise Gateway', '/repo/man', 'Managed Solution', 'Cloud', 'active', 'in_progress', 0, 45.00, 150000),
		('task-6', 'Sync Client Repos', '/repo/man', 'Managed Solution', 'Platform', 'active', 'todo', 0, 0.0, 0),
		('task-7', 'Failed Deployment Rollback', '/repo/man', 'Managed Solution', 'Cloud', 'active', 'failed', 0, 5.00, 18000),
		('task-8', 'Cancelled Maintenance Run', '/repo/per', 'Personal', 'Infra', 'soft_deleted', 'cancelled', 0, 0.0, 0);
	`)
	if err != nil {
		t.Fatalf("failed to seed tasks: %v", err)
	}

	// Insert test agent sessions
	_, err = store.DB().Exec(`
		INSERT INTO agent_sessions (id, agent_type, status, repo_path, last_heartbeat_at)
		VALUES
		('sess-1', 'claude', 'active', '/home/user/staypoint', '2026-09-30T07:00:00Z'),
		('sess-2', 'gemini', 'active', '/home/user/staypoint', '2026-09-30T07:05:00Z'),
		('sess-3', 'codex', 'active', '/home/user/mansol-repo', '2026-09-30T07:02:00Z'),
		('sess-4', 'claude', 'idle', '/home/user/other-repo', '2026-09-30T06:00:00Z');
	`)
	if err != nil {
		t.Fatalf("failed to seed agent sessions: %v", err)
	}

	// Insert quota windows
	_, err = store.DB().Exec(`
		INSERT INTO quota_windows (pool_key, window_type, used_percent, remaining_pct, is_locked, resets_at)
		VALUES
		('gemini', 'rolling_5h', 14.5, 85.5, 0, '2026-09-30T12:00:00Z'),
		('gemini', 'weekly_7d', 42.0, 58.0, 0, '2026-10-05T00:00:00Z'),
		('claude', 'rolling_5h', 92.0, 8.0, 0, '2026-09-30T10:00:00Z'),
		('claude', 'weekly_7d', 85.0, 15.0, 0, '2026-10-04T00:00:00Z'),
		('codex', 'rolling_5h', 100.0, 0.0, 1, '2026-09-30T09:30:00Z'),
		('codex', 'weekly_7d', 70.0, 30.0, 0, '2026-10-03T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("failed to seed quota windows: %v", err)
	}

	return store.DB()
}

func setupTestTelemetryDBFile(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "telemetry.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to create test telemetry db: %v", err)
	}
	defer conn.Close()

	_, err = conn.Exec(`
		CREATE TABLE requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			model TEXT,
			model_family TEXT,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cache_read_tokens INTEGER,
			cache_creation_tokens INTEGER,
			total_tokens INTEGER,
			cost_usd REAL,
			ts TEXT
		);
		INSERT INTO requests (model, model_family, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, cost_usd, ts)
		VALUES
		('claude-3-7-sonnet', 'claude', 100000, 20000, 5000, 1000, 126000, 0.60, '2026-09-30T07:00:00Z'),
		('gemini-2.0-flash', 'gemini', 500000, 50000, 0, 0, 550000, 0.07, '2026-09-30T07:10:00Z'),
		('o1-preview', 'codex', 20000, 5000, 0, 0, 25000, 0.60, '2026-09-30T07:15:00Z');
	`)
	if err != nil {
		t.Fatalf("failed to seed test telemetry requests: %v", err)
	}

	return dbPath
}

func TestAggregatorGather(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.Close()

	agg := &Aggregator{
		DB:              testDB,
		TelemetryDBPath: "", // will use fallback task spend
		PaperclipClient: nil,
		RateLimitsPath:  "",
		Now: func() time.Time {
			return time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
		},
	}

	overview, err := agg.Gather(context.Background())
	if err != nil {
		t.Fatalf("unexpected error gathering fleet overview: %v", err)
	}

	// 1. Verify Global Tasks
	if overview.GlobalTasks.Running != 2 {
		t.Errorf("expected 2 running tasks, got %d", overview.GlobalTasks.Running)
	}
	if overview.GlobalTasks.Active != 2 {
		t.Errorf("expected 2 active tasks, got %d", overview.GlobalTasks.Active)
	}
	if overview.GlobalTasks.Blocked != 1 {
		t.Errorf("expected 1 blocked task, got %d", overview.GlobalTasks.Blocked)
	}
	if overview.GlobalTasks.Errored != 1 {
		t.Errorf("expected 1 errored task, got %d", overview.GlobalTasks.Errored)
	}
	if overview.GlobalTasks.Done != 1 {
		t.Errorf("expected 1 done task, got %d", overview.GlobalTasks.Done)
	}

	// 2. Verify Organizations presence
	var foundStayPoint, foundManaged bool
	for _, org := range overview.Organizations {
		if org.Name == "StayPoint" {
			foundStayPoint = true
			if org.TaskCounts.Running != 1 {
				t.Errorf("expected 1 running task in StayPoint, got %d", org.TaskCounts.Running)
			}
			if org.TaskCounts.Blocked != 1 {
				t.Errorf("expected 1 blocked task in StayPoint, got %d", org.TaskCounts.Blocked)
			}
		}
		if org.Name == "Managed Solution" {
			foundManaged = true
			if org.TaskCounts.Running != 1 {
				t.Errorf("expected 1 running task in Managed Solution, got %d", org.TaskCounts.Running)
			}
			if org.TaskCounts.Errored != 1 {
				t.Errorf("expected 1 errored task in Managed Solution, got %d", org.TaskCounts.Errored)
			}
		}
	}
	if !foundStayPoint {
		t.Errorf("expected StayPoint organization in fleet summary")
	}
	if !foundManaged {
		t.Errorf("expected Managed Solution organization in fleet summary")
	}

	// 3. Verify Active Running Agents
	if overview.GlobalAgents.ActiveRunning != 3 {
		t.Errorf("expected 3 active running agents, got %d", overview.GlobalAgents.ActiveRunning)
	}
	if overview.GlobalAgents.ByProvider["claude"] != 1 {
		t.Errorf("expected 1 active claude agent, got %d", overview.GlobalAgents.ByProvider["claude"])
	}
	if overview.GlobalAgents.ByProvider["gemini"] != 1 {
		t.Errorf("expected 1 active gemini agent, got %d", overview.GlobalAgents.ByProvider["gemini"])
	}
	if overview.GlobalAgents.ByProvider["openai"] != 1 {
		t.Errorf("expected 1 active openai agent, got %d", overview.GlobalAgents.ByProvider["openai"])
	}

	// 4. Verify 5-Hour Rolling Quotas & Lockout Gauges
	gemGauge := overview.ProviderQuotas["gemini"]
	if gemGauge == nil {
		t.Fatalf("missing gemini quota gauge")
	}
	if gemGauge.FiveHourRemainingPct != 85.5 {
		t.Errorf("expected 85.5%% 5h remaining for gemini, got %.1f", gemGauge.FiveHourRemainingPct)
	}
	if gemGauge.ProjectionStatus != "on_track" {
		t.Errorf("expected on_track status for gemini, got %s", gemGauge.ProjectionStatus)
	}

	claudeGauge := overview.ProviderQuotas["claude"]
	if claudeGauge == nil {
		t.Fatalf("missing claude quota gauge")
	}
	if claudeGauge.FiveHourRemainingPct != 8.0 {
		t.Errorf("expected 8.0%% 5h remaining for claude, got %.1f", claudeGauge.FiveHourRemainingPct)
	}
	if claudeGauge.ProjectionStatus != "overpaced" {
		t.Errorf("expected overpaced status for claude, got %s", claudeGauge.ProjectionStatus)
	}

	openaiGauge := overview.ProviderQuotas["openai"]
	if openaiGauge == nil {
		t.Fatalf("missing openai quota gauge")
	}
	if !openaiGauge.IsLocked {
		t.Errorf("expected openai gauge to be locked")
	}
	if openaiGauge.ProjectionStatus != "locked_out" {
		t.Errorf("expected locked_out status for openai, got %s", openaiGauge.ProjectionStatus)
	}
}

func TestAggregatorWithTelemetryDB(t *testing.T) {
	testDB := setupTestDB(t)
	defer testDB.Close()

	telemDBPath := setupTestTelemetryDBFile(t)

	agg := &Aggregator{
		DB:              testDB,
		TelemetryDBPath: telemDBPath,
		Now: func() time.Time {
			return time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
		},
	}

	overview, err := agg.Gather(context.Background())
	if err != nil {
		t.Fatalf("unexpected error gathering fleet overview: %v", err)
	}

	// Verify token telemetry aggregation
	if overview.TokenTelemetry.TotalTokens != 701000 {
		t.Errorf("expected 701,000 total tokens, got %d", overview.TokenTelemetry.TotalTokens)
	}
	if overview.TokenTelemetry.InputTokens != 620000 {
		t.Errorf("expected 620,000 input tokens, got %d", overview.TokenTelemetry.InputTokens)
	}
	if overview.TokenTelemetry.OutputTokens != 75000 {
		t.Errorf("expected 75,000 output tokens, got %d", overview.TokenTelemetry.OutputTokens)
	}
	if overview.TokenTelemetry.TotalCostUSD <= 0 {
		t.Errorf("expected positive total cost USD, got %.2f", overview.TokenTelemetry.TotalCostUSD)
	}

	// Verify model breakdown
	if len(overview.ModelSpend) != 3 {
		t.Fatalf("expected 3 models in spend breakdown, got %d", len(overview.ModelSpend))
	}
}
