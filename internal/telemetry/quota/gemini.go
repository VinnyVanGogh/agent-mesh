package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	geminiCodeAssistBase = "https://cloudcode-pa.googleapis.com/v1internal"
	// Buckets resetting within this horizon are treated as the short window.
	geminiShortWindow = 6 * time.Hour
)

// GeminiFetcher reads per-model quota buckets from the Code Assist backend the
// installed Gemini CLI itself uses (`loadCodeAssist` for the project, then
// `retrieveUserQuota`). The endpoint is undocumented, so every failure is an
// ordinary error the caller fails open on.
//
// It is read-only: it uses the access token the Gemini CLI already cached in
// ~/.gemini/oauth_creds.json and never refreshes or rewrites it, so an expired
// token yields ErrNoCredentials until the user's own CLI refreshes it.
type GeminiFetcher struct {
	Client    HTTPDoer
	BaseURL   string
	CredsFile string
	Now       func() time.Time
}

func NewGeminiFetcher() *GeminiFetcher {
	f := &GeminiFetcher{Client: newHTTPClient(), BaseURL: geminiCodeAssistBase, Now: time.Now}
	if home, err := os.UserHomeDir(); err == nil {
		f.CredsFile = filepath.Join(home, ".gemini", "oauth_creds.json")
	}
	return f
}

func (f *GeminiFetcher) Provider() string { return "gemini" }

func (f *GeminiFetcher) token() (string, error) {
	b, err := os.ReadFile(f.CredsFile)
	if err != nil {
		return "", fmt.Errorf("%w: gemini oauth file unreadable", ErrNoCredentials)
	}
	var c struct {
		AccessToken string `json:"access_token"`
		ExpiryDate  int64  `json:"expiry_date"` // unix millis
	}
	if json.Unmarshal(b, &c) != nil || c.AccessToken == "" {
		return "", fmt.Errorf("%w: gemini", ErrNoCredentials)
	}
	if c.ExpiryDate > 0 && c.ExpiryDate <= f.Now().UnixMilli() {
		return "", fmt.Errorf("%w: gemini access token expired", ErrNoCredentials)
	}
	return c.AccessToken, nil
}

func (f *GeminiFetcher) post(ctx context.Context, tok, method string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.BaseURL+":"+method, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("quota: gemini build request: %w", sanitizeErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	return doJSON(f.Client, "gemini", req)
}

type geminiBucket struct {
	ModelID           string   `json:"modelId"`
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
}

func (f *GeminiFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	tok, err := f.token()
	if err != nil {
		return nil, err
	}

	meta := map[string]any{"metadata": map[string]string{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}}
	body, err := f.post(ctx, tok, "loadCodeAssist", meta)
	if err != nil {
		return nil, err
	}
	var la struct {
		Project json.RawMessage `json:"cloudaicompanionProject"`
	}
	if err := json.Unmarshal(body, &la); err != nil {
		return nil, fmt.Errorf("quota: gemini decode loadCodeAssist: %w", err)
	}
	project := decodeGeminiProject(la.Project)
	if project == "" {
		return nil, fmt.Errorf("quota: gemini loadCodeAssist returned no project")
	}

	body, err = f.post(ctx, tok, "retrieveUserQuota", map[string]string{"project": project})
	if err != nil {
		return nil, err
	}
	var q struct {
		Buckets []geminiBucket `json:"buckets"`
	}
	if err := json.Unmarshal(body, &q); err != nil {
		return nil, fmt.Errorf("quota: gemini decode quota: %w", err)
	}

	now := f.Now()
	snap := &Snapshot{Provider: "gemini", FetchedAt: now}
	// Per-model buckets: keep the most constrained one per horizon. Buckets
	// resetting within 6h map to the short window, the rest to the long one.
	for _, b := range q.Buckets {
		if b.RemainingFraction == nil {
			continue
		}
		used := clampPct((1 - *b.RemainingFraction) * 100)
		reset := parseTime(b.ResetTime)
		w := &Window{Utilization: used, ResetsAt: reset}
		slot := &snap.Weekly
		if !reset.IsZero() && reset.Sub(now) <= geminiShortWindow {
			slot = &snap.FiveHour
		}
		if *slot == nil || used > (*slot).Utilization {
			*slot = w
		}
	}
	return snap, nil
}

// cloudaicompanionProject is a string in current responses and an object with
// an id in older ones; accept both.
func decodeGeminiProject(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.ID
	}
	return ""
}

func clampPct(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}
