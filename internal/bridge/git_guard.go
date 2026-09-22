package bridge

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Tokyo Night palette constants for terminal UI
const (
	tnYellow = "\033[38;2;224;175;104m"
	tnPurple = "\033[38;2;187;154;247m"
	tnRed    = "\033[38;2;247;118;142m"
	tnCyan   = "\033[38;2;125;207;255m"
	tnGreen  = "\033[38;2;158;206;106m"
	tnBold   = "\033[1m"
	tnReset  = "\033[0m"
)

// RemoteGitStatus encapsulates git status metrics of a remote directory.
type RemoteGitStatus struct {
	IsRepo           bool
	Branch           string
	UncommittedCount int
	UnpushedCount    int
}

var (
	// InspectRemoteGitStatusFunc allows mocking git status discovery in tests.
	InspectRemoteGitStatusFunc = defaultInspectRemoteGitStatus

	// IsInteractiveTerminalFunc allows mocking interactive tty check in tests.
	IsInteractiveTerminalFunc = defaultIsInteractiveTerminal

	// PromptGitGuardActionFunc allows mocking action selection in tests.
	PromptGitGuardActionFunc = defaultPromptGitGuardAction

	// RunRemoteGitPushFunc allows mocking remote git push in tests.
	RunRemoteGitPushFunc = defaultRunRemoteGitPush

	// RunGitGuardSyncFunc allows mocking remote to local rsync in tests.
	RunGitGuardSyncFunc = defaultRunGitGuardSync
)

func defaultIsInteractiveTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func defaultPromptGitGuardAction() string {
	fmt.Printf("%sAction? [p]ush to origin / [s]ync to local / [i]gnore:%s ", tnCyan+tnBold, tnReset)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Println()
		return "i"
	}
	return strings.ToLower(strings.TrimSpace(line))
}

