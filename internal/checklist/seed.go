package checklist

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// DefaultChecklist returns the default verification items with machine contracts.
func DefaultChecklist(sprint string) []Item {
	type row struct{ section, title, desc, howTo, contract string }
	rows := []row{
		// Infrastructure
		{"Infrastructure", "Binary rebuilt from feat/webui-cookie-auth (e3a39ba)",
			"The go:embed binary must include all STA-168 UI changes.",
			"ls -la ~/.local/bin/staypointd — mtime should be Sep 30 09:52+", ""},
		{"Infrastructure", "Daemon restarted with new binary (PID 34952+)",
			"Old PID 73862 should no longer be active.",
			"lsof -i :41421 — PID should be 34952 or newer+", ""},
		{"Infrastructure", "go build ./... passes clean",
			"No compilation errors in any package.",
			"cd ~/Documents/dev/agent-mesh && go build ./... — exit 0",
			`{"type":"command","command":"go build ./..."}`},
		{"Infrastructure", "feat/webui-cookie-auth pushed to remote (e3a39ba)",
			"Remote branch should include STA-168 commit.",
			"git log --oneline -1 in agent-mesh repo", ""},

		// Bug 1
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Work) card",
			"Separate card for work seat Claude pool.",
			"All Organizations view → 5-Hour Rolling Quota section → look for 'Claude (Work)'",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["claude_work"]}`},
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Personal) card",
			"Separate card for personal seat Claude pool.",
			"Same section → look for 'Claude (Personal)'",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["claude_personal"]}`},
		{"Bug 1 — Two Claude Accounts", "Old combined 'Claude' card absent when split cards present",
			"Should not show a third duplicate card.",
			"Count cards in quota grid — should be 2 Claude cards not 3", ""},

		// Bug 2
		{"Bug 2 — Blocked via TUI", "Blocked task does NOT show 'Blocked via TUI' in detail panel",
			"normalizeBlockReason() strips the literal TUI string.",
			"Open any blocked task → detail panel → look for orange blocker tag",
			`{"type":"file_pattern","file_path":"internal/fleet/aggregator.go","must_contain":["normalizeBlockReason","blocked via tui"]}`},
		{"Bug 2 — Blocked via TUI", "Blocked reason shows generic text or real reason",
			"Should show 'Blocked — no specific reason recorded' or actual reason.",
			"Same as above — check text in blocker tag", ""},

		// Bug 3 (not fixed)
		{"Bug 3 — Gemini Cost (NOT FIXED)", "Cost page shows Gemini spend > $0",
			"Telemetry watcher does not yet record Gemini cost_usd. This item should FAIL until fixed.",
			"Cost & Accounting → Spend by Provider → Gemini row", ""},

		// Bug 4
		{"Bug 4 — PDF Spinner", "Report download button shows spinner while downloading",
			"Boss Card → Download Report dropdown → click a type → button should disable + spin.",
			"Boss Card view → Download Report → Combined Fleet — watch button state", ""},

		// Bug 5
		{"Bug 5 — Spend Breakdown", "Cost page has per-model spend card",
			"Spend by Model card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Model",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Model"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has per-organization spend card",
			"Spend by Organization card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Organization",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Organization"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has per-provider spend card",
			"Spend by Provider card — Claude, Gemini, OpenAI.",
			"Sidebar → Cost & Accounting → Spend by Provider",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["Spend by Provider"]}`},
		{"Bug 5 — Spend Breakdown", "Cost page has Top Tasks by Spend card",
			"Lists tasks with spent_usd > 0.",
			"Sidebar → Cost & Accounting → Top Tasks by Spend", ""},

		// Projects
		{"Projects Page", "Sidebar → Projects navigates to projects view", "", "", ""},
		{"Projects Page", "Tasks grouped by project field into cards", "", "", ""},
		{"Projects Page", "Project card shows running / blocked / done / spend stats", "", "", ""},
		{"Projects Page", "Project card shows preview of up to 5 tasks", "", "", ""},
		{"Projects Page", "Clicking a task in preview opens detail panel", "", "", ""},
		{"Projects Page", "Org filter dropdown narrows projects", "", "", ""},
		{"Projects Page", "Tasks with no project field group under (No Project)", "", "", ""},

		// Agents
		{"Agents Page", "Sidebar → Agents navigates to agents view", "", "", ""},
		{"Agents Page", "Agent cards show provider badge", "", "", ""},
		{"Agents Page", "Agent cards show status pill", "", "", ""},
		{"Agents Page", "Agent cards show 5h quota bar", "", "", ""},
		{"Agents Page", "Agent cards show last heartbeat time", "", "", ""},
		{"Agents Page", "Search input filters agents by name/role", "", "", ""},
		{"Agents Page", "Provider filter dropdown works", "", "", ""},

		// Recent Tasks
		{"Recent Tasks Page", "Sidebar → Recent Tasks navigates to activity feed", "", "", ""},
		{"Recent Tasks Page", "Feed sorted by updated_at descending", "", "", ""},
		{"Recent Tasks Page", "Dots colored by status (cyan/red/green)", "", "", ""},
		{"Recent Tasks Page", "Clicking task title opens detail panel", "", "", ""},
		{"Recent Tasks Page", "Project, Organization, and Priority dropdown filters work", "", "",
			`{"type":"file_pattern","file_path":"internal/server/webui/index.html","must_contain":["recent-tasks-project-filter","recent-tasks-org-filter","recent-tasks-priority-filter"]}`},
		{"Recent Tasks Page", "Subtask tree expansion displays child issues nested under parent tasks", "", "",
			`{"type":"file_pattern","file_path":"internal/server/webui/app.js","must_contain":["activity-subtasks-tree","activity-subtask-toggle","renderSubtaskTree"]}`},

		// Task Status
		{"Task Status Page", "Sidebar → Task Status shows 9-column table", "", "", ""},
		{"Task Status Page", "Table has Identifier / Task / Org / Project / Assignee / Status / Priority / Cost / Updated", "", "", ""},
		{"Task Status Page", "Search input filters table", "", "", ""},
		{"Task Status Page", "Org filter works", "", "", ""},
		{"Task Status Page", "Status filter works", "", "", ""},
		{"Task Status Page", "Row click opens detail panel", "", "", ""},

		// Cost
		{"Cost & Accounting Page", "Sidebar → Cost & Accounting navigates", "", "", ""},
		{"Cost & Accounting Page", "KPI row shows 5 metrics", "", "", ""},
		{"Cost & Accounting Page", "All 4 spend cards render", "", "", ""},

		// Settings
		{"Settings Page", "Sidebar → Settings navigates", "", "", ""},
		{"Settings Page", "Connection section shows endpoint + auth + SSE status", "", "", ""},
		{"Settings Page", "Provider Accounts section lists quota pools", "", "", ""},
		{"Settings Page", "Fleet Info section shows counts", "", "", ""},

		// Detail Panel
		{"Detail Panel", "Clicking task opens right-side detail panel", "", "", ""},
		{"Detail Panel", "Panel shows title / status / identifier / priority / org", "", "", ""},
		{"Detail Panel", "Panel renders description as markdown", "", "", ""},
		{"Detail Panel", "Panel shows Stage / Assignee / Project / Goal / Repo / Labels", "", "", ""},
		{"Detail Panel", "Panel shows Spend and Budget if present", "", "", ""},
		{"Detail Panel", "Panel shows normalized blocker tag for blocked tasks", "", "", ""},
		{"Detail Panel", "Panel shows timestamps", "", "", ""},
		{"Detail Panel", "Chat section shows comments thread", "", "", ""},
		{"Detail Panel", "Chat compose box sends message (⌘↵)", "", "", ""},
		{"Detail Panel", "Close button (×) works", "", "", ""},

		// Stubs
		{"Phase 2 Stubs", "Sidebar → Routines shows stub page with 'Coming in Phase 2'", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Artifacts shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Skills shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Connectors shows stub page", "", "", ""},
		{"Phase 2 Stubs", "Sidebar → Audit shows stub page", "", "", ""},

		// Architecture
		{"Architecture (NOT DONE — STA-167)", "Web UI reads tasks from native StayPoint SQLite",
			"Currently Paperclip-proxied. handlers_telemetry.go GetFleetTask/GetFleetTaskComments forward to 127.0.0.1:3100. This item should FAIL until STA-167 is done.",
			"Check handlers_telemetry.go — proxyPaperclip() must be replaced",
			`{"type":"file_pattern","file_path":"internal/server/handlers_telemetry.go","must_not_contain":["proxyPaperclip"]}`},

		// Regression
		{"Regression — Existing Views", "All Organizations overview loads with KPI cards", "", "", ""},
		{"Regression — Existing Views", "5-Hour Quota section renders gauge cards", "", "", ""},
		{"Regression — Existing Views", "Organization cards grid renders", "", "", ""},
		{"Regression — Existing Views", "Clicking org card opens org detail", "", "", ""},
		{"Regression — Existing Views", "Org detail shows tasks / agents / spend", "", "", ""},
		{"Regression — Existing Views", "Kanban board renders 4 columns", "", "", ""},
		{"Regression — Existing Views", "Boss Card renders stat grid + download button", "", "", ""},
		{"Regression — Existing Views", "SSE live badge shows 'live'", "", "", ""},
		{"Regression — Existing Views", "Quick filter buttons work in overview", "", "", ""},
		{"Regression — Existing Views", "Sidebar org tree populates", "", "", ""},
		{"Regression — Existing Views", "Clicking org in sidebar tree opens org detail", "", "", ""},
	}

	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, Item{
			ID:          uuid.NewSHA1(uuid.NameSpaceURL, []byte(sprint+":"+r.section+":"+r.title)).String(),
			Sprint:      sprint,
			Section:     r.section,
			Title:       r.title,
			Description: r.desc,
			HowToTest:   r.howTo,
			Contract:    r.contract,
			Status:      "pending",
		})
	}
	return out
}

// Seed populates the database with default checklist items.
func Seed(ctx context.Context, dbConn *sql.DB, sprint string, force bool) (int, int, error) {
	if sprint == "" {
		sprint = "STA-168"
	}

	// Ensure table has contract column
	var contractCount int
	_ = dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('checklist_items') WHERE name='contract'").Scan(&contractCount)
	if contractCount == 0 {
		_, _ = dbConn.ExecContext(ctx, "ALTER TABLE checklist_items ADD COLUMN contract TEXT;")
	}

	var count int
	_ = dbConn.QueryRowContext(ctx, `SELECT COUNT(*) FROM checklist_items WHERE sprint=?`, sprint).Scan(&count)
	if count > 0 && !force {
		// Update contracts on existing items
		items := DefaultChecklist(sprint)
		for _, it := range items {
			if it.Contract != "" {
				_, _ = dbConn.ExecContext(ctx,
					`UPDATE checklist_items SET contract=? WHERE sprint=? AND section=? AND title=?`,
					it.Contract, sprint, it.Section, it.Title)
			}
		}
		return 0, count, nil
	}

	items := DefaultChecklist(sprint)
	tx, err := dbConn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if force {
		if _, err := tx.ExecContext(ctx, `DELETE FROM checklist_items WHERE sprint=?`, sprint); err != nil {
			return 0, 0, fmt.Errorf("failed to reset sprint items: %w", err)
		}
	}

	for _, it := range items {
		if it.ID == "" {
			it.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(sprint+":"+it.Section+":"+it.Title)).String()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO checklist_items (id, sprint, section, title, description, how_to_test, contract, status) VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET contract=excluded.contract`,
			it.ID, sprint, it.Section, it.Title, it.Description, it.HowToTest, it.Contract, "pending",
		); err != nil {
			return 0, 0, fmt.Errorf("seed failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(items), 0, nil
}
