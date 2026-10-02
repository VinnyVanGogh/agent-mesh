package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/config"
	meshContext "github.com/VinnyVanGogh/staypoint/internal/context"
	"github.com/VinnyVanGogh/staypoint/internal/db"
)

func runInProcess(t *testing.T, dir string, args ...string) string {
	t.Helper()
	oldWd, _ := os.Getwd()
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	outChan := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outChan <- buf.String()
	}()

	restoreEnv := isolatePaperclipInProcess()
	rootCmd.SetArgs(args)
	_ = rootCmd.Execute()
	restoreEnv()

	_ = w.Close()
	os.Stdout = oldStdout
	return <-outChan
}

func TestCommands_InProcess(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", oldHome)

	// Ensure .staypoint data dir exists
	_ = os.MkdirAll(filepath.Join(homeDir, ".staypoint"), 0755)
	cfg = config.DefaultConfig()
	cfg.DataDir = filepath.Join(homeDir, ".staypoint")
	cfg.DBPath = filepath.Join(cfg.DataDir, "staypoint.db")

	t.Run("version", func(t *testing.T) {
		out := runInProcess(t, repoDir, "version")
		if out == "" {
			t.Errorf("expected version output, got empty")
		}
	})

	t.Run("init_shell", func(t *testing.T) {
		out := runInProcess(t, repoDir, "init", "--shell")
		if out == "" {
			t.Errorf("expected init shell output, got empty")
		}
	})

	t.Run("statusline", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "statusline")
	})

	t.Run("route", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "route", "--eval", "--no-ssh")
		_ = runInProcess(t, repoDir, "route", "--json", "--no-ssh")
		_ = runInProcess(t, repoDir, "route", "--no-ssh")
	})

	t.Run("where", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "where")
		_ = runInProcess(t, repoDir, "where", "--json")
	})

	t.Run("task", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "task", "add", "Test Task", "--budget", "5.0", "--max-turns", "20")
		_ = runInProcess(t, repoDir, "task", "list")
		_ = runInProcess(t, repoDir, "task", "list", "--all")
		if store, err := db.Open(cfg.DBPath); err == nil {
			tasks, _ := meshContext.ListTasks(store.DB(), true)
			if len(tasks) > 0 {
				_ = runInProcess(t, repoDir, "task", "budget", tasks[0].ID, "--usd", "10.0", "--turns", "30")
				_ = meshContext.AddWorkProduct(store.DB(), tasks[0].ID, "pull_request", "https://github.com/org/repo/pull/1")
				_ = runInProcess(t, repoDir, "task", "done", tasks[0].ID)
			}
			store.Close()
		}
	})

	t.Run("wire", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "wire", "post", "Test wire message", "-c", "general")
		_ = runInProcess(t, repoDir, "wire", "list")
		_ = runInProcess(t, repoDir, "wire", "list", "-c", "general")
		_ = runInProcess(t, repoDir, "wire", "prune")
	})

	t.Run("breaker", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "breaker", "list")
		_ = runInProcess(t, repoDir, "breaker", "list", "--all")
		_ = runInProcess(t, repoDir, "breaker", "reset", "test-session")
	})

	t.Run("checkpoint_and_undo", func(t *testing.T) {
		testFile := filepath.Join(repoDir, "test.txt")
		_ = os.WriteFile(testFile, []byte("hello checkpoint"), 0644)
		_ = runInProcess(t, repoDir, "checkpoint", "test checkpoint")
		_ = runInProcess(t, repoDir, "checkpoints")
		_ = runInProcess(t, repoDir, "undo", "--dry-run")
	})

	t.Run("condense", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "condense", "-f", "generic", "--", "echo", "hello compiler error")
	})

	t.Run("status", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "status")
	})

	t.Run("doctor", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "doctor")
	})

	t.Run("report", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "report")
	})

	t.Run("checkpoint_migrate", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "migrate-legacy-refs")
	})

	t.Run("hook", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "hook", "prompt")
	})

	t.Run("handoff", func(t *testing.T) {
		_ = runInProcess(t, repoDir, "handoff", "list")
		_ = runInProcess(t, repoDir, "handoff", "list", "--all")
		_ = runInProcess(t, repoDir, "handoff", "search", "test")
		_ = runInProcess(t, repoDir, "handoff", "branches")
	})
}

func TestIsHeadlessStream(t *testing.T) {
	cases := []struct {
		args     []string
		expected bool
	}{
		{args: []string{"--print"}, expected: true},
		{args: []string{"-p", "fix bug"}, expected: true},
		{args: []string{"--output-format", "stream-json"}, expected: true},
		{args: []string{"--output-format=stream-json"}, expected: true},
		{args: []string{"--output-format", "json"}, expected: true},
		{args: []string{"--output-format=json"}, expected: true},
		{args: []string{"something", "stream-json"}, expected: false},
		{args: []string{"staypoint", "fix the stream-json parser"}, expected: false},
		{args: []string{"--model", "claude-sonnet-4-6"}, expected: false},
		{args: []string{}, expected: false},
	}

	for _, tc := range cases {
		got := isHeadlessStream(tc.args)
		if got != tc.expected {
			t.Errorf("isHeadlessStream(%v) = %v; want %v", tc.args, got, tc.expected)
		}
	}
}

func TestExtractPassthroughArgs(t *testing.T) {
	raw := []string{"--claude", "-p", "fix bug", "--output-format", "stream-json", "--force"}
	expected := []string{"-p", "fix bug", "--output-format", "stream-json"}

	got := extractPassthroughArgs(raw)
	if len(got) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(got), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("arg[%d]: expected %s, got %s", i, expected[i], got[i])
		}
	}
}