func defaultRunRemoteGitPush(ctx context.Context, host, remoteDir string) error {
	remoteCd := ShellPathForDir(remoteDir)
	pushScript := fmt.Sprintf("cd %s && git push", remoteCd)
	cmd := exec.CommandContext(ctx, "ssh", host, pushScript)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func defaultRunGitGuardSync(ctx context.Context, host, remoteDir, localDir string) error {
	if localDir == "" {
		return fmt.Errorf("local directory not specified for sync")
	}

	cleanLocal := filepath.Clean(localDir) + "/"
	remoteSource := FormatRsyncRemotePath(host, remoteDir) + "/"

	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	cmd := exec.CommandContext(ctx, "rsync", "-az", "--progress", "--exclude=.DS_Store", remoteSource, cleanLocal)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func parseGitInspectOutput(out string) (RemoteGitStatus, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return RemoteGitStatus{IsRepo: false}, fmt.Errorf("insufficient output: %q", out)
	}

	branch := strings.TrimSpace(lines[0])
	uncommittedStr := strings.TrimSpace(lines[1])
	unpushedStr := strings.TrimSpace(lines[2])

	uncommitted, err := strconv.Atoi(uncommittedStr)
	if err != nil {
		return RemoteGitStatus{IsRepo: false}, fmt.Errorf("invalid uncommitted count %q: %w", uncommittedStr, err)
	}

	unpushed, err := strconv.Atoi(unpushedStr)
	if err != nil {
		return RemoteGitStatus{IsRepo: false}, fmt.Errorf("invalid unpushed count %q: %w", unpushedStr, err)
	}

	return RemoteGitStatus{
		IsRepo:           true,
		Branch:           branch,
		UncommittedCount: uncommitted,
		UnpushedCount:    unpushed,
	}, nil
}

func defaultInspectRemoteGitStatus(ctx context.Context, host, remoteDir string) (RemoteGitStatus, error) {
	if host == "" || remoteDir == "" {
		return RemoteGitStatus{}, nil
	}

	remoteCdPath := ShellPathForDir(remoteDir)
	script := fmt.Sprintf(`cd %s 2>/dev/null || exit 10
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    exit 11
fi
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "HEAD")
uncommitted=$(git status --porcelain 2>/dev/null | wc -l)
unpushed=0
if git rev-list @{u}..HEAD >/dev/null 2>&1; then
    unpushed=$(git rev-list @{u}..HEAD 2>/dev/null | wc -l)
elif [ -n "$branch" ] && git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
    unpushed=$(git rev-list "origin/$branch..HEAD" 2>/dev/null | wc -l)
elif git rev-parse --verify origin/main >/dev/null 2>&1; then
    unpushed=$(git rev-list origin/main..HEAD 2>/dev/null | wc -l)
elif git rev-parse --verify origin/master >/dev/null 2>&1; then
    unpushed=$(git rev-list origin/master..HEAD 2>/dev/null | wc -l)
fi
echo "$branch"
echo "$uncommitted"
echo "$unpushed"
`, remoteCdPath)

	cmd := exec.CommandContext(ctx, "ssh", host, script)
	out, err := cmd.Output()
	if err != nil {
		return RemoteGitStatus{IsRepo: false}, err
	}

	return parseGitInspectOutput(string(out))
}

func printTokyoNightAlert(repoName, host, branch string, uncommitted, unpushed int) {
	fmt.Printf("\n%s⚠️  [bridge]%s %sRemote repo '%s' on %s has uncommitted/unpushed changes!%s\n",
		tnYellow+tnBold, tnReset, tnBold, repoName, host, tnReset)
	fmt.Printf("   Branch: %s%s%s · Uncommitted files: %s%d%s · Unpushed commits: %s%d%s\n\n",
		tnPurple, branch, tnReset,
		tnRed, uncommitted, tnReset,
		tnYellow, unpushed, tnReset)
}

// CheckRemoteGitGuard inspects the remote repository after a session ends.
// If changes or unpushed commits exist, it alerts the user with a Tokyo Night box
// and prompts for action (push to origin, sync to local via rsync, or ignore).
// If the remote repo is clean, it exits silently with zero friction.
func CheckRemoteGitGuard(ctx context.Context, host, remoteDir, localDir string) error {
	if host == "" || remoteDir == "" {
		return nil
	}

	status, err := InspectRemoteGitStatusFunc(ctx, host, remoteDir)
	if err != nil || !status.IsRepo {
		// Non-repo or unreachable: exit cleanly with zero friction
		return nil
	}

	if status.UncommittedCount == 0 && status.UnpushedCount == 0 {
		// Clean repository: zero friction
		return nil
	}

	repoName := filepath.Base(filepath.Clean(remoteDir))
	printTokyoNightAlert(repoName, host, status.Branch, status.UncommittedCount, status.UnpushedCount)

	if !IsInteractiveTerminalFunc() {
		fmt.Printf("\033[1;33m[bridge]\033[0m Notice: Non-interactive session; remote changes left intact.\n")
		return nil
	}

	action := PromptGitGuardActionFunc()
	switch action {
	case "p", "push":
		fmt.Printf("\033[1;36m[bridge]\033[0m Running 'git push' on %s...\n", host)
		if err := RunRemoteGitPushFunc(ctx, host, remoteDir); err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ [bridge]\033[0m Push failed: %v\n", err)
			return err
		}
		fmt.Printf("\033[1;32m✔ [bridge]\033[0m Successfully pushed changes to origin.\n")
		return nil

	case "s", "sync":
		fmt.Printf("\033[1;36m[bridge]\033[0m Syncing remote changes from %s to %s via rsync...\n", host, localDir)
		if err := RunGitGuardSyncFunc(ctx, host, remoteDir, localDir); err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ [bridge]\033[0m Sync failed: %v\n", err)
			return err
		}
		fmt.Printf("\033[1;32m✔ [bridge]\033[0m Successfully synced remote changes to %s.\n", localDir)
		return nil

	case "i", "ignore", "":
		fallthrough
	default:
		fmt.Printf("\033[1;33m[bridge]\033[0m Notice: Remote changes left intact (ignored).\n")
		return nil
	}
}
