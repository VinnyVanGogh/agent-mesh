package bridge

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(f func()) string {
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestParseGitInspectOutput(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		expectedRepo     bool
		expectedBranch   string
		expectedDirty    int
		expectedUnpushed int
		expectErr        bool
	}{
		{
			name:             "clean repo",
			input:            "main\n0\n0\n",
			expectedRepo:     true,
			expectedBranch:   "main",
			expectedDirty:    0,
			expectedUnpushed: 0,
			expectErr:        false,
		},
		{
			name:             "dirty repo with spaces in wc -l output",
			input:            "feature/awesome\n       3\n       5\n",
			expectedRepo:     true,
			expectedBranch:   "feature/awesome",
			expectedDirty:    3,
			expectedUnpushed: 5,
			expectErr:        false,
		},
		{
			name:             "empty output",
			input:            "",
			expectedRepo:     false,
			expectedBranch:   "",
			expectedDirty:    0,
			expectedUnpushed: 0,
			expectErr:        true,
		},
		{
			name:             "invalid integer",
			input:            "main\nnotanumber\n0\n",
			expectedRepo:     false,
			expectedBranch:   "",
			expectedDirty:    0,
			expectedUnpushed: 0,
			expectErr:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := parseGitInspectOutput(tc.input)
			if (err != nil) != tc.expectErr {
				t.Fatalf("parseGitInspectOutput(%q) error = %v, expectErr = %v", tc.input, err, tc.expectErr)
			}
			if !tc.expectErr {
				if res.IsRepo != tc.expectedRepo || res.Branch != tc.expectedBranch || res.UncommittedCount != tc.expectedDirty || res.UnpushedCount != tc.expectedUnpushed {
					t.Errorf("got %+v, want isRepo=%v branch=%q dirty=%d unpushed=%d",
						res, tc.expectedRepo, tc.expectedBranch, tc.expectedDirty, tc.expectedUnpushed)
				}
			}
		})
	}
}

func TestCheckRemoteGitGuard_CleanRepo(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	origInteractive := IsInteractiveTerminalFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
		IsInteractiveTerminalFunc = origInteractive
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{
			IsRepo:           true,
			Branch:           "main",
			UncommittedCount: 0,
			UnpushedCount:    0,
		}, nil
	}
	IsInteractiveTerminalFunc = func() bool {
		return true
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/Users/remote/work/agent-mesh", "/Users/local/work/agent-mesh")
		if err != nil {
			t.Errorf("expected nil error on clean repo, got: %v", err)
		}
	})

	if out != "" {
		t.Errorf("expected clean repo to exit silently with zero output, got:\n%s", out)
	}
}

func TestCheckRemoteGitGuard_NotRepo(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{IsRepo: false}, nil
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/tmp/not-a-repo", "/tmp/local")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	if out != "" {
		t.Errorf("expected non-repo to exit silently with zero output, got:\n%s", out)
	}
}

func TestCheckRemoteGitGuard_Uncommitted_Push(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	origInteractive := IsInteractiveTerminalFunc
	origPrompt := PromptGitGuardActionFunc
	origPush := RunRemoteGitPushFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
		IsInteractiveTerminalFunc = origInteractive
		PromptGitGuardActionFunc = origPrompt
		RunRemoteGitPushFunc = origPush
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{
			IsRepo:           true,
			Branch:           "feature/cool",
			UncommittedCount: 3,
			UnpushedCount:    1,
		}, nil
	}
	IsInteractiveTerminalFunc = func() bool {
		return true
	}
	PromptGitGuardActionFunc = func() string {
		return "p"
	}

	pushCalled := false
	RunRemoteGitPushFunc = func(ctx context.Context, host, remoteDir string) error {
		pushCalled = true
		if host != "company-mbp" || remoteDir != "/Users/remote/work/agent-mesh" {
			t.Errorf("unexpected push args: host=%s, remoteDir=%s", host, remoteDir)
		}
		return nil
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/Users/remote/work/agent-mesh", "/Users/local/work/agent-mesh")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	if !pushCalled {
		t.Errorf("expected RunRemoteGitPushFunc to be called")
	}

	// Verify Tokyo Night alert box text
	if !strings.Contains(out, "⚠️  [bridge]") {
		t.Errorf("expected alert box header in output, got: %s", out)
	}
	if !strings.Contains(out, "Remote repo 'agent-mesh' on company-mbp has uncommitted/unpushed changes!") {
		t.Errorf("expected repo alert in output, got: %s", out)
	}
	if !strings.Contains(out, "Branch:") || !strings.Contains(out, "feature/cool") {
		t.Errorf("expected branch info in output, got: %s", out)
	}
	if !strings.Contains(out, "Uncommitted files:") || !strings.Contains(out, "3") {
		t.Errorf("expected uncommitted count in output, got: %s", out)
	}
	if !strings.Contains(out, "Unpushed commits:") || !strings.Contains(out, "1") {
		t.Errorf("expected unpushed count in output, got: %s", out)
	}
}

