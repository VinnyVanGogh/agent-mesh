package bridge

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGetRepoInfo(t *testing.T) {
	// 1. Current working directory should be inside git repo
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}

	info := GetRepoInfo(cwd)
	if !info.IsRepo {
		t.Fatalf("expected cwd %s to be recognized as git repo", cwd)
	}
	if info.RepoRoot == "" {
		t.Errorf("expected non-empty RepoRoot")
	}

	// 2. Non-git temp directory
	tmpDir := t.TempDir()
	nonRepo := GetRepoInfo(tmpDir)
	if nonRepo.IsRepo {
		t.Errorf("expected temp dir %s to NOT be a git repo", tmpDir)
	}
}

func TestFormatRsyncRemotePath(t *testing.T) {
	tests := []struct {
		host      string
		remoteDir string
		expected  string
	}{
		{
			host:      "mansol-mbp",
			remoteDir: "~/Documents/dev/repo",
			expected:  "mansol-mbp:Documents/dev/repo",
		},
		{
			host:      "mansol-mbp",
			remoteDir: "/Users/mansolvv/Documents/dev/repo",
			expected:  "mansol-mbp:/Users/mansolvv/Documents/dev/repo",
		},
		{
			host:      "mansol-mbp",
			remoteDir: "~",
			expected:  "mansol-mbp:",
		},
	}

	for _, tc := range tests {
		got := FormatRsyncRemotePath(tc.host, tc.remoteDir)
		if got != tc.expected {
			t.Errorf("FormatRsyncRemotePath(%q, %q) = %q, want %q", tc.host, tc.remoteDir, got, tc.expected)
		}
	}
}

func TestLaunchRemoteDirNotExistUserDeclines(t *testing.T) {
	origProbe := ProbeSSHFunc
	origRemoteExists := RemoteDirExistsFunc
	origPrompt := PromptYesNoFunc
	defer func() {
		ProbeSSHFunc = origProbe
		RemoteDirExistsFunc = origRemoteExists
		PromptYesNoFunc = origPrompt
	}()

	// 1. Mock reachable SSH probe
	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{
			Host:      host,
			Reachable: true,
			Latency:   50 * time.Millisecond,
		}
	}

	// 2. Mock remote dir does NOT exist
	RemoteDirExistsFunc = func(ctx context.Context, host, remoteDir string) bool {
		return false
	}

	// 3. Mock user answers 'no' (declines clone/sync)
	promptCalled := false
	PromptYesNoFunc = func(prompt string, defaultVal bool) bool {
		promptCalled = true
		return false
	}

	tmpDir := t.TempDir()
	opts := LaunchOptions{
		Host:       "mock-host",
		TargetDir:  tmpDir,
		Args:       []string{"echo", "local-fallback-ok"},
		ForceLocal: false,
	}

	ctx := context.Background()
	err := Launch(ctx, opts)
	if err != nil {
		t.Fatalf("Launch should fall back cleanly to local execution, got error: %v", err)
	}

	if !promptCalled {
		t.Errorf("expected PromptYesNo to be called when remote directory does not exist")
	}
}

func TestLaunchRemoteDirNotExistUserAcceptsSyncFailFallback(t *testing.T) {
	origProbe := ProbeSSHFunc
	origRemoteExists := RemoteDirExistsFunc
	origPrompt := PromptYesNoFunc
	defer func() {
		ProbeSSHFunc = origProbe
		RemoteDirExistsFunc = origRemoteExists
		PromptYesNoFunc = origPrompt
	}()

	ProbeSSHFunc = func(ctx context.Context, host string, timeout time.Duration) ProbeResult {
		return ProbeResult{
			Host:      host,
			Reachable: true,
			Latency:   50 * time.Millisecond,
		}
	}

	RemoteDirExistsFunc = func(ctx context.Context, host, remoteDir string) bool {
		return false
	}

	// Mock user answers 'yes' (wants sync)
	promptCalled := false
	PromptYesNoFunc = func(prompt string, defaultVal bool) bool {
		promptCalled = true
		return true
	}

	tmpDir := t.TempDir()
	opts := LaunchOptions{
		Host:       "mock-host-unreachable-for-sync",
		TargetDir:  tmpDir,
		Args:       []string{"echo", "local-fallback-after-sync-failure"},
		ForceLocal: false,
	}

	ctx := context.Background()
	// Even if sync to non-existent mock-host fails, Launch must gracefully fall back to local execution!
	err := Launch(ctx, opts)
	if err != nil {
		t.Fatalf("Launch should gracefully fall back to local execution on sync failure, got: %v", err)
	}

	if !promptCalled {
		t.Errorf("expected PromptYesNo to be called")
	}
}
