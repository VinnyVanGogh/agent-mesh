package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
	"github.com/VinnyVanGogh/staypoint/internal/paperclip"
)

// ChecklistHandler serves the /api/checklist endpoints.
type ChecklistHandler struct {
	db  *sql.DB
	hub *EventHub
}

func NewChecklistHandler(db *sql.DB, hub *EventHub) *ChecklistHandler {
	_ = ensureChecklistTables(db)
	return &ChecklistHandler{db: db, hub: hub}
}

func ensureChecklistTables(db *sql.DB) error {
	if db == nil {
		return nil
	}
	schema := `
	CREATE TABLE IF NOT EXISTS checklist_items (
		id          TEXT PRIMARY KEY,
		sprint      TEXT NOT NULL DEFAULT 'STA-168',
		section     TEXT NOT NULL,
		title       TEXT NOT NULL,
		description TEXT,
		how_to_test TEXT,
		contract    TEXT,
		status      TEXT NOT NULL DEFAULT 'pending'
		            CHECK (status IN ('pending','pass','partial','fail','skip','not_done')),
		notes       TEXT,
		version     INTEGER NOT NULL DEFAULT 1,
		created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
		updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	CREATE INDEX IF NOT EXISTS idx_checklist_sprint_section ON checklist_items (sprint, section);
	CREATE TABLE IF NOT EXISTS checklist_history (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		item_id     TEXT NOT NULL REFERENCES checklist_items(id) ON DELETE CASCADE,
		status      TEXT NOT NULL,
		notes       TEXT,
		changed_by  TEXT NOT NULL DEFAULT 'user',
		changed_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	);
	CREATE INDEX IF NOT EXISTS idx_checklist_history_item ON checklist_history (item_id, id DESC);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Idempotently add contract column if missing
	var contractColCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('checklist_items') WHERE name='contract'").Scan(&contractColCount)
	if contractColCount == 0 {
		_, _ = db.Exec("ALTER TABLE checklist_items ADD COLUMN contract TEXT;")
	}

	// Idempotently check and migrate CHECK constraint if partial is missing
	var sqlDef string
	_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='checklist_items'`).Scan(&sqlDef)
	if sqlDef != "" && !strings.Contains(sqlDef, "'partial'") {
		_, _ = db.Exec(`
			PRAGMA foreign_keys = OFF;
			CREATE TABLE IF NOT EXISTS checklist_items_new (
				id          TEXT PRIMARY KEY,
				sprint      TEXT NOT NULL DEFAULT 'STA-168',
				section     TEXT NOT NULL,
				title       TEXT NOT NULL,
				description TEXT,
				how_to_test TEXT,
				contract    TEXT,
				status      TEXT NOT NULL DEFAULT 'pending'
				            CHECK (status IN ('pending','pass','partial','fail','skip','not_done')),
				notes       TEXT,
				version     INTEGER NOT NULL DEFAULT 1,
				created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
				updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			);
			INSERT INTO checklist_items_new (id, sprint, section, title, description, how_to_test, contract, status, notes, version, created_at, updated_at)
			SELECT id, sprint, section, title, description, how_to_test, contract, status, notes, version, created_at, updated_at FROM checklist_items;
			DROP TABLE checklist_items;
			ALTER TABLE checklist_items_new RENAME TO checklist_items;
			CREATE INDEX IF NOT EXISTS idx_checklist_sprint_section ON checklist_items (sprint, section);
			PRAGMA foreign_keys = ON;
		`)
	}
	return nil
}

