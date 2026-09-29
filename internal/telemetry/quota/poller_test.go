package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/db"
)

type stubFetcher struct {
	name  string
	snap  *Snapshot
	err   error
	calls int
}

func (s *stubFetcher) Provider() string { return s.name }
func (s *stubFetcher) Fetch(context.Context) (*Snapshot, error) {
	s.calls++
	return s.snap, s.err
}

func newTestStore(t *testing.T) Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return Store{DB: d.DB()}
}

func TestPoller_ThrottlesToMinInterval(t *testing.T) {
	st := newTestStore(t)
	clock := fixedNow
	f := &stubFetcher{name: "claude", snap: &Snapshot{Provider: "claude", FiveHour: &Window{Utilization: 10}}}
	p := &Poller{Store: st, Fetchers: []Fetcher{f}, Now: func() time.Time { return clock }}

	p.PollOnce(context.Background())
	clock = clock.Add(MinInterval - time.Second)
	p.PollOnce(context.Background())
	if f.calls != 1 {
		t.Fatalf("fetched %d times inside the 15m floor, want 1", f.calls)
	}
	clock = clock.Add(2 * time.Second)
	p.PollOnce(context.Background())
	if f.calls != 2 {
		t.Fatalf("fetched %d times after the floor, want 2", f.calls)
	}

	// A second Poller (e.g. the CLI) shares the persisted throttle.
	p2 := &Poller{Store: st, Fetchers: []Fetcher{f}, Now: func() time.Time { return clock }}
	p2.PollOnce(context.Background())
	if f.calls != 2 {
		t.Fatalf("throttle not shared through SQLite: %d calls", f.calls)
	}
}

func TestPoller_FailsOpenAndBacksOff(t *testing.T) {
	for name, ferr := range map[string]error{
		"unauthorized": ErrUnauthorized,
		"nocreds":      ErrNoCredentials,
		"other":        errors.New("boom"),
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			// Previous good reading must survive a failed refresh.
			if err := st.Save(&Snapshot{Provider: "codex", FetchedAt: fixedNow, Weekly: &Window{Utilization: 42}}); err != nil {
				t.Fatal(err)
			}
			f := &stubFetcher{name: "codex", err: ferr}
			p := &Poller{Store: st, Fetchers: []Fetcher{f}, Now: now}
			if got := p.PollOnce(context.Background()); len(got) != 1 {
				t.Fatalf("fetched=%v", got)
			}
			rows, _ := st.Load("codex")
			if len(rows) != 1 || rows[0].UsedPct != 42 {
				t.Errorf("cache clobbered by failure: %+v", rows)
			}
			s, ok, _ := st.State("codex")
			if !ok || s.Status == "ok" || !s.NextAttempt.After(fixedNow) {
				t.Errorf("bad state after failure: %+v", s)
			}
			if name != "other" && s.NextAttempt.Sub(fixedNow) != RejectedBackoff {
				t.Errorf("rejected creds should back off %v, got %v", RejectedBackoff, s.NextAttempt.Sub(fixedNow))
			}
		})
	}
}

func TestPoller_OneFailureDoesNotStopOthers(t *testing.T) {
	st := newTestStore(t)
	bad := &stubFetcher{name: "claude", err: ErrUnauthorized}
	good := &stubFetcher{name: "gemini", snap: &Snapshot{Provider: "gemini", FiveHour: &Window{Utilization: 5}}}
	(&Poller{Store: st, Fetchers: []Fetcher{bad, good}, Now: now}).PollOnce(context.Background())
	if rows, _ := st.Load("gemini"); len(rows) != 1 {
		t.Fatalf("gemini not stored after claude 401: %+v", rows)
	}
}

func TestStore_SaveReplacesWindows(t *testing.T) {
	st := newTestStore(t)
	_ = st.Save(&Snapshot{Provider: "cursor", FetchedAt: fixedNow, Monthly: &Window{Utilization: 30}, FiveHour: &Window{Utilization: 1}})
	_ = st.Save(&Snapshot{Provider: "cursor", FetchedAt: fixedNow, Monthly: &Window{Utilization: 31}})
	rows, err := st.Load("cursor")
	if err != nil || len(rows) != 1 || rows[0].WindowType != WindowMonthly || rows[0].UsedPct != 31 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func geminiServer(t *testing.T, quotaBody string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			_, _ = w.Write([]byte(`{"cloudaicompanionProject":"proj-1"}`))
		case strings.HasSuffix(r.URL.Path, ":retrieveUserQuota"):
			w.WriteHeader(status)
			_, _ = w.Write([]byte(quotaBody))
		}
	}))
}

func geminiCreds(t *testing.T, expiry time.Time) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "oauth_creds.json")
	body := `{"access_token":"` + fakeToken + `","expiry_date":` + strconv.FormatInt(expiry.UnixMilli(), 10) + `}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGeminiFetch_Buckets(t *testing.T) {
	short := fixedNow.Add(3 * time.Hour).Format(time.RFC3339)
	long := fixedNow.Add(30 * time.Hour).Format(time.RFC3339)
	srv := geminiServer(t, `{"buckets":[
		{"modelId":"a","remainingFraction":0.9,"resetTime":"`+short+`"},
		{"modelId":"b","remainingFraction":0.25,"resetTime":"`+short+`"},
		{"modelId":"c","remainingFraction":0.6,"resetTime":"`+long+`"},
		{"modelId":"d"}]}`, 200)
	defer srv.Close()
	f := &GeminiFetcher{Client: srv.Client(), BaseURL: srv.URL + "/v1internal", CredsFile: geminiCreds(t, fixedNow.Add(time.Hour)), Now: now}
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.FiveHour == nil || snap.FiveHour.Utilization != 75 {
		t.Errorf("short window should be the most-used bucket (75), got %+v", snap.FiveHour)
	}
	if snap.Weekly == nil || snap.Weekly.Utilization != 40 {
		t.Errorf("long window: %+v", snap.Weekly)
	}
}

func TestGeminiFetch_Failures(t *testing.T) {
	srv := geminiServer(t, `{}`, http.StatusForbidden)
	defer srv.Close()
	f := &GeminiFetcher{Client: srv.Client(), BaseURL: srv.URL + "/v1internal", CredsFile: geminiCreds(t, fixedNow.Add(time.Hour)), Now: now}
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("403 should be ErrUnauthorized, got %v", err)
	}
	f.CredsFile = geminiCreds(t, fixedNow.Add(-time.Hour))
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("expired token should be ErrNoCredentials, got %v", err)
	}
	f.CredsFile = filepath.Join(t.TempDir(), "missing.json")
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("missing file should be ErrNoCredentials, got %v", err)
	}
	// Schema change: garbage body is an error, not a panic or fake reading.
	bad := geminiServer(t, `[1,2,3]`, 200)
	defer bad.Close()
	f = &GeminiFetcher{Client: bad.Client(), BaseURL: bad.URL + "/v1internal", CredsFile: geminiCreds(t, fixedNow.Add(time.Hour)), Now: now}
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Error("schema change must error")
	}
}
