package quota

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

const (
	cursorUsageURL = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage"
	cursorTokenKey = "cursorAuth/accessToken"
)

// CursorTokenSource yields Cursor's access token.
type CursorTokenSource interface {
	Token(ctx context.Context) (string, error)
}

// VSCDBTokenSource reads the token from Cursor's state.vscdb, opened read-only.
type VSCDBTokenSource struct{ Path string }

// DefaultCursorDBPath returns Cursor's state.vscdb location for this OS.
func DefaultCursorDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Cursor", "User", "globalStorage", "state.vscdb")
	default:
		return filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	}
}

func (s VSCDBTokenSource) Token(ctx context.Context) (string, error) {
	if _, err := os.Stat(s.Path); err != nil {
		return "", fmt.Errorf("%w: cursor state db not found", ErrNoCredentials)
	}
	u := url.URL{Scheme: "file", Path: s.Path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", fmt.Errorf("%w: cursor state db open failed", ErrNoCredentials)
	}
	defer db.Close()
	var tok string
	if err := db.QueryRowContext(ctx, `SELECT value FROM ItemTable WHERE key = ?`, cursorTokenKey).Scan(&tok); err != nil || tok == "" {
		return "", fmt.Errorf("%w: cursor token not present", ErrNoCredentials)
	}
	return tok, nil
}

// CursorFetcher probes Cursor's dashboard usage RPC (Connect protocol, JSON).
type CursorFetcher struct {
	Tokens CursorTokenSource
	Client HTTPDoer
	URL    string
	Now    func() time.Time
}

func NewCursorFetcher() *CursorFetcher {
	return &CursorFetcher{Tokens: VSCDBTokenSource{Path: DefaultCursorDBPath()}, Client: newHTTPClient(), URL: cursorUsageURL, Now: time.Now}
}

func (f *CursorFetcher) Provider() string { return "cursor" }

// cursorUsage is decoded leniently: Cursor's dashboard RPC is undocumented, so
// only fields we act on are declared and everything else is ignored.
type cursorUsage struct {
	BillingCycleEnd string `json:"billingCycleEnd"`
	PlanUsage       *struct {
		TotalPercentUsed *float64 `json:"totalPercentUsed"`
	} `json:"planUsage"`
}

func (f *CursorFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	tok, err := f.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.URL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("quota: cursor build request: %w", sanitizeErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", userAgent)
	body, err := doJSON(f.Client, "cursor", req)
	if err != nil {
		return nil, err
	}
	var u cursorUsage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("quota: cursor decode usage: %w", err)
	}
	snap := &Snapshot{Provider: "cursor", FetchedAt: f.Now()}
	if u.PlanUsage != nil && u.PlanUsage.TotalPercentUsed != nil {
		snap.Monthly = &Window{Utilization: *u.PlanUsage.TotalPercentUsed, ResetsAt: parseCursorTime(u.BillingCycleEnd)}
	}
	return snap, nil
}

// parseCursorTime accepts RFC3339 or unix milliseconds as a string.
func parseCursorTime(s string) time.Time {
	if t := parseTime(s); !t.IsZero() {
		return t
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
}
