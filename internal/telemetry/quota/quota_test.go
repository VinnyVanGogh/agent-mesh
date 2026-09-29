package quota

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fake credential; deliberately not token-shaped for any real provider.
const fakeToken = "FAKE-TEST-TOKEN-do-not-use"

var fixedNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func now() time.Time { return fixedNow }

type fakeKeychain struct {
	val    string
	err    error
	called string
}

func (k *fakeKeychain) Read(_ context.Context, service string) (string, error) {
	k.called = service
	return k.val, k.err
}

func claudeCreds(tok string) string {
	return `{"claudeAiOauth":{"accessToken":"` + tok + `","refreshToken":"FAKE-REFRESH"}}`
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("error leaks credential: %v", err)
	}
}

const claudeUsageFixture = `{
  "five_hour": {"utilization": 42.5, "resets_at": "2026-09-29T15:00:00Z"},
  "seven_day": {"utilization": 17, "resets_at": "2026-10-03T00:00:00.000000+00:00"},
  "seven_day_sonnet": {"utilization": 5, "resets_at": null},
  "extra_usage": {"is_enabled": true, "used_credits": 12.5, "monthly_limit": 100}
}`

func TestClaudeFetch_Success(t *testing.T) {
	var gotAuth, gotBeta, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta, gotPath = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"), r.URL.Path
		_, _ = w.Write([]byte(claudeUsageFixture))
	}))
	defer srv.Close()
	kc := &fakeKeychain{val: claudeCreds(fakeToken)}
	f := &ClaudeFetcher{Keychain: kc, Client: srv.Client(), URL: srv.URL + "/api/oauth/usage", Now: now}

	s, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if kc.called != "Claude Code-credentials" {
		t.Errorf("keychain service = %q", kc.called)
	}
	if gotAuth != "Bearer "+fakeToken || gotBeta != "oauth-2025-04-20" || gotPath != "/api/oauth/usage" {
		t.Errorf("request headers/path wrong: %q %q %q", gotAuth, gotBeta, gotPath)
	}
	if s.FiveHour == nil || s.FiveHour.Utilization != 42.5 || !s.FiveHour.ResetsAt.Equal(time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("five hour = %+v", s.FiveHour)
	}
	if s.Weekly == nil || s.Weekly.Utilization != 17 || s.Weekly.ResetsAt.IsZero() {
		t.Errorf("weekly = %+v", s.Weekly)
	}
	if s.WeeklySonnet == nil || s.WeeklySonnet.Utilization != 5 || !s.WeeklySonnet.ResetsAt.IsZero() {
		t.Errorf("sonnet = %+v", s.WeeklySonnet)
	}
	if s.Extra == nil || !s.Extra.Enabled || s.Extra.UsedCredits != 12.5 || s.Extra.MonthlyLimit != 100 {
		t.Errorf("extra = %+v", s.Extra)
	}
	if s.Provider != "claude" || !s.FetchedAt.Equal(fixedNow) {
		t.Errorf("meta = %+v", s)
	}
}

func TestClaudeFetch_FileFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":1}}`))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(path, []byte(claudeCreds(fakeToken)), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &ClaudeFetcher{Keychain: &fakeKeychain{err: ErrNoCredentials}, Client: srv.Client(), URL: srv.URL, CredsFile: path, Now: now}
	s, err := f.Fetch(context.Background())
	if err != nil || s.FiveHour == nil || s.Weekly != nil {
		t.Fatalf("snap=%+v err=%v", s, err)
	}
}

func TestClaudeFetch_NoCredentials(t *testing.T) {
	f := &ClaudeFetcher{Keychain: &fakeKeychain{val: "not json"}, Client: http.DefaultClient, URL: "http://unused.invalid", CredsFile: filepath.Join(t.TempDir(), "missing"), Now: now}
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeFetch_HTTPErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", 401, "token " + fakeToken + " expired", ErrUnauthorized},
		{"forbidden", 403, "", ErrUnauthorized},
		{"server error", 500, "boom", nil},
		{"bad json", 200, "{not json", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			f := &ClaudeFetcher{Keychain: &fakeKeychain{val: claudeCreds(fakeToken)}, Client: srv.Client(), URL: srv.URL, Now: now}
			_, err := f.Fetch(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			assertNoSecret(t, err)
		})
	}
}

func TestFetch_ContextCancelAndTransportErrorsDoNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f := &ClaudeFetcher{Keychain: &fakeKeychain{val: claudeCreds(fakeToken)}, Client: srv.Client(), URL: srv.URL, Now: now}
	_, err := f.Fetch(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	assertNoSecret(t, err)
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error leaks URL: %v", err)
	}
}

const whamFixture = `{
  "plan_type": "plus",
  "rate_limit": {
    "primary_window": {"used_percent": 30, "limit_window_seconds": 18000, "reset_after_seconds": 3600, "reset_at": 0},
    "secondary_window": {"used_percent": 61.5, "limit_window_seconds": 604800, "reset_after_seconds": 0, "reset_at": 1790000000}
  }
}`