func TestCheckRemoteGitGuard_Unpushed_Sync(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	origInteractive := IsInteractiveTerminalFunc
	origPrompt := PromptGitGuardActionFunc
	origSync := RunGitGuardSyncFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
		IsInteractiveTerminalFunc = origInteractive
		PromptGitGuardActionFunc = origPrompt
		RunGitGuardSyncFunc = origSync
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{
			IsRepo:           true,
			Branch:           "main",
			UncommittedCount: 1,
			UnpushedCount:    2,
		}, nil
	}
	IsInteractiveTerminalFunc = func() bool {
		return true
	}
	PromptGitGuardActionFunc = func() string {
		return "sync"
	}

	syncCalled := false
	RunGitGuardSyncFunc = func(ctx context.Context, host, remoteDir, localDir string) error {
		syncCalled = true
		if host != "company-mbp" || remoteDir != "/Users/remote/work/agent-mesh" || localDir != "/Users/local/work/agent-mesh" {
			t.Errorf("unexpected sync args: host=%s, remoteDir=%s, localDir=%s", host, remoteDir, localDir)
		}
		return nil
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/Users/remote/work/agent-mesh", "/Users/local/work/agent-mesh")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	if !syncCalled {
		t.Errorf("expected RunGitGuardSyncFunc to be called")
	}

	if !strings.Contains(out, "Successfully synced remote changes") {
		t.Errorf("expected sync success message, got: %s", out)
	}
}

func TestCheckRemoteGitGuard_Ignore(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	origInteractive := IsInteractiveTerminalFunc
	origPrompt := PromptGitGuardActionFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
		IsInteractiveTerminalFunc = origInteractive
		PromptGitGuardActionFunc = origPrompt
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{
			IsRepo:           true,
			Branch:           "main",
			UncommittedCount: 2,
			UnpushedCount:    0,
		}, nil
	}
	IsInteractiveTerminalFunc = func() bool {
		return true
	}
	PromptGitGuardActionFunc = func() string {
		return "i"
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/Users/remote/work/agent-mesh", "/Users/local/work/agent-mesh")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	if !strings.Contains(out, "Remote changes left intact (ignored)") {
		t.Errorf("expected ignore notice in output, got: %s", out)
	}
}

func TestCheckRemoteGitGuard_NonInteractive(t *testing.T) {
	origInspect := InspectRemoteGitStatusFunc
	origInteractive := IsInteractiveTerminalFunc
	defer func() {
		InspectRemoteGitStatusFunc = origInspect
		IsInteractiveTerminalFunc = origInteractive
	}()

	InspectRemoteGitStatusFunc = func(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
		return RemoteGitStatus{
			IsRepo:           true,
			Branch:           "main",
			UncommittedCount: 1,
			UnpushedCount:    1,
		}, nil
	}
	IsInteractiveTerminalFunc = func() bool {
		return false
	}

	out := captureStdout(func() {
		err := CheckRemoteGitGuard(context.Background(), "company-mbp", "/Users/remote/work/agent-mesh", "/Users/local/work/agent-mesh")
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	// Must print alert box and notice that it is non-interactive
	if !strings.Contains(out, "⚠️  [bridge]") {
		t.Errorf("expected alert box in output, got: %s", out)
	}
	if !strings.Contains(out, "Non-interactive session; remote changes left intact") {
		t.Errorf("expected non-interactive notice in output, got: %s", out)
	}
}
