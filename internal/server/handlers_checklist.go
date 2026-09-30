package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ChecklistHandler serves the /api/checklist endpoints.
type ChecklistHandler struct {
	db  *sql.DB
	hub *EventHub
}

func NewChecklistHandler(db *sql.DB, hub *EventHub) *ChecklistHandler {
	return &ChecklistHandler{db: db, hub: hub}
}

// ChecklistItem represents one verifiable claim.
type ChecklistItem struct {
	ID          string  `json:"id"`
	Sprint      string  `json:"sprint"`
	Section     string  `json:"section"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	HowToTest   string  `json:"how_to_test,omitempty"`
	Status      string  `json:"status"`   // pending | pass | fail | skip | not_done
	Notes       string  `json:"notes"`
	Version     int     `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// ChecklistHistoryEntry is a single audit record for a status change.
type ChecklistHistoryEntry struct {
	ID        int    `json:"id"`
	ItemID    string `json:"item_id"`
	Status    string `json:"status"`
	Notes     string `json:"notes"`
	ChangedBy string `json:"changed_by"`
	ChangedAt string `json:"changed_at"`
}

// GET /api/checklist
func (h *ChecklistHandler) ListItems(w http.ResponseWriter, r *http.Request) {
	sprint := r.URL.Query().Get("sprint")
	query := `SELECT id, sprint, section, title, description, how_to_test, status, notes, version, created_at, updated_at
		FROM checklist_items`
	args := []any{}
	if sprint != "" {
		query += " WHERE sprint = ?"
		args = append(args, sprint)
	}
	query += " ORDER BY section, rowid ASC"

	rows, err := h.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	items := []ChecklistItem{}
	for rows.Next() {
		var it ChecklistItem
		var desc, howTo, notes sql.NullString
		if err := rows.Scan(&it.ID, &it.Sprint, &it.Section, &it.Title,
			&desc, &howTo, &it.Status, &notes, &it.Version,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		it.Description = desc.String
		it.HowToTest = howTo.String
		it.Notes = notes.String
		items = append(items, it)
	}
	writeJSON(w, map[string]any{"items": items})
}

// GET /api/checklist/sprints — list distinct sprint identifiers
func (h *ChecklistHandler) ListSprints(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT DISTINCT sprint FROM checklist_items ORDER BY sprint DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var sprints []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		sprints = append(sprints, s)
	}
	writeJSON(w, map[string]any{"sprints": sprints})
}

// PATCH /api/checklist/{id}
func (h *ChecklistHandler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	var body struct {
		Status string `json:"status"`
		Notes  string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	validStatuses := map[string]bool{
		"pending": true, "pass": true, "fail": true, "skip": true, "not_done": true,
	}
	if body.Status != "" && !validStatuses[body.Status] {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Fetch current item to bump version
	var current ChecklistItem
	var desc, howTo, notes sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, sprint, section, title, description, how_to_test, status, notes, version, created_at, updated_at FROM checklist_items WHERE id = ?`, id).
		Scan(&current.ID, &current.Sprint, &current.Section, &current.Title,
			&desc, &howTo, &current.Status, &notes, &current.Version,
			&current.CreatedAt, &current.UpdatedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	current.Description = desc.String
	current.HowToTest = howTo.String
	current.Notes = notes.String

	newStatus := current.Status
	if body.Status != "" {
		newStatus = body.Status
	}
	newNotes := current.Notes
	if body.Notes != "" {
		newNotes = body.Notes
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	newVersion := current.Version + 1
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE checklist_items SET status=?, notes=?, version=?, updated_at=? WHERE id=?`,
		newStatus, newNotes, newVersion, now, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO checklist_history (item_id, status, notes, changed_by, changed_at) VALUES (?,?,?,?,?)`,
		id, newStatus, newNotes, "user", now); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	current.Status = newStatus
	current.Notes = newNotes
	current.Version = newVersion
	current.UpdatedAt = now
	writeJSON(w, current)
}

// GET /api/checklist/{id}/history
func (h *ChecklistHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, item_id, status, notes, changed_by, changed_at FROM checklist_history WHERE item_id=? ORDER BY id DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	entries := []ChecklistHistoryEntry{}
	for rows.Next() {
		var e ChecklistHistoryEntry
		var notes sql.NullString
		if err := rows.Scan(&e.ID, &e.ItemID, &e.Status, &notes, &e.ChangedBy, &e.ChangedAt); err == nil {
			e.Notes = notes.String
			entries = append(entries, e)
		}
	}
	writeJSON(w, map[string]any{"history": entries})
}

// POST /api/checklist/seed — idempotently inserts default STA-168 checklist
func (h *ChecklistHandler) Seed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sprint string `json:"sprint"`
		Force  bool   `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sprint := body.Sprint
	if sprint == "" {
		sprint = "STA-168"
	}

	// Skip if rows already exist for this sprint (unless force)
	var count int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM checklist_items WHERE sprint=?`, sprint).Scan(&count)
	if count > 0 && !body.Force {
		writeJSON(w, map[string]any{"seeded": 0, "skipped": count})
		return
	}

	items := defaultChecklist(sprint)
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	for _, it := range items {
		if it.ID == "" {
			it.ID = uuid.NewString()
		}
		if _, err := tx.ExecContext(r.Context(),
			`INSERT OR IGNORE INTO checklist_items (id, sprint, section, title, description, how_to_test, status) VALUES (?,?,?,?,?,?,?)`,
			it.ID, sprint, it.Section, it.Title, it.Description, it.HowToTest, "pending",
		); err != nil {
			writeError(w, http.StatusInternalServerError, "seed failed: "+err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"seeded": len(items)})
}

// defaultChecklist returns the STA-168 verification items.
func defaultChecklist(sprint string) []ChecklistItem {
	type row struct{ section, title, desc, howTo string }
	rows := []row{
		// Infrastructure
		{"Infrastructure", "Binary rebuilt from feat/webui-cookie-auth (e3a39ba)",
			"The go:embed binary must include all STA-168 UI changes.",
			"ls -la ~/.local/bin/staypointd — mtime should be Sep 30 09:52+"},
		{"Infrastructure", "Daemon restarted with new binary (PID 34952+)",
			"Old PID 73862 should no longer be active.",
			"lsof -i :41421 — PID should be 34952 or newer"},
		{"Infrastructure", "go build ./... passes clean",
			"No compilation errors in any package.",
			"cd ~/Documents/dev/agent-mesh && go build ./... — exit 0"},
		{"Infrastructure", "feat/webui-cookie-auth pushed to remote (e3a39ba)",
			"Remote branch should include STA-168 commit.",
			"git log --oneline -1 in agent-mesh repo"},

		// Bug 1
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Work) card",
			"Separate card for work seat Claude pool.",
			"All Organizations view → 5-Hour Rolling Quota section → look for 'Claude (Work)'"},
		{"Bug 1 — Two Claude Accounts", "Quota section shows Claude (Personal) card",
			"Separate card for personal seat Claude pool.",
			"Same section → look for 'Claude (Personal)'"},
		{"Bug 1 — Two Claude Accounts", "Old combined 'Claude' card absent when split cards present",
			"Should not show a third duplicate card.",
			"Count cards in quota grid — should be 2 Claude cards not 3"},

		// Bug 2
		{"Bug 2 — Blocked via TUI", "Blocked task does NOT show 'Blocked via TUI' in detail panel",
			"normalizeBlockReason() strips the literal TUI string.",
			"Open any blocked task → detail panel → look for orange blocker tag"},
		{"Bug 2 — Blocked via TUI", "Blocked reason shows generic text or real reason",
			"Should show 'Blocked — no specific reason recorded' or actual reason.",
			"Same as above — check text in blocker tag"},

		// Bug 3 (not fixed)
		{"Bug 3 — Gemini Cost (NOT FIXED)", "Cost page shows Gemini spend > $0",
			"Telemetry watcher does not yet record Gemini cost_usd. This item should FAIL until fixed.",
			"Cost & Accounting → Spend by Provider → Gemini row"},

		// Bug 4
		{"Bug 4 — PDF Spinner", "Report download button shows spinner while downloading",
			"Boss Card → Download Report dropdown → click a type → button should disable + spin.",
			"Boss Card view → Download Report → Combined Fleet — watch button state"},

		// Bug 5
		{"Bug 5 — Spend Breakdown", "Cost page has per-model spend card",
			"Spend by Model card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Model"},
		{"Bug 5 — Spend Breakdown", "Cost page has per-organization spend card",
			"Spend by Organization card with bar charts.",
			"Sidebar → Cost & Accounting → Spend by Organization"},
		{"Bug 5 — Spend Breakdown", "Cost page has per-provider spend card",
			"Spend by Provider card — Claude, Gemini, OpenAI.",
			"Sidebar → Cost & Accounting → Spend by Provider"},
		{"Bug 5 — Spend Breakdown", "Cost page has Top Tasks by Spend card",
			"Lists tasks with spent_usd > 0.",
			"Sidebar → Cost & Accounting → Top Tasks by Spend"},

		// Projects
		{"Projects Page", "Sidebar → Projects navigates to projects view", "", ""},
		{"Projects Page", "Tasks grouped by project field into cards", "", ""},
		{"Projects Page", "Project card shows running / blocked / done / spend stats", "", ""},
		{"Projects Page", "Project card shows preview of up to 5 tasks", "", ""},
		{"Projects Page", "Clicking a task in preview opens detail panel", "", ""},
		{"Projects Page", "Org filter dropdown narrows projects", "", ""},
		{"Projects Page", "Tasks with no project field group under (No Project)", "", ""},

		// Agents
		{"Agents Page", "Sidebar → Agents navigates to agents view", "", ""},
		{"Agents Page", "Agent cards show provider badge", "", ""},
		{"Agents Page", "Agent cards show status pill", "", ""},
		{"Agents Page", "Agent cards show 5h quota bar", "", ""},
		{"Agents Page", "Agent cards show last heartbeat time", "", ""},
		{"Agents Page", "Search input filters agents by name/role", "", ""},
		{"Agents Page", "Provider filter dropdown works", "", ""},

		// Recent Tasks
		{"Recent Tasks Page", "Sidebar → Recent Tasks navigates to activity feed", "", ""},
		{"Recent Tasks Page", "Feed sorted by updated_at descending", "", ""},
		{"Recent Tasks Page", "Dots colored by status (cyan/red/green)", "", ""},
		{"Recent Tasks Page", "Clicking task title opens detail panel", "", ""},

		// Task Status
		{"Task Status Page", "Sidebar → Task Status shows 9-column table", "", ""},
		{"Task Status Page", "Table has Identifier / Task / Org / Project / Assignee / Status / Priority / Cost / Updated", "", ""},
		{"Task Status Page", "Search input filters table", "", ""},
		{"Task Status Page", "Org filter works", "", ""},
		{"Task Status Page", "Status filter works", "", ""},
		{"Task Status Page", "Row click opens detail panel", "", ""},

		// Cost
		{"Cost & Accounting Page", "Sidebar → Cost & Accounting navigates", "", ""},
		{"Cost & Accounting Page", "KPI row shows 5 metrics", "", ""},
		{"Cost & Accounting Page", "All 4 spend cards render", "", ""},

		// Settings
		{"Settings Page", "Sidebar → Settings navigates", "", ""},
		{"Settings Page", "Connection section shows endpoint + auth + SSE status", "", ""},
		{"Settings Page", "Provider Accounts section lists quota pools", "", ""},
		{"Settings Page", "Fleet Info section shows counts", "", ""},

		// Detail Panel
		{"Detail Panel", "Clicking task opens right-side detail panel", "", ""},
		{"Detail Panel", "Panel shows title / status / identifier / priority / org", "", ""},
		{"Detail Panel", "Panel renders description as markdown", "", ""},
		{"Detail Panel", "Panel shows Stage / Assignee / Project / Goal / Repo / Labels", "", ""},
		{"Detail Panel", "Panel shows Spend and Budget if present", "", ""},
		{"Detail Panel", "Panel shows normalized blocker tag for blocked tasks", "", ""},
		{"Detail Panel", "Panel shows timestamps", "", ""},
		{"Detail Panel", "Chat section shows comments thread", "", ""},
		{"Detail Panel", "Chat compose box sends message (⌘↵)", "", ""},
		{"Detail Panel", "Close button (×) works", "", ""},

		// Stubs
		{"Phase 2 Stubs", "Sidebar → Routines shows stub page with 'Coming in Phase 2'", "", ""},
		{"Phase 2 Stubs", "Sidebar → Artifacts shows stub page", "", ""},
		{"Phase 2 Stubs", "Sidebar → Skills shows stub page", "", ""},
		{"Phase 2 Stubs", "Sidebar → Connectors shows stub page", "", ""},
		{"Phase 2 Stubs", "Sidebar → Audit shows stub page", "", ""},

		// Architecture
		{"Architecture (NOT DONE — STA-167)", "Web UI reads tasks from native StayPoint SQLite",
			"Currently Paperclip-proxied. handlers_telemetry.go GetFleetTask/GetFleetTaskComments forward to 127.0.0.1:3100. This item should FAIL until STA-167 is done.",
			"Check handlers_telemetry.go — proxyPaperclip() must be replaced"},

		// Regression
		{"Regression — Existing Views", "All Organizations overview loads with KPI cards", "", ""},
		{"Regression — Existing Views", "5-Hour Quota section renders gauge cards", "", ""},
		{"Regression — Existing Views", "Organization cards grid renders", "", ""},
		{"Regression — Existing Views", "Clicking org card opens org detail", "", ""},
		{"Regression — Existing Views", "Org detail shows tasks / agents / spend", "", ""},
		{"Regression — Existing Views", "Kanban board renders 4 columns", "", ""},
		{"Regression — Existing Views", "Boss Card renders stat grid + download button", "", ""},
		{"Regression — Existing Views", "SSE live badge shows 'live'", "", ""},
		{"Regression — Existing Views", "Quick filter buttons work in overview", "", ""},
		{"Regression — Existing Views", "Sidebar org tree populates", "", ""},
		{"Regression — Existing Views", "Clicking org in sidebar tree opens org detail", "", ""},
	}

	out := make([]ChecklistItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, ChecklistItem{
			ID:          uuid.NewString(),
			Sprint:      sprint,
			Section:     r.section,
			Title:       r.title,
			Description: r.desc,
			HowToTest:   r.howTo,
		})
	}
	return out
}
