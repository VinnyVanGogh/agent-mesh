package security

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// GateRequestStatus is the lifecycle state of a pending command-gate approval.
type GateRequestStatus string

const (
	GateRequestPending  GateRequestStatus = "pending"
	GateRequestApproved GateRequestStatus = "approved"
	GateRequestDenied   GateRequestStatus = "denied"
)

// GateRequest is a Board-approval record for a Red-tier command intercepted by
// the pre-tool hook. It persists across daemon restarts so the hook can always
// poll for a decision rather than auto-denying on timeout.
type GateRequest struct {
	ID        string            `json:"id"`
	Cmdline   string            `json:"cmdline"`
	Reasons   []string          `json:"reasons"`
	Status    GateRequestStatus `json:"status"`
	RunID     string            `json:"run_id,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	DecidedAt *time.Time        `json:"decided_at,omitempty"`
}

// CreateGateRequest inserts a new pending gate request and returns its id.
func CreateGateRequest(db *sql.DB, cmdline string, reasons []string, runID string) (*GateRequest, error) {
	id, err := genID()
	if err != nil {
		return nil, err
	}
	rj, _ := json.Marshal(reasons)
	now := time.Now().UTC()
	_, err = db.Exec(
		`INSERT INTO security_gate_requests (id, cmdline, reasons_json, run_id, status, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?)`,
		id, cmdline, string(rj), runID, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("create gate request: %w", err)
	}
	return &GateRequest{
		ID:        id,
		Cmdline:   cmdline,
		Reasons:   reasons,
		Status:    GateRequestPending,
		RunID:     runID,
		CreatedAt: now,
	}, nil
}

// GetGateRequest fetches a single gate request by id.
func GetGateRequest(db *sql.DB, id string) (*GateRequest, error) {
	row := db.QueryRow(
		`SELECT id, cmdline, reasons_json, run_id, status, created_at, decided_at
		 FROM security_gate_requests WHERE id = ?`, id)
	return scanGateRequest(row)
}

// ListPendingGateRequests returns all requests still awaiting a decision.
func ListPendingGateRequests(db *sql.DB) ([]*GateRequest, error) {
	rows, err := db.Query(
		`SELECT id, cmdline, reasons_json, run_id, status, created_at, decided_at
		 FROM security_gate_requests WHERE status = 'pending' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GateRequest
	for rows.Next() {
		r, err := scanGateRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DecideGateRequest sets the status to approved or denied and records the timestamp.
func DecideGateRequest(db *sql.DB, id string, approved bool) (*GateRequest, error) {
	status := GateRequestDenied
	if approved {
		status = GateRequestApproved
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.Exec(
		`UPDATE security_gate_requests SET status = ?, decided_at = ?
		 WHERE id = ? AND status = 'pending'`,
		string(status), now, id)
	if err != nil {
		return nil, fmt.Errorf("decide gate request: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("gate request %s not found or already decided", id)
	}
	return GetGateRequest(db, id)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanGateRequest(row scanner) (*GateRequest, error) {
	var (
		r         GateRequest
		rj        string
		runID     sql.NullString
		createdAt string
		decidedAt sql.NullString
	)
	if err := row.Scan(&r.ID, &r.Cmdline, &rj, &runID, &r.Status, &createdAt, &decidedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(rj), &r.Reasons)
	r.RunID = runID.String
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		r.CreatedAt = t
	}
	if decidedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, decidedAt.String); err == nil {
			r.DecidedAt = &t
		}
	}
	return &r, nil
}

func genID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
