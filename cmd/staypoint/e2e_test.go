package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testBinaryPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "staypoint-e2e-bin-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	// Tests point HOME at temp dirs. A real agent CLI run under a fake HOME
	// tries to store credentials, finds no keychain, and macOS pops a blocking
	// "Keychain Not Found" modal on the user's screen. Shadow them with no-op
	// stubs so no test can ever launch the real ones.
	stubDir := filepath.Join(tmpDir, "stub-bin")
	if err := os.MkdirAll(stubDir, 0755); err != nil {
		panic(err)
	}
	for _, name := range []string{"claude", "agy", "gemini", "codex"} {
		if err := os.WriteFile(filepath.Join(stubDir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			panic(err)
		}
	}
	os.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	testBinaryPath = filepath.Join(tmpDir, "staypoint")
	buildCmd := exec.Command("go", "build", "-o", testBinaryPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		panic("failed to build staypoint binary: " + string(out))
	}

	code := m.Run()
	os.Exit(code)
}

func setupE2ETestRepo(t *testing.T) (repoDir, homeDir string) {
	t.Helper()
	homeDir = t.TempDir()
	repoDir = t.TempDir()

	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(out))
		}
	}

	gitRun("init")
	gitRun("config", "user.email", "e2e@staypoint.dev")
	gitRun("config", "user.name", "E2E Agent")

	initialFile := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(initialFile, []byte("# E2E Test Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "README.md")
	gitRun("commit", "-m", "initial commit")

	return repoDir, homeDir
}

// Paperclip agents run the suite with their heartbeat PAPERCLIP_* credentials
// exported, and the client falls back to the live local control plane when
// PAPERCLIP_API_URL is unset. Commands the suite drives must reach neither, so
// the helpers below swap in an address that refuses connections. Set
// STAYPOINT_TEST_LIVE_PAPERCLIP=1 to deliberately run against the caller's board.
const (
	paperclipLiveOptInEnv = "STAYPOINT_TEST_LIVE_PAPERCLIP"
	unreachablePaperclip  = "http://127.0.0.1:1"
)

var isolatedPaperclipEnv = map[string]string{
	"PAPERCLIP_API_URL":    unreachablePaperclip,
	"PAPERCLIP_API_KEY":    "",
	"PAPERCLIP_COMPANY_ID": "",
	"PAPERCLIP_PROJECT_ID": "",
}

func paperclipLiveOptIn() bool {
	return os.Getenv(paperclipLiveOptInEnv) == "1"
}

// isolatedEnviron returns env with every PAPERCLIP_* variable dropped and the
// API URL pointed at an unreachable address, unless the caller opted in.
func isolatedEnviron(env []string) []string {
	if paperclipLiveOptIn() {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PAPERCLIP_") {
			out = append(out, kv)
		}
	}
	return append(out, "PAPERCLIP_API_URL="+unreachablePaperclip)
}

// isolatePaperclipInProcess applies the same isolation to this process for an
// in-process command run and returns a func that restores the prior values.
func isolatePaperclipInProcess() (restore func()) {
	if paperclipLiveOptIn() {
		return func() {}
	}
	saved := make(map[string]*string, len(isolatedPaperclipEnv))
	for k, v := range isolatedPaperclipEnv {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = &old
		} else {
			saved[k] = nil
		}
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
	return func() {
		for k, old := range saved {
			if old == nil {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, *old)
			}
		}
	}
}

func execStaypoint(t *testing.T, dir, home string, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(testBinaryPath, args...)
	cmd.Dir = dir
	cmd.Env = append(isolatedEnviron(os.Environ()), "HOME="+home)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestE2E_Version(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)
	out, err := execStaypoint(t, repoDir, homeDir, "", "version")
	if err != nil {
		t.Fatalf("staypoint version failed: %v, out: %s", err, out)
	}
	if !strings.Contains(out, "staypoint version") {
		t.Errorf("expected output to contain 'staypoint version', got: %s", out)
	}
}

