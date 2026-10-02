package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

// Paperclip agents run `go test ./...` with PAPERCLIP_API_URL, PAPERCLIP_API_KEY
// and PAPERCLIP_COMPANY_ID exported for their own heartbeat. TestE2E_Task and
// TestCommands_InProcess run `task add` without --dry-run, so before STA-422 the
// CLI inherited those credentials and POSTed "Feature: E2E Sample Task" and
// "Feature: Test Task" to the live board on every test run (STA-203, 216, 219,
// 225, 229, 238, 247, 259, 264, 271, 301, 365, 420, ...).
//
// These tests stand a fake control plane in for the live one, export the same
// variables an agent has, and drive the exact calls those tests make through
// the same helpers. The suite must never create an issue, whatever the caller's
// environment holds.

type issueRecorder struct {
	mu     sync.Mutex
	writes []string
}

func (r *issueRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writes)
}

func (r *issueRecorder) list() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.writes, "\n  ")
}

// exportAgentPaperclipEnv mimics a Paperclip agent heartbeat environment, with
// the control plane replaced by a recorder so the test itself cannot leak.
func exportAgentPaperclipEnv(t *testing.T) *issueRecorder {
	t.Helper()
	rec := &issueRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			rec.mu.Lock()
			rec.writes = append(rec.writes, r.Method+" "+r.URL.Path)
			rec.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"leaked-issue","identifier":"STA-999","title":"leaked"}`))
		case strings.HasSuffix(r.URL.Path, "/agents"), strings.HasSuffix(r.URL.Path, "/projects"):
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"id":"agent-company","issuePrefix":"STA"}`))
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("PAPERCLIP_API_URL", srv.URL)
	t.Setenv("PAPERCLIP_API_KEY", "agent-heartbeat-key")
	t.Setenv("PAPERCLIP_COMPANY_ID", "agent-company")
	return rec
}

func TestE2E_TaskAddDoesNotReachPaperclip(t *testing.T) {
	rec := exportAgentPaperclipEnv(t)
	repoDir, homeDir := setupE2ETestRepo(t)

	// Same invocation as TestE2E_Task.
	out, err := execStaypoint(t, repoDir, homeDir, "", "task", "add", "E2E Sample Task")
	if err != nil {
		t.Fatalf("task add failed: %v, out: %s", err, out)
	}

	if n := rec.count(); n != 0 {
		t.Fatalf("e2e `task add \"E2E Sample Task\"` sent %d write(s) to the Paperclip API from the caller's environment; the suite must never create real issues:\n  %s", n, rec.list())
	}
}

func TestCommands_InProcessTaskAddDoesNotReachPaperclip(t *testing.T) {
	rec := exportAgentPaperclipEnv(t)
	repoDir, homeDir := setupE2ETestRepo(t)
	t.Setenv("HOME", homeDir)

	_ = os.MkdirAll(filepath.Join(homeDir, ".staypoint"), 0755)
	cfg = config.DefaultConfig()
	cfg.DataDir = filepath.Join(homeDir, ".staypoint")
	cfg.DBPath = filepath.Join(cfg.DataDir, "staypoint.db")

	// Cobra flag values outlive Execute; start from a fresh process's defaults
	// so an earlier --dry-run test cannot mask the leak.
	_ = taskAddCmd.Flags().Set("dry-run", "false")

	// Same invocation as TestCommands_InProcess/task.
	_ = runInProcess(t, repoDir, "task", "add", "Test Task", "--budget", "5.0", "--max-turns", "20")

	if n := rec.count(); n != 0 {
		t.Fatalf("in-process `task add \"Test Task\"` sent %d write(s) to the Paperclip API from the caller's environment; the suite must never create real issues:\n  %s", n, rec.list())
	}
}
