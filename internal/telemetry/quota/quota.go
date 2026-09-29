// Package quota probes provider usage endpoints directly, in-process, so
// StayPoint no longer depends on external Python pollers for live quota data.
//
// Every fetcher takes its credential source and HTTP endpoint through
// injectable seams so tests never touch a real Keychain, file, or network.
// Credentials are never included in returned errors or log output.
package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Window is one rolling usage window. Utilization is a percentage in [0,100].
type Window struct {
	Utilization float64
	ResetsAt    time.Time // zero when the provider did not report a reset
}

// Extra describes pay-as-you-go overage credits, when the provider reports them.
type Extra struct {
	Enabled      bool
	UsedCredits  float64
	MonthlyLimit float64
}

// Snapshot is a provider-neutral point-in-time quota reading.
// Windows a provider does not report are nil.
type Snapshot struct {
	Provider     string
	FiveHour     *Window
	Weekly       *Window
	WeeklySonnet *Window
	Monthly      *Window // billing-cycle usage (Cursor)
	Extra        *Extra
	FetchedAt    time.Time
}

// Fetcher reads live quota for one provider.
type Fetcher interface {
	Provider() string
	Fetch(ctx context.Context) (*Snapshot, error)
}

// HTTPDoer is the subset of *http.Client the fetchers use.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var (
	// ErrNoCredentials means no usable credential source was found.
	ErrNoCredentials = errors.New("quota: no credentials found")
	// ErrUnauthorized means the provider rejected the credential (401/403).
	ErrUnauthorized = errors.New("quota: credentials rejected by provider")
)

const (
	defaultTimeout = 8 * time.Second
	maxBodyBytes   = 1 << 20
	userAgent      = "StayPoint/2.0"
)

func newHTTPClient() *http.Client { return &http.Client{Timeout: defaultTimeout} }

// doJSON executes req and returns the (size-capped) body of a 200 response.
// Error text carries only provider name and status, never headers or body,
// so bearer tokens cannot leak through wrapped errors.
func doJSON(c HTTPDoer, provider string, req *http.Request) ([]byte, error) {
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("quota: %s request failed: %w", provider, sanitizeErr(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s HTTP %d", ErrUnauthorized, provider, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("quota: %s returned HTTP %d", provider, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("quota: %s read body: %w", provider, sanitizeErr(err))
	}
	return body, nil
}

// sanitizeErr collapses transport errors to a fixed vocabulary so nothing
// request-derived (URL, headers) rides along in logs.
func sanitizeErr(err error) error {
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timeout")
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return errors.New("transport error")
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