func TestE2E_Checkpoint(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)
	testFile := filepath.Join(repoDir, "README.md")
	_ = os.WriteFile(testFile, []byte("# E2E Test Repo\nModified for checkpoint test\n"), 0644)

	out, err := execStaypoint(t, repoDir, homeDir, "", "checkpoint", "e2e snapshot")
	if err != nil {
		t.Fatalf("staypoint checkpoint failed: %v, out: %s", err, out)
	}
	if !strings.Contains(out, "Micro-checkpoint created in") {
		t.Errorf("expected output to contain 'Micro-checkpoint created in', got: %s", out)
	}
}

func TestE2E_UndoDryRunAndCleanIgnored(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)

	// Create and commit .gitignore
	gitignore := filepath.Join(repoDir, ".gitignore")
	_ = os.WriteFile(gitignore, []byte("*.ignored\n"), 0644)
	gitCmd := exec.Command("git", "add", ".gitignore")
	gitCmd.Dir = repoDir
	_ = gitCmd.Run()
	gitCmd = exec.Command("git", "commit", "-m", "add gitignore")
	gitCmd.Dir = repoDir
	_ = gitCmd.Run()

	// Create base checkpoint
	cpOut, err := execStaypoint(t, repoDir, homeDir, "", "checkpoint", "baseline")
	if err != nil {
		t.Fatalf("checkpoint failed: %v, out: %s", err, cpOut)
	}

	// Create dirty state: modified tracked file, new untracked file, new ignored file
	readme := filepath.Join(repoDir, "README.md")
	_ = os.WriteFile(readme, []byte("# Modified README\n"), 0644)
	untracked := filepath.Join(repoDir, "untracked.txt")
	_ = os.WriteFile(untracked, []byte("untracked content"), 0644)
	ignored := filepath.Join(repoDir, "temp.ignored")
	_ = os.WriteFile(ignored, []byte("ignored content"), 0644)

	// Run undo --dry-run
	dryOut, err := execStaypoint(t, repoDir, homeDir, "", "undo", "--dry-run", "--clean-ignored")
	if err != nil {
		t.Fatalf("undo --dry-run failed: %v, out: %s", err, dryOut)
	}
	if !strings.Contains(dryOut, "[Dry Run] Preview of undo to") {
		t.Errorf("expected dry run header, got: %s", dryOut)
	}
	if !strings.Contains(dryOut, "README.md") {
		t.Errorf("expected README.md in reverted list, got: %s", dryOut)
	}
	if !strings.Contains(dryOut, "untracked.txt") {
		t.Errorf("expected untracked.txt in removed list, got: %s", dryOut)
	}
	if !strings.Contains(dryOut, "temp.ignored") {
		t.Errorf("expected temp.ignored in removed ignored list, got: %s", dryOut)
	}

	// Verify all files still exist after dry run
	if _, err := os.Stat(untracked); os.IsNotExist(err) {
		t.Errorf("untracked file should remain after dry run")
	}
	if _, err := os.Stat(ignored); os.IsNotExist(err) {
		t.Errorf("ignored file should remain after dry run")
	}

	// Run actual undo with --clean-ignored
	undoOut, err := execStaypoint(t, repoDir, homeDir, "", "undo", "--clean-ignored")
	if err != nil {
		t.Fatalf("undo failed: %v, out: %s", err, undoOut)
	}
	if !strings.Contains(undoOut, "Working tree rolled back to checkpoint") {
		t.Errorf("expected success message, got: %s", undoOut)
	}

	// Verify untracked and ignored files are now removed
	if _, err := os.Stat(untracked); !os.IsNotExist(err) {
		t.Errorf("untracked file should be deleted after undo")
	}
	if _, err := os.Stat(ignored); !os.IsNotExist(err) {
		t.Errorf("ignored file should be deleted after undo --clean-ignored")
	}
}

