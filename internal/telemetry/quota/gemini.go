package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GeminiFetcher refreshes the Google ADC token and probes the Gemini models API.
type GeminiFetcher struct {
	Client    HTTPDoer
	URL       string
	TokenURL  string
	CredsFile string
	Now       func() time.Time
}

func NewGeminiFetcher() *GeminiFetcher {
	home, _ := os.UserHomeDir()
	return &GeminiFetcher{
		Client:    newHTTPClient(),
		URL:       "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1",
		TokenURL:  "https://oauth2.googleapis.com/token",
		CredsFile: filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"),
		Now:       time.Now,
	}
}

func (f *GeminiFetcher) Provider() string { return "gemini" }

func (f *GeminiFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	// Read gcloud credentials
	b, err := os.ReadFile(f.CredsFile)
	if err != nil {
		return nil, fmt.Errorf("%w: gemini ADC missing", ErrNoCredentials)
	}

	var creds struct {
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(b, &creds); err != nil {
		return nil, fmt.Errorf("%w: gemini ADC invalid JSON", ErrNoCredentials)
	}

	if creds.RefreshToken == "" || creds.ClientID == "" || creds.ClientSecret == "" {
		return nil, fmt.Errorf("%w: gemini ADC missing required fields", ErrNoCredentials)
	}

	// Refresh token
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", creds.ClientID)
	form.Set("client_secret", creds.ClientSecret)
	form.Set("refresh_token", creds.RefreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := doJSON(f.Client, "gemini_oauth", req)
	if err != nil {
		return nil, err
	}

	var tokResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tokResp); err != nil {
		return nil, fmt.Errorf("quota: gemini oauth decode: %w", err)
	}

	if tokResp.AccessToken == "" {
		return nil, fmt.Errorf("quota: gemini oauth returned empty token")
	}

	// Update agy oauth_creds.json
	home, _ := os.UserHomeDir()
	agyCreds := filepath.Join(home, ".gemini", "oauth_creds.json")
	if _, err := os.Stat(agyCreds); err == nil {
		if b, err := os.ReadFile(agyCreds); err == nil {
			var agy map[string]interface{}
			if err := json.Unmarshal(b, &agy); err == nil {
				agy["access_token"] = tokResp.AccessToken
				agy["expiry_date"] = f.Now().UnixMilli() + 3600000
				if out, err := json.MarshalIndent(agy, "", "  "); err == nil {
					_ = os.WriteFile(agyCreds, out, 0600)
				}
			}
		}
	}

	// Probe API
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, err
	}
	req2.Header.Set("Authorization", "Bearer "+tokResp.AccessToken)
	req2.Header.Set("User-Agent", userAgent)

	_, err = doJSON(f.Client, "gemini", req2)
	if err != nil {
		return nil, err
	}

	// Gemini API doesn't report quota.
	// Returning a placeholder so we know it succeeded.
	return &Snapshot{
		Provider:  "gemini",
		FetchedAt: f.Now(),
	}, nil
}
