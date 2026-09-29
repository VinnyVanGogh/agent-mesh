package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// KeychainServiceClaude is the Keychain item Claude Code stores OAuth creds in.
	KeychainServiceClaude = "Claude Code-credentials"
	claudeUsageURL        = "https://api.anthropic.com/api/oauth/usage"
	claudeBetaHeader      = "oauth-2025-04-20"
)

type anthropicWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type anthropicUsage struct {
	FiveHour       *anthropicWindow `json:"five_hour"`
	SevenDay       *anthropicWindow `json:"seven_day"`
	SevenDaySonnet *anthropicWindow `json:"seven_day_sonnet"`
	ExtraUsage     *struct {
		IsEnabled    bool    `json:"is_enabled"`
		UsedCredits  float64 `json:"used_credits"`
		MonthlyLimit float64 `json:"monthly_limit"`
	} `json:"extra_usage"`
}

// ClaudeFetcher probes Anthropic's OAuth usage endpoint using the token Claude
// Code keeps in the macOS Keychain, falling back to ~/.claude/.credentials.json.
type ClaudeFetcher struct {
	Keychain  KeychainReader
	Client    HTTPDoer
	URL       string
	CredsFile string // fallback path; empty disables the file fallback
	Now       func() time.Time
}

// NewClaudeFetcher returns a fetcher wired to the real Keychain and endpoint.
func NewClaudeFetcher() *ClaudeFetcher {
	f := &ClaudeFetcher{Keychain: SecurityKeychain{}, Client: newHTTPClient(), URL: claudeUsageURL, Now: time.Now}
	if home, err := os.UserHomeDir(); err == nil {
		f.CredsFile = filepath.Join(home, ".claude", ".credentials.json")
	}
	return f
}

func (f *ClaudeFetcher) Provider() string { return "claude" }

// parseClaudeCreds extracts the access token from Claude Code's credential JSON.
func parseClaudeCreds(raw []byte) string {
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return c.ClaudeAiOauth.AccessToken
}

func (f *ClaudeFetcher) accessToken(ctx context.Context) (string, error) {
	if f.Keychain != nil {
		if s, err := f.Keychain.Read(ctx, KeychainServiceClaude); err == nil {
			if tok := parseClaudeCreds([]byte(s)); tok != "" {
				return tok, nil
			}
		}
	}
	if f.CredsFile != "" {
		if b, err := os.ReadFile(f.CredsFile); err == nil {
			if tok := parseClaudeCreds(b); tok != "" {
				return tok, nil
			}
		}
	}
	return "", fmt.Errorf("%w: claude", ErrNoCredentials)
}

func (f *ClaudeFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	tok, err := f.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("quota: claude build request: %w", sanitizeErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", claudeBetaHeader)
	req.Header.Set("User-Agent", userAgent)
	body, err := doJSON(f.Client, "claude", req)
	if err != nil {
		return nil, err
	}
	var u anthropicUsage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("quota: claude decode usage: %w", err)
	}
	conv := func(w *anthropicWindow) *Window {
		if w == nil || w.Utilization == nil {
			return nil
		}
		return &Window{Utilization: *w.Utilization, ResetsAt: parseTime(w.ResetsAt)}
	}
	snap := &Snapshot{
		Provider:     "claude",
		FiveHour:     conv(u.FiveHour),
		Weekly:       conv(u.SevenDay),
		WeeklySonnet: conv(u.SevenDaySonnet),
		FetchedAt:    f.Now(),
	}
	if u.ExtraUsage != nil {
		snap.Extra = &Extra{Enabled: u.ExtraUsage.IsEnabled, UsedCredits: u.ExtraUsage.UsedCredits, MonthlyLimit: u.ExtraUsage.MonthlyLimit}
	}
	return snap, nil
}
