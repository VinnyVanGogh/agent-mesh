package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunErrors_SchemaExists(t *testing.T) {
	d := openTestDB(t)
	var count int
	err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='run_errors'`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected run_errors table to exist, got count=%d", count)
	}
}

func TestRunErrors_Columns(t *testing.T) {
	d := openTestDB(t)
	rows, err := d.Query(`PRAGMA table_info(run_errors)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ, notNull, dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name.String] = true
	}
	required := []string{"id", "run_id", "task_id", "turn", "exit_code", "stderr_tail", "duration_ms", "model", "adapter", "created_at"}
	for _, col := range required {
		if !got[col] {
			t.Errorf("expected column %q to exist in run_errors", col)
		}
	}
}

// fixedPathWM is a WorktreeManagerIface that always returns the same path.
// Used in tests where the harness must run on a pre-initialized git repo.
type fixedPathWM struct{ path string }

func (m *fixedPathWM) CreateContext(_ context.Context, _, _ string) (string, error) {
	return m.path, nil
}
func (m *fixedPathWM) PruneContext(_ context.Context, _ string) error           { return nil }
func (m *fixedPathWM) PruneWorktreeDirContext(_ context.Context, _ string) error { return nil }

func TestHarness_FailedAdapterWritesRunError(t *testing.T) {
	d := openTestDB(t)
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	_, err := d.Exec(`INSERT INTO tasks (id, name, repo_path, execution_stage) VALUES (?, ?, ?, ?)`,
		"task-test-err", "Test Task", repoDir, "todo")
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	h := &Harness{
		DB:          d,
		RepoRoot:    repoDir,
		WM:          &fixedPathWM{path: repoDir},
		Interceptor: NewInterceptor(d),
	}

	wantStderr := "unknown model: bad-model-name\ncheck your provider config"
	cfg := RunConfig{
		MaxTurns: 1,
		RunAdapter: func(ctx context.Context, cwd, provider string, rawArgs, extraEnv []string, stdout, stderr io.Writer) error {
			_, _ = stderr.Write([]byte(wantStderr))
			return errors.New("adapter exited with code 1")
		},
	}

	result, err := h.Run(context.Background(), "task-test-err", cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = result

	var count int
	if err := d.QueryRow(`SELECT count(*) FROM run_errors WHERE task_id = ?`, "task-test-err").Scan(&count); err != nil {
		t.Fatalf("query run_errors: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 run_errors row after adapter failure, got %d", count)
	}

	var stderrTail string
	if err := d.QueryRow(`SELECT stderr_tail FROM run_errors WHERE task_id = ?`, "task-test-err").Scan(&stderrTail); err != nil {
		t.Fatalf("query stderr_tail: %v", err)
	}
	if !strings.Contains(stderrTail, "bad-model-name") {
		t.Errorf("expected stderr_tail to contain 'bad-model-name', got %q", stderrTail)
	}
}

func TestLimitedWriter(t *testing.T) {
	w := &limitedWriter{maxSize: 10}
	_, _ = w.Write([]byte("hello world this is too long"))
	got := w.String()
	if len(got) > 10 {
		t.Errorf("expected at most 10 bytes, got %d: %q", len(got), got)
	}
	if !strings.HasSuffix("hello world this is too long", got) {
		t.Errorf("expected tail of input, got %q", got)
	}
}

func TestPlainAdapterError(t *testing.T) {
	err := errors.New("process exited with error")
	msg := plainAdapterError(err, 1, "unknown model: bad-model-name")
	if !strings.Contains(msg, "bad-model-name") {
		t.Errorf("expected error to mention model name, got %q", msg)
	}
}

func TestExitCodeFrom_Nil(t *testing.T) {
	if exitCodeFrom(nil) != 0 {
		t.Error("nil error should return 0")
	}
}

func TestExitCodeFrom_Generic(t *testing.T) {
	code := exitCodeFrom(errors.New("some error"))
	if code != 1 {
		t.Errorf("generic error should return 1, got %d", code)
	}
}