// ChecklistItem represents one verifiable claim.
type ChecklistItem struct {
	ID          string `json:"id"`
	Sprint      string `json:"sprint"`
	Section     string `json:"section"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	HowToTest   string `json:"how_to_test,omitempty"`
	Contract    string `json:"contract,omitempty"`
	Status      string `json:"status"` // pending | pass | fail | skip | not_done
	Notes       string `json:"notes"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
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
	_ = ensureChecklistTables(h.db)
	sprint := r.URL.Query().Get("sprint")
	query := `SELECT id, sprint, section, title, description, how_to_test, contract, status, notes, version, created_at, updated_at
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
		var desc, howTo, contract, notes sql.NullString
		if err := rows.Scan(&it.ID, &it.Sprint, &it.Section, &it.Title,
			&desc, &howTo, &contract, &it.Status, &notes, &it.Version,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		it.Description = desc.String
		it.HowToTest = howTo.String
		it.Contract = contract.String
		it.Notes = notes.String
		items = append(items, it)
	}
	writeJSON(w, map[string]any{"items": items})
}

// GET /api/checklist/sprints — list distinct sprint identifiers
func (h *ChecklistHandler) ListSprints(w http.ResponseWriter, r *http.Request) {
	_ = ensureChecklistTables(h.db)
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
		Status   string `json:"status"`
		Notes    string `json:"notes"`
		Contract string `json:"contract"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	validStatuses := map[string]bool{
		"pending": true, "pass": true, "partial": true, "fail": true, "skip": true, "not_done": true,
	}
	if body.Status == "in_between" || body.Status == "needs_work" {
		body.Status = "partial"
	}
	if body.Status != "" && !validStatuses[body.Status] {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Fetch current item to bump version
	var current ChecklistItem
	var desc, howTo, contract, notes sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, sprint, section, title, description, how_to_test, contract, status, notes, version, created_at, updated_at FROM checklist_items WHERE id = ?`, id).
		Scan(&current.ID, &current.Sprint, &current.Section, &current.Title,
			&desc, &howTo, &contract, &current.Status, &notes, &current.Version,
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
	current.Contract = contract.String
	current.Notes = notes.String

	newStatus := current.Status
	if body.Status != "" {
		newStatus = body.Status
	}
	newNotes := current.Notes
	if body.Notes != "" {
		newNotes = body.Notes
	}
	newContract := current.Contract
	if body.Contract != "" {
		newContract = body.Contract
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	newVersion := current.Version + 1
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE checklist_items SET status=?, notes=?, contract=?, version=?, updated_at=? WHERE id=?`,
		newStatus, newNotes, newContract, newVersion, now, id); err != nil {
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
	current.Contract = newContract
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
	_ = ensureChecklistTables(h.db)
	var body struct {
		Sprint string `json:"sprint"`
		Force  bool   `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sprint := body.Sprint
	if sprint == "" {
		sprint = "STA-168"
	}

	seeded, skipped, err := checklist.Seed(r.Context(), h.db, sprint, body.Force)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "seed failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"seeded": seeded, "skipped": skipped})
}

// POST /api/checklist/evaluate — evaluates machine-verifiable contracts and auto-downgrades regressed items
func (h *ChecklistHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	_ = ensureChecklistTables(h.db)
	sprint := r.URL.Query().Get("sprint")
	if sprint == "" {
		sprint = "STA-168"
	}

	downgrade := true
	if d := r.URL.Query().Get("downgrade"); d == "false" || d == "0" {
		downgrade = false
	}
	notify := false
	if n := r.URL.Query().Get("notify_paperclip"); n == "true" || n == "1" {
		notify = true
	}

	repoRoot := r.URL.Query().Get("repo_root")
	if repoRoot == "" {
		repoRoot = os.Getenv("STAYPOINT_REPO_ROOT")
	}
	if repoRoot == "" {
		if _, err := os.Stat("go.mod"); err == nil {
			repoRoot = "."
		} else if _, err := os.Stat("/Users/vincevasile/Documents/dev/agent-mesh/go.mod"); err == nil {
			repoRoot = "/Users/vincevasile/Documents/dev/agent-mesh"
		} else {
			repoRoot = "."
		}
	}

	opts := checklist.EvaluateOptions{
		RepoRoot:        repoRoot,
		BaseURL:         "http://" + r.Host,
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
		Downgrade:       downgrade,
		NotifyPaperclip: notify,
		BroadcastFn: func(event string, data any) {
			if h.hub != nil {
				h.hub.Publish(event, data)
			}
		},
	}
	if notify {
		opts.PaperclipClient = paperclip.NewClient("", "")
	}

	summary, err := checklist.EvaluateSprint(r.Context(), h.db, sprint, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, summary)
}

// defaultChecklist returns the STA-168 verification items.
func defaultChecklist(sprint string) []ChecklistItem {
	items := checklist.DefaultChecklist(sprint)
	out := make([]ChecklistItem, len(items))
	for i, it := range items {
		out[i] = ChecklistItem{
			ID:          it.ID,
			Sprint:      it.Sprint,
			Section:     it.Section,
			Title:       it.Title,
			Description: it.Description,
			HowToTest:   it.HowToTest,
			Contract:    it.Contract,
			Status:      it.Status,
		}
	}
	return out
}
