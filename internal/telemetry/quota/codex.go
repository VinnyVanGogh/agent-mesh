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

const codexWhamURL = "https://chatgpt.com/backend-api/wham/usage"

type whamWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAfterSeconds  int64    `json:"reset_after_seconds"`
	ResetAt            int64    `json:"reset_at"` // unix seconds
}

type whamUsage struct {
	RateLimit *struct {
		PrimaryWindow   *whamWindow `json:"primary_window"`
		SecondaryWindow *whamWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

// CodexFetcher probes OpenAI's Wham usage endpoint with the token from
// ~/.codex/auth.json.
type CodexFetcher struct {
	Client   HTTPDoer
	URL      string
	AuthFile string
	Now      func() time.Time
}

func NewCodexFetcher() *CodexFetcher {
	f := &CodexFetcher{Client: newHTTPClient(), URL: codexWhamURL, Now: time.Now}
	if home, err := os.UserHomeDir(); err == nil {
		f.AuthFile = filepath.Join(home, ".codex", "auth.json")
	}
	return f
}

func (f *CodexFetcher) Provider() string { return "codex" }

func (f *CodexFetcher) credentials() (token, accountID string, err error) {
	b, rerr := os.ReadFile(f.AuthFile)
	if rerr != nil {
		return "", "", fmt.Errorf("%w: codex auth file unreadable", ErrNoCredentials)
	}
	var a struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(b, &a) != nil || a.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("%w: codex", ErrNoCredentials)
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}

func (f *CodexFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	tok, acct, err := f.credentials()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("quota: codex build request: %w", sanitizeErr(err))
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if acct != "" {
		req.Header.Set("ChatGPT-Account-Id", acct)
	}
	req.Header.Set("User-Agent", userAgent)
	body, err := doJSON(f.Client, "codex", req)
	if err != nil {
		return nil, err
	}
	var u whamUsage
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("quota: codex decode usage: %w", err)
	}
	now := f.Now()
	snap := &Snapshot{Provider: "codex", FetchedAt: now}
	if u.RateLimit == nil {
		return snap, nil
	}
	// Wham labels windows primary/secondary; classify by length so a
	// reshuffled plan still lands in the right slot (<=6h is the 5h window).
	for _, w := range []*whamWindow{u.RateLimit.PrimaryWindow, u.RateLimit.SecondaryWindow} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		cw := &Window{Utilization: *w.UsedPercent}
		switch {
		case w.ResetAt > 0:
			cw.ResetsAt = time.Unix(w.ResetAt, 0).UTC()
		case w.ResetAfterSeconds > 0:
			cw.ResetsAt = now.Add(time.Duration(w.ResetAfterSeconds) * time.Second).UTC()
		}
		if w.LimitWindowSeconds > 0 && w.LimitWindowSeconds <= 6*3600 {
			snap.FiveHour = cw
		} else {
			snap.Weekly = cw
		}
	}
	return snap, nil
}