func TestE2E_Condense(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)
	inputLog := "main.go:10: undefined: Foo\nmain.go:12: cannot use bar as type string\n"
	out, err := execStaypoint(t, repoDir, homeDir, inputLog, "condense")
	if err != nil {
		t.Fatalf("condense failed: %v, out: %s", err, out)
	}
	if len(out) == 0 {
		t.Errorf("expected non-empty output from condense")
	}
}

func TestE2E_Wire(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)

	// Post message
	postOut, err := execStaypoint(t, repoDir, homeDir, "", "wire", "post", "E2E broadcast test message")
	if err != nil {
		t.Fatalf("wire post failed: %v, out: %s", err, postOut)
	}
	if !strings.Contains(postOut, "Broadcast posted") {
		t.Errorf("expected 'Broadcast posted', got: %s", postOut)
	}

	// List messages
	listOut, err := execStaypoint(t, repoDir, homeDir, "", "wire", "list")
	if err != nil {
		t.Fatalf("wire list failed: %v, out: %s", err, listOut)
	}
	if !strings.Contains(listOut, "[Staypoint Wire Broadcasts]") {
		t.Errorf("expected '[Staypoint Wire Broadcasts]', got: %s", listOut)
	}
	if !strings.Contains(listOut, "E2E broadcast test message") {
		t.Errorf("expected message in list output, got: %s", listOut)
	}
}

func TestE2E_Task(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)

	// Add task
	addOut, err := execStaypoint(t, repoDir, homeDir, "", "task", "add", "E2E Sample Task")
	if err != nil {
		t.Fatalf("task add failed: %v, out: %s", err, addOut)
	}
	if !strings.Contains(addOut, "Task created:") || !strings.Contains(addOut, "E2E Sample Task") {
		t.Errorf("expected 'Task created:' and 'E2E Sample Task' in output, got: %s", addOut)
	}

	// List tasks
	listOut, err := execStaypoint(t, repoDir, homeDir, "", "task", "list")
	if err != nil {
		t.Fatalf("task list failed: %v, out: %s", err, listOut)
	}
	if !strings.Contains(listOut, "[Staypoint Tasks]") {
		t.Errorf("expected '[Staypoint Tasks]', got: %s", listOut)
	}
	if !strings.Contains(listOut, "E2E Sample Task") {
		t.Errorf("expected task name in list output, got: %s", listOut)
	}
}

func TestE2E_TaskCreate(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)

	// Create dynamic task via CLI with dry-run
	createOut, err := execStaypoint(t, repoDir, homeDir, "", "task", "create", "Fix token telemetry rounding bug", "--dry-run")
	if err != nil {
		t.Fatalf("task create failed: %v, out: %s", err, createOut)
	}
	if !strings.Contains(createOut, "Inference Telemetry & Cost Engine") {
		t.Errorf("expected telemetry output, got: %s", createOut)
	}
	if !strings.Contains(createOut, "Fleet Status & Pacing Engine") {
		t.Errorf("expected pacing status output, got: %s", createOut)
	}

	// Verify local staypoint.db indexed the task
	listOut, err := execStaypoint(t, repoDir, homeDir, "", "task", "list")
	if err != nil {
		t.Fatalf("task list failed: %v, out: %s", err, listOut)
	}
	if !strings.Contains(listOut, "rounding bug") {
		t.Errorf("expected task in list output, got: %s", listOut)
	}
}

func TestE2E_HandoffList(t *testing.T) {
	repoDir, homeDir := setupE2ETestRepo(t)

	out, err := execStaypoint(t, repoDir, homeDir, "", "handoff", "list")
	if err != nil {
		t.Fatalf("handoff list failed: %v, out: %s", err, out)
	}
	if !strings.Contains(out, "Saved Handoffs in") && !strings.Contains(out, "No saved handoffs found.") {
		t.Errorf("expected handoff list output, got: %s", out)
	}
}

func TestRootCmd_SetArgs(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Staypoint") {
		t.Errorf("expected help output to mention Staypoint, got: %s", buf.String())
	}
}