func TestCodexFetch_Success(t *testing.T) {
	var gotAuth, gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAcct = r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-Id")
		_, _ = w.Write([]byte(whamFixture))
	}))
	defer srv.Close()
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"tokens":{"access_token":"`+fakeToken+`","account_id":"acct-123"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &CodexFetcher{Client: srv.Client(), URL: srv.URL, AuthFile: auth, Now: now}
	s, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+fakeToken || gotAcct != "acct-123" {
		t.Errorf("headers: %q %q", gotAuth, gotAcct)
	}
	if s.FiveHour == nil || s.FiveHour.Utilization != 30 || !s.FiveHour.ResetsAt.Equal(fixedNow.Add(time.Hour)) {
		t.Errorf("five hour = %+v", s.FiveHour)
	}
	if s.Weekly == nil || s.Weekly.Utilization != 61.5 || !s.Weekly.ResetsAt.Equal(time.Unix(1790000000, 0).UTC()) {
		t.Errorf("weekly = %+v", s.Weekly)
	}
}

func TestCodexFetch_MissingOrMalformedAuth(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"tokens":{}}`), 0o600)
	for _, p := range []string{filepath.Join(dir, "missing.json"), bad} {
		f := &CodexFetcher{Client: http.DefaultClient, URL: "http://unused.invalid", AuthFile: p, Now: now}
		if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrNoCredentials) {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}

func TestCodexFetch_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	auth := filepath.Join(t.TempDir(), "auth.json")
	_ = os.WriteFile(auth, []byte(`{"tokens":{"access_token":"`+fakeToken+`"}}`), 0o600)
	f := &CodexFetcher{Client: srv.Client(), URL: srv.URL, AuthFile: auth, Now: now}
	_, err := f.Fetch(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	assertNoSecret(t, err)
}

type fakeTokens struct {
	tok string
	err error
}

func (f fakeTokens) Token(context.Context) (string, error) { return f.tok, f.err }

func TestCursorFetch_Success(t *testing.T) {
	var gotAuth, gotMethod, gotProto, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotProto = r.Header.Get("Authorization"), r.Method, r.Header.Get("Connect-Protocol-Version")
		b := make([]byte, 16)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		_, _ = w.Write([]byte(`{"billingCycleEnd":"1791000000000","planUsage":{"totalPercentUsed":73.2},"extra":"ignored"}`))
	}))
	defer srv.Close()
	f := &CursorFetcher{Tokens: fakeTokens{tok: fakeToken}, Client: srv.Client(), URL: srv.URL, Now: now}
	s, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+fakeToken || gotMethod != http.MethodPost || gotProto != "1" || gotBody != "{}" {
		t.Errorf("request: %q %q %q %q", gotAuth, gotMethod, gotProto, gotBody)
	}
	if s.Monthly == nil || s.Monthly.Utilization != 73.2 || !s.Monthly.ResetsAt.Equal(time.UnixMilli(1791000000000).UTC()) {
		t.Errorf("monthly = %+v", s.Monthly)
	}
	if s.FiveHour != nil || s.Weekly != nil {
		t.Errorf("cursor must not report 5h/weekly: %+v", s)
	}
}

func TestCursorFetch_TokenErrorPropagates(t *testing.T) {
	f := &CursorFetcher{Tokens: fakeTokens{err: ErrNoCredentials}, Client: http.DefaultClient, URL: "http://unused.invalid", Now: now}
	if _, err := f.Fetch(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v", err)
	}
}

func makeVSCDB(t *testing.T, kv map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	for k, v := range kv {
		if _, err := db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestVSCDBTokenSource(t *testing.T) {
	ctx := context.Background()
	t.Run("present", func(t *testing.T) {
		p := makeVSCDB(t, map[string]string{"cursorAuth/accessToken": fakeToken})
		tok, err := VSCDBTokenSource{Path: p}.Token(ctx)
		if err != nil || tok != fakeToken {
			t.Fatalf("tok=%q err=%v", tok, err)
		}
	})
	t.Run("read only", func(t *testing.T) {
		p := makeVSCDB(t, map[string]string{"cursorAuth/accessToken": fakeToken})
		before, _ := os.ReadFile(p)
		if _, err := (VSCDBTokenSource{Path: p}).Token(ctx); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(p)
		if string(before) != string(after) {
			t.Error("state.vscdb modified by read")
		}
	})
	t.Run("key absent", func(t *testing.T) {
		p := makeVSCDB(t, map[string]string{"other": "x"})
		if _, err := (VSCDBTokenSource{Path: p}).Token(ctx); !errors.Is(err, ErrNoCredentials) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("file missing", func(t *testing.T) {
		if _, err := (VSCDBTokenSource{Path: filepath.Join(t.TempDir(), "nope")}).Token(ctx); !errors.Is(err, ErrNoCredentials) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFetchersSatisfyInterface(t *testing.T) {
	var _ Fetcher = NewClaudeFetcher()
	var _ Fetcher = NewCodexFetcher()
	var _ Fetcher = NewCursorFetcher()
	var _ KeychainReader = SecurityKeychain{}
	var _ CursorTokenSource = VSCDBTokenSource{}
}
